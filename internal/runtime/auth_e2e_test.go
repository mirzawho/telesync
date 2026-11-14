package runtime_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/runtime"
)

// TestDaemonLoginEndToEnd drives the daemon's real handler the way a client
// does: an anonymous call is refused, a password login issues a token, and that
// token opens the API. The daemon listens over an in-memory pipe, so no socket
// is bound.
func TestDaemonLoginEndToEnd(t *testing.T) {
	accounts := accountsFor(t, t.TempDir())
	accountID := accounts.telegramAccount(t, "official", channelstore.NetworkTelegram)
	official := &fakeUpstream{health: rpc.Health{Key: "official", State: rpc.StateConnected}}
	pipes := newPipeDialer()
	defer pipes.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runtime.RunDaemon(ctx, runtime.DaemonOptions{
			Config: appconfig.Config{
				API:     appconfig.APIConfig{ListenAddr: "127.0.0.1:0", RequestTimeout: 5 * time.Second},
				Clients: []appconfig.Client{{Key: "official", Enabled: true}},
			},
			Workers: map[string]gateway.Worker{
				accountID: official,
			},
			Channels: accounts.store,
			Users:    accounts.store,
			Tokens:   accounts.tokens,
			Listen:   func(string) (net.Listener, error) { return pipes, nil },
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the daemon did not stop")
		}
	})

	// One request per connection: a new in-memory pipe each time.
	call := func(method, path, body, token string) (int, *http.Response) {
		var reader *strings.Reader
		if body != "" {
			reader = strings.NewReader(body)
		} else {
			reader = strings.NewReader("")
		}
		req, err := http.NewRequest(method, "http://gateway"+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		transport := &http.Transport{
			DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return pipes.Dial(ctx, "", "") },
			DisableKeepAlives: true,
		}
		defer transport.CloseIdleConnections()
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp.StatusCode, resp
	}

	// The API needs a credential; the pages (health) do not. These tests run at
	// the listener's root, where there is no panel: its paths are the API's.
	code, resp := call(http.MethodGet, "/healthz", "", "")
	resp.Body.Close()
	if code != http.StatusOK {
		t.Fatalf("anonymous /healthz = %d, want 200", code)
	}
	code, resp = call(http.MethodGet, "/api/v1/telegram/official/health", "", "")
	resp.Body.Close()
	if code != http.StatusUnauthorized {
		t.Fatalf("anonymous health = %d, want 401", code)
	}
	code, resp = call(http.MethodPost, "/api/v1/auth/login", `{"username":"operator","password":"wrong"}`, "")
	resp.Body.Close()
	if code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", code)
	}

	code, resp = call(http.MethodPost, "/api/v1/auth/login",
		`{"username":"operator","password":"runtime-test-password"}`, "")
	var login struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	err := json.NewDecoder(resp.Body).Decode(&login)
	resp.Body.Close()
	if code != http.StatusOK || err != nil || login.AccessToken == "" || login.TokenType != "Bearer" || login.ExpiresIn <= 0 {
		t.Fatalf("login = %d, %+v, %v", code, login, err)
	}

	for _, path := range []string{"/api/v1/telegram/official/health", "/api/v1/auth/me", "/api/v1/local/users"} {
		code, resp := call(http.MethodGet, path, "", login.AccessToken)
		resp.Body.Close()
		if code != http.StatusOK {
			t.Fatalf("%s with token = %d, want 200", path, code)
		}
	}

	// A permission revoked in the database applies to the next request, with
	// the same token. /healthz is deliberately anonymous, so the check uses a
	// worker route.
	if err := accounts.store.ReplacePermissions(context.Background(), accounts.operator.ID,
		channelstore.PermissionSet{}); err != nil {
		t.Fatal(err)
	}
	code, resp = call(http.MethodGet, "/api/v1/telegram/official/channels", "", login.AccessToken)
	resp.Body.Close()
	if code != http.StatusForbidden {
		t.Fatalf("channels after revocation = %d, want 403", code)
	}

	// A deactivated account's token stops opening anything.
	inactive := false
	if _, err := accounts.store.UpdateUser(context.Background(), accounts.operator.ID,
		channelstore.UserUpdate{IsActive: &inactive}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	code, resp = call(http.MethodGet, "/api/v1/auth/me", "", login.AccessToken)
	resp.Body.Close()
	if code != http.StatusUnauthorized {
		t.Fatalf("me for a deactivated account = %d, want 401", code)
	}
}
