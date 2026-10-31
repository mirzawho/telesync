// Package secrets seals credentials that must be read back later.
//
// A password stored here is never hashed: a future WordPress request has to
// present it, so it is encrypted with AES-256-GCM instead — authenticated, so a
// tampered ciphertext is refused rather than opened, and versioned, so the
// format can change without a migration of every row. The key lives outside the
// database, in the configuration's environment variable.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors, matched with errors.Is.
var (
	// ErrKey means a key was given but is not a valid 32-byte AES-256 key.
	ErrKey = errors.New("secrets: invalid key")
	// ErrKeyNotSet means the variable that should hold the key is empty.
	ErrKeyNotSet = errors.New("secrets: key is not set")
	// ErrDecrypt means the box is malformed, was sealed with another key, or
	// was changed after sealing. The three are deliberately one answer.
	ErrDecrypt = errors.New("secrets: cannot decrypt")
)

// Version1 is the first encrypted-box format: AES-256-GCM with a 12-byte nonce.
const Version1 = 1

// KeySize is the only key length accepted.
const KeySize = 32

// Box is one sealed value, as stored.
type Box struct {
	// Version is the format the box was sealed with.
	Version int
	// Nonce is the per-seal GCM nonce. It is not a secret, but it must never
	// repeat under one key; Seal draws a fresh one every time.
	Nonce []byte
	// Ciphertext is the sealed value with its GCM tag.
	Ciphertext []byte
	// SealedAt is when Seal wrote the box.
	SealedAt time.Time
}

// Plaintext reports whether the box holds an unencrypted value.
//
// Nothing produces one, and Open refuses it: a row written this way is either
// a bug or an attempt to make an unprotected credential look sealed.
func (b Box) Plaintext() bool { return false }

// Encrypter seals and opens credentials with one key.
type Encrypter struct {
	aead cipher.AEAD
	now  func() time.Time
}

// NewEncrypter validates a key. Only 32 bytes (AES-256) is accepted: a shorter
// key would silently weaken every credential already sealed.
func NewEncrypter(key []byte) (*Encrypter, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: it must be %d bytes (base64-encoded, a 44-character string)", ErrKey, KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKey, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKey, err)
	}
	return &Encrypter{aead: aead, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Seal encrypts one value.
func (e *Encrypter) Seal(plaintext string) (Box, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Box{}, fmt.Errorf("secrets: draw a nonce: %w", err)
	}
	return Box{
		Version:    Version1,
		Nonce:      nonce,
		Ciphertext: e.aead.Seal(nil, nonce, []byte(plaintext), nil),
		SealedAt:   e.now(),
	}, nil
}

// Open decrypts one value.
func (e *Encrypter) Open(box Box) (string, error) {
	if box.Version != Version1 {
		return "", fmt.Errorf("%w: unsupported box version %d", ErrDecrypt, box.Version)
	}
	if len(box.Nonce) != e.aead.NonceSize() || len(box.Ciphertext) <= e.aead.Overhead() {
		return "", fmt.Errorf("%w: the box is malformed", ErrDecrypt)
	}
	plaintext, err := e.aead.Open(nil, box.Nonce, box.Ciphertext, nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plaintext), nil
}

// ParseKey turns a configured key value into a key.
//
// The value may be the base64 encoding of 32 bytes (what
// `openssl rand -base64 32` prints), their hex encoding (what
// `openssl rand -hex 32` prints), or the 32 raw bytes themselves. Anything
// else — a longer passphrase included — is refused rather than hashed into a
// key. Nothing about the value is ever printed.
func ParseKey(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%w: it is empty", ErrKey)
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == KeySize {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == KeySize {
		return decoded, nil
	}
	if len(value) == KeySize {
		return []byte(value), nil
	}
	return nil, fmt.Errorf("%w: it must be %d bytes, base64- or hex-encoded or raw "+
		"(generate one with: openssl rand -base64 %d)", ErrKey, KeySize, KeySize)
}

// KeyFromEnv reads the key from the variable variable.
//
// A missing variable is ErrKeyNotSet, and the error names the variable, never
// its value.
func KeyFromEnv(variable string, lookup func(string) string) ([]byte, error) {
	value := ""
	if lookup != nil {
		value = lookup(variable)
	}
	if value == "" {
		return nil, fmt.Errorf("%w: %s is not set", ErrKeyNotSet, variable)
	}
	key, err := ParseKey(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", variable, err)
	}
	return key, nil
}
