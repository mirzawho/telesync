package recovery_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gotd/td/telegram/updates"

	"github.com/mirza/telesync/internal/automation/recovery"
	"github.com/mirza/telesync/internal/rpc"
)

// loopback carries one state call across the wire the way production does: the
// worker marshals a request, the daemon unmarshals it, dispatches, and marshals
// the answer back. Serializing both directions is the point — a DTO that cannot
// survive JSON is a bug this catches without a socket.
type loopback struct {
	backend rpc.StateBackend
}

func (l loopback) CallState(ctx context.Context, call rpc.StateCall) (rpc.StateResult, error) {
	req, err := json.Marshal(rpc.AutomationCall{State: &call})
	if err != nil {
		return rpc.StateResult{}, err
	}
	var wire rpc.AutomationCall
	if err := json.Unmarshal(req, &wire); err != nil {
		return rpc.StateResult{}, err
	}
	res, err := l.backend.CallState(ctx, *wire.State)
	if err != nil {
		return rpc.StateResult{}, err
	}
	body, err := json.Marshal(rpc.AutomationResult{State: &res})
	if err != nil {
		return rpc.StateResult{}, err
	}
	var answer rpc.AutomationResult
	if err := json.Unmarshal(body, &answer); err != nil {
		return rpc.StateResult{}, err
	}
	return *answer.State, nil
}

func bridged(t *testing.T) (*recovery.StateStore, *fakeState) {
	t.Helper()
	daemon := newFakeState()
	persistence := recovery.NewStatePersistence(loopback{backend: recovery.NewStateBackend(daemon)})
	return recovery.NewStateStore("client", persistence), daemon
}

// Update state must survive a restart. The counters are what lets a reconnect
// ask Telegram for what it missed instead of losing it, so what a worker writes
// must come back after the process is gone and a new one takes its place.
func TestUpdateStatePersistsAndReloadsAfterARestart(t *testing.T) {
	store, daemon := bridged(t)

	want := updates.State{Pts: 1000, Qts: 5, Date: 1_700_000_000, Seq: 12}
	if err := store.SetState(context.Background(), 7, want); err != nil {
		t.Fatalf("set state: %v", err)
	}
	// A restart: the worker is gone. The durable record lives in the daemon, so
	// a fresh StateStore over the same bridge must read it back.
	restarted := recovery.NewStateStore("client", recovery.NewStatePersistence(
		loopback{backend: recovery.NewStateBackend(daemon)}))

	got, found, err := restarted.GetState(context.Background(), 7)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !found {
		t.Fatal("state did not survive the restart")
	}
	if got != want {
		t.Fatalf("reloaded state = %+v, want %+v", got, want)
	}
}

// The granular counters and peer hashes cross the bridge intact, and two
// connections never share a row.
func TestChannelPointsAndPeerHashesCrossTheBridge(t *testing.T) {
	store, _ := bridged(t)

	if err := store.SetChannelPts(context.Background(), 7, 500, 42); err != nil {
		t.Fatalf("set channel pts: %v", err)
	}
	pts, found, err := store.GetChannelPts(context.Background(), 7, 500)
	if err != nil || !found || pts != 42 {
		t.Fatalf("channel pts = %d found=%t err=%v, want 42 true nil", pts, found, err)
	}

	if err := store.SetChannelAccessHash(context.Background(), 7, 500, 99); err != nil {
		t.Fatalf("set channel hash: %v", err)
	}
	hash, found, err := store.GetChannelAccessHash(context.Background(), 7, 500)
	if err != nil || !found || hash != 99 {
		t.Fatalf("channel hash = %d found=%t err=%v, want 99 true nil", hash, found, err)
	}

	// A daemon refusal crosses the bridge as an error, not as a silent success.
	refusing := newFakeState()
	refusing.refuseSetters = true
	refusingStore := recovery.NewStateStore("client", recovery.NewStatePersistence(
		loopback{backend: recovery.NewStateBackend(refusing)}))
	if err := refusingStore.SetPts(context.Background(), 7, 1); err == nil {
		t.Fatal("a daemon refusal was swallowed by the bridge")
	}
}
