package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Login attempt errors. Callers match them with errors.Is.
var (
	// ErrLoginAttemptNotFound means no attempt of that owner has that id. An
	// attempt belonging to another owner is this answer too: the id never
	// confirms that an attempt exists to someone who does not own it.
	ErrLoginAttemptNotFound = errors.New("channelstore: login attempt not found")
	// ErrLoginAttemptExists means an attempt id was used twice. Ids are
	// server-generated and never reused, so this is a bug rather than a user
	// error.
	ErrLoginAttemptExists = errors.New("channelstore: login attempt already exists")
	// ErrInvalidLoginAttempt means the record is not one this repository can
	// store.
	ErrInvalidLoginAttempt = errors.New("channelstore: invalid login attempt")
)

// Login methods, as the schema constrains them.
const (
	LoginMethodPhone = "phone"
	LoginMethodQR    = "qr"
)

// Login attempt states, as the schema constrains them.
const (
	LoginStateCreated            = "created"
	LoginStateConnecting         = "connecting"
	LoginStateWaitingForCode     = "waiting_for_code"
	LoginStateWaitingForPassword = "waiting_for_password"
	LoginStateWaitingForQR       = "waiting_for_qr"
	LoginStateAuthorized         = "authorized"
	LoginStateFailed             = "failed"
	LoginStateExpired            = "expired"
	LoginStateCanceled           = "canceled"
)

// LoginAttempt is one login in progress, or one that finished.
//
// It carries no secret of the user's: no code, no password, no session bytes.
// What it does carry is the state a user needs to see what to do next. The one
// token it holds, QRToken, is a short-lived login token — never logged, never
// in error text, and purged when the attempt ends.
//
// DeliveryHint is third-party text the server sent to describe the next
// delivery channel. It is untrusted: never rendered as HTML and never allowed
// to influence control flow. Delivery is what the server actually did, so a
// user is never told "SMS sent" because the code went some other way.
type LoginAttempt struct {
	ID          string
	OwnerUserID int64
	// AccountID is set once the attempt is authorized.
	AccountID   string
	ServerKey   string
	NetworkType string
	Method      string
	State       string

	// CodeSubmissions counts code and password submissions. It bounds a brute
	// force against one attempt and is never serialized to a client.
	CodeSubmissions int

	PhoneMasked  string
	Delivery     string
	DeliveryHint string
	// CodeLength is the code length the server asked for, when it said.
	CodeLength int
	// NextStep is the one action the user can take now.
	NextStep string
	// ErrorCode is the last failure, as a stable code.
	ErrorCode string

	QRToken     string
	QRExpiresAt time.Time

	// ExpiresAt bounds the attempt. Its zero value means the attempt is not
	// bounded, and a sweep never removes it.
	ExpiresAt time.Time
	// ResendAfter is when a new code may be requested again. Its zero value
	// means no such limit is standing.
	ResendAfter  time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	AuthorizedAt time.Time
}

// Terminal reports whether the attempt can change again.
func (a LoginAttempt) Terminal() bool {
	switch a.State {
	case LoginStateAuthorized, LoginStateFailed, LoginStateExpired, LoginStateCanceled:
		return true
	default:
		return false
	}
}

// LoginAttemptStore is the persistence seam for login attempts.
//
// It is deliberately minimal and owner-scoped on every read and write: a store
// that could answer for another owner would break the authorization contract.
// Which states a login may move between is the login service's decision, not
// this store's; what this store refuses is a record the schema does not define.
type LoginAttemptStore interface {
	// Create records a new attempt. The id is chosen by the caller.
	Create(ctx context.Context, attempt LoginAttempt) (LoginAttempt, error)
	// ByID returns the attempt only if owner owns it.
	ByID(ctx context.Context, ownerUserID int64, id string) (LoginAttempt, error)
	// UpdateState persists an attempt as it now stands. It refuses an attempt
	// the owner does not own.
	UpdateState(ctx context.Context, ownerUserID int64, attempt LoginAttempt) error
	// TouchResend sets the next time a code may be requested again.
	TouchResend(ctx context.Context, ownerUserID int64, id string, resendAfter time.Time) error
	// DeleteExpired removes attempts whose expiry has passed before at. It
	// returns how many were removed.
	DeleteExpired(ctx context.Context, at time.Time) (int, error)
	// DeleteByOwner removes every attempt of one owner. It returns how many
	// were removed.
	DeleteByOwner(ctx context.Context, ownerUserID int64) (int, error)
	// Incomplete lists the attempts that stopped halfway and have expired, so
	// a restart can release what they were holding. A finished attempt is never
	// among them: it is a record, not an orphan.
	Incomplete(ctx context.Context, at time.Time) ([]LoginAttempt, error)
}

// LoginAttempts is a Store's login attempts.
//
// They are a type of their own rather than methods of Store because both this
// store and AccountStore name a method Create, and Go has no overloading.
type LoginAttempts struct {
	store *Store
}

// LoginAttempts returns the login attempts recorded in this store.
func (s *Store) LoginAttempts() *LoginAttempts { return &LoginAttempts{store: s} }

var _ LoginAttemptStore = (*LoginAttempts)(nil)

// Create records a new attempt.
//
// The id is chosen by the login service and is never generated here, so one
// place in the system decides what an attempt is called.
func (l *LoginAttempts) Create(ctx context.Context, attempt LoginAttempt) (LoginAttempt, error) {
	attempt, err := validLoginAttempt(attempt)
	if err != nil {
		return LoginAttempt{}, err
	}
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = l.store.now().UTC()
	}
	if attempt.UpdatedAt.IsZero() {
		attempt.UpdatedAt = attempt.CreatedAt
	}

	_, err = l.store.db.ExecContext(ctx, `
INSERT INTO login_attempts (
	id, owner_user_id, account_id, server_key, network_type, method, state,
	phone_number_masked, delivery, delivery_hint, code_length, code_submissions,
	next_step, qr_token, qr_expires_at, resend_after, expires_at,
	last_error_code, created_at, updated_at, authorized_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.ID, attempt.OwnerUserID, nullableText(attempt.AccountID), attempt.ServerKey,
		attempt.NetworkType, attempt.Method, attempt.State,
		attempt.PhoneMasked, attempt.Delivery, attempt.DeliveryHint, attempt.CodeLength,
		attempt.CodeSubmissions, attempt.NextStep, attempt.QRToken,
		nullableTime(attempt.QRExpiresAt), nullableTime(attempt.ResendAfter),
		formatExpiry(attempt.ExpiresAt), attempt.ErrorCode, formatTime(attempt.CreatedAt),
		formatTime(attempt.UpdatedAt), nullableTime(attempt.AuthorizedAt),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return LoginAttempt{}, ErrLoginAttemptExists
		}
		return LoginAttempt{}, fmt.Errorf("channelstore: create login attempt %s: %w", attempt.ID, err)
	}
	return l.ByID(ctx, attempt.OwnerUserID, attempt.ID)
}

// ByID returns the attempt only if owner owns it.
func (l *LoginAttempts) ByID(ctx context.Context, ownerUserID int64, id string) (LoginAttempt, error) {
	row := l.store.db.QueryRowContext(ctx, selectLoginAttemptColumns+`
		WHERE id = ? AND owner_user_id = ?`, id, ownerUserID)
	return scanLoginAttemptOrMissing(row, id)
}

// UpdateState persists an attempt as it now stands.
//
// Everything the attempt carries is written, so a caller that read an attempt,
// changed one field and wrote it back never loses another. created_at is never
// rewritten: it records when the login began.
func (l *LoginAttempts) UpdateState(ctx context.Context, ownerUserID int64, attempt LoginAttempt) error {
	attempt, err := validLoginAttempt(attempt)
	if err != nil {
		return err
	}
	res, err := l.store.db.ExecContext(ctx, `
UPDATE login_attempts
SET account_id = ?, server_key = ?, network_type = ?, method = ?, state = ?,
	phone_number_masked = ?, delivery = ?, delivery_hint = ?, code_length = ?,
	code_submissions = ?, next_step = ?, qr_token = ?, qr_expires_at = ?,
	resend_after = ?, expires_at = ?, last_error_code = ?, updated_at = ?,
	authorized_at = ?
WHERE id = ? AND owner_user_id = ?`,
		nullableText(attempt.AccountID), attempt.ServerKey, attempt.NetworkType, attempt.Method,
		attempt.State, attempt.PhoneMasked, attempt.Delivery, attempt.DeliveryHint,
		attempt.CodeLength, attempt.CodeSubmissions, attempt.NextStep, attempt.QRToken,
		nullableTime(attempt.QRExpiresAt), nullableTime(attempt.ResendAfter),
		formatExpiry(attempt.ExpiresAt), attempt.ErrorCode, formatTime(l.store.now()),
		nullableTime(attempt.AuthorizedAt), attempt.ID, ownerUserID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrLoginAttemptExists
		}
		return fmt.Errorf("channelstore: update login attempt %s: %w", attempt.ID, err)
	}
	return requireLoginAttemptRow(res, attempt.ID)
}

// TouchResend sets the next time a code may be requested again.
//
// It moves no state: a rate limit is a timer, not a step of the login.
func (l *LoginAttempts) TouchResend(ctx context.Context, ownerUserID int64, id string, resendAfter time.Time) error {
	res, err := l.store.db.ExecContext(ctx,
		`UPDATE login_attempts SET resend_after = ?, updated_at = ? WHERE id = ? AND owner_user_id = ?`,
		nullableTime(resendAfter), formatTime(l.store.now()), id, ownerUserID)
	if err != nil {
		return fmt.Errorf("channelstore: touch login attempt %s resend: %w", id, err)
	}
	return requireLoginAttemptRow(res, id)
}

// DeleteExpired removes attempts whose expiry has passed before at.
//
// The comparison is on the parsed time rather than on the stored text: RFC3339
// prints a whole second without a fraction, and text ordering would then call
// "12:00:00Z" later than "12:00:00.5Z". An attempt with no expiry is not
// expired — it is unbounded — and is left alone. Removing the row is what purges
// its QR token.
func (l *LoginAttempts) DeleteExpired(ctx context.Context, at time.Time) (int, error) {
	res, err := l.store.db.ExecContext(ctx, `
DELETE FROM login_attempts WHERE expires_at <> '' AND julianday(expires_at) < julianday(?)`,
		formatTime(at))
	if err != nil {
		return 0, fmt.Errorf("channelstore: delete expired login attempts: %w", err)
	}
	return rowsChanged(res, "delete expired login attempts")
}

// DeleteByOwner removes every attempt of one owner.
func (l *LoginAttempts) DeleteByOwner(ctx context.Context, ownerUserID int64) (int, error) {
	res, err := l.store.db.ExecContext(ctx,
		`DELETE FROM login_attempts WHERE owner_user_id = ?`, ownerUserID)
	if err != nil {
		return 0, fmt.Errorf("channelstore: delete login attempts of owner %d: %w", ownerUserID, err)
	}
	return rowsChanged(res, "delete login attempts")
}

// Incomplete lists the attempts that stopped halfway and have expired.
//
// They are the ones a restart has to clean up before it can say no permanent
// session was left behind by a login that never finished.
func (l *LoginAttempts) Incomplete(ctx context.Context, at time.Time) ([]LoginAttempt, error) {
	rows, err := l.store.db.QueryContext(ctx, selectLoginAttemptColumns+`
		WHERE state NOT IN (?, ?, ?, ?)
			AND expires_at <> '' AND julianday(expires_at) < julianday(?)
		ORDER BY created_at, id`,
		LoginStateAuthorized, LoginStateFailed, LoginStateExpired, LoginStateCanceled,
		formatTime(at))
	if err != nil {
		return nil, fmt.Errorf("channelstore: list incomplete login attempts: %w", err)
	}
	defer rows.Close()
	out := []LoginAttempt{}
	for rows.Next() {
		rec, err := scanLoginAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list incomplete login attempts: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list incomplete login attempts: %w", err)
	}
	return out, nil
}

const selectLoginAttemptColumns = `SELECT id, owner_user_id, account_id, server_key, network_type,
	method, state, phone_number_masked, delivery, delivery_hint, code_length,
	code_submissions, next_step, qr_token, qr_expires_at, resend_after, expires_at,
	last_error_code, created_at, updated_at, authorized_at FROM login_attempts`

func scanLoginAttempt(row scanner) (LoginAttempt, error) {
	var (
		a          LoginAttempt
		accountID  sql.NullString
		qrExpires  sql.NullString
		resend     sql.NullString
		authorized sql.NullString
		expires    string
		createdAt  string
		updated    string
	)
	if err := row.Scan(&a.ID, &a.OwnerUserID, &accountID, &a.ServerKey, &a.NetworkType,
		&a.Method, &a.State, &a.PhoneMasked, &a.Delivery, &a.DeliveryHint, &a.CodeLength,
		&a.CodeSubmissions, &a.NextStep, &a.QRToken, &qrExpires, &resend, &expires,
		&a.ErrorCode, &createdAt, &updated, &authorized); err != nil {
		return LoginAttempt{}, err
	}
	a.AccountID = accountID.String
	a.QRExpiresAt = parseTime(qrExpires.String)
	a.ResendAfter = parseTime(resend.String)
	a.ExpiresAt = parseTime(expires)
	a.CreatedAt = parseTime(createdAt)
	a.UpdatedAt = parseTime(updated)
	a.AuthorizedAt = parseTime(authorized.String)
	return a, nil
}

func scanLoginAttemptOrMissing(row scanner, id string) (LoginAttempt, error) {
	rec, err := scanLoginAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginAttempt{}, ErrLoginAttemptNotFound
	}
	if err != nil {
		return LoginAttempt{}, fmt.Errorf("channelstore: read login attempt %s: %w", id, err)
	}
	return rec, nil
}

func requireLoginAttemptRow(res sql.Result, id string) error {
	if changed, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("channelstore: write login attempt %s: %w", id, err)
	} else if changed == 0 {
		return ErrLoginAttemptNotFound
	}
	return nil
}

func rowsChanged(res sql.Result, what string) (int, error) {
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("channelstore: %s: %w", what, err)
	}
	return int(changed), nil
}

// validLoginAttempt checks what the repository can check: the record names an
// id, an owner, a server, and a method and a state the schema defines.
//
// Which states a login may move between is the login service's decision. This
// store only refuses a state no login can ever be in. Nothing here names a QR
// token or a delivery hint, so neither can reach a caller through an error.
func validLoginAttempt(a LoginAttempt) (LoginAttempt, error) {
	a.ID = strings.TrimSpace(a.ID)
	a.ServerKey = strings.TrimSpace(a.ServerKey)
	a.Method = strings.TrimSpace(a.Method)
	a.State = strings.TrimSpace(a.State)
	a.NextStep = strings.TrimSpace(a.NextStep)
	if a.ID == "" {
		return LoginAttempt{}, fmt.Errorf("%w: id is required", ErrInvalidLoginAttempt)
	}
	if a.OwnerUserID <= 0 {
		return LoginAttempt{}, fmt.Errorf("%w: an owner is required", ErrInvalidLoginAttempt)
	}
	if a.ServerKey == "" {
		return LoginAttempt{}, fmt.Errorf("%w: a server key is required", ErrInvalidLoginAttempt)
	}
	if !validLoginMethod(a.Method) {
		return LoginAttempt{}, fmt.Errorf("%w: unsupported method %q", ErrInvalidLoginAttempt, a.Method)
	}
	if !validLoginState(a.State) {
		return LoginAttempt{}, fmt.Errorf("%w: unsupported state %q", ErrInvalidLoginAttempt, a.State)
	}
	if a.CodeSubmissions < 0 || a.CodeLength < 0 {
		return LoginAttempt{}, fmt.Errorf("%w: a count cannot be negative", ErrInvalidLoginAttempt)
	}
	return a, nil
}

func validLoginMethod(method string) bool {
	return method == LoginMethodPhone || method == LoginMethodQR
}

func validLoginState(state string) bool {
	switch state {
	case LoginStateCreated, LoginStateConnecting, LoginStateWaitingForCode,
		LoginStateWaitingForPassword, LoginStateWaitingForQR, LoginStateAuthorized,
		LoginStateFailed, LoginStateExpired, LoginStateCanceled:
		return true
	default:
		return false
	}
}

// formatExpiry stores an attempt's expiry, and says "unbounded" for a zero time
// rather than writing a date that would sweep the attempt immediately.
func formatExpiry(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatTime(t)
}

// nullableText stores an empty value as NULL, so a column that names another
// row never holds a name that matches nothing.
func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
