package worker

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mirza/telesync/internal/appconfig"
)

// This file is the worker's account identity.
//
// A worker is one user-owned Telegram account. It carries the account's identity
// from the verified account record — never from an RPC payload — and everything
// it owns (session, peer cache, pagination cursors, update state) is scoped by
// that one opaque id.

// AccountIdentity is the verified account record a worker is bound to.
//
// Every field comes from the store's account row. A client-supplied owner in a
// request body is never an authorization decision and is ignored wherever it
// appears.
type AccountIdentity struct {
	// ID is the opaque account id: the authoritative session identity.
	ID string
	// OwnerUserID is the Telesync user that owns this account.
	OwnerUserID int64
	// ServerKey names the server definition: "official" or "cat".
	ServerKey string
	// NetworkType is "telegram" or "custom".
	NetworkType string
	// RemoteUserID is the authenticated Telegram user id, from the verified
	// account record. It is never taken from a transport callback.
	RemoteUserID int64
	// SessionRef is this account's sealed session file. It must name this
	// account and no other.
	SessionRef string
	// PhoneMasked is the masked phone on the account row. It is never an
	// identity and is never logged.
	PhoneMasked string
	// RemoteUsername is the Telegram handle. It is never an identity and is
	// never logged.
	RemoteUsername string
}

// Network types an account may name.
const (
	NetworkTelegram = "telegram"
	NetworkCustom   = "custom"
)

// ScopeKey is the key this worker's caches and state are scoped by.
func (a AccountIdentity) ScopeKey() string { return a.ID }

// LogSafe is the only form of this identity that may be printed: the opaque
// account id. A phone number, a username and a session path are not identities
// and are not safe to log.
func (a AccountIdentity) LogSafe() string { return a.ID }

// Valid reports whether this is a complete, self-consistent account identity.
func (a AccountIdentity) Valid() error {
	if !isAccountID(a.ID) {
		return fmt.Errorf("worker: account id is not an opaque account id")
	}
	if a.OwnerUserID <= 0 {
		return fmt.Errorf("worker: account %s has no owner", a.ID)
	}
	if a.ServerKey == "" {
		return fmt.Errorf("worker: account %s names no server definition", a.ID)
	}
	switch a.NetworkType {
	case NetworkTelegram, NetworkCustom:
	default:
		return fmt.Errorf("worker: account %s has an unknown network type %q", a.ID, a.NetworkType)
	}
	if a.RemoteUserID <= 0 {
		return fmt.Errorf("worker: account %s has no authenticated remote user", a.ID)
	}
	if a.SessionRef == "" {
		return fmt.Errorf("worker: account %s has no sealed session", a.ID)
	}
	if !filepath.IsAbs(a.SessionRef) || filepath.Clean(a.SessionRef) != a.SessionRef {
		return fmt.Errorf("worker: account %s has an invalid sealed session reference", a.ID)
	}
	// The session is the account's. A reference that names a different account
	// is how one account's credentials would be opened by another's worker, so
	// it is refused outright rather than corrected.
	if base := filepath.Base(a.SessionRef); base != a.ID+".box" {
		return fmt.Errorf("worker: account %s cannot use a session reference belonging to another account", a.ID)
	}
	return nil
}

// isAccountID reports whether id has the shape of an opaque account id. It is a
// check against garbage, not a boundary: the id is server-generated and no
// caller chooses it. What it does enforce is that a username, a phone number or
// a server key can never be mistaken for one.
func isAccountID(id string) bool {
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

// NewServiceForAccount builds a worker service bound to one account.
//
// The account is the worker's identity for the life of the process. It refuses
// an incomplete identity and a session reference that names a different
// account: one account's session must never be opened by another's worker.
func NewServiceForAccount(id AccountIdentity, tg Telegram, opt Options, adjust ...Option) (*Service, error) {
	if err := id.Valid(); err != nil {
		return nil, err
	}
	s := newService(opt, tg, adjust...)
	s.identity = &id
	s.client.Key = id.ServerKey
	s.client.Type = appconfigType(id.NetworkType)
	s.client.SessionPath = id.SessionRef
	return s, nil
}

// logName is the identity a worker log line names: the opaque account id when
// the worker serves an account, and the configured client key only on the
// legacy path. A phone number, a username and a session path are never
// identities and are never printed.
func (s *Service) logName() string {
	if s.identity != nil {
		return s.identity.LogSafe()
	}
	return s.client.Key
}

// Account is the identity this worker is bound to. A worker built without one
// (the legacy configured-client path) reports a zero identity.
func (s *Service) Account() AccountIdentity {
	if s.identity == nil {
		return AccountIdentity{}
	}
	return *s.identity
}

// appconfigType maps a network type onto the configured client type, so the
// legacy fields a worker reports stay consistent with the account's identity.
func appconfigType(networkType string) appconfig.Type {
	if networkType == NetworkCustom {
		return appconfig.Custom
	}
	return appconfig.Official
}
