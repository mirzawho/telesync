package channelstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/secrets"
)

// webhook is a valid webhook record with no sources and no secret.
func webhook(name, rawURL string) automation.Webhook {
	return automation.Webhook{
		Name:        name,
		Description: "posts the channel feed",
		Enabled:     true,
		URL:         rawURL,
		Method:      "POST",
		AuthMode:    "hmac",
		Timeout:     15 * time.Second,
		MaxAttempts: 5,
	}
}

// sourceChannel creates one legacy unbound managed channel for tests that
// exercise accountless behavior.
func sourceChannel(t *testing.T, store *channelstore.Store, key string, remoteID int64) int64 {
	t.Helper()
	rec := channel(key, remoteID)
	saved, err := store.Upsert(context.Background(), rec)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return saved.ID
}

// ownedSourceChannel creates a managed channel bound to the selected account.
func ownedSourceChannel(t *testing.T, store *channelstore.Store, accountID, key string, remoteID int64) int64 {
	t.Helper()
	rec := channel(key, remoteID)
	saved, err := store.UpsertFor(context.Background(), accountID, rec)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return saved.ID
}

// sealedSecret seals a webhook secret exactly as the service does.
func sealedSecret(t *testing.T, plaintext string) *secrets.Box {
	t.Helper()
	box := testBox(t, plaintext)
	return &box
}

func TestWebhookRoundTripKeepsTheSecretOutOfEveryRead(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "operator")
	accountID := ownerAccount(t, store, user.ID, "client-a", channelstore.NetworkTelegram, 101)
	channelID := ownedSourceChannel(t, store, accountID, "client-a", 100)

	rec := webhook("feed", "https://receiver.example.com/hook")
	rec.AccountID = accountID
	rec.SourceChannelIDs = []int64{channelID}
	secret := sealedSecret(t, "whsec_aaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	created, err := store.CreateWebhook(ctx, user.ID, rec, secret)
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("no id was assigned")
	}
	if !created.HasSecret {
		t.Fatal("HasSecret = false after creating a webhook with a secret")
	}

	got, found, err := store.GetWebhook(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("GetWebhook: found=%v err=%v", found, err)
	}
	if !got.HasSecret {
		t.Fatal("HasSecret = false on read")
	}
	// The read shape carries no secret material at all: only the flag.
	if got.SourceChannelIDs == nil || len(got.SourceChannelIDs) != 1 || got.SourceChannelIDs[0] != channelID {
		t.Fatalf("SourceChannelIDs = %v", got.SourceChannelIDs)
	}
	if got.Method != "POST" || got.AuthMode != "hmac" {
		t.Fatalf("method/auth = %q/%q", got.Method, got.AuthMode)
	}
	if got.Timeout != 15*time.Second || got.MaxAttempts != 5 {
		t.Fatalf("timeout/attempts = %v/%d", got.Timeout, got.MaxAttempts)
	}

	// The sealed box is only reachable through the backend-only accessor, and it
	// opens to exactly what was written.
	box, found, err := store.WebhookSecretBox(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("WebhookSecretBox: found=%v err=%v", found, err)
	}
	if box.Plaintext() {
		t.Fatal("the stored secret was returned in the clear")
	}
	enc, err := secrets.NewEncrypter([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := enc.Open(box)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if plain != "whsec_aaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatal("the sealed secret does not open to the original")
	}
}

func TestWebhookUpdateWithoutASecretKeepsTheStoredOne(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "operator")

	rec := webhook("feed", "https://receiver.example.com/hook")
	created, err := store.CreateWebhook(ctx, user.ID, rec, sealedSecret(t, "whsec_unchanged"))
	if err != nil {
		t.Fatal(err)
	}

	edited := created
	edited.Name = "renamed"
	saved, found, err := store.UpdateWebhook(ctx, edited, nil)
	if err != nil || !found {
		t.Fatalf("UpdateWebhook: found=%v err=%v", found, err)
	}
	if saved.Name != "renamed" {
		t.Fatalf("Name = %q", saved.Name)
	}
	if !saved.HasSecret {
		t.Fatal("HasSecret = false after an update that kept the secret")
	}
	box, ok, err := store.WebhookSecretBox(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("WebhookSecretBox: %v", err)
	}
	enc, err := secrets.NewEncrypter([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := enc.Open(box); err != nil || plain != "whsec_unchanged" {
		t.Fatalf("secret after update = %q err=%v", plain, err)
	}

	rotated := saved
	if _, found, err := store.UpdateWebhook(ctx, rotated, sealedSecret(t, "whsec_rotated")); err != nil || !found {
		t.Fatalf("UpdateWebhook: %v", err)
	}
	box, _, err = store.WebhookSecretBox(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := enc.Open(box); err != nil || plain != "whsec_rotated" {
		t.Fatalf("secret after rotation = %q err=%v", plain, err)
	}
}

func TestWebhookRequiresAValidShape(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "operator")

	cases := []struct {
		name   string
		mutate func(*automation.Webhook)
		secret *string
	}{
		{"no name", func(w *automation.Webhook) { w.Name = "  " }, nil},
		{"no url", func(w *automation.Webhook) { w.URL = "" }, nil},
		{"bad scheme", func(w *automation.Webhook) { w.URL = "ftp://receiver.example.com/hook" }, nil},
		{"credentials in url", func(w *automation.Webhook) { w.URL = "https://user:pass@receiver.example.com/hook" }, nil},
		{"fragment in url", func(w *automation.Webhook) { w.URL = "https://receiver.example.com/hook#x" }, nil},
		{"bad method", func(w *automation.Webhook) { w.Method = "GET" }, nil},
		{"bad auth mode", func(w *automation.Webhook) { w.AuthMode = "digest" }, nil},
		{"header mode without a header", func(w *automation.Webhook) { w.AuthMode = "header"; w.AuthHeader = " " }, nil},
		{"header mode with Host", func(w *automation.Webhook) { w.AuthMode = "header"; w.AuthHeader = "Host" }, nil},
		{"header mode with webhook prefix", func(w *automation.Webhook) { w.AuthMode = "header"; w.AuthHeader = "Webhook-Signature" }, nil},
		{"negative timeout", func(w *automation.Webhook) { w.Timeout = -time.Second }, nil},
		{"negative attempts", func(w *automation.Webhook) { w.MaxAttempts = -1 }, nil},
		{"unbounded attempts", func(w *automation.Webhook) { w.MaxAttempts = 1 << 20 }, nil},
		{"unreasonable timeout", func(w *automation.Webhook) { w.Timeout = 48 * time.Hour }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := webhook("feed", "https://receiver.example.com/hook")
			tc.mutate(&rec)
			secret := sealedSecret(t, "whsec_abc")
			if _, err := store.CreateWebhook(ctx, user.ID, rec, secret); !errors.Is(err, channelstore.ErrInvalidWebhook) {
				t.Fatalf("CreateWebhook err = %v, want ErrInvalidWebhook", err)
			}
		})
	}

	// A webhook made through the operator token has no owning account: the
	// operator is a credential and not a person. It is stored without one
	// rather than refused.
	rec := webhook("feed", "https://receiver.example.com/hook")
	if _, err := store.CreateWebhook(ctx, 0, rec, sealedSecret(t, "whsec_abc")); err != nil {
		t.Fatalf("CreateWebhook with no owning account: %v", err)
	}

	// A record that omits the optional bounds is filled in rather than refused:
	// the schema's own defaults are what a partial request gets.
	partial := webhook("defaults", "https://receiver.example.com/hook")
	partial.AuthMode = "none"
	partial.Timeout = 0
	partial.MaxAttempts = 0
	filled, err := store.CreateWebhook(ctx, user.ID, partial, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filled.Timeout != 15*time.Second || filled.MaxAttempts != 5 {
		t.Fatalf("defaults = %v / %d", filled.Timeout, filled.MaxAttempts)
	}
}

func TestWebhookNeedsASecretUnlessAuthModeIsNone(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "operator")

	rec := webhook("feed", "https://receiver.example.com/hook")
	rec.AuthMode = "hmac"
	if _, err := store.CreateWebhook(ctx, user.ID, rec, nil); !errors.Is(err, channelstore.ErrInvalidWebhook) {
		t.Fatalf("hmac without a secret err = %v, want ErrInvalidWebhook", err)
	}

	anon := rec
	anon.AuthMode = "none"
	created, err := store.CreateWebhook(ctx, user.ID, anon, nil)
	if err != nil {
		t.Fatalf("none without a secret: %v", err)
	}
	if created.HasSecret {
		t.Fatal("HasSecret = true for a webhook with no secret")
	}
	if _, _, err := store.WebhookSecretBox(ctx, created.ID); err != nil {
		t.Fatalf("WebhookSecretBox: %v", err)
	}
}

func TestWebhookSourcesAreReplacedListedAndCascaded(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "operator")
	accountID := ownerAccount(t, store, user.ID, "client-a", channelstore.NetworkTelegram, 101)
	first := ownedSourceChannel(t, store, accountID, "client-a", 100)
	second := ownedSourceChannel(t, store, accountID, "client-a", 200)

	rec := webhook("feed", "https://receiver.example.com/hook")
	rec.AccountID = accountID
	rec.SourceChannelIDs = []int64{first}
	created, err := store.CreateWebhook(ctx, user.ID, rec, sealedSecret(t, "whsec_abc"))
	if err != nil {
		t.Fatal(err)
	}

	ids, err := store.WebhookIDsForChannel(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != created.ID {
		t.Fatalf("WebhookIDsForChannel = %v", ids)
	}
	if ids, _ := store.WebhookIDsForChannel(ctx, second); len(ids) != 0 {
		t.Fatalf("unsubscribed channel = %v", ids)
	}

	edited := created
	edited.SourceChannelIDs = []int64{second}
	saved, found, err := store.UpdateWebhook(ctx, edited, nil)
	if err != nil || !found {
		t.Fatalf("UpdateWebhook: %v", err)
	}
	if len(saved.SourceChannelIDs) != 1 || saved.SourceChannelIDs[0] != second {
		t.Fatalf("SourceChannelIDs after replace = %v", saved.SourceChannelIDs)
	}
	if ids, _ := store.WebhookIDsForChannel(ctx, first); len(ids) != 0 {
		t.Fatalf("old source still subscribed: %v", ids)
	}

	if ok, err := store.DeleteWebhook(ctx, created.ID); err != nil || !ok {
		t.Fatalf("DeleteWebhook: ok=%v err=%v", ok, err)
	}
	if ids, _ := store.WebhookIDsForChannel(ctx, second); len(ids) != 0 {
		t.Fatalf("sources survived delete: %v", ids)
	}
	if _, found, err := store.GetWebhook(ctx, created.ID); err != nil || found {
		t.Fatalf("GetWebhook after delete: found=%v err=%v", found, err)
	}
	if ok, err := store.DeleteWebhook(ctx, created.ID); err != nil || ok {
		t.Fatalf("DeleteWebhook twice: ok=%v err=%v", ok, err)
	}
}

func TestWebhookListFiltersAndSummarizes(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "operator")

	rec := webhook("news feed", "https://receiver.example.com/hook")
	rec.AuthMode = "none"
	first, err := store.CreateWebhook(ctx, user.ID, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec2 := rec
	rec2.Name = "alerts"
	rec2.Enabled = false
	if _, err := store.CreateWebhook(ctx, user.ID, rec2, nil); err != nil {
		t.Fatal(err)
	}

	page, err := store.ListWebhooks(ctx, automation.WebhookListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Webhooks) != 2 {
		t.Fatalf("page = %+v", page)
	}

	off := false
	page, err = store.ListWebhooks(ctx, automation.WebhookListOptions{Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Webhooks[0].Name != "alerts" {
		t.Fatalf("enabled filter = %+v", page)
	}

	page, err = store.ListWebhooks(ctx, automation.WebhookListOptions{Query: "news"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Webhooks[0].ID != first.ID {
		t.Fatalf("query filter = %+v", page)
	}

	page, err = store.ListWebhooks(ctx, automation.WebhookListOptions{Page: 1, PerPage: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Webhooks) != 1 {
		t.Fatalf("paging = %+v", page)
	}
}

func TestWebhookSecretCanBeClearedWithoutBeingReadBack(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "operator")

	rec := webhook("feed", "https://receiver.example.com/hook")
	created, err := store.CreateWebhook(ctx, user.ID, rec, sealedSecret(t, "whsec_to_be_removed"))
	if err != nil {
		t.Fatal(err)
	}
	if !created.HasSecret {
		t.Fatal("HasSecret = false after creating with a secret")
	}

	// A non-nil empty box is the explicit request to remove the stored secret.
	// It is a different request from a nil secret, which leaves it alone. It
	// goes together with an auth mode that authenticates with nothing: a
	// webhook left signing with no key would be refused, and rightly so.
	empty := secrets.Box{}
	anon := created
	anon.AuthMode = "none"
	updated, found, err := store.UpdateWebhook(ctx, anon, &empty)
	if err != nil || !found {
		t.Fatalf("UpdateWebhook: found=%v err=%v", found, err)
	}
	if updated.HasSecret {
		t.Fatal("HasSecret = true after removing the secret")
	}
	box, _, err := store.WebhookSecretBox(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if box.Version != 0 || len(box.Nonce) != 0 || len(box.Ciphertext) != 0 {
		t.Fatalf("the stored secret was not cleared: %+v", box)
	}

	// Refilling it and then editing without mentioning the secret keeps it.
	refilled := anon
	refilled.AuthMode = "hmac"
	if _, _, err := store.UpdateWebhook(ctx, refilled, sealedSecret(t, "whsec_replaced")); err != nil {
		t.Fatal(err)
	}
	renamed := refilled
	renamed.Name = "renamed"
	kept, _, err := store.UpdateWebhook(ctx, renamed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !kept.HasSecret {
		t.Fatal("an update that said nothing about the secret removed it")
	}

	// An auth mode that authenticates cannot be left with nothing to use.
	stripped := refilled
	stripped.AuthMode = "hmac"
	if _, _, err := store.UpdateWebhook(ctx, stripped, &secrets.Box{}); !errors.Is(err, channelstore.ErrInvalidWebhook) {
		t.Fatalf("err = %v, want ErrInvalidWebhook", err)
	}
}
