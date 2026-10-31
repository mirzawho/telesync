package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PublishStatus is where one publishing attempt stands.
type PublishStatus string

const (
	// PublishInFlight means the attempt started and has not reported a result.
	// A second request with the same key must wait for it rather than post
	// twice.
	PublishInFlight PublishStatus = "in_flight"
	// PublishSucceeded means the destination confirmed the publish. A retry
	// returns the stored result.
	PublishSucceeded PublishStatus = "succeeded"
	// PublishFailed means the destination refused. A retry is allowed.
	PublishFailed PublishStatus = "failed"
	// PublishUnknown means the request reached the destination but no answer
	// came back. It is never re-run automatically: the post may already exist.
	PublishUnknown PublishStatus = "unknown"
)

// PublishOperation is one publishing attempt, kept so a request carrying the
// same Idempotency-Key cannot publish twice.
//
// It is not a publication history: it holds only what a retry needs.
type PublishOperation struct {
	ID              int64
	UserID          int64
	IdempotencyKey  string
	RequestHash     string
	DestinationType string // "telegram" or "wordpress"
	DestinationID   string
	Status          PublishStatus
	Result          string
	ErrorCode       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ErrIdempotencyConflict means the idempotency key was already used for a
// different request body.
var ErrIdempotencyConflict = errors.New("channelstore: idempotency key already used for a different request")

// ErrPublishOperationNotFound means no stored operation matched.
var ErrPublishOperationNotFound = errors.New("channelstore: publish operation not found")

// BeginPublishOperation records the start of one publishing attempt.
//
// created is true when the caller may run the attempt: it is a new key, or a
// retry of one that failed. It is false when an attempt with the same key is
// already in flight, already succeeded, or left an unknown outcome — the
// caller then reports the stored state instead of publishing again.
func (s *Store) BeginPublishOperation(ctx context.Context, op PublishOperation) (PublishOperation, bool, error) {
	if op.IdempotencyKey == "" {
		return PublishOperation{}, false, ErrInvalidPublishOperation
	}
	existing, found, err := s.GetPublishOperation(ctx, op.UserID, op.IdempotencyKey)
	if err != nil {
		return PublishOperation{}, false, err
	}
	if found {
		if existing.RequestHash != op.RequestHash {
			return existing, false, ErrIdempotencyConflict
		}
		if existing.Status != PublishFailed {
			return existing, false, nil
		}
		// A failed attempt is retryable: the same row is reopened rather than
		// a second one created.
		now := s.now()
		if _, err := s.db.ExecContext(ctx,
			`UPDATE publish_operations SET status = ?, error_code = '', updated_at = ? WHERE id = ?`,
			string(PublishInFlight), formatTime(now), existing.ID); err != nil {
			return PublishOperation{}, false, fmt.Errorf("channelstore: reopen publish operation %d: %w", existing.ID, err)
		}
		existing.Status = PublishInFlight
		existing.ErrorCode = ""
		existing.UpdatedAt = now
		return existing, true, nil
	}

	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO publish_operations (
	user_id, idempotency_key, request_hash, destination_type, destination_id,
	status, result, error_code, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, '', '', ?, ?)`,
		op.UserID, op.IdempotencyKey, op.RequestHash, op.DestinationType, op.DestinationID,
		string(PublishInFlight), formatTime(now), formatTime(now))
	if err != nil {
		if isUniqueViolation(err) {
			return PublishOperation{}, false, ErrIdempotencyConflict
		}
		return PublishOperation{}, false, fmt.Errorf("channelstore: begin publish operation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return PublishOperation{}, false, fmt.Errorf("channelstore: begin publish operation id: %w", err)
	}
	op.ID = id
	op.Status = PublishInFlight
	op.Result = ""
	op.ErrorCode = ""
	op.CreatedAt = now
	op.UpdatedAt = now
	return op, true, nil
}

// FinishPublishOperation records the outcome of an attempt.
func (s *Store) FinishPublishOperation(ctx context.Context, id int64, status PublishStatus, result, errorCode string) error {
	switch status {
	case PublishSucceeded, PublishFailed, PublishUnknown:
	default:
		return ErrInvalidPublishOperation
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE publish_operations SET status = ?, result = ?, error_code = ?, updated_at = ? WHERE id = ?`,
		string(status), result, errorCode, formatTime(s.now()), id)
	if err != nil {
		return fmt.Errorf("channelstore: finish publish operation %d: %w", id, err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return ErrPublishOperationNotFound
	}
	return nil
}

// GetPublishOperation returns the stored attempt for one account and key.
func (s *Store) GetPublishOperation(ctx context.Context, userID int64, key string) (PublishOperation, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, idempotency_key, request_hash, destination_type, destination_id,
	status, result, error_code, created_at, updated_at
	FROM publish_operations WHERE user_id = ? AND idempotency_key = ?`, userID, key)
	var (
		out            PublishOperation
		status         string
		createdAt      string
		updatedAt      string
		destinationTyp string
	)
	err := row.Scan(&out.ID, &out.UserID, &out.IdempotencyKey, &out.RequestHash, &destinationTyp,
		&out.DestinationID, &status, &out.Result, &out.ErrorCode, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishOperation{}, false, nil
	}
	if err != nil {
		return PublishOperation{}, false, fmt.Errorf("channelstore: get publish operation: %w", err)
	}
	out.DestinationType = destinationTyp
	out.Status = PublishStatus(status)
	out.CreatedAt = parseTime(createdAt)
	out.UpdatedAt = parseTime(updatedAt)
	return out, true, nil
}

// ErrInvalidPublishOperation means the attempt record itself is not usable.
var ErrInvalidPublishOperation = errors.New("channelstore: invalid publish operation")
