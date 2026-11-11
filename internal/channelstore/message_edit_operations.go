package channelstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MessageEditStatus is the durable state of one accepted full-message edit.
type MessageEditStatus string

const (
	MessageEditQueued     MessageEditStatus = "queued"
	MessageEditProcessing MessageEditStatus = "processing"
	MessageEditRetrying   MessageEditStatus = "retrying"
	MessageEditEdited     MessageEditStatus = "edited"
	MessageEditFailed     MessageEditStatus = "failed"
	MessageEditUnknown    MessageEditStatus = "unknown"
)

func (s MessageEditStatus) terminal() bool {
	switch s {
	case MessageEditEdited, MessageEditFailed, MessageEditUnknown:
		return true
	}
	return false
}

// MessageEditOperation is a complete durable description of one requested
// replacement. User text and staging names stay private to the background
// worker; API status reads expose only the operation's safe public fields.
type MessageEditOperation struct {
	ID                   int64
	PublicID             string
	UserID               int64
	ClientKey            string
	DestinationChannelID string
	TargetMessageID      int64
	RequestPayload       string
	MediaKind            string
	MediaStagingName     string
	MediaFilename        string
	MediaMimeType        string
	MediaSize            int64
	MediaSHA256          string
	IdempotencyKey       string
	RequestFingerprint   string

	Status           MessageEditStatus
	Stage            string
	AttemptCount     int
	NextAttemptAt    *time.Time
	RemoteMessageID  int64
	ResultMediaKind  string
	LastErrorCode    string
	LastErrorMessage string

	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	UpdatedAt   time.Time
}

var (
	ErrInvalidMessageEditOperation    = errors.New("channelstore: invalid message edit operation")
	ErrMessageEditOperationNotFound   = errors.New("channelstore: message edit operation not found")
	ErrMessageEditIdempotencyConflict = errors.New("channelstore: message edit idempotency key already used for another request")
)

const messageEditOperationColumns = `id, public_id, user_id, client_key, destination_channel_id,
	target_message_id, request_payload, media_kind, media_staging_name, media_filename,
	media_mime_type, media_size, media_sha256, idempotency_key, request_fingerprint,
	status, stage, attempt_count, next_attempt_at, remote_message_id, result_media_kind,
	last_error_code, last_error_message, created_at, started_at, completed_at, updated_at`

// BeginMessageEditOperation records a new replacement or returns the existing
// operation for an identical principal/key pair. It never queues work itself;
// callers enqueue only when created is true.
func (s *Store) BeginMessageEditOperation(ctx context.Context, op MessageEditOperation) (MessageEditOperation, bool, error) {
	if op.UserID <= 0 || strings.TrimSpace(op.ClientKey) == "" || strings.TrimSpace(op.DestinationChannelID) == "" || op.TargetMessageID <= 0 || strings.TrimSpace(op.RequestFingerprint) == "" {
		return MessageEditOperation{}, false, fmt.Errorf("%w: principal, target and fingerprint are required", ErrInvalidMessageEditOperation)
	}
	if key := strings.TrimSpace(op.IdempotencyKey); key != "" {
		existing, found, err := s.MessageEditOperationFor(ctx, op.UserID, key)
		if err != nil {
			return MessageEditOperation{}, false, err
		}
		if found {
			if existing.RequestFingerprint != op.RequestFingerprint {
				return MessageEditOperation{}, false, ErrMessageEditIdempotencyConflict
			}
			return existing, false, nil
		}
	}
	if op.PublicID == "" {
		id, err := newMessageEditPublicID()
		if err != nil {
			return MessageEditOperation{}, false, err
		}
		op.PublicID = id
	}
	now := s.now()
	if op.Status == "" {
		op.Status = MessageEditQueued
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO message_edit_operations (
	public_id, user_id, client_key, destination_channel_id, target_message_id, request_payload,
	media_kind, media_staging_name, media_filename, media_mime_type, media_size, media_sha256,
	idempotency_key, request_fingerprint, status, stage, attempt_count, next_attempt_at,
	remote_message_id, result_media_kind, last_error_code, last_error_message, created_at,
	started_at, completed_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, 0, '', '', '', ?, NULL, NULL, ?)`,
		op.PublicID, op.UserID, strings.TrimSpace(op.ClientKey), strings.TrimSpace(op.DestinationChannelID), op.TargetMessageID,
		op.RequestPayload, op.MediaKind, op.MediaStagingName, op.MediaFilename, op.MediaMimeType, op.MediaSize, op.MediaSHA256,
		strings.TrimSpace(op.IdempotencyKey), op.RequestFingerprint, string(op.Status), op.Stage, formatTime(now), formatTime(now))
	if err != nil {
		if strings.TrimSpace(op.IdempotencyKey) != "" {
			existing, found, readErr := s.MessageEditOperationFor(ctx, op.UserID, op.IdempotencyKey)
			if readErr == nil && found {
				if existing.RequestFingerprint == op.RequestFingerprint {
					return existing, false, nil
				}
				return MessageEditOperation{}, false, ErrMessageEditIdempotencyConflict
			}
		}
		return MessageEditOperation{}, false, fmt.Errorf("channelstore: begin message edit operation: %w", err)
	}
	op.ID, err = res.LastInsertId()
	if err != nil {
		return MessageEditOperation{}, false, fmt.Errorf("channelstore: message edit operation id: %w", err)
	}
	return op, true, nil
}

func (s *Store) MessageEditOperation(ctx context.Context, publicID string) (MessageEditOperation, bool, error) {
	return s.messageEditOperation(ctx, `SELECT `+messageEditOperationColumns+` FROM message_edit_operations WHERE public_id = ?`, publicID)
}

func (s *Store) MessageEditOperationFor(ctx context.Context, userID int64, key string) (MessageEditOperation, bool, error) {
	return s.messageEditOperation(ctx, `SELECT `+messageEditOperationColumns+` FROM message_edit_operations WHERE user_id = ? AND idempotency_key = ?`, userID, strings.TrimSpace(key))
}

func (s *Store) MessageEditOperationForTarget(ctx context.Context, userID int64, clientKey, channelID string, messageID int64, publicID string) (MessageEditOperation, bool, error) {
	return s.messageEditOperation(ctx, `SELECT `+messageEditOperationColumns+` FROM message_edit_operations
	WHERE user_id = ? AND client_key = ? AND destination_channel_id = ? AND target_message_id = ? AND public_id = ?`, userID, clientKey, channelID, messageID, publicID)
}

func (s *Store) messageEditOperation(ctx context.Context, query string, args ...any) (MessageEditOperation, bool, error) {
	op, err := scanMessageEditOperation(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return MessageEditOperation{}, false, nil
	}
	if err != nil {
		return MessageEditOperation{}, false, fmt.Errorf("channelstore: read message edit operation: %w", err)
	}
	return op, true, nil
}

func (s *Store) StartMessageEditOperation(ctx context.Context, id int64, attempt int, stage string) error {
	if attempt <= 0 {
		return fmt.Errorf("%w: attempt must be positive", ErrInvalidMessageEditOperation)
	}
	if stage == "" {
		stage = "editing_message"
	}
	now := s.now()
	res, err := s.db.ExecContext(ctx, `UPDATE message_edit_operations SET status = ?, stage = ?, attempt_count = ?,
	next_attempt_at = NULL, started_at = COALESCE(started_at, ?), updated_at = ? WHERE id = ? AND status IN (?, ?, ?)`,
		string(MessageEditProcessing), stage, attempt, formatTime(now), formatTime(now), id,
		string(MessageEditQueued), string(MessageEditRetrying), string(MessageEditProcessing))
	if err != nil {
		return fmt.Errorf("channelstore: start message edit operation: %w", err)
	}
	return requireMessageEditRow(res, id)
}

func (s *Store) RetryMessageEditOperation(ctx context.Context, id int64, attempt int, next time.Time, stage, code, message string) error {
	now := s.now()
	res, err := s.db.ExecContext(ctx, `UPDATE message_edit_operations SET status = ?, stage = ?, attempt_count = ?,
	next_attempt_at = ?, last_error_code = ?, last_error_message = ?, updated_at = ? WHERE id = ? AND status IN (?, ?)`,
		string(MessageEditRetrying), stage, attempt, formatTime(next), code, message, formatTime(now), id,
		string(MessageEditQueued), string(MessageEditProcessing))
	if err != nil {
		return fmt.Errorf("channelstore: retry message edit operation: %w", err)
	}
	return requireMessageEditRow(res, id)
}

// FinishMessageEditOperation records a terminal outcome. A success is only
// accepted when the remote response confirms the original message ID.
func (s *Store) FinishMessageEditOperation(ctx context.Context, id int64, status MessageEditStatus, remoteMessageID int64, mediaKind, code, message string) error {
	if !status.terminal() || (status == MessageEditEdited && remoteMessageID <= 0) || (status != MessageEditEdited && remoteMessageID != 0) {
		return fmt.Errorf("%w: invalid terminal result", ErrInvalidMessageEditOperation)
	}
	var targetID int64
	if err := s.db.QueryRowContext(ctx, `SELECT target_message_id FROM message_edit_operations WHERE id = ?`, id).Scan(&targetID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: operation %d", ErrMessageEditOperationNotFound, id)
		}
		return fmt.Errorf("channelstore: read target for message edit: %w", err)
	}
	if status == MessageEditEdited && remoteMessageID != targetID {
		return fmt.Errorf("%w: edit returned message id %d for target %d", ErrInvalidMessageEditOperation, remoteMessageID, targetID)
	}
	now := s.now()
	res, err := s.db.ExecContext(ctx, `UPDATE message_edit_operations SET status = ?, stage = '', next_attempt_at = NULL,
	remote_message_id = ?, result_media_kind = ?, last_error_code = ?, last_error_message = ?,
	completed_at = ?, updated_at = ? WHERE id = ? AND status IN (?, ?, ?)`,
		string(status), remoteMessageID, mediaKind, code, message, formatTime(now), formatTime(now), id,
		string(MessageEditQueued), string(MessageEditProcessing), string(MessageEditRetrying))
	if err != nil {
		return fmt.Errorf("channelstore: finish message edit operation: %w", err)
	}
	return requireMessageEditRow(res, id)
}

func (s *Store) MessageEditOperationsHoldingMedia(ctx context.Context) ([]MessageEditOperation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageEditOperationColumns+` FROM message_edit_operations
	WHERE media_staging_name <> '' AND status IN (?, ?, ?) ORDER BY created_at`,
		string(MessageEditQueued), string(MessageEditProcessing), string(MessageEditRetrying))
	if err != nil {
		return nil, fmt.Errorf("channelstore: list message edits holding media: %w", err)
	}
	defer rows.Close()
	var out []MessageEditOperation
	for rows.Next() {
		op, err := scanMessageEditOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: scan message edit operation: %w", err)
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: read message edits holding media: %w", err)
	}
	return out, nil
}

func (s *Store) ExpiredMessageEditOperations(ctx context.Context, before time.Time) ([]MessageEditOperation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageEditOperationColumns+` FROM message_edit_operations
	WHERE status IN (?, ?, ?) AND completed_at IS NOT NULL AND completed_at <= ? ORDER BY completed_at`,
		string(MessageEditEdited), string(MessageEditFailed), string(MessageEditUnknown), formatTime(before))
	if err != nil {
		return nil, fmt.Errorf("channelstore: list expired message edits: %w", err)
	}
	defer rows.Close()
	var out []MessageEditOperation
	for rows.Next() {
		op, err := scanMessageEditOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: scan expired message edit: %w", err)
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: read expired message edits: %w", err)
	}
	return out, nil
}

func (s *Store) DeleteMessageEditOperation(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM message_edit_operations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("channelstore: delete message edit operation: %w", err)
	}
	return requireMessageEditRow(res, id)
}

func requireMessageEditRow(res sql.Result, id int64) error {
	written, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("channelstore: message edit rows affected: %w", err)
	}
	if written == 0 {
		return fmt.Errorf("%w: operation %d", ErrMessageEditOperationNotFound, id)
	}
	return nil
}

type messageEditScanner interface{ Scan(...any) error }

func scanMessageEditOperation(row messageEditScanner) (MessageEditOperation, error) {
	var (
		op                                    MessageEditOperation
		status, createdAt, updatedAt          string
		nextAttemptAt, startedAt, completedAt sql.NullString
	)
	err := row.Scan(&op.ID, &op.PublicID, &op.UserID, &op.ClientKey, &op.DestinationChannelID,
		&op.TargetMessageID, &op.RequestPayload, &op.MediaKind, &op.MediaStagingName, &op.MediaFilename,
		&op.MediaMimeType, &op.MediaSize, &op.MediaSHA256, &op.IdempotencyKey, &op.RequestFingerprint,
		&status, &op.Stage, &op.AttemptCount, &nextAttemptAt, &op.RemoteMessageID, &op.ResultMediaKind,
		&op.LastErrorCode, &op.LastErrorMessage, &createdAt, &startedAt, &completedAt, &updatedAt)
	if err != nil {
		return MessageEditOperation{}, err
	}
	op.Status = MessageEditStatus(status)
	op.NextAttemptAt = parseOptionalTime(nextAttemptAt)
	op.StartedAt = parseOptionalTime(startedAt)
	op.CompletedAt = parseOptionalTime(completedAt)
	op.CreatedAt = parseTime(createdAt)
	op.UpdatedAt = parseTime(updatedAt)
	return op, nil
}

func newMessageEditPublicID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("channelstore: message edit operation id: %w", err)
	}
	return "edit_" + hex.EncodeToString(raw[:]), nil
}
