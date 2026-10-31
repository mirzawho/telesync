// Package keys loads a custom backend's RSA public keys.
//
// Keys come exclusively from the configured PEM file. The bundled Telegram keys
// are never substituted, not even when the file is missing or unusable.
package keys

import (
	"fmt"
	"os"
	"strings"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/exchange"
)

// ErrNotConfigured means the public key path is empty.
type ErrNotConfigured struct{ Path string }

// Error implements error.
func (e ErrNotConfigured) Error() string {
	return fmt.Sprintf("custom backend RSA public key is not configured: set server.rsa_public_key_path "+
		"to a PEM file holding the backend's RSA public key (path was %q)", e.Path)
}

// ErrEmpty means the file exists but contains nothing usable.
type ErrEmpty struct{ Path string }

// Error implements error.
func (e ErrEmpty) Error() string {
	return fmt.Sprintf("custom backend RSA public key file %q is empty: write the backend's "+
		"PEM-encoded RSA public key there (a public key only; never the private key)", e.Path)
}

// ErrInvalid means the file is not a usable RSA public key.
type ErrInvalid struct {
	Path   string
	Reason string
}

// Error implements error.
func (e ErrInvalid) Error() string {
	return fmt.Sprintf("custom backend RSA public key file %q is not a usable RSA public key: %s; "+
		"expected a PEM block (\"BEGIN RSA PUBLIC KEY\" or \"BEGIN PUBLIC KEY\") containing an RSA key",
		e.Path, e.Reason)
}

// Load reads PEM-encoded RSA public keys from path.
func Load(path string) ([]exchange.PublicKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrNotConfigured{Path: path}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("custom backend RSA public key file %q does not exist", path)
		}
		return nil, fmt.Errorf("read custom backend RSA public key file %q: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, ErrEmpty{Path: path}
	}

	rsaKeys, err := crypto.ParseRSAPublicKeys(data)
	if err != nil {
		return nil, ErrInvalid{Path: path, Reason: err.Error()}
	}

	out := make([]exchange.PublicKey, 0, len(rsaKeys))
	for _, k := range rsaKeys {
		if k == nil || k.N == nil || k.N.Sign() == 0 {
			continue
		}
		out = append(out, exchange.PublicKey{RSA: k})
	}
	if len(out) == 0 {
		return nil, ErrInvalid{Path: path, Reason: "no RSA public key found in any PEM block"}
	}
	return out, nil
}

// Fingerprints renders key fingerprints for diagnostics.
func Fingerprints(keys []exchange.PublicKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k.Zero() {
			out = append(out, "(zero key)")
			continue
		}
		out = append(out, fmt.Sprintf("%016x", uint64(k.Fingerprint())))
	}
	return out
}
