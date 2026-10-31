package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Token errors. Callers match them with errors.Is and answer with one generic
// authentication failure.
var (
	// ErrTokenMissing means no credential was presented.
	ErrTokenMissing = errors.New("auth: no token")
	// ErrTokenMalformed means the token is not a header.payload.signature JWT.
	ErrTokenMalformed = errors.New("auth: the token is malformed")
	// ErrTokenSignature means the signature does not verify with the key.
	ErrTokenSignature = errors.New("auth: the token signature is invalid")
	// ErrTokenExpired means the token's exp time has passed.
	ErrTokenExpired = errors.New("auth: the token has expired")
	// ErrTokenIssuer means iss or aud does not name this API.
	ErrTokenIssuer = errors.New("auth: the token belongs to another issuer")
	// ErrTokenSubject means sub is not a local user id.
	ErrTokenSubject = errors.New("auth: the token has no user subject")
)

// MinSecretLen is the shortest signing secret accepted. HS256's key should be
// at least as long as the SHA-256 output.
const MinSecretLen = 32

// TokenIssuer is the iss claim; TokenAudience is the aud claim.
const (
	TokenIssuer   = "telesync"
	TokenAudience = "telesync-api"
)

// TokenOptions configures a TokenIssuer.
type TokenOptions struct {
	// Secret is the HS256 key. It must be at least MinSecretLen bytes.
	Secret []byte
	// TTL is how long a token stays valid. Zero means one hour.
	TTL time.Duration
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Tokens issues and verifies access tokens.
type Tokens struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewTokens validates the signing configuration.
func NewTokens(opts TokenOptions) (*Tokens, error) {
	if len(opts.Secret) < MinSecretLen {
		return nil, fmt.Errorf("auth: the token signing secret must be at least %d bytes", MinSecretLen)
	}
	if opts.TTL < 0 {
		return nil, errors.New("auth: the token lifetime must not be negative")
	}
	if opts.TTL == 0 {
		opts.TTL = time.Hour
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Tokens{
		secret: append([]byte(nil), opts.Secret...),
		ttl:    opts.TTL,
		now:    opts.Now,
	}, nil
}

// TTL is how long issued tokens live.
func (t *Tokens) TTL() time.Duration { return t.ttl }

type claims struct {
	Sub string `json:"sub"`
	Iss string `json:"iss"`
	Aud string `json:"aud"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

var (
	b64 = base64.RawURLEncoding
	// errMalformed is what json decoding maps onto; the exact text is not
	// exposed.
	errMalformed = ErrTokenMalformed
)

// Issue mints a token for userID.
func (t *Tokens) Issue(userID int64) (string, time.Time, error) {
	now := t.now()
	exp := now.Add(t.ttl)
	c := claims{
		Sub: strconv.FormatInt(userID, 10),
		Iss: TokenIssuer,
		Aud: TokenAudience,
		Iat: now.Unix(),
		Exp: exp.Unix(),
	}
	headJSON, err := json.Marshal(header{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", time.Time{}, err
	}
	bodyJSON, err := json.Marshal(c)
	if err != nil {
		return "", time.Time{}, err
	}
	signing := b64.EncodeToString(headJSON) + "." + b64.EncodeToString(bodyJSON)
	return signing + "." + b64.EncodeToString(t.sign(signing)), exp, nil
}

// Parse verifies a token and returns the user id it was issued for.
//
// Every failure maps to one of the ErrToken* values; the caller decides what to
// say, and says the same thing for all of them.
func (t *Tokens) Parse(token string) (int64, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, ErrTokenMissing
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return 0, ErrTokenMalformed
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || subtle.ConstantTimeCompare(sig, t.sign(parts[0]+"."+parts[1])) != 1 {
		return 0, ErrTokenSignature
	}

	headJSON, err := b64.DecodeString(parts[0])
	if err != nil {
		return 0, errMalformed
	}
	var h header
	if err := json.Unmarshal(headJSON, &h); err != nil || h.Alg != "HS256" {
		// No "alg: none" and no algorithm confusion: only HS256 is accepted.
		return 0, ErrTokenSignature
	}
	bodyJSON, err := b64.DecodeString(parts[1])
	if err != nil {
		return 0, errMalformed
	}
	var c claims
	if err := json.Unmarshal(bodyJSON, &c); err != nil {
		return 0, errMalformed
	}
	if c.Iss != TokenIssuer || c.Aud != TokenAudience {
		return 0, ErrTokenIssuer
	}
	if c.Exp == 0 || !t.now().Before(time.Unix(c.Exp, 0)) {
		return 0, ErrTokenExpired
	}
	id, err := strconv.ParseInt(c.Sub, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrTokenSubject
	}
	return id, nil
}

func (t *Tokens) sign(signing string) []byte {
	mac := hmac.New(sha256.New, t.secret)
	mac.Write([]byte(signing))
	return mac.Sum(nil)
}
