package channelstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
)

// A direct send is accepted in one request and performed in another, so the
// record below is the only thing that survives between them: what was asked
// for, whose it was, where it was going, which attachment it needs, and what
// the outcome was. Without it a restart loses a message the caller was told
// was accepted, and a retry can post the same thing twice.

func sendStore(t *testing.T) (*channelstore.Store, int64) {
	t.Helper()
	store := open(t)
	user, err := store.CreateUser(context.Background(), "Sender", "sender", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return store, user.ID
}

func sendOp(userID int64, key, hash string) channelstore.SendOperation {
	return channelstore.SendOperation{
		UserID:               userID,
		ClientKey:            "cat",
		DestinationChannelID: "98",
		RequestPayload:       `{"text":"hello"}`,
		IdempotencyKey:       key,
		RequestFingerprint:   hash,
		RandomID:             424242,
	}
}

// Test A. Accepting a send durably records it as queued, with the attachment
// it will need and the identity a retry must reuse.
func TestAAcceptedSendIsRecordedAsQueuedWithItsAttachment(t *testing.T) {
	store, user := sendStore(t)
	op := sendOp(user, "key-a", "hash-a")
	op.MediaKind = "voice"
	op.MediaStagingName = "0123456789abcdef0123456789abcdef"
	op.MediaFilename = "note.ogg"
	op.MediaMimeType = "audio/ogg"
	op.MediaSize = 2048
	op.MediaSHA256 = "digest"

	got, created, err := store.BeginSendOperation(context.Background(), op)
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if !created {
		t.Fatal("a new operation was reported as already known")
	}
	if got.Status != channelstore.SendQueued {
		t.Fatalf("status = %q, want %q", got.Status, channelstore.SendQueued)
	}
	if got.PublicID == "" {
		t.Fatal("the operation has no public id; the caller was promised one")
	}
	if got.MediaStagingName != op.MediaStagingName || got.MediaFilename != "note.ogg" ||
		got.MediaSHA256 != "digest" || got.MediaKind != "voice" {
		t.Fatalf("the staged attachment was not carried into the record: %+v", got)
	}
	if got.RandomID != 424242 {
		t.Fatalf("random id = %d, want the one that was persisted before any attempt", got.RandomID)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("the operation carries no creation time")
	}
}

// Test B. The same key with the same request is the same operation. It is
// returned, not repeated: no second job, no second upload, no second message.
func TestBTheSameKeyAndRequestIsTheSameOperation(t *testing.T) {
	store, user := sendStore(t)
	first, created, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-b", "hash-b"))
	if err != nil || !created {
		t.Fatalf("BeginSendOperation: created=%v err=%v", created, err)
	}
	second, created, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-b", "hash-b"))
	if err != nil {
		t.Fatalf("second BeginSendOperation: %v", err)
	}
	if created {
		t.Fatal("the second request created a second operation")
	}
	if second.PublicID != first.PublicID {
		t.Fatalf("the second request is operation %q, not %q", second.PublicID, first.PublicID)
	}
}

// Test C. The same key naming a different request is a conflict and nothing
// else: it never becomes a second send, and it is never answered with the
// earlier operation as though it were this one.
func TestCTheSameKeyForADifferentRequestConflicts(t *testing.T) {
	store, user := sendStore(t)
	if _, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-c", "hash-c")); err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	_, created, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-c", "hash-other"))
	if !errors.Is(err, channelstore.ErrSendIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrSendIdempotencyConflict", err)
	}
	if created {
		t.Fatal("a conflicting request created an operation")
	}
}

// Test D. A key belongs to one account. Another account's key of the same
// spelling is a different operation and never sees the first one's result.
func TestDAKeyIsScopedToItsAccount(t *testing.T) {
	store, user := sendStore(t)
	other, err := store.CreateUser(context.Background(), "Other", "other", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	first, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "shared", "hash"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	second, created, err := store.BeginSendOperation(context.Background(), sendOp(other.ID, "shared", "hash"))
	if err != nil {
		t.Fatalf("second BeginSendOperation: %v", err)
	}
	if !created {
		t.Fatal("one account's key was answered with another account's operation")
	}
	if second.PublicID == first.PublicID {
		t.Fatal("two accounts share one operation")
	}
}

// Test E. A request with no key is still recorded — the caller was promised an
// operation to poll — but it joins nothing and nothing joins it.
func TestEARequestWithNoKeyIsRecordedAndJoinsNothing(t *testing.T) {
	store, user := sendStore(t)
	first, created, err := store.BeginSendOperation(context.Background(), sendOp(user, "", "hash"))
	if err != nil || !created {
		t.Fatalf("BeginSendOperation: created=%v err=%v", created, err)
	}
	second, created, err := store.BeginSendOperation(context.Background(), sendOp(user, "", "hash"))
	if err != nil {
		t.Fatalf("second BeginSendOperation: %v", err)
	}
	if !created {
		t.Fatal("a request with no idempotency key joined an earlier one")
	}
	if second.PublicID == first.PublicID {
		t.Fatal("two keyless requests share one operation")
	}
}

// Test F. The public id is what a caller is handed, so it must not be the row
// id: a sequential id tells a caller how many other operations exist and makes
// the next one guessable.
func TestFThePublicIDIsOpaque(t *testing.T) {
	store, user := sendStore(t)
	first, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "", "hash"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	second, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "", "hash"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if first.PublicID == second.PublicID {
		t.Fatalf("two operations share the public id %q", first.PublicID)
	}
	if first.PublicID == "1" || first.PublicID == "2" || second.PublicID == "2" {
		t.Fatal("the public id is a row number")
	}
}

// Test G. Starting an attempt records that it is in progress and which one it
// is, so a poll between "claimed" and "answered" says processing rather than
// leaving the caller on the acceptance answer forever.
func TestGStartingAnAttemptRecordsProcessing(t *testing.T) {
	store, user := sendStore(t)
	op, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-g", "hash-g"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if err := store.StartSendOperation(context.Background(), op.ID, 1, "uploading_media"); err != nil {
		t.Fatalf("StartSendOperation: %v", err)
	}
	got, found, err := store.SendOperation(context.Background(), op.PublicID)
	if err != nil || !found {
		t.Fatalf("SendOperation: found=%v err=%v", found, err)
	}
	if got.Status != channelstore.SendProcessing {
		t.Fatalf("status = %q, want %q", got.Status, channelstore.SendProcessing)
	}
	if got.Stage != "uploading_media" {
		t.Fatalf("stage = %q, want the stage the worker named", got.Stage)
	}
	if got.AttemptCount != 1 {
		t.Fatalf("attempt = %d, want 1", got.AttemptCount)
	}
	if got.StartedAt == nil {
		t.Fatal("the operation records no start time")
	}
}

// Test H. A confirmed send is terminal and carries the message the destination
// produced. That id is the whole point of polling: it is what a caller uses to
// find the message again.
func TestHAConfirmedSendRecordsTheDestinationMessage(t *testing.T) {
	store, user := sendStore(t)
	op, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-h", "hash-h"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if err := store.FinishSendOperation(context.Background(), op.ID, channelstore.SendSent, 1234, "sent", "", ""); err != nil {
		t.Fatalf("FinishSendOperation: %v", err)
	}
	got, _, err := store.SendOperation(context.Background(), op.PublicID)
	if err != nil {
		t.Fatalf("SendOperation: %v", err)
	}
	if got.Status != channelstore.SendSent {
		t.Fatalf("status = %q, want %q", got.Status, channelstore.SendSent)
	}
	if got.RemoteMessageID != 1234 {
		t.Fatalf("remote message id = %d, want 1234", got.RemoteMessageID)
	}
	if got.CompletedAt == nil {
		t.Fatal("the operation records no completion time")
	}
}

// Test I. A failure that will be tried again is not terminal: the reason and
// the next attempt are recorded and the operation stays live.
func TestIARecoverableFailureLeavesTheOperationLive(t *testing.T) {
	store, user := sendStore(t)
	op, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-i", "hash-i"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	next := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	if err := store.RetrySendOperation(context.Background(), op.ID, 1, next, "sending_message",
		"rate_limited", "Telegram is rate limiting this client."); err != nil {
		t.Fatalf("RetrySendOperation: %v", err)
	}
	got, _, err := store.SendOperation(context.Background(), op.PublicID)
	if err != nil {
		t.Fatalf("SendOperation: %v", err)
	}
	if got.Status != channelstore.SendRetrying {
		t.Fatalf("status = %q, want %q", got.Status, channelstore.SendRetrying)
	}
	if got.LastErrorCode != "rate_limited" {
		t.Fatalf("error code = %q, want the reason recorded", got.LastErrorCode)
	}
	if got.CompletedAt != nil {
		t.Fatal("a retrying operation was recorded as complete")
	}
	if got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(next) {
		t.Fatalf("next attempt = %v, want %v", got.NextAttemptAt, next)
	}
}

// Test J. An outcome the other end never confirmed is its own state. Calling
// it "failed" would invite a retry that posts the message a second time.
func TestJAnUnconfirmedOutcomeIsNotAFailure(t *testing.T) {
	store, user := sendStore(t)
	op, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-j", "hash-j"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if err := store.FinishSendOperation(context.Background(), op.ID, channelstore.SendUnknown, 0, "sending_message",
		"publish_outcome_unknown", "The destination never confirmed."); err != nil {
		t.Fatalf("FinishSendOperation: %v", err)
	}
	got, _, err := store.SendOperation(context.Background(), op.PublicID)
	if err != nil {
		t.Fatalf("SendOperation: %v", err)
	}
	if got.Status != channelstore.SendUnknown {
		t.Fatalf("status = %q, want %q", got.Status, channelstore.SendUnknown)
	}
	if got.RemoteMessageID != 0 {
		t.Fatalf("remote message id = %d; nothing was confirmed, so nothing may be claimed", got.RemoteMessageID)
	}
}

// Test K. Cleanup may only remove an attachment nothing is waiting for. The
// operations that still hold one are listed here, which is what stops a
// retention sweep from deleting a file a retrying job is about to upload.
func TestKOperationsHoldingAnAttachmentAreListable(t *testing.T) {
	store, user := sendStore(t)
	withMedia := sendOp(user, "key-k1", "hash-k1")
	withMedia.MediaStagingName = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, _, err := store.BeginSendOperation(context.Background(), withMedia); err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if _, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-k2", "hash-k2")); err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}

	held, err := store.SendOperationsHoldingMedia(context.Background())
	if err != nil {
		t.Fatalf("SendOperationsHoldingMedia: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("held = %d operations, want 1", len(held))
	}
	if held[0].MediaStagingName != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("held attachment = %q", held[0].MediaStagingName)
	}
}

// Test L. Retention is bounded: finished operations past their keep window are
// listed and removable, and one still in flight is never among them.
func TestLRetentionFindsOnlyFinishedOperationsPastTheirWindow(t *testing.T) {
	store, user := sendStore(t)
	done, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-l1", "hash-l1"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if err := store.FinishSendOperation(context.Background(), done.ID, channelstore.SendSent, 7, "sent", "", ""); err != nil {
		t.Fatalf("FinishSendOperation: %v", err)
	}
	live, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-l2", "hash-l2"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}

	expired, err := store.ExpiredSendOperations(context.Background(), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("ExpiredSendOperations: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expired = %d operations, want 1", len(expired))
	}
	if expired[0].ID != done.ID {
		t.Fatalf("the operation past its window is %d, want %d", expired[0].ID, done.ID)
	}
	if err := store.DeleteSendOperation(context.Background(), done.ID); err != nil {
		t.Fatalf("DeleteSendOperation: %v", err)
	}
	if _, found, err := store.SendOperation(context.Background(), done.PublicID); err != nil || found {
		t.Fatalf("the deleted operation is still readable: found=%v err=%v", found, err)
	}
	if _, found, err := store.SendOperation(context.Background(), live.PublicID); err != nil || !found {
		t.Fatalf("the live operation was deleted too: found=%v err=%v", found, err)
	}
}

// Test M. An operation is addressed by its public id alone. A caller who names
// one that is not theirs is told nothing exists — not that it exists and is
// forbidden, and not somebody else's message.
func TestMAnUnknownPublicIDIsNotFound(t *testing.T) {
	store, user := sendStore(t)
	op, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-m", "hash-m"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if _, found, err := store.SendOperation(context.Background(), "send_not-a-real-operation"); err != nil {
		t.Fatalf("SendOperation: %v", err)
	} else if found {
		t.Fatal("an id that was never issued resolved to an operation")
	}
	if _, found, err := store.SendOperation(context.Background(), op.PublicID); err != nil || !found {
		t.Fatalf("the issued id did not resolve: found=%v err=%v", found, err)
	}
}

// Test N. The MTProto random id is written before the first attempt and is not
// rewritten by a retry. It is the one thing that makes a resend after a lost
// confirmation the same message rather than a second one.
func TestNTheRandomIDSurvivesEveryAttempt(t *testing.T) {
	store, user := sendStore(t)
	op, _, err := store.BeginSendOperation(context.Background(), sendOp(user, "key-n", "hash-n"))
	if err != nil {
		t.Fatalf("BeginSendOperation: %v", err)
	}
	if err := store.StartSendOperation(context.Background(), op.ID, 1, "sending_message"); err != nil {
		t.Fatalf("StartSendOperation: %v", err)
	}
	if err := store.RetrySendOperation(context.Background(), op.ID, 1, time.Now().UTC(), "sending_message", "rate_limited", "wait"); err != nil {
		t.Fatalf("RetrySendOperation: %v", err)
	}
	if err := store.StartSendOperation(context.Background(), op.ID, 2, "sending_message"); err != nil {
		t.Fatalf("StartSendOperation: %v", err)
	}
	got, _, err := store.SendOperation(context.Background(), op.PublicID)
	if err != nil {
		t.Fatalf("SendOperation: %v", err)
	}
	if got.RandomID != 424242 {
		t.Fatalf("random id = %d after two attempts; a retry must reuse the one the first attempt sent", got.RandomID)
	}
	if got.AttemptCount != 2 {
		t.Fatalf("attempt = %d, want 2", got.AttemptCount)
	}
}

// Test O. Nothing is recorded against an account that does not exist: an
// operation is a principal's, and a background job that runs later has to be
// able to find that principal again.
func TestOAnOperationNeedsAnAccount(t *testing.T) {
	store, _ := sendStore(t)
	_, _, err := store.BeginSendOperation(context.Background(), sendOp(0, "key-o", "hash-o"))
	if err == nil {
		t.Fatal("an operation with no owning account was accepted")
	}
	if !errors.Is(err, channelstore.ErrInvalidSendOperation) {
		t.Fatalf("err = %v, want ErrInvalidSendOperation", err)
	}
}
