package webhooks_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/webhooktest"
)

// The delivery engine against a receiver of the same build.
//
// This is the whole chain the test receiver exists to close: the engine signs
// and sends, the receiver verifies and echoes, and the engine records what came
// back in delivery history. Both halves are production code — the sender is the
// one the daemon runs and the receiver is the one `telesync serve-webhook`
// starts — so a pass here is a statement about the wire and not about a fixture.

// testReceiver is the local receiver the delivery engine talks to.
type testReceiver struct {
	web    *httptest.Server
	secret string
}

func newTestReceiver(t *testing.T) *testReceiver {
	t.Helper()
	auth := webhooktest.Auth{
		HMACSecret:   "test-hmac-secret",
		BearerToken:  "test-bearer-token",
		HeaderName:   "X-API-Key",
		HeaderSecret: "test-header-secret",
	}
	srv, err := webhooktest.New(webhooktest.Options{Host: "127.0.0.1", Log: io.Discard}, auth)
	if err != nil {
		t.Fatalf("webhooktest.New: %v", err)
	}
	web := httptest.NewServer(srv.Handler())
	t.Cleanup(web.Close)
	return &testReceiver{web: web, secret: auth.HMACSecret}
}

// engine is one delivery engine wired to a fake store and the local receiver.
type engine struct {
	svc   *webhooks.Service
	store *fakeStore
	hook  *testReceiver
}

func newEngine(t *testing.T, hook *testReceiver) *engine {
	t.Helper()
	store := newFakeStore()
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout:                 5 * time.Second,
			MaxAttempts:             5,
			MaxResponseBytes:        64 << 10,
			MaxConcurrentDeliveries: 1,
		},
		Store:   store,
		Secrets: testOpener(t),
		// The loopback exception, the way a deployment enables it: the
		// deployment permits it here and the webhook asks for it below. Every
		// other address rule is left exactly as production runs it.
		Policy: webhooks.AllowLocalhost(),
		Events: func(_ context.Context, _ string) (automation.MessageEvent, bool, error) {
			return messageEvent(nil), true, nil
		},
	})
	if err != nil {
		t.Fatalf("webhooks.New: %v", err)
	}
	return &engine{svc: svc, store: store, hook: hook}
}

// addWebhook stores one webhook pointed at the receiver, carrying the secret
// the receiver was configured with — or another one, to test a mismatch.
func (e *engine) addWebhook(t *testing.T, mode, secret string) int64 {
	t.Helper()
	header := ""
	if mode == webhooks.AuthHeader {
		header = "X-API-Key"
	}
	rec := automation.Webhook{
		Name:       "receiver",
		Enabled:    true,
		URL:        e.hook.web.URL + "/webhook/" + mode,
		Method:     "POST",
		AuthMode:   mode,
		AuthHeader: header,
		Timeout:    5 * time.Second,
		// The webhook's half of the loopback exception.
		AllowLocalhost: true,
		MaxAttempts:    5,
	}
	created, err := e.store.CreateWebhook(context.Background(), 1, rec, boxOf(t, secret))
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	return created.ID
}

func boxOf(t *testing.T, secret string) *secrets.Box {
	t.Helper()
	box := testSecret(t, secret)
	return &box
}

func TestTheDeliveryEngineSendsWhatTheTestReceiverAccepts(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		secret string
	}{
		{"hmac", string(webhooktest.AuthHMAC), "test-hmac-secret"},
		{"bearer", string(webhooktest.AuthBearer), "test-bearer-token"},
		{"header", string(webhooktest.AuthHeader), "test-header-secret"},
		{"none", string(webhooktest.AuthNone), ""},
	}

	hook := newTestReceiver(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t, hook)
			id := e.addWebhook(t, tc.mode, tc.secret)

			res, ok, err := e.svc.TestWebhook(context.Background(), id)
			if err != nil || !ok {
				t.Fatalf("TestWebhook: ok=%v err=%v", ok, err)
			}
			if len(res.Delivery.Attempts) != 1 {
				t.Fatalf("%d attempts recorded", len(res.Delivery.Attempts))
			}
			att := res.Delivery.Attempts[0]
			if att.ResponseStatus != 200 {
				t.Fatalf("the receiver answered %d: %s", att.ResponseStatus, att.ResponseBody)
			}
			if res.Delivery.Status != webhooks.StatusSucceeded {
				t.Fatalf("delivery %s: %s", res.Delivery.Status, att.ErrorMessage)
			}
			// The receiver echoed the request it verified, so the payload the
			// engine signed is the payload that came back.
			if !strings.Contains(att.ResponseBody, `"success":true`) {
				t.Fatalf("the receiver did not report a verified request: %s", att.ResponseBody)
			}
			if !strings.Contains(att.ResponseBody, "specversion") {
				t.Fatalf("the echo carries no request: %s", att.ResponseBody)
			}
		})
	}
}

func TestTheDeliveryEngineIsRefusedWhenTheSecretDoesNotMatch(t *testing.T) {
	hook := newTestReceiver(t)
	for _, tc := range []struct {
		name   string
		mode   string
		secret string
	}{
		{"hmac", string(webhooktest.AuthHMAC), "not-the-hmac-secret"},
		{"bearer", string(webhooktest.AuthBearer), "not-the-bearer-token"},
		{"header", string(webhooktest.AuthHeader), "not-the-header-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t, hook)
			id := e.addWebhook(t, tc.mode, tc.secret)

			res, ok, err := e.svc.TestWebhook(context.Background(), id)
			if err != nil || !ok {
				t.Fatalf("TestWebhook: ok=%v err=%v", ok, err)
			}
			att := res.Delivery.Attempts[0]
			if att.ResponseStatus != 401 {
				t.Fatalf("the receiver answered %d, want 401: %s", att.ResponseStatus, att.ResponseBody)
			}
			if res.Delivery.Status != webhooks.StatusFailed {
				t.Fatalf("delivery %s, want %s", res.Delivery.Status, webhooks.StatusFailed)
			}
		})
	}
}

func TestADeliveryIsRecordedAgainstTheReceiverThatAnsweredIt(t *testing.T) {
	hook := newTestReceiver(t)
	e := newEngine(t, hook)
	id := e.addWebhook(t, string(webhooktest.AuthHMAC), hook.secret)
	event := messageEvent(nil)

	job := automation.Job{
		Kind:        automation.JobWebhookDelivery,
		TargetID:    id,
		EventID:     event.ID,
		Attempt:     1,
		MaxAttempts: 5,
		Owner:       automation.Owner{OwnerUserID: testOwnerUserID, AccountID: testAccountID},
	}
	if err := e.svc.Handler()(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	delivery, found, err := e.store.WebhookDeliveryForEvent(context.Background(), id, event.ID)
	if err != nil || !found {
		t.Fatalf("WebhookDeliveryForEvent: found=%v err=%v", found, err)
	}
	if delivery.Status != webhooks.StatusSucceeded {
		t.Fatalf("delivery %s: %s", delivery.Status, delivery.LastError)
	}
	if delivery.LastHTTPStatus != 200 {
		t.Fatalf("LastHTTPStatus = %d", delivery.LastHTTPStatus)
	}

	attempts, err := e.store.WebhookAttempts(context.Background(), delivery.ID)
	if err != nil {
		t.Fatalf("WebhookAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("%d attempts recorded", len(attempts))
	}
	att := attempts[0]
	if att.RequestURL == "" || att.RequestMethod != "POST" {
		t.Fatalf("the attempt records no request: %+v", att)
	}
	if !strings.Contains(att.ResponseBody, `"success":true`) {
		t.Fatalf("the attempt records no verified echo: %s", att.ResponseBody)
	}

	// The body the engine signed is the body the receiver echoed, byte for
	// byte: nothing re-serialized the payload between the two.
	var echoed map[string]any
	if err := json.Unmarshal([]byte(att.ResponseBody), &echoed); err != nil {
		t.Fatalf("the recorded echo is not JSON: %v", err)
	}
	request, _ := echoed["request"].(map[string]any)
	if request["body_text"] != att.RequestBody {
		t.Fatalf("echoed body = %v, want %q", request["body_text"], att.RequestBody)
	}
}
