package secrets_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/secrets"
)

// AAD binding: a session box must only open in the account context it was
// sealed for. The AEAD is AES-256-GCM exactly as before; the additional data
// is authenticated but not encrypted, which is the standard GCM construction.

func TestSealWithAADRoundTripsInTheSameContext(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "auth-key material for one account session"
	aad := []byte("acc_abc\x001")

	box, err := e.SealWithAAD(plaintext, aad)
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}
	if box.Plaintext() {
		t.Fatal("the box claims to be plaintext")
	}
	if box.Version != secrets.Version1 {
		t.Fatalf("Version = %d, want %d", box.Version, secrets.Version1)
	}
	if len(box.Nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(box.Nonce))
	}

	got, err := e.OpenWithAAD(box, aad)
	if err != nil {
		t.Fatalf("OpenWithAAD: %v", err)
	}
	if got != plaintext {
		t.Fatalf("OpenWithAAD = %q, want %q", got, plaintext)
	}
}

func TestOpenWithAADRefusesAnotherAccountsContext(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	mine := []byte("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa\x001")
	theirs := []byte("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb\x001")

	box, err := e.SealWithAAD("one account's session", mine)
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}

	// The key security property: one account's box cannot be opened under
	// another account's identity, even with the same key.
	if _, err := e.OpenWithAAD(box, theirs); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenWithAAD(other account) error = %v, want ErrDecrypt", err)
	}
	if _, err := e.OpenWithAAD(box, nil); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenWithAAD(no context) error = %v, want ErrDecrypt", err)
	}
	if _, err := e.OpenWithAAD(box, []byte("")); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenWithAAD(empty context) error = %v, want ErrDecrypt", err)
	}
}

func TestOpenWithAADRefusesAnotherKeyVersion(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	v1 := []byte("acc_same-account\x001")
	v2 := []byte("acc_same-account\x002")

	box, err := e.SealWithAAD("session", v1)
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}
	if _, err := e.OpenWithAAD(box, v2); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenWithAAD(other key version) error = %v, want ErrDecrypt", err)
	}
}

func TestOpenWithAADRefusesATamperedCiphertext(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("acc_abc\x001")
	box, err := e.SealWithAAD("session", aad)
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}

	tampered := box
	tampered.Ciphertext = append([]byte(nil), box.Ciphertext...)
	tampered.Ciphertext[len(tampered.Ciphertext)/2] ^= 0x01
	if _, err := e.OpenWithAAD(tampered, aad); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenWithAAD(tampered) error = %v, want ErrDecrypt", err)
	}

	tamperedNonce := box
	tamperedNonce.Nonce = append([]byte(nil), box.Nonce...)
	tamperedNonce.Nonce[0] ^= 0x80
	if _, err := e.OpenWithAAD(tamperedNonce, aad); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenWithAAD(tampered nonce) error = %v, want ErrDecrypt", err)
	}
}

func TestOpenWithAADRefusesAnotherKey(t *testing.T) {
	sealer, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("acc_abc\x001")
	box, err := sealer.SealWithAAD("session", aad)
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}
	other, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.OpenWithAAD(box, aad); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenWithAAD(other key) error = %v, want ErrDecrypt", err)
	}
}

func TestOpenWithAADRefusesMalformedBoxes(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("acc_abc\x001")
	for name, box := range map[string]secrets.Box{
		"zero":     {},
		"v1 empty": {Version: secrets.Version1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := e.OpenWithAAD(box, aad); !errors.Is(err, secrets.ErrDecrypt) {
				t.Fatalf("OpenWithAAD(%s) error = %v, want ErrDecrypt", name, err)
			}
		})
	}
}

func TestSealWithAADUsesAFreshNonce(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("acc_abc\x001")
	first, err := e.SealWithAAD("same plaintext twice", aad)
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}
	second, err := e.SealWithAAD("same plaintext twice", aad)
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("two seals reused the nonce")
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("two seals of the same plaintext produced the same ciphertext")
	}
}

func TestSealWithAADNeverCarriesThePlaintext(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "a session key that must never be readable"
	box, err := e.SealWithAAD(plaintext, []byte("acc_abc\x001"))
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}
	for name, field := range map[string][]byte{
		"ciphertext": box.Ciphertext,
		"nonce":      box.Nonce,
	} {
		if bytes.Contains(field, []byte(plaintext)) {
			t.Fatalf("%s contains the plaintext", name)
		}
		if bytes.Contains(field, []byte("session key")) {
			t.Fatalf("%s contains part of the plaintext", name)
		}
	}
}

// WordPress credentials depend on Seal/Open and must not regress: the AAD pair
// is additive and the original pair is untouched.
func TestSealAndOpenStillRoundTripBesideTheAADPair(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "a wordpress application password"

	box, err := e.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := e.Open(box)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != plaintext {
		t.Fatalf("Open = %q, want %q", got, plaintext)
	}

	// A box sealed without context is not readable as a contextual box, and a
	// contextual box is not readable without its context: the two never mix.
	if _, err := e.OpenWithAAD(box, []byte("acc_abc\x001")); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenWithAAD on a context-free box error = %v, want ErrDecrypt", err)
	}
	withAAD, err := e.SealWithAAD(plaintext, []byte("acc_abc\x001"))
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}
	if _, err := e.Open(withAAD); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("Open on a contextual box error = %v, want ErrDecrypt", err)
	}
}

func TestSealWithAADErrorsNeverEchoThePlaintext(t *testing.T) {
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "99887766554433221100-super-secret"
	box, err := e.SealWithAAD(plaintext, []byte("acc_abc\x001"))
	if err != nil {
		t.Fatalf("SealWithAAD: %v", err)
	}
	_, err = e.OpenWithAAD(box, []byte("acc_other\x001"))
	if err == nil {
		t.Fatal("OpenWithAAD with the wrong context succeeded")
	}
	for _, secret := range []string{plaintext, "99887766", "super-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error %q leaks %q", err.Error(), secret)
		}
	}
}
