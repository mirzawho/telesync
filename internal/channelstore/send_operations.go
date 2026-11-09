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

// SendStatus is where one accepted send stands.
//
// The states are the ones a caller is told about. A send is accepted long
// before it is delivered, and everything between those two moments is here
// rather than in anybody's memory, so a poll after a restart gives the same
// answer as a poll before one.
type SendStatus string

const (
	// SendQueued means the request was durably accepted and its job is waiting
	// for a worker. Nothing has been sent.
	SendQueued SendStatus = "queued"
	// SendProcessing means a worker holds the job and is doing the work now.
	SendProcessing SendStatus = "processing"
	// SendRetrying means an attempt did not complete and another is scheduled.
	SendRetrying SendStatus = "retrying"
	// SendSent means the destination confirmed the message.
	SendSent SendStatus = "sent"
	// SendFailed means the send will not be attempted again. It is the only
	// terminal state in which nothing was posted.
	SendFailed SendStatus = "failed"
	// SendUnknown means the request reached the destination but no answer came
	// back. The message may or may not exist there. It is terminal and is never
	// retried behind the caller's back: the way to make a duplicate is to try
	// again after this.
	SendUnknown SendStatus = "unknown"
)

// terminalSendStates are the states no further attempt will change.
func (s SendStatus) terminal() bool {
	switch s {
	case SendSent, SendFailed, SendUnknown:
		return true
	}
	return false
}

// SendOperation is one accepted send: what was asked for, whose it was, where
// it was going and what became of it.
//
// It is the record an asynchronous send is made of. The request that created it
// has long since been answered, and this is the only description of the work
// left anywhere — which is why the staged attachment's name, the content digest
// and the MTProto random id live here and not in the queue row.
type SendOperation struct {
	// ID is the row id. It is never handed to a caller: PublicID is.
	ID int64
	// PublicID is the opaque operation id the API returns and the status route
	// is addressed by. It is not sequential and it reveals nothing.
	PublicID string
	// UserID is the account the send runs under. Its permissions are read
	// again before the send, so a withdrawn grant stops one that is still
	// queued.
	UserID int64
	// ClientKey and DestinationChannelID name the destination connection and
	// the channel on it. The channel id is a decimal string so JavaScript
	// cannot truncate it.
	ClientKey            string
	DestinationChannelID string
	// RequestPayload is the effective request as JSON: the text, its spans and
	// the options. It is what the background worker sends, and what a repeated
	// request is compared against.
	RequestPayload string
	// MediaKind is the attachment's kind, empty for a text-only send.
	MediaKind string
	// MediaStagingName is the staged attachment's storage name. It is an opaque
	// name in this deployment's staging area and never a path.
	MediaStagingName string
	// MediaFilename is the name the sender gave the file, kept as metadata so
	// the message can show it. It is never used to build a path.
	MediaFilename string
	MediaMimeType string
	MediaSize     int64
	// MediaSHA256 is the digest of the staged bytes. Two uploads of the same
	// file name are different attachments and this is what says so.
	MediaSHA256 string
	// IdempotencyKey is the caller's own key, empty when it sent none.
	IdempotencyKey string
	// RequestFingerprint identifies the effective request. The same key with a
	// different fingerprint is a conflict, never a second send.
	RequestFingerprint string
	// RandomID is the client message id the first attempt sends. Every later
	// attempt reuses it, which is what makes a resend after a lost confirmation
	// the same message rather than a second one. It is written before the first
	// attempt and is never rewritten.
	RandomID int64

	Status SendStatus
	// Stage is the last stage the worker named, such as uploading_media or
	// sending_message. It is reported so a caller can see a send is not stuck
	// without being told anything about this deployment's internals.
	Stage         string
	AttemptCount  int
	NextAttemptAt *time.Time
	// RemoteMessageID is the message id the destination confirmed. It is zero
	// until one has been, and an unconfirmed send never claims one.
	RemoteMessageID  int64
	LastErrorCode    string
	LastErrorMessage string

	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	UpdatedAt   time.Time
}

// Errors a caller is expected to act on.
var (
	// ErrInvalidSendOperation means the operation record itself is not usable.
	ErrInvalidSendOperation = errors.New("channelstore: invalid send operation")
	// ErrSendOperationNotFound means no operation has that public id.
	ErrSendOperationNotFound = errors.New("channelstore: send operation not found")
	// ErrSendIdempotencyConflict means the idempotency key was already used for
	// a different request. The earlier operation is not this one and must not be
	// answered as though it were.
	ErrSendIdempotencyConflict = errors.New("channelstore: idempotency key already used for a different request")
)

// sendOperationColumns is the one list of a send operation's columns, so the
// insert, the lookup and the scans cannot drift apart.
const sendOperationColumns = `id, public_id, user_id, client_key, destination_channel_id,
	request_payload, media_kind, media_staging_name, media_filename, media_mime_type,
	media_size, media_sha256, idempotency_key, request_fingerprint, random_id,
	status, stage, attempt_count, next_attempt_at, remote_message_id,
	last_error_code, last_error_message, created_at, started_at, completed_at, updated_at`

// BeginSendOperation records a send that has been accepted for background
// processing, and reports whether it is new.
//
// created is false when an operation with this key already exists and is
// identical: the caller returns that operation and enqueues nothing, so a
// retried request cannot post twice. A key that named a different request is
// ErrSendIdempotencyConflict — the caller's own key has to mean one request,
// and a second meaning is refused rather than quietly given the first answer.
//
// A request with no key is always its own operation. It is still recorded, so
// the caller has something to poll; it simply joins nothing.
func (s *Store) BeginSendOperation(ctx context.Context, op SendOperation) (SendOperation, bool, error) {
	if op.UserID <= 0 {
		// A send runs under an account whose permissions can be read again when
		// it executes. Work with no account cannot, so it is not accepted.
		return SendOperation{}, false, fmt.Errorf("%w: the operation has no owning account", ErrInvalidSendOperation)
	}
	if strings.TrimSpace(op.ClientKey) == "" || strings.TrimSpace(op.DestinationChannelID) == "" {
		return SendOperation{}, false, fmt.Errorf("%w: the destination is not named", ErrInvalidSendOperation)
	}
	if strings.TrimSpace(op.RequestFingerprint) == "" {
		return SendOperation{}, false, fmt.Errorf("%w: the request has no fingerprint", ErrInvalidSendOperation)
	}

	if key := strings.TrimSpace(op.IdempotencyKey); key != "" {
		existing, found, err := s.SendOperationFor(ctx, op.UserID, key)
		if err != nil {
			return SendOperation{}, false, err
		}
		if found {
			if existing.RequestFingerprint != op.RequestFingerprint {
				return SendOperation{}, false, ErrSendIdempotencyConflict
			}
			return existing, false, nil
		}
	}

	now := s.now()
	publicID, err := newSendPublicID()
	if err != nil {
		return SendOperation{}, false, err
	}
	status := SendQueued
	res, err := s.db.ExecContext(ctx, `
INSERT INTO message_send_operations (
	public_id, user_id, client_key, destination_channel_id,
	request_payload, media_kind, media_staging_name, media_filename, media_mime_type,
	media_size, media_sha256, idempotency_key, request_fingerprint, random_id,
	status, stage, attempt_count, next_attempt_at, remote_message_id,
	last_error_code, last_error_message, created_at, started_at, completed_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', 0, NULL, 0, '', '', ?, NULL, NULL, ?)`,
		publicID, op.UserID, op.ClientKey, op.DestinationChannelID,
		op.RequestPayload, op.MediaKind, op.MediaStagingName, op.MediaFilename, op.MediaMimeType,
		op.MediaSize, op.MediaSHA256, strings.TrimSpace(op.IdempotencyKey), op.RequestFingerprint, op.RandomID,
		string(status), formatTime(now), formatTime(now))
	if err != nil {
		// Two requests with the same key can arrive together. The unique index
		// settles it: the one that lost is answered with the one that won, and
		// never with a second operation of its own.
		if isUniqueViolation(err) {
			existing, found, lookupErr := s.SendOperationFor(ctx, op.UserID, strings.TrimSpace(op.IdempotencyKey))
			if lookupErr != nil {
				return SendOperation{}, false, lookupErr
			}
			if found {
				if existing.RequestFingerprint != op.RequestFingerprint {
					return SendOperation{}, false, ErrSendIdempotencyConflict
				}
				return existing, false, nil
			}
			return SendOperation{}, false, ErrSendIdempotencyConflict
		}
		return SendOperation{}, false, fmt.Errorf("channelstore: begin send operation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SendOperation{}, false, fmt.Errorf("channelstore: begin send operation id: %w", err)
	}
	return SendOperation{
		ID:                   id,
		PublicID:             publicID,
		UserID:               op.UserID,
		ClientKey:            op.ClientKey,
		DestinationChannelID: op.DestinationChannelID,
		RequestPayload:       op.RequestPayload,
		MediaKind:            op.MediaKind,
		MediaStagingName:     op.MediaStagingName,
		MediaFilename:        op.MediaFilename,
		MediaMimeType:        op.MediaMimeType,
		MediaSize:            op.MediaSize,
		MediaSHA256:          op.MediaSHA256,
		IdempotencyKey:       strings.TrimSpace(op.IdempotencyKey),
		RequestFingerprint:   op.RequestFingerprint,
		RandomID:             op.RandomID,
		Status:               status,
		CreatedAt:            now,
		UpdatedAt:            now,
	}, true, nil
}

// SendOperation reads one operation by the public id a caller was given.
//
// It is a lookup by an opaque id and nothing else: the caller's own account and
// the destination named in the route are matched against what is stored, so an
// id that has leaked is still not somebody else's message.
func (s *Store) SendOperation(ctx context.Context, publicID string) (SendOperation, bool, error) {
	if strings.TrimSpace(publicID) == "" {
		return SendOperation{}, false, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sendOperationColumns+` FROM message_send_operations WHERE public_id = ?`, publicID)
	return scanSendOperation(row)
}

// SendOperationFor reads one operation by its account and idempotency key.
func (s *Store) SendOperationFor(ctx context.Context, userID int64, key string) (SendOperation, bool, error) {
	if key == "" {
		return SendOperation{}, false, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sendOperationColumns+` FROM message_send_operations
		 WHERE user_id = ? AND idempotency_key = ?`, userID, key)
	return scanSendOperation(row)
}

// StartSendOperation records that a worker has begun an attempt.
//
// The attempt number is written as the worker reports it rather than counted
// here: the queue owns the attempt budget, and a second count of the same thing
// would eventually disagree with it.
func (s *Store) StartSendOperation(ctx context.Context, id int64, attempt int, stage string) error {
	if attempt < 1 {
		return fmt.Errorf("%w: attempt must be positive", ErrInvalidSendOperation)
	}
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE message_send_operations
		 SET status = ?, stage = ?, attempt_count = ?, started_at = COALESCE(started_at, ?), updated_at = ?
		 WHERE id = ?`,
		string(SendProcessing), stage, attempt, formatTime(now), formatTime(now), id)
	if err != nil {
		return fmt.Errorf("channelstore: start send operation %d: %w", id, err)
	}
	return requireSendRow(res, id)
}

// RetrySendOperation records an attempt that will be tried again.
//
// It is not a failure: nothing is terminal here, the reason is kept for the
// caller who polls, and the time the next attempt becomes possible is stored so
// the answer is not "failed" while the queue is simply waiting. A zero next
// means the delay is the queue's own backoff and is not known here; the field is
// then left empty rather than filled with a guess.
func (s *Store) RetrySendOperation(ctx context.Context, id int64, attempt int, next time.Time, stage, code, message string) error {
	if attempt < 1 {
		return fmt.Errorf("%w: attempt must be positive", ErrInvalidSendOperation)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE message_send_operations
		 SET status = ?, stage = ?, attempt_count = ?, next_attempt_at = ?,
		     last_error_code = ?, last_error_message = ?, updated_at = ?
		 WHERE id = ? AND status NOT IN (?, ?, ?)`,
		string(SendRetrying), stage, attempt, nullableTimePtr(&next),
		code, message, formatTime(s.now()), id,
		string(SendSent), string(SendFailed), string(SendUnknown))
	if err != nil {
		return fmt.Errorf("channelstore: retry send operation %d: %w", id, err)
	}
	return requireSendRow(res, id)
}

// FinishSendOperation records a terminal outcome.
//
// A send that is already terminal is left alone: whatever a late worker reports
// after another one finished cannot rewrite a confirmed delivery or turn an
// unknown outcome into a failure.
func (s *Store) FinishSendOperation(ctx context.Context, id int64, status SendStatus, remoteMessageID int64, stage, code, message string) error {
	if !status.terminal() {
		return fmt.Errorf("%w: %q is not a terminal status", ErrInvalidSendOperation, status)
	}
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE message_send_operations
		 SET status = ?, stage = ?, remote_message_id = ?, last_error_code = ?, last_error_message = ?,
		     next_attempt_at = NULL, completed_at = ?, updated_at = ?
		 WHERE id = ? AND status NOT IN (?, ?, ?)`,
		string(status), stage, remoteMessageID, code, message, formatTime(now), formatTime(now), id,
		string(SendSent), string(SendFailed), string(SendUnknown))
	if err != nil {
		return fmt.Errorf("channelstore: finish send operation %d: %w", id, err)
	}
	return requireSendRow(res, id)
}

// SendOperationsHoldingMedia lists the operations whose staged attachment must
// not be deleted yet.
//
// It is what a cleanup sweep asks before removing anything: a file is kept
// while any operation still names it, which covers an operation that is queued,
// one that is retrying and one whose outcome is unknown — all of which may yet
// need the bytes.
func (s *Store) SendOperationsHoldingMedia(ctx context.Context) ([]SendOperation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sendOperationColumns+` FROM message_send_operations
		 WHERE media_staging_name <> '' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list send operations holding media: %w", err)
	}
	defer rows.Close()
	return scanSendOperations(rows)
}

// ExpiredSendOperations lists finished operations whose retention window has
// passed. An operation that is still live is never among them, however old: the
// work it describes has not happened yet.
func (s *Store) ExpiredSendOperations(ctx context.Context, before time.Time) ([]SendOperation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sendOperationColumns+` FROM message_send_operations
		 WHERE status IN (?, ?, ?) AND updated_at < ? ORDER BY id`,
		string(SendSent), string(SendFailed), string(SendUnknown), formatTime(before))
	if err != nil {
		return nil, fmt.Errorf("channelstore: list expired send operations: %w", err)
	}
	defer rows.Close()
	return scanSendOperations(rows)
}

// DeleteSendOperation removes one operation record. Its staged attachment is
// removed separately, because the two cannot be committed together.
func (s *Store) DeleteSendOperation(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM message_send_operations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("channelstore: delete send operation %d: %w", id, err)
	}
	return requireSendRow(res, id)
}

// requireSendRow reports a write that matched no operation.
func requireSendRow(res sql.Result, id int64) error {
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("channelstore: send operation %d: %w", id, err)
	}
	if changed == 0 {
		return ErrSendOperationNotFound
	}
	return nil
}

// scanSendOperation reads one row, reporting found=false for no row at all.
func scanSendOperation(row *sql.Row) (SendOperation, bool, error) {
	out, err := scanSendOperationValues(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return SendOperation{}, false, nil
	}
	if err != nil {
		return SendOperation{}, false, fmt.Errorf("channelstore: read send operation: %w", err)
	}
	return out, true, nil
}

func scanSendOperations(rows *sql.Rows) ([]SendOperation, error) {
	var out []SendOperation
	for rows.Next() {
		op, err := scanSendOperationValues(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("channelstore: read send operation: %w", err)
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: read send operations: %w", err)
	}
	return out, nil
}

// scanSendOperationValues is the one column order, shared by the single-row and
// the many-row reads.
func scanSendOperationValues(scan func(...any) error) (SendOperation, error) {
	var (
		out            SendOperation
		status         string
		nextAttemptAt  sql.NullString
		startedAt      sql.NullString
		completedAt    sql.NullString
		createdAt      string
		updatedAt      string
		idempotencyKey string
	)
	err := scan(&out.ID, &out.PublicID, &out.UserID, &out.ClientKey, &out.DestinationChannelID,
		&out.RequestPayload, &out.MediaKind, &out.MediaStagingName, &out.MediaFilename, &out.MediaMimeType,
		&out.MediaSize, &out.MediaSHA256, &idempotencyKey, &out.RequestFingerprint, &out.RandomID,
		&status, &out.Stage, &out.AttemptCount, &nextAttemptAt, &out.RemoteMessageID,
		&out.LastErrorCode, &out.LastErrorMessage, &createdAt, &startedAt, &completedAt, &updatedAt)
	if err != nil {
		return SendOperation{}, err
	}
	out.IdempotencyKey = idempotencyKey
	out.Status = SendStatus(status)
	out.NextAttemptAt = parseOptionalTime(nextAttemptAt)
	out.StartedAt = parseOptionalTime(startedAt)
	out.CompletedAt = parseOptionalTime(completedAt)
	out.CreatedAt = parseTime(createdAt)
	out.UpdatedAt = parseTime(updatedAt)
	return out, nil
}

// newSendPublicID issues the operation id a caller is handed: a fixed prefix so
// it is recognisable as one of these, and 16 bytes of randomness so it cannot
// be guessed, enumerated or derived from the row it names.
func newSendPublicID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("channelstore: send operation id: %w", err)
	}
	return "send_" + hex.EncodeToString(raw[:]), nil
}
