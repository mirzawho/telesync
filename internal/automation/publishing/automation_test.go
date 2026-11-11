package publishing_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// A background publication is not a privileged path. It runs as the account
// that configured the workflow, with that account's permissions as they are at
// execution time — so a withdrawn grant stops publishing immediately, and an
// account that is gone publishes nothing at all.

// fakeWorker is one Telegram connection that accepts every send.
//
// The source message and its attachment are served from page and the media
// fields, so a test can publish a real attachment and still see exactly what
// the destination was handed. A worker that has no page answers with the plain
// post, and a worker that has no media refuses the attachment read: that is
// what makes "the destination never reads the source" visible in a test rather
// than merely intended.
type fakeWorker struct {
	sends       []rpc.SendRequest
	sendErr     error
	albums      []rpc.SendAlbumRequest
	albumResult rpc.SendAlbumResult

	page      rpc.MessagePage
	media     []byte
	mediaType string
	mediaName string
	mediaSize int64
	mediaErr  error
	// mediaReads counts the attachment reads this connection answered. Only the
	// connection that recorded the source message should ever move this.
	mediaReads int
	sendCalls  int
}

func (w *fakeWorker) Message(_ context.Context, channelID int64, messageID int) (rpc.MessagePage, error) {
	if w.page.Messages != nil {
		for _, message := range w.page.Messages {
			if message.ID == messageID {
				return rpc.MessagePage{Channel: w.page.Channel, Messages: []rpc.Message{message}}, nil
			}
		}
		return w.page, nil
	}
	return rpc.MessagePage{
		Channel:  rpc.Channel{ID: channelID},
		Messages: []rpc.Message{{ID: messageID, Text: "hello"}},
	}, nil
}

func (w *fakeWorker) Media(context.Context, int64, int, int, rpc.MediaSlice) (rpc.MediaStream, error) {
	w.mediaReads++
	if w.mediaErr != nil {
		return rpc.MediaStream{}, w.mediaErr
	}
	if w.media == nil {
		return rpc.MediaStream{}, errors.New("not used")
	}
	size := w.mediaSize
	if size == 0 {
		size = int64(len(w.media))
	}
	return rpc.MediaStream{
		MimeType: w.mediaType,
		Filename: w.mediaName,
		Size:     size,
		Body:     io.NopCloser(bytes.NewReader(w.media)),
	}, nil
}

func (w *fakeWorker) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	return rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "555", Permissions: rpc.Permissions{New: true, Read: true},
	}}, nil
}

func (w *fakeWorker) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	w.sendCalls++
	w.sends = append(w.sends, req)
	if w.sendErr != nil {
		return rpc.SendResult{}, w.sendErr
	}
	return rpc.SendResult{Message: &rpc.Message{ID: 4321, Text: req.Text},
		ChannelID: strconv.FormatInt(req.ChannelID, 10)}, nil
}

func (w *fakeWorker) SendAlbum(_ context.Context, req rpc.SendAlbumRequest) (rpc.SendAlbumResult, error) {
	w.albums = append(w.albums, req)
	return w.albumResult, w.sendErr
}

// keyedRegistry hands out one connection per configured client key, so a test
// can hold the source and the destination on genuinely separate connections.
type keyedRegistry map[string]publishing.Worker

func (k keyedRegistry) Worker(key string) (publishing.Worker, bool) {
	w, ok := k[key]
	return w, ok
}

// registry hands out the one connection above under its configured key.
type registry struct {
	worker publishing.Worker
}

func (r registry) Worker(string) (publishing.Worker, bool) {
	if r.worker == nil {
		return nil, false
	}
	return r.worker, true
}

// world is a store with one account, one managed source channel and one
// destination channel, and a publisher wired to it.
type world struct {
	store   *channelstore.Store
	user    channelstore.User
	source  channelstore.Channel
	dest    channelstore.Channel
	worker  *fakeWorker
	service *publishing.Service
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state", "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	user, err := store.CreateUser(ctx, "Publisher", "publisher", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	source, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "source", ChannelID: 555, PeerType: channelstore.PeerChannel,
		Title: "Source", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	dest, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "dest", ChannelID: 777, PeerType: channelstore.PeerChannel,
		Title: "Destination", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	worker := &fakeWorker{}
	return &world{
		store: store, user: user, source: source, dest: dest, worker: worker,
		service: &publishing.Service{
			Workers:   registry{worker: worker},
			Channels:  store,
			Publishes: store,
			Actors:    store,
		},
	}
}

// request is one background publication of the world's source message.
func (w *world) request() automation.PublishRequest {
	return automation.PublishRequest{
		Source: automation.SourceRef{
			ManagedChannelID: w.source.ID, ClientKey: "source",
			TelegramChannelID: 555, MessageID: 9,
		},
		Destination: automation.Destination{
			Type: automation.DestinationTelegram, ClientKey: "dest", ChannelID: 777,
		},
		Text:           "hello",
		IdempotencyKey: "wf-run-1-dest-1",
		UserID:         w.user.ID,
	}
}

// grant gives the account a permission, replacing what it held.
func (w *world) grant(t *testing.T, globals ...channelstore.GlobalPermission) {
	t.Helper()
	if err := w.store.ReplacePermissions(context.Background(), w.user.ID, channelstore.PermissionSet{
		Global: globals,
	}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
}

func TestBackgroundPublishingRunsUnderTheAccountsCurrentPermissions(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)

	res, err := w.service.Publish(context.Background(), w.request())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.Outcome != automation.PublishSucceeded || res.RemoteMessageID != 4321 {
		t.Fatalf("result = %+v", res)
	}
	if len(w.worker.sends) != 1 {
		t.Fatalf("sends = %d", len(w.worker.sends))
	}

	// The publishing right is withdrawn between two runs of the same workflow.
	// The next one publishes nothing: a queued run is not a way around a
	// permission that was taken away.
	w.grant(t, channelstore.PermissionChannelsView)
	_, err = w.service.Publish(context.Background(), w.request())
	if err == nil {
		t.Fatal("a run without content.publish must not publish")
	}
	if code := publishing.CodeOf(err); code != publishing.CodePublishForbidden {
		t.Fatalf("code = %q, want %q", code, publishing.CodePublishForbidden)
	}
	if len(w.worker.sends) != 1 {
		t.Fatalf("sends = %d, want 1 — nothing may be sent without the right", len(w.worker.sends))
	}
}

func TestBackgroundPublishingRefusesAnAccountThatIsGone(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	if err := w.store.DeleteUser(context.Background(), w.user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, err := w.service.Publish(context.Background(), w.request())
	if err == nil {
		t.Fatal("a deleted account must not publish")
	}
	if code := publishing.CodeOf(err); code != publishing.CodePublishForbidden {
		t.Fatalf("code = %q, want %q", code, publishing.CodePublishForbidden)
	}
	if len(w.worker.sends) != 0 {
		t.Fatalf("sends = %d, want 0", len(w.worker.sends))
	}
}

func TestBackgroundPublishingRefusesAnInactiveAccount(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	if _, err := w.store.UpdateUser(context.Background(), w.user.ID, channelstore.UserUpdate{IsActive: boolPtr(false)}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	if _, err := w.service.Publish(context.Background(), w.request()); err == nil {
		t.Fatal("an inactive account must not publish")
	}
	if len(w.worker.sends) != 0 {
		t.Fatalf("sends = %d, want 0", len(w.worker.sends))
	}
}

// A source the account may not read is refused before anything is sent, exactly
// as it would be for an interactive caller.
func TestBackgroundPublishingRefusesASourceTheAccountCannotRead(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	if err := w.store.ReplacePermissions(context.Background(), w.user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionContentPublish},
		Channels: []channelstore.ResourcePermissions{{
			ResourceID:  w.source.ID,
			Permissions: []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: channelstore.EffectDeny}},
		}},
	}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}

	_, err := w.service.Publish(context.Background(), w.request())
	if err == nil {
		t.Fatal("a source the account cannot read must not be published")
	}
	if code := publishing.CodeOf(err); code != publishing.CodePublishSourceDenied {
		t.Fatalf("code = %q, want %q", code, publishing.CodePublishSourceDenied)
	}
	if len(w.worker.sends) != 0 {
		t.Fatalf("sends = %d, want 0", len(w.worker.sends))
	}
}

// The media mode a request names decides what travels with the post, and
// excluding the source attachment is never a side effect of anything else.
func TestBackgroundPublishingKeepsTheSourceMediaUnlessItIsDropped(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	req := w.request()
	req.Options.MediaMode = publishing.MediaOmit
	if _, err := w.service.Publish(context.Background(), req); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if w.worker.sends[0].Media != nil {
		t.Fatal("media.mode=omit must not send an attachment")
	}
}

func boolPtr(v bool) *bool { return &v }

// A workflow made through the operator token has no owning account. A
// publishing attempt is recorded against its author — that is what makes a
// retry safe — so work with no author is refused rather than run with the
// deployment's own authority.
func TestAWorkflowWithNoOwningAccountIsRefusedRatherThanElevated(t *testing.T) {
	w := newWorld(t)
	req := w.request()
	req.UserID = 0

	_, err := w.service.Publish(context.Background(), req)
	if err == nil {
		t.Fatal("a publication with no owning account must be refused")
	}
	if code := publishing.CodeOf(err); code != publishing.CodePublishForbidden {
		t.Fatalf("code = %q, want %q", code, publishing.CodePublishForbidden)
	}
	if len(w.worker.sends) != 0 {
		t.Fatalf("sends = %d, want 0", len(w.worker.sends))
	}
}
