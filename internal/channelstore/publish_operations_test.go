package channelstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
)

// Publishing is the one operation in this application with a side effect the
// other end cannot undo. The record below is what makes a retry safe: the same
// key never posts twice, and a key that already named a different request is
// refused rather than silently re-run.

// publishStore opens a store with one account in it: the attempts reference a
// real user, like every other per-account table here.
func publishStore(t *testing.T, mutate ...func(*channelstore.Options)) (*channelstore.Store, int64) {
	t.Helper()
	store := open(t, mutate...)
	user, err := store.CreateUser(context.Background(), "Operator", "operator", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return store, user.ID
}

func publishOp(key, hash string, userID int64) channelstore.PublishOperation {
	return channelstore.PublishOperation{
		UserID:          userID,
		IdempotencyKey:  key,
		RequestHash:     hash,
		DestinationType: "telegram",
		DestinationID:   "123456789",
	}
}

func TestAPublishOperationIsRecordedAsInFlight(t *testing.T) {
	store, user := publishStore(t)
	ctx := context.Background()
	op, created, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if !created {
		t.Fatal("the first attempt must be reported as created")
	}
	if op.Status != channelstore.PublishInFlight {
		t.Fatalf("status = %q, want in_flight", op.Status)
	}
	if op.ID == 0 {
		t.Fatal("the stored operation must have an id")
	}
	got, ok, err := store.GetPublishOperation(ctx, user, "key-1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ID != op.ID || got.RequestHash != "hash-a" {
		t.Fatalf("stored = %+v", got)
	}
}

func TestTheSameKeyWithADifferentRequestIsAConflict(t *testing.T) {
	store, user := publishStore(t)
	ctx := context.Background()
	if _, _, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user)); err != nil {
		t.Fatal(err)
	}
	_, created, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-b", user))
	if !errors.Is(err, channelstore.ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
	if created {
		t.Fatal("a conflict must not report a created operation")
	}
}

func TestAConfirmedSuccessIsReplayedNotRepeated(t *testing.T) {
	store, user := publishStore(t)
	ctx := context.Background()
	first, _, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublishOperation(ctx, first.ID, channelstore.PublishSucceeded, `{"message":{"id":7}}`, ""); err != nil {
		t.Fatal(err)
	}
	again, created, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatalf("begin again: %v", err)
	}
	if created {
		t.Fatal("a retry of a confirmed success must not report a new attempt")
	}
	if again.Status != channelstore.PublishSucceeded {
		t.Fatalf("status = %q, want succeeded", again.Status)
	}
	if again.Result != `{"message":{"id":7}}` {
		t.Fatalf("result = %q", again.Result)
	}
}

func TestAFailedAttemptMayBeRetried(t *testing.T) {
	store, user := publishStore(t)
	ctx := context.Background()
	first, _, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublishOperation(ctx, first.ID, channelstore.PublishFailed, "", "publish_failed"); err != nil {
		t.Fatal(err)
	}
	retry, created, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatalf("begin again: %v", err)
	}
	if !created {
		t.Fatal("a failed attempt must be retryable")
	}
	if retry.Status != channelstore.PublishInFlight {
		t.Fatalf("status = %q, want in_flight", retry.Status)
	}
	if retry.ID != first.ID {
		t.Fatalf("a retry must reuse the row: %d vs %d", retry.ID, first.ID)
	}
}

func TestAnUncertainOutcomeIsNeverReRun(t *testing.T) {
	store, user := publishStore(t)
	ctx := context.Background()
	first, _, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublishOperation(ctx, first.ID, channelstore.PublishUnknown, "", "publish_outcome_unknown"); err != nil {
		t.Fatal(err)
	}
	again, created, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatalf("begin again: %v", err)
	}
	if created {
		t.Fatal("an uncertain outcome must not be re-executed")
	}
	if again.Status != channelstore.PublishUnknown {
		t.Fatalf("status = %q, want unknown", again.Status)
	}
}

func TestAnInFlightAttemptIsNotReRunConcurrently(t *testing.T) {
	store, user := publishStore(t)
	ctx := context.Background()
	if _, _, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user)); err != nil {
		t.Fatal(err)
	}
	_, created, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatalf("begin again: %v", err)
	}
	if created {
		t.Fatal("a request still in flight must not start a second attempt")
	}
}

func TestTheIdempotencyKeyIsScopedToTheAccount(t *testing.T) {
	store, user := publishStore(t)
	ctx := context.Background()
	accountA := mustAccount(t, store, newAccount(user, accountID("a"), "official", 8101))
	accountB := mustAccount(t, store, newAccount(user, accountID("b"), "cat", 8102))
	opA := publishOp("key-1", "hash-a", user)
	opA.AccountID = accountA.ID
	firstA, createdA, err := store.BeginPublishOperation(ctx, opA)
	if err != nil || !createdA {
		t.Fatalf("begin account A: created=%v err=%v", createdA, err)
	}
	opB := publishOp("key-1", "hash-a", user)
	opB.AccountID = accountB.ID
	firstB, createdB, err := store.BeginPublishOperation(ctx, opB)
	if err != nil || !createdB {
		t.Fatalf("same user on another account must be free to use the same key: created=%v err=%v", createdB, err)
	}
	if firstA.ID == firstB.ID {
		t.Fatalf("account A/B operations share row %d", firstA.ID)
	}
	if replayA, created, err := store.BeginPublishOperation(ctx, opA); err != nil || created || replayA.ID != firstA.ID {
		t.Fatalf("retry on account A = %+v, created=%v err=%v; want A's row %d",
			replayA, created, err, firstA.ID)
	}
	if replayB, created, err := store.BeginPublishOperation(ctx, opB); err != nil || created || replayB.ID != firstB.ID {
		t.Fatalf("retry on account B = %+v, created=%v err=%v; want B's row %d",
			replayB, created, err, firstB.ID)
	}
}

func TestFinishingAnOperationThatDoesNotExistIsAnError(t *testing.T) {
	store, _ := publishStore(t)
	err := store.FinishPublishOperation(context.Background(), 999, channelstore.PublishSucceeded, "{}", "")
	if !errors.Is(err, channelstore.ErrPublishOperationNotFound) {
		t.Fatalf("err = %v, want ErrPublishOperationNotFound", err)
	}
}

func TestPublishOperationStampsItsOwnClock(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	store, user := publishStore(t, func(o *channelstore.Options) {
		o.Now = func() time.Time { return now }
	})
	ctx := context.Background()
	op, _, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user))
	if err != nil {
		t.Fatal(err)
	}
	if !op.CreatedAt.Equal(now) || !op.UpdatedAt.Equal(now) {
		t.Fatalf("created=%v updated=%v, want %v", op.CreatedAt, op.UpdatedAt, now)
	}
}

func TestDeletingAnAccountRemovesItsPublishOperations(t *testing.T) {
	store, user := publishStore(t)
	ctx := context.Background()
	if _, _, err := store.BeginPublishOperation(ctx, publishOp("key-1", "hash-a", user)); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetPublishOperation(ctx, user, "key-1"); err != nil || ok {
		t.Fatalf("the record must go with the account: ok=%v err=%v", ok, err)
	}
}
