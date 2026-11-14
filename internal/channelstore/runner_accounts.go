package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// runnerAccountColumns selects the persisted account fields the account
// supervisor needs. The associated session is ciphertext only; the store never
// opens it.
const runnerAccountColumns = `SELECT a.id, a.owner_user_id, a.server_key, a.network_type,
	a.remote_user_id, a.remote_username, a.remote_display_name, a.phone_number_masked,
	a.status, a.session_enc_version, a.session_nonce, a.session_ciphertext,
	a.session_enc_key_version, a.created_at, a.authorized_at, a.last_connected_at,
	a.last_disconnected_at, a.last_error_code, a.updated_at
	FROM telegram_accounts AS a
	JOIN users AS u ON u.id = a.owner_user_id`

// runnerEligiblePredicate is the complete persisted eligibility check for a
// normal account Runner. A server definition is deliberately not part of this
// query: the daemon must also resolve the account's server_key against its
// trusted current configuration before it starts a worker.
const runnerEligiblePredicate = `a.owner_user_id IS NOT NULL
	AND u.is_active = 1
	AND a.deleted_at IS NULL
	AND a.status IN (?, ?, ?, ?)
	AND a.remote_user_id > 0
	AND a.session_enc_version > 0
	AND a.session_nonce IS NOT NULL AND length(a.session_nonce) > 0
	AND a.session_ciphertext IS NOT NULL AND length(a.session_ciphertext) > 0`

func runnerStatuses() []any {
	return []any{
		AccountStatusConnected,
		AccountStatusDisconnected,
		AccountStatusReconnecting,
		AccountStatusError,
	}
}

// ListRunnerAccounts returns only active, owner-bound accounts with a
// server-confirmed remote identity and a persisted encrypted session. It is an
// internal daemon startup query, not an API listing: account ids and sealed
// session bytes must never be exposed by a public route.
//
// Connecting, authentication-required, disabled, removed and quarantined rows
// are not eligible. A missing or incomplete session is not treated as a fresh
// login; it is left for the lifecycle layer to mark as requiring
// authentication.
func (s *Store) ListRunnerAccounts(ctx context.Context) ([]Account, error) {
	args := runnerStatuses()
	rows, err := s.db.QueryContext(ctx, runnerAccountColumns+`
	WHERE `+runnerEligiblePredicate+`
	ORDER BY a.created_at, a.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list account Runners: %w", err)
	}
	defer rows.Close()

	out := []Account{}
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list account Runners: %w", err)
		}
		if err := validRunnerSession(account); err != nil {
			// Corrupt/incomplete persisted session data must not start a worker.
			// It remains visible to its owner for recovery, but cannot execute.
			continue
		}
		out = append(out, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list account Runners: %w", err)
	}
	return out, nil
}

// AccountForRunner resolves one account for the worker process named by
// account_id. The trusted daemon creates the argument from this database row;
// this lookup still enforces current ownership eligibility and never resolves a
// server key or legacy account as a personal identity.
func (s *Store) AccountForRunner(ctx context.Context, accountID string) (Account, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return Account{}, ErrAccountNotFound
	}
	args := runnerStatuses()
	queryArgs := append([]any{accountID}, args...)
	row := s.db.QueryRowContext(ctx, runnerAccountColumns+`
	WHERE a.id = ? AND `+runnerEligiblePredicate, queryArgs...)
	account, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("channelstore: load account Runner %s: %w", accountID, err)
	}
	if err := validRunnerSession(account); err != nil {
		return Account{}, ErrAccountNotFound
	}
	return account, nil
}

func validRunnerSession(account Account) error {
	if account.OwnerUserID <= 0 || account.RemoteUserID <= 0 {
		return fmt.Errorf("%w: account identity is incomplete", ErrInvalidAccount)
	}
	if account.Session.Version != 1 || len(account.Session.Nonce) == 0 || len(account.Session.Ciphertext) == 0 {
		return fmt.Errorf("%w: account has no persisted encrypted session", ErrInvalidAccount)
	}
	if account.SessionEncKeyVersion < 1 {
		return fmt.Errorf("%w: account session has no encryption key version", ErrInvalidAccount)
	}
	return nil
}
