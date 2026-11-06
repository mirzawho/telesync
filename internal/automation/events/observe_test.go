package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/events"
	"github.com/mirza/telesync/internal/rpc"
)

// dedupStore is a store that refuses a second insert of one event id, the way
// the real channel store's uniqueness constraint does. It is the property that
// makes replay safe.
type dedupStore struct {
	seen map[string]bool
	got  []automation.MessageEvent
}

func newDedupStore() *dedupStore { return &dedupStore{seen: map[string]bool{}} }

func (d *dedupStore) InsertMessageEvent(_ context.Context, event automation.MessageEvent) (bool, error) {
	if d.seen[event.ID] {
		return false, nil
	}
	d.seen[event.ID] = true
	d.got = append(d.got, event)
	return true, nil
}

// resolver decides which channels this installation manages.
type resolver struct {
	managed map[int64]bool
}

func (r resolver) ManagedChannelFor(_ context.Context, _ string, telegramChannelID int64) (int64, bool, error) {
	if r.managed[telegramChannelID] {
		// A deterministic local id, standing in for the managed_channels row.
		return telegramChannelID / 1000, true, nil
	}
	return 0, false, nil
}

func obs(clientKey, networkKey string, channelID int64, messageID int) events.Observation {
	return events.Observation{
		ClientKey:         clientKey,
		NetworkKey:        networkKey,
		TelegramChannelID: channelID,
		Message: rpc.Message{
			ID:   messageID,
			Date: time.Unix(1_700_000_000, 0).UTC(),
			Text: "hello",
			Media: &rpc.Media{
				ID:       "m1",
				Type:     "photo",
				Label:    "photo",
				MimeType: "image/jpeg",
				Size:     1234,
			},
			Views:     intPtr(3),
			Reactions: []rpc.Reaction{},
			Entities:  []rpc.Entity{},
		},
		ReceivedAt: time.Unix(1_700_000_010, 0).UTC(),
	}
}

func ingest(t *testing.T) (*events.Ingestor, *dedupStore, resolver) {
	t.Helper()
	store := newDedupStore()
	ing := events.NewIngestor(store, nil, nil, nil, nil)
	return ing, store, resolver{managed: map[int64]bool{123456789: true}}
}

// A message in a managed source channel becomes exactly one durable event.
func TestAManagedMessageProducesExactlyOneStoredEvent(t *testing.T) {
	ing, store, res := ingest(t)

	outcome, managed, err := ing.Observe(context.Background(), res, obs("official", "official", 123456789, 36))
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !managed {
		t.Fatal("a managed channel was reported unmanaged")
	}
	if outcome != events.OutcomeInserted {
		t.Fatalf("outcome = %q, want %q", outcome, events.OutcomeInserted)
	}
	if len(store.got) != 1 {
		t.Fatalf("stored %d events, want exactly 1", len(store.got))
	}
	got := store.got[0]
	if got.MessageID != 36 || got.TelegramChannelID != 123456789 {
		t.Fatalf("event identity = message %d channel %d, want 36 123456789",
			got.MessageID, got.TelegramChannelID)
	}
	if got.ManagedChannelID == 0 {
		t.Fatal("the event carries no local managed channel id")
	}
	if got.Payload.Message.Media == nil || got.Payload.Message.Media.Size != 1234 {
		t.Fatalf("media metadata = %+v, want the size preserved", got.Payload.Message.Media)
	}
}

// Duplicate delivery is expected and must not create a second event.
func TestADuplicateObservationProducesNoSecondEvent(t *testing.T) {
	ing, store, res := ingest(t)

	if _, _, err := ing.Observe(context.Background(), res, obs("official", "official", 123456789, 36)); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	outcome, _, err := ing.Observe(context.Background(), res, obs("official", "official", 123456789, 36))
	if err != nil {
		t.Fatalf("second observe: %v", err)
	}
	if outcome != events.OutcomeDuplicate {
		t.Fatalf("outcome = %q, want %q", outcome, events.OutcomeDuplicate)
	}
	if len(store.got) != 1 {
		t.Fatalf("stored %d events, want exactly 1 after a replay", len(store.got))
	}
}

// Two accounts watching one channel on one network describe one logical
// message; a different network is a different message even when its numbers
// coincide.
func TestTwoAccountsOneNetworkAreOneEventAndTwoNetworksAreNot(t *testing.T) {
	ing, store, res := ingest(t)

	if _, _, err := ing.Observe(context.Background(), res, obs("a", "net", 123456789, 36)); err != nil {
		t.Fatalf("first account: %v", err)
	}
	if _, _, err := ing.Observe(context.Background(), res, obs("b", "net", 123456789, 36)); err != nil {
		t.Fatalf("second account: %v", err)
	}
	if len(store.got) != 1 {
		t.Fatalf("stored %d events for two accounts on one network, want 1", len(store.got))
	}

	if _, _, err := ing.Observe(context.Background(), res, obs("c", "other-net", 123456789, 36)); err != nil {
		t.Fatalf("other network: %v", err)
	}
	if len(store.got) != 2 {
		t.Fatalf("stored %d events after a second network, want 2", len(store.got))
	}
	if store.got[0].ID == store.got[1].ID {
		t.Fatal("two networks collapsed onto one event id")
	}
}

// A message in a channel this installation does not manage is not automation's
// business: nothing is stored and it is not an error.
func TestAnUnmanagedChannelStoresNothing(t *testing.T) {
	ing, store, res := ingest(t)

	outcome, managed, err := ing.Observe(context.Background(), res, obs("official", "official", 999, 1))
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if managed {
		t.Fatal("an unmanaged channel was reported managed")
	}
	if outcome != events.OutcomeUnmanaged {
		t.Fatalf("outcome = %q, want %q", outcome, events.OutcomeUnmanaged)
	}
	if len(store.got) != 0 {
		t.Fatalf("stored %d events for an unmanaged channel, want 0", len(store.got))
	}
}

// After a crash between "event stored" and "state advanced", the update is
// replayed. Ingestion must absorb the replay without producing a second event,
// so the pipeline is idempotent under exactly the failure that would otherwise
// skip or duplicate a message.
func TestIngestionIsIdempotentUnderReplayAfterACrash(t *testing.T) {
	ing, store, res := ingest(t)

	// The event is stored...
	if _, _, err := ing.Observe(context.Background(), res, obs("official", "official", 123456789, 36)); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	// ...and then the process dies before the update counter advances. On
	// restart getDifference replays the message.
	outcome, _, err := ing.Observe(context.Background(), res, obs("official", "official", 123456789, 36))
	if err != nil {
		t.Fatalf("replay observe: %v", err)
	}
	if outcome != events.OutcomeDuplicate {
		t.Fatalf("replay outcome = %q, want %q", outcome, events.OutcomeDuplicate)
	}
	if len(store.got) != 1 {
		t.Fatalf("stored %d events after a crash replay, want exactly 1", len(store.got))
	}
}
