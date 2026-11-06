package automation_test

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/events"
	"github.com/mirza/telesync/internal/automation/queue"
	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/secrets"
)

// The tests in this file cross package boundaries on purpose.
//
// Each package proves its own behaviour, and that is not the same as proving
// the pipeline works: that an observed message becomes a stored event, that the
// event becomes queued work, that the work produces a real HTTP request and a
// persisted record of the exchange, and that doing it twice changes nothing.
// That whole path is what an operator experiences as "automation".

// receiver stands in for the endpoint a webhook posts to. Everything up to the
// socket is real: the request is built, signed and dispatched by the production
// code, and the attempt record is written by it.
type receiver struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	status   int
	body     string
}

func (r *receiver) RoundTrip(req *http.Request) (*http.Response, error) {
	payload, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.bodies = append(r.bodies, string(payload))
	status, body := r.status, r.body
	r.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	if body == "" {
		body = `{"received":true}`
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

type pipeline struct {
	t        *testing.T
	store    *channelstore.Store
	svc      *webhooks.Service
	runner   *queue.Runner
	enqueuer *queue.Enqueuer
	ingest   *events.Ingestor
	recv     *receiver
	secret   *secrets.Encrypter
	channel  channelstore.Channel
	owner    int64
}

func newPipeline(t *testing.T) *pipeline {
	t.Helper()
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state", "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	channel, err := store.Upsert(context.Background(), channelstore.Channel{
		ClientKey: "official", ChannelID: 123456789, PeerType: channelstore.PeerChannel,
		Title: "Source", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	hash, err := auth.HashPassword("PipelinePass123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := store.CreateUser(context.Background(), "Pipeline Owner", "pipeline-owner", hash, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	box, err := secrets.NewEncrypter([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("encrypter: %v", err)
	}

	recv := &receiver{status: http.StatusOK}
	enqueuer := queue.NewEnqueuer(store, 3)
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout: 5 * time.Second, MaxAttempts: 3,
			MaxResponseBytes: 64 << 10, MaxConcurrentDeliveries: 2,
		},
		Store:     store,
		Secrets:   box,
		Events:    store.AutomationEvent,
		Jobs:      enqueuer,
		Reopen:    enqueuer.Reopen,
		Transport: recv,
	})
	if err != nil {
		t.Fatalf("webhook service: %v", err)
	}

	runner := queue.New(store, automation.JobHandlers{
		automation.JobWebhookDelivery: svc.Handler(),
	}, queue.Config{Workers: 2, PollInterval: time.Millisecond, MaxAttempts: 3},
		queue.WithClock(time.Now), queue.WithWorkerName("pipeline"))

	return &pipeline{
		t: t, store: store, svc: svc, runner: runner, enqueuer: enqueuer,
		ingest: events.NewIngestor(store, enqueuer, store, store, store),
		recv:   recv, secret: box, channel: channel, owner: user.ID,
	}
}

// observe records one message the way the update listener will: a normalized
// source, through the real ingestor.
func (p *pipeline) observe(messageID int) automation.MessageEvent {
	p.t.Helper()
	ev := events.Source{
		ClientKey: "official", NetworkKey: "official",
		ManagedChannelID:  p.channel.ID,
		TelegramChannelID: p.channel.ChannelID,
		Message: rpc.Message{
			ID: messageID, Date: time.Now().UTC(), Text: "hello from the channel",
			Media:     &rpc.Media{ID: "123456789:1", Type: "photo", Size: 1024},
			Reactions: []rpc.Reaction{}, Entities: []rpc.Entity{},
		},
		ReceivedAt: time.Now().UTC(),
	}.Event()
	if _, err := p.ingest.Ingest(context.Background(), ev); err != nil {
		p.t.Fatalf("ingest: %v", err)
	}
	return ev
}

// drain runs every queued job the way the background runner would.
func (p *pipeline) drain() {
	p.t.Helper()
	for {
		job, ok, err := p.store.ClaimAutomationJob(context.Background(),
			channelstore.ClaimJobRequest{Worker: "pipeline", Lease: time.Minute})
		if err != nil {
			p.t.Fatalf("claim: %v", err)
		}
		if !ok {
			return
		}
		if err := p.runner.Execute(context.Background(), "pipeline", job); err != nil {
			p.t.Fatalf("execute job %d: %v", job.ID, err)
		}
	}
}

func (p *pipeline) webhook() automation.Webhook {
	p.t.Helper()
	secret := "whsec_" + "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	created, err := p.svc.CreateWebhook(context.Background(), automation.WebhookInput{
		Name: "pipeline", Enabled: true,
		URL: "https://receiver.example.com/hook", Method: "POST",
		AuthMode: webhooks.AuthHMAC, Secret: &secret,
		SourceChannelIDs: []int64{p.channel.ID}, CreatedBy: p.owner,
	})
	if err != nil {
		p.t.Fatalf("create webhook: %v", err)
	}
	return created
}

// Scenario B: an observed message reaches a receiver and the exchange is
// recorded. Nothing about this is simulated except the far side of the socket.
func TestAnObservedMessageDeliversToTheWebhookAndRecordsTheExchange(t *testing.T) {
	p := newPipeline(t)
	hook := p.webhook()

	ev := p.observe(41)
	p.drain()

	if p.recv.count() != 1 {
		t.Fatalf("the receiver saw %d requests, want exactly 1", p.recv.count())
	}
	req := p.recv.requests[0]
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.URL.String() != "https://receiver.example.com/hook" {
		t.Fatalf("url = %s", req.URL)
	}
	for _, h := range []string{"Webhook-Id", "Webhook-Timestamp", "Webhook-Signature"} {
		if req.Header.Get(h) == "" {
			t.Fatalf("the signed request is missing %s", h)
		}
	}

	// The body is the event envelope, and it carries media as a link, never
	// bytes.
	body := p.recv.bodies[0]
	for _, want := range []string{`"specversion"`, `"telegram.message.created"`, `"url"`, `"hello from the channel"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the envelope is missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "data:media") || strings.Contains(body, "AAAA") {
		t.Fatal("media bytes were embedded in the webhook payload")
	}

	page, err := p.svc.ListDeliveries(context.Background(), hook.ID, automation.DeliveryListOptions{})
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("deliveries = %d, want one", page.Total)
	}
	full, found, err := p.svc.GetDelivery(context.Background(), hook.ID, page.Deliveries[0].ID)
	if err != nil || !found {
		t.Fatalf("get delivery: found=%v err=%v", found, err)
	}
	if full.EventID != ev.ID {
		t.Fatalf("delivery %q is not for event %q", full.EventID, ev.ID)
	}
	if full.Status != webhooks.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", full.Status)
	}
	if len(full.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(full.Attempts))
	}
	attempt := full.Attempts[0]
	if attempt.ResponseStatus != http.StatusOK {
		t.Fatalf("recorded response status = %d, want 200", attempt.ResponseStatus)
	}
	if attempt.RequestBody == "" || attempt.ResponseBody == "" {
		t.Fatal("the exchange was not recorded in full")
	}
}

// Idempotency is what makes at-least-once delivery safe: replaying the same
// observation must not produce a second delivery.
func TestReplayingAnObservationDoesNotDeliverTwice(t *testing.T) {
	p := newPipeline(t)
	hook := p.webhook()

	p.observe(42)
	p.drain()
	// The same message arrives again — a duplicate update, a replay after a
	// crash, or a second account on the same network.
	p.observe(42)
	p.drain()

	if p.recv.count() != 1 {
		t.Fatalf("the receiver saw %d requests, want 1", p.recv.count())
	}
	page, _ := p.svc.ListDeliveries(context.Background(), hook.ID, automation.DeliveryListOptions{})
	if page.Total != 1 {
		t.Fatalf("deliveries = %d, want 1", page.Total)
	}
	jobs, err := p.store.AutomationJobs(context.Background(), channelstore.AutomationJobQuery{
		Kind: automation.JobWebhookDelivery,
	})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("queued %d jobs, want 1", len(jobs))
	}
}

// A message this installation published must never come back through the
// pipeline, or a workflow that publishes into a monitored channel feeds itself.
func TestOurOwnPublicationNeverReachesThePipeline(t *testing.T) {
	p := newPipeline(t)
	p.webhook()

	if err := p.store.MarkPublished(context.Background(), automation.OutboundMessage{
		NetworkKey: "official", ClientKey: "official",
		ChannelID: p.channel.ChannelID, RemoteMessageID: 7,
		Origin: "workflow", OriginID: 1,
	}); err != nil {
		t.Fatalf("mark published: %v", err)
	}

	p.observe(7)
	p.drain()

	if p.recv.count() != 0 {
		t.Fatalf("our own message was delivered %d time(s)", p.recv.count())
	}
	page, _ := p.svc.ListDeliveries(context.Background(), 1, automation.DeliveryListOptions{})
	if page.Total != 0 {
		t.Fatalf("deliveries = %d, want none for our own message", page.Total)
	}
}

// A failure is not the end: the attempt is recorded and the work comes back.
func TestAFailedDeliveryIsRecordedAndRetried(t *testing.T) {
	p := newPipeline(t)
	p.recv.status = http.StatusInternalServerError
	hook := p.webhook()

	p.observe(43)
	p.drain()

	page, _ := p.svc.ListDeliveries(context.Background(), hook.ID, automation.DeliveryListOptions{})
	if page.Total != 1 {
		t.Fatalf("deliveries = %d, want one", page.Total)
	}
	full, _, _ := p.svc.GetDelivery(context.Background(), hook.ID, page.Deliveries[0].ID)
	if len(full.Attempts) != 1 {
		t.Fatalf("attempts = %d, want the failure to have been recorded", len(full.Attempts))
	}
	if full.Attempts[0].ResponseStatus != http.StatusInternalServerError {
		t.Fatalf("recorded status = %d, want 500", full.Attempts[0].ResponseStatus)
	}
	if full.Attempts[0].ResponseBody == "" {
		t.Fatal("the failure recorded no response")
	}

	// And a later attempt extends the same delivery rather than creating a
	// second one.
	p.recv.status = http.StatusOK
	p.drain()
	page, _ = p.svc.ListDeliveries(context.Background(), hook.ID, automation.DeliveryListOptions{})
	if page.Total != 1 {
		t.Fatalf("after the retry there are %d deliveries, want still 1", page.Total)
	}
}
