package channelstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
	ID     int64
	UserID int64
	// AccountID is the user-owned account this publication runs under. It is
	// what the background job is bound to, so a publish recorded for one user's
	// account is never delivered through another's session. It is empty only on
	// a row that predates account ownership.
	AccountID       string
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
	existing, found, err := s.GetPublishOperationForAccount(ctx, op.UserID, op.AccountID, op.IdempotencyKey)
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
	user_id, account_id, idempotency_key, request_hash, destination_type, destination_id,
	status, result, error_code, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, '', '', ?, ?)`,
		op.UserID, nullableText(op.AccountID), op.IdempotencyKey, op.RequestHash, op.DestinationType, op.DestinationID,
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

// PublishIdempotencyKey derives the key that makes one publication one
// publication.
//
// The owner and its account are part of the key, and that is the point. Two
// users may legitimately copy the same remote message to the same destination,
// and a key built from the message alone would make the second user's publish a
// silent no-op — their content would never arrive and nothing would say why. A
// key that names the owner makes each user's publish its own.
//
// The same owner asking twice for the same source and destination gets the same
// key back, which is what makes a retry one publication rather than two.
//
// The key is a digest: it is bounded, and no source metadata lands in the
// publisher's idempotency record.
func (s *Store) PublishIdempotencyKey(_ context.Context, ownerUserID int64, accountID, kind, networkKey string, channelID, messageID int64, destinationID string) (string, error) {
	if ownerUserID <= 0 || strings.TrimSpace(accountID) == "" {
		// Work with no owning account is work nothing may execute, and it must
		// not share a key with a real owner's publication.
		return "", fmt.Errorf("%w: an idempotency key needs an owning account", ErrInvalidPublishOperation)
	}
	identity := strings.Join([]string{
		kind,
		strconv.FormatInt(ownerUserID, 10),
		strings.TrimSpace(accountID),
		networkKey,
		strconv.FormatInt(channelID, 10),
		strconv.FormatInt(messageID, 10),
		destinationID,
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return "pub-v2-" + hex.EncodeToString(sum[:]), nil
}

// GetPublishOperation returns a legacy/unscoped attempt for one user and key.
// Personal-account publication must use GetPublishOperationForAccount.
func (s *Store) GetPublishOperation(ctx context.Context, userID int64, key string) (PublishOperation, bool, error) {
	unscoped := ""
	return s.getPublishOperation(ctx, userID, &unscoped, key)
}

// GetPublishOperationForAccount returns one stored attempt within the named
// account scope. An empty account ID selects legacy/unscoped operations only.
func (s *Store) GetPublishOperationForAccount(ctx context.Context, userID int64, accountID, key string) (PublishOperation, bool, error) {
	return s.getPublishOperation(ctx, userID, &accountID, key)
}

func (s *Store) getPublishOperation(ctx context.Context, userID int64, accountScope *string, key string) (PublishOperation, bool, error) {
	query := `SELECT id, user_id, account_id, idempotency_key, request_hash, destination_type, destination_id,
	status, result, error_code, created_at, updated_at
	FROM publish_operations WHERE user_id = ? AND idempotency_key = ?`
	args := []any{userID, key}
	if accountScope != nil {
		query += ` AND account_id IS ?`
		if strings.TrimSpace(*accountScope) == "" {
			args = append(args, nil)
		} else {
			args = append(args, strings.TrimSpace(*accountScope))
		}
	}
	row := s.db.QueryRowContext(ctx,
		query, args...)
	var (
		out            PublishOperation
		status         string
		createdAt      string
		updatedAt      string
		destinationTyp string
		accountID      sql.NullString
	)
	err := row.Scan(&out.ID, &out.UserID, &accountID, &out.IdempotencyKey, &out.RequestHash, &destinationTyp,
		&out.DestinationID, &status, &out.Result, &out.ErrorCode, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishOperation{}, false, nil
	}
	if err != nil {
		return PublishOperation{}, false, fmt.Errorf("channelstore: get publish operation: %w", err)
	}
	out.AccountID = accountID.String
	out.DestinationType = destinationTyp
	out.Status = PublishStatus(status)
	out.CreatedAt = parseTime(createdAt)
	out.UpdatedAt = parseTime(updatedAt)
	return out, true, nil
}

// ErrInvalidPublishOperation means the attempt record itself is not usable.
var ErrInvalidPublishOperation = errors.New("channelstore: invalid publish operation")
