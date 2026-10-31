package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/auth"
)

func TestHashPasswordRoundTripsAndUsesArgon2id(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$"+auth.HashParamsForTest()+"$") {
		t.Fatalf("hash = %q, want a PHC Argon2id string with the documented cost", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("the hash contains the password")
	}
	ok, err := auth.VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("VerifyPassword = %v, %v", ok, err)
	}
	wrong, err := auth.VerifyPassword(hash, "correct horse battery stapl")
	if err != nil || wrong {
		t.Fatalf("a wrong password verified: %v, %v", wrong, err)
	}
}

func TestHashPasswordUsesAFreshSaltEachTime(t *testing.T) {
	a, _ := auth.HashPassword("same-password")
	b, _ := auth.HashPassword("same-password")
	if a == b {
		t.Fatal("two hashes of the same password are identical, so the salt is not random")
	}
}

func TestVerifyPasswordRejectsAnUnusableHash(t *testing.T) {
	for _, hash := range []string{
		"",
		"plaintext",
		"$2a$10$abcdefghijklmnopqrstuv",
		"$argon2id$v=19$m=1,t=1,p=1$c2FsdHNhbHQ$a2V5",
		"$argon2i$v=19$m=65536,t=1,p=4$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
	} {
		if ok, err := auth.VerifyPassword(hash, "x"); ok || !errors.Is(err, auth.ErrInvalidHash) {
			t.Errorf("VerifyPassword(%q) = %v, %v; want false, ErrInvalidHash", hash, ok, err)
		}
	}
}

func TestNewTokensRequiresASecretOfAtLeast32Bytes(t *testing.T) {
	if _, err := auth.NewTokens(auth.TokenOptions{Secret: []byte("short")}); err == nil {
		t.Fatal("a short secret was accepted")
	}
	if _, err := auth.NewTokens(auth.TokenOptions{}); err == nil {
		t.Fatal("no secret was accepted")
	}
	if _, err := auth.NewTokens(auth.TokenOptions{Secret: []byte(strings.Repeat("k", 32))}); err != nil {
		t.Fatalf("a 32-byte secret was rejected: %v", err)
	}
}

func newTokens(t *testing.T, now time.Time) *auth.Tokens {
	t.Helper()
	tokens, err := auth.NewTokens(auth.TokenOptions{
		Secret: []byte(strings.Repeat("s", 48)),
		TTL:    time.Hour,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func TestTokenDefaultsToOneHour(t *testing.T) {
	tokens, err := auth.NewTokens(auth.TokenOptions{Secret: []byte(strings.Repeat("s", 48))})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.TTL() != time.Hour {
		t.Fatalf("ttl = %s, want 1h", tokens.TTL())
	}
}

func TestIssueAndParseRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	tokens := newTokens(t, now)

	token, exp, err := tokens.Issue(7)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("exp = %v", exp)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token = %q, want three segments", token)
	}
	id, err := tokens.Parse(token)
	if err != nil || id != 7 {
		t.Fatalf("Parse = %d, %v", id, err)
	}
}

func TestParseRejectsAnExpiredToken(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	tokens := newTokens(t, now)
	token, _, _ := tokens.Issue(7)

	later := newTokens(t, now.Add(time.Hour))
	if _, err := later.Parse(token); !errors.Is(err, auth.ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

func TestParseRejectsATokenSignedWithAnotherSecret(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	issuer := newTokens(t, now)
	token, _, _ := issuer.Issue(7)

	other, err := auth.NewTokens(auth.TokenOptions{Secret: []byte(strings.Repeat("x", 48)), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Parse(token); !errors.Is(err, auth.ErrTokenSignature) {
		t.Fatalf("err = %v, want ErrTokenSignature", err)
	}
}

func TestParseRejectsMalformedAndUnsignedTokens(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	tokens := newTokens(t, now)

	// "alg: none" with an empty signature must not be accepted.
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body, _ := json.Marshal(map[string]any{"sub": "7", "iss": "telesync", "aud": "telesync-api", "exp": now.Add(time.Hour).Unix()})
	unsigned := head + "." + base64.RawURLEncoding.EncodeToString(body) + "."

	for name, token := range map[string]string{
		"empty":     "",
		"two parts": "abc.def",
		"junk":      "not-a-token",
		"unsigned":  unsigned,
	} {
		if _, err := tokens.Parse(token); err == nil {
			t.Errorf("%s: Parse accepted the token", name)
		}
	}
}

func TestParseRejectsAnotherIssuerAndABadSubject(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	tokens := newTokens(t, now)
	secret := []byte(strings.Repeat("s", 48))

	forge := func(c map[string]any) string {
		h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
		b, _ := json.Marshal(c)
		signing := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(b)
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(signing))
		return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	base := func() map[string]any {
		return map[string]any{"sub": "7", "iss": "telesync", "aud": "telesync-api", "exp": now.Add(time.Hour).Unix()}
	}

	wrongIss := base()
	wrongIss["iss"] = "another-service"
	if _, err := tokens.Parse(forge(wrongIss)); !errors.Is(err, auth.ErrTokenIssuer) {
		t.Errorf("wrong issuer: err = %v", err)
	}
	noSub := base()
	delete(noSub, "sub")
	if _, err := tokens.Parse(forge(noSub)); !errors.Is(err, auth.ErrTokenSubject) {
		t.Errorf("no subject: err = %v", err)
	}
	zeroSub := base()
	zeroSub["sub"] = "0"
	if _, err := tokens.Parse(forge(zeroSub)); !errors.Is(err, auth.ErrTokenSubject) {
		t.Errorf("zero subject: err = %v", err)
	}
}

func TestBurnPasswordCheckCompletes(t *testing.T) {
	// The timing equality is a property of the constant work, not something a
	// test can assert reliably; this only proves the path runs.
	auth.BurnPasswordCheck("anything")
}
