package accountauth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
)

// idPrefix marks a login attempt id. The suffix is 26 base32 characters drawn
// from crypto/rand: an id no client chooses or predicts.
const idPrefix = "lga_"

// crockford is the base32 alphabet the id contract names, lower-cased.
var crockford = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// newAttemptID returns an opaque, server-generated attempt id.
func newAttemptID() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("accountauth: draw a login attempt id: %w", err)
	}
	// 20 bytes base32-encodes to 32 characters; the contract wants 26.
	return idPrefix + crockford.EncodeToString(raw)[:26], nil
}

// Start begins one login attempt for one owner.
//
// The owner comes from the authenticated principal, never from the request.
// The attempt is persisted before anything is sent, so a crash mid-login leaves
// a record to expire rather than an orphaned session.
func (s *Service) Start(ctx context.Context, owner int64, req StartRequest) (LoginAttempt, error) {
	if owner <= 0 {
		return LoginAttempt{}, ErrNotAuthorized
	}
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = MethodPhone
	}
	switch method {
	case MethodPhone, MethodQR:
	default:
		return LoginAttempt{}, fmt.Errorf("%w: %q is not a login method", ErrUnsupportedMethod, method)
	}
	if strings.TrimSpace(req.ServerKey) == "" {
		return LoginAttempt{}, errors.New("accountauth: a server definition is required")
	}
	switch strings.TrimSpace(req.NetworkType) {
	case "telegram", "custom":
	default:
		return LoginAttempt{}, fmt.Errorf("accountauth: %q is not a network kind", req.NetworkType)
	}
	if method == MethodPhone && strings.TrimSpace(req.Phone) == "" {
		return LoginAttempt{}, errors.New("accountauth: a phone number is required")
	}
	if method == MethodQR && !s.qrSupported {
		return LoginAttempt{}, fmt.Errorf("%w: qr", ErrUnsupportedMethod)
	}

	id, err := newAttemptID()
	if err != nil {
		return LoginAttempt{}, err
	}
	accountID, err := NewAccountID()
	if err != nil {
		return LoginAttempt{}, err
	}
	now := s.now().UTC()
	attempt := LoginAttempt{
		ID:          id,
		AccountID:   accountID,
		ServerKey:   req.ServerKey,
		NetworkType: req.NetworkType,
		Method:      method,
		State:       StateCreated,
		OwnerUserID: owner,
		PhoneMasked: maskPhone(req.Phone),
		NextStep:    nextStep(StateCreated, method),
		ExpiresAt:   now.Add(s.ttl),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.accounts.CreatePending(ctx, owner, accountID, req.ServerKey, req.NetworkType); err != nil {
		return LoginAttempt{}, err
	}
	created, err := s.attempts.Create(ctx, attempt)
	if err != nil {
		_ = s.accounts.ReleasePending(ctx, owner, accountID)
		return LoginAttempt{}, err
	}
	if method == MethodPhone {
		s.mux.Lock()
		s.transientPhones[created.ID] = req.Phone
		s.mux.Unlock()
	}
	// The API's login-start request is the first user-visible step: it both
	// reserves the stable account identity and asks this account's server to
	// send a code (or exports a QR token) before it returns.
	return s.Connect(ctx, owner, created.ID)
}

// Connect drives a persisted attempt to its first user-visible auth step: a
// code is on its way, or a QR token is waiting to be shown. Start calls Connect
// before returning so login-start never leaves an attempt merely "created".
// Repeated calls after that first step only return the current state.
func (s *Service) Connect(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error) {
	attempt, err := s.ownedAttempt(ctx, owner, attemptID)
	if err != nil {
		return LoginAttempt{}, err
	}
	if attempt.Terminal() {
		return LoginAttempt{}, ErrAttemptFinished
	}
	if attempt.State == StateWaitingForCode || attempt.State == StateWaitingForPassword ||
		attempt.State == StateWaitingForQR {
		// Start already opened the session and performed its first auth step.
		// Repeated connect calls are read-only, not another code request.
		return attempt, nil
	}
	if attempt.State != StateCreated && attempt.State != StateConnecting {
		return LoginAttempt{}, ErrInvalidTransition
	}

	phone := ""
	if attempt.Method == MethodPhone {
		s.mux.Lock()
		phone = s.transientPhones[attempt.ID]
		delete(s.transientPhones, attempt.ID)
		s.mux.Unlock()
		if phone == "" {
			return s.fail(ctx, attempt, CodeFailed)
		}
	}

	mtproto, err := s.mtprotoFor(attempt)
	if err != nil {
		return s.fail(ctx, attempt, errorCode(err))
	}

	attempt.State = StateConnecting
	attempt.NextStep = nextStep(attempt.State, attempt.Method)
	attempt.UpdatedAt = s.now().UTC()
	if err := s.attempts.UpdateState(ctx, owner, attempt); err != nil {
		return LoginAttempt{}, err
	}

	if attempt.Method == MethodQR {
		return s.beginQR(ctx, attempt, mtproto)
	}
	return s.beginCode(ctx, attempt, mtproto, phone)
}

func (s *Service) beginCode(ctx context.Context, attempt LoginAttempt, mtproto MTProtoAuth, phone string) (LoginAttempt, error) {
	challenge, err := mtproto.StartCode(ctx, StartRequest{
		ServerKey:   attempt.ServerKey,
		NetworkType: attempt.NetworkType,
		Method:      attempt.Method,
		Phone:       phone,
	})
	if err != nil {
		if errors.Is(err, ErrUnsupportedMethod) {
			// A server that cannot deliver a code this way must say so; the
			// attempt fails with an explicit reason and phone login is never
			// silently redirected elsewhere.
			return s.fail(ctx, attempt, CodeUnsupportedLoginMethod)
		}
		return s.fail(ctx, attempt, errorCode(err))
	}

	// The delivery method is reported exactly as the server described it.
	attempt.State = StateWaitingForCode
	attempt.Delivery = describeDelivery(challenge.Delivery)
	attempt.DeliveryHint = challenge.DeliveryHint
	attempt.CodeLength = challenge.CodeLength
	attempt.ResendAfter = challenge.ResendAfter
	attempt.NextStep = nextStep(attempt.State, attempt.Method)
	attempt.UpdatedAt = s.now().UTC()
	attempt.ExpiresAt = s.now().UTC().Add(s.ttl)
	if err := s.attempts.UpdateState(ctx, attempt.OwnerUserID, attempt); err != nil {
		return LoginAttempt{}, err
	}
	return attempt, nil
}

// ConfirmQR checks whether the displayed QR token was confirmed on another
// device. Until it is, the attempt simply stays in waiting_for_qr.
func (s *Service) ConfirmQR(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error) {
	attempt, err := s.ownedAttempt(ctx, owner, attemptID)
	if err != nil {
		return LoginAttempt{}, err
	}
	if attempt.Terminal() {
		return LoginAttempt{}, ErrAttemptFinished
	}
	if attempt.State != StateWaitingForQR {
		return LoginAttempt{}, ErrInvalidTransition
	}
	mtproto, err := s.mtprotoFor(attempt)
	if err != nil {
		return s.fail(ctx, attempt, errorCode(err))
	}
	result, err := mtproto.PollQR(ctx, attempt.QRToken)
	if err != nil {
		if errors.Is(err, ErrQRExpired) {
			clearQRToken(&attempt)
			attempt.ErrorCode = CodeQRExpired
			attempt.NextStep = nextStep(attempt.State, attempt.Method)
			attempt.UpdatedAt = s.now().UTC()
			_ = s.attempts.UpdateState(ctx, owner, attempt)
			return attempt, ErrQRExpired
		}
		return s.fail(ctx, attempt, errorCode(err))
	}
	return s.applyResult(ctx, attempt, result)
}

// RefreshQR exchanges an expired token for a fresh, short-lived one. A login
// token is never long lived and never becomes a session.
func (s *Service) RefreshQR(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error) {
	attempt, err := s.ownedAttempt(ctx, owner, attemptID)
	if err != nil {
		return LoginAttempt{}, err
	}
	if attempt.Terminal() {
		return LoginAttempt{}, ErrAttemptFinished
	}
	if attempt.State != StateWaitingForQR {
		return LoginAttempt{}, ErrInvalidTransition
	}
	mtproto, err := s.mtprotoFor(attempt)
	if err != nil {
		return s.fail(ctx, attempt, errorCode(err))
	}
	challenge, err := mtproto.RefreshQR(ctx)
	if err != nil {
		if errors.Is(err, ErrUnsupportedMethod) {
			return s.fail(ctx, attempt, CodeUnsupportedLoginMethod)
		}
		return s.fail(ctx, attempt, errorCode(err))
	}
	attempt.QRToken = challenge.Token
	attempt.QRExpiresAt = challenge.ExpiresAt
	attempt.ErrorCode = ""
	attempt.NextStep = nextStep(attempt.State, attempt.Method)
	attempt.UpdatedAt = s.now().UTC()
	if err := s.attempts.UpdateState(ctx, owner, attempt); err != nil {
		return LoginAttempt{}, err
	}
	return attempt, nil
}

// SetQRSupported records whether this deployment can do QR login at all. It is
// a capability decision taken at startup, not something a client supplies.
func (s *Service) SetQRSupported(supported bool) error {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.qrSupported = supported
	return nil
}

// QRSupported reports the capability, so a caller can hide the option rather
// than offer a method the server cannot do.
func (s *Service) QRSupported() bool {
	s.mux.Lock()
	defer s.mux.Unlock()
	return s.qrSupported
}

func (s *Service) beginQR(ctx context.Context, attempt LoginAttempt, mtproto MTProtoAuth) (LoginAttempt, error) {
	challenge, err := mtproto.StartQR(ctx, StartRequest{
		ServerKey:   attempt.ServerKey,
		NetworkType: attempt.NetworkType,
		Method:      attempt.Method,
	})
	if err != nil {
		if errors.Is(err, ErrUnsupportedMethod) {
			return s.fail(ctx, attempt, CodeUnsupportedLoginMethod)
		}
		return s.fail(ctx, attempt, errorCode(err))
	}

	attempt.State = StateWaitingForQR
	attempt.QRToken = challenge.Token
	attempt.QRExpiresAt = challenge.ExpiresAt
	attempt.NextStep = nextStep(attempt.State, attempt.Method)
	attempt.UpdatedAt = s.now().UTC()
	if err := s.attempts.UpdateState(ctx, attempt.OwnerUserID, attempt); err != nil {
		return LoginAttempt{}, err
	}
	return attempt, nil
}

// Status reports one attempt to its owner.
func (s *Service) Status(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error) {
	return s.ownedAttempt(ctx, owner, attemptID)
}

// SubmitCode submits one login code.
//
// The code is transient: it goes to the MTProto seam and is never written to
// the attempt, the store or a log line.
func (s *Service) SubmitCode(ctx context.Context, owner int64, attemptID, code string) (LoginAttempt, error) {
	unlock := s.lockAttemptSubmission(attemptID)
	defer unlock()

	attempt, err := s.ownedAttempt(ctx, owner, attemptID)
	if err != nil {
		return LoginAttempt{}, err
	}
	if attempt.Terminal() {
		return LoginAttempt{}, ErrAttemptFinished
	}
	if attempt.State != StateWaitingForCode {
		return LoginAttempt{}, ErrInvalidTransition
	}
	if strings.TrimSpace(code) == "" {
		return LoginAttempt{}, errors.New("accountauth: a login code is required")
	}
	if err := s.checkRateLimit(attempt); err != nil {
		return LoginAttempt{}, err
	}

	mtproto, err := s.mtprotoFor(attempt)
	if err != nil {
		return s.fail(ctx, attempt, errorCode(err))
	}
	attempt.CodeSubmissions++
	result, err := mtproto.SubmitCode(ctx, code)
	if err != nil {
		return s.codeFailure(ctx, attempt, err)
	}
	return s.applyResult(ctx, attempt, result)
}

// SubmitPassword submits the two-step verification password.
//
// The password is transient and never reaches the store: gotd's SRP helpers
// compute the answer, and this package does not keep a copy.
func (s *Service) SubmitPassword(ctx context.Context, owner int64, attemptID, password string) (LoginAttempt, error) {
	unlock := s.lockAttemptSubmission(attemptID)
	defer unlock()

	attempt, err := s.ownedAttempt(ctx, owner, attemptID)
	if err != nil {
		return LoginAttempt{}, err
	}
	if attempt.Terminal() {
		return LoginAttempt{}, ErrAttemptFinished
	}
	if attempt.State != StateWaitingForPassword {
		return LoginAttempt{}, ErrInvalidTransition
	}
	if err := s.checkRateLimit(attempt); err != nil {
		return LoginAttempt{}, err
	}
	if strings.TrimSpace(password) == "" {
		return LoginAttempt{}, errors.New("accountauth: a password is required")
	}

	mtproto, err := s.mtprotoFor(attempt)
	if err != nil {
		return s.fail(ctx, attempt, errorCode(err))
	}
	attempt.CodeSubmissions++
	result, err := mtproto.SubmitPassword(ctx, password)
	if err != nil {
		return s.codeFailure(ctx, attempt, err)
	}
	return s.applyResult(ctx, attempt, result)
}

// Cancel ends an attempt and releases the MTProto resources it holds.
func (s *Service) Cancel(ctx context.Context, owner int64, attemptID string) error {
	attempt, err := s.ownedAttempt(ctx, owner, attemptID)
	if err != nil {
		return err
	}
	if attempt.Terminal() {
		return ErrAttemptFinished
	}
	s.clearTransientPhone(attempt.ID)
	cleanupErr := s.closeMTProto(ctx, attempt, true, false, true)
	attempt.State = StateCanceled
	clearQRToken(&attempt)
	attempt.NextStep = nextStep(attempt.State, attempt.Method)
	attempt.UpdatedAt = s.now().UTC()
	if err := s.attempts.UpdateState(ctx, owner, attempt); err != nil {
		return err
	}
	if err := s.accounts.ReleasePending(ctx, owner, attempt.AccountID); err != nil {
		return err
	}
	return cleanupErr
}

// ownedAttempt reads an attempt and refuses one the owner does not own.
//
// "Not found" is the answer for both a missing attempt and someone else's:
// an attempt id must not tell another user that it exists.
func (s *Service) ownedAttempt(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error) {
	if owner <= 0 {
		return LoginAttempt{}, ErrNotAuthorized
	}
	attempt, err := s.attempts.ByID(ctx, owner, attemptID)
	if err != nil {
		return LoginAttempt{}, err
	}
	if attempt.OwnerUserID != owner {
		return LoginAttempt{}, ErrNotFound
	}
	if attempt.Terminal() && attempt.State != StateAuthorized {
		s.clearTransientPhone(attempt.ID)
		// A finished attempt is reported as it stands; only an expiring one is
		// marked expired.
		if attempt.State == StateExpired {
			return attempt, ErrAttemptExpired
		}
		return attempt, nil
	}
	if s.now().UTC().After(attempt.ExpiresAt) {
		s.clearTransientPhone(attempt.ID)
		attempt.State = StateExpired
		clearQRToken(&attempt)
		attempt.NextStep = nextStep(attempt.State, attempt.Method)
		attempt.UpdatedAt = s.now().UTC()
		_ = s.attempts.UpdateState(ctx, owner, attempt)
		_ = s.closeMTProto(ctx, attempt, true, true, true)
		_ = s.accounts.ReleasePending(ctx, owner, attempt.AccountID)
		return attempt, ErrAttemptExpired
	}
	return attempt, nil
}

// checkRateLimit bounds code submissions per attempt.
func (s *Service) checkRateLimit(attempt LoginAttempt) error {
	if attempt.CodeSubmissions >= s.rateLimit {
		return ErrRateLimited
	}
	return nil
}

// codeFailure records a rejected code or password without keeping either.
func (s *Service) codeFailure(ctx context.Context, attempt LoginAttempt, err error) (LoginAttempt, error) {
	switch {
	case errors.Is(err, ErrWrongPassword):
		attempt.ErrorCode = CodeWrongPassword
		attempt.State = StateWaitingForPassword
		attempt.NextStep = nextStep(attempt.State, attempt.Method)
	case errors.Is(err, ErrWrongCode):
		attempt.ErrorCode = CodeWrongCode
		attempt.State = StateWaitingForCode
		attempt.NextStep = nextStep(attempt.State, attempt.Method)
	case errors.Is(err, ErrCodeExpired):
		attempt.ErrorCode = CodeExpired
		attempt.State = StateWaitingForCode
		attempt.NextStep = nextStep(attempt.State, attempt.Method)
	default:
		return s.fail(ctx, attempt, errorCode(err))
	}
	attempt.UpdatedAt = s.now().UTC()
	if uerr := s.attempts.UpdateState(ctx, attempt.OwnerUserID, attempt); uerr != nil {
		return LoginAttempt{}, uerr
	}
	return attempt, err
}

func (s *Service) applyResult(ctx context.Context, attempt LoginAttempt, result CodeResult) (LoginAttempt, error) {
	now := s.now().UTC()
	switch {
	case result.Authorized && result.User != nil:
		if attempt.AccountID == "" {
			return s.fail(ctx, attempt, CodeFailed)
		}
		// The temporary auth client must fully stop and flush its account-bound
		// AccountBox before the connected account is made eligible for the
		// permanent Runner. This prevents two gotd clients writing one session.
		if err := s.closeMTProto(ctx, attempt, false, false, false); err != nil {
			return s.fail(ctx, attempt, CodeSessionUnavailable)
		}
		if err := s.accounts.CompletePending(ctx, attempt.OwnerUserID, attempt.AccountID, *result.User); err != nil {
			return s.fail(ctx, attempt, errorCode(err))
		}
		attempt.State = StateAuthorized
		clearQRToken(&attempt)
		if result.User.PhoneMasked != "" {
			attempt.PhoneMasked = result.User.PhoneMasked
		}
		attempt.ErrorCode = ""
		attempt.NextStep = nextStep(attempt.State, attempt.Method)
		attempt.AuthorizedAt = now
	case result.PasswordRequired:
		attempt.State = StateWaitingForPassword
		attempt.ErrorCode = ""
		attempt.NextStep = nextStep(attempt.State, attempt.Method)
		if attempt.AccountID == "" {
			return s.fail(ctx, attempt, CodeFailed)
		}
	default:
		return s.fail(ctx, attempt, CodeFailed)
	}
	attempt.UpdatedAt = now
	if err := s.attempts.UpdateState(ctx, attempt.OwnerUserID, attempt); err != nil {
		return LoginAttempt{}, err
	}
	if attempt.State == StateAuthorized {
		s.forgetMTProto(attempt.ID)
		s.clearTransientPhone(attempt.ID)
	}
	return attempt, nil
}

func (s *Service) fail(ctx context.Context, attempt LoginAttempt, code string) (LoginAttempt, error) {
	s.clearTransientPhone(attempt.ID)
	cleanupErr := s.closeMTProto(ctx, attempt, true, false, true)
	attempt.State = StateFailed
	clearQRToken(&attempt)
	attempt.ErrorCode = code
	attempt.NextStep = nextStep(attempt.State, attempt.Method)
	attempt.UpdatedAt = s.now().UTC()
	if err := s.attempts.UpdateState(ctx, attempt.OwnerUserID, attempt); err != nil {
		return LoginAttempt{}, err
	}
	if attempt.AccountID != "" {
		if err := s.accounts.ReleasePending(ctx, attempt.OwnerUserID, attempt.AccountID); err != nil {
			return LoginAttempt{}, err
		}
	}
	if cleanupErr != nil {
		return attempt, fmt.Errorf("%w: %s", ErrUnavailable, CodeSessionUnavailable)
	}
	return attempt, fmt.Errorf("%w: %s", ErrUnavailable, code)
}

// mtprotoFor returns the one auth adapter assigned to an attempt. A new adapter
// per HTTP request would open a competing gotd client over the same auth key
// and lose private code-round state.
func (s *Service) mtprotoFor(attempt LoginAttempt) (MTProtoAuth, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if current := s.authSessions[attempt.ID]; current != nil {
		return current, nil
	}
	mtproto, err := s.mtproto(attempt)
	if err != nil {
		return nil, err
	}
	s.authSessions[attempt.ID] = mtproto
	return mtproto, nil
}

// closeMTProto stops the attempt's session and optionally creates an unopened
// adapter for recovery, where AccountBox still has to discard stale files.
func (s *Service) closeMTProto(
	ctx context.Context,
	attempt LoginAttempt,
	cancel, create, forget bool,
) error {
	s.authMu.Lock()
	mtproto := s.authSessions[attempt.ID]
	if mtproto == nil && create {
		var err error
		mtproto, err = s.mtproto(attempt)
		if err != nil {
			s.authMu.Unlock()
			return err
		}
		s.authSessions[attempt.ID] = mtproto
	}
	s.authMu.Unlock()
	if mtproto == nil {
		return nil
	}
	if cancel {
		mtproto.Cancel(ctx)
	}
	err := mtproto.Close()
	if forget {
		s.forgetMTProto(attempt.ID)
	}
	return err
}

func (s *Service) forgetMTProto(attemptID string) {
	s.authMu.Lock()
	delete(s.authSessions, attemptID)
	s.authMu.Unlock()
}

func clearQRToken(attempt *LoginAttempt) {
	attempt.QRToken = ""
	attempt.QRExpiresAt = time.Time{}
}

func (s *Service) clearTransientPhone(attemptID string) {
	s.mux.Lock()
	delete(s.transientPhones, attemptID)
	s.mux.Unlock()
}

// lockAttemptSubmission serializes code and password submissions for one
// attempt. The reference count lets idle per-attempt locks be reclaimed.
func (s *Service) lockAttemptSubmission(attemptID string) func() {
	s.submitLocksMu.Lock()
	lock := s.submitLocks[attemptID]
	if lock == nil {
		lock = &attemptSubmitLock{}
		s.submitLocks[attemptID] = lock
	}
	lock.refs++
	s.submitLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.submitLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.submitLocks, attemptID)
		}
		s.submitLocksMu.Unlock()
	}
}

// Stable error codes for the account API contract.
const (
	CodeWrongCode              = "login_code_invalid"
	CodeExpired                = "login_code_expired"
	CodeWrongPassword          = "login_password_invalid"
	CodeRateLimited            = "login_rate_limited"
	CodeResendNotReady         = "login_resend_not_ready"
	CodeUnsupportedLoginMethod = "unsupported_login_method"
	CodeQRExpired              = "login_qr_expired"
	CodeFloodWait              = "login_flood_wait"
	CodeSessionUnavailable     = "session_encryption_unavailable"
	CodeFailed                 = "login_attempt_invalid"
)

// errorCode maps an error to a stable code without ever echoing the value that
// failed: a code or a password must not come back out in a message.
func errorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrRateLimited):
		return CodeRateLimited
	case errors.Is(err, ErrCodeExpired):
		return CodeExpired
	case errors.Is(err, ErrWrongCode):
		return CodeWrongCode
	case errors.Is(err, ErrWrongPassword):
		return CodeWrongPassword
	case errors.Is(err, ErrPasswordRequired):
		return CodeWrongPassword
	case errors.Is(err, ErrResendNotReady):
		return CodeResendNotReady
	case errors.Is(err, ErrQRExpired):
		return CodeQRExpired
	case errors.Is(err, ErrUnsupportedMethod):
		return CodeUnsupportedLoginMethod
	case errors.Is(err, ErrFloodWait):
		return CodeFloodWait
	case errors.Is(err, ErrUnavailable):
		return CodeSessionUnavailable
	default:
		return CodeFailed
	}
}

// nextStep is the single action a user can take in one state. It is a sentence
// a person can follow, and it never contains a secret.
func nextStep(state, method string) string {
	switch state {
	case StateCreated:
		return "connecting to the server"
	case StateConnecting:
		return "connecting to the server"
	case StateWaitingForCode:
		return "enter the login code the server sent"
	case StateWaitingForPassword:
		return "enter the two-step verification password"
	case StateWaitingForQR:
		return "scan the QR code with the Telegram app"
	case StateAuthorized:
		return "the account is signed in"
	case StateFailed:
		return "start a new login attempt"
	case StateExpired:
		return "start a new login attempt"
	case StateCanceled:
		return "start a new login attempt"
	default:
		_ = method
		return "start a new login attempt"
	}
}

// maskPhone keeps enough digits to recognise a number and no more. The full
// number is never stored on an attempt.
func maskPhone(phone string) string {
	digits := make([]rune, 0, len(phone))
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
		}
	}
	if len(digits) == 0 {
		return ""
	}
	if len(digits) <= 4 {
		return strings.Repeat("•", len(digits))
	}
	return "+" + strings.Repeat("•", 4) + string(digits[len(digits)-4:])
}

// describeDelivery maps the server's own delivery type to a stable code. An
// unrecognised type is "unknown", never an optimistic "sms".
// describeDelivery maps the server's own delivery type to a stable code. An
// unrecognised type is "unknown", never an optimistic "sms".
func describeDelivery(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case DeliveryTelegramApp, "telegramapp", "telegram_app":
		return DeliveryTelegramApp
	case DeliverySMS:
		return DeliverySMS
	case DeliveryCall:
		return DeliveryCall
	case DeliveryFlashCall, "flashcall":
		return DeliveryFlashCall
	case DeliveryMissedCall, "missedcall":
		return DeliveryMissedCall
	case DeliveryEmail:
		return DeliveryEmail
	case DeliveryFragmentSMS, "fragmentsms":
		return DeliveryFragmentSMS
	case DeliveryFirebaseSMS, "firebasesms":
		return DeliveryFirebaseSMS
	case DeliverySMSWord, "smsword":
		return DeliverySMSWord
	case DeliverySMSPhrase, "smsphrase":
		return DeliverySMSPhrase
	default:
		return DeliveryUnknown
	}
}

var _ = context.Background
