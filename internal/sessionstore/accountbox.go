package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/mirza/telesync/internal/secrets"
)

// ErrSessionNotFound means the account has no stored session yet. It is the
// sealed equivalent of gotd's session.ErrNotFound, so callers can keep the
// "this account is not signed in" branch they already have.
var ErrSessionNotFound = session.ErrNotFound

// ErrPhoneCodeHashNotFound means this account has no private code-round handle
// stored. It is local attempt state, never a credential returned to a caller.
var ErrPhoneCodeHashNotFound = errors.New("sessionstore: phone code hash not found")

// accountIDPattern is the opaque server-generated account id from the v0.0.5
// identity contract: `acc_` plus 26 base32 characters.
//
// A session path is derived from this and nothing else — never a username, a
// phone number or a display name — so a path cannot leak who the account is and
// cannot be steered out of the session directory.
var accountIDPattern = regexp.MustCompile(`^acc_[0-9a-hjkmnp-tv-z]{26}$`)

// boxSuffix names the sealed session file of one account.
const boxSuffix = ".box"

const phoneCodeHashSuffix = ".login"

// boxFormat is the versioned file format wrapping one sealed session box.
//
// The session bytes themselves are never written: only the sealed box is, in
// the same version/nonce/ciphertext shape the WordPress credentials use.
type boxFormat struct {
	FormatVersion int    `json:"format_version"`
	AccountID     string `json:"account_id"`
	NetworkKey    string `json:"network_key"`
	NetworkType   string `json:"network_type"`
	KeyVersion    int    `json:"key_version"`

	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
	SealedAt   string `json:"sealed_at"`
}

// privateHashFormat stores the server-issued phone_code_hash separately from
// the public login-attempt row. The value is encrypted with account AAD.
type privateHashFormat struct {
	FormatVersion int    `json:"format_version"`
	Version       int    `json:"version"`
	Nonce         []byte `json:"nonce"`
	Ciphertext    []byte `json:"ciphertext"`
	SealedAt      string `json:"sealed_at"`
}

// AccountBoxConfig names one account and the context its session is sealed in.
type AccountBoxConfig struct {
	// Dir is the per-account session directory. It is created 0700.
	Dir string
	// AccountID is the opaque account id. It is the only thing a path is
	// derived from.
	AccountID string
	// Encrypter seals and opens the session. It must be non-nil.
	Encrypter *secrets.Encrypter
	// KeyVersion is the encryption key generation this box is sealed with. It
	// is part of the AAD, so a rotated key cannot open an old box by mistake.
	KeyVersion int
	// NetworkKey is the server definition ("official" or "cat").
	NetworkKey string
	// NetworkType is the connection kind ("telegram" or "custom").
	NetworkType string
}

// AccountBox stores one account's MTProto session as a sealed box in its own
// file, reachable only under that account's identity.
//
// It implements gotd's session.Storage, so a worker can use it directly and
// never sees another account's bytes. LoadSession and StoreSession move
// plaintext session bytes in memory only: what reaches disk is always sealed.
type AccountBox struct {
	mux         sync.Mutex
	store       *Store
	accountID   string
	networkKey  string
	networkType string
	keyVersion  int
	enc         *secrets.Encrypter
}

// NewAccountBox returns a sealed per-account session store.
//
// The configuration is checked against the session already on disk, if any: a
// session written for one server definition or network kind is never reopened
// under another, because that would be one account acting as two clients.
func NewAccountBox(cfg AccountBoxConfig) (*AccountBox, error) {
	if cfg.Encrypter == nil {
		return nil, errors.New("sessionstore: an encrypter is required")
	}
	if !accountIDPattern.MatchString(cfg.AccountID) {
		return nil, fmt.Errorf("sessionstore: account id %q is not an opaque account id", cfg.AccountID)
	}
	if strings.TrimSpace(cfg.NetworkKey) == "" {
		return nil, errors.New("sessionstore: a server definition is required")
	}
	switch strings.TrimSpace(cfg.NetworkType) {
	case NetworkTelegram, NetworkCustom:
	default:
		return nil, fmt.Errorf("sessionstore: %q is not a network kind", cfg.NetworkType)
	}
	if cfg.KeyVersion < 1 {
		return nil, errors.New("sessionstore: a key version of at least 1 is required")
	}
	if strings.TrimSpace(cfg.Dir) == "" {
		return nil, errors.New("sessionstore: a session directory is required")
	}

	path := filepath.Join(cfg.Dir, cfg.AccountID+boxSuffix)
	store, err := NewAt(path)
	if err != nil {
		return nil, err
	}
	box := &AccountBox{
		store:       store,
		accountID:   cfg.AccountID,
		networkKey:  cfg.NetworkKey,
		networkType: cfg.NetworkType,
		keyVersion:  cfg.KeyVersion,
		enc:         cfg.Encrypter,
	}
	if err := box.matchesExistingFile(); err != nil {
		return nil, err
	}
	return box, nil
}

// Network kinds a session may be bound to.
const (
	NetworkTelegram = "telegram"
	NetworkCustom   = "custom"
)

// Path returns the sealed session file path. It carries the opaque account id
// and nothing else.
func (a *AccountBox) Path() string { return a.store.Path() }

// String describes the store without any key material.
func (a *AccountBox) String() string {
	return fmt.Sprintf("AccountBox{account:%s network:%s/%s path:%s}",
		a.accountID, a.networkKey, a.networkType, filepath.Base(a.store.Path()))
}

// LoadSession implements session.Storage.
func (a *AccountBox) LoadSession(ctx context.Context) ([]byte, error) {
	a.mux.Lock()
	defer a.mux.Unlock()
	return a.load(ctx)
}

func (a *AccountBox) load(_ context.Context) ([]byte, error) {
	raw, err := os.ReadFile(a.store.Path())
	if os.IsNotExist(err) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sessionstore: read the sealed session: %w", err)
	}
	plaintext, err := a.open(raw)
	if err != nil {
		return nil, err
	}
	return []byte(plaintext), nil
}

// StoreSession implements session.Storage, sealing the session under this
// account's context before anything is written.
func (a *AccountBox) StoreSession(ctx context.Context, data []byte) error {
	a.mux.Lock()
	defer a.mux.Unlock()

	box, err := a.enc.SealWithAAD(string(data), a.aad())
	if err != nil {
		return fmt.Errorf("sessionstore: seal the session: %w", err)
	}
	payload, err := a.marshal(box)
	if err != nil {
		return err
	}
	if err := a.store.StoreSession(ctx, payload); err != nil {
		return err
	}
	return nil
}

// StorePhoneCodeHash persists the current server-issued code-round handle as
// private, encrypted account state. It is never stored in the login-attempt
// record or returned to the browser.
func (a *AccountBox) StorePhoneCodeHash(hash string) error {
	if strings.TrimSpace(hash) == "" {
		return errors.New("sessionstore: a phone code hash is required")
	}
	a.mux.Lock()
	defer a.mux.Unlock()

	box, err := a.enc.SealWithAAD(hash, a.phoneCodeHashAAD())
	if err != nil {
		return fmt.Errorf("sessionstore: seal the private code state: %w", err)
	}
	payload, err := json.Marshal(privateHashFormat{
		FormatVersion: boxFormatVersion,
		Version:       box.Version,
		Nonce:         box.Nonce,
		Ciphertext:    box.Ciphertext,
		SealedAt:      box.SealedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("sessionstore: encode the private code state: %w", err)
	}
	store, err := NewAt(a.phoneCodeHashPath())
	if err != nil {
		return err
	}
	if err := store.StoreSession(context.Background(), payload); err != nil {
		return fmt.Errorf("sessionstore: persist the private code state: %w", err)
	}
	return nil
}

// LoadPhoneCodeHash reads the private code-round handle for this account.
func (a *AccountBox) LoadPhoneCodeHash() (string, error) {
	a.mux.Lock()
	defer a.mux.Unlock()

	raw, err := os.ReadFile(a.phoneCodeHashPath())
	if os.IsNotExist(err) {
		return "", ErrPhoneCodeHashNotFound
	}
	if err != nil {
		return "", fmt.Errorf("sessionstore: read the private code state: %w", err)
	}
	var payload privateHashFormat
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("%w: the private code state is not a readable box", secrets.ErrDecrypt)
	}
	if payload.FormatVersion != boxFormatVersion {
		return "", fmt.Errorf("%w: unsupported private code state format", secrets.ErrDecrypt)
	}
	return a.enc.OpenWithAAD(secrets.Box{
		Version:    payload.Version,
		Nonce:      payload.Nonce,
		Ciphertext: payload.Ciphertext,
	}, a.phoneCodeHashAAD())
}

// ClearPhoneCodeHash removes the private code-round handle. It is idempotent.
func (a *AccountBox) ClearPhoneCodeHash() error {
	a.mux.Lock()
	defer a.mux.Unlock()
	if err := os.Remove(a.phoneCodeHashPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sessionstore: clear the private code state: %w", err)
	}
	return nil
}

// Discard removes both temporary session material and its private login state.
// It is used only when a login does not complete.
func (a *AccountBox) Discard() error {
	a.mux.Lock()
	defer a.mux.Unlock()
	for _, path := range []string{a.store.Path(), a.phoneCodeHashPath()} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("sessionstore: discard temporary account state: %w", err)
		}
	}
	return nil
}

// WriteBox stores an already-sealed box whose context matches this account.
//
// A box sealed under no context, or under another account, is refused rather
// than adopted.
func (a *AccountBox) WriteBox(box secrets.Box) error {
	a.mux.Lock()
	defer a.mux.Unlock()

	if box.Version != secrets.Version1 {
		return fmt.Errorf("sessionstore: the box is not in a supported format")
	}
	// Prove the box belongs to this account before it becomes this account's
	// session: the AAD is what binds it.
	raw, err := a.marshal(box)
	if err != nil {
		return err
	}
	if _, err := a.open(raw); err != nil {
		return err
	}
	return a.store.StoreSession(context.Background(), raw)
}

// OpenFile opens one sealed session file under this account's context.
//
// It exists so a caller can be pointed at a specific file and still not be able
// to read another account's session: the context, not the path, decides.
func (a *AccountBox) OpenFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("sessionstore: the session file path is empty")
	}
	if !strings.HasSuffix(path, boxSuffix) {
		return "", fmt.Errorf("sessionstore: %q is not a sealed session file", filepath.Base(path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sessionstore: read the sealed session: %w", err)
	}
	a.mux.Lock()
	defer a.mux.Unlock()
	return a.open(raw)
}

// aad binds the sealed box to this account and key version.
//
// This is what makes "wrong encryption context cannot decrypt another
// account's session" true: a box sealed here cannot be opened anywhere else,
// even with the same key.
func (a *AccountBox) aad() []byte {
	return AccountAAD(a.accountID, a.keyVersion)
}

func (a *AccountBox) phoneCodeHashAAD() []byte {
	return append(AccountAAD(a.accountID, a.keyVersion), []byte("\x00phone-code-hash")...)
}

func (a *AccountBox) phoneCodeHashPath() string {
	return strings.TrimSuffix(a.store.Path(), boxSuffix) + phoneCodeHashSuffix
}

// AccountAAD is the encryption context of one account's session:
// account_id || "\x00" || key_version.
func AccountAAD(accountID string, keyVersion int) []byte {
	return []byte(fmt.Sprintf("%s\x00%d", accountID, keyVersion))
}

func (a *AccountBox) marshal(box secrets.Box) ([]byte, error) {
	payload := boxFormat{
		FormatVersion: boxFormatVersion,
		AccountID:     a.accountID,
		NetworkKey:    a.networkKey,
		NetworkType:   a.networkType,
		KeyVersion:    a.keyVersion,

		Version:    box.Version,
		Nonce:      box.Nonce,
		Ciphertext: box.Ciphertext,
		SealedAt:   box.SealedAt.UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: write the sealed session: %w", err)
	}
	return raw, nil
}

// boxFormatVersion is the first sealed-session file format.
const boxFormatVersion = 1

func (a *AccountBox) open(raw []byte) (string, error) {
	var payload boxFormat
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("%w: the sealed session is not a readable box", secrets.ErrDecrypt)
	}
	if payload.FormatVersion != boxFormatVersion {
		return "", fmt.Errorf("%w: unsupported session format %d", secrets.ErrDecrypt, payload.FormatVersion)
	}
	box := secrets.Box{
		Version:    payload.Version,
		Nonce:      payload.Nonce,
		Ciphertext: payload.Ciphertext,
	}
	return a.enc.OpenWithAAD(box, a.aad())
}

// matchesExistingFile refuses to reuse a session file written for another
// server definition, network kind or key version.
func (a *AccountBox) matchesExistingFile() error {
	raw, err := os.ReadFile(a.store.Path())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sessionstore: read the sealed session: %w", err)
	}
	var payload boxFormat
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil // a malformed file is refused later, when it is opened
	}
	if payload.NetworkKey != "" && payload.NetworkKey != a.networkKey {
		return fmt.Errorf("sessionstore: this session belongs to the %q server definition, not %q",
			payload.NetworkKey, a.networkKey)
	}
	if payload.NetworkType != "" && payload.NetworkType != a.networkType {
		return fmt.Errorf("sessionstore: this session is a %q session, not %q",
			payload.NetworkType, a.networkType)
	}
	// A key version is deliberately not checked here: rotation is a re-seal
	// under a new AAD, and the AAD itself refuses an old box. A caller that
	// points at a file sealed for another key version gets ErrDecrypt, which is
	// the one answer the encryption contract allows.
	return nil
}
