package channelstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
)

// Two kinds of state live here, and both are the same promise: what this
// installation learned while it was running is still there after it restarts.
// Update state is what lets a reconnect catch up; the outbound record is what
// keeps a workflow from reacting to its own publication.

func stateStore(t *testing.T) *channelstore.Store {
	t.Helper()
	return open(t)
}

func TestUpdateStateRoundTripsAndIsScopedToOneClient(t *testing.T) {
	store := stateStore(t)
	ctx := context.Background()

	if err := store.SetAutomationUpdateState(ctx, "official", 42,
		channelstore.AutomationState{Pts: 10, Qts: 4, Date: 1700000000, Seq: 2}); err != nil {
		t.Fatalf("set state: %v", err)
	}
	got, ok, err := store.AutomationUpdateState(ctx, "official", 42)
	if err != nil || !ok {
		t.Fatalf("get state: ok=%v err=%v", ok, err)
	}
	want := channelstore.AutomationState{Pts: 10, Qts: 4, Date: 1700000000, Seq: 2}
	if got != want {
		t.Fatalf("state = %+v, want %+v", got, want)
	}

	if _, ok, err := store.AutomationUpdateState(ctx, "secondary", 42); err != nil || ok {
		t.Fatalf("state must be scoped to one client: ok=%v err=%v", ok, err)
	}
}

func TestTheIncrementalSettersRefuseAnUnknownAccount(t *testing.T) {
	store := stateStore(t)
	ctx := context.Background()

	cases := map[string]func() error{
		"pts":      func() error { return store.SetAutomationUpdatePts(ctx, "official", 7, 1) },
		"qts":      func() error { return store.SetAutomationUpdateQts(ctx, "official", 7, 1) },
		"date":     func() error { return store.SetAutomationUpdateDate(ctx, "official", 7, 1) },
		"seq":      func() error { return store.SetAutomationUpdateSeq(ctx, "official", 7, 1) },
		"date seq": func() error { return store.SetAutomationUpdateDateSeq(ctx, "official", 7, 1, 1) },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, channelstore.ErrAutomationStateNotFound) {
				t.Fatalf("err = %v, want ErrAutomationStateNotFound", err)
			}
		})
	}
}

func TestTheIncrementalSettersMoveOneCounterEach(t *testing.T) {
	store := stateStore(t)
	ctx := context.Background()
	if err := store.SetAutomationUpdateState(ctx, "official", 7,
		channelstore.AutomationState{Pts: 1, Qts: 1, Date: 1, Seq: 1}); err != nil {
		t.Fatalf("set state: %v", err)
	}

	if err := store.SetAutomationUpdatePts(ctx, "official", 7, 900); err != nil {
		t.Fatalf("set pts: %v", err)
	}
	if err := store.SetAutomationUpdateDateSeq(ctx, "official", 7, 55, 66); err != nil {
		t.Fatalf("set date seq: %v", err)
	}
	got, _, err := store.AutomationUpdateState(ctx, "official", 7)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	want := channelstore.AutomationState{Pts: 900, Qts: 1, Date: 55, Seq: 66}
	if got != want {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
}

func TestChannelPtsAreSavedPerAccountAndPerChannel(t *testing.T) {
	store := stateStore(t)
	ctx := context.Background()

	if err := store.SetAutomationChannelPts(ctx, "official", 7, 1000, 33); err != nil {
		t.Fatalf("set channel pts: %v", err)
	}
	if err := store.SetAutomationChannelPts(ctx, "official", 7, 2000, 44); err != nil {
		t.Fatalf("set channel pts: %v", err)
	}
	pts, ok, err := store.AutomationChannelPts(ctx, "official", 7, 1000)
	if err != nil || !ok || pts != 33 {
		t.Fatalf("channel pts = %d ok=%v err=%v, want 33", pts, ok, err)
	}
	if _, ok, err := store.AutomationChannelPts(ctx, "secondary", 7, 1000); err != nil || ok {
		t.Fatalf("channel pts must be scoped to one client: ok=%v err=%v", ok, err)
	}

	seen := map[int64]int{}
	if err := store.ForEachAutomationChannelPts(ctx, "official", 7,
		func(_ context.Context, channelID int64, pts int) error {
			seen[channelID] = pts
			return nil
		}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(seen) != 2 || seen[1000] != 33 || seen[2000] != 44 {
		t.Fatalf("walked %v, want both channels", seen)
	}
}

func TestPeerHashesKeepChannelsAndUsersApart(t *testing.T) {
	store := stateStore(t)
	ctx := context.Background()

	if err := store.SetAutomationPeerHash(ctx, "official", 7, channelstore.AutomationPeerChannel, 1000, 111); err != nil {
		t.Fatalf("set channel hash: %v", err)
	}
	if err := store.SetAutomationPeerHash(ctx, "official", 7, channelstore.AutomationPeerUser, 1000, 222); err != nil {
		t.Fatalf("set user hash: %v", err)
	}
	hash, ok, err := store.AutomationPeerHash(ctx, "official", 7, channelstore.AutomationPeerChannel, 1000)
	if err != nil || !ok || hash != 111 {
		t.Fatalf("channel hash = %d ok=%v err=%v, want 111", hash, ok, err)
	}
	hash, ok, err = store.AutomationPeerHash(ctx, "official", 7, channelstore.AutomationPeerUser, 1000)
	if err != nil || !ok || hash != 222 {
		t.Fatalf("user hash = %d ok=%v err=%v, want 222", hash, ok, err)
	}
}

func TestACheckpointStartsUnsetAndOnlyMovesForward(t *testing.T) {
	store := stateStore(t)
	ctx := context.Background()

	last, ok, err := store.AutomationCheckpoint(ctx, "official", 1000)
	if err != nil || ok || last != 0 {
		t.Fatalf("checkpoint = %d ok=%v err=%v, want an unset mark", last, ok, err)
	}

	if err := store.SetAutomationCheckpoint(ctx, "official", 1000, 50); err != nil {
		t.Fatalf("set checkpoint: %v", err)
	}
	if err := store.SetAutomationCheckpoint(ctx, "official", 1000, 20); err != nil {
		t.Fatalf("set checkpoint: %v", err)
	}
	last, ok, err = store.AutomationCheckpoint(ctx, "official", 1000)
	if err != nil || !ok {
		t.Fatalf("read checkpoint: ok=%v err=%v", ok, err)
	}
	if last != 50 {
		t.Fatalf("checkpoint = %d, want the mark to hold at 50", last)
	}
}

func TestAPublicationIsRecognizedOnlyOnItsOwnNetworkAndClient(t *testing.T) {
	store := stateStore(t)
	ctx := context.Background()
	mark := automation.OutboundMessage{
		NetworkKey: "official", ClientKey: "official",
		ChannelID: 1000, RemoteMessageID: 77, Origin: "workflow", OriginID: 5,
	}
	if err := store.MarkPublished(ctx, mark); err != nil {
		t.Fatalf("mark: %v", err)
	}
	// Recording it twice is how two destinations of one run agree.
	if err := store.MarkPublished(ctx, mark); err != nil {
		t.Fatalf("mark twice: %v", err)
	}

	own, err := store.IsOwnPublication(ctx, "official", "official", 1000, 77)
	if err != nil || !own {
		t.Fatalf("IsOwnPublication = %v err=%v, want true", own, err)
	}
	for name, tc := range map[string]struct {
		network, client string
		channel, msg    int64
	}{
		"another network": {"custom@10.0.0.1:8080", "official", 1000, 77},
		"another client":  {"official", "secondary", 1000, 77},
		"another channel": {"official", "official", 2000, 77},
		"another message": {"official", "official", 1000, 78},
	} {
		t.Run(name, func(t *testing.T) {
			own, err := store.IsOwnPublication(ctx, tc.network, tc.client, tc.channel, tc.msg)
			if err != nil {
				t.Fatalf("IsOwnPublication: %v", err)
			}
			if own {
				t.Fatal("a message this installation did not send must not be suppressed")
			}
		})
	}
}

func TestAnUnrecordedMessageIsNotOurs(t *testing.T) {
	store := stateStore(t)
	own, err := store.IsOwnPublication(context.Background(), "official", "official", 1000, 1)
	if err != nil {
		t.Fatalf("IsOwnPublication: %v", err)
	}
	if own {
		t.Fatal("nothing was published yet, so nothing is our own publication")
	}
}
