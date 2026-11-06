package recovery

import (
	"context"
	"fmt"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// This file bridges gotd's update-state storage across the worker/daemon
// boundary.
//
// A worker runs the gotd update manager but holds no database; the durable
// counters live in the daemon's channel store. StatePersistence is the seam both
// sides meet at: NewStatePersistence turns the worker's bridge client into one,
// and NewStateBackend turns the daemon's channel store into the wire's handler.
// The two are mirror images, so a change on one side cannot silently diverge
// from the other.

// StateCaller performs one update-state call over the automation bridge. It is
// satisfied by *rpc.AutomationClient.
type StateCaller interface {
	CallState(ctx context.Context, call rpc.StateCall) (rpc.StateResult, error)
}

// NewStatePersistence returns a StatePersistence backed by the automation
// bridge. Each call becomes one bounded request to the daemon, where the real
// channel store lives.
//
// The connection (clientKey) is a parameter of every StatePersistence method and
// is forwarded as-is, so several independent workers keep their counters apart
// even though they share one daemon.
func NewStatePersistence(call StateCaller) StatePersistence {
	return &bridgePersistence{call: call}
}

type bridgePersistence struct {
	call StateCaller
}

func (p *bridgePersistence) AutomationUpdateState(ctx context.Context, clientKey string, userID int64) (channelstore.AutomationState, bool, error) {
	res, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateGetState, ClientKey: clientKey, UserID: userID,
	})
	if err != nil {
		return channelstore.AutomationState{}, false, err
	}
	if !res.Found || res.State == nil {
		return channelstore.AutomationState{}, false, nil
	}
	return snapshotToState(*res.State), true, nil
}

func (p *bridgePersistence) SetAutomationUpdateState(ctx context.Context, clientKey string, userID int64, state channelstore.AutomationState) error {
	snap := stateToSnapshot(state)
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetState, ClientKey: clientKey, UserID: userID, State: &snap,
	})
	return err
}

func (p *bridgePersistence) SetAutomationUpdatePts(ctx context.Context, clientKey string, userID int64, pts int) error {
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetPts, ClientKey: clientKey, UserID: userID, Pts: pts,
	})
	return err
}

func (p *bridgePersistence) SetAutomationUpdateQts(ctx context.Context, clientKey string, userID int64, qts int) error {
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetQts, ClientKey: clientKey, UserID: userID, Qts: qts,
	})
	return err
}

func (p *bridgePersistence) SetAutomationUpdateDate(ctx context.Context, clientKey string, userID int64, date int) error {
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetDate, ClientKey: clientKey, UserID: userID, Date: date,
	})
	return err
}

func (p *bridgePersistence) SetAutomationUpdateSeq(ctx context.Context, clientKey string, userID int64, seq int) error {
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetSeq, ClientKey: clientKey, UserID: userID, Seq: seq,
	})
	return err
}

func (p *bridgePersistence) SetAutomationUpdateDateSeq(ctx context.Context, clientKey string, userID int64, date, seq int) error {
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetDateSeq, ClientKey: clientKey, UserID: userID, Date: date, Seq: seq,
	})
	return err
}

func (p *bridgePersistence) AutomationChannelPts(ctx context.Context, clientKey string, userID, channelID int64) (int, bool, error) {
	res, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateGetChannelPts, ClientKey: clientKey, UserID: userID, ChannelID: channelID,
	})
	if err != nil {
		return 0, false, err
	}
	return res.Pts, res.Found, nil
}

func (p *bridgePersistence) SetAutomationChannelPts(ctx context.Context, clientKey string, userID, channelID int64, pts int) error {
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetChannelPts, ClientKey: clientKey, UserID: userID, ChannelID: channelID, Pts: pts,
	})
	return err
}

func (p *bridgePersistence) ForEachAutomationChannelPts(ctx context.Context, clientKey string, userID int64,
	f func(ctx context.Context, channelID int64, pts int) error) error {

	res, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateListChannels, ClientKey: clientKey, UserID: userID,
	})
	if err != nil {
		return err
	}
	for _, ch := range res.Channels {
		if err := f(ctx, ch.ChannelID, ch.Pts); err != nil {
			return err
		}
	}
	return nil
}

func (p *bridgePersistence) AutomationPeerHash(ctx context.Context, clientKey string, userID int64, kind string, peerID int64) (int64, bool, error) {
	res, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateGetPeerHash, ClientKey: clientKey, UserID: userID, Kind: kind, TargetID: peerID,
	})
	if err != nil {
		return 0, false, err
	}
	return res.AccessHash, res.Found, nil
}

func (p *bridgePersistence) SetAutomationPeerHash(ctx context.Context, clientKey string, userID int64, kind string, peerID, accessHash int64) error {
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetPeerHash, ClientKey: clientKey, UserID: userID, Kind: kind,
		TargetID: peerID, AccessHash: accessHash,
	})
	return err
}

// AutomationStore is the daemon's durable automation state: the gotd update
// counters and the per-channel backfill checkpoints. *channelstore.Store
// satisfies it. It is what the daemon side of the bridge dispatches onto.
type AutomationStore interface {
	StatePersistence
	CheckpointStore
}

// NewCheckpointPersistence returns a CheckpointStore backed by the automation
// bridge, for the bounded gap backfill a worker runs. The checkpoint lives in
// the daemon's channel store so a restart does not re-download what was already
// consumed.
func NewCheckpointPersistence(call StateCaller) CheckpointStore {
	return &bridgePersistence{call: call}
}

// NewStateBackend returns the daemon half of the bridge: the wire's StateBackend
// dispatching onto a real AutomationStore, which the daemon backs with its
// channel store.
//
// store may be nil, in which case every call reports that state is unavailable
// rather than pretending to have stored it.
func NewStateBackend(store AutomationStore) rpc.StateBackend {
	if store == nil {
		return nil
	}
	return rpc.StateBackendFunc(func(ctx context.Context, call rpc.StateCall) (rpc.StateResult, error) {
		return dispatchState(ctx, store, call)
	})
}

func dispatchState(ctx context.Context, store AutomationStore, call rpc.StateCall) (rpc.StateResult, error) {
	switch call.Method {
	case rpc.StateGetState:
		state, found, err := store.AutomationUpdateState(ctx, call.ClientKey, call.UserID)
		if err != nil {
			return rpc.StateResult{}, err
		}
		snap := stateToSnapshot(state)
		return rpc.StateResult{Found: found, State: &snap}, nil

	case rpc.StateSetState:
		if call.State == nil {
			return rpc.StateResult{}, fmt.Errorf("recovery: set_state needs a state")
		}
		return rpc.StateResult{}, store.SetAutomationUpdateState(ctx, call.ClientKey, call.UserID, snapshotToState(*call.State))

	case rpc.StateSetPts:
		return rpc.StateResult{}, store.SetAutomationUpdatePts(ctx, call.ClientKey, call.UserID, call.Pts)
	case rpc.StateSetQts:
		return rpc.StateResult{}, store.SetAutomationUpdateQts(ctx, call.ClientKey, call.UserID, call.Qts)
	case rpc.StateSetDate:
		return rpc.StateResult{}, store.SetAutomationUpdateDate(ctx, call.ClientKey, call.UserID, call.Date)
	case rpc.StateSetSeq:
		return rpc.StateResult{}, store.SetAutomationUpdateSeq(ctx, call.ClientKey, call.UserID, call.Seq)
	case rpc.StateSetDateSeq:
		return rpc.StateResult{}, store.SetAutomationUpdateDateSeq(ctx, call.ClientKey, call.UserID, call.Date, call.Seq)

	case rpc.StateGetChannelPts:
		pts, found, err := store.AutomationChannelPts(ctx, call.ClientKey, call.UserID, call.ChannelID)
		if err != nil {
			return rpc.StateResult{}, err
		}
		return rpc.StateResult{Found: found, Pts: pts}, nil

	case rpc.StateSetChannelPts:
		return rpc.StateResult{}, store.SetAutomationChannelPts(ctx, call.ClientKey, call.UserID, call.ChannelID, call.Pts)

	case rpc.StateListChannels:
		var out []rpc.ChannelPts
		err := store.ForEachAutomationChannelPts(ctx, call.ClientKey, call.UserID,
			func(_ context.Context, channelID int64, pts int) error {
				out = append(out, rpc.ChannelPts{ChannelID: channelID, Pts: pts})
				return nil
			})
		if err != nil {
			return rpc.StateResult{}, err
		}
		return rpc.StateResult{Channels: out}, nil

	case rpc.StateGetPeerHash:
		hash, found, err := store.AutomationPeerHash(ctx, call.ClientKey, call.UserID, call.Kind, call.TargetID)
		if err != nil {
			return rpc.StateResult{}, err
		}
		return rpc.StateResult{Found: found, AccessHash: hash}, nil

	case rpc.StateSetPeerHash:
		return rpc.StateResult{}, store.SetAutomationPeerHash(ctx, call.ClientKey, call.UserID, call.Kind, call.TargetID, call.AccessHash)

	case rpc.StateGetCheckpoint:
		last, found, err := store.AutomationCheckpoint(ctx, call.ClientKey, call.ChannelID)
		if err != nil {
			return rpc.StateResult{}, err
		}
		return rpc.StateResult{Found: found, LastMessageID: last}, nil

	case rpc.StateSetCheckpoint:
		return rpc.StateResult{}, store.SetAutomationCheckpoint(ctx, call.ClientKey, call.ChannelID, call.LastMessageID)

	default:
		return rpc.StateResult{}, fmt.Errorf("recovery: unknown state method %q", call.Method)
	}
}

// AutomationCheckpoint reads one channel's backfill high-water mark.
func (p *bridgePersistence) AutomationCheckpoint(ctx context.Context, clientKey string, channelID int64) (int64, bool, error) {
	res, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateGetCheckpoint, ClientKey: clientKey, ChannelID: channelID,
	})
	if err != nil {
		return 0, false, err
	}
	return res.LastMessageID, res.Found, nil
}

// SetAutomationCheckpoint advances one channel's backfill high-water mark.
func (p *bridgePersistence) SetAutomationCheckpoint(ctx context.Context, clientKey string, channelID, lastMessageID int64) error {
	_, err := p.call.CallState(ctx, rpc.StateCall{
		Method: rpc.StateSetCheckpoint, ClientKey: clientKey, ChannelID: channelID, LastMessageID: lastMessageID,
	})
	return err
}

func stateToSnapshot(s channelstore.AutomationState) rpc.StateSnapshot {
	return rpc.StateSnapshot{Pts: s.Pts, Qts: s.Qts, Date: s.Date, Seq: s.Seq}
}

func snapshotToState(s rpc.StateSnapshot) channelstore.AutomationState {
	return channelstore.AutomationState{Pts: s.Pts, Qts: s.Qts, Date: s.Date, Seq: s.Seq}
}
