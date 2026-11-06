package recovery_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/updates"

	"github.com/mirza/telesync/internal/automation/recovery"
	"github.com/mirza/telesync/internal/channelstore"
)

// fakeState records what the adapter asked of the store and can refuse the
// setters, which is how gotd's "do not create state on write" rule is asserted
// without a database.
//
// Every map is keyed by the connection as well as by the account or peer,
// because that is the real store's contract: two connections must never be
// able to see one another's counters. A fake that ignored the key would make
// the isolation test meaningless.
type fakeState struct {
	states   map[string]channelstore.AutomationState
	pts      map[string]int
	peerHash map[string]int64
	// checkpoints is the per-channel backfill high-water mark, so one fake can
	// stand in for the whole channel store the daemon bridge dispatches onto.
	checkpoints map[string]int64

	refuseSetters bool
	// keys records every client key the adapter passed, so a test can prove two
	// connections never share a row.
	keys []string
}

func newFakeState() *fakeState {
	return &fakeState{
		states:      map[string]channelstore.AutomationState{},
		pts:         map[string]int{},
		peerHash:    map[string]int64{},
		checkpoints: map[string]int64{},
	}
}

func stateKey(key string, userID int64) string { return key + "/" + strconv.FormatInt(userID, 10) }
func ptsKey(key string, channelID int64) string {
	return key + "/" + strconv.FormatInt(channelID, 10)
}
func peerKey(key, kind string, peerID int64) string {
	return key + "/" + kind + "/" + strconv.FormatInt(peerID, 10)
}

func (f *fakeState) note(key string) { f.keys = append(f.keys, key) }

func (f *fakeState) AutomationUpdateState(_ context.Context, key string, userID int64) (channelstore.AutomationState, bool, error) {
	f.note(key)
	s, ok := f.states[stateKey(key, userID)]
	return s, ok, nil
}

func (f *fakeState) SetAutomationUpdateState(_ context.Context, key string, userID int64, s channelstore.AutomationState) error {
	f.note(key)
	f.states[stateKey(key, userID)] = s
	return nil
}

func (f *fakeState) refuse() error {
	if f.refuseSetters {
		return channelstore.ErrAutomationStateNotFound
	}
	return nil
}

func (f *fakeState) SetAutomationUpdatePts(_ context.Context, key string, userID int64, pts int) error {
	f.note(key)
	if err := f.refuse(); err != nil {
		return err
	}
	s := f.states[stateKey(key, userID)]
	s.Pts = pts
	f.states[stateKey(key, userID)] = s
	return nil
}

func (f *fakeState) SetAutomationUpdateQts(_ context.Context, key string, userID int64, qts int) error {
	f.note(key)
	if err := f.refuse(); err != nil {
		return err
	}
	s := f.states[stateKey(key, userID)]
	s.Qts = qts
	f.states[stateKey(key, userID)] = s
	return nil
}

func (f *fakeState) SetAutomationUpdateDate(_ context.Context, key string, userID int64, date int) error {
	f.note(key)
	if err := f.refuse(); err != nil {
		return err
	}
	s := f.states[stateKey(key, userID)]
	s.Date = date
	f.states[stateKey(key, userID)] = s
	return nil
}

func (f *fakeState) SetAutomationUpdateSeq(_ context.Context, key string, userID int64, seq int) error {
	f.note(key)
	if err := f.refuse(); err != nil {
		return err
	}
	s := f.states[stateKey(key, userID)]
	s.Seq = seq
	f.states[stateKey(key, userID)] = s
	return nil
}

func (f *fakeState) SetAutomationUpdateDateSeq(_ context.Context, key string, userID int64, date, seq int) error {
	f.note(key)
	if err := f.refuse(); err != nil {
		return err
	}
	s := f.states[stateKey(key, userID)]
	s.Date, s.Seq = date, seq
	f.states[stateKey(key, userID)] = s
	return nil
}

func (f *fakeState) AutomationChannelPts(_ context.Context, key string, _, channelID int64) (int, bool, error) {
	f.note(key)
	v, ok := f.pts[ptsKey(key, channelID)]
	return v, ok, nil
}

func (f *fakeState) SetAutomationChannelPts(_ context.Context, key string, _, channelID int64, pts int) error {
	f.note(key)
	f.pts[ptsKey(key, channelID)] = pts
	return nil
}

func (f *fakeState) ForEachAutomationChannelPts(_ context.Context, key string, _ int64,
	fn func(ctx context.Context, channelID int64, pts int) error) error {
	f.note(key)
	for id, pts := range f.pts {
		if !strings.HasPrefix(id, key+"/") {
			continue
		}
		channelID, err := strconv.ParseInt(strings.TrimPrefix(id, key+"/"), 10, 64)
		if err != nil {
			continue
		}
		if err := fn(context.Background(), channelID, pts); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeState) AutomationPeerHash(_ context.Context, key string, _ int64, kind string, peerID int64) (int64, bool, error) {
	f.note(key)
	v, ok := f.peerHash[peerKey(key, kind, peerID)]
	return v, ok, nil
}

func (f *fakeState) SetAutomationPeerHash(_ context.Context, key string, _ int64, kind string, peerID, accessHash int64) error {
	f.note(key)
	f.peerHash[peerKey(key, kind, peerID)] = accessHash
	return nil
}

func (f *fakeState) AutomationCheckpoint(_ context.Context, key string, channelID int64) (int64, bool, error) {
	v, ok := f.checkpoints[ptsKey(key, channelID)]
	return v, ok, nil
}

func (f *fakeState) SetAutomationCheckpoint(_ context.Context, key string, channelID, last int64) error {
	f.checkpoints[ptsKey(key, channelID)] = last
	return nil
}

// The whole point of persisting this state is that it survives a restart, so
// what is written must come back exactly as Telegram's counters were.
func TestUpdateStateRoundTripsThroughTheStore(t *testing.T) {
	backing := newFakeState()
	store := recovery.NewStateStore("official", backing)
	ctx := context.Background()

	if _, found, err := store.GetState(ctx, 42); err != nil || found {
		t.Fatalf("before any write: found=%v err=%v, want nothing stored", found, err)
	}
	if err := store.SetState(ctx, 42, updates.State{Pts: 100, Qts: 5, Date: 1700, Seq: 3}); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	got, found, err := store.GetState(ctx, 42)
	if err != nil || !found {
		t.Fatalf("GetState: found=%v err=%v", found, err)
	}
	want := updates.State{Pts: 100, Qts: 5, Date: 1700, Seq: 3}
	if got != want {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
}

// Two connections run side by side and their counters must never be confused.
func TestEachConnectionKeepsItsOwnState(t *testing.T) {
	backing := newFakeState()
	ctx := context.Background()

	if err := recovery.NewStateStore("official", backing).SetState(ctx, 7, updates.State{Pts: 100}); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	other := recovery.NewStateStore("custom@cat.example:443", backing)
	if err := other.SetState(ctx, 7, updates.State{Pts: 5000}); err != nil {
		t.Fatalf("SetState on the second connection: %v", err)
	}

	got, found, err := recovery.NewStateStore("official", backing).GetState(ctx, 7)
	if err != nil || !found || got.Pts != 100 {
		t.Fatalf("first connection saw %+v (found=%v err=%v), want pts 100 untouched", got, found, err)
	}
	for _, key := range backing.keys {
		if key == "" {
			t.Fatal("the adapter passed an empty client key to the store")
		}
	}
}

// gotd documents that the counters must not create state on write. Silently
// inventing a state Telegram never reported would make the manager think it was
// up to date and skip the recovery that a restart needs.
func TestCounterSettersRefuseToInventMissingState(t *testing.T) {
	backing := newFakeState()
	backing.refuseSetters = true
	store := recovery.NewStateStore("official", backing)
	ctx := context.Background()

	setters := map[string]func() error{
		"SetPts":     func() error { return store.SetPts(ctx, 7, 1) },
		"SetQts":     func() error { return store.SetQts(ctx, 7, 1) },
		"SetDate":    func() error { return store.SetDate(ctx, 7, 1) },
		"SetSeq":     func() error { return store.SetSeq(ctx, 7, 1) },
		"SetDateSeq": func() error { return store.SetDateSeq(ctx, 7, 1, 1) },
	}
	for name, fn := range setters {
		if err := fn(); !errors.Is(err, channelstore.ErrAutomationStateNotFound) {
			t.Fatalf("%s = %v, want the refusal to reach the caller", name, err)
		}
	}
}

func TestChannelPointsRoundTrip(t *testing.T) {
	backing := newFakeState()
	store := recovery.NewStateStore("official", backing)
	ctx := context.Background()

	if _, found, _ := store.GetChannelPts(ctx, 7, 55); found {
		t.Fatal("a channel with no saved pts was reported as having one")
	}
	if err := store.SetChannelPts(ctx, 7, 55, 900); err != nil {
		t.Fatalf("SetChannelPts: %v", err)
	}
	pts, found, err := store.GetChannelPts(ctx, 7, 55)
	if err != nil || !found || pts != 900 {
		t.Fatalf("GetChannelPts = %d %v %v, want 900 true nil", pts, found, err)
	}
}

func TestAccessHashesAreHeldApartByKind(t *testing.T) {
	backing := newFakeState()
	store := recovery.NewStateStore("official", backing)
	ctx := context.Background()

	if err := store.SetChannelAccessHash(ctx, 7, 55, 111); err != nil {
		t.Fatalf("SetChannelAccessHash: %v", err)
	}
	if err := store.SetUserAccessHash(ctx, 7, 55, 222); err != nil {
		t.Fatalf("SetUserAccessHash: %v", err)
	}
	channelHash, _, _ := store.GetChannelAccessHash(ctx, 7, 55)
	userHash, _, _ := store.GetUserAccessHash(ctx, 7, 55)
	if channelHash != 111 || userHash != 222 {
		t.Fatalf("hashes = channel %d user %d, want 111 and 222 kept apart", channelHash, userHash)
	}
}

// --- bounded backfill ---

type fakeCheckpoints struct {
	last    int64
	written []int64
	err     error
}

func (f *fakeCheckpoints) AutomationCheckpoint(_ context.Context, _ string, _ int64) (int64, bool, error) {
	return f.last, true, nil
}

func (f *fakeCheckpoints) SetAutomationCheckpoint(_ context.Context, _ string, _, lastMessageID int64) error {
	if f.err != nil {
		return f.err
	}
	f.written = append(f.written, lastMessageID)
	f.last = lastMessageID
	return nil
}

func TestBackfillAdvancesTheCheckpointPastWhatItAccepted(t *testing.T) {
	checkpoints := &fakeCheckpoints{last: 10}
	b := recovery.Backfill{Store: checkpoints, Limit: 3}

	var accepted []int64
	delivered, high, err := b.Run(context.Background(), "official", 55,
		func(_ context.Context, afterID int64, limit int) ([]int64, error) {
			if afterID != 10 {
				t.Fatalf("read after %d, want the stored checkpoint 10", afterID)
			}
			if limit != 3 {
				t.Fatalf("read limit %d, want the configured bound 3", limit)
			}
			return []int64{11, 12, 13}, nil
		},
		func(_ context.Context, id int64) error {
			accepted = append(accepted, id)
			return nil
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if delivered != 3 || high != 13 {
		t.Fatalf("delivered %d high %d, want 3 and 13", delivered, high)
	}
	if !reflect.DeepEqual(accepted, []int64{11, 12, 13}) {
		t.Fatalf("accepted %v, want them oldest first", accepted)
	}
	if len(checkpoints.written) != 1 || checkpoints.written[0] != 13 {
		t.Fatalf("checkpoints written = %v, want [13]", checkpoints.written)
	}
}

// A message that cannot be stored is not lost: the mark stays below it so the
// next pass tries again.
func TestBackfillStopsAtTheFirstRefusalAndKeepsTheMarkBelowIt(t *testing.T) {
	checkpoints := &fakeCheckpoints{last: 10}
	b := recovery.Backfill{Store: checkpoints, Limit: 10}

	var accepted []int64
	_, high, err := b.Run(context.Background(), "official", 55,
		func(_ context.Context, _ int64, _ int) ([]int64, error) {
			return []int64{11, 12, 13}, nil
		},
		func(_ context.Context, id int64) error {
			if id == 12 {
				return errors.New("cannot store")
			}
			accepted = append(accepted, id)
			return nil
		})
	if err == nil {
		t.Fatal("a refusal was reported as success")
	}
	if !reflect.DeepEqual(accepted, []int64{11}) {
		t.Fatalf("accepted %v, want only the message before the failure", accepted)
	}
	if high != 11 {
		t.Fatalf("high-water mark = %d, want 11 so 12 is retried", high)
	}
	if len(checkpoints.written) != 0 {
		t.Fatalf("the checkpoint was advanced to %v despite the failure", checkpoints.written)
	}
}

// Recovery is a bounded drain, not a download. A call that returned the limit
// has more to fetch and says so; a short batch means caught up.
func TestBackfillIsBoundedAndReportsWhetherItHasCaughtUp(t *testing.T) {
	b := recovery.Backfill{Store: &fakeCheckpoints{}, Limit: 2}

	delivered, _, err := b.Run(context.Background(), "official", 55,
		func(_ context.Context, _ int64, _ int) ([]int64, error) { return []int64{1, 2}, nil },
		func(_ context.Context, _ int64) error { return nil })
	if err != nil || delivered != 2 {
		t.Fatalf("first pass delivered %d err %v, want a full bound of 2", delivered, err)
	}

	delivered, _, err = b.Run(context.Background(), "official", 55,
		func(_ context.Context, _ int64, _ int) ([]int64, error) { return nil, nil },
		func(_ context.Context, _ int64) error { return nil })
	if err != nil || delivered != 0 {
		t.Fatalf("second pass delivered %d err %v, want caught up", delivered, err)
	}
}

func TestBackfillNeverGoesBackwardsOverAnEmptyChannel(t *testing.T) {
	checkpoints := &fakeCheckpoints{last: 42}
	b := recovery.Backfill{Store: checkpoints, Limit: 20}

	delivered, high, err := b.Run(context.Background(), "official", 55,
		func(_ context.Context, _ int64, _ int) ([]int64, error) { return nil, nil },
		func(_ context.Context, _ int64) error { return nil })
	if err != nil || delivered != 0 {
		t.Fatalf("Run = %d %v", delivered, err)
	}
	if high != 42 {
		t.Fatalf("high-water mark moved to %d, want it to stay at 42", high)
	}
	if len(checkpoints.written) != 0 {
		t.Fatalf("the checkpoint was rewritten for no reason: %v", checkpoints.written)
	}
}
