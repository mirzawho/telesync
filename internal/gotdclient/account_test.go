package gotdclient

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/automation/recovery"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/profile"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

const (
	accA = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	accB = "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// memState records every durable update-state write with the scope key it was
// made under, so a test can prove two accounts never share a counter.
type memState struct {
	mu    sync.Mutex
	byKey map[string]*channelstore.AutomationState
	pts   map[string]int
	peers map[string]int64
	seen  []string
}

func newMemState() *memState {
	return &memState{
		byKey: map[string]*channelstore.AutomationState{},
		pts:   map[string]int{},
		peers: map[string]int64{},
	}
}

func (m *memState) key(scope string, userID, channelID int64) string {
	return scope + "|" + strconv.FormatInt(userID, 10) + "|" + strconv.FormatInt(channelID, 10)
}

func (m *memState) record(scope string) {
	m.seen = append(m.seen, scope)
}

func (m *memState) AutomationUpdateState(_ context.Context, scope string, userID int64) (channelstore.AutomationState, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(scope)
	st, ok := m.byKey[m.key(scope, userID, 0)]
	if !ok {
		return channelstore.AutomationState{}, false, nil
	}
	return *st, true, nil
}

func (m *memState) SetAutomationUpdateState(_ context.Context, scope string, userID int64, st channelstore.AutomationState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(scope)
	m.byKey[m.key(scope, userID, 0)] = &st
	return nil
}

func (m *memState) SetAutomationUpdatePts(ctx context.Context, scope string, userID int64, pts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(scope)
	m.pts[m.key(scope, userID, 0)] = pts
	return nil
}
func (m *memState) SetAutomationUpdateQts(context.Context, string, int64, int) error { return nil }
func (m *memState) SetAutomationUpdateDate(context.Context, string, int64, int) error {
	return nil
}
func (m *memState) SetAutomationUpdateSeq(context.Context, string, int64, int) error { return nil }
func (m *memState) SetAutomationUpdateDateSeq(context.Context, string, int64, int, int) error {
	return nil
}

func (m *memState) AutomationChannelPts(_ context.Context, scope string, userID, channelID int64) (int, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(scope)
	v, ok := m.pts[m.key(scope, userID, channelID)]
	return v, ok, nil
}

func (m *memState) SetAutomationChannelPts(_ context.Context, scope string, userID, channelID int64, pts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(scope)
	m.pts[m.key(scope, userID, channelID)] = pts
	return nil
}

func (m *memState) ForEachAutomationChannelPts(context.Context, string, int64,
	func(context.Context, int64, int) error) error {
	return nil
}

func (m *memState) AutomationPeerHash(_ context.Context, scope string, userID int64, kind string, peerID int64) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(scope)
	v, ok := m.peers[scope+"|"+strconv.FormatInt(userID, 10)+"|"+kind+"|"+strconv.FormatInt(peerID, 10)]
	return v, ok, nil
}

func (m *memState) SetAutomationPeerHash(_ context.Context, scope string, userID int64, kind string, peerID, accessHash int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(scope)
	m.peers[scope+"|"+strconv.FormatInt(userID, 10)+"|"+kind+"|"+strconv.FormatInt(peerID, 10)] = accessHash
	return nil
}

func (m *memState) scopes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.seen...)
}

func accountManager(accountID, serverKey string, state recovery.StatePersistence) *Manager {
	spec := &profile.Spec{Key: serverKey, Kind: profile.Official}
	return NewManager(spec, ManagerOptions{
		AccountID:    accountID,
		RemoteUserID: 1007,
		Automation: &AutomationOptions{
			NetworkKey: serverKey,
			State:      state,
		},
	})
}

// Update state is persisted per (account, server, channel). There is never one
// global pts: two accounts watching the same remote channel keep two cursors.
func TestUpdateStateIsScopedToTheAccountNotTheServerKey(t *testing.T) {
	state := newMemState()
	// Both accounts are on the same server definition, so a scope keyed on the
	// server would collapse them.
	a := accountManager(accA, "official", state)
	b := accountManager(accB, "official", state)

	storeA := a.buildStateStore()
	storeB := b.buildStateStore()
	if storeA == nil || storeB == nil {
		t.Fatal("automation must build a state store")
	}
	if storeA.ClientKey() != accA {
		t.Errorf("A's update state is scoped to %q, want its account id %q", storeA.ClientKey(), accA)
	}
	if storeB.ClientKey() != accB {
		t.Errorf("B's update state is scoped to %q, want its account id %q", storeB.ClientKey(), accB)
	}
	if storeA.ClientKey() == storeB.ClientKey() {
		t.Fatal("two accounts share one update-state scope")
	}
	for _, s := range state.scopes() {
		if s == "official" {
			t.Fatal("update state was persisted under the server key: one global pts")
		}
	}
}

func TestTwoAccountsOnOneChannelKeepTheirOwnCursors(t *testing.T) {
	state := newMemState()
	storeA := accountManager(accA, "official", state).buildStateStore()
	storeB := accountManager(accB, "official", state).buildStateStore()

	ctx := context.Background()
	if err := storeA.SetChannelPts(ctx, 7, 4242, 100); err != nil {
		t.Fatalf("A SetChannelPts: %v", err)
	}
	if err := storeB.SetChannelPts(ctx, 7, 4242, 55); err != nil {
		t.Fatalf("B SetChannelPts: %v", err)
	}

	gotA, ok, err := storeA.GetChannelPts(ctx, 7, 4242)
	if err != nil || !ok {
		t.Fatalf("A GetChannelPts: ok=%v err=%v", ok, err)
	}
	gotB, ok, err := storeB.GetChannelPts(ctx, 7, 4242)
	if err != nil || !ok {
		t.Fatalf("B GetChannelPts: ok=%v err=%v", ok, err)
	}
	if gotA != 100 {
		t.Errorf("A's cursor = %d, want its own 100", gotA)
	}
	if gotB != 55 {
		t.Errorf("B's cursor = %d, want its own 55, not A's 100", gotB)
	}
}

func TestAReconnectResumesFromTheAccountsOwnDurableState(t *testing.T) {
	state := newMemState()
	ctx := context.Background()

	// A first connection advances its own counter.
	first := accountManager(accA, "official", state).buildStateStore()
	if err := first.SetState(ctx, 7, updates.State{Pts: 100, Qts: 5, Date: 900, Seq: 3}); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	// A second account on the same server starts from nothing, not from A's.
	other := accountManager(accB, "official", state).buildStateStore()
	if _, found, err := other.GetState(ctx, 7); err != nil || found {
		t.Fatalf("B inherited A's update state: found=%v err=%v", found, err)
	}

	// A's own reconnect resumes where its own durable state left off.
	resumed := accountManager(accA, "official", state).buildStateStore()
	got, found, err := resumed.GetState(ctx, 7)
	if err != nil || !found {
		t.Fatalf("A GetState: found=%v err=%v", found, err)
	}
	if got.Pts != 100 || got.Qts != 5 || got.Date != 900 || got.Seq != 3 {
		t.Errorf("A resumed from %+v, want its own 100/5/900/3", got)
	}
}

// The peer cache is account-scoped too: an access hash learned by one account is
// never used to address a channel on another's behalf.
func TestPeerAccessHashesAreAccountScoped(t *testing.T) {
	state := newMemState()
	storeA := accountManager(accA, "official", state).buildStateStore()
	storeB := accountManager(accB, "official", state).buildStateStore()
	ctx := context.Background()

	if err := storeA.SetChannelAccessHash(ctx, 7, 4242, 0xA11CE); err != nil {
		t.Fatalf("A SetChannelAccessHash: %v", err)
	}
	if _, found, err := storeB.GetChannelAccessHash(ctx, 7, 4242); err != nil || found {
		t.Fatalf("B read A's access hash: found=%v err=%v", found, err)
	}
	got, found, err := storeA.GetChannelAccessHash(ctx, 7, 4242)
	if err != nil || !found || got != 0xA11CE {
		t.Fatalf("A's own access hash = %x found=%v err=%v", got, found, err)
	}
}

// An observation is attributed to the account that saw it, never to the server
// it happened on.
func TestAnObservationCarriesTheAccountThatSawIt(t *testing.T) {
	var got []rpc.IngestMessage
	h := newUpdateHandler(accA, accA, "official", IngestSinkFunc(
		func(_ context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
			got = append(got, msg)
			return rpc.IngestInserted, nil
		}), time.Now, nil)

	if err := h.Handle(context.Background(), newChannelMessage(4242, 11)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("observations = %d, want 1", len(got))
	}
	if got[0].AccountID != accA {
		t.Errorf("observation account_id = %q, want the account that saw it %q", got[0].AccountID, accA)
	}
	if got[0].ClientKey != accA {
		t.Errorf("observation client_key = %q, want the account id %q", got[0].ClientKey, accA)
	}
	if got[0].NetworkKey != "official" {
		t.Errorf("observation network_key = %q, want the server key", got[0].NetworkKey)
	}
	if strings.Contains(got[0].ClientKey, "official") {
		t.Error("the observation was attributed to the server, not the account")
	}
}

func TestTwoAccountsOnOneNetworkDescribeTwoLogicalMessages(t *testing.T) {
	// The same remote channel, observed by two accounts: two independent
	// observations, so one user's event is never suppressed as another's
	// duplicate.
	var a, b []rpc.IngestMessage
	ha := newUpdateHandler(accA, accA, "official", IngestSinkFunc(
		func(_ context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
			a = append(a, msg)
			return rpc.IngestInserted, nil
		}), time.Now, nil)
	hb := newUpdateHandler(accB, accB, "official", IngestSinkFunc(
		func(_ context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
			b = append(b, msg)
			return rpc.IngestInserted, nil
		}), time.Now, nil)

	ctx := context.Background()
	if err := ha.Handle(ctx, newChannelMessage(4242, 11)); err != nil {
		t.Fatal(err)
	}
	if err := hb.Handle(ctx, newChannelMessage(4242, 11)); err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("observations a=%d b=%d, want one each", len(a), len(b))
	}
	if a[0].AccountID == b[0].AccountID {
		t.Fatal("two accounts produced one identity: a duplicate would suppress a user's event")
	}
	if a[0].AccountID != accA || b[0].AccountID != accB {
		t.Fatalf("observations were attributed to the wrong accounts: %q and %q",
			a[0].AccountID, b[0].AccountID)
	}
}

func TestConnectionLogsCarryTheOpaqueAccountID(t *testing.T) {
	var lines []string
	spec := &profile.Spec{Key: "official", Kind: profile.Official}
	m := NewManager(spec, ManagerOptions{
		AccountID: accA,
		OnEvent: func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	})
	m.emitLockedForTest(tgapi.StateReady)

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, accA) {
		t.Errorf("the connection log must carry the opaque account id; got:\n%s", out)
	}
	if strings.Contains(out, "official") {
		t.Errorf("the connection log identified the account by its server key:\n%s", out)
	}
	for _, forbidden := range []string{"+98", "phone", "session", ".box"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the connection log leaked %q:\n%s", forbidden, out)
		}
	}
}

func TestAWorkerWithoutAnAccountIsNotStarted(t *testing.T) {
	spec := &profile.Spec{Key: "official", Kind: profile.Official}
	m := NewManager(spec, ManagerOptions{Automation: &AutomationOptions{NetworkKey: "official"}})
	if err := m.Validate(); err == nil {
		t.Error("a manager with no account identity must be refused: it would share state across accounts")
	}

	m = accountManager(accA, "official", newMemState())
	if err := m.Validate(); err != nil {
		t.Errorf("an account-bound manager must validate: %v", err)
	}
}

func TestManagerAccountBindingChecksThePersistedIdentity(t *testing.T) {
	m := accountManager(accA, "official", newMemState())
	if err := m.ValidateAccount(accA, 1007, "official", "telegram"); err != nil {
		t.Fatalf("ValidateAccount: %v", err)
	}
	for name, check := range map[string]func() error{
		"another account": func() error {
			return m.ValidateAccount(accB, 1007, "official", "telegram")
		},
		"another remote user": func() error {
			return m.ValidateAccount(accA, 999, "official", "telegram")
		},
		"another server": func() error {
			return m.ValidateAccount(accA, 1007, "cat", "telegram")
		},
		"another network": func() error {
			return m.ValidateAccount(accA, 1007, "official", "custom")
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil {
				t.Error("manager accepted account metadata that does not match its trusted profile")
			}
		})
	}
}

func TestManagerRefusesNonCanonicalAccountIDs(t *testing.T) {
	spec := &profile.Spec{Key: "official", Kind: profile.Official}
	m := NewManager(spec, ManagerOptions{
		AccountID:    "acc_" + strings.Repeat("a", 25),
		RemoteUserID: 1007,
	})
	if err := m.Validate(); err == nil {
		t.Fatal("a malformed account id must not become the update/session scope")
	}
}

func TestManagerStartIsIdempotentEvenWhenTheContextIsAlreadyCanceled(t *testing.T) {
	spec := &profile.Spec{Key: "official", Kind: profile.Official}
	m := NewManager(spec, ManagerOptions{AccountID: accA, RemoteUserID: 1007})
	ctx, cancel := context.WithCancel(context.Background())

	m.Start(ctx)
	first := m.runDone
	if first == nil {
		t.Fatal("Start did not publish its lifecycle before returning")
	}
	m.Start(ctx)
	if m.runDone != first {
		t.Fatal("a second Start replaced the manager's lifecycle and wait signal")
	}
	cancel()
	waitCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := m.Wait(waitCtx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestManagerDoesNotAcceptOrExposeAChangedRemoteUser(t *testing.T) {
	m := accountManager(accA, "official", newMemState())

	err := m.acceptRemoteUser(&tg.User{ID: 999, Username: "other-user"}, 2)
	if err == nil {
		t.Fatal("Self for another remote user must be rejected")
	}
	status := m.Status(context.Background())
	if status.State != tgapi.StateError || status.User != nil {
		t.Errorf("status = %+v, want an identity error without an exposed user", status)
	}
	if status.Err == "" {
		t.Error("identity mismatch must remain visible as a safe error")
	}

	if err := m.acceptRemoteUser(&tg.User{ID: 1007, Username: "expected-user"}, 2); err != nil {
		t.Fatalf("the persisted remote user should be accepted: %v", err)
	}
	status = m.Status(context.Background())
	if status.State != tgapi.StateReady || status.User == nil || status.User.ID != 1007 {
		t.Errorf("matching account status = %+v, want the persisted remote identity", status)
	}
}

// newChannelMessage builds one update batch carrying a single new channel
// message.
func newChannelMessage(channelID int64, messageID int) tg.UpdatesClass {
	return &tg.Updates{
		Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{
			Message: &tg.Message{
				ID:     messageID,
				PeerID: &tg.PeerChannel{ChannelID: channelID},
				Date:   1700000000,
			},
		}},
	}
}

// A gap is recovered in the account's own scope. Its checkpoint is its own, so
// backfilling one account never advances or consumes another's.
func TestGapRecoveryIsScopedToTheAccount(t *testing.T) {
	cpA := &memCheckpoints{m: map[int64]int64{}}
	cpB := &memCheckpoints{m: map[int64]int64{}}
	state := newMemState()

	mA := accountManager(accA, "official", state)
	mB := accountManager(accB, "official", state)
	mA.automation.Checkpoints = cpA
	mB.automation.Checkpoints = cpB

	bfA := mA.buildBackfillerForTest()
	bfB := mB.buildBackfillerForTest()
	if bfA.clientKey != accA {
		t.Errorf("A's gap recovery is scoped to %q, want its account id", bfA.clientKey)
	}
	if bfB.clientKey != accB {
		t.Errorf("B's gap recovery is scoped to %q, want its account id", bfB.clientKey)
	}

	ctx := context.Background()
	read := func(context.Context, int64, int) ([]int64, error) { return []int64{100, 101}, nil }
	accept := func(context.Context, int64) error { return nil }
	if _, _, err := (recovery.Backfill{Store: cpA, Limit: 10}).Run(ctx, bfA.clientKey, 4242, read, accept); err != nil {
		t.Fatalf("A backfill: %v", err)
	}

	got, found, err := cpA.AutomationCheckpoint(ctx, accA, 4242)
	if err != nil || !found || got != 101 {
		t.Fatalf("A's checkpoint = %d found=%v err=%v, want 101", got, found, err)
	}
	if _, found, _ := cpB.AutomationCheckpoint(ctx, accB, 4242); found {
		t.Fatal("A's gap recovery advanced B's checkpoint")
	}

	// B recovers its own gap from its own mark, not from A's.
	readB := func(context.Context, int64, int) ([]int64, error) { return []int64{7}, nil }
	if _, _, err := (recovery.Backfill{Store: cpB, Limit: 10}).Run(ctx, bfB.clientKey, 4242, readB, accept); err != nil {
		t.Fatalf("B backfill: %v", err)
	}
	gotB, foundB, _ := cpB.AutomationCheckpoint(ctx, accB, 4242)
	if !foundB || gotB != 7 {
		t.Fatalf("B's checkpoint = %d found=%v, want its own 7", gotB, foundB)
	}
}
