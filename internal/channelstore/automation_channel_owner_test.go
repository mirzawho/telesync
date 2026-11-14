package channelstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
)

func TestAWorkflowIsPerUserOwnedChannelRegistration(t *testing.T) {
	// The product rule is one workflow per managed source channel. Under
	// user-owned accounts the unit is the registration: two users subscribed to
	// one remote channel are two managed channels and get two independent
	// workflows, each running under its own account.
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)

	chA := ownedChannel(t, store, accA, "official", 500)
	chB := ownedChannel(t, store, accB, "official", 600)

	// An enabled workflow must publish somewhere, so each carries a destination.
	dest := []automation.WorkflowDestination{{
		DestinationType: string(automation.DestinationTelegram),
		ClientKey:       "official",
		ChannelID:       500,
		AccountID:       accA,
		Enabled:         true,
	}}
	wfA, err := store.CreateWorkflow(ctx, automation.WorkflowInput{
		ManagedChannelID: chA, Enabled: true, CreatedBy: userA.ID, AccountID: accA,
		Destinations: dest,
	})
	if err != nil {
		t.Fatalf("alice's workflow: %v", err)
	}
	wfB, err := store.CreateWorkflow(ctx, automation.WorkflowInput{
		ManagedChannelID: chB, Enabled: true, CreatedBy: userB.ID, AccountID: accB,
		Destinations: []automation.WorkflowDestination{{
			DestinationType: string(automation.DestinationTelegram),
			ClientKey:       "official",
			ChannelID:       600,
			AccountID:       accB,
			Enabled:         true,
		}},
	})
	if err != nil {
		t.Fatalf("bob's workflow: %v", err)
	}
	if wfA.ID == wfB.ID {
		t.Fatal("two owners' workflows must be two rows")
	}
	if wfA.AccountID != accA || wfB.AccountID != accB {
		t.Fatalf("workflow bindings = (%q, %q), want (%q, %q)", wfA.AccountID, wfB.AccountID, accA, accB)
	}

	// Each resolves for its own channel and no other.
	got, found, err := store.WorkflowByChannel(ctx, chA)
	if err != nil || !found {
		t.Fatalf("WorkflowByChannel(a): found=%v err=%v", found, err)
	}
	if got.ID != wfA.ID || got.AccountID != accA {
		t.Fatalf("alice's channel resolved to workflow %d/%q", got.ID, got.AccountID)
	}
	gotB, found, err := store.WorkflowByChannel(ctx, chB)
	if err != nil || !found {
		t.Fatalf("WorkflowByChannel(b): found=%v err=%v", found, err)
	}
	if gotB.ID != wfB.ID || gotB.AccountID != accB {
		t.Fatalf("bob's channel resolved to workflow %d/%q", gotB.ID, gotB.AccountID)
	}

	// Listing is owner-scoped: alice never sees bob's workflow.
	page, err := store.ListWorkflows(ctx, automation.WorkflowListOptions{OwnerUserID: userA.ID})
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("alice sees %d workflows, want 1", page.Total)
	}
	if page.Workflows[0].ID != wfA.ID {
		t.Fatalf("alice sees workflow %d, want %d", page.Workflows[0].ID, wfA.ID)
	}
}

func TestAWorkflowRefusesAnAccountTheCallerDoesNotOwn(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	chB := ownedChannel(t, store,
		ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2), "official", 500)

	if _, err := store.CreateWorkflow(ctx, automation.WorkflowInput{
		ManagedChannelID: chB, Enabled: true, CreatedBy: userA.ID, AccountID: accA,
	}); err == nil {
		t.Fatal("a workflow must not be created over another user's channel registration")
	}
}

func TestWebhookHistoryIsPrivateToItsOwner(t *testing.T) {
	// Delivery payloads and request/response history are exactly the material a
	// tenant must never see about another. Listing, reading and the delivery
	// history are all owner-scoped.
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)
	chA := ownedChannel(t, store, accA, "official", 1000)
	chB := ownedChannel(t, store, accB, "official", 1000)

	inA := webhook("alice feed", "https://a.example.com/hook")
	inA.SourceChannelIDs = []int64{chA}
	inA.AccountID = accA
	hookA, err := store.CreateWebhook(ctx, userA.ID, inA, sealedSecret(t, "whsec_alice"))
	if err != nil {
		t.Fatalf("create alice's webhook: %v", err)
	}
	inB := webhook("bob feed", "https://b.example.com/hook")
	inB.SourceChannelIDs = []int64{chB}
	inB.AccountID = accB
	hookB, err := store.CreateWebhook(ctx, userB.ID, inB, sealedSecret(t, "whsec_bob"))
	if err != nil {
		t.Fatalf("create bob's webhook: %v", err)
	}

	// Alice may not read bob's webhook at all.
	if _, found, err := store.GetWebhookFor(ctx, userA.ID, hookB.ID); err != nil {
		t.Fatalf("GetWebhookFor: %v", err)
	} else if found {
		t.Fatal("an owner must not be able to read another owner's webhook")
	}
	// Nor its secret.
	if _, found, err := store.WebhookSecretBoxFor(ctx, userA.ID, hookB.ID); err != nil {
		t.Fatalf("WebhookSecretBoxFor: %v", err)
	} else if found {
		t.Fatal("an owner must not reach another owner's webhook secret")
	}
	// Nor its delivery history.
	page, err := store.ListDeliveriesFor(ctx, userA.ID, hookB.ID, automation.DeliveryListOptions{})
	if err != nil {
		t.Fatalf("ListDeliveriesFor: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("alice sees %d of bob's deliveries, want 0", page.Total)
	}

	// Alice's own webhook is still fully hers.
	if _, found, err := store.GetWebhookFor(ctx, userA.ID, hookA.ID); err != nil || !found {
		t.Fatalf("alice must read her own webhook: found=%v err=%v", found, err)
	}

	// And bob's listing does not show alice's.
	bobs, err := store.ListWebhooks(ctx, automation.WebhookListOptions{OwnerUserID: userB.ID})
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if bobs.Total != 1 || bobs.Webhooks[0].ID != hookB.ID {
		t.Fatalf("bob's listing = %d rows, want exactly his own", bobs.Total)
	}
}

func TestWebhookDeliveryHistoryIsScopedToTheWebhooksOwner(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)

	ev := eventFor(ownedChannel(t, store, accA, "official", 1000))
	ev.ID = "evt_delivery"
	ev.SourceAccountID, ev.AccountID = accA, accA
	if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	in := webhook("alice feed", "https://a.example.com/hook")
	in.AccountID = accA
	// This test is about who may read a delivery's history, not about signing.
	// The shared fixture defaults to hmac, which correctly demands a secret.
	in.AuthMode = "none"
	hook, err := store.CreateWebhook(ctx, userA.ID, in, nil)
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	delivery, err := store.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
		WebhookID: hook.ID, EventID: ev.ID, AccountID: accA, OwnerUserID: userA.ID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create delivery: %v", err)
	}
	if delivery.AccountID != accA {
		t.Fatalf("delivery binding = %q, want %q", delivery.AccountID, accA)
	}

	if _, found, err := store.GetDeliveryFor(ctx, userA.ID, hook.ID, delivery.ID); err != nil || !found {
		t.Fatalf("alice must read her own delivery: found=%v err=%v", found, err)
	}
	if _, found, err := store.GetDeliveryFor(ctx, userB.ID, hook.ID, delivery.ID); err != nil {
		t.Fatalf("GetDeliveryFor: %v", err)
	} else if found {
		t.Fatal("a delivery payload must not be readable by another owner")
	}

	attempts, err := store.WebhookAttemptsFor(ctx, userB.ID, delivery.ID)
	if err != nil {
		t.Fatalf("WebhookAttemptsFor: %v", err)
	}
	if len(attempts) != 0 {
		t.Fatalf("bob reads %d of alice's request/response attempts, want 0", len(attempts))
	}

	// A worker bound to bob's account never claims alice's delivery job.
	if _, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "wb", Lease: time.Minute, AccountID: accB,
	}); err != nil {
		t.Fatalf("claim: %v", err)
	} else if ok {
		t.Fatal("a delivery job must never run under another account")
	}
}

func TestPublishOperationsCarryTheirAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)

	op, created, err := store.BeginPublishOperation(ctx, channelstore.PublishOperation{
		UserID:          userA.ID,
		AccountID:       accA,
		IdempotencyKey:  "op-alice",
		RequestHash:     "hash-alice",
		DestinationType: "telegram",
		DestinationID:   "500",
	})
	if err != nil {
		t.Fatalf("BeginPublishOperation: %v", err)
	}
	if !created || op.AccountID != accA {
		t.Fatalf("publish operation binding = %q (created=%v)", op.AccountID, created)
	}

	// The same key under a different owner is a different operation: two users
	// copying the same remote message must not suppress each other.
	other, created, err := store.BeginPublishOperation(ctx, channelstore.PublishOperation{
		UserID:          userB.ID,
		AccountID:       accB,
		IdempotencyKey:  "op-alice",
		RequestHash:     "hash-alice",
		DestinationType: "telegram",
		DestinationID:   "500",
	})
	if err != nil {
		t.Fatalf("BeginPublishOperation for bob: %v", err)
	}
	if !created || other.ID == op.ID {
		t.Fatalf("a second owner's operation must be distinct (created=%v id=%d vs %d)", created, other.ID, op.ID)
	}
}

func TestSendAndEditOperationsCarryTheirAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "alice")
	acc := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 1)

	send, created, err := store.BeginSendOperation(ctx, channelstore.SendOperation{
		UserID:               user.ID,
		AccountID:            acc,
		ClientKey:            "official",
		DestinationChannelID: "500",
		IdempotencyKey:       "k1",
		RequestFingerprint:   "fp-send",
	})
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if !created {
		t.Fatal("a first send must be created")
	}
	if send.AccountID != acc {
		t.Fatalf("send operation binding = %q, want %q", send.AccountID, acc)
	}

	edit, created, err := store.BeginMessageEditOperation(ctx, channelstore.MessageEditOperation{
		UserID:               user.ID,
		AccountID:            acc,
		ClientKey:            "official",
		DestinationChannelID: "500",
		TargetMessageID:      7,
		RequestFingerprint:   "fp-edit",
	})
	if err != nil {
		t.Fatalf("BeginMessageEditOperation: %v", err)
	}
	if !created {
		t.Fatal("a first edit must be created")
	}
	if edit.AccountID != acc {
		t.Fatalf("edit operation binding = %q, want %q", edit.AccountID, acc)
	}

	creation, created, err := store.BeginChannelCreation(ctx, user.ID, acc, "official", "ck1", "fp", "{}")
	if err != nil {
		t.Fatalf("BeginChannelCreation: %v", err)
	}
	if !created {
		t.Fatal("a first channel creation must be created")
	}
	if creation.AccountID != acc {
		t.Fatalf("channel creation binding = %q, want %q", creation.AccountID, acc)
	}
}

func TestQuarantinedAccountsAreNeverReturnedAsOwned(t *testing.T) {
	// A legacy central session belongs to nobody until an operator adopts it.
	// Every account-scoped read must exclude it, or a user inherits a session
	// that was never theirs.
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "alice")
	acc := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 1)

	// A channel joined through the pre-account path lands on a quarantine
	// account: the row keeps working for reads but belongs to nobody until an
	// operator adopts it. Registering through UpsertFor would attach it to a
	// real account, which is the other half of this test.
	legacy, err := store.Upsert(ctx, channel("legacy-key", 999))
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if legacy.ID == 0 {
		t.Fatal("no channel")
	}

	// The legacy client key's quarantine account is reachable only through it.
	quarantined, err := store.ResolveLegacy(ctx, "legacy-key")
	if err != nil {
		t.Fatalf("ResolveLegacy: %v", err)
	}
	if quarantined == "" {
		t.Fatal("a legacy key must resolve to its quarantine account")
	}
	if quarantined == acc {
		t.Fatal("a quarantine account must not be a user's account")
	}

	accounts, err := store.ListByOwner(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	for _, a := range accounts {
		if a.ID == quarantined {
			t.Fatal("a quarantined account must never appear in a user's account listing")
		}
	}
	if _, err := store.ByID(ctx, user.ID, quarantined); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("ByID on a quarantine account = %v, want ErrAccountNotFound", err)
	}
}
