package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/automation/queue"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/sending"
	"github.com/mirza/telesync/internal/staging"
)

// A background publication is recorded against the account it runs under: the
// idempotency record names one, and the account's permissions are read again at
// execution time so a grant that has since been withdrawn stops the next
// publication. The publisher the daemon builds for the background therefore has
// to carry a resolver.
//
// Without one the interactive route would never reveal the problem — it passes
// its own actor — and every background publish is refused with "this deployment
// cannot resolve the account a background publication runs under".
func TestBackgroundPublisherResolvesTheAccountItPublishesUnder(t *testing.T) {
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	publisher := newBackgroundPublisher(nil, store, nil, t.Logf)
	if publisher.Actors == nil {
		t.Fatal("the background publisher cannot resolve the account it publishes under")
	}
	// The resolver is the store the rest of the pipeline reads, so an account
	// is looked up on every publication rather than carried in from a request.
	if _, found, err := publisher.Actors.GetUser(context.Background(), 1); err != nil {
		t.Fatalf("resolving an account: %v", err)
	} else if found {
		t.Fatal("an empty store resolved an account that does not exist")
	}
}

// sendingWorker is one connection that accepts a send.
type sendingWorker struct {
	sends []rpc.SendRequest
}

func (w *sendingWorker) Message(context.Context, int64, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{}, errors.New("not used")
}

func (w *sendingWorker) Media(context.Context, int64, int, int, rpc.MediaSlice) (rpc.MediaStream, error) {
	return rpc.MediaStream{}, errors.New("not used")
}

func (w *sendingWorker) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	return rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Permissions: rpc.Permissions{New: true, Read: true},
	}}, nil
}

func (w *sendingWorker) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	w.sends = append(w.sends, req)
	return rpc.SendResult{Message: &rpc.Message{ID: 77}, ChannelID: "98"}, nil
}

// TestTheDaemonWiresDirectSendingEndToEnd pins the wiring the daemon performs.
//
// It is a behaviour test rather than a list of non-nil fields, because the way
// this goes wrong is a field that is present in the type and absent in the
// constructor: the send is accepted, queued and performed, and a hole anywhere
// in that chain stops it. The interactive route would not reveal the problem —
// it never reaches the background half.
func TestTheDaemonWiresDirectSendingEndToEnd(t *testing.T) {
	dir := t.TempDir()
	store, err := channelstore.Open(filepath.Join(dir, "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	user, err := store.CreateUser(ctx, "Sender", "sender", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "cat", ChannelID: 98, PeerType: channelstore.PeerChannel,
		Title: "Destination", MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := store.ReplacePermissions(ctx, user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{
			channelstore.PermissionContentPublish, channelstore.PermissionChannelsView,
		},
	}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}

	area, err := staging.Open(filepath.Join(dir, "send-staging"), 1<<20)
	if err != nil {
		t.Fatalf("staging.Open: %v", err)
	}
	worker := &sendingWorker{}
	publisher := newBackgroundPublisher(publishing.RegistryFunc(func(key string) (publishing.Worker, bool) {
		return worker, key == "cat"
	}), store, nil, t.Logf)
	enqueuer := queue.NewEnqueuer(store, 5)

	send := newSendService(appconfig.SendConfig{HistoryRetentionDays: 30},
		publisher, store, area, enqueuer, t.Logf)
	if send.Sender == nil || send.Operations == nil || send.Staging == nil || send.Jobs == nil {
		t.Fatal("the daemon's send service is missing part of what a send needs")
	}
	if send.HistoryRetention != 30*24*time.Hour {
		t.Fatalf("retention = %v, want the configured window", send.HistoryRetention)
	}

	authr, err := store.AuthorizerFor(ctx, user.ID)
	if err != nil {
		t.Fatalf("AuthorizerFor: %v", err)
	}
	actor := publishing.Actor{UserID: user.ID, Auth: authr}

	accepted, err := send.Accept(ctx, actor, sending.AcceptInput{
		ClientKey:            "cat",
		DestinationChannelID: 98,
		IdempotencyKey:       "wiring-001",
		Request:              publishing.DirectSendRequest{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !accepted.Created {
		t.Fatal("a new send was reported as already known")
	}
	if len(worker.sends) != 0 {
		t.Fatal("the message was sent during the request rather than in the background")
	}

	// The job the queue would hand a worker is what performs it.
	if err := send.Execute(ctx, automation.Job{EventID: accepted.Operation.PublicID, Attempt: 1}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(worker.sends) != 1 {
		t.Fatalf("%d messages were sent, want 1", len(worker.sends))
	}

	op, found, err := store.SendOperation(ctx, accepted.Operation.PublicID)
	if err != nil || !found {
		t.Fatalf("SendOperation: found=%v err=%v", found, err)
	}
	if op.Status != channelstore.SendSent {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendSent)
	}
	if op.RemoteMessageID != 77 {
		t.Fatalf("remote message id = %d, want the one the destination confirmed", op.RemoteMessageID)
	}
}
