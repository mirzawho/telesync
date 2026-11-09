package webhooks_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/webhooks"
)

// A real delivery to a real loopback server.
//
// Everything else in this package proves the policy with a scripted transport.
// These do not: they bind a port, build the production client with its
// resolve-once dialler, and let a real TCP connection happen. That is what
// makes "localhost works when it is enabled" a fact about delivery and not
// only about the rules that decide it.

// deliveryJob is one queued delivery of the recorded event.
func deliveryJob(webhookID int64, attempt, max int) automation.Job {
	return automation.Job{
		ID:          1,
		EventID:     testEventID,
		Kind:        automation.JobWebhookDelivery,
		TargetID:    webhookID,
		Attempt:     attempt,
		MaxAttempts: max,
	}
}

// loopbackTarget is the address a test server is listening on, as the
// exception's own spelling of it: `http://127.0.0.1:PORT/hook`.
func loopbackTarget(srv *httptest.Server) string {
	return srv.URL + "/hook"
}

// localEnv builds the delivery engine against the production client — no
// scripted transport — and returns the store so a test can read what was
// recorded.
func localEnv(t *testing.T, allowLocalhost bool) (*webhooks.Service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	events := map[string]automation.MessageEvent{testEventID: messageEvent(nil)}
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: "https://telesync.example.com",
		BasePath:      "/admin",
		Webhooks: appconfig.WebhookConfig{
			Timeout:                 5 * time.Second,
			MaxAttempts:             5,
			MaxResponseBytes:        64 << 10,
			MaxConcurrentDeliveries: 2,
			AllowLocalhost:          allowLocalhost,
		},
		Store:   store,
		Secrets: testOpener(t),
		Events: func(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
			ev, ok := events[id]
			return ev, ok, nil
		},
		Jobs: &fakeEnqueuer{},
		// No Transport: the real one, with its resolve-once dialler.
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, store
}

func localWebhook(t *testing.T, store *fakeStore, url string, allowLocalhost bool) automation.Webhook {
	t.Helper()
	secret := testSecret(t, testSecretText)
	saved, err := store.CreateWebhook(context.Background(), 1, automation.Webhook{
		Name:           "Local Development Hook",
		Enabled:        true,
		URL:            url,
		Method:         "POST",
		AuthMode:       webhooks.AuthBearer,
		Timeout:        5 * time.Second,
		MaxAttempts:    5,
		AllowLocalhost: allowLocalhost,
	}, &secret)
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	return saved
}

func TestALocalhostWebhookIsDeliveredToALoopbackServer(t *testing.T) {
	var mu sync.Mutex
	var method, path, auth, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		method, path, auth, body = r.Method, r.URL.Path, r.Header.Get("Authorization"), string(raw)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("thanks"))
	}))
	defer srv.Close()

	svc, store := localEnv(t, true)
	wh := localWebhook(t, store, loopbackTarget(srv), true)

	if err := svc.Handler()(context.Background(), deliveryJob(wh.ID, 1, 5)); err != nil {
		t.Fatalf("deliver to a permitted loopback target: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if method != http.MethodPost || path != "/hook" {
		t.Fatalf("the receiver got %s %s, want POST /hook", method, path)
	}
	if auth == "" {
		t.Fatal("the bearer token did not reach the receiver")
	}
	if !strings.Contains(body, testEventID) {
		t.Fatalf("the payload never arrived: %q", body)
	}

	// The result of a real request is what the history keeps.
	deliveries, _ := store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	if len(deliveries.Deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries.Deliveries))
	}
	attempts, _ := store.WebhookAttempts(context.Background(), deliveries.Deliveries[0].ID)
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(attempts))
	}
	att := attempts[0]
	if att.ResponseStatus != http.StatusAccepted {
		t.Fatalf("stored status = %d, want %d", att.ResponseStatus, http.StatusAccepted)
	}
	if att.ResponseBody != "thanks" {
		t.Fatalf("stored body = %q, want %q", att.ResponseBody, "thanks")
	}
	if deliveries.Deliveries[0].Status != webhooks.StatusSucceeded {
		t.Fatalf("delivery status = %q, want %q", deliveries.Deliveries[0].Status, webhooks.StatusSucceeded)
	}
}

func TestALocalhostWebhookIsRefusedWhenTheDeploymentForbidsIt(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	defer srv.Close()

	// The webhook asked for the exception; the deployment did not grant it.
	svc, store := localEnv(t, false)
	wh := localWebhook(t, store, loopbackTarget(srv), true)

	if err := svc.Handler()(context.Background(), deliveryJob(wh.ID, 1, 5)); err == nil {
		t.Fatal("a delivery the policy refuses must report the refusal")
	}
	if reached {
		t.Fatal("the receiver was contacted although the policy refused it")
	}
}

func TestAWebhookThatDidNotAskForLocalhostIsRefused(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	defer srv.Close()

	// The deployment permits loopback; this webhook did not ask for it.
	svc, store := localEnv(t, true)
	wh := localWebhook(t, store, loopbackTarget(srv), false)

	if err := svc.Handler()(context.Background(), deliveryJob(wh.ID, 1, 5)); err == nil {
		t.Fatal("a delivery the policy refuses must report the refusal")
	}
	if reached {
		t.Fatal("the receiver was contacted although this webhook never asked for the exception")
	}
}

func TestAPolicyRefusalIsRecordedAndNotRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	svc, store := localEnv(t, true)
	wh := localWebhook(t, store, loopbackTarget(srv), false)

	// The queue's own bound is generous; what must not happen is the attempt
	// being counted as one worth repeating.
	_ = svc.Handler()(context.Background(), deliveryJob(wh.ID, 1, 5))

	deliveries, _ := store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	if len(deliveries.Deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries.Deliveries))
	}
	d := deliveries.Deliveries[0]
	if d.Status != webhooks.StatusFailed {
		t.Fatalf("delivery status = %q, want %q", d.Status, webhooks.StatusFailed)
	}
	attempts, _ := store.WebhookAttempts(context.Background(), d.ID)
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1: a policy refusal is not a transient failure", len(attempts))
	}
	if d.NextAttemptAt != nil {
		t.Fatal("a configuration refusal scheduled another attempt: it is not a transient failure")
	}
	if attempts[0].ErrorCode != "invalid_target" {
		t.Fatalf("error code = %q, want invalid_target", attempts[0].ErrorCode)
	}
}

func TestAClosedLoopbackPortIsRecordedAsAConnectionFailure(t *testing.T) {
	// Nothing is listening. The request really goes out and really fails, and
	// the failure is what the history keeps.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	target := loopbackTarget(srv)
	srv.Close()

	svc, store := localEnv(t, true)
	wh := localWebhook(t, store, target, true)

	_ = svc.Handler()(context.Background(), deliveryJob(wh.ID, 1, 5))

	deliveries, _ := store.ListWebhookDeliveries(context.Background(), wh.ID, automation.DeliveryListOptions{})
	if len(deliveries.Deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries.Deliveries))
	}
	d := deliveries.Deliveries[0]
	// Nothing refused this request and nothing was wrong with its
	// configuration: the receiver was not listening. That is an ordinary
	// network failure, so the delivery stays live and the existing retry
	// policy decides when to try again.
	if d.Status != webhooks.StatusPending {
		t.Fatalf("delivery status = %q, want %q", d.Status, webhooks.StatusPending)
	}
	if d.NextAttemptAt == nil {
		t.Fatal("a connection failure must be retried on the ordinary policy")
	}
	attempts, _ := store.WebhookAttempts(context.Background(), d.ID)
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(attempts))
	}
	if attempts[0].ResponseStatus != 0 {
		t.Fatalf("a connection that never opened reported status %d", attempts[0].ResponseStatus)
	}
	if strings.TrimSpace(attempts[0].ErrorMessage) == "" {
		t.Fatal("a connection failure must say what failed")
	}
	// And the policy did not decide this one: the receiver was reachable and
	// refused nothing.
	if attempts[0].ErrorCode == "invalid_target" {
		t.Fatalf("a network failure was recorded as a policy refusal: %s", attempts[0].ErrorMessage)
	}
	_ = time.Second
}

func TestTheTestActionUsesTheSameDestinationPolicy(t *testing.T) {
	// The manual "send a test" action goes through the same validator as
	// delivery, so a webhook the policy refuses cannot be probed by hand.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	svc, store := localEnv(t, true)
	wh := localWebhook(t, store, loopbackTarget(srv), false)

	if _, ok, err := svc.TestWebhook(context.Background(), wh.ID); err == nil && ok {
		t.Fatal("a test send must be refused for a webhook the policy refuses")
	}
}
