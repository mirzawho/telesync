package supervisor_test

import (
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/supervisor"
)

// The account is the worker's identity, so every path it owns is derived from
// the short opaque account id. Nothing here may be derived from a username, a
// phone number or a client key: those are not identities and they are not
// opaque.

func TestAccountPathsAreDerivedFromTheOpaqueAccountID(t *testing.T) {
	const id = "acc_01h9x3q7m2k4v6b8n0p2r4t6w8"
	p, err := supervisor.AccountPaths("/run", "/var/lib/telesync/sessions",
		"/var/lib/telesync/peers", id)
	if err != nil {
		t.Fatalf("AccountPaths: %v", err)
	}
	if want := "/run/" + id + ".sock"; p.SocketPath != want {
		t.Errorf("socket = %q, want %q", p.SocketPath, want)
	}
	if want := "/var/lib/telesync/sessions/" + id + ".box"; p.SessionPath != want {
		t.Errorf("session = %q, want %q", p.SessionPath, want)
	}
	if want := "/var/lib/telesync/peers/" + id + ".json"; p.PeerCachePath != want {
		t.Errorf("peer cache = %q, want %q", p.PeerCachePath, want)
	}
}

func TestAccountPathsRefuseAnIdentityThatIsNotOpaque(t *testing.T) {
	// A path separator in an identity would let one account escape the
	// directory it owns.
	for _, id := range []string{
		"",
		"../etc/passwd",
		"acc/../other",
		"acc_x/y",
		"..",
		"/abs",
	} {
		if _, err := supervisor.AccountPaths("/run", "/sessions", "/peers", id); err == nil {
			t.Errorf("AccountPaths(%q) accepted an identity that can escape its directory", id)
		}
	}
}

func TestAccountPathsRefuseTraversalInTheRoots(t *testing.T) {
	const id = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := supervisor.AccountPaths("/run/../etc", "/sessions", "/peers", id); err == nil {
		t.Error("a run directory containing .. must be refused")
	}
	if _, err := supervisor.AccountPaths("/run", "/sessions/../secrets", "/peers", id); err == nil {
		t.Error("a session directory containing .. must be refused")
	}
}

func TestAccountPathsEnforceTheAppconfigGuards(t *testing.T) {
	p, err := supervisor.AccountPaths("/run", "/sessions", "/peers", "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("AccountPaths: %v", err)
	}

	// The socket lives in the run directory and nowhere else.
	if !supervisor.Within("/run", p.SocketPath) {
		t.Errorf("socket %q is not inside the run directory", p.SocketPath)
	}
	// A session is a credential, so it must not live in the run directory.
	if supervisor.Within("/run", p.SessionPath) {
		t.Errorf("session %q must not be inside the run directory", p.SessionPath)
	}
	if !supervisor.Within("/sessions", p.SessionPath) {
		t.Errorf("session %q is not inside the session directory", p.SessionPath)
	}
	if !supervisor.Within("/peers", p.PeerCachePath) {
		t.Errorf("peer cache %q is not inside the peer directory", p.PeerCachePath)
	}
	// A session path and a socket path are different resources.
	if p.SessionPath == p.SocketPath {
		t.Error("session path and socket path must differ")
	}
}

func TestTwoAccountsNeverShareASessionPathOrASocket(t *testing.T) {
	a, err := supervisor.AccountPaths("/run", "/sessions", "/peers", "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	b, err := supervisor.AccountPaths("/run", "/sessions", "/peers", "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionPath == b.SessionPath {
		t.Errorf("two accounts share a session path %q", a.SessionPath)
	}
	if a.SocketPath == b.SocketPath {
		t.Errorf("two accounts share a socket path %q", a.SocketPath)
	}
	if a.PeerCachePath == b.PeerCachePath {
		t.Errorf("two accounts share a peer cache path %q", a.PeerCachePath)
	}
}

func TestAccountSocketNamesStayWithinThePlatformPathLimit(t *testing.T) {
	// A Unix socket path is bounded (~104 bytes on macOS, 108 on Linux). A
	// long run directory must not push the socket past that: the name falls
	// back to a short hash instead.
	longRun := "/tmp/" + strings.Repeat("d", 60) + "/run"
	longID := "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	p, err := supervisor.AccountPaths(longRun, "/sessions", "/peers", longID)
	if err != nil {
		t.Fatalf("AccountPaths: %v", err)
	}
	if limit := supervisor.SocketPathLimit; len(p.SocketPath) > limit {
		t.Errorf("socket path is %d bytes, over the %d byte platform limit: %s",
			len(p.SocketPath), limit, p.SocketPath)
	}
	if base := strings.TrimSuffix(p.SocketPath[strings.LastIndex(p.SocketPath, "/")+1:], ".sock"); base == longID {
		t.Error("an over-long account id was used raw as the socket name")
	}
}

func TestAShortAccountIDKeepsItsReadableSocketName(t *testing.T) {
	const id = "acc_01h9x3q7m2k4v6b8n0p2r4t6w8"
	p, err := supervisor.AccountPaths("/run", "/sessions", "/peers", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p.SocketPath, id+".sock") {
		t.Errorf("socket %q must keep the readable account name when it fits", p.SocketPath)
	}
}

func TestTheHashedSocketNameIsStableAndDistinct(t *testing.T) {
	long := func(s string) string { return "/tmp/" + strings.Repeat("d", 60) + "/run" }
	first, err := supervisor.AccountPaths(long(""), "/s", "/p", "acc_"+strings.Repeat("a", 26))
	if err != nil {
		t.Fatal(err)
	}
	again, err := supervisor.AccountPaths(long(""), "/s", "/p", "acc_"+strings.Repeat("a", 26))
	if err != nil {
		t.Fatal(err)
	}
	other, err := supervisor.AccountPaths(long(""), "/s", "/p", "acc_"+strings.Repeat("b", 26))
	if err != nil {
		t.Fatal(err)
	}
	if first.SocketPath != again.SocketPath {
		t.Errorf("the socket name must be stable: %q vs %q", first.SocketPath, again.SocketPath)
	}
	if first.SocketPath == other.SocketPath {
		t.Errorf("two different accounts hashed to one socket name %q", first.SocketPath)
	}
}

func TestAccountRefIdentityIsTheAccountID(t *testing.T) {
	ref := supervisor.AccountRef{
		ID:          "acc_01h9x3q7m2k4v6b8n0p2r4t6w8",
		OwnerUserID: 7,
		ServerKey:   "official",
		NetworkType: "telegram",
		AppID:       12345,
		APIHash:     "hash",
	}
	if got := ref.ScopeKey(); got != ref.ID {
		t.Errorf("ScopeKey() = %q, want the account id %q", got, ref.ID)
	}
	if ref.ID == ref.ServerKey {
		t.Error("the account id must not be the server key")
	}
}

func TestAccountRefValidationRefusesAnIncompleteIdentity(t *testing.T) {
	ok := supervisor.AccountRef{
		ID:          "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerUserID: 1,
		ServerKey:   "official",
		NetworkType: "telegram",
		AppID:       1,
		APIHash:     "hash",
	}
	if err := ok.Valid(); err != nil {
		t.Fatalf("a complete account ref must validate: %v", err)
	}

	broken := ok
	broken.ID = ""
	if err := broken.Valid(); err == nil {
		t.Error("an account without an id must be refused")
	}
	broken = ok
	broken.ServerKey = ""
	if err := broken.Valid(); err == nil {
		t.Error("an account without a server definition must be refused")
	}
	broken = ok
	broken.NetworkType = "telepathy"
	if err := broken.Valid(); err == nil {
		t.Error("an unknown network type must be refused")
	}
	broken = ok
	broken.AppID = 0
	if err := broken.Valid(); err == nil {
		t.Error("an account must carry the server definition's app id")
	}
	// A quarantined legacy account has no owner and must never start a worker.
	broken = ok
	broken.OwnerUserID = 0
	if err := broken.Valid(); err == nil {
		t.Error("an ownerless account must be refused")
	}
}

// A custom backend's connection configuration is carried explicitly by the
// account. It is never allowed to fall back to Telegram's own servers, so a
// custom account without a complete server block is refused rather than
// silently dialled against official Telegram.
func TestCustomAccountRequiresEveryEndpointSetting(t *testing.T) {
	ref := supervisor.AccountRef{
		ID:          "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerUserID: 1,
		ServerKey:   "cat",
		NetworkType: "custom",
		AppID:       1,
		APIHash:     "hash",
	}
	if err := ref.Valid(); err == nil {
		t.Fatal("a custom account without a server block must be refused")
	}

	ref.Server = &supervisor.Server{Host: "10.0.0.1", Port: 443}
	if err := ref.Valid(); err == nil {
		t.Error("a custom account without a DC must be refused")
	}
	ref.Server.DCID = 2
	if err := ref.Valid(); err == nil {
		t.Error("a custom account without a public key must be refused")
	}
	ref.Server.RSAPublicKeyPath = "/etc/telesync/keys/cat.pem"
	if err := ref.Valid(); err != nil {
		t.Fatalf("a fully specified custom account must validate: %v", err)
	}
	if got, want := ref.Server.Endpoint(), "10.0.0.1:443"; got != want {
		t.Errorf("Endpoint() = %q, want %q", got, want)
	}
}

func TestAnOfficialAccountNeverCarriesACustomEndpoint(t *testing.T) {
	ref := supervisor.AccountRef{
		ID:          "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerUserID: 1,
		ServerKey:   "official",
		NetworkType: "telegram",
		AppID:       1,
		APIHash:     "hash",
	}
	if err := ref.Valid(); err != nil {
		t.Fatalf("an official account must validate: %v", err)
	}
	if ref.Server != nil {
		t.Error("an official account must leave the custom server block at zero")
	}
}
