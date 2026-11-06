package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
)

// Durable state for update recovery and for loop prevention.
//
// Both are the same kind of thing: facts this installation must not lose across
// a restart, that no route ever returns and no user ever edits.
//
// Invariant: package automation holds only shared contracts and must never
// import this package.

// ErrInvalidAutomationState means the key itself is not usable: an empty client
// key, a non-positive user, or a peer kind this schema does not record.
var ErrInvalidAutomationState = fmt.Errorf("channelstore: invalid automation state: %w", automation.ErrInvalid)

// ErrAutomationStateNotFound means the per-user update state does not exist.
//
// gotd requires the incremental setters to fail in exactly this case: a partial
// update for an account whose baseline was never stored has nothing to be
// applied to, and silently creating a baseline from it would invent a state
// Telegram never reported.
var ErrAutomationStateNotFound = fmt.Errorf("channelstore: automation update state not found: %w", automation.ErrNotFound)

// Peer kinds recorded in automation_peer_hashes.
const (
	AutomationPeerChannel = "channel"
	AutomationPeerUser    = "user"
)

// automationNow is the store's clock in UTC, which is what every timestamp in
// these tables holds.
func (s *Store) automationNow() time.Time { return s.now().UTC() }

// formatAutomationTime renders an instant for the automation tables.
//
// It is RFC3339 in UTC with a fixed nine-digit fraction, and therefore still
// parses with parseTime. The width is the point: these columns are compared and
// ordered inside SQL, and Go's RFC3339Nano drops trailing zeros, so a plain
// RFC3339Nano value can sort before an instant that is actually earlier
// ("…T12:00:00Z" sorts after "…T12:00:00.5Z"). Fixed width keeps the textual
// order and the chronological order the same.
func formatAutomationTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

// AutomationState is one account's update state on one client connection. The
// fields are Telegram's own counters; they are stored as opaque integers and
// only ever compared with what Telegram reports next.
type AutomationState struct {
	Pts  int
	Qts  int
	Date int
	Seq  int
}

// AutomationUpdateState returns the stored state for one account on one client.
func (s *Store) AutomationUpdateState(ctx context.Context, clientKey string, userID int64) (AutomationState, bool, error) {
	if err := validateAutomationStateKey(clientKey, userID); err != nil {
		return AutomationState{}, false, err
	}
	var out AutomationState
	err := s.db.QueryRowContext(ctx,
		`SELECT pts, qts, date, seq FROM automation_update_states
		 WHERE client_key = ? AND user_id = ?`, clientKey, userID).Scan(&out.Pts, &out.Qts, &out.Date, &out.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationState{}, false, nil
	}
	if err != nil {
		return AutomationState{}, false, fmt.Errorf("channelstore: read automation update state: %w", err)
	}
	return out, true, nil
}

// SetAutomationUpdateState stores the whole state, creating the row if needed.
// It is what the first synchronization writes.
func (s *Store) SetAutomationUpdateState(ctx context.Context, clientKey string, userID int64, state AutomationState) error {
	if err := validateAutomationStateKey(clientKey, userID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO automation_update_states (client_key, user_id, pts, qts, date, seq, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (client_key, user_id) DO UPDATE SET
	pts = excluded.pts, qts = excluded.qts, date = excluded.date, seq = excluded.seq,
	updated_at = excluded.updated_at`,
		clientKey, userID, state.Pts, state.Qts, state.Date, state.Seq, formatAutomationTime(s.automationNow()))
	if err != nil {
		return fmt.Errorf("channelstore: write automation update state: %w", err)
	}
	return nil
}

// SetAutomationUpdatePts, SetAutomationUpdateQts, SetAutomationUpdateDate,
// SetAutomationUpdateSeq and SetAutomationUpdateDateSeq apply one counter to an
// existing state. Each reports ErrAutomationStateNotFound when there is no row
// to apply it to.
func (s *Store) SetAutomationUpdatePts(ctx context.Context, clientKey string, userID int64, pts int) error {
	return s.setAutomationUpdateCounter(ctx, clientKey, userID, "pts", pts)
}

func (s *Store) SetAutomationUpdateQts(ctx context.Context, clientKey string, userID int64, qts int) error {
	return s.setAutomationUpdateCounter(ctx, clientKey, userID, "qts", qts)
}

func (s *Store) SetAutomationUpdateDate(ctx context.Context, clientKey string, userID int64, date int) error {
	return s.setAutomationUpdateCounter(ctx, clientKey, userID, "date", date)
}

func (s *Store) SetAutomationUpdateSeq(ctx context.Context, clientKey string, userID int64, seq int) error {
	return s.setAutomationUpdateCounter(ctx, clientKey, userID, "seq", seq)
}

func (s *Store) SetAutomationUpdateDateSeq(ctx context.Context, clientKey string, userID int64, date, seq int) error {
	if err := validateAutomationStateKey(clientKey, userID); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE automation_update_states SET date = ?, seq = ?, updated_at = ?
WHERE client_key = ? AND user_id = ?`,
		date, seq, formatAutomationTime(s.automationNow()), clientKey, userID)
	if err != nil {
		return fmt.Errorf("channelstore: write automation update state: %w", err)
	}
	return requireAutomationStateRow(res, clientKey, userID)
}

// setAutomationUpdateCounter applies one named counter. The column is a literal
// from this file, never caller input.
func (s *Store) setAutomationUpdateCounter(ctx context.Context, clientKey string, userID int64, column string, value int) error {
	if err := validateAutomationStateKey(clientKey, userID); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE automation_update_states SET `+column+` = ?, updated_at = ?
WHERE client_key = ? AND user_id = ?`,
		value, formatAutomationTime(s.automationNow()), clientKey, userID)
	if err != nil {
		return fmt.Errorf("channelstore: write automation update state: %w", err)
	}
	return requireAutomationStateRow(res, clientKey, userID)
}

func requireAutomationStateRow(res sql.Result, clientKey string, userID int64) error {
	written, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("channelstore: write automation update state: %w", err)
	}
	if written == 0 {
		return fmt.Errorf("%w: %s/%d", ErrAutomationStateNotFound, clientKey, userID)
	}
	return nil
}

func validateAutomationStateKey(clientKey string, userID int64) error {
	if strings.TrimSpace(clientKey) == "" {
		return fmt.Errorf("%w: client_key is empty", ErrInvalidAutomationState)
	}
	if userID <= 0 {
		return fmt.Errorf("%w: user id must be positive", ErrInvalidAutomationState)
	}
	return nil
}

// AutomationChannelPts returns one channel's saved pts for an account.
func (s *Store) AutomationChannelPts(ctx context.Context, clientKey string, userID, channelID int64) (int, bool, error) {
	if err := validateAutomationStateKey(clientKey, userID); err != nil {
		return 0, false, err
	}
	var pts int
	err := s.db.QueryRowContext(ctx,
		`SELECT pts FROM automation_channel_pts
		 WHERE client_key = ? AND user_id = ? AND channel_id = ?`, clientKey, userID, channelID).Scan(&pts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("channelstore: read automation channel pts: %w", err)
	}
	return pts, true, nil
}

// SetAutomationChannelPts stores one channel's pts for an account.
func (s *Store) SetAutomationChannelPts(ctx context.Context, clientKey string, userID, channelID int64, pts int) error {
	if err := validateAutomationStateKey(clientKey, userID); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("%w: channel id must not be zero", ErrInvalidAutomationState)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO automation_channel_pts (client_key, user_id, channel_id, pts, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (client_key, user_id, channel_id) DO UPDATE SET
	pts = excluded.pts, updated_at = excluded.updated_at`,
		clientKey, userID, channelID, pts, formatAutomationTime(s.automationNow()))
	if err != nil {
		return fmt.Errorf("channelstore: write automation channel pts: %w", err)
	}
	return nil
}

// ForEachAutomationChannelPts walks every channel pts saved for one account.
// A channel with no saved pts is not reported at all.
func (s *Store) ForEachAutomationChannelPts(ctx context.Context, clientKey string, userID int64,
	f func(ctx context.Context, channelID int64, pts int) error) error {
	if err := validateAutomationStateKey(clientKey, userID); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, pts FROM automation_channel_pts
		 WHERE client_key = ? AND user_id = ? ORDER BY channel_id`, clientKey, userID)
	if err != nil {
		return fmt.Errorf("channelstore: walk automation channel pts: %w", err)
	}
	defer rows.Close()

	// Collect first: the callback may want to write, and this database allows
	// one connection at a time, so reading while the callback runs would
	// deadlock against itself.
	type entry struct {
		channelID int64
		pts       int
	}
	var all []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.channelID, &e.pts); err != nil {
			return fmt.Errorf("channelstore: walk automation channel pts: %w", err)
		}
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("channelstore: walk automation channel pts: %w", err)
	}
	for _, e := range all {
		if err := f(ctx, e.channelID, e.pts); err != nil {
			return err
		}
	}
	return nil
}

// AutomationPeerHash returns the saved access hash for one peer.
func (s *Store) AutomationPeerHash(ctx context.Context, clientKey string, userID int64, kind string, peerID int64) (int64, bool, error) {
	if err := validateAutomationPeerKey(clientKey, userID, kind); err != nil {
		return 0, false, err
	}
	var hash int64
	err := s.db.QueryRowContext(ctx, `
SELECT access_hash FROM automation_peer_hashes
WHERE client_key = ? AND user_id = ? AND peer_kind = ? AND peer_id = ?`,
		clientKey, userID, kind, peerID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("channelstore: read automation peer hash: %w", err)
	}
	return hash, true, nil
}

// SetAutomationPeerHash remembers one access hash observed by the update
// manager, so a later update naming that peer can be resolved without asking
// Telegram again.
func (s *Store) SetAutomationPeerHash(ctx context.Context, clientKey string, userID int64, kind string, peerID, accessHash int64) error {
	if err := validateAutomationPeerKey(clientKey, userID, kind); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO automation_peer_hashes (client_key, user_id, peer_kind, peer_id, access_hash, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (client_key, user_id, peer_kind, peer_id) DO UPDATE SET
	access_hash = excluded.access_hash, updated_at = excluded.updated_at`,
		clientKey, userID, kind, peerID, accessHash, formatAutomationTime(s.automationNow()))
	if err != nil {
		return fmt.Errorf("channelstore: write automation peer hash: %w", err)
	}
	return nil
}

func validateAutomationPeerKey(clientKey string, userID int64, kind string) error {
	if err := validateAutomationStateKey(clientKey, userID); err != nil {
		return err
	}
	if kind != AutomationPeerChannel && kind != AutomationPeerUser {
		return fmt.Errorf("%w: unsupported peer kind %q", ErrInvalidAutomationState, kind)
	}
	return nil
}

// AutomationCheckpoint returns the highest message id already ingested for one
// source channel, and whether any was ever recorded.
//
// It is the starting point of the bounded history fallback: recovery reads
// forward from here and never below it, which is what keeps a reconnect from
// turning into an archive backfill.
func (s *Store) AutomationCheckpoint(ctx context.Context, clientKey string, channelID int64) (int64, bool, error) {
	if strings.TrimSpace(clientKey) == "" {
		return 0, false, fmt.Errorf("%w: client_key is empty", ErrInvalidAutomationState)
	}
	var last int64
	err := s.db.QueryRowContext(ctx,
		`SELECT last_message_id FROM automation_checkpoints
		 WHERE client_key = ? AND channel_id = ?`, clientKey, channelID).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("channelstore: read automation checkpoint: %w", err)
	}
	return last, true, nil
}

// SetAutomationCheckpoint advances the high-water mark for one source channel.
// The mark only ever moves forward: a late or replayed page cannot walk it back
// and cause the same messages to be ingested twice.
func (s *Store) SetAutomationCheckpoint(ctx context.Context, clientKey string, channelID, lastMessageID int64) error {
	if strings.TrimSpace(clientKey) == "" {
		return fmt.Errorf("%w: client_key is empty", ErrInvalidAutomationState)
	}
	if channelID == 0 {
		return fmt.Errorf("%w: channel id must not be zero", ErrInvalidAutomationState)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO automation_checkpoints (client_key, channel_id, last_message_id, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (client_key, channel_id) DO UPDATE SET
	last_message_id = MAX(automation_checkpoints.last_message_id, excluded.last_message_id),
	updated_at = excluded.updated_at`,
		clientKey, channelID, lastMessageID, formatAutomationTime(s.automationNow()))
	if err != nil {
		return fmt.Errorf("channelstore: write automation checkpoint: %w", err)
	}
	return nil
}

// IsOwnPublication reports whether this installation sent the message.
//
// Ingestion asks before creating an event, which is what stops a workflow from
// reacting to its own output. The pair is the destination's network and the
// destination's client key, since a workflow may publish across networks and
// the same channel and message ids on two networks are two different messages.
func (s *Store) IsOwnPublication(ctx context.Context, networkKey, clientKey string, channelID, messageID int64) (bool, error) {
	if strings.TrimSpace(networkKey) == "" || strings.TrimSpace(clientKey) == "" {
		return false, fmt.Errorf("%w: network_key and client_key are required", ErrInvalidAutomationState)
	}
	var one int
	err := s.db.QueryRowContext(ctx, `
SELECT 1 FROM automation_outbound_messages
WHERE network_key = ? AND client_key = ? AND channel_id = ? AND remote_message_id = ?`,
		networkKey, clientKey, channelID, messageID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("channelstore: read automation outbound message: %w", err)
	}
	return true, nil
}

// MarkPublished records one message this installation put into a Telegram
// channel. Re-recording the same message is a no-op.
func (s *Store) MarkPublished(ctx context.Context, m automation.OutboundMessage) error {
	if strings.TrimSpace(m.NetworkKey) == "" || strings.TrimSpace(m.ClientKey) == "" {
		return fmt.Errorf("%w: network_key and client_key are required", ErrInvalidAutomationState)
	}
	if m.ChannelID == 0 {
		return fmt.Errorf("%w: channel id must not be zero", ErrInvalidAutomationState)
	}
	if m.RemoteMessageID <= 0 {
		return fmt.Errorf("%w: remote message id must be positive", ErrInvalidAutomationState)
	}
	if m.Origin != "manual" && m.Origin != "workflow" {
		return fmt.Errorf("%w: unsupported origin %q", ErrInvalidAutomationState, m.Origin)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO automation_outbound_messages (
	network_key, client_key, channel_id, remote_message_id, origin, origin_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (network_key, client_key, channel_id, remote_message_id) DO NOTHING`,
		m.NetworkKey, m.ClientKey, m.ChannelID, m.RemoteMessageID, m.Origin, m.OriginID,
		formatAutomationTime(s.automationNow()))
	if err != nil {
		return fmt.Errorf("channelstore: record automation outbound message: %w", err)
	}
	return nil
}

// AutomationOutboundMessages lists the recorded publications, newest first.
// It exists for diagnosis and tests.
func (s *Store) AutomationOutboundMessages(ctx context.Context, limit int) ([]automation.OutboundMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT network_key, client_key, channel_id, remote_message_id, origin, origin_id
FROM automation_outbound_messages ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list automation outbound messages: %w", err)
	}
	defer rows.Close()
	out := []automation.OutboundMessage{}
	for rows.Next() {
		var m automation.OutboundMessage
		var origin string
		if err := rows.Scan(&m.NetworkKey, &m.ClientKey, &m.ChannelID, &m.RemoteMessageID, &origin, &m.OriginID); err != nil {
			return nil, fmt.Errorf("channelstore: list automation outbound messages: %w", err)
		}
		m.Origin = origin
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list automation outbound messages: %w", err)
	}
	return out, nil
}
