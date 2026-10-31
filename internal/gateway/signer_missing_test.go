package gateway_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mirza/telesync/internal/auth"
)

// A daemon started without api.jwt.secret_env has no signer, and the field
// then holds a nil *auth.Tokens inside an interface: not == nil. Login must
// answer 503, not dereference it and crash the server.
func TestLoginWithoutASignerAnswers503InsteadOfPanicking(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	env.account(t, "alice")

	cfg := env.handlerConfig()
	cfg.Tokens = (*auth.Tokens)(nil)
	env.useConfig(cfg)

	resp := env.send(t, "", http.MethodPost, loginPath, `{"username":"alice","password":"password-alice"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", resp.StatusCode, bodyOf(resp))
	}
	if code := decode[errorEnvelope](t, resp).Error.Code; code != "worker_unavailable" {
		t.Fatalf("code = %q", code)
	}
	if got := resp.Header.Get("Content-Type"); got == "" || got[:16] != "application/json" {
		t.Fatalf("content type = %q", got)
	}
}

// The same missing signer makes every token unverifiable, so protected routes
// answer 401 rather than panicking in Parse.
func TestProtectedRoutesWithNoSignerAnswer401(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	who := env.account(t, "alice")

	cfg := env.handlerConfig()
	cfg.Tokens = nil
	env.useConfig(cfg)

	for _, path := range []string{mePath, "/api/v1/telegram/official/health"} {
		resp := env.send(t, who.token, http.MethodGet, path, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", path, resp.StatusCode)
		}
	}
	// /healthz needs nothing, so it keeps answering.
	if resp := env.send(t, "", http.MethodGet, "/healthz", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}
}

// The end of the reported incident: the daemon is configured with a literal
// api.jwt.secret, so login issues a token, /me works, and the token expires.
// Nothing panics, because the signer is present.
func TestLoginWorksWithAConfiguredSigner(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	who := env.account(t, "alice")

	resp := env.send(t, "", http.MethodPost, loginPath, `{"username":"alice","password":"password-alice"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(bodyOf(resp)), &body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken == "" || body.TokenType != "Bearer" || body.ExpiresIn <= 0 {
		t.Fatalf("login body = %+v", body)
	}
	if resp := env.send(t, body.AccessToken, http.MethodGet, mePath, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("me = %d", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodGet, "/api/v1/auth/me", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("the suite's token = %d", resp.StatusCode)
	}
}
