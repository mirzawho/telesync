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
// inserted is false when this account had already recorded the same logical
// message. That is a normal outcome, not an error: duplicate update delivery is
// expected. The observing client_key is still recorded against the existing
// row, so at least one connection authorized to read the media stays known even
// if the connection that created the row later loses access.
//
// The identity is (account_id, type, network_key, telegram_channel_id,
// message_id). It deliberately excludes source_client_key: a second connection
// of the same account on one network describes one logical message and must not
// run an automation twice. It deliberately includes the account, because two
// users may watch the same remote channel on purpose, and dropping one user's
// event because the other had already seen the message is dropping their
// automation silently. That is the v0.0.3 assumption this release removes.
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
	id, type, network_key, source_client_key, account_id, source_account_id,
	owner_user_id, managed_channel_id,
	telegram_channel_id, message_id, grouped_id, occurred_at, received_at,
	payload, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (account_id, type, network_key, telegram_channel_id, message_id) DO NOTHING`,
		event.ID, string(event.Type), event.NetworkKey, event.SourceClientKey,
		nullableText(event.AccountID), nullableText(event.SourceAccountID),
		nullableOwnerID(event.AccountOwnerUserID()), event.ManagedChannelID,
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
			 WHERE account_id IS ? AND type = ? AND network_key = ?
			   AND telegram_channel_id = ? AND message_id = ?`,
			nullableText(event.AccountID),
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
INSERT INTO automation_event_sources (event_id, client_key, account_id, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (event_id, client_key) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
		id, event.SourceClientKey, nullableText(event.SourceAccountID),
		formatAutomationTime(now), formatAutomationTime(now),
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
	case strings.TrimSpace(event.AccountID) == "":
		// An event names its owning account. A legacy observation with no
		// account is not an event ingestion produces any more, and accepting one
		// would create work nothing is allowed to run.
		return fmt.Errorf("%w: account_id is empty", ErrInvalidAutomationEvent)
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
SELECT e.id, e.type, e.network_key, e.source_client_key, e.managed_channel_id,
	e.telegram_channel_id, e.message_id, e.grouped_id, e.occurred_at, e.received_at,
	e.payload, COALESCE(e.account_id, ''), COALESCE(e.source_account_id, ''),
	COALESCE(a.owner_user_id, 0)
FROM automation_events e
LEFT JOIN telegram_accounts a ON a.id = e.account_id
WHERE e.id = ?`, id)
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
		accountID  string
		sourceAcc  string
		ownerID    int64
	)
	if err := row.Scan(&out.ID, &typ, &out.NetworkKey, &out.SourceClientKey, &out.ManagedChannelID,
		&out.TelegramChannelID, &out.MessageID, &grouped, &occurredAt, &receivedAt, &payload,
		&accountID, &sourceAcc, &ownerID); err != nil {
		return automation.MessageEvent{}, err
	}
	out.Type = automation.EventType(typ)
	out.AccountID = accountID
	out.SourceAccountID = sourceAcc
	out = out.WithOwner(ownerID)
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

// nullableOwnerID stores a missing owner as NULL, which is what says "this row
// predates accounts". A zero would look like a user id that exists.
func nullableOwnerID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
