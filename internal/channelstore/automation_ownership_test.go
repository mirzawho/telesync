package channelstore_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
)

// Ownership is a dimension on every automation row, and it is what makes the
// background subsystem multi-tenant. These tests pin the three promises that
// dimension has to keep: two owners watching one remote channel get their own
// work, a worker only claims its own account's jobs, and a job never runs
// through an account it was not bound to.

// accountFor registers one user-owned account and returns its opaque id.
func ownerAccount(t *testing.T, store *channelstore.Store, owner int64, serverKey, networkType string, remoteUser int64) string {
	t.Helper()
	saved := mustAccount(t, store, channelstore.Account{
		// Seeded on the owner too: two users may legitimately register the
		// same remote identity, and each keeps its own account and its own
		// session. An id derived from the remote user alone would collide.
		ID:           accountID(fmt.Sprintf("u%d-a%d", owner, remoteUser)),
		OwnerUserID:  owner,
		ServerKey:    serverKey,
		NetworkType:  networkType,
		RemoteUserID: remoteUser,
		Status:       channelstore.AccountStatusConnected,
	})
	return saved.ID
}

// ownedChannel registers one managed channel for one account's own subscription
// to a remote channel, returning the local row id.
func ownedChannel(t *testing.T, store *channelstore.Store, accountID string, serverKey string, remoteID int64) int64 {
	t.Helper()
	rec := channel(serverKey, remoteID)
	saved, err := store.UpsertFor(context.Background(), accountID, rec)
	if err != nil {
		t.Fatalf("UpsertFor: %v", err)
	}
	return saved.ID
}

func TestTwoOwnersWatchingTheSameRemoteChannelEachGetTheirOwnEvent(t *testing.T) {
	// The regression this whole task exists for. v0.0.3 deduplicated events
	// across connections because it assumed one centrally-managed channel: the
	// second observer of a message was a duplicate of the first. Under
	// user-owned accounts that assumption is wrong — two users may deliberately
	// watch the same remote channel — and dropping the second user's event is
	// dropping their automation, silently.
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")

	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 1)

	// The same remote channel, independently subscribed: two local rows.
	chA := ownedChannel(t, store, accA, "official", 777)
	chB := ownedChannel(t, store, accB, "official", 777)
	if chA == chB {
		t.Fatal("two owners subscribing to one remote channel must be two local rows")
	}

	eventA := eventFor(chA)
	eventA.ID = "evt_alice"
	eventA.SourceAccountID = accA
	eventA.AccountID = accA
	eventA.SourceClientKey = "official"
	eventA = eventA.WithOwner(userA.ID)

	eventB := eventFor(chB)
	eventB.ID = "evt_bob"
	eventB.SourceAccountID = accB
	eventB.AccountID = accB
	eventB.SourceClientKey = "official"
	eventB.TelegramChannelID = 777
	eventB = eventB.WithOwner(userB.ID)

	insertedA, err := store.InsertMessageEvent(ctx, eventA)
	if err != nil {
		t.Fatalf("insert alice's event: %v", err)
	}
	if !insertedA {
		t.Fatal("alice's observation must be stored")
	}
	insertedB, err := store.InsertMessageEvent(ctx, eventB)
	if err != nil {
		t.Fatalf("insert bob's event: %v", err)
	}
	if !insertedB {
		t.Fatal("bob's observation must not be suppressed as alice's duplicate")
	}

	total, err := store.CountAutomationEvents(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 2 {
		t.Fatalf("stored %d events for two owners watching one remote channel, want 2", total)
	}

	// Each owner's work is bound to their own account. Fan-out is what decides
	// which automations run and lives in internal/automation/events; what this
	// store guarantees is that a job row carries the account identity of the
	// work it represents, so a worker bound to one account can never claim the
	// other's.
	for _, ev := range []automation.MessageEvent{eventA, eventB} {
		owner := automation.Owner{OwnerUserID: ev.AccountOwnerUserID(), AccountID: ev.AccountID}
		if _, _, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
			EventID: ev.ID, Kind: automation.JobWebhookDelivery, TargetID: 1,
			MaxAttempts: 3, Owner: owner,
		}); err != nil {
			t.Fatalf("enqueue for %s: %v", ev.AccountID, err)
		}
	}

	// Each owner's work is scheduled against their own event.
	jobsA, err := store.AutomationJobs(ctx, channelstore.AutomationJobQuery{EventID: "evt_alice"})
	if err != nil {
		t.Fatalf("list alice's jobs: %v", err)
	}
	jobsB, err := store.AutomationJobs(ctx, channelstore.AutomationJobQuery{EventID: "evt_bob"})
	if err != nil {
		t.Fatalf("list bob's jobs: %v", err)
	}
	if len(jobsA) == 0 || len(jobsB) == 0 {
		t.Fatalf("each owner's event must schedule its own work (alice %d, bob %d)", len(jobsA), len(jobsB))
	}
	for _, job := range append(jobsA, jobsB...) {
		if job.AccountID == "" {
			t.Fatalf("job %d carries no account identity", job.ID)
		}
	}
}

func TestOneOwnersSecondAccountStillDeduplicatesItsOwnObservation(t *testing.T) {
	// Dedup is per owner and per account, not per owner or per account alone.
	// Losing the within-account dedup would make a reconnect run a workflow
	// twice, which is the other half of the same promise.
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "alice")
	acc := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 1)
	ch := ownedChannel(t, store, acc, "official", 900)

	first := eventFor(ch)
	first.ID = "evt_first"
	first.SourceAccountID = acc
	first.AccountID = acc

	second := first
	second.ID = "evt_replayed"

	if _, err := store.InsertMessageEvent(ctx, first); err != nil {
		t.Fatalf("insert: %v", err)
	}
	inserted, err := store.InsertMessageEvent(ctx, second)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if inserted {
		t.Fatal("a replayed update from the same account must not create a second event")
	}
	total, err := store.CountAutomationEvents(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Fatalf("stored %d events for one account replaying one message, want 1", total)
	}
}

func TestQueuedWorkIsBoundToItsAccountAndCannotBeClaimedByAnother(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)
	chA := ownedChannel(t, store, accA, "official", 1000)

	ev := eventFor(chA)
	ev.ID = "evt_owned"
	ev.SourceAccountID = accA
	ev.AccountID = accA
	if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	job, created, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
		EventID:     ev.ID,
		Kind:        automation.JobWebhookDelivery,
		TargetID:    1,
		MaxAttempts: 3,
		Owner:       automation.Owner{AccountID: accA, OwnerUserID: userA.ID},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !created {
		t.Fatal("a first enqueue must be reported as created")
	}
	if job.AccountID != accA || job.OwnerUserID != userA.ID {
		t.Fatalf("job binding = (%q, %d), want (%q, %d)", job.AccountID, job.OwnerUserID, accA, userA.ID)
	}

	// A worker bound to bob's account must not be handed alice's job.
	if _, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker:    "worker-b",
		Lease:     time.Minute,
		AccountID: accB,
	}); err != nil {
		t.Fatalf("claim for bob: %v", err)
	} else if ok {
		t.Fatal("a worker must never claim another account's job")
	}

	// A worker bound to the owning account claims it.
	claimed, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker:    "worker-a",
		Lease:     time.Minute,
		AccountID: accA,
	})
	if err != nil {
		t.Fatalf("claim for alice: %v", err)
	}
	if !ok {
		t.Fatal("the owning account's worker must claim its own job")
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job %d, want %d", claimed.ID, job.ID)
	}
}

func TestSharedClaimantClaimsOwnerBoundWorkButWrongAccountCannot(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "alice")
	acc := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 1)
	defaultOwner = automation.Owner{OwnerUserID: user.ID, AccountID: acc}
	ch := ownedChannel(t, store, acc, "official", 1000)

	ev := eventFor(ch)
	ev.ID = "evt_unbound"
	ev.SourceAccountID = acc
	ev.AccountID = acc
	if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	enqueueJob(t, store, ev.ID, 1)

	// An account-specific claimant still cannot take another account's work.
	other := ownerAccount(t, store, createUser(t, store, "stranger").ID,
		"official", channelstore.NetworkTelegram, 99)
	if _, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "account-b", Lease: time.Minute, AccountID: other,
	}); err != nil {
		t.Fatalf("account-specific claim: %v", err)
	} else if ok {
		t.Fatal("an account-specific claimant must not claim another account's job")
	}

	// The shared pool has no account identity. It claims globally, but the
	// durable owner remains attached for dispatch to the matching account.
	claimed, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "host/123/0", Lease: time.Minute,
	})
	if err != nil || !ok {
		t.Fatalf("shared claimant must claim bound work (ok=%v err=%v)", ok, err)
	}
	if claimed.OwnerUserID != user.ID || claimed.AccountID != acc {
		t.Fatalf("shared claimant got owner (%d, %q), want (%d, %q)",
			claimed.OwnerUserID, claimed.AccountID, user.ID, acc)
	}
}

func TestOneSharedClaimantClaimsJobsForBothAccounts(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)

	for _, tc := range []struct {
		id      string
		userID  int64
		account string
		remote  int64
	}{
		{id: "evt_shared_a", userID: userA.ID, account: accA, remote: 1001},
		{id: "evt_shared_b", userID: userB.ID, account: accB, remote: 1002},
	} {
		ch := ownedChannel(t, store, tc.account, "official", tc.remote)
		ev := eventFor(ch)
		ev.ID = tc.id
		ev.SourceAccountID, ev.AccountID = tc.account, tc.account
		if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
			t.Fatalf("insert %s: %v", ev.ID, err)
		}
		if _, _, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
			EventID: ev.ID, Kind: automation.JobWebhookDelivery, TargetID: 1,
			MaxAttempts: 3, Owner: automation.Owner{OwnerUserID: tc.userID, AccountID: tc.account},
		}); err != nil {
			t.Fatalf("enqueue %s: %v", ev.ID, err)
		}
	}

	seen := map[string]int64{}
	for i := 0; i < 2; i++ {
		job, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
			Worker: "host/123/0", Lease: time.Minute,
		})
		if err != nil || !ok {
			t.Fatalf("shared claim %d: ok=%v err=%v", i+1, ok, err)
		}
		seen[job.AccountID] = job.OwnerUserID
		if err := store.CompleteAutomationJob(ctx, job.ID, "host/123/0"); err != nil {
			t.Fatalf("complete job %d: %v", job.ID, err)
		}
	}
	if seen[accA] != userA.ID || seen[accB] != userB.ID {
		t.Fatalf("shared claimant returned bindings = %#v, want %q→%d and %q→%d",
			seen, accA, userA.ID, accB, userB.ID)
	}
}

func TestLegacyJobRowsAreNotClaimableAndDoNotInheritAnAccount(t *testing.T) {
	// Migration 21 leaves account_id NULL on every pre-existing row. Those rows
	// describe work nobody owns, and brief §34 says they are paused, never
	// silently run under whichever account happens to be connected.
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "alice")
	acc := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 1)
	ch := ownedChannel(t, store, acc, "official", 1000)

	ev := eventFor(ch)
	ev.ID = "evt_legacy"
	ev.SourceAccountID = acc
	ev.AccountID = acc
	if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	legacy, created, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
		EventID:     ev.ID,
		Kind:        automation.JobWebhookDelivery,
		TargetID:    1,
		MaxAttempts: 3,
		Owner:       automation.Owner{AccountID: acc, OwnerUserID: user.ID},
	})
	if err != nil || !created {
		t.Fatalf("enqueue: created=%v err=%v", created, err)
	}
	if err := store.ClearJobOwnership(ctx, legacy.ID); err != nil {
		t.Fatalf("simulate legacy row: %v", err)
	}

	// A healthy job with the same ordering key must still proceed. The legacy
	// row stays intact and paused, but cannot hold the shared pool behind it.
	healthyUser := createUser(t, store, "bob")
	healthyAccount := ownerAccount(t, store, healthyUser.ID, "official", channelstore.NetworkTelegram, 2)
	healthyChannel := ownedChannel(t, store, healthyAccount, "official", 2000)
	healthyEvent := eventFor(healthyChannel)
	healthyEvent.ID = "evt_healthy_after_legacy"
	healthyEvent.SourceAccountID, healthyEvent.AccountID = healthyAccount, healthyAccount
	if _, err := store.InsertMessageEvent(ctx, healthyEvent); err != nil {
		t.Fatalf("insert healthy event: %v", err)
	}
	healthy, created, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
		EventID: healthyEvent.ID, Kind: automation.JobWebhookDelivery, TargetID: 1,
		MaxAttempts: 3, Owner: automation.Owner{AccountID: healthyAccount, OwnerUserID: healthyUser.ID},
	})
	if err != nil || !created {
		t.Fatalf("enqueue healthy work: created=%v err=%v", created, err)
	}

	paused, err := store.PausedAutomationJobs(ctx, 0)
	if err != nil {
		t.Fatalf("PausedAutomationJobs: %v", err)
	}
	if len(paused) != 1 || paused[0].ID != legacy.ID {
		t.Fatalf("ownerless work must be preserved and reported as paused, got %+v", paused)
	}

	claimed, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "host/123/0", Lease: time.Minute,
	})
	if err != nil || !ok || claimed.ID != healthy.ID {
		t.Fatalf("healthy work behind paused legacy row: job=%+v ok=%v err=%v; want %d",
			claimed, ok, err, healthy.ID)
	}
	if claimed.ID == legacy.ID || claimed.AccountID != healthyAccount || claimed.OwnerUserID != healthyUser.ID {
		t.Fatalf("claim did not retain healthy job binding: %+v", claimed)
	}
	stillPaused, found, err := store.AutomationJob(ctx, legacy.ID)
	if err != nil || !found || !stillPaused.Paused || stillPaused.AccountID != "" || stillPaused.OwnerUserID != 0 {
		t.Fatalf("legacy row changed after claim: %+v found=%v err=%v", stillPaused, found, err)
	}
}

func TestSharedClaimSkipsDisconnectedAccountAndResumesAfterReconnect(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)
	enqueueFor := func(eventID, account string, ownerID, remote int64) channelstore.AutomationJob {
		t.Helper()
		ch := ownedChannel(t, store, account, "official", remote)
		ev := eventFor(ch)
		ev.ID = eventID
		ev.SourceAccountID, ev.AccountID = account, account
		if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
			t.Fatalf("insert %s: %v", eventID, err)
		}
		job, _, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
			EventID: eventID, Kind: automation.JobWebhookDelivery, TargetID: 1,
			MaxAttempts: 3, Owner: automation.Owner{OwnerUserID: ownerID, AccountID: account},
		})
		if err != nil {
			t.Fatalf("enqueue %s: %v", eventID, err)
		}
		return job
	}
	jobA := enqueueFor("evt_disconnected_a", accA, userA.ID, 3001)
	jobB := enqueueFor("evt_available_b", accB, userB.ID, 3002)

	if err := store.SetStatus(ctx, userA.ID, accA, channelstore.AccountStatusDisconnected, "connection_lost"); err != nil {
		t.Fatalf("disconnect account A: %v", err)
	}
	claimed, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "host/123/0", Lease: time.Minute,
	})
	if err != nil || !ok || claimed.ID != jobB.ID {
		t.Fatalf("claim with A disconnected: job=%+v ok=%v err=%v; want B job %d",
			claimed, ok, err, jobB.ID)
	}
	if err := store.CompleteAutomationJob(ctx, claimed.ID, "host/123/0"); err != nil {
		t.Fatalf("complete B job: %v", err)
	}
	if _, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "host/123/0", Lease: time.Minute,
	}); err != nil || ok {
		t.Fatalf("disconnected account work must remain unclaimed: ok=%v err=%v", ok, err)
	}
	preserved, found, err := store.AutomationJob(ctx, jobA.ID)
	if err != nil || !found || preserved.Status != automation.JobPending || preserved.AccountID != accA {
		t.Fatalf("disconnected job was not preserved: %+v found=%v err=%v", preserved, found, err)
	}

	if err := store.SetStatus(ctx, userA.ID, accA, channelstore.AccountStatusConnected, ""); err != nil {
		t.Fatalf("reconnect account A: %v", err)
	}
	resumed, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "host/123/0", Lease: time.Minute,
	})
	if err != nil || !ok || resumed.ID != jobA.ID {
		t.Fatalf("claim after reconnect: job=%+v ok=%v err=%v; want A job %d",
			resumed, ok, err, jobA.ID)
	}
}

func TestJobRetriesKeepTheirOriginalOwnerAndAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "alice")
	acc := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 1)
	ch := ownedChannel(t, store, acc, "official", 1000)

	ev := eventFor(ch)
	ev.ID = "evt_retry"
	ev.SourceAccountID = acc
	ev.AccountID = acc
	if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	created, _, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
		EventID: ev.ID, Kind: automation.JobWebhookDelivery, TargetID: 1,
		MaxAttempts: 3, Owner: automation.Owner{AccountID: acc, OwnerUserID: user.ID},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	claimed, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "w", Lease: time.Minute, AccountID: acc,
	})
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, err := store.RecordAutomationJobFailure(ctx, claimed.ID, "w",
		automation.JobError{Code: "temp", Message: "boom", Retryable: true}, true, time.Now().UTC()); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	again, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "w", Lease: time.Minute, AccountID: acc,
	})
	if err != nil || !ok {
		t.Fatalf("reclaim: ok=%v err=%v", ok, err)
	}
	if again.AccountID != acc || again.OwnerUserID != user.ID {
		t.Fatalf("a retry changed its binding to (%q, %d), want (%q, %d)",
			again.AccountID, again.OwnerUserID, acc, user.ID)
	}
	if again.ID != created.ID {
		t.Fatalf("a retry is the same job: got %d, want %d", again.ID, created.ID)
	}

	// Work that is still running is not reopenable — it is in flight, and
	// handing it a second life is how one message gets published twice. The
	// operator retry is offered against a job that has stopped.
	if _, err := store.RecordAutomationJobFailure(ctx, again.ID, "w",
		automation.JobError{Code: "temp", Message: "boom", Retryable: true}, false, time.Now().UTC()); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	// A reopen keeps the binding too: an operator retry is still the same work
	// under the same account.
	reopened, _, err := store.ReopenAutomationJob(ctx, channelstore.ReopenJobRequest{
		EventID: ev.ID, Kind: automation.JobWebhookDelivery, TargetID: 1,
		Owner: automation.Owner{AccountID: acc, OwnerUserID: user.ID},
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.AccountID != acc || reopened.OwnerUserID != user.ID {
		t.Fatalf("a reopen changed its binding to (%q, %d)", reopened.AccountID, reopened.OwnerUserID)
	}
}

func TestIdempotencyKeysDifferAcrossOwners(t *testing.T) {
	// Two users copying the same remote message into the same destination must
	// not suppress each other: a dedup key that omits the owner makes the second
	// user's publish a silent no-op.
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)

	keyA, err := store.PublishIdempotencyKey(ctx, userA.ID, accA, "workflow-publication-v2", "official", 777, 42, "dest-1")
	if err != nil {
		t.Fatalf("key for alice: %v", err)
	}
	keyB, err := store.PublishIdempotencyKey(ctx, userB.ID, accB, "workflow-publication-v2", "official", 777, 42, "dest-1")
	if err != nil {
		t.Fatalf("key for bob: %v", err)
	}
	if keyA == keyB {
		t.Fatalf("two owners publishing the same remote message share the idempotency key %q", keyA)
	}

	// And the same owner asking twice gets the same key back: that is what makes
	// a retry of one publish one publish.
	again, err := store.PublishIdempotencyKey(ctx, userA.ID, accA, "workflow-publication-v2", "official", 777, 42, "dest-1")
	if err != nil {
		t.Fatalf("key again: %v", err)
	}
	if again != keyA {
		t.Fatalf("the same owner and message gave %q then %q", keyA, again)
	}
}

func TestPausingAnAccountPausesOnlyItsOwnJobs(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	userA := createUser(t, store, "alice")
	userB := createUser(t, store, "bob")
	accA := ownerAccount(t, store, userA.ID, "official", channelstore.NetworkTelegram, 1)
	accB := ownerAccount(t, store, userB.ID, "official", channelstore.NetworkTelegram, 2)

	evA := eventFor(ownedChannel(t, store, accA, "official", 1000))
	evA.ID = "evt_a"
	evA.SourceAccountID, evA.AccountID = accA, accA
	evB := eventFor(ownedChannel(t, store, accB, "official", 2000))
	evB.ID = "evt_b"
	evB.SourceAccountID, evB.AccountID = accB, accB
	for _, ev := range []automation.MessageEvent{evA, evB} {
		if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
			t.Fatalf("insert %s: %v", ev.ID, err)
		}
	}
	jobA, _, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
		EventID: evA.ID, Kind: automation.JobWebhookDelivery, TargetID: 1,
		MaxAttempts: 3, Owner: automation.Owner{AccountID: accA, OwnerUserID: userA.ID},
	})
	if err != nil {
		t.Fatalf("enqueue a: %v", err)
	}
	if _, _, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
		EventID: evB.ID, Kind: automation.JobWebhookDelivery, TargetID: 2,
		MaxAttempts: 3, Owner: automation.Owner{AccountID: accB, OwnerUserID: userB.ID},
	}); err != nil {
		t.Fatalf("enqueue b: %v", err)
	}

	// Alice's account disconnects.
	if err := store.PauseAccountJobs(ctx, accA); err != nil {
		t.Fatalf("PauseAccountJobs: %v", err)
	}

	if _, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "wa", Lease: time.Minute, AccountID: accA,
	}); err != nil {
		t.Fatalf("claim a: %v", err)
	} else if ok {
		t.Fatal("a paused account's jobs must not be claimable")
	}

	claimed, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "wb", Lease: time.Minute, AccountID: accB,
	})
	if err != nil {
		t.Fatalf("claim b: %v", err)
	}
	if !ok {
		t.Fatal("pausing one account must not stop another account's jobs")
	}
	if claimed.AccountID != accB {
		t.Fatalf("claimed job bound to %q, want %q", claimed.AccountID, accB)
	}

	// Reauthentication resumes exactly the paused work.
	if err := store.ResumeAccountJobs(ctx, accA); err != nil {
		t.Fatalf("ResumeAccountJobs: %v", err)
	}
	resumed, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "wa", Lease: time.Minute, AccountID: accA,
	})
	if err != nil {
		t.Fatalf("claim after resume: %v", err)
	}
	if !ok || resumed.ID != jobA.ID {
		t.Fatalf("after reauthentication the account's own job must resume (ok=%v id=%d)", ok, resumed.ID)
	}
}

func TestRemovingAnAccountStopsFutureExecution(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "alice")
	acc := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 1)
	ev := eventFor(ownedChannel(t, store, acc, "official", 1000))
	ev.ID = "evt_removed"
	ev.SourceAccountID, ev.AccountID = acc, acc
	if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, _, err := store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
		EventID: ev.ID, Kind: automation.JobWebhookDelivery, TargetID: 1,
		MaxAttempts: 3, Owner: automation.Owner{AccountID: acc, OwnerUserID: user.ID},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := store.Delete(ctx, user.ID, acc); err != nil {
		t.Fatalf("Delete account: %v", err)
	}

	paused, err := store.PausedAutomationJobs(ctx, 0)
	if err != nil {
		t.Fatalf("PausedAutomationJobs: %v", err)
	}
	if len(paused) != 1 {
		t.Fatalf("removing an account must leave its work paused, got %d paused", len(paused))
	}
	if _, ok, err := store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: "w", Lease: time.Minute, AccountID: acc,
	}); err != nil {
		t.Fatalf("claim: %v", err)
	} else if ok {
		t.Fatal("work of a removed account must never run again")
	}
}

func TestUnresolvedLegacyWebhookDoesNotSubscribeToAnyAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := createUser(t, store, "legacy-hook-owner")
	channelID := sourceChannel(t, store, "official", 4242)
	hook := webhook("legacy", "https://receiver.example.com/hook")
	hook.SourceChannelIDs = []int64{channelID}
	created, err := store.CreateWebhook(ctx, owner.ID, hook, sealedSecret(t, "whsec_legacy"))
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}

	ids, err := store.WebhookIDsForChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("WebhookIDsForChannel: %v", err)
	}
	for _, id := range ids {
		if id == created.ID {
			t.Fatal("an unresolved webhook must not subscribe to a legacy/quarantined source")
		}
	}
}

func TestWebhookDeliveryRequiresAnExactOwnerAndAccountMatch(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "webhook-owner")
	account := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 31337)
	channelID := ownedChannel(t, store, account, "official", 5151)

	hook := webhook("bound", "https://receiver.example.com/hook")
	hook.AccountID = account
	hook.SourceChannelIDs = []int64{channelID}
	created, err := store.CreateWebhook(ctx, user.ID, hook, sealedSecret(t, "whsec_bound"))
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	event := eventFor(channelID)
	event.ID = "evt_webhook_bound"
	event.AccountID = account
	event.SourceAccountID = account
	event = event.WithOwner(user.ID)
	if _, err := store.InsertMessageEvent(ctx, event); err != nil {
		t.Fatalf("InsertMessageEvent: %v", err)
	}

	delivery, err := store.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
		WebhookID: created.ID, EventID: event.ID, Status: "pending",
		OwnerUserID: user.ID + 99, AccountID: "acc_wrong",
	})
	if err == nil {
		t.Fatalf("CreateWebhookDelivery accepted a mismatched execution identity: %+v", delivery)
	}
}
