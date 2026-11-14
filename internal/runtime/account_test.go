package runtime_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
)

// The daemon serves a login-required API, so the runtime tests drive it the way
// a client does: log in with a real account and send the token. These helpers
// own the one store the daemon and the tests share.

type testAccounts struct {
	store *channelstore.Store
	// operator is the user this suite signs in as: every permission, so the
	// routing tests are not about authorization.
	operator    channelstore.User
	operatorTok string
	// tokens is the signer the daemon is given.
	tokens *auth.Tokens
}

// accountsFor returns a fresh account fixture rooted at dir. Every test gets
// its own directory and its own database.
func accountsFor(t *testing.T, dir string) *testAccounts {
	t.Helper()
	store, err := channelstore.Open(filepath.Join(dir, "accounts.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open account store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hash, err := auth.HashPassword("runtime-test-password")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(context.Background(), "Runtime Operator", "operator", hash, true)
	if err != nil {
		t.Fatalf("create operator: %v", err)
	}
	if err := store.ReplacePermissions(context.Background(), u.ID,
		channelstore.PermissionSet{Global: channelstore.GlobalPermissions()}); err != nil {
		t.Fatalf("grant permissions: %v", err)
	}
	tokens, err := auth.NewTokens(auth.TokenOptions{Secret: []byte(strings.Repeat("r", 48))})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := tokens.Issue(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &testAccounts{store: store, operator: u, operatorTok: tok, tokens: tokens}
}

// authHeader is the bearer header for the operator.
func (a *testAccounts) authHeader() string { return "Bearer " + a.operatorTok }

// telegramAccount adds one personal session identity for daemon routing tests.
func (a *testAccounts) telegramAccount(t *testing.T, serverKey, networkType string) string {
	t.Helper()
	id, err := accountid.New()
	if err != nil {
		t.Fatalf("create account id: %v", err)
	}
	if _, err := a.store.Create(context.Background(), channelstore.Account{
		ID: id, OwnerUserID: a.operator.ID, ServerKey: serverKey, NetworkType: networkType,
		RemoteUserID: 1, Status: channelstore.AccountStatusConnected,
	}); err != nil {
		t.Fatalf("create personal Telegram account: %v", err)
	}
	return id
}

// with sends the operator's token on every request.
func (a *testAccounts) with(client *http.Client) *http.Client {
	return &http.Client{Transport: &bearerTransport{base: client.Transport, header: a.authHeader()}, Timeout: client.Timeout}
}

type bearerTransport struct {
	base   http.RoundTripper
	header string
}

func (b *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// The default transport, which refuses a non-https URL, is replaced for this
	// request so the fixtures can speak HTTP over an in-memory pipe.
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", b.header)
	if b.base == nil {
		return proxyAwarePlain{}.RoundTrip(req)
	}
	return b.base.RoundTrip(req)
}

// proxyAwarePlain is a plain HTTP transport for the fixtures' http://gateway.
type proxyAwarePlain struct{}

func (proxyAwarePlain) RoundTrip(req *http.Request) (*http.Response, error) {
	return (&http.Transport{}).RoundTrip(req)
}

// ensure the fixture compiles against the gateway's interfaces.
var _ gateway.UserStore = (*channelstore.Store)(nil)
