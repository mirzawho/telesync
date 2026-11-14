package channelstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/secrets"
)

// These tests reach the database handle directly because webhook_deliveries
// references automation_events, which is another workstream's table: inserting
// one here would mean writing its store. The rows are inserted as raw schema,
// which is exactly what the foreign key sees.

func testStore(t *testing.T) *Store {
	t.Helper()
	return openInternal(t)
}

type webhookTestIdentity struct {
	ownerID   int64
	accountID string
}

func testIdentity(t *testing.T, s *Store) webhookTestIdentity {
	return testIdentityNamed(t, s, "Operator", 101)
}

func testIdentityNamed(t *testing.T, s *Store, username string, remoteUserID int64) webhookTestIdentity {
	t.Helper()
	u, err := s.CreateUser(context.Background(), username, username, "hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	id, err := accountid.New()
	if err != nil {
		t.Fatalf("accountid.New: %v", err)
	}
	if _, err := s.Create(context.Background(), Account{
		ID: id, OwnerUserID: u.ID, ServerKey: "client-a",
		NetworkType: NetworkTelegram, RemoteUserID: remoteUserID,
		Status: AccountStatusConnected,
	}); err != nil {
		t.Fatalf("Create account: %v", err)
	}
	return webhookTestIdentity{ownerID: u.ID, accountID: id}
}

func testChannel(t *testing.T, s *Store, identity webhookTestIdentity) int64 {
	t.Helper()
	ch, err := s.UpsertFor(context.Background(), identity.accountID, Channel{
		ClientKey:        "client-a",
		ChannelID:        42,
		PeerType:         PeerChannel,
		Title:            "Source",
		MembershipStatus: StatusMember,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return ch.ID
}

func testEvent(t *testing.T, s *Store, identity webhookTestIdentity, managedChannelID int64, messageID int64) string {
	t.Helper()
	id := "evt_" + strings.Repeat("a", 8) + itoa(messageID)
	now := formatTime(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	if _, err := s.db.ExecContext(context.Background(), `
INSERT INTO automation_events (id, type, network_key, source_client_key, account_id,
	source_account_id, owner_user_id, managed_channel_id,
	telegram_channel_id, message_id, grouped_id, occurred_at, received_at, payload, created_at)
VALUES (?, 'telegram.message.created', 'official', 'client-a', ?, ?, ?, ?, -10042, ?, NULL, ?, ?, '{}', ?)`,
		id, identity.accountID, identity.accountID, identity.ownerID, managedChannelID,
		messageID, now, now, now); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return id
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func testSeal(t *testing.T, plaintext string) *secrets.Box {
	t.Helper()
	enc, err := secrets.NewEncrypter([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	box, err := enc.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return &box
}

// testWebhook creates one enabled webhook and returns its id.
func testWebhook(t *testing.T, s *Store, identity webhookTestIdentity) int64 {
	t.Helper()
	rec := automation.Webhook{
		Name:        "feed-" + identity.accountID,
		AccountID:   identity.accountID,
		Enabled:     true,
		URL:         "https://receiver.example.com/hook",
		Method:      "POST",
		AuthMode:    "hmac",
		Timeout:     15 * time.Second,
		MaxAttempts: 5,
	}
	saved, err := s.CreateWebhook(context.Background(), identity.ownerID, rec, testSeal(t, "whsec_abc"))
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	return saved.ID
}

func TestWebhookDeliveryIsOneRowPerWebhookAndEvent(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	identity := testIdentity(t, s)
	channelID := testChannel(t, s, identity)
	webhookID := testWebhook(t, s, identity)
	eventID := testEvent(t, s, identity, channelID, 1)

	rec := automation.WebhookDelivery{
		WebhookID: webhookID,
		EventID:   eventID,
		Status:    DeliveryPending,
	}
	created, err := s.CreateWebhookDelivery(ctx, rec)
	if err != nil {
		t.Fatalf("CreateWebhookDelivery: %v", err)
	}
	if created.ID == 0 || created.AttemptCount != 0 {
		t.Fatalf("created = %+v", created)
	}

	if _, err := s.CreateWebhookDelivery(ctx, rec); !errors.Is(err, ErrDuplicateDelivery) {
		t.Fatalf("second create err = %v, want ErrDuplicateDelivery", err)
	}

	found, ok, err := s.WebhookDeliveryForEvent(ctx, webhookID, eventID)
	if err != nil || !ok {
		t.Fatalf("WebhookDeliveryForEvent: ok=%v err=%v", ok, err)
	}
	if found.ID != created.ID {
		t.Fatalf("delivery id = %d, want %d", found.ID, created.ID)
	}
	if _, ok, _ := s.WebhookDeliveryForEvent(ctx, webhookID, "evt_missing"); ok {
		t.Fatal("an unknown event reported a delivery")
	}
}

func TestWebhookAttemptsAreAppendedOnceEachAndRedactedHeadersSurvive(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	identity := testIdentity(t, s)
	channelID := testChannel(t, s, identity)
	webhookID := testWebhook(t, s, identity)
	eventID := testEvent(t, s, identity, channelID, 1)

	delivery, err := s.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
		WebhookID: webhookID, EventID: eventID, Status: DeliveryDelivering,
	})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 11, 5, 12, 1, 0, 0, time.UTC)
	finished := started.Add(250 * time.Millisecond)
	att := automation.WebhookAttempt{
		AttemptNumber:        1,
		RequestMethod:        "POST",
		RequestURL:           "https://receiver.example.com/hook?token=%5Bredacted%5D",
		RequestHeaders:       map[string]string{"Authorization": "[redacted]", "Webhook-Id": "wbd_1"},
		RequestBody:          `{"specversion":"1.0"}`,
		RequestBodyTruncated: false,
		ResponseStatus:       503,
		ResponseHeaders:      map[string]string{"Content-Type": "text/plain", "Set-Cookie": "[redacted]"},
		ResponseBody:         "unavailable",
		ResponseBytes:        11,
		DurationMS:           250,
		ErrorCode:            "http_status",
		ErrorMessage:         "the receiver answered 503",
		StartedAt:            started,
		FinishedAt:           &finished,
	}
	saved, err := s.AppendWebhookAttempt(ctx, delivery.ID, att)
	if err != nil {
		t.Fatalf("AppendWebhookAttempt: %v", err)
	}
	if saved.ID == 0 {
		t.Fatal("no attempt id was assigned")
	}
	if _, err := s.AppendWebhookAttempt(ctx, delivery.ID, att); !errors.Is(err, ErrDuplicateAttempt) {
		t.Fatalf("second append err = %v, want ErrDuplicateAttempt", err)
	}

	// The stored attempt is exactly what was recorded, header map included.
	history, err := s.WebhookAttempts(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("attempts = %d", len(history))
	}
	got := history[0]
	if got.RequestHeaders["Authorization"] != "[redacted]" {
		t.Fatalf("request headers = %v", got.RequestHeaders)
	}
	if got.ResponseHeaders["Set-Cookie"] != "[redacted]" {
		t.Fatalf("response headers = %v", got.ResponseHeaders)
	}
	if got.RequestBody != `{"specversion":"1.0"}` || got.ResponseStatus != 503 {
		t.Fatalf("attempt = %+v", got)
	}
	if got.StartedAt.IsZero() || got.FinishedAt == nil {
		t.Fatalf("timestamps = %v / %v", got.StartedAt, got.FinishedAt)
	}

	latest, ok, err := s.LatestWebhookAttempt(ctx, delivery.ID)
	if err != nil || !ok {
		t.Fatalf("LatestWebhookAttempt: ok=%v err=%v", ok, err)
	}
	if latest.AttemptNumber != 1 {
		t.Fatalf("latest = %+v", latest)
	}
}

func TestWebhookDeliveryDetailCarriesItsRequestAndAttempts(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	identity := testIdentity(t, s)
	channelID := testChannel(t, s, identity)
	webhookID := testWebhook(t, s, identity)
	eventID := testEvent(t, s, identity, channelID, 1)

	delivery, err := s.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
		WebhookID: webhookID, EventID: eventID, Status: DeliveryDelivering,
	})
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		if _, err := s.AppendWebhookAttempt(ctx, delivery.ID, automation.WebhookAttempt{
			AttemptNumber:  n,
			RequestMethod:  "POST",
			RequestURL:     "https://receiver.example.com/hook",
			RequestHeaders: map[string]string{"Content-Type": "application/json"},
			RequestBody:    `{"n":` + itoa(int64(n)) + `}`,
			ResponseStatus: 200,
			StartedAt:      time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	detail, ok, err := s.GetWebhookDelivery(ctx, delivery.ID)
	if err != nil || !ok {
		t.Fatalf("GetWebhookDelivery: ok=%v err=%v", ok, err)
	}
	if len(detail.Attempts) != 2 {
		t.Fatalf("attempts = %d", len(detail.Attempts))
	}
	if detail.Request == nil {
		t.Fatal("Request is nil on a detail response")
	}
	// The request shown is the one last transmitted.
	if detail.Request.Body != `{"n":2}` {
		t.Fatalf("Request.Body = %q", detail.Request.Body)
	}
	if detail.Request.Method != "POST" || detail.Request.Headers["Content-Type"] != "application/json" {
		t.Fatalf("Request = %+v", detail.Request)
	}

	if _, ok, _ := s.GetWebhookDelivery(ctx, 9999); ok {
		t.Fatal("an unknown delivery was reported")
	}
}

func TestWebhookDeliveryStateIsUpdatedAndListed(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	identity := testIdentity(t, s)
	channelID := testChannel(t, s, identity)
	webhookID := testWebhook(t, s, identity)

	ids := []int64{}
	for i := int64(1); i <= 3; i++ {
		eventID := testEvent(t, s, identity, channelID, i)
		rec, err := s.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
			WebhookID: webhookID, EventID: eventID, Status: DeliveryPending,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rec.ID)
	}

	next := time.Date(2026, 11, 5, 13, 0, 0, 0, time.UTC)
	done := time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC)
	updated, ok, err := s.UpdateWebhookDelivery(ctx, automation.WebhookDelivery{
		ID:             ids[0],
		Status:         DeliveryFailed,
		AttemptCount:   3,
		NextAttemptAt:  &next,
		LastHTTPStatus: 404,
		LastError:      "the receiver refused the payload",
		CompletedAt:    &done,
	})
	if err != nil || !ok {
		t.Fatalf("UpdateWebhookDelivery: ok=%v err=%v", ok, err)
	}
	if updated.Status != DeliveryFailed || updated.AttemptCount != 3 || updated.LastHTTPStatus != 404 {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.NextAttemptAt == nil || !updated.NextAttemptAt.Equal(next) {
		t.Fatalf("NextAttemptAt = %v", updated.NextAttemptAt)
	}
	if updated.CompletedAt == nil || !updated.CompletedAt.Equal(done) {
		t.Fatalf("CompletedAt = %v", updated.CompletedAt)
	}
	if updated.CreatedAt.IsZero() {
		t.Fatal("CreatedAt was cleared by the update")
	}

	if _, ok, err := s.UpdateWebhookDelivery(ctx, automation.WebhookDelivery{ID: 4242, Status: DeliveryFailed}); !errors.Is(err, ErrDeliveryNotFound) || ok {
		t.Fatalf("update of a missing delivery: ok=%v err=%v, want ErrDeliveryNotFound", ok, err)
	}

	page, err := s.ListWebhookDeliveries(ctx, webhookID, automation.DeliveryListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Deliveries) != 3 {
		t.Fatalf("page = %+v", page)
	}
	page, err = s.ListWebhookDeliveries(ctx, webhookID, automation.DeliveryListOptions{Status: DeliveryFailed})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Deliveries[0].ID != ids[0] {
		t.Fatalf("filtered page = %+v", page)
	}
	page, err = s.ListWebhookDeliveries(ctx, webhookID, automation.DeliveryListOptions{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Deliveries) != 2 {
		t.Fatalf("paged = %+v", page)
	}
}

func TestDeletingAWebhookRemovesItsDeliveriesAndAttempts(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	identity := testIdentity(t, s)
	channelID := testChannel(t, s, identity)
	webhookID := testWebhook(t, s, identity)
	eventID := testEvent(t, s, identity, channelID, 1)

	delivery, err := s.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
		WebhookID: webhookID, EventID: eventID, Status: DeliveryDelivering,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendWebhookAttempt(ctx, delivery.ID, automation.WebhookAttempt{
		AttemptNumber: 1, RequestMethod: "POST", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if ok, err := s.DeleteWebhook(ctx, webhookID); err != nil || !ok {
		t.Fatalf("DeleteWebhook: ok=%v err=%v", ok, err)
	}
	var deliveries, attempts int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries`).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_attempts`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 || attempts != 0 {
		t.Fatalf("after delete: deliveries=%d attempts=%d", deliveries, attempts)
	}
}

func TestWebhookDeliveryRefusesAnUnknownStatusOrANonPositiveAttempt(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	identity := testIdentity(t, s)
	channelID := testChannel(t, s, identity)
	webhookID := testWebhook(t, s, identity)
	eventID := testEvent(t, s, identity, channelID, 1)

	if _, err := s.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
		WebhookID: webhookID, EventID: eventID, Status: "exploded",
	}); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("err = %v, want ErrInvalidDelivery", err)
	}
	rec, err := s.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
		WebhookID: webhookID, EventID: eventID, Status: DeliveryPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendWebhookAttempt(ctx, rec.ID, automation.WebhookAttempt{AttemptNumber: 0}); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("err = %v, want ErrInvalidDelivery", err)
	}
	if _, err := s.AppendWebhookAttempt(ctx, rec.ID, automation.WebhookAttempt{AttemptNumber: maxDeliveryAttempts + 1}); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("err = %v, want ErrInvalidDelivery", err)
	}
}

func TestWebhookDeliveryRejectsCrossAccountAndCrossUserMismatches(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	sourceIdentity := testIdentityNamed(t, s, "source-owner", 101)
	otherOwnerAccountID, err := accountid.New()
	if err != nil {
		t.Fatalf("accountid.New: %v", err)
	}
	if _, err := s.Create(ctx, Account{
		ID: otherOwnerAccountID, OwnerUserID: sourceIdentity.ownerID, ServerKey: "client-a",
		NetworkType: NetworkTelegram, RemoteUserID: 202,
		Status: AccountStatusConnected,
	}); err != nil {
		t.Fatalf("Create second same-owner account: %v", err)
	}
	sameOwnerDifferentAccount := webhookTestIdentity{
		ownerID: sourceIdentity.ownerID, accountID: otherOwnerAccountID,
	}
	foreignUserIdentity := testIdentityNamed(t, s, "foreign-owner", 303)
	channelID := testChannel(t, s, sourceIdentity)
	eventID := testEvent(t, s, sourceIdentity, channelID, 1)

	for _, identity := range []webhookTestIdentity{sameOwnerDifferentAccount, foreignUserIdentity} {
		webhookID := testWebhook(t, s, identity)
		_, err := s.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
			WebhookID: webhookID, EventID: eventID, Status: DeliveryPending,
		})
		if !errors.Is(err, ErrInvalidDelivery) {
			t.Fatalf("CreateWebhookDelivery for owner=%d account=%q error = %v, want ErrInvalidDelivery",
				identity.ownerID, identity.accountID, err)
		}
	}
}
