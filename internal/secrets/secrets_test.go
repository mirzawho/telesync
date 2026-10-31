package secrets_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/secrets"
)

// testKey is a 32-byte key for tests. It is not a credential for anything.
func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestRoundTripReturnsThePlaintext(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "abcd EFGH 1234 ijkl MNOP 5678"

	box, err := e.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if box.Plaintext() {
		t.Fatal("the box claims to be plaintext")
	}
	got, err := e.Open(box)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != plaintext {
		t.Fatalf("Open = %q, want %q", got, plaintext)
	}
}

func TestSealNeverCarriesThePlaintext(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "very distinct application password value"
	box, err := e.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	for name, field := range map[string][]byte{
		"ciphertext": box.Ciphertext,
		"nonce":      box.Nonce,
	} {
		if bytes.Contains(field, []byte(plaintext)) {
			t.Fatalf("%s contains the plaintext", name)
		}
		if bytes.Contains(field, []byte("distinct")) {
			t.Fatalf("%s contains part of the plaintext", name)
		}
	}
}

func TestSealUsesAFreshNonce(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.Seal("same plaintext twice")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Seal("same plaintext twice")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("two seals reused the nonce")
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("two seals of the same plaintext produced the same ciphertext")
	}
}

func TestOpenRejectsATamperedCiphertext(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	box, err := e.Seal("application password")
	if err != nil {
		t.Fatal(err)
	}
	tampered := box
	tampered.Ciphertext = append([]byte(nil), box.Ciphertext...)
	tampered.Ciphertext[len(tampered.Ciphertext)/2] ^= 0x01

	if _, err := e.Open(tampered); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("Open(tampered) error = %v, want ErrDecrypt", err)
	}
}

func TestOpenRejectsATamperedNonce(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	box, err := e.Seal("application password")
	if err != nil {
		t.Fatal(err)
	}
	tampered := box
	tampered.Nonce = append([]byte(nil), box.Nonce...)
	tampered.Nonce[0] ^= 0x80

	if _, err := e.Open(tampered); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("Open(tampered nonce) error = %v, want ErrDecrypt", err)
	}
}

func TestAnotherKeyCannotOpenTheBox(t *testing.T) {
	sealer, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	box, err := sealer.Seal("application password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(box); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("Open with another key error = %v, want ErrDecrypt", err)
	}
}

func TestNewEncrypterRefusesAMissingOrShortKey(t *testing.T) {
	for name, key := range map[string][]byte{
		"nil":     nil,
		"empty":   {},
		"16 byte": make([]byte, 16),
		"31 byte": make([]byte, 31),
		"40 byte": make([]byte, 40),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := secrets.NewEncrypter(key); !errors.Is(err, secrets.ErrKey) {
				t.Fatalf("NewEncrypter error = %v, want ErrKey", err)
			}
		})
	}
}

func TestParseKeyAcceptsBothEncodings(t *testing.T) {
	raw := testKey(t)
	fromBase64, err := secrets.ParseKey(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatalf("ParseKey(base64): %v", err)
	}
	if !bytes.Equal(fromBase64, raw) {
		t.Fatal("ParseKey(base64) did not decode the key")
	}
	fromRaw, err := secrets.ParseKey(string(raw))
	if err != nil {
		t.Fatalf("ParseKey(raw): %v", err)
	}
	if !bytes.Equal(fromRaw, raw) {
		t.Fatal("ParseKey(raw) did not keep the key")
	}
	if _, err := secrets.ParseKey(""); !errors.Is(err, secrets.ErrKey) {
		t.Fatalf("ParseKey(\"\") error = %v, want ErrKey", err)
	}
	if _, err := secrets.ParseKey(strings.Repeat("s", 32)); err != nil {
		t.Fatalf("ParseKey(32 characters) error = %v", err)
	}
	if _, err := secrets.ParseKey("short"); !errors.Is(err, secrets.ErrKey) {
		t.Fatalf("ParseKey(short) error = %v, want ErrKey", err)
	}
}

func TestOpenRejectsPlaintextAndEmptyBoxes(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, box := range map[string]secrets.Box{
		"zero":     {},
		"v1 empty": {Version: secrets.Version1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := e.Open(box); !errors.Is(err, secrets.ErrDecrypt) {
				t.Fatalf("Open(%s) error = %v, want ErrDecrypt", name, err)
			}
		})
	}
}

func TestSealUsesVersionOne(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	box, err := e.Seal("x")
	if err != nil {
		t.Fatal(err)
	}
	if box.Version != 1 {
		t.Fatalf("Version = %d, want 1", box.Version)
	}
	if secrets.Version1 != 1 {
		t.Fatalf("Version = %d, want 1", box.Version)
	}
	if len(box.Nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(box.Nonce))
	}
}

func TestKeyFromEnvNamesTheVariableAndNeverTheValue(t *testing.T) {
	raw := testKey(t)
	value := base64.StdEncoding.EncodeToString(raw)
	key, err := secrets.KeyFromEnv("TELESYNC_TEST_WP_KEY", func(name string) string {
		if name != "TELESYNC_TEST_WP_KEY" {
			t.Fatalf("looked up %q", name)
		}
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, raw) {
		t.Fatal("KeyFromEnv did not decode the key")
	}

	_, err = secrets.KeyFromEnv("TELESYNC_TEST_WP_KEY", func(string) string { return "" })
	if !errors.Is(err, secrets.ErrKeyNotSet) {
		t.Fatalf("unset variable error = %v, want ErrKeyNotSet", err)
	}
	if !strings.Contains(err.Error(), "TELESYNC_TEST_WP_KEY") {
		t.Fatalf("error does not name the variable: %v", err)
	}

	_, err = secrets.KeyFromEnv("TELESYNC_TEST_WP_KEY", func(string) string { return "too-short" })
	if !errors.Is(err, secrets.ErrKey) {
		t.Fatalf("short value error = %v, want ErrKey", err)
	}
}

func TestParseKeyAcceptsHex(t *testing.T) {
	// 64 hex characters is what `openssl rand -hex 32` prints; reading it as a
	// 64-character passphrase would be a different key and would silently
	// weaken it to the lower half of every AES key.
	raw := testKey(t)
	hexText := hex.EncodeToString(raw)
	key, err := secrets.ParseKey(hexText)
	if err != nil {
		t.Fatalf("ParseKey(hex): %v", err)
	}
	if !bytes.Equal(key, raw) {
		t.Fatal("ParseKey(hex) did not decode the key")
	}
	fromUpper, err := secrets.ParseKey(strings.ToUpper(hexText))
	if err != nil || !bytes.Equal(fromUpper, raw) {
		t.Fatalf("ParseKey(upper-case hex) = %x, %v", fromUpper, err)
	}
	// A 64-character value that is not hex is not a key.
	if _, err := secrets.ParseKey(strings.Repeat("z", 64)); !errors.Is(err, secrets.ErrKey) {
		t.Fatalf("err = %v, want ErrKey", err)
	}
}

func TestParseKeyDoesNotAcceptLongerPassphrases(t *testing.T) {
	for _, value := range []string{
		"a passphrase of exactly thirty-two!", // 34
		strings.Repeat("x", 48),
		strings.Repeat("x", 64),
	} {
		if _, err := secrets.ParseKey(value); !errors.Is(err, secrets.ErrKey) {
			t.Fatalf("ParseKey(%d chars) err = %v, want ErrKey", len(value), err)
		}
	}
}
