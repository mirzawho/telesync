package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/secrets"
)

// Webhook errors. Callers match them with errors.Is, and each one wraps the
// shared automation sentinel so a gateway that only knows the shared vocabulary
// still classifies it correctly.
var (
	ErrWebhookNotFound = fmt.Errorf("%w: webhook not found", automation.ErrNotFound)
	ErrInvalidWebhook  = fmt.Errorf("%w: invalid webhook", automation.ErrInvalid)
	// ErrDuplicateWebhookSource means the channel is already a source of this
	// webhook. webhook_sources' primary key is the proof.
	ErrDuplicateWebhookSource = fmt.Errorf("%w: that channel is already a source of this webhook", automation.ErrConflict)
)

// Defaults and bounds for one webhook. They mirror appconfig's automation
// webhooks block; the store keeps its own copy so a record written without them
// is still well formed.
const (
	defaultWebhookTimeout     = 15 * time.Second
	defaultWebhookMaxAttempts = 5
	// maxWebhookAttempts is the hard ceiling, whatever a record asks for.
	maxWebhookAttempts = 100
)

// requireOwnedWebhookSource refuses a source channel the webhook's account does
// not hold.
//
// managed_channels.account_id is the registration and the authority: a channel
// belongs to exactly one account, and only that account's automations may react
// to it.
func (s *Store) requireOwnedWebhookSource(ctx context.Context, accountID string, channelID int64) error {
	if accountID == "" {
		return nil
	}
	var registered string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(account_id, '') FROM managed_channels WHERE id = ?`, channelID).
		Scan(&registered)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no managed channel has id %d", ErrInvalidWebhook, channelID)
	}
	if err != nil {
		return fmt.Errorf("channelstore: check webhook source: %w", err)
	}
	if registered != accountID {
		return fmt.Errorf("%w: that channel is not registered under this account", ErrInvalidWebhook)
	}
	return nil
}

// webhookRow is the stored shape of one webhook. It is unexported on purpose:
// it is the only place the sealed secret is held, and it never leaves this file
// except through WebhookSecretBox.
type webhookRow struct {
	rec       automation.Webhook
	secret    secrets.Box
	createdBy int64
}

// CreateWebhook stores one outbound webhook and its source channels.
//
// secret is the already sealed secret; nil or an empty box means the webhook has
// none, which is only valid for auth_mode "none". The plaintext never reaches
// this method — the caller seals it first, exactly as WordPress site
// registration does.
func (s *Store) CreateWebhook(ctx context.Context, createdBy int64, rec automation.Webhook, secret *secrets.Box) (automation.Webhook, error) {
	row, err := validWebhook(rec, secret, hasSecret(secret))
	if err != nil {
		return automation.Webhook{}, err
	}
	// createdBy may be zero: the operator token is a credential and not an
	// account, so a webhook made through it has no owner. It is stored as NULL
	// and no account's deletion takes it with it.
	row.createdBy = createdBy

	// The webhook is registered against one user-owned account, and that account
	// has to be the creator's own. A webhook subscribed to a channel it has no
	// business watching is how one tenant receives another's message payloads,
	// so the binding is checked here and never taken on trust.
	if rec.AccountID != "" {
		var owner int64
		err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(owner_user_id, 0) FROM telegram_accounts WHERE id = ?`,
			rec.AccountID).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			return automation.Webhook{}, fmt.Errorf("%w: no account has that id", ErrInvalidWebhook)
		}
		if err != nil {
			return automation.Webhook{}, fmt.Errorf("channelstore: create webhook: %w", err)
		}
		if createdBy > 0 && owner != 0 && owner != createdBy {
			return automation.Webhook{}, fmt.Errorf("%w: that account belongs to another user", ErrInvalidWebhook)
		}
		row.rec.OwnerUserID = owner
		row.rec.AccountID = rec.AccountID
	} else {
		row.rec.OwnerUserID = createdBy
	}
	// Every source channel must be one that account holds. A subscription to a
	// channel registered under a different account would deliver that account's
	// messages here.
	for _, channelID := range rec.SourceChannelIDs {
		if err := s.requireOwnedWebhookSource(ctx, row.rec.AccountID, channelID); err != nil {
			return automation.Webhook{}, err
		}
	}

	now := s.now().UTC()
	row.rec.CreatedAt = now
	row.rec.UpdatedAt = now

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return automation.Webhook{}, fmt.Errorf("channelstore: create webhook: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
INSERT INTO webhooks (name, description, enabled, url, method, auth_mode, auth_header,
	secret_version, secret_nonce, secret_ciphertext, secret_sealed_at,
	timeout_ms, max_attempts, allow_localhost, created_by, created_at, updated_at,
	owner_user_id, account_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.rec.Name, row.rec.Description, boolInt(row.rec.Enabled), row.rec.URL,
		row.rec.Method, row.rec.AuthMode, row.rec.AuthHeader,
		row.secret.Version, row.secret.Nonce, row.secret.Ciphertext,
		nullableTime(row.secret.SealedAt),
		row.rec.Timeout.Milliseconds(), row.rec.MaxAttempts, boolInt(row.rec.AllowLocalhost),
		nullableUserID(row.createdBy),
		formatTime(row.rec.CreatedAt), formatTime(row.rec.UpdatedAt),
		nullableOwnerID(row.rec.OwnerUserID), nullableText(row.rec.AccountID))
	if err != nil {
		return automation.Webhook{}, fmt.Errorf("channelstore: create webhook: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return automation.Webhook{}, fmt.Errorf("channelstore: create webhook: %w", err)
	}
	if err := replaceWebhookSources(ctx, tx, id, row.rec.AccountID, row.rec.SourceChannelIDs, now); err != nil {
		return automation.Webhook{}, err
	}
	if err := tx.Commit(); err != nil {
		return automation.Webhook{}, fmt.Errorf("channelstore: create webhook: %w", err)
	}
	saved, _, err := s.GetWebhook(ctx, id)
	return saved, err
}

// GetWebhook returns one webhook by its local id, with its source channels.
//
// The returned record carries HasSecret and nothing else about the secret: the
// sealed box stays behind WebhookSecretBox, which only the delivery engine
// calls.
//
// It is unscoped and is for the engine, which reaches a webhook through a job
// that is already bound to one account. Anything that answers a request uses
// GetWebhookFor instead.
func (s *Store) GetWebhook(ctx context.Context, id int64) (automation.Webhook, bool, error) {
	row, err := scanWebhookRow(s.db.QueryRowContext(ctx, selectWebhookColumns+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return automation.Webhook{}, false, nil
	}
	if err != nil {
		return automation.Webhook{}, false, fmt.Errorf("channelstore: read webhook %d: %w", id, err)
	}
	sources, err := webhookSourceIDs(ctx, s.db, id)
	if err != nil {
		return automation.Webhook{}, false, err
	}
	row.rec.SourceChannelIDs = sources
	return row.rec, true, nil
}

// GetWebhookFor returns one webhook only when it belongs to owner.
//
// A webhook belonging to another owner is reported as not there rather than as
// forbidden: knowing an id must not confirm that it exists. It is the same rule
// the send operations already follow, and it is what stops an id from being an
// oracle across tenants.
func (s *Store) GetWebhookFor(ctx context.Context, ownerUserID, id int64) (automation.Webhook, bool, error) {
	if ownerUserID <= 0 {
		return automation.Webhook{}, false, nil
	}
	rec, found, err := s.GetWebhook(ctx, id)
	if err != nil || !found {
		return automation.Webhook{}, false, err
	}
	if rec.OwnerUserID != ownerUserID {
		return automation.Webhook{}, false, nil
	}
	return rec, true, nil
}

// ListWebhooks returns one page of webhooks and the total matching the filter,
// newest first. List rows also carry their source channels, the last delivery
// and the delivery tally.
//
// opts.Visible is the caller's own visibility restriction. It decides which rows
// are in the collection at all, so when it is set the whole matching set is read
// before a page is cut from it: filtering after the page was chosen would make
// the reported total count rows the caller may not see and would leave pages
// short. A configuration collection is small enough that this is the honest way
// to keep the totals true.
func (s *Store) ListWebhooks(ctx context.Context, opts automation.WebhookListOptions) (automation.WebhookPage, error) {
	conds := []string{"1 = 1"}
	args := []any{}
	// A webhook collection is one tenant's. Scoping here rather than after the
	// page is cut is what keeps the reported total honest and keeps one page
	// from being a page of everybody's.
	if opts.OwnerUserID > 0 {
		conds = append(conds, "owner_user_id = ?")
		args = append(args, opts.OwnerUserID)
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		conds = append(conds, "(name LIKE ? ESCAPE '\\' OR description LIKE ? ESCAPE '\\')")
		like := "%" + escapeLike(q) + "%"
		args = append(args, like, like)
	}
	if opts.Enabled != nil {
		conds = append(conds, "enabled = ?")
		args = append(args, boolInt(*opts.Enabled))
	}
	where := " WHERE " + strings.Join(conds, " AND ")

	if opts.Visible != nil {
		all, _, err := s.readWebhooks(ctx, where, args, -1, 0)
		if err != nil {
			return automation.WebhookPage{}, err
		}
		kept, _ := keepVisibleWebhooks(all, opts.Visible)
		limit, offset := pageBounds(opts.Page, opts.PerPage, 50)
		page := kept
		if offset < len(page) {
			page = page[offset:]
		} else {
			page = nil
		}
		if len(page) > limit {
			page = page[:limit]
		}
		ids := make([]int64, 0, len(page))
		for _, hook := range page {
			ids = append(ids, hook.ID)
		}
		if err := s.fillWebhookSummaries(ctx, page, ids); err != nil {
			return automation.WebhookPage{}, err
		}
		if page == nil {
			page = []automation.Webhook{}
		}
		return automation.WebhookPage{Webhooks: page, Total: len(kept)}, nil
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhooks`+where, args...).Scan(&total); err != nil {
		return automation.WebhookPage{}, fmt.Errorf("channelstore: list webhooks: %w", err)
	}
	limit, offset := pageBounds(opts.Page, opts.PerPage, 50)
	out, ids, err := s.readWebhooks(ctx, where, args, limit, offset)
	if err != nil {
		return automation.WebhookPage{}, err
	}
	if err := s.fillWebhookSummaries(ctx, out, ids); err != nil {
		return automation.WebhookPage{}, err
	}
	return automation.WebhookPage{Webhooks: out, Total: total}, nil
}

// readWebhooks reads matching webhooks newest first, with their source
// channels. A negative limit reads the whole matching set.
func (s *Store) readWebhooks(ctx context.Context, where string, args []any,
	limit, offset int) ([]automation.Webhook, []int64, error) {

	query := selectWebhookColumns + where + ` ORDER BY created_at DESC, id DESC`
	callArgs := args
	if limit >= 0 {
		query += ` LIMIT ? OFFSET ?`
		callArgs = append(append([]any{}, args...), limit, offset)
	}
	rows, err := s.db.QueryContext(ctx, query, callArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("channelstore: list webhooks: %w", err)
	}

	out := []automation.Webhook{}
	ids := []int64{}
	for rows.Next() {
		row, err := scanWebhookRow(rows)
		if err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("channelstore: list webhooks: %w", err)
		}
		out = append(out, row.rec)
		ids = append(ids, row.rec.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("channelstore: list webhooks: %w", err)
	}
	rows.Close()
	// The sources are read after the page's own cursor is closed, never while it
	// is open: a second query on the same handle inside the loop is how a store
	// deadlocks against itself.
	for i := range out {
		sources, err := webhookSourceIDs(ctx, s.db, out[i].ID)
		if err != nil {
			return nil, nil, err
		}
		out[i].SourceChannelIDs = sources
	}
	return out, ids, nil
}

// keepVisibleWebhooks keeps the rows the caller may see, which for a webhook
// means at least one visible source channel. A webhook with no source channel
// subscribes to nothing and is visible to nobody.
func keepVisibleWebhooks(in []automation.Webhook, visible func(localChannelID int64) bool) ([]automation.Webhook, []int64) {
	out := make([]automation.Webhook, 0, len(in))
	ids := make([]int64, 0, len(in))
	for _, hook := range in {
		if !anyVisible(visible, hook.SourceChannelIDs) {
			continue
		}
		out = append(out, hook)
		ids = append(ids, hook.ID)
	}
	return out, ids
}

// anyVisible reports whether any of the ids is visible under the restriction.
func anyVisible(visible func(localChannelID int64) bool, ids []int64) bool {
	for _, id := range ids {
		if visible(id) {
			return true
		}
	}
	return false
}

// UpdateWebhook replaces every stored field of one webhook and its sources.
//
// The secret has three states. A nil secret leaves the stored one untouched; a
// non-nil empty box removes it; a sealed box rotates it. That is the only way to
// keep a secret out of a read: an update never needs to round-trip it, and a
// client can clear one it cannot see.
func (s *Store) UpdateWebhook(ctx context.Context, rec automation.Webhook, secret *secrets.Box) (automation.Webhook, bool, error) {
	if rec.ID <= 0 {
		return automation.Webhook{}, false, fmt.Errorf("%w: id is required", ErrInvalidWebhook)
	}
	existing, found, err := s.GetWebhook(ctx, rec.ID)
	if err != nil {
		return automation.Webhook{}, false, err
	}
	if !found {
		return automation.Webhook{}, false, ErrWebhookNotFound
	}
	// What the webhook will have after this update: a nil secret keeps the
	// stored one, a non-nil empty box removes it, a sealed box replaces it.
	stored := existing.HasSecret
	if secret != nil {
		stored = hasSecret(secret)
	}
	row, err := validWebhook(rec, secret, stored)
	if err != nil {
		return automation.Webhook{}, false, err
	}
	row.rec.ID = existing.ID
	row.rec.CreatedAt = existing.CreatedAt
	row.rec.UpdatedAt = s.now().UTC()
	author, err := s.webhookAuthor(ctx, rec.ID)
	if err != nil {
		return automation.Webhook{}, false, err
	}
	row.createdBy = author
	// The binding is carried over from the stored row and never from the
	// request: an update changes what a webhook does, never whose it is.
	row.rec.OwnerUserID = existing.OwnerUserID
	row.rec.AccountID = existing.AccountID
	for _, channelID := range rec.SourceChannelIDs {
		if err := s.requireOwnedWebhookSource(ctx, existing.AccountID, channelID); err != nil {
			return automation.Webhook{}, false, err
		}
	}

	now := row.rec.UpdatedAt
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return automation.Webhook{}, false, fmt.Errorf("channelstore: update webhook %d: %w", rec.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	// A secret that was not given keeps its columns; one that was given — a
	// rotation or an explicit removal — rewrites them.
	secretSQL := `secret_version = ?, secret_nonce = ?, secret_ciphertext = ?, secret_sealed_at = ?, `
	secretArgs := []any{row.secret.Version, row.secret.Nonce, row.secret.Ciphertext, nullableTime(row.secret.SealedAt)}
	if secret == nil {
		secretSQL = ``
		secretArgs = nil
	}
	args := append([]any{row.rec.Name, row.rec.Description, boolInt(row.rec.Enabled), row.rec.URL,
		row.rec.Method, row.rec.AuthMode, row.rec.AuthHeader}, secretArgs...)
	args = append(args, row.rec.Timeout.Milliseconds(), row.rec.MaxAttempts,
		boolInt(row.rec.AllowLocalhost), formatTime(now), rec.ID)

	res, err := tx.ExecContext(ctx, `
UPDATE webhooks SET name = ?, description = ?, enabled = ?, url = ?, method = ?, auth_mode = ?, auth_header = ?, `+
		secretSQL+`timeout_ms = ?, max_attempts = ?, allow_localhost = ?, updated_at = ? WHERE id = ?`, args...)
	if err != nil {
		return automation.Webhook{}, false, fmt.Errorf("channelstore: update webhook %d: %w", rec.ID, err)
	}
	if changed, err := res.RowsAffected(); err != nil {
		return automation.Webhook{}, false, fmt.Errorf("channelstore: update webhook %d: %w", rec.ID, err)
	} else if changed == 0 {
		return automation.Webhook{}, false, ErrWebhookNotFound
	}
	if err := replaceWebhookSources(ctx, tx, rec.ID, row.rec.AccountID, row.rec.SourceChannelIDs, now); err != nil {
		return automation.Webhook{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return automation.Webhook{}, false, fmt.Errorf("channelstore: update webhook %d: %w", rec.ID, err)
	}
	saved, found, err := s.GetWebhook(ctx, rec.ID)
	return saved, found, err
}

// DeleteWebhook removes one webhook. Its sources, deliveries and attempts go by
// ON DELETE CASCADE; nothing about the receiver is touched.
func (s *Store) DeleteWebhook(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("channelstore: delete webhook %d: %w", id, err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("channelstore: delete webhook %d: %w", id, err)
	}
	return removed > 0, nil
}

// WebhookSecretBox returns one webhook's sealed secret.
//
// This is deliberately not part of the gateway's contract: automation.Webhook
// carries only HasSecret, and no HTTP route can reach this method. The one
// caller is the delivery engine, which opens the box to sign or authenticate a
// single request and never persists what it opened.
// WebhookSecretBoxFor returns one webhook's sealed secret only when it belongs
// to owner.
//
// The secret is the one field that must never cross a tenant boundary: it is
// what signs the deliveries and a leak of it lets one user forge another's
// traffic. "Not found" is the answer for another owner's webhook, exactly as it
// is for the record itself.
func (s *Store) WebhookSecretBoxFor(ctx context.Context, ownerUserID, id int64) (secrets.Box, bool, error) {
	if ownerUserID <= 0 {
		return secrets.Box{}, false, nil
	}
	rec, found, err := s.GetWebhookFor(ctx, ownerUserID, id)
	if err != nil || !found {
		return secrets.Box{}, false, err
	}
	if rec.AccountID == "" && rec.OwnerUserID == 0 {
		// An unowned webhook belongs to the operator token and has no tenant.
		// It is not handed to any user on request.
		return secrets.Box{}, false, nil
	}
	return s.WebhookSecretBox(ctx, id)
}

func (s *Store) WebhookSecretBox(ctx context.Context, id int64) (secrets.Box, bool, error) {
	var (
		box               secrets.Box
		nonce, ciphertext []byte
		sealedAt          sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
SELECT secret_version, secret_nonce, secret_ciphertext, secret_sealed_at FROM webhooks WHERE id = ?`, id).
		Scan(&box.Version, &nonce, &ciphertext, &sealedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return secrets.Box{}, false, nil
	}
	if err != nil {
		return secrets.Box{}, false, fmt.Errorf("channelstore: read webhook %d secret: %w", id, err)
	}
	box.Nonce = nonce
	box.Ciphertext = ciphertext
	if sealedAt.Valid {
		box.SealedAt = parseTime(sealedAt.String)
	}
	return box, true, nil
}

// webhookAuthor reads the account a webhook was created under.
//
// Nullable: a webhook made through the operator token has no owning account,
// and zero says so rather than failing the read.
func (s *Store) webhookAuthor(ctx context.Context, id int64) (int64, error) {
	var createdBy sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT created_by FROM webhooks WHERE id = ?`, id).Scan(&createdBy); err != nil {
		return 0, fmt.Errorf("channelstore: read webhook %d author: %w", id, err)
	}
	return createdBy.Int64, nil
}

// WebhookIDsForChannel reports which enabled webhooks subscribe to one managed
// source channel. It is the automation.WebhookSubscriptions implementation.
//
// The subscription is also required to match the channel's account. A managed
// channel belongs to exactly one account, so only that account's automations may
// react to it; the match is stated in the query rather than left to the write
// that created the subscription, so a row that somehow disagrees cannot put one
// tenant's messages into another's endpoint.
func (s *Store) WebhookIDsForChannel(ctx context.Context, managedChannelID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT w.id FROM webhooks w
JOIN webhook_sources s ON s.webhook_id = w.id
JOIN managed_channels c ON c.id = s.managed_channel_id
WHERE s.managed_channel_id = ? AND w.enabled = 1
  AND w.account_id IS NOT NULL
  AND c.account_id IS NOT NULL
  AND s.account_id = w.account_id
  AND w.account_id = c.account_id
ORDER BY w.id`, managedChannelID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: read webhook subscriptions: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("channelstore: read webhook subscriptions: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// webhookSourceIDs reads one webhook's managed channel ids, in insertion order.
// querier is what *sql.DB and *sql.Tx both offer for a multi-row read.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func webhookSourceIDs(ctx context.Context, q querier, webhookID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `
SELECT managed_channel_id FROM webhook_sources WHERE webhook_id = ? ORDER BY created_at, managed_channel_id`, webhookID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: read webhook sources: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("channelstore: read webhook sources: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// replaceWebhookSources rewrites one webhook's source set inside an open
// transaction, so a failed write cannot leave a half-subscribed webhook.
func replaceWebhookSources(ctx context.Context, tx *sql.Tx, webhookID int64, accountID string, channelIDs []int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM webhook_sources WHERE webhook_id = ?`, webhookID); err != nil {
		return fmt.Errorf("channelstore: replace webhook sources: %w", err)
	}
	for _, id := range channelIDs {
		if id <= 0 {
			return fmt.Errorf("%w: source channel ids must be positive", ErrInvalidWebhook)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO webhook_sources (webhook_id, managed_channel_id, account_id, created_at) VALUES (?, ?, ?, ?)`,
			webhookID, id, nullableText(accountID), formatTime(now)); err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicateWebhookSource
			}
			if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				return fmt.Errorf("%w: source channel %d does not exist", ErrInvalidWebhook, id)
			}
			return fmt.Errorf("channelstore: replace webhook sources: %w", err)
		}
	}
	return nil
}

const selectWebhookColumns = `SELECT id, name, description, enabled, url, method, auth_mode, auth_header,
	secret_version, secret_nonce, secret_ciphertext, secret_sealed_at,
	timeout_ms, max_attempts, allow_localhost, created_by, created_at, updated_at,
	COALESCE(owner_user_id, 0), COALESCE(account_id, '') FROM webhooks`

func scanWebhookRow(row scanner) (webhookRow, error) {
	var (
		out                        webhookRow
		enabled                    int
		timeoutMS                  int64
		allowLocalhost             int
		createdBy                  sql.NullInt64
		sealedAt, created, updated sql.NullString
		nonce, ciphertext          []byte
	)
	if err := row.Scan(&out.rec.ID, &out.rec.Name, &out.rec.Description, &enabled,
		&out.rec.URL, &out.rec.Method, &out.rec.AuthMode, &out.rec.AuthHeader,
		&out.secret.Version, &nonce, &ciphertext, &sealedAt,
		&timeoutMS, &out.rec.MaxAttempts, &allowLocalhost, &createdBy, &created, &updated,
		&out.rec.OwnerUserID, &out.rec.AccountID); err != nil {
		return webhookRow{}, err
	}
	out.createdBy = createdBy.Int64
	out.rec.Enabled = enabled != 0
	out.rec.AllowLocalhost = allowLocalhost != 0
	out.rec.Timeout = time.Duration(timeoutMS) * time.Millisecond
	out.secret.Nonce = nonce
	out.secret.Ciphertext = ciphertext
	if sealedAt.Valid {
		out.secret.SealedAt = parseTime(sealedAt.String)
	}
	out.rec.HasSecret = out.secret.Version == secrets.Version1 && len(out.secret.Ciphertext) > 0
	out.rec.CreatedAt = parseTime(created.String)
	out.rec.UpdatedAt = parseTime(updated.String)
	return out, nil
}

// fillWebhookSummaries attaches the delivery tally and the last delivery to a
// page of list rows. It is one aggregate query plus one "latest row" query, not
// a per-row round trip.
func (s *Store) fillWebhookSummaries(ctx context.Context, hooks []automation.Webhook, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := toAny(ids)

	rows, err := s.db.QueryContext(ctx, `
SELECT webhook_id,
	COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN status IN ('pending', 'delivering') THEN 1 ELSE 0 END), 0)
FROM webhook_deliveries WHERE webhook_id IN (`+placeholders+`) GROUP BY webhook_id`, args...)
	if err != nil {
		return fmt.Errorf("channelstore: summarize webhook deliveries: %w", err)
	}
	counts := map[int64]*automation.WebhookDeliveryCounts{}
	for rows.Next() {
		var id int64
		c := &automation.WebhookDeliveryCounts{}
		if err := rows.Scan(&id, &c.Succeeded, &c.Failed, &c.Pending); err != nil {
			rows.Close()
			return fmt.Errorf("channelstore: summarize webhook deliveries: %w", err)
		}
		counts[id] = c
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("channelstore: summarize webhook deliveries: %w", err)
	}
	rows.Close()

	lastRows, err := s.db.QueryContext(ctx, `
SELECT d.webhook_id, d.status, d.last_http_status, d.updated_at, d.attempt_count
FROM webhook_deliveries d
WHERE d.webhook_id IN (`+placeholders+`)
	AND d.id = (SELECT x.id FROM webhook_deliveries x WHERE x.webhook_id = d.webhook_id
		ORDER BY x.created_at DESC, x.id DESC LIMIT 1)`, args...)
	if err != nil {
		return fmt.Errorf("channelstore: read last webhook deliveries: %w", err)
	}
	defer lastRows.Close()
	last := map[int64]*automation.WebhookDeliverySummary{}
	for lastRows.Next() {
		var (
			id       int64
			status   string
			http     int
			at       sql.NullString
			attempts int
		)
		if err := lastRows.Scan(&id, &status, &http, &at, &attempts); err != nil {
			return fmt.Errorf("channelstore: read last webhook deliveries: %w", err)
		}
		last[id] = &automation.WebhookDeliverySummary{
			Status:       status,
			HTTPStatus:   http,
			At:           parseTime(at.String),
			AttemptCount: attempts,
		}
	}
	if err := lastRows.Err(); err != nil {
		return fmt.Errorf("channelstore: read last webhook deliveries: %w", err)
	}

	for i := range hooks {
		hooks[i].Summary = counts[hooks[i].ID]
		if hooks[i].Summary == nil {
			hooks[i].Summary = &automation.WebhookDeliveryCounts{}
		}
		hooks[i].LastDelivery = last[hooks[i].ID]
	}
	return nil
}

// isEmptyBox reports whether a box carries no secret at all.
//
// A non-nil empty box is how an update says "remove the stored secret", which is
// different from a nil secret, which says "leave it alone". Keeping the two
// apart is what lets a client clear a credential without ever reading it back.
func isEmptyBox(b secrets.Box) bool {
	return b.Version == 0 && len(b.Nonce) == 0 && len(b.Ciphertext) == 0
}

// hasSecret reports whether a secret argument is a real sealed value rather than
// an explicit "none".
func hasSecret(secret *secrets.Box) bool {
	return secret != nil && !isEmptyBox(*secret)
}

// validWebhook checks what the store can check without opening the secret: the
// record is well formed, the secret is sealed when it is given, and the
// configuration can actually be delivered.
//
// hasSecretAfter is what the webhook will hold once this change is applied, so
// an update may keep a secret it is not touching while still being refused an
// auth mode that has nothing to authenticate with.
func validWebhook(rec automation.Webhook, secret *secrets.Box, hasSecretAfter bool) (webhookRow, error) {
	out := webhookRow{rec: rec}
	out.rec.Name = strings.TrimSpace(rec.Name)
	out.rec.Description = strings.TrimSpace(rec.Description)
	out.rec.URL = strings.TrimSpace(rec.URL)
	out.rec.Method = strings.ToUpper(strings.TrimSpace(rec.Method))
	out.rec.AuthMode = strings.ToLower(strings.TrimSpace(rec.AuthMode))
	out.rec.AuthHeader = strings.TrimSpace(rec.AuthHeader)

	if out.rec.Name == "" {
		return webhookRow{}, fmt.Errorf("%w: name is required", ErrInvalidWebhook)
	}
	if err := validWebhookURL(out.rec.URL); err != nil {
		return webhookRow{}, err
	}
	switch out.rec.Method {
	case "POST", "PUT", "PATCH":
	default:
		return webhookRow{}, fmt.Errorf("%w: method must be POST, PUT or PATCH", ErrInvalidWebhook)
	}
	switch out.rec.AuthMode {
	case "hmac", "bearer", "header", "none":
	default:
		return webhookRow{}, fmt.Errorf("%w: auth_mode must be hmac, bearer, header or none", ErrInvalidWebhook)
	}
	if out.rec.AuthMode == "header" {
		if err := validAuthHeader(out.rec.AuthHeader); err != nil {
			return webhookRow{}, err
		}
	} else if out.rec.AuthHeader != "" {
		return webhookRow{}, fmt.Errorf("%w: auth_header is only used with auth_mode header", ErrInvalidWebhook)
	}

	// Zero means "use the default"; a negative value is a mistake, not a
	// request for the default.
	switch {
	case out.rec.Timeout == 0:
		out.rec.Timeout = defaultWebhookTimeout
	case out.rec.Timeout < 0:
		return webhookRow{}, fmt.Errorf("%w: timeout must not be negative", ErrInvalidWebhook)
	case out.rec.Timeout > 24*time.Hour:
		return webhookRow{}, fmt.Errorf("%w: timeout is unreasonably long", ErrInvalidWebhook)
	}
	switch {
	case out.rec.MaxAttempts == 0:
		out.rec.MaxAttempts = defaultWebhookMaxAttempts
	case out.rec.MaxAttempts < 0:
		return webhookRow{}, fmt.Errorf("%w: max_attempts must not be negative", ErrInvalidWebhook)
	case out.rec.MaxAttempts > maxWebhookAttempts:
		return webhookRow{}, fmt.Errorf("%w: max_attempts must be at most %d", ErrInvalidWebhook, maxWebhookAttempts)
	}

	// The secret has three states: left alone (nil), explicitly absent (a
	// non-nil empty box) and a sealed box. Only the last is written in full.
	switch {
	case secret == nil:
	case isEmptyBox(*secret):
		out.secret = secrets.Box{}
	default:
		if secret.Version != secrets.Version1 {
			return webhookRow{}, fmt.Errorf("%w: the secret must be sealed (version %d)", ErrInvalidWebhook, secrets.Version1)
		}
		if len(secret.Nonce) == 0 || len(secret.Ciphertext) == 0 {
			return webhookRow{}, fmt.Errorf("%w: the sealed secret is incomplete", ErrInvalidWebhook)
		}
		out.secret = *secret
	}
	if !hasSecretAfter && out.rec.AuthMode != "none" {
		return webhookRow{}, fmt.Errorf("%w: auth_mode %s needs a secret", ErrInvalidWebhook, out.rec.AuthMode)
	}

	for _, id := range out.rec.SourceChannelIDs {
		if id <= 0 {
			return webhookRow{}, fmt.Errorf("%w: source channel ids must be positive", ErrInvalidWebhook)
		}
	}
	return out, nil
}

// validWebhookURL accepts an absolute http(s) address with a host and no
// credentials or fragment. Whether the host is somewhere this installation may
// connect is decided at delivery time by the webhook transport, which re-checks
// it on every attempt; this is only about the record being well formed.
func validWebhookURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: url is required", ErrInvalidWebhook)
	}
	if len(raw) > 2048 {
		return fmt.Errorf("%w: url is too long", ErrInvalidWebhook)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: url is malformed", ErrInvalidWebhook)
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return fmt.Errorf("%w: url must be an absolute http(s) address", ErrInvalidWebhook)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: url must use http or https", ErrInvalidWebhook)
	}
	if u.User != nil {
		return fmt.Errorf("%w: url must not carry credentials", ErrInvalidWebhook)
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return fmt.Errorf("%w: url must not have a fragment", ErrInvalidWebhook)
	}
	return nil
}

// refusedAuthHeaders are request headers a webhook may never set: they describe
// the transport itself, or they are the signature headers this package owns.
var refusedAuthHeaders = map[string]bool{
	"host":                true,
	"content-length":      true,
	"connection":          true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"proxy-authorization": true,
	"authorization":       true,
}

// validAuthHeader refuses a custom auth header that would rewrite the request's
// framing or one of the Standard Webhooks headers.
func validAuthHeader(name string) error {
	if name == "" {
		return fmt.Errorf("%w: auth_header is required with auth_mode header", ErrInvalidWebhook)
	}
	if strings.ContainsAny(name, " \t\r\n:/") {
		return fmt.Errorf("%w: auth_header is not a valid header name", ErrInvalidWebhook)
	}
	for _, r := range name {
		if r <= 0x20 || r >= 0x7f {
			return fmt.Errorf("%w: auth_header is not a valid header name", ErrInvalidWebhook)
		}
	}
	lower := strings.ToLower(name)
	if refusedAuthHeaders[lower] {
		return fmt.Errorf("%w: auth_header must not overwrite %s", ErrInvalidWebhook, name)
	}
	if strings.HasPrefix(lower, "webhook-") {
		return fmt.Errorf("%w: auth_header must not use the webhook- prefix", ErrInvalidWebhook)
	}
	return nil
}

// escapeLike neutralizes the LIKE wildcards in a user-supplied search term.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// pageBounds turns 1-based paging into a LIMIT/OFFSET pair with a sane cap.
func pageBounds(page, perPage, maxPerPage int) (limit, offset int) {
	if perPage <= 0 {
		perPage = maxPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}
	if page < 1 {
		page = 1
	}
	return perPage, (page - 1) * perPage
}
