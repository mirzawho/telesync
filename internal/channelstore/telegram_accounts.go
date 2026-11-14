package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/secrets"
)

// Account errors. Callers match them with errors.Is.
var (
	// ErrAccountNotFound means no account of that owner has that id. An account
	// belonging to another owner, and a quarantined account that belongs to no
	// one, are this answer too: an id never confirms that an account exists to
	// someone it is not theirs to know about.
	ErrAccountNotFound = errors.New("channelstore: telegram account not found")
	// ErrDuplicateAccount means the owner already holds that remote identity on
	// that server.
	ErrDuplicateAccount = errors.New("channelstore: that remote account is already registered for this owner")
	// ErrInvalidAccount means the record is not one this repository can store.
	ErrInvalidAccount = errors.New("channelstore: invalid telegram account")
)

// Network types: which kind of connection the account authenticates over.
const (
	NetworkTelegram = "telegram"
	NetworkCustom   = "custom"
)

// Account status values, as the schema constrains them.
const (
	AccountStatusConnecting             = "connecting"
	AccountStatusConnected              = "connected"
	AccountStatusDisconnected           = "disconnected"
	AccountStatusReconnecting           = "reconnecting"
	AccountStatusAuthenticationRequired = "authentication_required"
	AccountStatusDisabled               = "disabled"
	AccountStatusError                  = "error"
	AccountStatusRemoved                = "removed"
	// AccountStatusQuarantined is a legacy central session whose owner is not
	// known. It holds channels and nothing else: no worker runs for it, no user
	// sees it in their accounts, and it becomes a real account only when an
	// operator adopts it and says who owns it.
	AccountStatusQuarantined = "quarantined"
)

// Account is one user-owned authenticated Telegram account.
//
// ID is the authoritative session identity: opaque, stable, never reused, and
// never a username, a phone number or a server key. ServerKey is the server
// definition it connects through, which is not an identity.
//
// OwnerUserID is zero for a quarantined account, which is the one case where an
// account has no owner.
type Account struct {
	ID, ServerKey, NetworkType        string
	OwnerUserID                       int64
	RemoteUserID                      int64
	RemoteUsername, RemoteDisplayName string
	PhoneNumberMasked                 string
	Status                            string
	// Session is the sealed session box. Its zero value means there is none.
	Session                             secrets.Box
	SessionEncKeyVersion                int
	CreatedAt, AuthorizedAt             time.Time
	LastConnectedAt, LastDisconnectedAt time.Time
	LastErrorCode                       string
}

// AccountIdentity is one confirmed remote identity and the user it was
// confirmed for.
//
// It is built from the server's own authorization response, never from a
// client-supplied field: the owner comes from the authenticated principal and
// the remote identity from what the server said.
type AccountIdentity struct {
	OwnerUserID       int64
	ServerKey         string
	NetworkType       string
	RemoteUserID      int64
	RemoteUsername    string
	RemoteDisplayName string
	PhoneNumberMasked string
}

// AccountStore records the accounts a user owns.
//
// Every lookup is scoped to its owner and skips quarantined accounts: an
// account is what a user acts as, so answering for another owner would hand
// that user someone else's session, and answering with a quarantined account
// would hand them a shared legacy one.
type AccountStore interface {
	Create(ctx context.Context, a Account) (Account, error)
	// Ensure returns the account id of the owner's account for this remote
	// identity on this server, creating the record the first time it is seen.
	//
	// It never overwrites an already-authorized account with a newer login: an
	// existing account is returned as it stands.
	Ensure(ctx context.Context, identity AccountIdentity) (string, error)
	// DropIncomplete removes the account this identity was being recorded
	// under when the login did not finish. An account that holds a session is
	// never touched.
	DropIncomplete(ctx context.Context, identity AccountIdentity) error
	ByID(ctx context.Context, ownerUserID int64, id string) (Account, error)
	ListByOwner(ctx context.Context, ownerUserID int64) ([]Account, error)
	UpdateSession(ctx context.Context, ownerUserID int64, id string, box secrets.Box, keyVersion int) error
	SetStatus(ctx context.Context, ownerUserID int64, id, status, errorCode string) error
	TouchConnected(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, ownerUserID int64, id string) error
	// ResolveLegacy maps one legacy client key to the account that holds its
	// channels, or "" when there is none. It is the only way to reach a
	// quarantined account.
	ResolveLegacy(ctx context.Context, legacyKey string) (string, error)
}

var _ AccountStore = (*Store)(nil)

// CreatePending records the permanent account identity before login begins.
//
// It has no remote identity or session yet, so it is not eligible for worker
// routing. The same opaque id is used by the login attempt, session box and
// eventual worker.
func (s *Store) CreatePending(ctx context.Context, ownerUserID int64, id, serverKey, networkType string) error {
	_, err := s.Create(ctx, Account{
		ID:          id,
		OwnerUserID: ownerUserID,
		ServerKey:   serverKey,
		NetworkType: networkType,
		Status:      AccountStatusConnecting,
	})
	return err
}

// CompletePending binds the remote identity confirmed by Telegram to the
// account reserved for this login. It will not replace a connected account.
func (s *Store) CompletePending(ctx context.Context, ownerUserID int64, id string, user accountauth.AuthorizedUser) error {
	if ownerUserID <= 0 || strings.TrimSpace(id) == "" || user.RemoteUserID <= 0 {
		return fmt.Errorf("%w: owner, account id and confirmed remote identity are required", ErrInvalidAccount)
	}
	displayName := strings.TrimSpace(strings.TrimSpace(user.RemoteFirstName) + " " + strings.TrimSpace(user.RemoteLastName))
	res, err := s.db.ExecContext(ctx, `
UPDATE telegram_accounts
SET remote_user_id = ?, remote_username = ?, remote_display_name = ?,
	phone_number_masked = ?, status = ?, authorized_at = ?, updated_at = ?
WHERE id = ? AND owner_user_id = ? AND status = ? AND remote_user_id = 0 AND deleted_at IS NULL`,
		user.RemoteUserID, user.RemoteUsername, displayName, user.PhoneMasked,
		AccountStatusConnected, formatTime(s.now()), formatTime(s.now()),
		id, ownerUserID, AccountStatusConnecting)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicateAccount
		}
		return fmt.Errorf("channelstore: complete pending telegram account: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrAccountNotFound
	}
	return nil
}

// ReleasePending retires a login's unconfirmed account identity.
//
// The WHERE clause makes cleanup harmless if authorization completed before a
// concurrent terminal transition.
func (s *Store) ReleasePending(ctx context.Context, ownerUserID int64, id string) error {
	now := formatTime(s.now())
	_, err := s.db.ExecContext(ctx, `
UPDATE telegram_accounts
SET status = ?, deleted_at = ?, updated_at = ?
WHERE id = ? AND owner_user_id = ? AND status = ? AND remote_user_id = 0 AND deleted_at IS NULL`,
		AccountStatusRemoved, now, now, id, ownerUserID, AccountStatusConnecting)
	if err != nil {
		return fmt.Errorf("channelstore: release pending telegram account: %w", err)
	}
	return nil
}

// Create records one account.
//
// The id is chosen by the caller — the login service generates it, so no client
// ever sees it chosen — and is never rewritten here. Two owners holding the
// same remote identity on the same server is allowed and is two accounts with
// two sessions; one owner holding it twice is not.
func (s *Store) Create(ctx context.Context, a Account) (Account, error) {
	a, err := validAccount(a)
	if err != nil {
		return Account{}, err
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = s.now().UTC()
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO telegram_accounts (
	id, owner_user_id, server_key, network_type,
	remote_user_id, remote_username, remote_display_name, phone_number_masked,
	status,
	session_enc_version, session_nonce, session_ciphertext, session_enc_key_version,
	created_at, authorized_at, last_connected_at, last_disconnected_at,
	last_error_code, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.OwnerUserID, a.ServerKey, a.NetworkType,
		a.RemoteUserID, a.RemoteUsername, a.RemoteDisplayName, a.PhoneNumberMasked,
		a.Status,
		a.Session.Version, a.Session.Nonce, a.Session.Ciphertext, a.SessionEncKeyVersion,
		formatTime(a.CreatedAt), nullableTime(a.AuthorizedAt), nullableTime(a.LastConnectedAt),
		nullableTime(a.LastDisconnectedAt), a.LastErrorCode, formatTime(s.now()),
	)
	if err != nil {
		if isUniqueViolation(err) {
			// The primary key and the owner-scoped remote identity index are
			// both UNIQUE. Reusing an id is a bug rather than a second account:
			// ids are opaque, stable and never reused.
			if strings.Contains(err.Error(), "telegram_accounts.id") {
				return Account{}, fmt.Errorf("%w: the account id is already used", ErrInvalidAccount)
			}
			return Account{}, ErrDuplicateAccount
		}
		return Account{}, fmt.Errorf("channelstore: create telegram account %s: %w", a.ID, err)
	}
	return s.ByID(ctx, a.OwnerUserID, a.ID)
}

// Ensure returns the account of one owner for one confirmed remote identity,
// creating it the first time it is seen.
//
// An account that already exists is returned as it stands: a later login never
// overwrites an account that is already authorized, so a second sign-in cannot
// replace the first one's session or its history.
func (s *Store) Ensure(ctx context.Context, identity AccountIdentity) (string, error) {
	if identity.OwnerUserID <= 0 {
		return "", fmt.Errorf("%w: an owner is required", ErrInvalidAccount)
	}
	if strings.TrimSpace(identity.ServerKey) == "" || identity.RemoteUserID <= 0 {
		return "", fmt.Errorf("%w: a server and a remote identity are required", ErrInvalidAccount)
	}

	existing, err := s.accountForRemoteIdentity(ctx, identity.OwnerUserID, identity.ServerKey, identity.RemoteUserID)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.ID, nil
	}

	id, err := newAccountID()
	if err != nil {
		return "", err
	}
	a := Account{
		ID:                id,
		OwnerUserID:       identity.OwnerUserID,
		ServerKey:         identity.ServerKey,
		NetworkType:       identity.NetworkType,
		RemoteUserID:      identity.RemoteUserID,
		RemoteUsername:    identity.RemoteUsername,
		RemoteDisplayName: identity.RemoteDisplayName,
		PhoneNumberMasked: identity.PhoneNumberMasked,
		Status:            AccountStatusConnected,
		AuthorizedAt:      s.now().UTC(),
	}
	created, err := s.Create(ctx, a)
	if err != nil {
		if !errors.Is(err, ErrDuplicateAccount) {
			return "", err
		}
		// Another login confirmed the same identity first: that account wins.
		if existing, err := s.accountForRemoteIdentity(ctx, identity.OwnerUserID, identity.ServerKey, identity.RemoteUserID); err != nil {
			return "", err
		} else if existing != nil {
			return existing.ID, nil
		}
		return "", ErrDuplicateAccount
	}
	return created.ID, nil
}

// DropIncomplete removes the account one identity was being recorded under when
// the login did not finish.
//
// An account that never got as far as storing a session cannot act and is an
// orphan of a half-completed login, so it is dropped rather than left behind by
// a restart. An account that holds a session is never touched, and neither is
// one that belongs to someone else. Dropping is the same soft removal as
// Delete, so the identity is free again and the id stays retired.
func (s *Store) DropIncomplete(ctx context.Context, identity AccountIdentity) error {
	existing, err := s.accountForRemoteIdentity(ctx, identity.OwnerUserID, identity.ServerKey, identity.RemoteUserID)
	if err != nil {
		return err
	}
	if existing == nil || !isEmptyBox(existing.Session) {
		return nil
	}
	return s.Delete(ctx, identity.OwnerUserID, existing.ID)
}

// accountForRemoteIdentity finds the one account of one owner for a remote
// identity, or nil when there is none.
func (s *Store) accountForRemoteIdentity(ctx context.Context, ownerUserID int64, serverKey string, remoteUserID int64) (*Account, error) {
	row := s.db.QueryRowContext(ctx, selectAccountColumns+`
		WHERE owner_user_id = ? AND server_key = ? AND remote_user_id = ?
			AND deleted_at IS NULL AND status != ?
		ORDER BY created_at, id LIMIT 1`,
		ownerUserID, serverKey, remoteUserID, AccountStatusQuarantined)
	rec, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("channelstore: read telegram account of remote user %d: %w", remoteUserID, err)
	}
	return &rec, nil
}

// ByID returns one account of one owner.
//
// The owner is part of the identity of the lookup, never a filter applied
// afterwards: an account belonging to someone else, or to nobody, is not found.
func (s *Store) ByID(ctx context.Context, ownerUserID int64, id string) (Account, error) {
	row := s.db.QueryRowContext(ctx, selectAccountColumns+`
		WHERE id = ? AND owner_user_id = ? AND deleted_at IS NULL AND status != ?`,
		id, ownerUserID, AccountStatusQuarantined)
	return scanAccountOrMissing(row, id)
}

// ListByOwner returns every account one owner holds, newest first.
func (s *Store) ListByOwner(ctx context.Context, ownerUserID int64) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, selectAccountColumns+`
		WHERE owner_user_id = ? AND deleted_at IS NULL AND status != ?
		ORDER BY created_at DESC, id DESC`, ownerUserID, AccountStatusQuarantined)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list telegram accounts: %w", err)
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		rec, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list telegram accounts: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list telegram accounts: %w", err)
	}
	return out, nil
}

// UpdateSession replaces the sealed session of one account of one owner.
//
// The box is sealed before it gets here and is never opened by this package. A
// zero box is refused rather than stored: it says "no session", which is not a
// session that can be sealed.
func (s *Store) UpdateSession(ctx context.Context, ownerUserID int64, id string, box secrets.Box, keyVersion int) error {
	if err := validSessionBox(box, keyVersion); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE telegram_accounts
SET session_enc_version = ?, session_nonce = ?, session_ciphertext = ?,
	session_enc_key_version = ?, updated_at = ?
WHERE id = ? AND owner_user_id = ? AND deleted_at IS NULL AND status != ?`,
		box.Version, box.Nonce, box.Ciphertext, keyVersion, formatTime(s.now()), id, ownerUserID,
		AccountStatusQuarantined)
	if err != nil {
		return fmt.Errorf("channelstore: write telegram account %s session: %w", id, err)
	}
	return requireAccountRow(res, id)
}

// ClearSession removes one owner's sealed session while retaining the account
// record and its remote identity. Logout/removal calls this only after the
// account Runner has stopped and flushed its session.
func (s *Store) ClearSession(ctx context.Context, ownerUserID int64, id string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE telegram_accounts
SET session_enc_version = 0, session_nonce = NULL, session_ciphertext = NULL,
	updated_at = ?
WHERE id = ? AND owner_user_id = ? AND status != ?`,
		formatTime(s.now()), id, ownerUserID, AccountStatusQuarantined)
	if err != nil {
		return fmt.Errorf("channelstore: clear telegram account %s session: %w", id, err)
	}
	return requireAccountRow(res, id)
}

// SetStatus records the connection state of one account of one owner.
func (s *Store) SetStatus(ctx context.Context, ownerUserID int64, id, status, errorCode string) error {
	status = strings.TrimSpace(status)
	if !validAccountStatus(status) {
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidAccount, status)
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE telegram_accounts SET status = ?, last_error_code = ?, updated_at = ?
WHERE id = ? AND owner_user_id = ? AND deleted_at IS NULL AND status != ?`,
		status, errorCode, formatTime(s.now()), id, ownerUserID, AccountStatusQuarantined)
	if err != nil {
		return fmt.Errorf("channelstore: write telegram account %s status: %w", id, err)
	}
	return requireAccountRow(res, id)
}

// TouchConnected records when an account last connected.
//
// The v0.0.5 contract's AccountStore gives this method the account id alone, so
// it is the one write here that is not scoped to an owner.
func (s *Store) TouchConnected(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE telegram_accounts SET last_connected_at = ?, updated_at = ?
WHERE id = ? AND deleted_at IS NULL AND status != ?`,
		nullableTime(at), formatTime(s.now()), id, AccountStatusQuarantined)
	if err != nil {
		return fmt.Errorf("channelstore: write telegram account %s connection: %w", id, err)
	}
	return requireAccountRow(res, id)
}

// Delete removes one account of one owner.
//
// The row is marked gone rather than erased, for two reasons: the id stays
// retired so it is never reused, and erasing it would take the channels,
// permissions and automation that reference it with it through ON DELETE
// CASCADE — resources that move only once their new ownership is established.
// Marking it gone also frees the owner's remote identity, so the same account
// can be signed in to again.
func (s *Store) Delete(ctx context.Context, ownerUserID int64, id string) error {
	now := formatTime(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: delete telegram account %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
UPDATE telegram_accounts SET deleted_at = ?, status = ?, updated_at = ?
WHERE id = ? AND owner_user_id = ? AND deleted_at IS NULL AND status != ?`,
		now, AccountStatusRemoved, now, id, ownerUserID, AccountStatusQuarantined)
	if err != nil {
		return fmt.Errorf("channelstore: delete telegram account %s: %w", id, err)
	}
	if err := requireAccountRow(res, id); err != nil {
		return err
	}
	// The account's work is paused rather than dropped or left runnable. A
	// queued send that still names a removed account is work nothing may
	// execute: running it would use a session that is gone, and deleting the row
	// would lose an operation somebody is still waiting for. Paused is the
	// honest state — held, visible, and resumable if the account comes back.
	if _, err := tx.ExecContext(ctx, `
UPDATE automation_jobs SET paused = 1, updated_at = ?
WHERE account_id = ? AND status IN ('pending', 'retry_wait', 'processing')`, now, id); err != nil {
		return fmt.Errorf("channelstore: pause the work of removed account %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: delete telegram account %s: %w", id, err)
	}
	return nil
}

// ResolveLegacy maps one legacy client key to the account that holds its
// channels.
//
// A quarantined account is reachable here and nowhere else: no user's account
// listing shows it. An adoption recorded by an operator takes precedence, so a
// legacy session an operator moved to a real account resolves to that account.
// It reports "" when nothing has ever been registered under the key.
func (s *Store) ResolveLegacy(ctx context.Context, legacyKey string) (string, error) {
	legacyKey = strings.TrimSpace(legacyKey)
	if legacyKey == "" {
		return "", errors.New("channelstore: a legacy key is required")
	}
	var adopted sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT account_id FROM legacy_session_adoptions
WHERE legacy_key = ? ORDER BY adopted_at DESC, rowid DESC LIMIT 1`, legacyKey).Scan(&adopted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("channelstore: resolve legacy key %s: %w", legacyKey, err)
	}
	if err == nil {
		return adopted.String, nil
	}

	var quarantined sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM telegram_accounts WHERE id = ?`, legacyAccountID(legacyKey)).Scan(&quarantined)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("channelstore: resolve legacy key %s: %w", legacyKey, err)
	}
	return quarantined.String, nil
}

// RecordLegacyAdoption records that an operator adopted one legacy central
// session.
//
// The row is the audit record of a decision no code makes on its own: adopting
// a session means telling the account who owns it, and this says who decided
// and why. A legacy key may be adopted more than once, so the record keeps what
// happened rather than only what stands now.
func (s *Store) RecordLegacyAdoption(ctx context.Context, legacyKey, accountID string, adoptedBy int64, reason string) error {
	legacyKey = strings.TrimSpace(legacyKey)
	accountID = strings.TrimSpace(accountID)
	reason = strings.TrimSpace(reason)
	if legacyKey == "" || accountID == "" || adoptedBy <= 0 || reason == "" {
		return errors.New("channelstore: an adoption requires a legacy key, account, operator, and reason")
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO legacy_session_adoptions (legacy_key, account_id, adopted_by, adopted_at, reason)
SELECT ?, a.id, u.id, ?, ?
FROM telegram_accounts AS a CROSS JOIN users AS u
WHERE a.id = ? AND a.owner_user_id IS NOT NULL AND a.status != ?
	AND a.deleted_at IS NULL AND u.id = ? AND u.is_active = 1`,
		legacyKey, formatTime(s.now()), reason, accountID, AccountStatusQuarantined, adoptedBy)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("channelstore: legacy key %s is already adopted by account %s", legacyKey, accountID)
		}
		return fmt.Errorf("channelstore: record legacy adoption %s: %w", legacyKey, err)
	}
	if changed, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("channelstore: record legacy adoption %s: %w", legacyKey, err)
	} else if changed == 0 {
		return errors.New("channelstore: adoption target account or active operator not found")
	}
	return nil
}

// legacyAccountID names the quarantined account that holds the channels of one
// legacy central session.
//
// The name is what an operator sees when they come to adopt a legacy session.
// It names the session's server, never a username and never a phone number.
func legacyAccountID(clientKey string) string { return "legacy_" + clientKey }

// accountIDPrefix marks an account id.
const accountIDPrefix = accountid.Prefix

// newAccountID returns the id of a newly recorded account.
//
// Ensure draws one here because it is this package that decides the account
// record exists at all; a caller that names its own account passes the id to
// Create instead. The draw itself lives in `internal/accountid`, shared with
// the login flow that also mints one, so the two can never drift apart in
// alphabet or length.
func newAccountID() (string, error) { return accountid.New() }

// accountWriter is what both *sql.DB and *sql.Tx offer for a write.
type accountWriter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ensureLegacyAccount returns the account that holds the channels of one legacy
// client key, recording its quarantined account the first time the key is used.
//
// A channel written without an account is a channel of the shared legacy
// session of that server. Recording it as quarantined is what keeps it out of
// every user's account listing until an operator says who owns it.
func (s *Store) ensureLegacyAccount(ctx context.Context, w accountWriter, clientKey string) (string, error) {
	id := legacyAccountID(clientKey)
	now := formatTime(s.now())
	_, err := w.ExecContext(ctx, `
INSERT INTO telegram_accounts (
	id, owner_user_id, server_key, network_type, remote_user_id,
	remote_username, remote_display_name, phone_number_masked, status,
	session_enc_version, session_enc_key_version, session_storage_ref,
	last_error_code, created_at, updated_at)
SELECT ?, NULL, ?, '', 0, '', '', '', ?, 0, 1, '', '', ?, ?
WHERE NOT EXISTS (SELECT 1 FROM telegram_accounts WHERE id = ?)`,
		id, clientKey, AccountStatusQuarantined, now, now, id)
	if err != nil {
		return "", fmt.Errorf("channelstore: record the legacy account of %s: %w", clientKey, err)
	}
	return id, nil
}

const selectAccountColumns = `SELECT id, owner_user_id, server_key, network_type,
	remote_user_id, remote_username, remote_display_name, phone_number_masked,
	status,
	session_enc_version, session_nonce, session_ciphertext, session_enc_key_version,
	created_at, authorized_at, last_connected_at, last_disconnected_at,
	last_error_code, updated_at FROM telegram_accounts`

func scanAccount(row scanner) (Account, error) {
	var (
		a                                   Account
		owner                               sql.NullInt64
		nonce, ciphertext                   []byte
		authorized, connected, disconnected sql.NullString
		created, updated                    string
	)
	if err := row.Scan(&a.ID, &owner, &a.ServerKey, &a.NetworkType,
		&a.RemoteUserID, &a.RemoteUsername, &a.RemoteDisplayName, &a.PhoneNumberMasked,
		&a.Status,
		&a.Session.Version, &nonce, &ciphertext, &a.SessionEncKeyVersion,
		&created, &authorized, &connected, &disconnected,
		&a.LastErrorCode, &updated); err != nil {
		return Account{}, err
	}
	a.OwnerUserID = owner.Int64
	a.Session.Nonce = nonce
	a.Session.Ciphertext = ciphertext
	a.CreatedAt = parseTime(created)
	a.AuthorizedAt = parseTime(authorized.String)
	a.LastConnectedAt = parseTime(connected.String)
	a.LastDisconnectedAt = parseTime(disconnected.String)
	return a, nil
}

// scanAccountOrMissing turns "there is no such account of this owner" into the
// one answer a caller may act on: it is not theirs to know about.
func scanAccountOrMissing(row scanner, id string) (Account, error) {
	rec, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("channelstore: read telegram account %s: %w", id, err)
	}
	return rec, nil
}

// requireAccountRow reports a write that named an account this owner does not
// hold as "not found", never as a silent success.
func requireAccountRow(res sql.Result, id string) error {
	if changed, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("channelstore: write telegram account %s: %w", id, err)
	} else if changed == 0 {
		return ErrAccountNotFound
	}
	return nil
}

// validAccount checks what the repository can check without a key: the record
// names an owner, a server and a status the schema defines, and any session it
// carries is a complete sealed box.
func validAccount(a Account) (Account, error) {
	a.ID = strings.TrimSpace(a.ID)
	a.ServerKey = strings.TrimSpace(a.ServerKey)
	a.NetworkType = strings.TrimSpace(a.NetworkType)
	a.Status = strings.TrimSpace(a.Status)
	if a.ID == "" {
		return Account{}, fmt.Errorf("%w: id is required", ErrInvalidAccount)
	}
	if a.OwnerUserID <= 0 {
		return Account{}, fmt.Errorf("%w: an owner is required", ErrInvalidAccount)
	}
	if a.ServerKey == "" {
		return Account{}, fmt.Errorf("%w: a server key is required", ErrInvalidAccount)
	}
	if a.NetworkType == "" {
		return Account{}, fmt.Errorf("%w: a network type is required", ErrInvalidAccount)
	}
	if a.RemoteUserID < 0 {
		return Account{}, fmt.Errorf("%w: a remote user id cannot be negative", ErrInvalidAccount)
	}
	if !validAccountStatus(a.Status) {
		return Account{}, fmt.Errorf("%w: unsupported status %q", ErrInvalidAccount, a.Status)
	}
	if a.Status == AccountStatusQuarantined {
		return Account{}, fmt.Errorf("%w: a quarantined account is recorded by the migration, not created here", ErrInvalidAccount)
	}
	if !isEmptyBox(a.Session) {
		if err := validSessionBox(a.Session, a.SessionEncKeyVersion); err != nil {
			return Account{}, err
		}
	}
	return a, nil
}

// validSessionBox checks that a session is stored sealed, as the WordPress
// credentials are, and that the key version it was sealed with is named.
func validSessionBox(box secrets.Box, keyVersion int) error {
	if isEmptyBox(box) {
		return fmt.Errorf("%w: there is no session to store", ErrInvalidAccount)
	}
	if box.Version != secrets.Version1 {
		return fmt.Errorf("%w: the session must be sealed (version %d)", ErrInvalidAccount, secrets.Version1)
	}
	if len(box.Nonce) == 0 || len(box.Ciphertext) == 0 {
		return fmt.Errorf("%w: the sealed session is incomplete", ErrInvalidAccount)
	}
	if keyVersion < 1 {
		return fmt.Errorf("%w: the session needs the key version it was sealed with", ErrInvalidAccount)
	}
	return nil
}

func validAccountStatus(status string) bool {
	switch status {
	case AccountStatusConnecting, AccountStatusConnected, AccountStatusDisconnected,
		AccountStatusReconnecting, AccountStatusAuthenticationRequired,
		AccountStatusDisabled, AccountStatusError, AccountStatusRemoved,
		AccountStatusQuarantined:
		return true
	default:
		return false
	}
}
