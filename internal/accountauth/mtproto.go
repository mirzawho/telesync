package accountauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// GotdAuth is the set of gotd authentication calls one login uses.
//
// It is the *auth.Client of a connected session. Tests drive a fake; nothing
// in production reimplements any of it — the password step goes through gotd's
// SRP helpers and never through a verifier of our own.
type GotdAuth interface {
	SendCode(ctx context.Context, phone string, options auth.SendCodeOptions) (tg.AuthSentCodeClass, error)
	SignIn(ctx context.Context, phone, code, codeHash string) (*tg.AuthAuthorization, error)
	PasswordWith(ctx context.Context, hash auth.PasswordHashFunc) (*tg.AuthAuthorization, error)
	Status(ctx context.Context) (*auth.Status, error)
}

// GotdQR is the gotd QR login flow: export a token, poll it, finish.
type GotdQR interface {
	Export(ctx context.Context, exceptIDs ...int64) (qrlogin.Token, error)
	Import(ctx context.Context) (*tg.AuthAuthorization, error)
}

// GotdMTProto binds one account's MTProto session to the login state machine.
//
// Nothing here stores a code or a password. The password reaches
// auth.PasswordHashFor inside one call and is not kept; the code is passed to
// SignIn and dropped.
type GotdMTProto struct {
	auth       GotdAuth
	qr         GotdQR
	phone      string
	key        string
	user       *AuthorizedUser
	authorized *AuthorizedUser
	now        func() time.Time
	resendAt   time.Time
	// hash is the server's handle for the current code round. It is not a user
	// secret and is never returned to a client.
	hash string
}

// GotdMTProtoConfig wires the gotd adapter.
type GotdMTProtoConfig struct {
	// Auth is the connected session's auth client. Required.
	Auth GotdAuth
	// QR is the QR login flow for this session. It may be nil when the server
	// cannot do QR: phone login still works and QR answers unsupported.
	QR GotdQR
	// Phone is the number to sign in. It is held in memory for the length of
	// the login and never written anywhere.
	Phone string
	// PhoneCodeHash restores private server state for this login attempt. It is
	// never serialized or returned from the account API.
	PhoneCodeHash string
	// Key names the instance in errors. It is never a secret.
	Key string
	// Now defaults to time.Now in UTC.
	Now func() time.Time
}

// NewGotdMTProto returns the MTProto side of one login attempt.
func NewGotdMTProto(cfg GotdMTProtoConfig) (*GotdMTProto, error) {
	if cfg.Auth == nil {
		return nil, errors.New("accountauth: a gotd auth client is required")
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &GotdMTProto{
		auth:  cfg.Auth,
		qr:    cfg.QR,
		phone: cfg.Phone,
		key:   cfg.Key,
		hash:  cfg.PhoneCodeHash,
		now:   now,
	}, nil
}

// StartCode asks the server for a login code and reports how it will arrive.
//
// The delivery method is taken from the server's own auth.sentCode type. It is
// never assumed to be SMS.
func (g *GotdMTProto) StartCode(ctx context.Context, req StartRequest) (CodeChallenge, error) {
	phone := req.Phone
	if strings.TrimSpace(phone) == "" {
		phone = g.phone
	}
	if strings.TrimSpace(phone) == "" {
		return CodeChallenge{}, fmt.Errorf("accountauth: a phone number is required")
	}
	g.phone = phone

	sent, err := g.auth.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return CodeChallenge{}, classifyAuthError(err)
	}

	switch s := sent.(type) {
	case *tg.AuthSentCode:
		challenge := CodeChallenge{
			Delivery:      deliveryOf(s.Type),
			PhoneCodeHash: s.PhoneCodeHash,
		}
		g.hash = s.PhoneCodeHash
		if hint, ok := sentCodeHint(s.Type); ok {
			challenge.DeliveryHint = hint
		}
		if codeLen, ok := codeLengthOf(s.Type); ok {
			challenge.CodeLength = codeLen
		}
		// auth.sentCodeTimeout is how long the code stays valid; a resend is
		// not sensible before then.
		if s.Timeout > 0 {
			challenge.ResendAfter = g.now().Add(time.Duration(s.Timeout) * time.Second)
		}
		g.resendAt = challenge.ResendAfter
		return challenge, nil
	case *tg.AuthSentCodeSuccess:
		// The server answered with an authorization instead of a code: there is
		// no code round to invent, so this is a completed login.
		result, err := g.authorizedResult(ctx)
		if err != nil {
			return CodeChallenge{}, err
		}
		if result.User == nil {
			return CodeChallenge{}, fmt.Errorf("accountauth: the server authorized a login without naming a user")
		}
		g.authorized = result.User
		return CodeChallenge{Delivery: DeliveryUnknown}, nil
	default:
		return CodeChallenge{}, fmt.Errorf("accountauth: the server returned %T for a login code", sent)
	}
}

// SubmitCode submits one login code.
//
// gotd's SignIn decides whether 2FA is next; this never inspects or stores the
// password. The code is dropped when the call returns.
func (g *GotdMTProto) SubmitCode(ctx context.Context, code string) (CodeResult, error) {
	// A server that signed the number in without a code round is done.
	if g.authorized != nil {
		return CodeResult{Authorized: true, User: g.authorized}, nil
	}
	if strings.TrimSpace(g.phone) == "" {
		return CodeResult{}, fmt.Errorf("accountauth: no phone number for this login")
	}
	_, err := g.auth.SignIn(ctx, g.phone, code, g.hash)
	switch {
	case err == nil:
		return g.authorizedResult(ctx)
	case errors.Is(err, auth.ErrPasswordAuthNeeded) || tgerr.Is(err, "SESSION_PASSWORD_NEEDED"):
		return CodeResult{PasswordRequired: true}, nil
	case errors.Is(err, auth.ErrPasswordNotProvided):
		return CodeResult{PasswordRequired: true}, nil
	default:
		return CodeResult{}, classifyAuthError(err)
	}
}

// SubmitPassword completes a 2FA login through gotd's SRP helpers.
//
// The password is turned into an SRP answer inside auth.PasswordHashFor and
// never leaves this call. There is deliberately no verifier of our own here.
func (g *GotdMTProto) SubmitPassword(ctx context.Context, password string) (CodeResult, error) {
	if strings.TrimSpace(password) == "" {
		return CodeResult{}, fmt.Errorf("accountauth: a password is required")
	}
	// auth.PasswordHashFor hashes inside the call: the plaintext is passed
	// once, to gotd, and not retained by this package.
	if _, err := g.auth.PasswordWith(ctx, auth.PasswordHashFor([]byte(password))); err != nil {
		return CodeResult{}, classifyAuthError(err)
	}
	return g.authorizedResult(ctx)
}

// StartQR exports a short-lived login token.
func (g *GotdMTProto) StartQR(ctx context.Context, _ StartRequest) (QRChallenge, error) {
	if g.qr == nil {
		return QRChallenge{}, fmt.Errorf("%w: qr", ErrUnsupportedMethod)
	}
	token, err := g.qr.Export(ctx)
	if err != nil {
		if isQRUnsupported(err) {
			return QRChallenge{}, fmt.Errorf("%w: qr", ErrUnsupportedMethod)
		}
		return QRChallenge{}, classifyAuthError(err)
	}
	return QRChallenge{
		Token:     token.String(),
		ExpiresAt: token.Expires(),
	}, nil
}

// RefreshQR exports a fresh token for the same login.
func (g *GotdMTProto) RefreshQR(ctx context.Context) (QRChallenge, error) {
	return g.StartQR(ctx, StartRequest{Method: MethodQR})
}

// PollQR reports whether the displayed token has been confirmed.
//
// Until the remote device confirms, this returns no result and no error: the
// attempt stays in waiting_for_qr and the token keeps its own expiry.
func (g *GotdMTProto) PollQR(ctx context.Context, token string) (CodeResult, error) {
	if g.qr == nil {
		return CodeResult{}, fmt.Errorf("%w: qr", ErrUnsupportedMethod)
	}
	if strings.TrimSpace(token) == "" {
		return CodeResult{}, fmt.Errorf("accountauth: there is no QR token to confirm")
	}
	authorization, err := g.qr.Import(ctx)
	if err != nil {
		if isQRUnsupported(err) {
			return CodeResult{}, fmt.Errorf("%w: qr", ErrUnsupportedMethod)
		}
		return CodeResult{}, classifyAuthError(err)
	}
	if authorization == nil {
		// The token has not been confirmed yet.
		return CodeResult{}, nil
	}
	return g.authorizedResult(ctx)
}

// CodeRoundHandle reports the server's handle for the current code round, or
// "" when there is none.
//
// It is a server token, not a user secret: it is what auth.resendCode needs and
// is deliberately not part of anything a client sees. A canceled login drops it.
func (g *GotdMTProto) CodeRoundHandle() string { return g.hash }

// Resend asks the server for a new code. It refuses before the resend time the
// server itself named.
func (g *GotdMTProto) Resend(ctx context.Context, _ string) (CodeChallenge, error) {
	if !g.resendAt.IsZero() && g.now().Before(g.resendAt) {
		return CodeChallenge{}, ErrResendNotReady
	}
	return g.StartCode(ctx, StartRequest{Phone: g.phone, Method: MethodPhone})
}

// Cancel releases the resources this login held. The phone number goes with
// them: a canceled attempt keeps no trace of the number it was for.
func (g *GotdMTProto) Cancel(context.Context) {
	g.phone = ""
	g.hash = ""
	g.authorized = nil
}

// Close releases the session backing this login.
func (g *GotdMTProto) Close() error {
	g.phone = ""
	g.hash = ""
	g.authorized = nil
	return nil
}

// authorizedResult reads the identity the server confirmed.
func (g *GotdMTProto) authorizedResult(ctx context.Context) (CodeResult, error) {
	status, err := g.auth.Status(ctx)
	if err != nil {
		return CodeResult{}, classifyAuthError(err)
	}
	if status == nil || !status.Authorized {
		return CodeResult{}, fmt.Errorf("accountauth: the login finished but the session is not authorized")
	}
	user := &AuthorizedUser{
		RemoteUserID:    status.User.ID,
		RemoteUsername:  status.User.Username,
		RemoteFirstName: status.User.FirstName,
		RemoteLastName:  status.User.LastName,
		PhoneMasked:     maskPhone(status.User.Phone),
	}
	g.user = user
	return CodeResult{Authorized: true, User: user}, nil
}

// deliveryOf reports the delivery method the server actually chose.
func deliveryOf(t tg.AuthSentCodeTypeClass) string {
	switch t.(type) {
	case *tg.AuthSentCodeTypeApp:
		return DeliveryTelegramApp
	case *tg.AuthSentCodeTypeSMS:
		return DeliverySMS
	case *tg.AuthSentCodeTypeCall:
		return DeliveryCall
	case *tg.AuthSentCodeTypeFlashCall:
		return DeliveryFlashCall
	case *tg.AuthSentCodeTypeMissedCall:
		return DeliveryMissedCall
	case *tg.AuthSentCodeTypeEmailCode:
		return DeliveryEmail
	case *tg.AuthSentCodeTypeFragmentSMS:
		return DeliveryFragmentSMS
	case *tg.AuthSentCodeTypeFirebaseSMS:
		return DeliveryFirebaseSMS
	case *tg.AuthSentCodeTypeSMSWord:
		return DeliverySMSWord
	case *tg.AuthSentCodeTypeSMSPhrase:
		return DeliverySMSPhrase
	default:
		return DeliveryUnknown
	}
}

// sentCodeHint carries the server's own detail about the delivery — an email
// pattern, the beginning of a word — when it gave one.
func sentCodeHint(t tg.AuthSentCodeTypeClass) (string, bool) {
	switch v := t.(type) {
	case *tg.AuthSentCodeTypeEmailCode:
		if v.EmailPattern != "" {
			return v.EmailPattern, true
		}
	case *tg.AuthSentCodeTypeSMSWord:
		if v.Beginning != "" {
			return v.Beginning, true
		}
	case *tg.AuthSentCodeTypeSMSPhrase:
		if v.Beginning != "" {
			return v.Beginning, true
		}
	case *tg.AuthSentCodeTypeFlashCall:
		if v.Pattern != "" {
			return v.Pattern, true
		}
	}
	return "", false
}

// codeLengthOf reports the code length the server asked for, when it said.
func codeLengthOf(t tg.AuthSentCodeTypeClass) (int, bool) {
	switch v := t.(type) {
	case *tg.AuthSentCodeTypeApp:
		return v.Length, true
	case *tg.AuthSentCodeTypeSMS:
		return v.Length, true
	case *tg.AuthSentCodeTypeCall:
		return v.Length, true
	case *tg.AuthSentCodeTypeEmailCode:
		return v.Length, true
	case *tg.AuthSentCodeTypeFirebaseSMS:
		return v.Length, true
	}
	return 0, false
}

// classifyAuthError maps an MTProto failure onto the login state machine's
// vocabulary. The message never contains the value that failed.
func classifyAuthError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, auth.ErrPasswordAuthNeeded), errors.Is(err, auth.ErrPasswordNotProvided):
		return ErrPasswordRequired
	case tgerr.Is(err, "PHONE_CODE_INVALID"):
		return fmt.Errorf("%w", ErrWrongCode)
	case tgerr.Is(err, "PHONE_CODE_EXPIRED"):
		return fmt.Errorf("%w", ErrCodeExpired)
	case tgerr.Is(err, "PASSWORD_HASH_INVALID"):
		return fmt.Errorf("%w", ErrWrongPassword)
	case tgerr.Is(err, "PHONE_NUMBER_INVALID"), tgerr.Is(err, "PHONE_NUMBER_BANNED"), tgerr.Is(err, "PHONE_NUMBER_FLOOD"):
		return fmt.Errorf("%w: the number is not accepted", ErrUnavailable)
	case tgerr.Is(err, "SESSION_PASSWORD_NEEDED"):
		return ErrPasswordRequired
	case tgerr.Is(err, "API_ID_INVALID"), tgerr.Is(err, "AUTH_KEY_UNREGISTERED"), tgerr.Is(err, "SESSION_REVOKED"):
		return fmt.Errorf("%w", ErrUnavailable)
	}

	var flood *tgerr.Error
	if errors.As(err, &flood) && strings.HasPrefix(flood.Type, "FLOOD_WAIT") {
		return fmt.Errorf("%w: %s", ErrFloodWait, flood.Type)
	}
	if isQRUnsupported(err) {
		return fmt.Errorf("%w: qr", ErrUnsupportedMethod)
	}
	return fmt.Errorf("%w", ErrUnavailable)
}

// isQRUnsupported reports whether the server refused the QR flow itself.
//
// A custom backend that does not implement auth.exportLoginToken must say so
// explicitly; it must never make phone login fall back to official Telegram.
func isQRUnsupported(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case tgerr.Is(err, "METHOD_INVALID"),
		tgerr.Is(err, "METHOD_NOT_ALLOWED"),
		tgerr.Is(err, "NOT_SUPPORTED"):
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "exportlogintoken") && strings.Contains(msg, "unsupported")
}

var _ MTProtoAuth = (*GotdMTProto)(nil)
