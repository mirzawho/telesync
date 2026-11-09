package webhooktest

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation/webhooks"
)

// AuthType names which authentication one route verifies.
type AuthType string

// The four routes, one per mode. The sender's own mode names, so a record read
// beside a delivery history means the same thing in both.
const (
	AuthHMAC    AuthType = "hmac"
	AuthBearer  AuthType = "bearer"
	AuthHeader  AuthType = "header"
	AuthNone    AuthType = "none"
	AuthUnknown AuthType = "unknown"
)

// ErrCode classifies why a request was refused. They are what an operator
// reads in a saved record, so each names one thing that was wrong.
type ErrCode string

// The failure codes this receiver reports.
const (
	CodeNotConfigured     ErrCode = "not_configured"
	CodeMissingDeliveryID ErrCode = "missing_delivery_id"
	CodeMissingTimestamp  ErrCode = "missing_timestamp"
	CodeInvalidTimestamp  ErrCode = "invalid_timestamp"
	CodeExpiredTimestamp  ErrCode = "expired_timestamp"
	CodeMissingSignature  ErrCode = "missing_signature"
	CodeInvalidSignature  ErrCode = "invalid_signature"
	CodeMissingBearer     ErrCode = "missing_bearer_token"
	CodeInvalidBearer     ErrCode = "invalid_bearer_token"
	CodeMissingHeader     ErrCode = "missing_custom_header"
	CodeInvalidHeader     ErrCode = "invalid_custom_header"
	CodeUnknownAuthType   ErrCode = "unknown_auth_type"

	// CodeNotVerified marks a request this receiver refused before it looked at
	// the credentials at all, so "invalid" would be a claim it never made.
	CodeNotVerified ErrCode = "not_verified"
	// CodeBodyTooLarge and CodeNotFound are refusals that are about the request
	// rather than about how it authenticated.
	CodeBodyTooLarge ErrCode = "body_too_large"
	CodeNotFound     ErrCode = "not_found"
)

// TimestampTolerance is how far a signature timestamp may be from now and still
// be believed.
//
// The sender signs with the clock it has and re-signs a retry at a later
// second, so a timestamp outside this window is not one of its live requests.
// Nothing in the Standard Webhooks scheme fixes a window, and the sender's own
// Verify takes the timestamp as given, so the replay window is the receiver's
// to choose: five minutes is the convention the ecosystem uses.
const TimestampTolerance = 5 * time.Minute

// Auth is the credential material the receiver verifies against. It comes from
// this command's own variables and never from the main application's
// configuration.
type Auth struct {
	HMACSecret   string
	BearerToken  string
	HeaderName   string
	HeaderSecret string
}

// Result is what one verification concluded.
type Result struct {
	Type    AuthType `json:"type"`
	Valid   bool     `json:"valid"`
	Code    ErrCode  `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
}

// Verify checks one request against the configured credentials, using the raw
// body exactly as it arrived. now is what a signature timestamp is measured
// against.
//
// A mode with no credential configured refuses everything: an unconfigured
// route never becomes an unauthenticated one. No failure message carries the
// expected value, so a wrong guess cannot be corrected from the answer.
func (a Auth) Verify(mode AuthType, r *http.Request, body []byte, now time.Time) Result {
	switch mode {
	case AuthNone:
		return Result{Type: AuthNone, Valid: true}
	case AuthHMAC:
		return a.verifyHMAC(r, body, now)
	case AuthBearer:
		return a.verifyBearer(r)
	case AuthHeader:
		return a.verifyHeader(r)
	default:
		return Result{
			Type:    mode,
			Code:    CodeUnknownAuthType,
			Message: "this receiver verifies hmac, bearer, header or none",
		}
	}
}

// verifyHMAC checks a Standard Webhooks signature against the bytes that
// arrived. Nothing re-serializes the body first: the signature is over the
// transmitted bytes and only those bytes can confirm it.
func (a Auth) verifyHMAC(r *http.Request, body []byte, now time.Time) Result {
	if strings.TrimSpace(a.HMACSecret) == "" {
		return Result{Type: AuthHMAC, Code: CodeNotConfigured,
			Message: "no HMAC secret is configured for this receiver"}
	}

	id := strings.TrimSpace(r.Header.Get(webhooks.IDHeader))
	sentAt := strings.TrimSpace(r.Header.Get(webhooks.TimestampHeader))
	signature := strings.TrimSpace(r.Header.Get(webhooks.SignatureHeader))

	switch {
	case id == "":
		return Result{Type: AuthHMAC, Code: CodeMissingDeliveryID,
			Message: "the " + webhooks.IDHeader + " header is missing"}
	case sentAt == "":
		return Result{Type: AuthHMAC, Code: CodeMissingTimestamp,
			Message: "the " + webhooks.TimestampHeader + " header is missing"}
	case signature == "":
		return Result{Type: AuthHMAC, Code: CodeMissingSignature,
			Message: "the " + webhooks.SignatureHeader + " header is missing"}
	}

	seconds, err := strconv.ParseInt(sentAt, 10, 64)
	if err != nil || seconds <= 0 {
		return Result{Type: AuthHMAC, Code: CodeInvalidTimestamp,
			Message: "the " + webhooks.TimestampHeader + " header is not a unix timestamp in seconds"}
	}
	signedAt := time.Unix(seconds, 0).UTC()
	if signedAt.Before(now.Add(-TimestampTolerance)) {
		return Result{Type: AuthHMAC, Code: CodeExpiredTimestamp,
			Message: fmt.Sprintf("the signature was made %s ago, outside the %s tolerance",
				now.Sub(signedAt).Round(time.Second), TimestampTolerance)}
	}
	if signedAt.After(now.Add(TimestampTolerance)) {
		return Result{Type: AuthHMAC, Code: CodeExpiredTimestamp,
			Message: fmt.Sprintf("the signature is dated %s in the future, outside the %s tolerance",
				signedAt.Sub(now).Round(time.Second), TimestampTolerance)}
	}

	if err := webhooks.Verify(a.HMACSecret, id, signedAt, body, signature); err != nil {
		return Result{Type: AuthHMAC, Code: CodeInvalidSignature,
			Message: "webhook HMAC signature verification failed"}
	}
	return Result{Type: AuthHMAC, Valid: true}
}

// verifyBearer checks the Authorization header. The token is never accepted
// from the query: a URL is logged, cached and left in a shell history, and a
// credential in one is a credential already lost.
func (a Auth) verifyBearer(r *http.Request) Result {
	if strings.TrimSpace(a.BearerToken) == "" {
		return Result{Type: AuthBearer, Code: CodeNotConfigured,
			Message: "no bearer token is configured for this receiver"}
	}

	raw := r.Header.Get("Authorization")
	if strings.TrimSpace(raw) == "" {
		return Result{Type: AuthBearer, Code: CodeMissingBearer,
			Message: "the Authorization header is missing"}
	}
	// The scheme and its credential are split before anything is trimmed, so a
	// scheme with no token at all is "missing" and a bare token is "invalid":
	// they are different mistakes and the operator fixes them differently.
	scheme, presented, _ := strings.Cut(strings.TrimLeft(raw, " \t"), " ")
	if !strings.EqualFold(strings.TrimSpace(scheme), "Bearer") {
		return Result{Type: AuthBearer, Code: CodeInvalidBearer,
			Message: "the Authorization header does not use the Bearer scheme"}
	}
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return Result{Type: AuthBearer, Code: CodeMissingBearer,
			Message: "the Authorization header carries no token"}
	}
	if !constantTimeEqual(presented, a.BearerToken) {
		return Result{Type: AuthBearer, Code: CodeInvalidBearer,
			Message: "the bearer token is not the configured one"}
	}
	return Result{Type: AuthBearer, Valid: true}
}

// verifyHeader checks the one header this receiver was named, and nothing else.
func (a Auth) verifyHeader(r *http.Request) Result {
	name := strings.TrimSpace(a.HeaderName)
	if name == "" || strings.TrimSpace(a.HeaderSecret) == "" {
		return Result{Type: AuthHeader, Code: CodeNotConfigured,
			Message: "no custom header is configured for this receiver"}
	}

	presented := strings.TrimSpace(r.Header.Get(name))
	if presented == "" {
		return Result{Type: AuthHeader, Code: CodeMissingHeader,
			Message: "the " + name + " header is missing"}
	}
	if !constantTimeEqual(presented, a.HeaderSecret) {
		return Result{Type: AuthHeader, Code: CodeInvalidHeader,
			Message: "the " + name + " header is not the configured secret"}
	}
	return Result{Type: AuthHeader, Valid: true}
}

// constantTimeEqual compares two secrets without leaking their contents or,
// through an early return, how long they were. Comparing digests rather than
// the values themselves is what makes the length of an attacker's guess
// unobservable.
func constantTimeEqual(a, b string) bool {
	x := sha256.Sum256([]byte(a))
	y := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
