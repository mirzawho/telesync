package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

// Account metadata errors are shared with the identity-aware readiness probe.
var (
	// ErrAccountIdentityMismatch means the socket answered, but its worker
	// reports metadata for an account other than the one being supervised.
	ErrAccountIdentityMismatch = errors.New("supervisor: worker account metadata does not match")
	// ErrAccountReadinessUnsupported means an account worker cannot be admitted
	// using a prober that only checks whether some socket answers.
	ErrAccountReadinessUnsupported = errors.New("supervisor: account readiness requires an identity-aware probe")
	// ErrAccountMetadataConflict means an account id is already supervised with
	// different trusted owner/server metadata.
	ErrAccountMetadataConflict = errors.New("supervisor: account id is already bound to different metadata")
	// ErrSupervisorStopped means the supervisor has closed account admission.
	ErrSupervisorStopped = errors.New("supervisor: account admission is closed")
)

// AccountMetadata is the non-secret identity a worker must report before it is
// considered ready. Server credentials and endpoint details are deliberately
// excluded.
type AccountMetadata struct {
	AccountID   string
	OwnerUserID int64
	ServerKey   string
	NetworkType string
}

// AccountReadyProber confirms both that an account worker answers and that it
// reports the trusted identity expected by the supervisor.
type AccountReadyProber interface {
	ReadyAccount(ctx context.Context, socketPath string, expected AccountMetadata) error
}

// Server is a custom backend's connection configuration, carried by the account
// that uses it. It mirrors appconfig.Server so the supervisor can check an
// account's isolation without importing the application configuration.
//
// An official account leaves this nil: official profiles must keep gotd's DC,
// resolver, DC-list and public-key options at zero.
type Server struct {
	Host             string
	Port             int
	DCID             int
	RSAPublicKeyPath string
	Transport        string
	Routing          string
	Obfuscated       bool
}

// Endpoint is the host:port this backend is reached at.
func (s Server) Endpoint() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// AccountRef is one user-owned authenticated Telegram account, as the
// supervisor is told about it.
//
// The account is the worker's identity. Nothing here is a client key: the
// server definition (ServerKey) says which backend to reach, and the account id
// says whose session is being used. AppID and APIHash always come from the
// server definition, never from the account.
type AccountRef struct {
	// ID is the opaque account id: the authoritative session identity.
	ID string
	// OwnerUserID is the Telesync user that owns this account.
	OwnerUserID int64
	// ServerKey names the server definition: "official" or "cat".
	ServerKey string
	// NetworkType is "telegram" or "custom".
	NetworkType string
	// Server holds the custom-backend connection configuration when the
	// account is on a custom server. Nil for official.
	Server *Server
	// AppID and APIHash come from the server definition, never from the account.
	AppID   int
	APIHash string
}

// ScopeKey is the key this account's worker, session, peer cache and update
// state are scoped by. It is the account id and nothing else.
func (a AccountRef) ScopeKey() string { return a.ID }

// Metadata returns the safe identity the worker must report at readiness.
func (a AccountRef) Metadata() AccountMetadata {
	return AccountMetadata{
		AccountID: a.ID, OwnerUserID: a.OwnerUserID,
		ServerKey: a.ServerKey, NetworkType: a.NetworkType,
	}
}

// Network types an account may name.
const (
	NetworkTelegram = "telegram"
	NetworkCustom   = "custom"
)

// Valid reports whether this is a complete, isolated account reference.
//
// It refuses what would break the isolation rules: an account with no identity,
// an account with no owner (a quarantined legacy row must never get a worker),
// and a custom account whose backend is not fully specified — a custom
// connection must set every endpoint, DC and key explicitly and must never fall
// back to Telegram's own infrastructure.
func (a AccountRef) Valid() error {
	if !isOpaqueID(a.ID) {
		return errors.New("supervisor: account id is not an opaque account id")
	}
	if a.OwnerUserID <= 0 {
		return fmt.Errorf("supervisor: account %s has no owner; an ownerless account never gets a worker", a.ID)
	}
	if strings.TrimSpace(a.ServerKey) == "" {
		return fmt.Errorf("supervisor: account %s names no server definition", a.ID)
	}
	if a.AppID <= 0 || a.APIHash == "" {
		return fmt.Errorf("supervisor: account %s is missing the server definition's app id or api hash", a.ID)
	}
	switch a.NetworkType {
	case NetworkTelegram:
		if a.Server != nil {
			return fmt.Errorf("supervisor: account %s is an official account but carries a custom endpoint", a.ID)
		}
	case NetworkCustom:
		if err := a.Server.valid(a.ID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("supervisor: account %s has an unknown network type %q", a.ID, a.NetworkType)
	}
	return nil
}

// valid checks that a custom backend names every connection setting, so a
// custom account can never be dialled against Telegram's own servers, its CDN,
// its bundled keys or another client's endpoint.
func (s *Server) valid(accountID string) error {
	if s == nil {
		return fmt.Errorf("supervisor: account %s is on a custom server but carries no server block", accountID)
	}
	switch {
	case s.Host == "":
		return fmt.Errorf("supervisor: account %s has no custom endpoint host", accountID)
	case strings.Contains(s.Host, "://") || strings.ContainsAny(s.Host, "/ \t"):
		return fmt.Errorf("supervisor: account %s has an invalid custom endpoint host", accountID)
	case s.Port < 1 || s.Port > 65535:
		return fmt.Errorf("supervisor: account %s has no usable custom endpoint port", accountID)
	case s.DCID < 1 || s.DCID > 5:
		return fmt.Errorf("supervisor: account %s names no data centre", accountID)
	case s.RSAPublicKeyPath == "":
		return fmt.Errorf("supervisor: account %s names no public key; a custom backend's keys are never taken from Telegram's", accountID)
	}
	return nil
}

// Paths are the three per-account files a worker owns.
type Paths struct {
	SocketPath    string
	SessionPath   string
	PeerCachePath string
}

// SocketPathLimit is the longest Unix socket path a platform accepts, including
// the trailing NUL. macOS is 104 bytes and most Linux builds 108, so the
// smaller usable length is used and a name that fits here fits everywhere.
const SocketPathLimit = 103

// accountIDPattern is what an opaque account id may be made of. It is a check
// against garbage and against a name that could escape its directory, not a
// security boundary: the id is server-generated and no caller chooses it. What
// it does enforce is that a username, a phone number or a path can never be
// used where an account id belongs.
func isOpaqueID(id string) bool {
	const suffixLength = 26
	if len(id) != len("acc_")+suffixLength || !strings.HasPrefix(id, "acc_") {
		return false
	}
	const crockford = "0123456789abcdefghjkmnpqrstvwxyz"
	for i := len("acc_"); i < len(id); i++ {
		if !strings.ContainsRune(crockford, rune(id[i])) {
			return false
		}
	}
	return true
}

// AccountPaths derives one account's private files from its opaque id.
//
// The three files are named from the account id and from nothing else: never a
// username, never a phone number, never a client key. The guards from
// appconfig are applied here too, so a derived path can never be somewhere it
// must not be:
//
//   - the socket is inside the run directory,
//   - the sealed session is not inside the run directory (it is a credential),
//   - the session path and the socket path differ,
//   - and the socket name fits the platform's Unix socket path limit, falling
//     back to a short stable hash when the readable name would not.
func AccountPaths(runDir, sessionDir, peerDir, accountID string) (Paths, error) {
	if !isOpaqueID(accountID) {
		return Paths{}, errors.New("supervisor: account id is not an opaque account id")
	}
	for name, dir := range map[string]string{
		"run directory":     runDir,
		"session directory": sessionDir,
		"peer directory":    peerDir,
	} {
		if dir == "" {
			return Paths{}, fmt.Errorf("supervisor: the %s is empty", name)
		}
		if strings.Contains(dir, "..") {
			return Paths{}, fmt.Errorf("supervisor: the %s %q contains %q", name, dir, "..")
		}
	}

	socket, err := socketPath(runDir, accountID)
	if err != nil {
		return Paths{}, err
	}
	p := Paths{
		SocketPath:    socket,
		SessionPath:   filepath.Clean(filepath.Join(sessionDir, accountID+".box")),
		PeerCachePath: filepath.Clean(filepath.Join(peerDir, accountID+".json")),
	}

	if !Within(runDir, p.SocketPath) {
		return Paths{}, fmt.Errorf("supervisor: the socket %q is outside the run directory %q", p.SocketPath, runDir)
	}
	if Within(runDir, p.SessionPath) {
		return Paths{}, fmt.Errorf("supervisor: the session %q must not live in the run directory %q (it is a credential, not a runtime file)",
			p.SessionPath, runDir)
	}
	if !Within(sessionDir, p.SessionPath) {
		return Paths{}, fmt.Errorf("supervisor: the session %q is outside the session directory %q", p.SessionPath, sessionDir)
	}
	if !Within(peerDir, p.PeerCachePath) {
		return Paths{}, fmt.Errorf("supervisor: the peer cache %q is outside the peer directory %q", p.PeerCachePath, peerDir)
	}
	if p.SessionPath == p.SocketPath {
		return Paths{}, fmt.Errorf("supervisor: the session path and the socket path must differ")
	}
	return p, nil
}

// socketPath names one account's RPC socket, keeping the readable account name
// when it fits the platform limit and falling back to a short hash of the id
// when it does not.
//
// A Unix socket path is bounded (104 bytes on macOS, 108 on most Linux builds),
// so a long run directory plus a long account id would silently fail to bind.
// The hash is of the account id only, so it is stable across restarts and
// distinct per account.
func socketPath(runDir, accountID string) (string, error) {
	dir := filepath.Clean(runDir)
	budget := SocketPathLimit - len(dir) - 1 - len(".sock")
	if budget < minSocketName {
		return "", fmt.Errorf("supervisor: run directory %q is too long to hold a socket name (%d bytes of the %d available)",
			dir, budget, SocketPathLimit)
	}
	name := accountID
	if len(name) > budget {
		sum := sha256.Sum256([]byte(accountID))
		name = "acc-" + hex.EncodeToString(sum[:])[:shortHashLen]
		if len(name) > budget {
			name = name[:budget]
		}
	}
	return filepath.Join(dir, name+".sock"), nil
}

const (
	// minSocketName is the shortest hashed socket name worth using: enough hex
	// to keep two accounts apart.
	minSocketName = 16
	// shortHashLen is how much of the account id's digest becomes the socket
	// name. 24 hex characters are 96 bits, far more than a deployment with a
	// bounded worker budget can collide on.
	shortHashLen = 24
)

// Within reports whether path is dir itself or inside it. It mirrors
// appconfig's guard exactly, so a path the configuration would refuse is also
// refused here.
func Within(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
