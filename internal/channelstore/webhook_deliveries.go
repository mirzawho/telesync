package channelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
)

// Delivery errors. Callers match them with errors.Is.
var (
	ErrDeliveryNotFound = fmt.Errorf("%w: webhook delivery not found", automation.ErrNotFound)
	// ErrDuplicateDelivery means this webhook already has a delivery for that
	// event. webhook_deliveries' UNIQUE (webhook_id, event_id) is the proof, and
	// it is the idempotency guarantee: a retry extends the row it finds.
	ErrDuplicateDelivery = fmt.Errorf("%w: that event is already being delivered to this webhook", automation.ErrConflict)
	// ErrDuplicateAttempt means the attempt number is already recorded. Attempts
	// are append-only history and are never rewritten.
	ErrDuplicateAttempt = fmt.Errorf("%w: that attempt is already recorded", automation.ErrConflict)
	ErrInvalidDelivery  = fmt.Errorf("%w: invalid webhook delivery", automation.ErrInvalid)
)

// Delivery lifecycle states, matching the schema's CHECK constraint.
const (
	DeliveryPending    = "pending"
	DeliveryDelivering = "delivering"
	DeliverySucceeded  = "succeeded"
	DeliveryFailed     = "failed"
	DeliveryCancelled  = "cancelled"
)

// maxDeliveryAttempts is the hard ceiling on one delivery's attempts, whatever
// a webhook's configuration asks for. Retries are never unbounded.
const maxDeliveryAttempts = 100

// CreateWebhookDelivery records one logical delivery of one event to one
// webhook.
//
// There is at most one row per (webhook_id, event_id): a second call reports
// ErrDuplicateDelivery rather than starting a second delivery, so a duplicate
// job or a replayed event cannot send the same message twice by accident.
func (s *Store) CreateWebhookDelivery(ctx context.Context, rec automation.WebhookDelivery) (automation.WebhookDelivery, error) {
	if rec.WebhookID <= 0 {
		return automation.WebhookDelivery{}, fmt.Errorf("%w: webhook_id is required", ErrInvalidDelivery)
	}
	if strings.TrimSpace(rec.EventID) == "" {
		return automation.WebhookDelivery{}, fmt.Errorf("%w: event_id is required", ErrInvalidDelivery)
	}
	if !validDeliveryStatus(rec.Status) {
		return automation.WebhookDelivery{}, fmt.Errorf("%w: unknown status %q", ErrInvalidDelivery, rec.Status)
	}
	// A delivery is owned by the webhook it belongs to, and that is a
	// consistency check as well as a fact: the webhook's owner and the event's
	// owner have to be the same user. A webhook subscribed to a channel it has
	// no business watching would otherwise be a way to receive another tenant's
	// message payloads, and this is where that stops.
	owner, err := s.deliveryOwnership(ctx, rec.WebhookID, rec.EventID)
	if err != nil {
		return automation.WebhookDelivery{}, err
	}
	if !owner.Bound() || (rec.OwnerUserID != 0 && rec.OwnerUserID != owner.OwnerUserID) ||
		(rec.AccountID != "" && rec.AccountID != owner.AccountID) {
		return automation.WebhookDelivery{}, fmt.Errorf(
			"%w: queued delivery does not match its persisted owner and account", ErrInvalidDelivery)
	}
	rec.OwnerUserID = owner.OwnerUserID
	rec.AccountID = owner.AccountID

	now := s.now().UTC()
	rec.CreatedAt = now
	rec.UpdatedAt = now
	rec.AttemptCount = 0
	rec.CompletedAt = nil

	res, err := s.db.ExecContext(ctx, `
INSERT INTO webhook_deliveries (webhook_id, event_id, status, attempt_count, next_attempt_at,
	last_http_status, last_error, created_at, updated_at, completed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.WebhookID, rec.EventID, rec.Status, rec.AttemptCount,
		nullableTimePtr(rec.NextAttemptAt), rec.LastHTTPStatus, rec.LastError,
		formatTime(rec.CreatedAt), formatTime(rec.UpdatedAt), nullableTimePtr(rec.CompletedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return automation.WebhookDelivery{}, ErrDuplicateDelivery
		}
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return automation.WebhookDelivery{}, fmt.Errorf("%w: the webhook or the event does not exist", ErrInvalidDelivery)
		}
		return automation.WebhookDelivery{}, fmt.Errorf("channelstore: create webhook delivery: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return automation.WebhookDelivery{}, fmt.Errorf("channelstore: create webhook delivery: %w", err)
	}
	return s.getWebhookDeliveryRow(ctx, id)
}

// deliveryOwnership derives the owner a delivery is recorded under.
//
// It is a consistency check as well as a derivation: the webhook's owner and the
// event's owner must be the same user. A webhook subscribed to a channel it has
// no business watching would otherwise be a way to receive another tenant's
// message payloads, and this is where that stops.
func (s *Store) deliveryOwnership(ctx context.Context, webhookID int64, eventID string) (automation.Owner, error) {
	var (
		hookOwner, eventOwner sql.NullInt64
		hookAccount           sql.NullString
		eventAccount          sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
SELECT w.owner_user_id, w.account_id, e.account_id, a.owner_user_id
FROM webhooks w
JOIN automation_events e ON e.id = ?
LEFT JOIN telegram_accounts a ON a.id = e.account_id
WHERE w.id = ?`, eventID, webhookID).Scan(&hookOwner, &hookAccount, &eventAccount, &eventOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.Owner{}, fmt.Errorf("%w: the webhook or the event does not exist", ErrInvalidDelivery)
	}
	if err != nil {
		return automation.Owner{}, fmt.Errorf("channelstore: read delivery ownership: %w", err)
	}
	if !hookOwner.Valid || !eventOwner.Valid || !hookAccount.Valid || !eventAccount.Valid ||
		hookOwner.Int64 <= 0 || eventOwner.Int64 <= 0 ||
		hookAccount.String == "" || eventAccount.String == "" ||
		hookOwner.Int64 != eventOwner.Int64 || hookAccount.String != eventAccount.String {
		return automation.Owner{}, fmt.Errorf(
			"%w: this webhook has no resolved owner/account match for that event", ErrInvalidDelivery)
	}
	owner := automation.Owner{}
	owner.OwnerUserID = hookOwner.Int64
	owner.AccountID = hookAccount.String
	return owner, nil
}

// WebhookDeliveryForEvent reads the one delivery of one event to one webhook.
// It is how a retried job finds the row it must extend instead of creating a
// second one.
func (s *Store) WebhookDeliveryForEvent(ctx context.Context, webhookID int64, eventID string) (automation.WebhookDelivery, bool, error) {
	row := s.db.QueryRowContext(ctx, selectDeliveryColumns+` WHERE webhook_id = ? AND event_id = ?`, webhookID, eventID)
	rec, err := scanDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.WebhookDelivery{}, false, nil
	}
	if err != nil {
		return automation.WebhookDelivery{}, false, fmt.Errorf("channelstore: read webhook delivery: %w", err)
	}
	return rec, true, nil
}

// GetWebhookDelivery returns one delivery with its full attempt history and the
// request actually transmitted. This is the detail response.
func (s *Store) GetWebhookDelivery(ctx context.Context, id int64) (automation.WebhookDelivery, bool, error) {
	rec, err := s.getWebhookDeliveryRow(ctx, id)
	if errors.Is(err, ErrDeliveryNotFound) {
		return automation.WebhookDelivery{}, false, nil
	}
	if err != nil {
		return automation.WebhookDelivery{}, false, err
	}
	attempts, err := s.WebhookAttempts(ctx, id)
	if err != nil {
		return automation.WebhookDelivery{}, false, err
	}
	rec.Attempts = attempts
	rec.Request = requestRecordOf(attempts)
	return rec, true, nil
}

// GetDeliveryFor returns one delivery only when owner owns its webhook.
//
// A delivery's request and response history is the most sensitive material in
// the subsystem: it is a message payload and an endpoint's answer. "Not found"
// is the answer for another owner's delivery, so an id never confirms that one
// exists.
func (s *Store) GetDeliveryFor(ctx context.Context, ownerUserID, webhookID, deliveryID int64) (automation.WebhookDelivery, bool, error) {
	if ownerUserID <= 0 {
		return automation.WebhookDelivery{}, false, nil
	}
	rec, found, err := s.GetWebhookDelivery(ctx, deliveryID)
	if err != nil || !found {
		return automation.WebhookDelivery{}, false, err
	}
	if rec.OwnerUserID != ownerUserID || rec.WebhookID != webhookID {
		return automation.WebhookDelivery{}, false, nil
	}
	return rec, true, nil
}

// ListDeliveriesFor lists one webhook's deliveries only when owner owns it.
func (s *Store) ListDeliveriesFor(ctx context.Context, ownerUserID, webhookID int64, opts automation.DeliveryListOptions) (automation.WebhookDeliveryPage, error) {
	if ownerUserID <= 0 {
		return automation.WebhookDeliveryPage{Deliveries: []automation.WebhookDelivery{}}, nil
	}
	owned, found, err := s.GetWebhookFor(ctx, ownerUserID, webhookID)
	if err != nil {
		return automation.WebhookDeliveryPage{}, err
	}
	if !found {
		return automation.WebhookDeliveryPage{Deliveries: []automation.WebhookDelivery{}}, nil
	}
	opts.OwnerUserID = owned.OwnerUserID
	return s.ListWebhookDeliveries(ctx, webhookID, opts)
}

// WebhookAttemptsFor reads one delivery's request/response history only when
// owner owns it.
func (s *Store) WebhookAttemptsFor(ctx context.Context, ownerUserID, deliveryID int64) ([]automation.WebhookAttempt, error) {
	if ownerUserID <= 0 {
		return []automation.WebhookAttempt{}, nil
	}
	rec, found, err := s.getWebhookDeliveryRowOrNone(ctx, deliveryID)
	if err != nil {
		return nil, err
	}
	if !found || rec.OwnerUserID != ownerUserID {
		return []automation.WebhookAttempt{}, nil
	}
	return s.WebhookAttempts(ctx, deliveryID)
}

func (s *Store) getWebhookDeliveryRowOrNone(ctx context.Context, id int64) (automation.WebhookDelivery, bool, error) {
	rec, err := s.getWebhookDeliveryRow(ctx, id)
	if errors.Is(err, ErrDeliveryNotFound) {
		return automation.WebhookDelivery{}, false, nil
	}
	if err != nil {
		return automation.WebhookDelivery{}, false, err
	}
	return rec, true, nil
}

// fillDeliveryOwners attaches each delivery's ownership from its webhook.
//
// webhook_deliveries carries no owner columns: a delivery is owned by the
// webhook it is a delivery of, and that relationship is the whole of the
// binding. It is read out here so every record handed to a caller says whose it
// is, and the owner-scoped reads filter on it.
func (s *Store) fillDeliveryOwners(ctx context.Context, recs []automation.WebhookDelivery) error {
	if len(recs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(recs))
	seen := map[int64]bool{}
	for _, rec := range recs {
		if !seen[rec.WebhookID] {
			seen[rec.WebhookID] = true
			ids = append(ids, rec.WebhookID)
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(owner_user_id, 0), COALESCE(account_id, '')
		 FROM webhooks WHERE id IN (`+placeholders+`)`, toAny(ids)...)
	if err != nil {
		return fmt.Errorf("channelstore: read delivery owners: %w", err)
	}
	defer rows.Close()
	owners := map[int64]automation.Owner{}
	for rows.Next() {
		var id, ownerID int64
		var account string
		if err := rows.Scan(&id, &ownerID, &account); err != nil {
			return fmt.Errorf("channelstore: read delivery owners: %w", err)
		}
		owners[id] = automation.Owner{OwnerUserID: ownerID, AccountID: account}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("channelstore: read delivery owners: %w", err)
	}
	for i := range recs {
		if o, ok := owners[recs[i].WebhookID]; ok {
			recs[i].OwnerUserID = o.OwnerUserID
			recs[i].AccountID = o.AccountID
		}
	}
	return nil
}

// ListWebhookDeliveries returns one page of one webhook's deliveries and the
// total matching the filter, newest first.
func (s *Store) ListWebhookDeliveries(ctx context.Context, webhookID int64, opts automation.DeliveryListOptions) (automation.WebhookDeliveryPage, error) {
	if webhookID <= 0 {
		return automation.WebhookDeliveryPage{}, fmt.Errorf("%w: webhook_id is required", ErrInvalidDelivery)
	}
	conds := []string{"webhook_id = ?"}
	args := []any{webhookID}
	// Ownership is not a column on this table: a delivery is owned by the
	// webhook it is a delivery of. The caller's owner is applied against that
	// binding after the rows are read, so a page never contains another
	// tenant's history even when the ids were guessed.
	if opts.OwnerUserID > 0 {
		conds = append(conds, "webhook_id IN (SELECT id FROM webhooks WHERE owner_user_id = ?)")
		args = append(args, opts.OwnerUserID)
	}
	if status := strings.TrimSpace(opts.Status); status != "" {
		if !validDeliveryStatus(status) {
			return automation.WebhookDeliveryPage{}, fmt.Errorf("%w: unknown status %q", ErrInvalidDelivery, status)
		}
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	where := " WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries`+where, args...).Scan(&total); err != nil {
		return automation.WebhookDeliveryPage{}, fmt.Errorf("channelstore: list webhook deliveries: %w", err)
	}
	limit, offset := pageBounds(opts.Page, opts.PerPage, 100)
	rows, err := s.db.QueryContext(ctx, selectDeliveryColumns+where+
		` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return automation.WebhookDeliveryPage{}, fmt.Errorf("channelstore: list webhook deliveries: %w", err)
	}
	defer rows.Close()

	out := []automation.WebhookDelivery{}
	for rows.Next() {
		rec, err := scanDelivery(rows)
		if err != nil {
			return automation.WebhookDeliveryPage{}, fmt.Errorf("channelstore: list webhook deliveries: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return automation.WebhookDeliveryPage{}, fmt.Errorf("channelstore: list webhook deliveries: %w", err)
	}
	if err := s.fillDeliveryOwners(ctx, out); err != nil {
		return automation.WebhookDeliveryPage{}, err
	}
	return automation.WebhookDeliveryPage{Deliveries: out, Total: total}, nil
}

// UpdateWebhookDelivery rewrites one delivery's state after an attempt.
//
// created_at is never rewritten: it records when the event arrived. The
// attempt history is append-only and is never modified from here.
func (s *Store) UpdateWebhookDelivery(ctx context.Context, rec automation.WebhookDelivery) (automation.WebhookDelivery, bool, error) {
	if rec.ID <= 0 {
		return automation.WebhookDelivery{}, false, fmt.Errorf("%w: id is required", ErrInvalidDelivery)
	}
	if !validDeliveryStatus(rec.Status) {
		return automation.WebhookDelivery{}, false, fmt.Errorf("%w: unknown status %q", ErrInvalidDelivery, rec.Status)
	}
	if rec.AttemptCount < 0 {
		return automation.WebhookDelivery{}, false, fmt.Errorf("%w: attempt_count must not be negative", ErrInvalidDelivery)
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE webhook_deliveries SET status = ?, attempt_count = ?, next_attempt_at = ?,
	last_http_status = ?, last_error = ?, updated_at = ?, completed_at = ?
WHERE id = ?`,
		rec.Status, rec.AttemptCount, nullableTimePtr(rec.NextAttemptAt),
		rec.LastHTTPStatus, rec.LastError, formatTime(s.now().UTC()),
		nullableTimePtr(rec.CompletedAt), rec.ID)
	if err != nil {
		return automation.WebhookDelivery{}, false, fmt.Errorf("channelstore: update webhook delivery %d: %w", rec.ID, err)
	}
	if changed, err := res.RowsAffected(); err != nil {
		return automation.WebhookDelivery{}, false, fmt.Errorf("channelstore: update webhook delivery %d: %w", rec.ID, err)
	} else if changed == 0 {
		return automation.WebhookDelivery{}, false, ErrDeliveryNotFound
	}
	saved, err := s.getWebhookDeliveryRow(ctx, rec.ID)
	if errors.Is(err, ErrDeliveryNotFound) {
		return automation.WebhookDelivery{}, false, nil
	}
	if err != nil {
		return automation.WebhookDelivery{}, false, err
	}
	return saved, true, nil
}

// AppendWebhookAttempt records one HTTP exchange against one delivery.
//
// The record must already be redacted and already bounded before it gets here:
// this method stores what it is given, so a caller that skipped redaction would
// persist a secret. Attempts are never rewritten, so the history is a faithful
// account of what was sent and what came back.
func (s *Store) AppendWebhookAttempt(ctx context.Context, deliveryID int64, att automation.WebhookAttempt) (automation.WebhookAttempt, error) {
	if deliveryID <= 0 {
		return automation.WebhookAttempt{}, fmt.Errorf("%w: delivery_id is required", ErrInvalidDelivery)
	}
	if att.AttemptNumber <= 0 {
		return automation.WebhookAttempt{}, fmt.Errorf("%w: attempt_number must be positive", ErrInvalidDelivery)
	}
	if att.AttemptNumber > maxDeliveryAttempts {
		return automation.WebhookAttempt{}, fmt.Errorf("%w: attempt_number must be at most %d", ErrInvalidDelivery, maxDeliveryAttempts)
	}
	reqHeaders, err := encodeHeaders(att.RequestHeaders)
	if err != nil {
		return automation.WebhookAttempt{}, fmt.Errorf("%w: request headers: %v", ErrInvalidDelivery, err)
	}
	resHeaders, err := encodeHeaders(att.ResponseHeaders)
	if err != nil {
		return automation.WebhookAttempt{}, fmt.Errorf("%w: response headers: %v", ErrInvalidDelivery, err)
	}
	now := s.now().UTC()
	if att.StartedAt.IsZero() {
		att.StartedAt = now
	}

	res, err := s.db.ExecContext(ctx, `
INSERT INTO webhook_attempts (delivery_id, attempt_number,
	request_method, request_url, request_headers, request_body, request_body_truncated,
	response_status, response_headers, response_body, response_body_truncated, response_bytes,
	duration_ms, error_code, error_message, started_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		deliveryID, att.AttemptNumber,
		att.RequestMethod, att.RequestURL, reqHeaders, att.RequestBody, boolInt(att.RequestBodyTruncated),
		att.ResponseStatus, resHeaders, att.ResponseBody, boolInt(att.ResponseBodyTruncated), att.ResponseBytes,
		att.DurationMS, att.ErrorCode, att.ErrorMessage,
		formatTime(att.StartedAt), nullableTimePtr(att.FinishedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return automation.WebhookAttempt{}, ErrDuplicateAttempt
		}
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return automation.WebhookAttempt{}, fmt.Errorf("%w: the delivery does not exist", ErrInvalidDelivery)
		}
		return automation.WebhookAttempt{}, fmt.Errorf("channelstore: record webhook attempt: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return automation.WebhookAttempt{}, fmt.Errorf("channelstore: record webhook attempt: %w", err)
	}
	att.ID = id
	return att, nil
}

// WebhookAttempts returns one delivery's full attempt history, oldest first.
func (s *Store) WebhookAttempts(ctx context.Context, deliveryID int64) ([]automation.WebhookAttempt, error) {
	rows, err := s.db.QueryContext(ctx, selectAttemptColumns+
		` WHERE delivery_id = ? ORDER BY attempt_number`, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: read webhook attempts: %w", err)
	}
	defer rows.Close()
	out := []automation.WebhookAttempt{}
	for rows.Next() {
		att, err := scanAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: read webhook attempts: %w", err)
		}
		out = append(out, att)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: read webhook attempts: %w", err)
	}
	return out, nil
}

// LatestWebhookAttempt reads the most recent exchange recorded for a delivery.
// A retry prefers these exact request bytes over rebuilding them, so what was
// signed is what is re-sent.
func (s *Store) LatestWebhookAttempt(ctx context.Context, deliveryID int64) (automation.WebhookAttempt, bool, error) {
	row := s.db.QueryRowContext(ctx, selectAttemptColumns+
		` WHERE delivery_id = ? ORDER BY attempt_number DESC LIMIT 1`, deliveryID)
	att, err := scanAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.WebhookAttempt{}, false, nil
	}
	if err != nil {
		return automation.WebhookAttempt{}, false, fmt.Errorf("channelstore: read latest webhook attempt: %w", err)
	}
	return att, true, nil
}

// getWebhookDeliveryRow reads the delivery row without its history.
//
// Its ownership comes from the webhook it belongs to: webhook_deliveries has no
// owner columns of its own, and the webhook is the authority on whose delivery
// this is. The field is filled in here so every record that leaves this file
// says whose it is.
func (s *Store) getWebhookDeliveryRow(ctx context.Context, id int64) (automation.WebhookDelivery, error) {
	rec, err := scanDelivery(s.db.QueryRowContext(ctx, selectDeliveryColumns+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return automation.WebhookDelivery{}, ErrDeliveryNotFound
	}
	if err != nil {
		return automation.WebhookDelivery{}, fmt.Errorf("channelstore: read webhook delivery %d: %w", id, err)
	}
	owner, err := s.webhookOwner(ctx, rec.WebhookID)
	if err != nil {
		return automation.WebhookDelivery{}, err
	}
	rec.OwnerUserID = owner.OwnerUserID
	rec.AccountID = owner.AccountID
	return rec, nil
}

// webhookOwner reads one webhook's ownership binding.
func (s *Store) webhookOwner(ctx context.Context, webhookID int64) (automation.Owner, error) {
	var (
		ownerID sql.NullInt64
		account sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT owner_user_id, account_id FROM webhooks WHERE id = ?`, webhookID).
		Scan(&ownerID, &account)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.Owner{}, fmt.Errorf("%w: webhook %d does not exist", ErrInvalidDelivery, webhookID)
	}
	if err != nil {
		return automation.Owner{}, fmt.Errorf("channelstore: read webhook %d owner: %w", webhookID, err)
	}
	return automation.Owner{OwnerUserID: ownerID.Int64, AccountID: account.String}, nil
}

const selectDeliveryColumns = `SELECT id, webhook_id, event_id, status, attempt_count, next_attempt_at,
	last_http_status, last_error, created_at, updated_at, completed_at FROM webhook_deliveries`

const selectAttemptColumns = `SELECT id, delivery_id, attempt_number,
	request_method, request_url, request_headers, request_body, request_body_truncated,
	response_status, response_headers, response_body, response_body_truncated, response_bytes,
	duration_ms, error_code, error_message, started_at, finished_at FROM webhook_attempts`

func scanDelivery(row scanner) (automation.WebhookDelivery, error) {
	var (
		out                                     automation.WebhookDelivery
		nextAttempt, created, updated, finished sql.NullString
		lastError                               string
	)
	if err := row.Scan(&out.ID, &out.WebhookID, &out.EventID, &out.Status, &out.AttemptCount,
		&nextAttempt, &out.LastHTTPStatus, &lastError, &created, &updated, &finished); err != nil {
		return automation.WebhookDelivery{}, err
	}
	out.LastError = lastError
	out.NextAttemptAt = parseOptionalTime(nextAttempt)
	out.CreatedAt = parseTime(created.String)
	out.UpdatedAt = parseTime(updated.String)
	out.CompletedAt = parseOptionalTime(finished)
	return out, nil
}

func scanAttempt(row scanner) (automation.WebhookAttempt, error) {
	var (
		out                      automation.WebhookAttempt
		deliveryID               int64
		reqHeaders, resHeaders   string
		reqTrunc, resTrunc       int
		started, finished        sql.NullString
		method, url, reqBody     string
		resBody, errCode, errMsg string
	)
	_ = deliveryID
	if err := row.Scan(&out.ID, &deliveryID, &out.AttemptNumber,
		&method, &url, &reqHeaders, &reqBody, &reqTrunc,
		&out.ResponseStatus, &resHeaders, &resBody, &resTrunc, &out.ResponseBytes,
		&out.DurationMS, &errCode, &errMsg, &started, &finished); err != nil {
		return automation.WebhookAttempt{}, err
	}
	out.RequestMethod = method
	out.RequestURL = url
	out.RequestBody = reqBody
	out.RequestBodyTruncated = reqTrunc != 0
	out.ResponseBody = resBody
	out.ResponseBodyTruncated = resTrunc != 0
	out.ErrorCode = errCode
	out.ErrorMessage = errMsg
	out.StartedAt = parseTime(started.String)
	out.FinishedAt = parseOptionalTime(finished)
	var err error
	if out.RequestHeaders, err = decodeHeaders(reqHeaders); err != nil {
		return automation.WebhookAttempt{}, err
	}
	if out.ResponseHeaders, err = decodeHeaders(resHeaders); err != nil {
		return automation.WebhookAttempt{}, err
	}
	return out, nil
}

// nullableTimePtr stores an optional timestamp, or NULL when there is none.
func nullableTimePtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatTime(*t)
}

func parseOptionalTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t := parseTime(v.String)
	return &t
}

// requestRecordOf derives the "request actually transmitted" view from the
// attempt history. The most recent attempt is the one a reader means when they
// ask what was sent.
func requestRecordOf(attempts []automation.WebhookAttempt) *automation.WebhookRequestRecord {
	if len(attempts) == 0 {
		return nil
	}
	last := attempts[len(attempts)-1]
	return &automation.WebhookRequestRecord{
		Method:        last.RequestMethod,
		URL:           last.RequestURL,
		Headers:       last.RequestHeaders,
		Body:          last.RequestBody,
		BodyTruncated: last.RequestBodyTruncated,
	}
}

// encodeHeaders stores a header map as a JSON object, so the shape a reader
// gets back is the shape that was recorded.
func encodeHeaders(h map[string]string) (string, error) {
	if len(h) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeHeaders(raw string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

func validDeliveryStatus(status string) bool {
	switch status {
	case DeliveryPending, DeliveryDelivering, DeliverySucceeded, DeliveryFailed, DeliveryCancelled:
		return true
	}
	return false
}
