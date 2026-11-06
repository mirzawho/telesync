package channelstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// Event storage is where "the same message" stops being a matter of opinion.
// Two accounts watching one channel on one network describe one logical
// message; two backends whose channel and message ids happen to overlap do not.
// The tests below pin that distinction to the schema rather than to whichever
// connection happened to deliver the update first.

// automationStore opens a store holding one managed channel to attach events to.
func automationStore(t *testing.T) (*channelstore.Store, int64) {
	t.Helper()
	store := open(t)
	ch, err := store.Upsert(context.Background(), channelstore.Channel{
		ClientKey:        "official",
		ChannelID:        1000,
		PeerType:         channelstore.PeerChannel,
		Title:            "Source",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("upsert channel: %v", err)
	}
	return store, ch.ID
}

// eventFor builds one normalized event. Callers override the fields that carry
// the identity under test.
func eventFor(managedID int64) automation.MessageEvent {
	return automation.MessageEvent{
		ID:                "evt_test",
		Type:              automation.EventMessageCreated,
		NetworkKey:        "official",
		SourceClientKey:   "official",
		ManagedChannelID:  managedID,
		TelegramChannelID: 1000,
		MessageID:         7,
		OccurredAt:        time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC),
		ReceivedAt:        time.Date(2026, 11, 5, 10, 0, 1, 0, time.UTC),
		Payload: automation.MessagePayload{
			Message: rpc.Message{ID: 7, Text: "hello", Reactions: []rpc.Reaction{}, Entities: []rpc.Entity{}},
		},
	}
}

func TestAnEventIsStoredWithItsIdentityAndPayload(t *testing.T) {
	store, managedID := automationStore(t)
	ctx := context.Background()

	in := eventFor(managedID)
	in.Payload.Message.Media = &rpc.Media{
		ID: "7:0", Type: "photo", Label: "Photo", Filename: "cat.jpg",
		MimeType: "image/jpeg", Size: 1234,
	}
	in.Payload.MediaURLPath = "/telegram/official/channels/1000/messages/7/media"

	inserted, err := store.InsertMessageEvent(ctx, in)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !inserted {
		t.Fatal("a first observation must be reported as inserted")
	}

	got, ok, err := store.AutomationEvent(ctx, in.ID)
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if got.Type != automation.EventMessageCreated {
		t.Fatalf("type = %q, want %q", got.Type, automation.EventMessageCreated)
	}
	if got.NetworkKey != "official" || got.SourceClientKey != "official" {
		t.Fatalf("network/client = %q/%q", got.NetworkKey, got.SourceClientKey)
	}
	if got.ManagedChannelID != managedID || got.TelegramChannelID != 1000 || got.MessageID != 7 {
		t.Fatalf("identity = %d/%d/%d", got.ManagedChannelID, got.TelegramChannelID, got.MessageID)
	}
	if got.Payload.Message.Text != "hello" {
		t.Fatalf("text = %q", got.Payload.Message.Text)
	}
	media := got.Payload.Message.Media
	if media == nil {
		t.Fatal("the stored payload lost its media metadata")
	}
	if media.Type != "photo" || media.Filename != "cat.jpg" || media.MimeType != "image/jpeg" || media.Size != 1234 {
		t.Fatalf("media = %+v", media)
	}
	if got.Payload.MediaURLPath != "/telegram/official/channels/1000/messages/7/media" {
		t.Fatalf("media path = %q", got.Payload.MediaURLPath)
	}
}

func TestADuplicateMessageIsNotStoredTwiceButKeepsItsObserver(t *testing.T) {
	store, managedID := automationStore(t)
	ctx := context.Background()

	first := eventFor(managedID)
	inserted, err := store.InsertMessageEvent(ctx, first)
	if err != nil || !inserted {
		t.Fatalf("first insert: inserted=%v err=%v", inserted, err)
	}

	// The same message arriving again on another connection of the same network.
	replay := eventFor(managedID)
	replay.SourceClientKey = "secondary"
	inserted, err = store.InsertMessageEvent(ctx, replay)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if inserted {
		t.Fatal("a duplicate observation must be reported as not inserted")
	}

	total, err := store.CountAutomationEvents(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Fatalf("stored %d events, want 1", total)
	}

	sources, err := store.AutomationEventSources(ctx, first.ID)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %v, want both observers", sources)
	}
	seen := map[string]bool{}
	for _, k := range sources {
		seen[k] = true
	}
	if !seen["official"] || !seen["secondary"] {
		t.Fatalf("sources = %v, want official and secondary", sources)
	}
}

func TestTwoAccountsOnOneNetworkProduceOneEvent(t *testing.T) {
	store, managedID := automationStore(t)
	ctx := context.Background()

	a := eventFor(managedID)
	a.SourceClientKey = "account-a"
	b := eventFor(managedID)
	b.SourceClientKey = "account-b"
	// Distinct ids on purpose: the identity constraint, not the id, decides.
	b.ID = "evt_other"

	if _, err := store.InsertMessageEvent(ctx, a); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	inserted, err := store.InsertMessageEvent(ctx, b)
	if err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if inserted {
		t.Fatal("the second account must not create a second event on one network")
	}
	total, err := store.CountAutomationEvents(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Fatalf("stored %d events, want 1", total)
	}
}

func TestTheSameMessageOnAnotherNetworkIsADistinctEvent(t *testing.T) {
	store, managedID := automationStore(t)
	ctx := context.Background()

	a := eventFor(managedID)
	a.NetworkKey = "official"
	b := eventFor(managedID)
	b.NetworkKey = "custom@10.0.0.1:8080"
	b.ID = "evt_other_network"

	if _, err := store.InsertMessageEvent(ctx, a); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	inserted, err := store.InsertMessageEvent(ctx, b)
	if err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if !inserted {
		t.Fatal("the same ids on a different network must be a distinct event")
	}
	total, err := store.CountAutomationEvents(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 2 {
		t.Fatalf("stored %d events, want 2", total)
	}
}

func TestTheSameMessageIDInAnotherChannelIsADistinctEvent(t *testing.T) {
	store, managedID := automationStore(t)
	ctx := context.Background()

	other, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey:        "official",
		ChannelID:        2000,
		PeerType:         channelstore.PeerChannel,
		Title:            "Other",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("upsert second channel: %v", err)
	}

	a := eventFor(managedID)
	b := eventFor(other.ID)
	b.TelegramChannelID = 2000
	b.ID = "evt_other_channel"

	if _, err := store.InsertMessageEvent(ctx, a); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	inserted, err := store.InsertMessageEvent(ctx, b)
	if err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if !inserted {
		t.Fatal("the same message id in another channel must be a distinct event")
	}
}

func TestAnEventWithoutAValidIdentityIsRefused(t *testing.T) {
	store, managedID := automationStore(t)
	ctx := context.Background()

	cases := map[string]func(*automation.MessageEvent){
		"no network":    func(e *automation.MessageEvent) { e.NetworkKey = "  " },
		"no client":     func(e *automation.MessageEvent) { e.SourceClientKey = "" },
		"no type":       func(e *automation.MessageEvent) { e.Type = "" },
		"zero message":  func(e *automation.MessageEvent) { e.MessageID = 0 },
		"zero channel":  func(e *automation.MessageEvent) { e.TelegramChannelID = 0 },
		"no managed id": func(e *automation.MessageEvent) { e.ManagedChannelID = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := eventFor(managedID)
			mutate(&in)
			if _, err := store.InsertMessageEvent(ctx, in); !errors.Is(err, channelstore.ErrInvalidAutomationEvent) {
				t.Fatalf("err = %v, want ErrInvalidAutomationEvent", err)
			}
		})
	}
}

func TestAnEventForAnUnmanagedChannelIsRefused(t *testing.T) {
	store, managedID := automationStore(t)
	ctx := context.Background()

	in := eventFor(managedID)
	in.ManagedChannelID = 99999
	if _, err := store.InsertMessageEvent(ctx, in); err == nil {
		t.Fatal("an event must not be stored against a managed channel that does not exist")
	}
}
