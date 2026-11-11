package channelstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
)

func messageEditOp(userID, target int64, key, fingerprint string) channelstore.MessageEditOperation {
	return channelstore.MessageEditOperation{
		UserID: userID, ClientKey: "cat", DestinationChannelID: "98",
		TargetMessageID: target, RequestPayload: `{"text":"replacement"}`,
		MediaKind: "", IdempotencyKey: key, RequestFingerprint: fingerprint,
	}
}

func TestMessageEditOperationIdempotencyIsScopedToPrincipalAndWholeRequest(t *testing.T) {
	store, userID := sendStore(t)
	ctx := context.Background()

	first, created, err := store.BeginMessageEditOperation(ctx, messageEditOp(userID, 11, "edit-key", "fingerprint-a"))
	if err != nil || !created {
		t.Fatalf("BeginMessageEditOperation: created=%v err=%v", created, err)
	}
	second, created, err := store.BeginMessageEditOperation(ctx, messageEditOp(userID, 11, "edit-key", "fingerprint-a"))
	if err != nil || created || second.PublicID != first.PublicID {
		t.Fatalf("same request replay: op=%+v created=%v err=%v; want existing operation", second, created, err)
	}
	if _, _, err := store.BeginMessageEditOperation(ctx, messageEditOp(userID, 12, "edit-key", "fingerprint-target12")); !errors.Is(err, channelstore.ErrMessageEditIdempotencyConflict) {
		t.Fatalf("same key for another target error = %v, want idempotency conflict", err)
	}
	if _, _, err := store.BeginMessageEditOperation(ctx, messageEditOp(userID, 11, "edit-key", "fingerprint-b")); !errors.Is(err, channelstore.ErrMessageEditIdempotencyConflict) {
		t.Fatalf("same key for different replacement error = %v, want idempotency conflict", err)
	}

	secondUser, err := store.CreateUser(ctx, "Second Editor", "second-editor", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", true)
	if err != nil {
		t.Fatalf("create second principal: %v", err)
	}
	otherPrincipal, created, err := store.BeginMessageEditOperation(ctx, messageEditOp(secondUser.ID, 11, "edit-key", "fingerprint-a"))
	if err != nil || !created || otherPrincipal.PublicID == first.PublicID {
		t.Fatalf("another principal's key scope: op=%+v created=%v err=%v", otherPrincipal, created, err)
	}
}

func TestMessageEditOperationLifecycleNeverChangesTargetMessageID(t *testing.T) {
	store, userID := sendStore(t)
	ctx := context.Background()
	op, _, err := store.BeginMessageEditOperation(ctx, messageEditOp(userID, 11, "", "fingerprint"))
	if err != nil {
		t.Fatalf("BeginMessageEditOperation: %v", err)
	}
	if err := store.StartMessageEditOperation(ctx, op.ID, 1, "editing_message"); err != nil {
		t.Fatalf("StartMessageEditOperation: %v", err)
	}
	next := time.Now().UTC().Add(time.Minute)
	if err := store.RetryMessageEditOperation(ctx, op.ID, 1, next, "editing_message", "flood_wait", "Telegram is rate limiting this client."); err != nil {
		t.Fatalf("RetryMessageEditOperation: %v", err)
	}
	if err := store.FinishMessageEditOperation(ctx, op.ID, channelstore.MessageEditEdited, 11, "photo", "", ""); err != nil {
		t.Fatalf("FinishMessageEditOperation: %v", err)
	}
	got, found, err := store.MessageEditOperation(ctx, op.PublicID)
	if err != nil || !found {
		t.Fatalf("MessageEditOperation: found=%v err=%v", found, err)
	}
	if got.Status != channelstore.MessageEditEdited || got.TargetMessageID != 11 || got.RemoteMessageID != 11 || got.ResultMediaKind != "photo" {
		t.Fatalf("completed operation = %+v; target and returned message id must remain 11", got)
	}
	if err := store.FinishMessageEditOperation(ctx, op.ID, channelstore.MessageEditEdited, 12, "photo", "", ""); !errors.Is(err, channelstore.ErrInvalidMessageEditOperation) {
		t.Fatalf("mismatched remote message id error = %v, want invalid edit operation", err)
	}
}
