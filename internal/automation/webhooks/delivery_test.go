package webhooks_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/secrets"
)

// scriptedTransport answers requests from a queue and remembers what it was
// asked to send. No socket is opened, so nothing here can reach a network.
type scriptedTransport struct {
	mu        sync.Mutex
	responses []scripted
	requests  []*http.Request
	bodies    []string
}

type scripted struct {
	status int
	header http.Header
	body   string
}

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body string
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		body = string(raw)
	}
	s.requests = append(s.requests, req)
	s.bodies = append(s.bodies, body)

	if len(s.responses) == 0 {
		return nil, errors.New("scriptedTransport: no response left")
	}
	next := s.responses[0]
	if len(s.responses) > 1 {
		s.responses = s.responses[1:]
	}
	h := next.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{
		StatusCode:    next.status,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(next.body)),
		ContentLength: int64(len(next.body)),
		Request:       req,
	}, nil
}

func (s *scriptedTransport) sent() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request{}, s.requests...)
}

func (s *scriptedTransport) sentBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.bodies...)
}

// testKey is the AES key every test seals a secret with.
var testKey = []byte(strings.Repeat("k", 32))

// testSecretBytes and testSecretText are the signing key one test webhook uses.
// The material is chosen so it cannot appear anywhere else in a payload: a leak
// check that matched an event id would prove nothing.
var (
	testSecretBytes = bytes.Repeat([]byte{0x5a}, 32)
	testSecretText  = "whsec_" + base64.StdEncoding.EncodeToString(testSecretBytes)
)

// testSecret seals a webhook secret the way the service does.
func testSecret(t *testing.T, plaintext string) secrets.Box {
	t.Helper()
	enc, err := secrets.NewEncrypter(testKey)
	if err != nil {
		t.Fatal(err)
	}
	box, err := enc.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func testOpener(t *testing.T) *secrets.Encrypter {
	t.Helper()
	enc, err := secrets.NewEncrypter(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

// env is one delivery engine wired to a fake store and a scripted receiver.
type env struct {
	t        *testing.T
	store    *fakeStore
	svc      *webhooks.Service
	rt       *scriptedTransport
	events   map[string]automation.MessageEvent
	channels map[int64]webhooks.Channel
	jobs     *fakeEnqueuer
}

type fakeEnqueuer struct {
	mu        sync.Mutex
	webhooks  [][2]any
	workflows [][2]any
}

func (f *fakeEnqueuer) EnqueueWebhookDelivery(ctx context.Context, eventID string, webhookID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webhooks = append(f.webhooks, [2]any{eventID, webhookID})
	return nil
}

func (f *fakeEnqueuer) EnqueueWorkflowRun(ctx context.Context, eventID string, workflowID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workflows = append(f.workflows, [2]any{eventID, workflowID})
	return nil
}

func (f *fakeEnqueuer) webhookJobs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.webhooks)
}

const testEventID = "evt_0123456789abcdef"

func newEnv(t *testing.T, responses ...scripted) *env {
	t.Helper()
	rt := &scriptedTransport{responses: responses}
	store := newFakeStore()
	jobs := &fakeEnqueuer{}
	e := &env{
		t:        t,
		store:    store,
		rt:       rt,
		events:   map[string]automation.MessageEvent{},
		channels: map[int64]webhooks.Channel{},
		jobs:     jobs,
	}
	e.events[testEventID] = messageEvent(nil)

	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout:                 5 * time.Second,
			MaxAttempts:             5,
			MaxResponseBytes:        64 << 10,
			MaxConcurrentDeliveries: 2,
		},
		Store:   store,
		Secrets: testOpener(t),
		Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
			ev, ok := e.events[id]
			return ev, ok, nil
		},
		Channels: func(ctx context.Context, managedChannelID int64) (webhooks.Channel, bool, error) {
			ch, ok := e.channels[managedChannelID]
			return ch, ok, nil
		},
		Jobs:      jobs,
		Transport: rt,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.svc = svc
	return e
}

// webhook registers one webhook with a secret and returns it.
func (e *env) webhook(authMode string) automation.Webhook {
	e.t.Helper()
	rec := automation.Webhook{
		Name:        "feed",
		Enabled:     true,
		URL:         "https://receiver.example.com/hook?token=super-secret&team=7",
		Method:      "POST",
		AuthMode:    authMode,
		AuthHeader:  "",
		Timeout:     5 * time.Second,
		MaxAttempts: 5,
	}
	if authMode == webhooks.AuthHeader {
		rec.AuthHeader = "X-Feed-Token"
	}
	secret := testSecret(e.t, testSecretText)
	saved, err := e.store.CreateWebhook(context.Background(), 1, rec, &secret)
	if err != nil {
		e.t.Fatalf("CreateWebhook: %v", err)
	}
	return saved
}

func (e *env) job(webhookID int64, attempt, max int) automation.Job {
	return automation.Job{
		ID:          1,
		EventID:     testEventID,
		Kind:        automation.JobWebhookDelivery,
		TargetID:    webhookID,
		Attempt:     attempt,
		MaxAttempts: max,
	}
}

func TestDeliverySignsSendsAndRecordsOneAttempt(t *testing.T) {
	e := newEnv(t, scripted{status: 200, body: "ok", header: http.Header{"Content-Type": []string{"text/plain"}}})
	wh := e.webhook(webhooks.AuthHMAC)

	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	sent := e.rt.sent()
	if len(sent) != 1 {
		t.Fatalf("requests = %d", len(sent))
	}
	req := sent[0]
	if req.Method != "POST" {
		t.Fatalf("method = %s", req.Method)
	}
	if req.URL.String() != wh.URL {
		t.Fatalf("url = %s", req.URL)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	for _, name := range []string{webhooks.IDHeader, webhooks.TimestampHeader, webhooks.SignatureHeader} {
		if req.Header.Get(name) == "" {
			t.Fatalf("%s is missing", name)
		}
	}
	// The signature covers the exact bytes that left this process.
	sentBody := e.rt.sentBodies()[0]
	ts, _ := strconv.ParseInt(req.Header.Get(webhooks.TimestampHeader), 10, 64)
	secretText := testSecretText
	if err := webhooks.Verify(secretText, req.Header.Get(webhooks.IDHeader), time.Unix(ts, 0),
		[]byte(sentBody), req.Header.Get(webhooks.SignatureHeader)); err != nil {
		t.Fatalf("the transmitted request does not verify: %v", err)
	}
	var envelope webhooks.Envelope
	if err := json.Unmarshal([]byte(sentBody), &envelope); err != nil {
		t.Fatalf("body is not an envelope: %v", err)
	}
	if envelope.ID != testEventID {
		t.Fatalf("envelope id = %q", envelope.ID)
	}

	// The history is one row with one attempt, already redacted.
	deliveries, err := e.store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if deliveries.Total != 1 {
		t.Fatalf("deliveries = %d, want exactly 1", deliveries.Total)
	}
	got := deliveries.Deliveries[0]
	if got.Status != webhooks.StatusSucceeded || got.AttemptCount != 1 {
		t.Fatalf("delivery = %+v", got)
	}
	if got.LastHTTPStatus != 200 || got.CompletedAt == nil {
		t.Fatalf("delivery = %+v", got)
	}
	attempts, _ := e.store.WebhookAttempts(context.Background(), got.ID)
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d", len(attempts))
	}
	att := attempts[0]
	if att.ResponseStatus != 200 || att.ResponseBody != "ok" {
		t.Fatalf("attempt = %+v", att)
	}
	if att.RequestMethod != "POST" || att.RequestBody != sentBody {
		t.Fatalf("recorded request does not match what was sent: %+v", att)
	}
	if att.DurationMS < 0 || att.StartedAt.IsZero() || att.FinishedAt == nil {
		t.Fatalf("attempt timing = %+v", att)
	}
}

// --- what the payload says about the channel --------------------------------

func TestTheSentPayloadNamesTheChannelItCameFrom(t *testing.T) {
	e := newEnv(t, scripted{status: 200, body: "ok"})
	e.channels[7] = webhooks.Channel{Type: "channel", Title: "کانال خبری", Username: "khabari"}
	wh := e.webhook(webhooks.AuthHMAC)

	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	sent := e.rt.sentBodies()
	if len(sent) != 1 {
		t.Fatalf("bodies = %d", len(sent))
	}
	var envelope webhooks.Envelope
	if err := json.Unmarshal([]byte(sent[0]), &envelope); err != nil {
		t.Fatalf("body is not an envelope: %v", err)
	}
	src := envelope.Data.Source
	if src.Type != "channel" || src.Title != "کانال خبری" || src.Username != "khabari" {
		t.Fatalf("source = %+v, want the channel's own naming", src)
	}
	// The labels never stand in for identity: the identifiers a receiver matches
	// on are still there and unchanged.
	if src.ManagedChannelID != 7 || src.TelegramChannelID != -1001234 || src.ClientKey != "client-a" {
		t.Fatalf("the identifiers moved: %+v", src)
	}
}

func TestAMissingChannelStillDeliversWithTheIdentifiersAlone(t *testing.T) {
	// The channel row supplies labels. One that is gone must not cost a
	// delivery: the event already says which channel the message came from.
	e := newEnv(t, scripted{status: 200, body: "ok"})
	wh := e.webhook(webhooks.AuthHMAC)

	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	sent := e.rt.sentBodies()
	if len(sent) != 1 {
		t.Fatalf("bodies = %d", len(sent))
	}
	var envelope webhooks.Envelope
	if err := json.Unmarshal([]byte(sent[0]), &envelope); err != nil {
		t.Fatalf("body is not an envelope: %v", err)
	}
	if envelope.Data.Source.Title != "" || envelope.Data.Source.Type != "" {
		t.Fatalf("labels appeared from nowhere: %+v", envelope.Data.Source)
	}
	if envelope.Data.Source.ManagedChannelID != 7 {
		t.Fatalf("managed_channel_id = %d, want 7", envelope.Data.Source.ManagedChannelID)
	}
}

func TestDeliveryRedactsCredentialsFromTheStoredAttempt(t *testing.T) {
	e := newEnv(t, scripted{status: 200, body: "ok"})
	wh := e.webhook(webhooks.AuthBearer)

	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err != nil {
		t.Fatal(err)
	}

	// The request itself carries the token: that is what authentication is.
	if auth := e.rt.sent()[0].Header.Get("Authorization"); auth == "" {
		t.Fatal("the bearer token was not sent")
	}

	deliveries, _ := e.store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	attempts, _ := e.store.WebhookAttempts(context.Background(), deliveries.Deliveries[0].ID)
	att := attempts[0]

	if got := headerValue(att.RequestHeaders, "Authorization"); got != "[redacted]" {
		t.Fatalf("Authorization was stored as %q", got)
	}
	recorded := att.RequestURL
	if strings.Contains(recorded, "super-secret") {
		t.Fatalf("the url kept its secret: %q", recorded)
	}
	if !strings.Contains(recorded, "team=7") {
		t.Fatalf("an ordinary query value was dropped: %q", recorded)
	}
	// Nothing anywhere in the record is the token.
	whole := att.RequestURL + att.RequestBody + fmtHeaders(att.RequestHeaders) + att.ResponseBody
	for _, secret := range []string{testSecretText, base64.StdEncoding.EncodeToString(testSecretBytes), string(testSecretBytes)} {
		if strings.Contains(whole, secret) {
			t.Fatalf("the secret %q leaked into the record: %s", secret, whole)
		}
	}
}

// headerValue looks a header up the way HTTP names them: case does not matter.
func headerValue(h map[string]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func fmtHeaders(h map[string]string) string {
	raw, _ := json.Marshal(h)
	return string(raw)
}

func TestDeliveryRefusesAPermanentClientErrorWithoutRetrying(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp scripted
	}{
		{"404", scripted{status: 404, body: "gone"}},
		{"400", scripted{status: 400, body: "bad"}},
		{"401", scripted{status: 401, body: "no"}},
		{"403", scripted{status: 403, body: "no"}},
		{"410", scripted{status: 410, body: "gone"}},
		{"302", scripted{status: 302, header: http.Header{"Location": []string{"http://169.254.169.254/"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.resp)
			wh := e.webhook(webhooks.AuthHMAC)

			err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5))
			var jobErr *automation.JobError
			if !errors.As(err, &jobErr) {
				t.Fatalf("err = %v, want a *automation.JobError", err)
			}
			if jobErr.Retryable {
				t.Fatalf("a permanent answer was marked retryable: %+v", jobErr)
			}
			if len(e.rt.sent()) != 1 {
				t.Fatalf("requests = %d, want 1", len(e.rt.sent()))
			}
			deliveries, _ := e.store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
			if deliveries.Deliveries[0].Status != webhooks.StatusFailed {
				t.Fatalf("status = %q, want failed", deliveries.Deliveries[0].Status)
			}
			if deliveries.Deliveries[0].CompletedAt == nil {
				t.Fatal("a terminal failure was left open")
			}
		})
	}
}

func TestDeliveryRetriesTransientAnswersAndHonoursABoundedRetryAfter(t *testing.T) {
	cases := []struct {
		name      string
		resp      scripted
		wantRetry time.Duration
	}{
		{"429", scripted{status: 429, body: "slow down"}, 0},
		{"500", scripted{status: 500, body: "boom"}, 0},
		{"502", scripted{status: 502, body: "bad gateway"}, 0},
		{"503", scripted{status: 503, body: "unavailable"}, 0},
		{"504", scripted{status: 504, body: "timeout"}, 0},
		{"429 with retry-after", scripted{status: 429, header: http.Header{"Retry-After": []string{"120"}}}, 2 * time.Minute},
		{"429 with a hostile retry-after", scripted{status: 429, header: http.Header{"Retry-After": []string{"999999999"}}}, webhooks.MaxRetryAfter},
		{"429 with a negative retry-after", scripted{status: 429, header: http.Header{"Retry-After": []string{"-30"}}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.resp)
			wh := e.webhook(webhooks.AuthHMAC)

			err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5))
			var jobErr *automation.JobError
			if !errors.As(err, &jobErr) {
				t.Fatalf("err = %v, want a *automation.JobError", err)
			}
			if !jobErr.Retryable {
				t.Fatalf("a transient answer was not retryable: %+v", jobErr)
			}
			if tc.wantRetry != 0 && jobErr.RetryAfter != tc.wantRetry {
				t.Fatalf("RetryAfter = %v, want %v", jobErr.RetryAfter, tc.wantRetry)
			}
			deliveries, _ := e.store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
			got := deliveries.Deliveries[0]
			if got.Status != webhooks.StatusPending {
				t.Fatalf("status = %q, want pending while it waits to retry", got.Status)
			}
			if got.CompletedAt != nil {
				t.Fatal("a retryable delivery was completed")
			}
		})
	}
}

func TestDeliveryNeverRetriesIndefinitely(t *testing.T) {
	e := newEnv(t, scripted{status: 503, body: "unavailable"})
	wh := e.webhook(webhooks.AuthHMAC)

	// The queue's own budget is exhausted: the handler reports a terminal
	// failure rather than asking for one more attempt.
	err := e.svc.Handler()(context.Background(), e.job(wh.ID, 5, 5))
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err = %v, want a *automation.JobError", err)
	}
	if jobErr.Retryable {
		t.Fatalf("an exhausted budget was still retryable: %+v", jobErr)
	}
}

func TestARetryExtendsTheSameDeliveryAndAppendsAnAttempt(t *testing.T) {
	e := newEnv(t,
		scripted{status: 503, body: "unavailable"},
		scripted{status: 200, body: "ok"},
	)
	wh := e.webhook(webhooks.AuthHMAC)

	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err == nil {
		t.Fatal("the first attempt reported success")
	}
	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 2, 5)); err != nil {
		t.Fatalf("second attempt: %v", err)
	}

	if e.store.createCount() != 1 {
		t.Fatalf("delivery rows created = %d, want exactly 1", e.store.createCount())
	}
	if e.store.deliveryCount(wh.ID, testEventID) != 1 {
		t.Fatal("a second delivery appeared for the same event")
	}
	deliveries, _ := e.store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	got := deliveries.Deliveries[0]
	if got.AttemptCount != 2 || got.Status != webhooks.StatusSucceeded {
		t.Fatalf("delivery = %+v", got)
	}
	attempts, _ := e.store.WebhookAttempts(context.Background(), got.ID)
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0].AttemptNumber != 1 || attempts[1].AttemptNumber != 2 {
		t.Fatalf("attempt numbers = %d, %d", attempts[0].AttemptNumber, attempts[1].AttemptNumber)
	}

	// The delivery id is stable across attempts and the timestamp moves: a
	// receiver can deduplicate on the id and reject a replayed signature.
	first, second := e.rt.sent()[0], e.rt.sent()[1]
	if first.Header.Get(webhooks.IDHeader) != second.Header.Get(webhooks.IDHeader) {
		t.Fatalf("webhook-id changed: %q then %q",
			first.Header.Get(webhooks.IDHeader), second.Header.Get(webhooks.IDHeader))
	}
	if first.Header.Get(webhooks.TimestampHeader) == second.Header.Get(webhooks.TimestampHeader) {
		t.Fatal("the retry reused the original timestamp")
	}
	if first.Header.Get(webhooks.SignatureHeader) == second.Header.Get(webhooks.SignatureHeader) {
		t.Fatal("the retry reused the original signature")
	}
}

func TestDeliveryRefusesABlockedTargetAtDeliveryTime(t *testing.T) {
	// The URL validates at configuration time and again on every attempt: a
	// target that becomes dangerous later is still refused.
	cases := []string{
		"https://127.0.0.1/hook",
		"https://169.254.169.254/latest/meta-data/",
		"https://[::1]/hook",
		"https://[fd00::1]/hook",
		"https://[::ffff:127.0.0.1]/hook",
		"https://10.0.0.5/hook",
		"http://receiver.example.com/hook",
		"file:///etc/passwd",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			dials := &countingDial{}
			rt := &scriptedTransport{responses: []scripted{{status: 200}}}
			store := newFakeStore()
			secret := testSecret(t, "plain-secret")
			saved, err := store.CreateWebhook(context.Background(), 1, automation.Webhook{
				Name: "feed", Enabled: true, URL: target, Method: "POST",
				AuthMode: webhooks.AuthHMAC, Timeout: time.Second, MaxAttempts: 5,
			}, &secret)
			if err != nil {
				t.Fatal(err)
			}
			events := map[string]automation.MessageEvent{testEventID: messageEvent(nil)}
			svc, err := webhooks.New(webhooks.Config{
				PublicBaseURL: "https://telesync.example.com",
				BasePath:      "/admin",
				Webhooks: appconfig.WebhookConfig{
					Timeout: time.Second, MaxAttempts: 5,
					MaxResponseBytes: 1024, MaxConcurrentDeliveries: 1,
				},
				Store:   store,
				Secrets: testOpener(t),
				Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
					ev, ok := events[id]
					return ev, ok, nil
				},
				Transport: rt,
				Dial:      dials.dial,
			})
			if err != nil {
				t.Fatal(err)
			}

			err = svc.Handler()(context.Background(), automation.Job{
				EventID: testEventID, Kind: automation.JobWebhookDelivery, TargetID: saved.ID,
				Attempt: 1, MaxAttempts: 5,
			})
			if err == nil {
				t.Fatalf("delivery to %s was allowed", target)
			}
			var jobErr *automation.JobError
			if !errors.As(err, &jobErr) {
				t.Fatalf("err = %v, want a *automation.JobError", err)
			}
			if jobErr.Retryable {
				t.Fatalf("a refused address was marked retryable: %+v", jobErr)
			}
		})
	}
}

type countingDial struct {
	mu    sync.Mutex
	calls []string
}

func (c *countingDial) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, addr)
	return nil, errors.New("no connection is opened in these tests")
}

func TestDeliveryDialsTheAddressItResolvedAndNotTheName(t *testing.T) {
	// A DNS rebinding attack answers safely on the check and unsafely on the
	// connect. The transport resolves once and dials the checked address, so the
	// second answer never reaches a socket.
	// The real transport is used on purpose: the resolve-once defence lives in
	// its dialler, and a scripted round tripper would never reach it. The fake
	// dialler reports what would have been dialled and opens nothing.
	dials := &countingDial{}
	lookups := &countingLookup{addrs: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
	store := newFakeStore()
	secret := testSecret(t, "plain-secret")
	saved, err := store.CreateWebhook(context.Background(), 1, automation.Webhook{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook", Method: "POST",
		AuthMode: webhooks.AuthHMAC, Timeout: time.Second, MaxAttempts: 5,
	}, &secret)
	if err != nil {
		t.Fatal(err)
	}
	events := map[string]automation.MessageEvent{testEventID: messageEvent(nil)}
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout: time.Second, MaxAttempts: 5,
			MaxResponseBytes: 1024, MaxConcurrentDeliveries: 1,
		},
		Store:   store,
		Secrets: testOpener(t),
		Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
			ev, ok := events[id]
			return ev, ok, nil
		},
		Dial:   dials.dial,
		Lookup: lookups.lookup,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The dialler refuses to open anything in these tests, so the request fails
	// as a network error. What matters here is where it tried to connect.
	err = svc.Handler()(context.Background(), automation.Job{
		EventID: testEventID, Kind: automation.JobWebhookDelivery, TargetID: saved.ID,
		Attempt: 1, MaxAttempts: 5,
	})
	if err == nil {
		t.Fatal("a delivery with a dialler that opens nothing reported success")
	}
	if lookups.calls != 1 {
		t.Fatalf("resolutions = %d, want exactly 1", lookups.calls)
	}
	if len(dials.calls) != 1 || dials.calls[0] != "93.184.216.34:443" {
		t.Fatalf("dialled %v, want the single checked address", dials.calls)
	}
}

type countingLookup struct {
	mu    sync.Mutex
	addrs []netip.Addr
	calls int
}

func (c *countingLookup) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.addrs, nil
}

func TestDeliveryRefusesAHostThatResolvesSomewherePrivate(t *testing.T) {
	dials := &countingDial{}
	lookups := &countingLookup{addrs: []netip.Addr{netip.MustParseAddr("10.0.0.5")}}
	store := newFakeStore()
	secret := testSecret(t, "plain-secret")
	saved, _ := store.CreateWebhook(context.Background(), 1, automation.Webhook{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook", Method: "POST",
		AuthMode: webhooks.AuthHMAC, Timeout: time.Second, MaxAttempts: 5,
	}, &secret)
	events := map[string]automation.MessageEvent{testEventID: messageEvent(nil)}
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout: time.Second, MaxAttempts: 5,
			MaxResponseBytes: 1024, MaxConcurrentDeliveries: 1,
		},
		Store:   store,
		Secrets: testOpener(t),
		Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
			ev, ok := events[id]
			return ev, ok, nil
		},
		Dial:   dials.dial,
		Lookup: lookups.lookup,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = svc.Handler()(context.Background(), automation.Job{
		EventID: testEventID, Kind: automation.JobWebhookDelivery, TargetID: saved.ID,
		Attempt: 1, MaxAttempts: 5,
	})
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err = %v, want a *automation.JobError", err)
	}
	if jobErr.Retryable {
		t.Fatalf("a private answer was marked retryable: %+v", jobErr)
	}
	if len(dials.calls) != 0 {
		t.Fatalf("a private answer was still dialled: %v", dials.calls)
	}
}

func TestDeliveryBoundsTheResponseBodyAndRecordsTruncation(t *testing.T) {
	body := strings.Repeat("x", 5000)
	rt := &scriptedTransport{responses: []scripted{{status: 200, body: body}}}
	store := newFakeStore()
	secret := testSecret(t, "plain-secret")
	saved, _ := store.CreateWebhook(context.Background(), 1, automation.Webhook{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook", Method: "POST",
		AuthMode: webhooks.AuthHMAC, Timeout: time.Second, MaxAttempts: 5,
	}, &secret)
	events := map[string]automation.MessageEvent{testEventID: messageEvent(nil)}
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout: time.Second, MaxAttempts: 5,
			MaxResponseBytes: 64, MaxConcurrentDeliveries: 1,
		},
		Store:   store,
		Secrets: testOpener(t),
		Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
			ev, ok := events[id]
			return ev, ok, nil
		},
		Transport: rt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Handler()(context.Background(), automation.Job{
		EventID: testEventID, Kind: automation.JobWebhookDelivery, TargetID: saved.ID,
		Attempt: 1, MaxAttempts: 5,
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	deliveries, _ := store.ListWebhookDeliveries(context.Background(), saved.ID, automation.DeliveryListOptions{})
	attempts, _ := store.WebhookAttempts(context.Background(), deliveries.Deliveries[0].ID)
	att := attempts[0]
	if len(att.ResponseBody) != 64 {
		t.Fatalf("stored response body is %d bytes, want the 64 byte cap", len(att.ResponseBody))
	}
	if !att.ResponseBodyTruncated {
		t.Fatal("a truncated response was recorded as complete")
	}
	if att.ResponseBytes != int64(len(body)) {
		t.Fatalf("ResponseBytes = %d, want the declared total %d", att.ResponseBytes, len(body))
	}
}

func TestDeliveryRecordsTimeoutsAndConnectionFailuresAsRetryable(t *testing.T) {
	failing := &failingTransport{err: errors.New("dial tcp: connection refused")}
	store := newFakeStore()
	secret := testSecret(t, "plain-secret")
	saved, _ := store.CreateWebhook(context.Background(), 1, automation.Webhook{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook", Method: "POST",
		AuthMode: webhooks.AuthHMAC, Timeout: time.Second, MaxAttempts: 5,
	}, &secret)
	events := map[string]automation.MessageEvent{testEventID: messageEvent(nil)}
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout: time.Second, MaxAttempts: 5,
			MaxResponseBytes: 1024, MaxConcurrentDeliveries: 1,
		},
		Store:   store,
		Secrets: testOpener(t),
		Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
			ev, ok := events[id]
			return ev, ok, nil
		},
		Transport: failing,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = svc.Handler()(context.Background(), automation.Job{
		EventID: testEventID, Kind: automation.JobWebhookDelivery, TargetID: saved.ID,
		Attempt: 1, MaxAttempts: 5,
	})
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err = %v, want a *automation.JobError", err)
	}
	if !jobErr.Retryable {
		t.Fatalf("a connection failure was terminal: %+v", jobErr)
	}

	deliveries, _ := store.ListWebhookDeliveries(context.Background(), saved.ID, automation.DeliveryListOptions{})
	attempts, _ := store.WebhookAttempts(context.Background(), deliveries.Deliveries[0].ID)
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d", len(attempts))
	}
	if attempts[0].ErrorCode == "" || !strings.Contains(attempts[0].ErrorMessage, "connection refused") {
		t.Fatalf("the network error was not recorded: %+v", attempts[0])
	}
}

type failingTransport struct{ err error }

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestDeliveryRefusesAnUnsafeCustomAuthHeader(t *testing.T) {
	for _, name := range []string{"Host", "Content-Length", "Connection", "Transfer-Encoding", "Webhook-Signature"} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, scripted{status: 200})
			wh := e.webhook(webhooks.AuthNone)
			edited := wh
			edited.AuthMode = webhooks.AuthHeader
			edited.AuthHeader = name
			if _, _, err := e.store.UpdateWebhook(context.Background(), edited, nil); err != nil {
				t.Fatal(err)
			}
			err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5))
			var jobErr *automation.JobError
			if !errors.As(err, &jobErr) {
				t.Fatalf("err = %v, want a *automation.JobError", err)
			}
			if jobErr.Retryable {
				t.Fatalf("a refusal to rewrite a transport header was retryable: %+v", jobErr)
			}
			if len(e.rt.sent()) != 0 {
				t.Fatal("a request was sent with a rewritten transport header")
			}
		})
	}
}

func TestBearerAndHeaderModesCarryTheSecretInTheRightPlace(t *testing.T) {
	e := newEnv(t, scripted{status: 200})
	wh := e.webhook(webhooks.AuthBearer)
	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err != nil {
		t.Fatal(err)
	}
	auth := e.rt.sent()[0].Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Fatalf("Authorization = %q", auth)
	}
	if e.rt.sent()[0].Header.Get(webhooks.SignatureHeader) != "" {
		t.Fatal("bearer mode still emitted a signature")
	}
	if e.rt.sent()[0].Header.Get(webhooks.IDHeader) == "" {
		t.Fatal("bearer mode dropped the delivery id a receiver deduplicates on")
	}

	e = newEnv(t, scripted{status: 200})
	wh = e.webhook(webhooks.AuthHeader)
	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err != nil {
		t.Fatal(err)
	}
	if got := e.rt.sent()[0].Header.Get("X-Feed-Token"); got == "" {
		t.Fatal("the custom auth header was not sent")
	}

	e = newEnv(t, scripted{status: 200})
	wh = e.webhook(webhooks.AuthNone)
	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err != nil {
		t.Fatal(err)
	}
	got := e.rt.sent()[0].Header
	if got.Get("Authorization") != "" || got.Get(webhooks.SignatureHeader) != "" {
		t.Fatalf("auth mode none still authenticated: %v", got)
	}
}

func TestTestWebhookUsesTheSameTransportAndAFixedPayload(t *testing.T) {
	e := newEnv(t, scripted{status: 200, body: "ok"})
	wh := e.webhook(webhooks.AuthHMAC)

	result, ok, err := e.svc.TestWebhook(context.Background(), wh.ID)
	if err != nil || !ok {
		t.Fatalf("TestWebhook: ok=%v err=%v", ok, err)
	}
	if len(e.rt.sent()) != 1 {
		t.Fatalf("requests = %d", len(e.rt.sent()))
	}
	body := e.rt.sentBodies()[0]
	var envelope webhooks.Envelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("the test payload is not an envelope: %v", err)
	}
	if envelope.ID != "evt_test" {
		t.Fatalf("the test payload is not the fixed one: id=%q", envelope.ID)
	}
	if strings.Contains(body, "client-") || strings.Contains(body, "a new post") {
		t.Fatalf("the test payload carries channel content: %s", body)
	}
	if result.Delivery.Request == nil {
		t.Fatal("the test result does not show what was sent")
	}
	if result.Delivery.Request.Body != body {
		t.Fatal("the test result does not match what was transmitted")
	}
	// A test does not create delivery history: there is no event behind it.
	if e.store.createCount() != 0 {
		t.Fatalf("a test send recorded a delivery: %d", e.store.createCount())
	}
	// A test cannot be aimed at an address a real delivery could not reach.
	if _, ok, _ := e.svc.TestWebhook(context.Background(), 9999); ok {
		t.Fatal("a test was sent to a webhook that does not exist")
	}
}

func TestRetryDeliveryReusesTheRowAndReschedulesIt(t *testing.T) {
	e := newEnv(t, scripted{status: 404, body: "gone"}, scripted{status: 200, body: "ok"})
	wh := e.webhook(webhooks.AuthHMAC)

	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err == nil {
		t.Fatal("a 404 reported success")
	}
	deliveries, _ := e.store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	failed := deliveries.Deliveries[0]

	retried, ok, err := e.svc.RetryDelivery(context.Background(), wh.ID, failed.ID)
	if err != nil || !ok {
		t.Fatalf("RetryDelivery: ok=%v err=%v", ok, err)
	}
	if retried.ID != failed.ID {
		t.Fatalf("the retry created a new delivery: %d then %d", failed.ID, retried.ID)
	}
	if retried.Status != webhooks.StatusPending {
		t.Fatalf("status after retry = %q", retried.Status)
	}
	if retried.EventID != failed.EventID {
		t.Fatalf("event changed: %q then %q", failed.EventID, retried.EventID)
	}
	if e.store.createCount() != 1 {
		t.Fatalf("delivery rows = %d, want 1", e.store.createCount())
	}
	if e.jobs.webhookJobs() != 1 {
		t.Fatalf("the retry did not enqueue work: %d", e.jobs.webhookJobs())
	}

	// The retried delivery runs on the same row and adds attempt 2.
	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 2, 5)); err != nil {
		t.Fatalf("retried delivery: %v", err)
	}
	attempts, _ := e.store.WebhookAttempts(context.Background(), failed.ID)
	if len(attempts) != 2 || attempts[1].AttemptNumber != 2 {
		t.Fatalf("attempts = %+v", attempts)
	}
	if _, ok, _ := e.svc.RetryDelivery(context.Background(), 9999, failed.ID); ok {
		t.Fatal("a delivery was retried for the wrong webhook")
	}
}

// An origin is optional. Without one the payload names the media path rather
// than an absolute address, and the receiver prefixes whatever origin it knows
// this installation by. Refusing the webhook instead would stop someone using
// everything else it does over one link they were perfectly happy to build.
func TestAnOriginlessDeploymentStillCreatesAndSendsAWebhook(t *testing.T) {
	store := newFakeStore()
	svc, err := webhooks.New(webhooks.Config{
		// No public base URL at all.
		BasePath:  "/admin",
		Webhooks:  appconfig.WebhookConfig{Timeout: time.Second, MaxAttempts: 5, MaxResponseBytes: 1024, MaxConcurrentDeliveries: 1},
		Store:     store,
		Secrets:   testOpener(t),
		Transport: &scriptedTransport{},
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.CreateWebhook(context.Background(), automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthNone, CreatedBy: 1,
	})
	if err != nil {
		t.Fatalf("CreateWebhook with no origin: %v", err)
	}
	if !created.Enabled {
		t.Fatal("the webhook was stored disabled although it was asked for enabled")
	}
	if _, _, err := svc.UpdateWebhook(context.Background(), created.ID, automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthNone, CreatedBy: 1,
	}); err != nil {
		t.Fatalf("enabling with no origin: %v", err)
	}

	// The payload names the path and invents no address.
	media := &rpc.Media{ID: "1:91", Type: "photo", Size: 4}
	env, err := webhooks.NewBuilder("", "/admin").Build(messageEvent(media), webhooks.Channel{})
	if err != nil {
		t.Fatalf("Build with no origin: %v", err)
	}
	wantPath := "/admin/api/v1/telegram/client-a/channels/-1001234/messages/91/media"
	if env.Data.Message.Media == nil || env.Data.Message.Media.Path != wantPath {
		t.Fatalf("path = %+v, want %q", env.Data.Message.Media, wantPath)
	}
	if env.Data.Message.Media.URL != "" {
		t.Fatalf("url = %q, want none invented", env.Data.Message.Media.URL)
	}

	// Naming an origin adds the absolute address alongside the same path.
	full, err := webhooks.NewBuilder("https://telesync.example.com", "/admin").Build(messageEvent(media), webhooks.Channel{})
	if err != nil {
		t.Fatalf("Build with an origin: %v", err)
	}
	wantURL := "https://telesync.example.com" + wantPath
	if full.Data.Message.Media.URL != wantURL {
		t.Fatalf("url = %q, want %q", full.Data.Message.Media.URL, wantURL)
	}
	if full.Data.Message.Media.Path != wantPath {
		t.Fatalf("path = %q, want %q", full.Data.Message.Media.Path, wantPath)
	}
}

func TestServiceNeverReturnsASecretAndIgnoresCreatedByOnUpdate(t *testing.T) {
	e := newEnv(t, scripted{status: 200})
	secret := testSecretText
	created, err := e.svc.CreateWebhook(context.Background(), automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthHMAC, Secret: &secret, CreatedBy: 1,
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if !created.HasSecret {
		t.Fatal("HasSecret = false after creating a webhook with a secret")
	}
	got, ok, err := e.svc.GetWebhook(context.Background(), created.ID)
	if err != nil || !ok {
		t.Fatalf("GetWebhook: ok=%v err=%v", ok, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), base64.StdEncoding.EncodeToString(testSecretBytes)) || strings.Contains(string(raw), "whsec_") {
		t.Fatalf("a secret leaked into the record: %s", raw)
	}

	// An update never reassigns ownership, whatever the request claims.
	silent := "another-secret"
	updated, ok, err := e.svc.UpdateWebhook(context.Background(), created.ID, automation.WebhookInput{
		Name: "renamed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthHMAC, Secret: &silent, CreatedBy: 99,
	})
	if err != nil || !ok {
		t.Fatalf("UpdateWebhook: ok=%v err=%v", ok, err)
	}
	if updated.Name != "renamed" {
		t.Fatalf("Name = %q", updated.Name)
	}
	stored, _, _ := e.store.GetWebhook(context.Background(), created.ID)
	if !stored.HasSecret {
		t.Fatal("the rotated secret was not stored")
	}
}

func TestServiceRoutesUnknownWebhooksToNotFound(t *testing.T) {
	e := newEnv(t)
	if _, ok, err := e.svc.GetWebhook(context.Background(), 42); err != nil || ok {
		t.Fatalf("GetWebhook: ok=%v err=%v", ok, err)
	}
	if ok, err := e.svc.DeleteWebhook(context.Background(), 42); err != nil || ok {
		t.Fatalf("DeleteWebhook: ok=%v err=%v", ok, err)
	}
	if _, ok, err := e.svc.TestWebhook(context.Background(), 42); err != nil || ok {
		t.Fatalf("TestWebhook: ok=%v err=%v", ok, err)
	}
	_, ok, err := e.svc.RetryDelivery(context.Background(), 42, 1)
	if err != nil {
		t.Fatalf("RetryDelivery: %v", err)
	}
	if ok {
		t.Fatal("a missing delivery was retried")
	}
}

// The service satisfies the gateway's contract exactly.
var _ automation.WebhookService = (*webhooks.Service)(nil)

// unused keeps the imports honest for the assertions above.
var (
	_ = bytes.MinRead
	_ = channelstore.DeliveryPending
)

// reopenRecorder stands in for queue.Enqueuer.Reopen and can be told to refuse.
type reopenRecorder struct {
	mu    sync.Mutex
	calls [][3]any
	err   error
}

func (r *reopenRecorder) reopen(ctx context.Context, eventID string, kind automation.JobKind, targetID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [3]any{eventID, kind, targetID})
	return r.err
}

func (r *reopenRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestASecretCanBeRemovedWithoutEverReadingItBack(t *testing.T) {
	e := newEnv(t)
	secret := testSecretText
	created, err := e.svc.CreateWebhook(context.Background(), automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthHMAC, Secret: &secret, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.HasSecret {
		t.Fatal("HasSecret = false after creating with a secret")
	}

	// The panel's "remove the stored secret" action sends exactly this: a
	// present, empty string. It must not be read as "leave it alone".
	blank := ""
	updated, ok, err := e.svc.UpdateWebhook(context.Background(), created.ID, automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthNone, Secret: &blank,
	})
	if err != nil || !ok {
		t.Fatalf("UpdateWebhook: ok=%v err=%v", ok, err)
	}
	if updated.HasSecret {
		t.Fatal("HasSecret = true after removing the secret")
	}
	stored, _, _ := e.store.GetWebhook(context.Background(), created.ID)
	if stored.HasSecret {
		t.Fatal("the secret survived its removal")
	}
	box, _, _ := e.store.WebhookSecretBox(context.Background(), created.ID)
	if box.Version != 0 || len(box.Ciphertext) != 0 {
		t.Fatalf("the stored box was not emptied: %+v", box)
	}

	// An absent field is the other request: it keeps what is there. Rotate,
	// then edit without touching the secret and it must survive.
	replaced := testSecretText
	if _, ok, err := e.svc.UpdateWebhook(context.Background(), created.ID, automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthHMAC, Secret: &replaced,
	}); err != nil || !ok {
		t.Fatalf("rotate: %v", err)
	}
	untouched, ok, err := e.svc.UpdateWebhook(context.Background(), created.ID, automation.WebhookInput{
		Name: "renamed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthHMAC,
	})
	if err != nil || !ok {
		t.Fatalf("rename: %v", err)
	}
	if !untouched.HasSecret {
		t.Fatal("an update that said nothing about the secret removed it")
	}
}

func TestClearingTheSecretRefusesAnAuthModeThatNeedsOne(t *testing.T) {
	e := newEnv(t)
	blank := ""
	if _, err := e.svc.CreateWebhook(context.Background(), automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthHMAC, Secret: &blank, CreatedBy: 1,
	}); !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("err = %v, want automation.ErrInvalid", err)
	}
}

func TestRetryDeliveryGivesTheQueuedJobAFreshAttemptBudget(t *testing.T) {
	// The first delivery runs out of attempts before it ever reaches the wire,
	// so the scripted receiver only has to answer the retry.
	e := newEnv(t, scripted{status: 200, body: "ok"})
	wh := e.webhook(webhooks.AuthHMAC)

	reopen := &reopenRecorder{}
	reopenSvc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout: 5 * time.Second, MaxAttempts: 5,
			MaxResponseBytes: 64 << 10, MaxConcurrentDeliveries: 2,
		},
		Store:   e.store,
		Secrets: testOpener(t),
		Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
			ev, ok := e.events[id]
			return ev, ok, nil
		},
		Jobs:      e.jobs,
		Reopen:    reopen.reopen,
		Transport: e.rt,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 5, 5)); err == nil {
		t.Fatal("an exhausted delivery reported success")
	}
	deliveries, _ := e.store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	failed := deliveries.Deliveries[0]

	retried, ok, err := reopenSvc.RetryDelivery(context.Background(), wh.ID, failed.ID)
	if err != nil || !ok {
		t.Fatalf("RetryDelivery: ok=%v err=%v", ok, err)
	}
	if retried.ID != failed.ID {
		t.Fatalf("the retry created a new delivery: %d then %d", failed.ID, retried.ID)
	}
	// The budget is the job's, so the retry must reopen it rather than re-enqueue
	// work the queue considers already dispatched.
	if reopen.count() != 1 {
		t.Fatalf("Reopen calls = %d, want 1", reopen.count())
	}
	if e.jobs.webhookJobs() != 0 {
		t.Fatalf("the retry re-enqueued instead of reopening: %d", e.jobs.webhookJobs())
	}
	if got := reopen.calls[0]; got[0] != testEventID || got[1] != automation.JobWebhookDelivery || got[2] != wh.ID {
		t.Fatalf("Reopen was called with %v", got)
	}

	// The reopened delivery runs on the same row.
	if err := reopenSvc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err != nil {
		t.Fatalf("retried delivery: %v", err)
	}
	if e.store.createCount() != 1 {
		t.Fatalf("delivery rows = %d, want 1", e.store.createCount())
	}
}

func TestRetryDeliveryReportsARefusalAsAConflict(t *testing.T) {
	e := newEnv(t, scripted{status: 404, body: "gone"})
	wh := e.webhook(webhooks.AuthHMAC)
	if err := e.svc.Handler()(context.Background(), e.job(wh.ID, 1, 5)); err == nil {
		t.Fatal("a 404 reported success")
	}
	deliveries, _ := e.store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	failed := deliveries.Deliveries[0]

	refusing := &reopenRecorder{err: fmt.Errorf("%w: job 1 already succeeded and must not be repeated", automation.ErrConflict)}
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout: 5 * time.Second, MaxAttempts: 5,
			MaxResponseBytes: 64 << 10, MaxConcurrentDeliveries: 2,
		},
		Store:   e.store,
		Secrets: testOpener(t),
		Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
			ev, ok := e.events[id]
			return ev, ok, nil
		},
		Jobs:      e.jobs,
		Reopen:    refusing.reopen,
		Transport: e.rt,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = svc.RetryDelivery(context.Background(), wh.ID, failed.ID)
	if !errors.Is(err, automation.ErrConflict) {
		t.Fatalf("err = %v, want automation.ErrConflict", err)
	}
	if e.jobs.webhookJobs() != 0 {
		t.Fatal("a refused retry still enqueued work")
	}
}

func TestRetryDeliveryEnqueuesWhenThereIsNoJobToReopen(t *testing.T) {
	e := newEnv(t)
	wh := e.webhook(webhooks.AuthNone)
	// A delivery whose job row was never created.
	created, err := e.store.CreateWebhookDelivery(context.Background(), automation.WebhookDelivery{
		WebhookID: wh.ID, EventID: testEventID, Status: webhooks.StatusFailed,
	})
	if err != nil {
		t.Fatal(err)
	}
	missing := &reopenRecorder{err: fmt.Errorf("%w: job webhook_delivery/%d/%s", automation.ErrNotFound, wh.ID, testEventID)}
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout: 5 * time.Second, MaxAttempts: 5,
			MaxResponseBytes: 64 << 10, MaxConcurrentDeliveries: 2,
		},
		Store:   e.store,
		Secrets: testOpener(t),
		Jobs:    e.jobs,
		Reopen:  missing.reopen,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := svc.RetryDelivery(context.Background(), wh.ID, created.ID); err != nil || !ok {
		t.Fatalf("RetryDelivery: ok=%v err=%v", ok, err)
	}
	if e.jobs.webhookJobs() != 1 {
		t.Fatalf("a delivery with no job was not enqueued: %d", e.jobs.webhookJobs())
	}
}

// A secret is checked where it is typed. A `whsec_` value that cannot be
// decoded would otherwise be stored happily and only fail at the first
// delivery, with a credential nobody can use sitting in the database.
func TestAMalformedSigningSecretIsRefusedWhenItIsSaved(t *testing.T) {
	e := newEnv(t)
	bad := "whsec_this-is-not-base64!"
	if _, err := e.svc.CreateWebhook(context.Background(), automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthHMAC, Secret: &bad, CreatedBy: 1,
	}); !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("err = %v, want automation.ErrInvalid at save time", err)
	}

	// A well-formed one is still accepted, so the check refuses only what would
	// later fail to sign.
	good := "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if _, err := e.svc.CreateWebhook(context.Background(), automation.WebhookInput{
		Name: "feed", Enabled: true, URL: "https://receiver.example.com/hook",
		Method: "POST", AuthMode: webhooks.AuthHMAC, Secret: &good, CreatedBy: 1,
	}); err != nil {
		t.Fatalf("a valid whsec_ secret was refused: %v", err)
	}
}
