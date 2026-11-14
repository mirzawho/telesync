package channelstore_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

func TestChannelCreationIdempotencyReplaysSamePayloadAndRejectsKeyReuse(t *testing.T) {
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// The operation is recorded against an account, so one must exist. The
	// subject here is idempotency, not ownership, but the binding is real
	// rather than a bare number the schema would have to tolerate.
	user := createUser(t, store, "creator")
	acc := ownerAccount(t, store, user.ID, "the-cat", channelstore.NetworkCustom, 1)

	first, created, err := store.BeginChannelCreation(ctx, user.ID, acc, "the-cat", "request-1", "fingerprint-a", `{"title":"News"}`)
	if err != nil || !created {
		t.Fatalf("first BeginChannelCreation = (%+v, %v, %v), want new operation", first, created, err)
	}
	if first.Status != channelstore.ChannelCreationPending {
		t.Fatalf("initial status = %q", first.Status)
	}

	replay, created, err := store.BeginChannelCreation(ctx, user.ID, acc, "the-cat", "request-1", "fingerprint-a", `{"title":"News"}`)
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("identical retry = (%+v, %v, %v), want existing operation", replay, created, err)
	}

	_, _, err = store.BeginChannelCreation(ctx, user.ID, acc, "the-cat", "request-1", "fingerprint-b", `{"title":"Other"}`)
	if !errors.Is(err, channelstore.ErrChannelCreationIdempotencyConflict) {
		t.Fatalf("different payload error = %v, want idempotency conflict", err)
	}
}

func TestChannelCreationOperationPersistsRemoteIdentityAndPartialSteps(t *testing.T) {
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	user := createUser(t, store, "creator")
	acc := ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 2)

	op, _, err := store.BeginChannelCreation(ctx, user.ID, acc, "official", "request-2", "fingerprint", `{"title":"News"}`)
	if err != nil {
		t.Fatal(err)
	}
	op.Status = channelstore.ChannelCreationPartial
	op.RemoteChannelID = 987654321
	op.ChannelCreated = true
	op.UsernameAssigned = false
	op.AvatarUpdated = true
	op.LocallyRegistered = true
	op.ErrorCode = "channel_username_occupied"
	if err := store.UpdateChannelCreation(ctx, op); err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.GetChannelCreation(ctx, user.ID, "official", "request-2")
	if err != nil || !ok {
		t.Fatalf("GetChannelCreation = (%+v, %v, %v)", got, ok, err)
	}
	if got.RemoteChannelID != op.RemoteChannelID || !got.ChannelCreated || got.UsernameAssigned || !got.AvatarUpdated || !got.LocallyRegistered || got.ErrorCode != op.ErrorCode {
		t.Fatalf("persisted operation = %+v", got)
	}
}
