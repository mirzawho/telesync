package events_test

import (
	"context"
	"strconv"
	"strings"
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
//
// It is keyed by (account, remote channel) and not by the remote channel alone,
// which is the whole point of the account-scoped lookup: two users may
// independently subscribe to the same remote channel and each observation must
// resolve to the row belonging to the account that saw it.
type resolver struct {
	managed map[string]bool
	// rows maps an account's registration to the local row and its owner.
	rows map[string]events.ChannelRegistration
}

// key is the account-scoped registration identity.
func key(accountID string, telegramChannelID int64) string {
	return accountID + "/" + strconv.FormatInt(telegramChannelID, 10)
}

func (r resolver) ManagedChannelFor(_ context.Context, accountID, _ string, telegramChannelID int64) (events.ChannelRegistration, bool, error) {
	k := key(accountID, telegramChannelID)
	if !r.managed[k] {
		return events.ChannelRegistration{}, false, nil
	}
	if reg, ok := r.rows[k]; ok {
		return reg, true, nil
	}
	// A deterministic local id, standing in for the managed_channels row.
	return events.ChannelRegistration{
		ManagedChannelID: telegramChannelID / 1000,
		AccountID:        accountID,
		OwnerUserID:      ownerOf(accountID),
	}, true, nil
}

// ownerOf is a stand-in for the account's owner, so a job scheduled from an
// observation is bound to somebody.
func ownerOf(accountID string) int64 {
	if strings.HasPrefix(accountID, "acc_b") {
		return 200
	}
	return 100
}

func obs(clientKey, networkKey string, channelID int64, messageID int) events.Observation {
	return obsFor("acc_a", clientKey, networkKey, channelID, messageID)
}

// obsFor is obs for one named account: the account is what scopes the event
// identity, so two accounts observing the same remote message are two events.
func obsFor(accountID, clientKey, networkKey string, channelID int64, messageID int) events.Observation {
	return events.Observation{
		ClientKey:         clientKey,
		AccountID:         accountID,
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
	return ing, store, resolver{managed: map[string]bool{key("acc_a", 123456789): true}}
}

// Two users may independently subscribe to the same remote channel. Each must
// get its own event: the old deduplication compared messages without an account
// and would have dropped the second user's copy, silently starving their
// automation of everything after the first.
func TestTwoAccountsObservingOneRemoteChannelEachGetTheirOwnEvent(t *testing.T) {
	store := newDedupStore()
	ing := events.NewIngestor(store, nil, nil, nil, nil)
	res := resolver{managed: map[string]bool{
		key("acc_a", 123456789): true,
		key("acc_b", 123456789): true,
	}}

	ctx := context.Background()
	if _, _, err := ing.Observe(ctx, res, obsFor("acc_a", "official", "official", 123456789, 36)); err != nil {
		t.Fatalf("first account: %v", err)
	}
	outcome, managed, err := ing.Observe(ctx, res, obsFor("acc_b", "official", "official", 123456789, 36))
	if err != nil {
		t.Fatalf("second account: %v", err)
	}
	if !managed {
		t.Fatal("the second account's registration resolved unmanaged")
	}
	if outcome == events.OutcomeDuplicate {
		t.Fatal("the second account's event was suppressed as a duplicate of the first account's")
	}
	if len(store.got) != 2 {
		t.Fatalf("stored %d events, want 2 — one per account", len(store.got))
	}
	if store.got[0].AccountID == store.got[1].AccountID {
		t.Fatalf("both events are bound to %q; they must be distinct accounts", store.got[0].AccountID)
	}
}

// The same account seeing the same message twice is still one event. Account
// scoping must widen the dedup key, not remove it — otherwise a reconnect
// would replay every message into the automations.
func TestOneAccountSeeingTheSameMessageTwiceIsStillOneEvent(t *testing.T) {
	store := newDedupStore()
	ing := events.NewIngestor(store, nil, nil, nil, nil)
	res := resolver{managed: map[string]bool{key("acc_a", 123456789): true}}

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		outcome, _, err := ing.Observe(ctx, res, obsFor("acc_a", "official", "official", 123456789, 36))
		if err != nil {
			t.Fatalf("observe %d: %v", i, err)
		}
		if i > 0 && outcome != events.OutcomeDuplicate {
			t.Fatalf("observe %d outcome = %q, want %q", i, outcome, events.OutcomeDuplicate)
		}
	}
	if len(store.got) != 1 {
		t.Fatalf("stored %d events, want 1", len(store.got))
	}
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
