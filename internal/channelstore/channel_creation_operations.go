package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ChannelCreationPending  = "pending"
	ChannelCreationUnknown  = "unknown"
	ChannelCreationComplete = "created"
	ChannelCreationPartial  = "created_partial"
)

var ErrChannelCreationIdempotencyConflict = errors.New("channelstore: channel creation idempotency key reused with a different request")

// ChannelCreationOperation is the durable recovery record for one non-idempotent
// remote create. UserID 0 is reserved for the loopback operator token, which
// has no database user record.
type ChannelCreationOperation struct {
	ID        int64
	UserID    int64
	// AccountID is the user-owned account this creation runs under.
	AccountID         string
	ClientKey         string
	IdempotencyKey    string
	Fingerprint       string
	RequestPayload    string
	Status            string
	RemoteChannelID   int64
	LocalChannelID    int64
	ChannelCreated    bool
	UsernameAssigned  bool
	AvatarUpdated     bool
	LocallyRegistered bool
	ErrorCode         string
	ErrorMessage      string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// BeginChannelCreation records an attempt before any remote side effect. A
// retry with the same principal, connection, key and fingerprint returns the
// existing operation; a mismatched request is a conflict. An unfinished
// operation must never be silently restarted after an ambiguous outcome.
func (s *Store) BeginChannelCreation(ctx context.Context, userID int64, accountID, clientKey, idempotencyKey, fingerprint, payload string) (ChannelCreationOperation, bool, error) {
	if userID < 0 || strings.TrimSpace(clientKey) == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(fingerprint) == "" {
		return ChannelCreationOperation{}, false, errors.New("channelstore: invalid channel creation identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChannelCreationOperation{}, false, fmt.Errorf("channelstore: begin channel creation: %w", err)
	}
	defer tx.Rollback()

	existing, found, err := scanChannelCreation(tx.QueryRowContext(ctx, `SELECT `+channelCreationColumns+`
		FROM channel_creation_operations WHERE user_id = ? AND client_key = ? AND idempotency_key = ?`, userID, clientKey, idempotencyKey))
	if err != nil {
		return ChannelCreationOperation{}, false, err
	}
	if found {
		if existing.Fingerprint != fingerprint {
			return ChannelCreationOperation{}, false, ErrChannelCreationIdempotencyConflict
		}
		return existing, false, nil
	}
	now := s.now().UTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO channel_creation_operations
		(user_id, account_id, client_key, idempotency_key, request_fingerprint, request_payload, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, userID, nullableText(accountID), clientKey, idempotencyKey, fingerprint, payload,
		ChannelCreationPending, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return ChannelCreationOperation{}, false, fmt.Errorf("channelstore: insert channel creation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return ChannelCreationOperation{}, false, fmt.Errorf("channelstore: read channel creation id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ChannelCreationOperation{}, false, fmt.Errorf("channelstore: commit channel creation: %w", err)
	}
	return ChannelCreationOperation{
		// The binding is reported back as it was stored, so a caller that checks
		// it does not see an ownerless operation it just created.
		ID: id, UserID: userID, AccountID: accountID, ClientKey: clientKey, IdempotencyKey: idempotencyKey,
		Fingerprint: fingerprint, RequestPayload: payload, Status: ChannelCreationPending,
		CreatedAt: now, UpdatedAt: now,
	}, true, nil
}

// UpdateChannelCreation persists the latest confirmed remote/local stages.
func (s *Store) UpdateChannelCreation(ctx context.Context, op ChannelCreationOperation) error {
	if op.ID <= 0 || op.UserID < 0 || strings.TrimSpace(op.ClientKey) == "" || strings.TrimSpace(op.IdempotencyKey) == "" || strings.TrimSpace(op.Fingerprint) == "" {
		return errors.New("channelstore: invalid channel creation operation")
	}
	switch op.Status {
	case ChannelCreationPending, ChannelCreationUnknown, ChannelCreationComplete, ChannelCreationPartial:
	default:
		return fmt.Errorf("channelstore: invalid channel creation status %q", op.Status)
	}
	if op.RemoteChannelID < 0 || op.LocalChannelID < 0 || (op.RemoteChannelID == 0 && (op.ChannelCreated || op.UsernameAssigned || op.AvatarUpdated || op.LocallyRegistered)) {
		return errors.New("channelstore: invalid channel creation identity or completed stage")
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE channel_creation_operations SET
		status = ?, remote_channel_id = ?, local_channel_id = ?, channel_created = ?, username_assigned = ?,
		avatar_updated = ?, locally_registered = ?, error_code = ?, error_message = ?, updated_at = ?
		WHERE id = ? AND user_id = ? AND client_key = ? AND idempotency_key = ? AND request_fingerprint = ?`,
		op.Status, op.RemoteChannelID, op.LocalChannelID, op.ChannelCreated, op.UsernameAssigned,
		op.AvatarUpdated, op.LocallyRegistered, op.ErrorCode, op.ErrorMessage, now.Format(time.RFC3339Nano),
		op.ID, op.UserID, op.ClientKey, op.IdempotencyKey, op.Fingerprint)
	if err != nil {
		return fmt.Errorf("channelstore: update channel creation: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("channelstore: count channel creation update: %w", err)
	}
	if changed != 1 {
		return errors.New("channelstore: channel creation operation not found")
	}
	return nil
}

// GetChannelCreation reads one operation scoped to the authenticated principal
// and selected connection.
func (s *Store) GetChannelCreation(ctx context.Context, userID int64, clientKey, idempotencyKey string) (ChannelCreationOperation, bool, error) {
	if userID < 0 || strings.TrimSpace(clientKey) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return ChannelCreationOperation{}, false, errors.New("channelstore: invalid channel creation identity")
	}
	return scanChannelCreation(s.db.QueryRowContext(ctx, `SELECT `+channelCreationColumns+`
		FROM channel_creation_operations WHERE user_id = ? AND client_key = ? AND idempotency_key = ?`, userID, clientKey, idempotencyKey))
}

const channelCreationColumns = `id, user_id, account_id, client_key, idempotency_key, request_fingerprint, request_payload,
	status, remote_channel_id, local_channel_id, channel_created, username_assigned, avatar_updated,
	locally_registered, error_code, error_message, created_at, updated_at`

type channelCreationRow interface {
	Scan(dest ...any) error
}

func scanChannelCreation(row channelCreationRow) (ChannelCreationOperation, bool, error) {
	var op ChannelCreationOperation
	var createdAt, updatedAt string
	var accountID sql.NullString
	err := row.Scan(&op.ID, &op.UserID, &accountID, &op.ClientKey, &op.IdempotencyKey, &op.Fingerprint, &op.RequestPayload,
		&op.Status, &op.RemoteChannelID, &op.LocalChannelID, &op.ChannelCreated, &op.UsernameAssigned,
		&op.AvatarUpdated, &op.LocallyRegistered, &op.ErrorCode, &op.ErrorMessage, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelCreationOperation{}, false, nil
	}
	if err != nil {
		return ChannelCreationOperation{}, false, fmt.Errorf("channelstore: read channel creation: %w", err)
	}
	op.AccountID = accountID.String
	op.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ChannelCreationOperation{}, false, fmt.Errorf("channelstore: parse channel creation created_at: %w", err)
	}
	op.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return ChannelCreationOperation{}, false, fmt.Errorf("channelstore: parse channel creation updated_at: %w", err)
	}
	return op, true, nil
}
