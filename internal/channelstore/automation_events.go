package channelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/rpc"
)

// Persistence for normalized incoming Telegram messages.
//
// Invariant: package automation holds only shared contracts and must never
// import this package. The store may depend on it, never the other way round,
// so the types every workstream implements against stay the single source of
// truth instead of being mirrored here.

// ErrInvalidAutomationEvent means the event itself is not storable: an empty
// identity, a missing channel reference, or a message id Telegram never issues.
// It is one of automation.ErrInvalid's own cases, so a service can map it onto
// the narrow HTTP code without a second vocabulary.
var ErrInvalidAutomationEvent = fmt.Errorf("channelstore: invalid automation event: %w", automation.ErrInvalid)

// InsertMessageEvent stores one normalized event and reports whether it was
// new.
//
// inserted is false when the same logical message was already recorded. That
// is a normal outcome, not an error: duplicate update delivery is expected, and
// the same message seen through a second account on the same network is one
// event. The observing client_key is still recorded against the existing row,
// so at least one connection authorized to read the media stays known even if
// the connection that created the row later loses access.
//
// The identity is (type, network_key, telegram_channel_id, message_id). It
// deliberately excludes source_client_key: two accounts on one network describe
// one logical message and must not run an automation twice.
func (s *Store) InsertMessageEvent(ctx context.Context, event automation.MessageEvent) (bool, error) {
	if err := validateAutomationEvent(event); err != nil {
		return false, err
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return false, fmt.Errorf("channelstore: encode automation event payload: %w", err)
	}

	now := s.automationNow()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("channelstore: write automation event %s: %w", event.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
INSERT INTO automation_events (
	id, type, network_key, source_client_key, managed_channel_id,
	telegram_channel_id, message_id, grouped_id, occurred_at, received_at,
	payload, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (type, network_key, telegram_channel_id, message_id) DO NOTHING`,
		event.ID, string(event.Type), event.NetworkKey, event.SourceClientKey, event.ManagedChannelID,
		event.TelegramChannelID, event.MessageID, nullableInt64(event.GroupedID),
		formatAutomationTime(event.OccurredAt), formatAutomationTime(event.ReceivedAt), string(payload), formatAutomationTime(now),
	)
	if err != nil {
		return false, fmt.Errorf("channelstore: write automation event %s: %w", event.ID, err)
	}
	written, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("channelstore: write automation event %s: %w", event.ID, err)
	}

	id := event.ID
	inserted := written == 1
	if !inserted {
		// A replay: the row that already holds this identity keeps its id, which
		// is derived from the same identity and so must be the same string. Read
		// it rather than assume it, and refuse the rare disagreement instead of
		// filing an observer under an id that is not the event's.
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM automation_events
			 WHERE type = ? AND network_key = ? AND telegram_channel_id = ? AND message_id = ?`,
			string(event.Type), event.NetworkKey, event.TelegramChannelID, event.MessageID,
		).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("channelstore: write automation event %s: %w", event.ID, ErrInvalidAutomationEvent)
		}
		if err != nil {
			return false, fmt.Errorf("channelstore: read automation event %s: %w", event.ID, err)
		}
	}

	// Either way the observer is recorded. This is the row that keeps an
	// authorized media reader discoverable after a duplicate observation.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO automation_event_sources (event_id, client_key, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (event_id, client_key) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
		id, event.SourceClientKey, formatAutomationTime(now), formatAutomationTime(now),
	); err != nil {
		return false, fmt.Errorf("channelstore: record automation event source %s: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("channelstore: write automation event %s: %w", event.ID, err)
	}
	return inserted, nil
}

func validateAutomationEvent(event automation.MessageEvent) error {
	switch {
	case strings.TrimSpace(event.ID) == "":
		return fmt.Errorf("%w: event id is empty", ErrInvalidAutomationEvent)
	case strings.TrimSpace(string(event.Type)) == "":
		return fmt.Errorf("%w: event type is empty", ErrInvalidAutomationEvent)
	case strings.TrimSpace(event.NetworkKey) == "":
		return fmt.Errorf("%w: network_key is empty", ErrInvalidAutomationEvent)
	case strings.TrimSpace(event.SourceClientKey) == "":
		return fmt.Errorf("%w: source_client_key is empty", ErrInvalidAutomationEvent)
	case event.ManagedChannelID <= 0:
		return fmt.Errorf("%w: managed_channel_id must be positive", ErrInvalidAutomationEvent)
	case event.TelegramChannelID == 0:
		return fmt.Errorf("%w: telegram_channel_id must not be zero", ErrInvalidAutomationEvent)
	case event.MessageID <= 0:
		return fmt.Errorf("%w: message_id must be positive", ErrInvalidAutomationEvent)
	}
	return nil
}

// AutomationEvent returns one stored event by its id.
func (s *Store) AutomationEvent(ctx context.Context, id string) (automation.MessageEvent, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, type, network_key, source_client_key, managed_channel_id,
	telegram_channel_id, message_id, grouped_id, occurred_at, received_at, payload
FROM automation_events WHERE id = ?`, id)
	out, err := scanAutomationEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.MessageEvent{}, false, nil
	}
	if err != nil {
		return automation.MessageEvent{}, false, fmt.Errorf("channelstore: read automation event %s: %w", id, err)
	}
	return out, true, nil
}

func scanAutomationEvent(row scanner) (automation.MessageEvent, error) {
	var (
		out        automation.MessageEvent
		typ        string
		grouped    sql.NullInt64
		occurredAt string
		receivedAt string
		payload    string
	)
	if err := row.Scan(&out.ID, &typ, &out.NetworkKey, &out.SourceClientKey, &out.ManagedChannelID,
		&out.TelegramChannelID, &out.MessageID, &grouped, &occurredAt, &receivedAt, &payload); err != nil {
		return automation.MessageEvent{}, err
	}
	out.Type = automation.EventType(typ)
	if grouped.Valid {
		v := grouped.Int64
		out.GroupedID = &v
	}
	out.OccurredAt = parseTime(occurredAt)
	out.ReceivedAt = parseTime(receivedAt)
	if strings.TrimSpace(payload) != "" {
		if err := json.Unmarshal([]byte(payload), &out.Payload); err != nil {
			return automation.MessageEvent{}, fmt.Errorf("decode payload: %w", err)
		}
	}
	// A message read back must never look like a message with no reactions and
	// no formatting: the empty slice is what the API always reports, and a nil
	// here would serialize as null and break that promise.
	if out.Payload.Message.Reactions == nil {
		out.Payload.Message.Reactions = []rpc.Reaction{}
	}
	if out.Payload.Message.Entities == nil {
		out.Payload.Message.Entities = []rpc.Entity{}
	}
	return out, nil
}

// AutomationEventSources returns the client keys that have observed an event,
// oldest observation first.
func (s *Store) AutomationEventSources(ctx context.Context, eventID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT client_key FROM automation_event_sources WHERE event_id = ?
		 ORDER BY first_seen_at, client_key`, eventID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list automation event sources: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("channelstore: list automation event sources: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list automation event sources: %w", err)
	}
	return out, nil
}

// CountAutomationEvents returns how many events are stored. It exists for
// diagnosis and tests: ingestion itself never counts.
func (s *Store) CountAutomationEvents(ctx context.Context) (int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM automation_events`).Scan(&total); err != nil {
		return 0, fmt.Errorf("channelstore: count automation events: %w", err)
	}
	return total, nil
}

func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
