// Package recovery keeps enough durable state to resume after a disconnect,
// a restart or a crash.
//
// Two things live here. The first is the backing store gotd's update manager
// needs, so its pts/qts/seq counters survive a process boundary and missed
// updates can be asked for instead of lost. The second is the bounded fallback
// for the gaps the update manager cannot close on its own.
//
// Neither backfills history for its own sake. Recovery covers what automation
// was already entitled to see; it never turns a newly enabled workflow into a
// publisher of a channel's archive.
package recovery

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram/updates"

	"github.com/mirza/telesync/internal/channelstore"
)

// StatePersistence is the durable state the store needs. It is satisfied by
// *channelstore.Store and named here so the adapter can be tested without a
// database.
type StatePersistence interface {
	AutomationUpdateState(ctx context.Context, clientKey string, userID int64) (channelstore.AutomationState, bool, error)
	SetAutomationUpdateState(ctx context.Context, clientKey string, userID int64, state channelstore.AutomationState) error
	SetAutomationUpdatePts(ctx context.Context, clientKey string, userID int64, pts int) error
	SetAutomationUpdateQts(ctx context.Context, clientKey string, userID int64, qts int) error
	SetAutomationUpdateDate(ctx context.Context, clientKey string, userID int64, date int) error
	SetAutomationUpdateSeq(ctx context.Context, clientKey string, userID int64, seq int) error
	SetAutomationUpdateDateSeq(ctx context.Context, clientKey string, userID int64, date, seq int) error

	AutomationChannelPts(ctx context.Context, clientKey string, userID, channelID int64) (int, bool, error)
	SetAutomationChannelPts(ctx context.Context, clientKey string, userID, channelID int64, pts int) error
	ForEachAutomationChannelPts(ctx context.Context, clientKey string, userID int64,
		f func(ctx context.Context, channelID int64, pts int) error) error

	AutomationPeerHash(ctx context.Context, clientKey string, userID int64, kind string, peerID int64) (int64, bool, error)
	SetAutomationPeerHash(ctx context.Context, clientKey string, userID int64, kind string, peerID, accessHash int64) error
}

// Peer kinds, matching the check constraint on automation_peer_hashes.
const (
	PeerChannel = "channel"
	PeerUser    = "user"
)

// StateStore is one connection's share of the durable update state.
//
// gotd's interfaces are keyed by user id only, while this installation runs
// several independent connections whose counters must not be confused with one
// another. The connection is therefore bound once here rather than threaded
// through every call, and the two never mix.
type StateStore struct {
	clientKey string
	state     StatePersistence
}

// NewStateStore binds the durable state to one connection.
func NewStateStore(clientKey string, state StatePersistence) *StateStore {
	return &StateStore{clientKey: clientKey, state: state}
}

// ClientKey reports the connection this store is bound to.
func (s *StateStore) ClientKey() string { return s.clientKey }

// --- updates.StateStorage ---

// GetState returns the stored user state.
func (s *StateStore) GetState(ctx context.Context, userID int64) (updates.State, bool, error) {
	rec, found, err := s.state.AutomationUpdateState(ctx, s.clientKey, userID)
	if err != nil || !found {
		return updates.State{}, false, err
	}
	return updates.State{Pts: rec.Pts, Qts: rec.Qts, Date: rec.Date, Seq: rec.Seq}, true, nil
}

// SetState stores the whole user state.
func (s *StateStore) SetState(ctx context.Context, userID int64, state updates.State) error {
	return s.state.SetAutomationUpdateState(ctx, s.clientKey, userID, channelstore.AutomationState{
		Pts: state.Pts, Qts: state.Qts, Date: state.Date, Seq: state.Seq,
	})
}

// SetPts advances the pts counter.
//
// gotd requires the setters to fail when the user state does not exist yet, so
// an accidental create-on-write cannot manufacture a state Telegram never
// reported. The store enforces that; this method only forwards the refusal.
func (s *StateStore) SetPts(ctx context.Context, userID int64, pts int) error {
	return s.state.SetAutomationUpdatePts(ctx, s.clientKey, userID, pts)
}

// SetQts advances the qts counter. See SetPts.
func (s *StateStore) SetQts(ctx context.Context, userID int64, qts int) error {
	return s.state.SetAutomationUpdateQts(ctx, s.clientKey, userID, qts)
}

// SetDate advances the date. See SetPts.
func (s *StateStore) SetDate(ctx context.Context, userID int64, date int) error {
	return s.state.SetAutomationUpdateDate(ctx, s.clientKey, userID, date)
}

// SetSeq advances the seq counter. See SetPts.
func (s *StateStore) SetSeq(ctx context.Context, userID int64, seq int) error {
	return s.state.SetAutomationUpdateSeq(ctx, s.clientKey, userID, seq)
}

// SetDateSeq advances date and seq together. See SetPts.
func (s *StateStore) SetDateSeq(ctx context.Context, userID int64, date, seq int) error {
	return s.state.SetAutomationUpdateDateSeq(ctx, s.clientKey, userID, date, seq)
}

// GetChannelPts returns one channel's stored pts.
func (s *StateStore) GetChannelPts(ctx context.Context, userID, channelID int64) (int, bool, error) {
	return s.state.AutomationChannelPts(ctx, s.clientKey, userID, channelID)
}

// SetChannelPts stores one channel's pts.
func (s *StateStore) SetChannelPts(ctx context.Context, userID, channelID int64, pts int) error {
	return s.state.SetAutomationChannelPts(ctx, s.clientKey, userID, channelID, pts)
}

// ForEachChannels walks every channel with a stored pts.
func (s *StateStore) ForEachChannels(ctx context.Context, userID int64,
	f func(ctx context.Context, channelID int64, pts int) error) error {
	return s.state.ForEachAutomationChannelPts(ctx, s.clientKey, userID, f)
}

// --- updates.ChannelAccessHasher ---

// SetChannelAccessHash records a channel's access hash.
func (s *StateStore) SetChannelAccessHash(ctx context.Context, userID, channelID, accessHash int64) error {
	return s.state.SetAutomationPeerHash(ctx, s.clientKey, userID, PeerChannel, channelID, accessHash)
}

// GetChannelAccessHash returns a channel's access hash.
func (s *StateStore) GetChannelAccessHash(ctx context.Context, userID, channelID int64) (int64, bool, error) {
	return s.state.AutomationPeerHash(ctx, s.clientKey, userID, PeerChannel, channelID)
}

// --- updates.UserAccessHasher ---

// SetUserAccessHash records a user's access hash.
func (s *StateStore) SetUserAccessHash(ctx context.Context, userID, targetUserID, accessHash int64) error {
	return s.state.SetAutomationPeerHash(ctx, s.clientKey, userID, PeerUser, targetUserID, accessHash)
}

// GetUserAccessHash returns a user's access hash.
func (s *StateStore) GetUserAccessHash(ctx context.Context, userID, targetUserID int64) (int64, bool, error) {
	return s.state.AutomationPeerHash(ctx, s.clientKey, userID, PeerUser, targetUserID)
}

// Compile-time proof that the adapter really is what gotd asks for. If a gotd
// upgrade changes one of these interfaces this line fails at build time rather
// than at the first reconnect in production.
var (
	_ updates.StateStorage        = (*StateStore)(nil)
	_ updates.ChannelAccessHasher = (*StateStore)(nil)
	_ updates.UserAccessHasher    = (*StateStore)(nil)
)

// Checkpoint is one channel's high-water mark: the newest source message that
// has already been turned into an event. It is what the bounded fallback
// advances, and it is deliberately separate from the update counters above:
// those say where Telegram's difference API got to, this says which messages
// automation has actually consumed.
type Checkpoint struct {
	ClientKey     string
	ChannelID     int64
	LastMessageID int64
}

// CheckpointStore reads and writes high-water marks.
type CheckpointStore interface {
	AutomationCheckpoint(ctx context.Context, clientKey string, channelID int64) (int64, bool, error)
	SetAutomationCheckpoint(ctx context.Context, clientKey string, channelID, lastMessageID int64) error
}

// Backfill pulls one bounded batch of messages a gap left behind and hands each
// to accept, oldest first.
//
// read must return messages after afterID, at most limit of them. The bound is
// not cosmetic: a channel can hold years of history and a reconnect must not be
// allowed to turn into a full download of it. One call moves at most one batch;
// the caller schedules the next one, so a recovered backlog drains gradually
// instead of in one burst.
//
// It returns how many messages it delivered and the new high-water mark. A
// caller that hits the bound should call again later; a caller that does not has
// caught up.
type Backfill struct {
	Store CheckpointStore
	// Limit is the batch size. Non-positive means one batch of 20.
	Limit int
}

// Run delivers one batch. accept is called oldest first, and the checkpoint
// only advances past messages accept has accepted: a message that could not be
// stored is retried on the next pass rather than skipped.
func (b Backfill) Run(ctx context.Context, clientKey string, channelID int64,
	read func(ctx context.Context, afterID int64, limit int) ([]int64, error),
	accept func(ctx context.Context, messageID int64) error,
) (delivered int, checkpoint int64, err error) {

	limit := b.Limit
	if limit <= 0 {
		limit = 20
	}
	after, _, err := b.Store.AutomationCheckpoint(ctx, clientKey, channelID)
	if err != nil {
		return 0, after, fmt.Errorf("recovery: read checkpoint for channel %d: %w", channelID, err)
	}

	ids, err := read(ctx, after, limit)
	if err != nil {
		return 0, after, fmt.Errorf("recovery: read history for channel %d: %w", channelID, err)
	}
	if len(ids) == 0 {
		return 0, after, nil
	}

	high := after
	for _, id := range ids {
		if err := accept(ctx, id); err != nil {
			// Stop at the first refusal. The checkpoint stays below the message
			// that failed, so it is retried rather than lost.
			return delivered, high, fmt.Errorf("recovery: accept message %d of channel %d: %w", id, channelID, err)
		}
		delivered++
		if id > high {
			high = id
		}
	}
	if err := b.Store.SetAutomationCheckpoint(ctx, clientKey, channelID, high); err != nil {
		return delivered, after, fmt.Errorf("recovery: write checkpoint for channel %d: %w", channelID, err)
	}
	return delivered, high, nil
}
