package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file is the account-scoped registration and read path (v0.0.5 R8).
//
// Store.Upsert records a channel under the quarantined account of a legacy
// client key. That is correct for a join made through the legacy keyed route
// and wrong for a user-initiated one: a channel a user joined through their own
// account belongs to that account. These methods are the difference.
//
// Ownership is a dimension of the row, never a filter applied afterwards. A
// caller that names an account it does not own is refused at the write and is
// not found at the read, so no identifier answers for someone else's session.

// RegisterOption adjusts one account-scoped registration.
type RegisterOption func(*registerRequest)

type registerRequest struct {
	ownerUserID int64
}

// WithOwner scopes a registration to the user that owns the account.
//
// Without it the write trusts the account id alone, which is right for a
// background worker already bound to one account and wrong for an HTTP request
// path. A request-scoped caller always passes it.
func WithOwner(ownerUserID int64) RegisterOption {
	return func(r *registerRequest) { r.ownerUserID = ownerUserID }
}

// ErrAccountNotOwned means the named account does not belong to the caller.
// It is never reported to a client as a distinct answer: an account another
// user holds is not-found, so an id never confirms its own existence.
var ErrAccountNotOwned = errors.New("channelstore: that account is not yours")

// RegisterForAccount records one managed channel under one account.
//
// The pair (account_id, channel_id) is unique, so two users subscribing to the
// same remote channel get two local rows with two local ids, each with its own
// categories, workflows, rights and history (brief §15). Re-registering the
// same channel for the same account refreshes the one row it already has.
//
// Unlike Upsert this never invents an account: an empty or unknown account id
// is refused rather than silently falling back to a quarantined legacy one.
func (s *Store) RegisterForAccount(ctx context.Context, ch Channel, opts ...RegisterOption) (Channel, error) {
	req := registerRequest{}
	for _, opt := range opts {
		opt(&req)
	}
	ch.AccountID = strings.TrimSpace(ch.AccountID)
	if ch.AccountID == "" {
		return Channel{}, errors.New("channelstore: an account_id is required to register a channel")
	}
	if strings.TrimSpace(ch.ClientKey) == "" {
		return Channel{}, errors.New("channelstore: client_key is required")
	}
	if ch.ChannelID <= 0 {
		return Channel{}, errors.New("channelstore: channel_id must be positive")
	}
	if ch.PeerType != PeerChannel && ch.PeerType != PeerGroup {
		return Channel{}, fmt.Errorf("channelstore: unsupported peer_type %q", ch.PeerType)
	}
	switch ch.MembershipStatus {
	case StatusMember, StatusJoinPending, StatusUnknown:
	default:
		return Channel{}, fmt.Errorf("channelstore: unsupported membership_status %q", ch.MembershipStatus)
	}
	if req.ownerUserID > 0 {
		if err := s.requireAccountOwner(ctx, ch.AccountID, req.ownerUserID); err != nil {
			return Channel{}, err
		}
	}
	return s.upsertOnAccount(ctx, ch.AccountID, ch)
}

// UpsertFor records one managed channel under one named account.
//
// It is RegisterForAccount for a caller that already holds exactly one account
// — a worker bound to it, a background job carrying its owner binding — and so
// needs no separate owner argument. The owner is never taken from the caller
// here: the account id is the identity, and who owns it is the store's business
// to know.
//
// It never invents an account. An empty or unknown account id is refused,
// never quietly recorded against a quarantined legacy session the way the
// pre-account Upsert does.
func (s *Store) UpsertFor(ctx context.Context, accountID string, ch Channel) (Channel, error) {
	ch.AccountID = strings.TrimSpace(accountID)
	return s.RegisterForAccount(ctx, ch)
}

// requireAccountOwner reports whether owner holds the named account.
//
// The lookup is owner-scoped in SQL, so it is one query and not a read followed
// by a comparison that could be raced. A quarantined account belongs to nobody
// and is never owned.
func (s *Store) requireAccountOwner(ctx context.Context, accountID string, owner int64) error {
	if owner <= 0 {
		return ErrAccountNotOwned
	}
	var found int
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM telegram_accounts
	WHERE id = ? AND owner_user_id = ? AND deleted_at IS NULL AND status != ?)`,
		accountID, owner, AccountStatusQuarantined).Scan(&found)
	if err != nil {
		return fmt.Errorf("channelstore: verify account %s ownership: %w", accountID, err)
	}
	if found == 0 {
		return ErrAccountNotOwned
	}
	return nil
}

// scanAccountID is the account column of one managed channel row.
const scanAccountID = `SELECT account_id FROM managed_channels`

// upsertOnAccount is Upsert's write for one named account.
//
// It is the same statement Upsert makes, with the account id supplied rather
// than derived from a legacy client key. Notices and permissions keep the same
// rule: a report that did not carry them leaves the stored values alone.
func (s *Store) upsertOnAccount(ctx context.Context, accountID string, ch Channel) (Channel, error) {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO managed_channels (
	account_id, client_key, channel_id, peer_type, title, username, description,
	avatar_photo_id, avatar_available, membership_status, participants_count,
	verified, scam, fake, notices_known, created_at, updated_at, last_synced_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (account_id, channel_id) DO UPDATE SET
	peer_type = excluded.peer_type,
	title = excluded.title,
	username = excluded.username,
	description = excluded.description,
	avatar_photo_id = excluded.avatar_photo_id,
	avatar_available = excluded.avatar_available,
	membership_status = excluded.membership_status,
	participants_count = excluded.participants_count,
	updated_at = excluded.updated_at,
	last_synced_at = excluded.last_synced_at`,
		accountID, ch.ClientKey, ch.ChannelID, ch.PeerType, ch.Title, ch.Username, ch.Description,
		ch.AvatarPhotoID, boolInt(ch.AvatarAvailable), ch.MembershipStatus,
		nullableInt(ch.ParticipantsCount),
		boolInt(ch.Verified), boolInt(ch.Scam), boolInt(ch.Fake), boolInt(ch.NoticesKnown),
		formatTime(now), formatTime(now), formatTime(now),
	); err != nil {
		return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
	}
	if ch.NoticesKnown {
		if _, err := tx.ExecContext(ctx, `
UPDATE managed_channels SET verified = ?, scam = ?, fake = ?, notices_known = 1
WHERE account_id = ? AND channel_id = ?`,
			boolInt(ch.Verified), boolInt(ch.Scam), boolInt(ch.Fake),
			accountID, ch.ChannelID,
		); err != nil {
			return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
		}
	}
	if ch.PermissionsKnown {
		if _, err := tx.ExecContext(ctx, `
UPDATE managed_channels SET permission_new = ?, permission_edit = ?, permission_delete = ?,
	permission_read = ?, permissions_known = 1
WHERE account_id = ? AND channel_id = ?`,
			boolInt(ch.Permissions.New), boolInt(ch.Permissions.Edit),
			boolInt(ch.Permissions.Delete), boolInt(ch.Permissions.Read),
			accountID, ch.ChannelID,
		); err != nil {
			return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
		}
	}

	row := tx.QueryRowContext(ctx, selectColumns+` WHERE account_id = ? AND channel_id = ?`,
		accountID, ch.ChannelID)
	saved, err := s.scanChannel(row)
	if err != nil {
		return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
	}
	saved.AccountID = accountID
	if err := tx.Commit(); err != nil {
		return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
	}
	return saved, nil
}

// GetForAccount returns one channel registered under one account, by its
// Telegram id.
//
// A channel registered under another account is not found here, so a valid
// channel id in a URL cannot reach past the account that owns the row.
func (s *Store) GetForAccount(ctx context.Context, accountID string, channelID int64) (Channel, bool, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || channelID <= 0 {
		return Channel{}, false, nil
	}
	row := s.db.QueryRowContext(ctx,
		selectColumns+` WHERE account_id = ? AND channel_id = ?`, accountID, channelID)
	ch, err := s.scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, false, nil
	}
	if err != nil {
		return Channel{}, false, fmt.Errorf("channelstore: read channel %d: %w", channelID, err)
	}
	ch.AccountID = accountID
	return ch, true, nil
}

// GetByIDForAccount returns one channel by its local row id, but only when that
// row belongs to the named account.
//
// The local id is what a permission assignment and the Channel Explorer route
// name, so it is exactly the identifier that must not reach past its owner.
func (s *Store) GetByIDForAccount(ctx context.Context, accountID string, id int64) (Channel, bool, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || id <= 0 {
		return Channel{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE id = ? AND account_id = ?`, id, accountID)
	ch, err := s.scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, false, nil
	}
	if err != nil {
		return Channel{}, false, fmt.Errorf("channelstore: read channel row %d: %w", id, err)
	}
	ch.AccountID = accountID
	return ch, true, nil
}

// ListChannelsForAccount returns every channel one account registered, newest
// selection first.
func (s *Store) ListChannelsForAccount(ctx context.Context, accountID string) ([]Channel, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return []Channel{}, nil
	}
	rows, err := s.db.QueryContext(ctx,
		selectColumns+` WHERE account_id = ? ORDER BY created_at DESC, channel_id DESC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		ch, err := s.scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		ch.AccountID = accountID
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, nil
}

// ManagedIDsForAccount returns every channel id one account registered.
//
// It is what marks search results as locally selected for that account alone:
// a channel another user registered is not "managed" for this one.
func (s *Store) ManagedIDsForAccount(ctx context.Context, accountID string) (map[int64]bool, error) {
	accountID = strings.TrimSpace(accountID)
	out := map[int64]bool{}
	if accountID == "" {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id FROM managed_channels WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, nil
}

// AccountOfChannel reports which account one managed channel belongs to.
//
// It is the ownership trace the authorization pipeline needs before it can ask
// whether a caller may use a channel at all. It returns "" when no row has that
// local id.
func (s *Store) AccountOfChannel(ctx context.Context, localID int64) (string, error) {
	if localID <= 0 {
		return "", nil
	}
	var accountID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT account_id FROM managed_channels WHERE id = ?`, localID).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("channelstore: read channel row %d owner: %w", localID, err)
	}
	return accountID.String, nil
}

// ChannelsOwnedByAccounts returns the managed channels of the named accounts,
// in listing order.
//
// It is the batch form of ListChannelsForAccount, for a route that pages a
// catalogue across several accounts a caller owns.
func (s *Store) ChannelsOwnedByAccounts(ctx context.Context, accountIDs []string) ([]Channel, error) {
	out := []Channel{}
	ids := make([]string, 0, len(accountIDs))
	for _, id := range accountIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		selectColumns+` WHERE account_id IN (`+placeholders(len(ids))+`) ORDER BY channel_id DESC, id DESC`,
		toAnyStrings(ids)...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		ch, err := s.scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, nil
}

// toAnyStrings converts a slice of ids to the driver's parameter form.
func toAnyStrings(ids []string) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

// lastAccountWrite is the timestamp helper these writes share with Upsert.
var _ = time.Time{}
