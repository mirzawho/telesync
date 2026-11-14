package gotdclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/events"
	"github.com/mirza/telesync/internal/rpc"
)

// batch builds a tg.Updates carrying one new channel message with a document
// attachment. The document names a file and a byte size so the test can see the
// metadata survive — and, because rpc.Media holds no bytes at all, see that the
// content itself can never cross.
func batch(messageID int, channelID int64) *tg.Updates {
	return &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{
				Message: &tg.Message{
					ID:      messageID,
					PeerID:  &tg.PeerChannel{ChannelID: channelID},
					Date:    1_700_000_000,
					Message: "release build",
					Media: &tg.MessageMediaDocument{Document: &tg.Document{
						ID:         42,
						AccessHash: 7,
						MimeType:   "application/vnd.android.package-archive",
						Size:       59797754,
						Attributes: []tg.DocumentAttributeClass{
							&tg.DocumentAttributeFilename{FileName: "app-release.apk"},
						},
					}},
				},
			},
		},
	}
}

func collectSink(dst *[]rpc.IngestMessage) IngestSinkFunc {
	return func(_ context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
		*dst = append(*dst, msg)
		return rpc.IngestInserted, nil
	}
}

// A new channel message becomes one bounded observation carrying its media
// metadata and no media bytes.
func TestANewChannelMessageBecomesOneBoundedObservation(t *testing.T) {
	var got []rpc.IngestMessage
	h := newUpdateHandler("official", "acc_00000000000000000000000001", "official", collectSink(&got), nil, nil)

	if err := h.Handle(context.Background(), batch(36, 123456789)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("produced %d observations, want 1", len(got))
	}
	obs := got[0]
	if obs.ClientKey != "official" || obs.NetworkKey != "official" {
		t.Fatalf("client %q network %q, want both official", obs.ClientKey, obs.NetworkKey)
	}
	if obs.TelegramChannelID != 123456789 || obs.Message.ID != 36 {
		t.Fatalf("channel %d message %d, want 123456789 36",
			obs.TelegramChannelID, obs.Message.ID)
	}
	if obs.Message.Text != "release build" {
		t.Fatalf("text = %q", obs.Message.Text)
	}

	// Media metadata survives...
	media := obs.Message.Media
	if media == nil {
		t.Fatal("media metadata was dropped")
	}
	if media.Filename != "app-release.apk" {
		t.Fatalf("filename = %q, want app-release.apk", media.Filename)
	}
	if media.MimeType != "application/vnd.android.package-archive" {
		t.Fatalf("mime = %q", media.MimeType)
	}
	if media.Size != 59797754 {
		t.Fatalf("size = %d, want the reported size", media.Size)
	}

	// ...and bytes do not cross. The observation is pure metadata: marshaling it
	// yields a small JSON document with no attachment payload in it.
	payload, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) > 4<<10 {
		t.Fatalf("observation is %d bytes; it must be metadata only", len(payload))
	}
	if strings.Contains(string(payload), "data") && strings.Contains(string(payload), "base64") {
		t.Fatal("observation carries a base64 media payload")
	}
}

// The update callback hands off and returns. It never waits for the delivery a
// webhook, AI call or publication performs later on the daemon's queue.
func TestTheUpdateCallbackReturnsWithoutWaitingOnDelivery(t *testing.T) {
	delivered := make(chan struct{})
	var handed []rpc.IngestMessage
	sink := IngestSinkFunc(func(_ context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
		handed = append(handed, msg)
		// Storing and fanning out is bounded and returns here. The delivery
		// itself runs later and blocks on `delivered`; the handler must not wait
		// for it.
		go func() { <-delivered }()
		return rpc.IngestInserted, nil
	})
	h := newUpdateHandler("official", "acc_00000000000000000000000001", "official", sink, nil, nil)

	if err := h.Handle(context.Background(), batch(36, 123456789)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(handed) != 1 {
		t.Fatalf("handed off %d observations, want 1", len(handed))
	}
	select {
	case <-delivered:
		t.Fatal("the update callback waited for delivery")
	default:
		// Delivery is still pending, yet Handle already returned.
	}
	close(delivered)
}

// Only new channel messages are observations. Edits, reactions and service
// updates are not the event the pipeline consumes and must not become one.
func TestNonMessageUpdatesProduceNoObservation(t *testing.T) {
	var got []rpc.IngestMessage
	h := newUpdateHandler("official", "acc_00000000000000000000000001", "official", collectSink(&got), nil, nil)

	u := &tg.Updates{Updates: []tg.UpdateClass{
		// A private message is not a channel message.
		&tg.UpdateNewMessage{Message: &tg.Message{
			ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 1, Message: "dm",
		}},
		// An edit is not a new message.
		&tg.UpdateEditChannelMessage{Message: &tg.Message{
			ID: 2, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 2, Message: "edited",
		}},
	}}
	if err := h.Handle(context.Background(), u); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("produced %d observations for non-message updates, want 0", len(got))
	}
}

// A batch with two new messages yields two observations, in order.
func TestEachNewChannelMessageInTheBatchIsAnObservation(t *testing.T) {
	u := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{
			ID: 1, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 1, Message: "one",
		}},
		&tg.UpdateNewChannelMessage{Message: &tg.Message{
			ID: 2, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 2, Message: "two",
		}},
	}}
	var got []rpc.IngestMessage
	h := newUpdateHandler("official", "acc_00000000000000000000000001", "official", collectSink(&got), nil, nil)
	if err := h.Handle(context.Background(), u); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(got) != 2 || got[0].Message.ID != 1 || got[1].Message.ID != 2 {
		t.Fatalf("observations = %v, want messages 1 then 2", got)
	}
}

// When a message cannot be stored, Handle reports it. That is what keeps gotd
// from advancing the persisted update counter past a message that never reached
// the store: the counter stays put and the message is replayed instead of
// skipped.
func TestAnUnstorableMessageIsReportedSoTheCounterCannotSkipIt(t *testing.T) {
	sink := IngestSinkFunc(func(_ context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
		if msg.Message.ID == 2 {
			return "", context.DeadlineExceeded
		}
		return rpc.IngestInserted, nil
	})
	h := newUpdateHandler("official", "acc_00000000000000000000000001", "official", sink, nil, nil)

	u := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{
			ID: 1, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 1, Message: "one",
		}},
		&tg.UpdateNewChannelMessage{Message: &tg.Message{
			ID: 2, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 2, Message: "two",
		}},
	}}
	err := h.Handle(context.Background(), u)
	if err == nil {
		t.Fatal("Handle swallowed an un-storable message; the counter would skip it")
	}
}

// The whole MTProto hop, end to end: an update reaches the durable event store
// exactly once, and a replayed update after a reconnect or a crash does not
// create a second one.
func TestTheUpdateStreamReachesTheDurableEventStoreOnce(t *testing.T) {
	store := &dedupStore{seen: map[string]bool{}}
	ingest := events.NewIngestor(store, nil, nil, nil, nil)
	backend := events.NewRPCIngestBackend(ingest, managedResolver{})
	sink := IngestSinkFunc(func(ctx context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
		return backend.Ingest(ctx, msg)
	})
	h := newUpdateHandler("official", "acc_00000000000000000000000001", "official", sink, time.Now, nil)

	if err := h.Handle(context.Background(), batch(36, 123456789)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.got) != 1 {
		t.Fatalf("stored %d events after the update, want exactly 1", len(store.got))
	}

	// A replay — the same message redelivered after a reconnect or a crash —
	// must not store a second event.
	if err := h.Handle(context.Background(), batch(36, 123456789)); err != nil {
		t.Fatalf("handle replay: %v", err)
	}
	if len(store.got) != 1 {
		t.Fatalf("stored %d events after a replay, want exactly 1", len(store.got))
	}

	// A genuinely new message is a new event.
	if err := h.Handle(context.Background(), batch(37, 123456789)); err != nil {
		t.Fatalf("handle new: %v", err)
	}
	if len(store.got) != 2 {
		t.Fatalf("stored %d events after a new message, want 2", len(store.got))
	}
}

// dedupStore refuses a second insert of one event id, the way the real channel
// store's uniqueness constraint does.
type dedupStore struct {
	seen map[string]bool
	got  []automation.MessageEvent
}

func (d *dedupStore) InsertMessageEvent(_ context.Context, event automation.MessageEvent) (bool, error) {
	if d.seen[event.ID] {
		return false, nil
	}
	d.seen[event.ID] = true
	d.got = append(d.got, event)
	return true, nil
}

// managedResolver treats every channel under test as a managed source of the
// account that observed it.
type managedResolver struct{}

func (managedResolver) ManagedChannelFor(_ context.Context, accountID, _ string, channelID int64) (events.ChannelRegistration, bool, error) {
	return events.ChannelRegistration{
		ManagedChannelID: channelID / 1000,
		AccountID:        accountID,
		OwnerUserID:      1,
	}, true, nil
}
