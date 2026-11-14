// Package accountauth runs the per-account Telegram/Cat login state machine.
//
// Telesync web authentication and MTProto authentication are two separate
// systems. This package owns only the second: one user signing one Telegram
// account into one server definition, as a bounded, persisted, owner-scoped
// attempt — never as a goroutine holding the user's secrets.
//
// Two rules are absolute here. A 2FA password and a login code are transient:
// they are passed to gotd's SRP helpers and then dropped, never persisted,
// logged or returned. And the login delivery method is reported exactly as the
// server described it, because Telegram delivers codes over SMS, phone calls,
// flash calls, its own app, email and more.
package accountauth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Attempt states, exactly as the v0.0.5 login contract defines them.
const (
	StateCreated            = "created"
	StateConnecting         = "connecting"
	StateWaitingForCode     = "waiting_for_code"
	StateWaitingForPassword = "waiting_for_password"
	StateWaitingForQR       = "waiting_for_qr"
	StateAuthorized         = "authorized"
	StateFailed             = "failed"
	StateExpired            = "expired"
	StateCanceled           = "canceled"
)

// Login methods.
const (
	MethodPhone = "phone"
	MethodQR    = "qr"
)

// Delivery methods. These mirror the ways Telegram actually delivers a login
// code; "sms" is one of them and is never assumed.
const (
	DeliveryUnknown     = "unknown"
	DeliveryTelegramApp = "app"
	DeliverySMS         = "sms"
	DeliveryCall        = "call"
	DeliveryFlashCall   = "flash_call"
	DeliveryMissedCall  = "missed_call"
	DeliveryEmail       = "email"
	DeliveryFragmentSMS = "fragment_sms"
	DeliveryFirebaseSMS = "firebase_sms"
	DeliverySMSWord     = "sms_word"
	DeliverySMSPhrase   = "sms_phrase"
)

// Sentinel errors, matched with errors.Is. They map to the error codes the
// account API contract names; the gateway turns them into responses.
var (
	// ErrNotFound means no such attempt for that owner. It is also the answer
	// for an attempt owned by someone else: an owner oracle is not exposed.
	ErrNotFound = errors.New("accountauth: login attempt not found")
	// ErrAttemptExpired means the attempt's TTL passed.
	ErrAttemptExpired = errors.New("accountauth: login attempt expired")
	// ErrAttemptFinished means the attempt reached a terminal state and cannot
	// be driven again.
	ErrAttemptFinished = errors.New("accountauth: login attempt is finished")
	// ErrInvalidTransition means the requested step does not apply in the
	// attempt's current state.
	ErrInvalidTransition = errors.New("accountauth: invalid login attempt transition")
	// ErrRateLimited means too many code submissions for one attempt.
	ErrRateLimited = errors.New("accountauth: too many code submissions")
	// ErrWrongCode means the server rejected the login code.
	ErrWrongCode = errors.New("accountauth: the login code was rejected")
	// ErrCodeExpired means the login code is no longer valid.
	ErrCodeExpired = errors.New("accountauth: the login code expired")
	// ErrResendNotReady means a new code cannot be requested yet.
	ErrResendNotReady = errors.New("accountauth: a new code cannot be requested yet")
	// ErrWrongPassword means the two-step verification password was rejected.
	ErrWrongPassword = errors.New("accountauth: the two-step verification password was rejected")
	// ErrPasswordRequired means the account has 2FA and no password was given
	// yet.
	ErrPasswordRequired = errors.New("accountauth: two-step verification is required")
	// ErrUnsupportedMethod means this server cannot do this login method. Phone
	// login is never blocked by this.
	ErrUnsupportedMethod = errors.New("accountauth: this login method is not supported by this server")
	// ErrFloodWait means the server asked us to slow down.
	ErrFloodWait = errors.New("accountauth: the server asked to wait")
	// ErrNotAuthorized means the caller has no owner to act as. A
	// client-supplied owner is never an authorization decision.
	ErrNotAuthorized = errors.New("accountauth: not authorized to act as this user")
	// ErrUnavailable means the account's MTProto session could not be reached.
	ErrUnavailable = errors.New("accountauth: the account session is unavailable")
	// ErrQRUnsupported means QR login is not available for this server.
	ErrQRUnsupported = ErrUnsupportedMethod
	// ErrQRExpired means the displayed login token is no longer valid and a
	// fresh one must be shown.
	ErrQRExpired = errors.New("accountauth: the QR login token expired")
)

// LoginAttempt is one login in progress, or one that finished.
//
// It carries no secret: no code, no password, no session bytes. What it does
// carry is the state a user needs to see what to do next.
type LoginAttempt struct {
	ID          string `json:"id"`
	ServerKey   string `json:"server_key"`
	NetworkType string `json:"network_type"`
	Method      string `json:"method"`
	State       string `json:"state"`

	// CodeSubmissions counts code and password submissions. It bounds a brute
	// force against one attempt. It is never part of the JSON a client sees.
	CodeSubmissions int `json:"-"`

	OwnerUserID int64 `json:"-"`
	// AccountID is the permanent account identity reserved when the attempt
	// starts. Its account row remains unroutable until authorization succeeds.
	AccountID string `json:"account_id,omitempty"`

	PhoneMasked string `json:"phone_masked,omitempty"`
	// Delivery is how the server actually sent the code. It is never guessed.
	Delivery string `json:"delivery,omitempty"`
	// DeliveryHint is the server's own description of the next delivery
	// channel, when it named one.
	DeliveryHint string `json:"delivery_hint,omitempty"`
	// CodeLength is the code length the server asked for, when it said.
	CodeLength int `json:"code_length,omitempty"`
	// NextStep is the one action the user can take now.
	NextStep string `json:"next_step"`
	// ErrorCode is the last failure, as a stable code.
	ErrorCode string `json:"error_code,omitempty"`

	// QRToken is the short-lived token to display as a QR code. It is a login
	// token, not a session secret, and it expires.
	QRToken     string    `json:"qr_token,omitempty"`
	QRExpiresAt time.Time `json:"qr_expires_at,omitempty"`

	ExpiresAt    time.Time `json:"expires_at"`
	ResendAfter  time.Time `json:"resend_after,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	AuthorizedAt time.Time `json:"authorized_at,omitempty"`
}

// Terminal reports whether the attempt can change again.
func (a LoginAttempt) Terminal() bool {
	switch a.State {
	case StateAuthorized, StateFailed, StateExpired, StateCanceled:
		return true
	default:
		return false
	}
}

// StartRequest begins one login.
type StartRequest struct {
	// ServerKey names the server definition: "official" or "cat".
	ServerKey string
	// NetworkType is the connection kind: "telegram" or "custom".
	NetworkType string
	// Method is "phone" (default) or "qr".
	Method string
	// Phone is the number to sign in. It is transient and never stored in the
	// clear; only a masked form is kept on the attempt.
	Phone string
}

// AuthorizedUser is the identity the server confirmed.
//
// It is built from the server's own authorization response, never from a
// client-supplied field.
type AuthorizedUser struct {
	RemoteUserID    int64
	RemoteUsername  string
	RemoteFirstName string
	RemoteLastName  string
	PhoneMasked     string

	// AccountID is the account record this identity is stored under. It is
	// written by the account store, never invented here.
	AccountID string
}

// CodeChallenge is what the server said when it sent a code.
type CodeChallenge struct {
	Delivery     string
	DeliveryHint string
	CodeLength   int
	// PhoneCodeHash is the server's handle for this code round. It is a server
	// token, not a user secret, and it is never returned to a client.
	PhoneCodeHash string
	ResendAfter   time.Time
}

// CodeResult is the outcome of a code or password submission.
type CodeResult struct {
	// Authorized means the account is signed in.
	Authorized bool
	User       *AuthorizedUser
	// PasswordRequired means 2FA is enabled and the next step is a password.
	PasswordRequired bool
	// PhoneCodeHash is kept for the password step when the server still needs
	// it. It is never surfaced to a client.
	PhoneCodeHash string
}

// QRChallenge is a QR login token with its expiry.
type QRChallenge struct {
	Token     string
	ExpiresAt time.Time
	// MigrationDC names the data centre the token must be re-exported from.
	MigrationDC int
}

// MTProtoAuth is the one seam between the state machine and the MTProto
// session. A worker implements it; nothing else in the system does.
//
// Implementations must never log or persist a code or a password.
type MTProtoAuth interface {
	// StartCode sends a login code and reports how the server will deliver it.
	StartCode(ctx context.Context, req StartRequest) (CodeChallenge, error)
	// SubmitCode submits one login code. The code is transient.
	SubmitCode(ctx context.Context, code string) (CodeResult, error)
	// SubmitPassword submits the 2FA password through gotd's SRP helpers. The
	// password is transient.
	SubmitPassword(ctx context.Context, password string) (CodeResult, error)
	// StartQR exports a QR login token.
	StartQR(ctx context.Context, req StartRequest) (QRChallenge, error)
	// PollQR reports whether the token has been confirmed on another device.
	PollQR(ctx context.Context, token string) (CodeResult, error)
	// RefreshQR exchanges an expired token for a short-lived one. It is only
	// called for the qr method.
	RefreshQR(ctx context.Context) (QRChallenge, error)
	// Resend asks the server for a new code.
	Resend(ctx context.Context, phoneCodeHash string) (CodeChallenge, error)
	// Cancel releases the MTProto resources this attempt holds.
	Cancel(ctx context.Context)
	// Close releases the session backing this attempt.
	Close() error
}

// LoginAttemptStore is the persistence seam for login attempts.
//
// Agent A implements this over SQLite. It is deliberately minimal and
// owner-scoped on every read and write: a store that could answer for another
// owner would break the authorization contract.
// IncompleteLoginAttemptStore lists the attempts that stopped halfway.
//
// A restart has to find these: an attempt that never reached authorized holds a
// half-open MTProto session and, if it reached the account step, an account
// record that was never confirmed. Nothing may be left signed in by a crash.
type IncompleteLoginAttemptStore interface {
	// Incomplete lists attempts that are neither authorized nor terminal, and
	// whose expiry has passed before at. A store that cannot scope this may
	// return every non-terminal attempt and let the caller expire them.
	Incomplete(ctx context.Context, at time.Time) ([]LoginAttempt, error)
}

// Recovery undoes what an interrupted login left behind.
//
// It is the "a restart leaves no orphaned permanent session" rule: whatever a
// half-finished attempt created is released when the process comes back. It
// never touches an authorized attempt — that session is real and owned.
type Recovery interface {
	// Recover settles every attempt that stopped halfway and reports what it
	// released.
	Recover(ctx context.Context) (RecoveryReport, error)
}

type LoginAttemptStore interface {
	// Create records a new attempt. The id is chosen by the service.
	Create(ctx context.Context, attempt LoginAttempt) (LoginAttempt, error)
	// ByID returns the attempt only if owner owns it.
	ByID(ctx context.Context, ownerUserID int64, id string) (LoginAttempt, error)
	// UpdateState persists a state change. It refuses an attempt the owner does
	// not own.
	UpdateState(ctx context.Context, ownerUserID int64, attempt LoginAttempt) error
	// TouchResend sets the next time a code may be requested again.
	TouchResend(ctx context.Context, ownerUserID int64, id string, resendAfter time.Time) error
	// DeleteExpired removes attempts whose expiry has passed before at. It
	// returns how many were removed.
	DeleteExpired(ctx context.Context, at time.Time) (int, error)
	// DeleteByOwner removes every attempt of one owner. It returns how many
	// were removed.
	DeleteByOwner(ctx context.Context, ownerUserID int64) (int, error)
}

// AccountIdentity is one confirmed remote identity, with the context it was
// confirmed in. The owner is never taken from a request.
type AccountIdentity struct {
	OwnerUserID int64
	ServerKey   string
	NetworkType string
	User        AuthorizedUser
	// AccountID names the permanent account identity reserved for this login.
	AccountID string
}

// AccountStore records the account a confirmed identity belongs to.
//
// Agent A implements this over the telegram_accounts table. It is defined here
// so the login state machine does not import the database package.
type AccountStore interface {
	// CreatePending creates the permanent, owner/server-bound account identity
	// before MTProto starts. It remains unroutable until CompletePending.
	CreatePending(ctx context.Context, ownerUserID int64, accountID, serverKey, networkType string) error
	// CompletePending binds the server-confirmed remote identity to the account
	// reserved by this login. It never replaces an already-authorized account.
	CompletePending(ctx context.Context, ownerUserID int64, accountID string, user AuthorizedUser) error
	// ReleasePending removes an uncompleted account, never an authorized one.
	ReleasePending(ctx context.Context, ownerUserID int64, accountID string) error
}

// Service runs the login state machine.
type Service struct {
	attempts LoginAttemptStore
	accounts AccountStore
	mtproto  func(LoginAttempt) (MTProtoAuth, error)
	now      func() time.Time

	ttl         time.Duration
	rateLimit   int
	qrSupported bool

	mux sync.Mutex

	// Phones exist only between Start and Connect. They are intentionally not
	// part of LoginAttempt, whose persisted and returned representation is masked.
	transientPhones map[string]string

	// Each attempt has one temporary MTProto client. It stays alive across
	// HTTP requests so the server's private code-round state and auth key remain
	// on the same session until handoff or cleanup.
	authMu       sync.Mutex
	authSessions map[string]MTProtoAuth

	submitLocksMu sync.Mutex
	submitLocks   map[string]*attemptSubmitLock
}

type attemptSubmitLock struct {
	mu   sync.Mutex
	refs int
}

// Config wires a Service.
type Config struct {
	// Attempts is the persistence seam. Required.
	Attempts LoginAttemptStore
	// MTProto builds the MTProto side of one attempt. It is called when an
	// attempt starts and closed when the attempt ends. Required.
	MTProto func(LoginAttempt) (MTProtoAuth, error)
	// Accounts records the account a confirmed identity belongs to. Required:
	// an authorized attempt without an account is not a login.
	Accounts AccountStore
	// Now defaults to time.Now in UTC.
	Now func() time.Time
	// AttemptTTL bounds one attempt. It defaults to 10 minutes, from
	// TELESYNC_LOGIN_ATTEMPT_TTL.
	AttemptTTL time.Duration
	// RateLimit is how many code submissions one attempt accepts. It defaults
	// to 5, from TELESYNC_LOGIN_RATE_LIMIT.
	RateLimit int
	// QRSupported reports whether this server can do QR login at all.
	QRSupported bool
}

// New returns a login service. A missing piece is refused at construction
// rather than failing mid-login.
func New(cfg Config) (*Service, error) {
	if cfg.Attempts == nil {
		return nil, errors.New("accountauth: a login attempt store is required")
	}
	if cfg.MTProto == nil {
		return nil, errors.New("accountauth: an MTProto login seam is required")
	}
	if cfg.Accounts == nil {
		return nil, errors.New("accountauth: an account store is required")
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	ttl := cfg.AttemptTTL
	if ttl <= 0 {
		ttl = DefaultAttemptTTL
	}
	limit := cfg.RateLimit
	if limit <= 0 {
		limit = DefaultRateLimit
	}
	return &Service{
		attempts:        cfg.Attempts,
		mtproto:         cfg.MTProto,
		accounts:        cfg.Accounts,
		now:             now,
		ttl:             ttl,
		rateLimit:       limit,
		qrSupported:     cfg.QRSupported,
		transientPhones: make(map[string]string),
		authSessions:    make(map[string]MTProtoAuth),
		submitLocks:     make(map[string]*attemptSubmitLock),
	}, nil
}

// DefaultAttemptTTL matches TELESYNC_LOGIN_ATTEMPT_TTL's default.
const DefaultAttemptTTL = 10 * time.Minute

// DefaultRateLimit matches TELESYNC_LOGIN_RATE_LIMIT's default.
const DefaultRateLimit = 5

// LoginService is the interface the account API uses.
type LoginService interface {
	Start(ctx context.Context, owner int64, req StartRequest) (LoginAttempt, error)
	Status(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error)
	SubmitCode(ctx context.Context, owner int64, attemptID, code string) (LoginAttempt, error)
	SubmitPassword(ctx context.Context, owner int64, attemptID, password string) (LoginAttempt, error)
	Cancel(ctx context.Context, owner int64, attemptID string) error
}

// QRLoginService is the QR half of a login, for servers that support it.
//
// It is a separate interface because QR is a capability, not a given: a server
// that cannot do it answers ErrUnsupportedMethod and phone login continues
// untouched.
type QRLoginService interface {
	// ConfirmQR reports whether the displayed token has been confirmed on
	// another device, and finishes the login when it has.
	ConfirmQR(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error)
	// RefreshQR exchanges an expired token for a fresh, short-lived one.
	RefreshQR(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error)
}

// AccountResolver is the one seam that turns "which Telegram identity" into an
// account. Every legacy {key} route goes through it. It never falls back.
type AccountResolver interface {
	// Resolve returns the single eligible account for owner on serverKey, or
	// ErrAccountSelectionRequired / ErrNoEligibleAccount.
	Resolve(ctx context.Context, owner int64, serverKey string) (string, error)
	// Owns reports whether owner owns accountID.
	Owns(ctx context.Context, owner int64, accountID string) (bool, error)
}

// Account resolution errors, matched with errors.Is.
var (
	// ErrAccountSelectionRequired means the owner has more than one eligible
	// account on that server and must pick one.
	ErrAccountSelectionRequired = errors.New("accountauth: account selection required")
	// ErrNoEligibleAccount means the owner has no account on that server. There
	// is never a fallback to another user's account or a central session.
	ErrNoEligibleAccount = errors.New("accountauth: no eligible account")
)

var (
	_ LoginService   = (*Service)(nil)
	_ QRLoginService = (*Service)(nil)
)
