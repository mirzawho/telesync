package webhooktest_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/webhooktest"
)

// Authentication is checked against the configured credential, on the raw
// bytes that arrived. A route that has no credential configured refuses
// everything; a route that has one never accepts a request that does not prove
// it.

var authNow = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func testAuth() webhooktest.Auth {
	return webhooktest.Auth{
		HMACSecret:   "test-hmac-secret",
		BearerToken:  "test-bearer-token",
		HeaderName:   "X-API-Key",
		HeaderSecret: "test-header-secret",
	}
}

// signedRequest builds a request the way the sender builds one: the signature
// is over exactly these bytes, at exactly this moment.
func signedRequest(t *testing.T, secret, deliveryID string, at time.Time, body string) *http.Request {
	t.Helper()
	sig, err := webhooks.Sign(secret, deliveryID, at, []byte(body))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhook/hmac", strings.NewReader(body))
	for name, values := range sig.Headers() {
		req.Header[name] = values
	}
	return req
}

func TestHMACAcceptsTheSignatureTheSenderMakes(t *testing.T) {
	body := `{"specversion":"1.0","id":"evt_abc"}`
	req := signedRequest(t, "test-hmac-secret", "wbd_1", authNow, body)

	got := testAuth().Verify(webhooktest.AuthHMAC, req, []byte(body), authNow)
	if !got.Valid {
		t.Fatalf("a signature this sender produced was refused: %+v", got)
	}
	if got.Type != webhooktest.AuthHMAC {
		t.Fatalf("Type = %q", got.Type)
	}
}

func TestHMACAcceptsASecretInTheStandardWebhooksForm(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	auth := testAuth()
	auth.HMACSecret = secret
	body := `{"specversion":"1.0"}`
	req := signedRequest(t, secret, "wbd_2", authNow, body)

	if got := auth.Verify(webhooktest.AuthHMAC, req, []byte(body), authNow); !got.Valid {
		t.Fatalf("a whsec_ secret the sender signed with was refused: %+v", got)
	}
}

func TestHMACRefusesEveryWayAResultCouldBeWrong(t *testing.T) {
	body := `{"specversion":"1.0","id":"evt_abc"}`
	good := signedRequest(t, "test-hmac-secret", "wbd_1", authNow, body)

	cases := []struct {
		name string
		code webhooktest.ErrCode
		req  func() *http.Request
	}{
		{"a signature for another secret", webhooktest.CodeInvalidSignature, func() *http.Request {
			return signedRequest(t, "another-secret", "wbd_1", authNow, body)
		}},
		{"a body modified after signing", webhooktest.CodeInvalidSignature, func() *http.Request {
			req := signedRequest(t, "test-hmac-secret", "wbd_1", authNow, body)
			return req
		}},
		{"no signature header", webhooktest.CodeMissingSignature, func() *http.Request {
			req := cloneRequest(good)
			req.Header.Del(webhooks.SignatureHeader)
			return req
		}},
		{"no timestamp header", webhooktest.CodeMissingTimestamp, func() *http.Request {
			req := cloneRequest(good)
			req.Header.Del(webhooks.TimestampHeader)
			return req
		}},
		{"no delivery id header", webhooktest.CodeMissingDeliveryID, func() *http.Request {
			req := cloneRequest(good)
			req.Header.Del(webhooks.IDHeader)
			return req
		}},
		{"a malformed signature", webhooktest.CodeInvalidSignature, func() *http.Request {
			req := cloneRequest(good)
			req.Header.Set(webhooks.SignatureHeader, "not-a-signature")
			return req
		}},
		{"a malformed timestamp", webhooktest.CodeInvalidTimestamp, func() *http.Request {
			req := cloneRequest(good)
			req.Header.Set(webhooks.TimestampHeader, "yesterday")
			return req
		}},
		{"a timestamp from another age", webhooktest.CodeExpiredTimestamp, func() *http.Request {
			req := cloneRequest(good)
			req.Header.Set(webhooks.TimestampHeader,
				strconv.FormatInt(authNow.Add(-time.Hour).Unix(), 10))
			return req
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req()
			verified := body
			if tc.name == "a body modified after signing" {
				verified = `{"specversion":"1.0","id":"evt_abd"}`
			}
			got := testAuth().Verify(webhooktest.AuthHMAC, req, []byte(verified), authNow)
			if got.Valid {
				t.Fatalf("a request that must fail was accepted: %+v", got)
			}
			if got.Code != tc.code {
				t.Fatalf("Code = %q, want %q (%s)", got.Code, tc.code, got.Message)
			}
			if got.Type != webhooktest.AuthHMAC {
				t.Fatalf("Type = %q", got.Type)
			}
		})
	}
}

func TestHMACRefusesATimestampTooFarInTheFuture(t *testing.T) {
	body := `{"specversion":"1.0"}`
	req := signedRequest(t, "test-hmac-secret", "wbd_1", authNow.Add(time.Hour), body)
	got := testAuth().Verify(webhooktest.AuthHMAC, req, []byte(body), authNow)
	if got.Valid {
		t.Fatal("a signature made an hour in the future was accepted")
	}
	if got.Code != webhooktest.CodeExpiredTimestamp {
		t.Fatalf("Code = %q, want %q", got.Code, webhooktest.CodeExpiredTimestamp)
	}
}

func TestHMACAcceptsATimestampJustInsideTheTolerance(t *testing.T) {
	body := `{"specversion":"1.0"}`
	skew := webhooktest.TimestampTolerance - time.Second
	for _, at := range []time.Time{authNow.Add(-skew), authNow.Add(skew)} {
		req := signedRequest(t, "test-hmac-secret", "wbd_1", at, body)
		if got := testAuth().Verify(webhooktest.AuthHMAC, req, []byte(body), authNow); !got.Valid {
			t.Fatalf("a signature %s from now was refused: %+v", at.Sub(authNow), got)
		}
	}
}

func TestHMACAcceptsOneOfSeveralOfferedSignatures(t *testing.T) {
	// A receiver that has just rotated keys is offered several candidates; one
	// match is enough.
	body := `{"specversion":"1.0"}`
	req := signedRequest(t, "test-hmac-secret", "wbd_1", authNow, body)
	other, err := webhooks.Sign("rotated-away", "wbd_1", authNow, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(webhooks.SignatureHeader, other.Value+" "+req.Header.Get(webhooks.SignatureHeader))

	if got := testAuth().Verify(webhooktest.AuthHMAC, req, []byte(body), authNow); !got.Valid {
		t.Fatalf("the matching candidate was not found: %+v", got)
	}
}

func TestHMACRefusesWhenNoSecretIsConfigured(t *testing.T) {
	body := `{"specversion":"1.0"}`
	req := signedRequest(t, "test-hmac-secret", "wbd_1", authNow, body)
	auth := testAuth()
	auth.HMACSecret = ""

	got := auth.Verify(webhooktest.AuthHMAC, req, []byte(body), authNow)
	if got.Valid {
		t.Fatal("an unconfigured route accepted a signature")
	}
	if got.Code != webhooktest.CodeNotConfigured {
		t.Fatalf("Code = %q, want %q", got.Code, webhooktest.CodeNotConfigured)
	}
}

func TestBearerChecksTheAuthorizationHeaderAndNothingElse(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*http.Request)
		code  webhooktest.ErrCode
	}{
		{"the right token", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer test-bearer-token")
		}, ""},
		{"the wrong token", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer test-bearer-tokens")
		}, webhooktest.CodeInvalidBearer},
		{"the right token under the wrong scheme", func(r *http.Request) {
			r.Header.Set("Authorization", "Basic dGVzdC1iZWFyZXItdG9rZW4=")
		}, webhooktest.CodeInvalidBearer},
		{"the bare token with no scheme", func(r *http.Request) {
			r.Header.Set("Authorization", "test-bearer-token")
		}, webhooktest.CodeInvalidBearer},
		{"no authorization at all", func(r *http.Request) {}, webhooktest.CodeMissingBearer},
		{"an empty authorization", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer ")
		}, webhooktest.CodeMissingBearer},
		{"the token in the query instead", func(r *http.Request) {}, webhooktest.CodeMissingBearer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost,
				"/webhook/bearer?token=test-bearer-token", strings.NewReader("{}"))
			tc.setup(req)
			got := testAuth().Verify(webhooktest.AuthBearer, req, []byte("{}"), authNow)
			if tc.code == "" {
				if !got.Valid {
					t.Fatalf("the configured token was refused: %+v", got)
				}
				return
			}
			if got.Valid {
				t.Fatalf("a request that must fail was accepted: %+v", got)
			}
			if got.Code != tc.code {
				t.Fatalf("Code = %q, want %q (%s)", got.Code, tc.code, got.Message)
			}
		})
	}
}

func TestCustomHeaderChecksTheConfiguredHeaderName(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*http.Request)
		code  webhooktest.ErrCode
	}{
		{"the right value", func(r *http.Request) {
			r.Header.Set("X-API-Key", "test-header-secret")
		}, ""},
		{"the wrong value", func(r *http.Request) {
			r.Header.Set("X-API-Key", "test-header-secre")
		}, webhooktest.CodeInvalidHeader},
		{"no header at all", func(r *http.Request) {}, webhooktest.CodeMissingHeader},
		{"the secret under another name", func(r *http.Request) {
			r.Header.Set("X-Api-Key-2", "test-header-secret")
		}, webhooktest.CodeMissingHeader},
		{"an authorization header instead", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer test-header-secret")
		}, webhooktest.CodeMissingHeader},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/webhook/header", strings.NewReader("{}"))
			tc.setup(req)
			got := testAuth().Verify(webhooktest.AuthHeader, req, []byte("{}"), authNow)
			if tc.code == "" {
				if !got.Valid {
					t.Fatalf("the configured header secret was refused: %+v", got)
				}
				return
			}
			if got.Valid {
				t.Fatalf("a request that must fail was accepted: %+v", got)
			}
			if got.Code != tc.code {
				t.Fatalf("Code = %q, want %q (%s)", got.Code, tc.code, got.Message)
			}
		})
	}
}

func TestCustomHeaderHonoursAConfiguredName(t *testing.T) {
	auth := testAuth()
	auth.HeaderName = "X-Telesync-Hook"
	req := httptest.NewRequest(http.MethodPut, "/webhook/header", strings.NewReader("{}"))
	req.Header.Set("X-Telesync-Hook", "test-header-secret")

	if got := auth.Verify(webhooktest.AuthHeader, req, []byte("{}"), authNow); !got.Valid {
		t.Fatalf("the configured header name was not honoured: %+v", got)
	}

	req.Header.Del("X-Telesync-Hook")
	req.Header.Set("X-API-Key", "test-header-secret")
	if got := auth.Verify(webhooktest.AuthHeader, req, []byte("{}"), authNow); got.Valid {
		t.Fatal("a header the receiver was not configured for was accepted")
	}
}

func TestNoAuthenticationAcceptsAnything(t *testing.T) {
	for _, method := range []string{
		http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions,
	} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/webhook/none", strings.NewReader("{}"))
			if got := testAuth().Verify(webhooktest.AuthNone, req, []byte("{}"), authNow); !got.Valid {
				t.Fatalf("the unauthenticated route refused %s: %+v", method, got)
			}
		})
	}
}

func TestVerificationFailuresNeverEchoTheExpectedSecret(t *testing.T) {
	body := `{"specversion":"1.0"}`
	auth := testAuth()
	cases := []struct {
		mode webhooktest.AuthType
		req  *http.Request
	}{
		{webhooktest.AuthHMAC, signedRequest(t, "another-secret", "wbd_1", authNow, body)},
		{webhooktest.AuthBearer, httptest.NewRequest(http.MethodPost, "/webhook/bearer", strings.NewReader(body))},
		{webhooktest.AuthHeader, httptest.NewRequest(http.MethodPost, "/webhook/header", strings.NewReader(body))},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			got := auth.Verify(tc.mode, tc.req, []byte(body), authNow)
			if got.Valid {
				t.Fatal("this request must fail")
			}
			for _, secret := range []string{auth.HMACSecret, auth.BearerToken, auth.HeaderSecret} {
				if strings.Contains(got.Message, secret) {
					t.Fatalf("the failure message leaks %q: %q", secret, got.Message)
				}
			}
		})
	}
}

// cloneRequest copies a request so a table of cases can each damage their own.
func cloneRequest(r *http.Request) *http.Request {
	out := r.Clone(r.Context())
	out.Header = r.Header.Clone()
	return out
}
