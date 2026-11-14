package appconfig_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/secrets"
)

// baseAccountsConfig is the smallest configuration Load accepts, so every test
// here varies only the `accounts` block or the environment.
const baseAccountsConfig = `{"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"H","phone_env":"P"}]}`

func accountsEnv() map[string]string {
	return map[string]string{"H": "hash", "P": "+15551230000"}
}

func TestSessionEncryptionKeyComesFromTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, baseAccountsConfig)

	key := "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=" // 32 bytes base64
	extra := accountsEnv()
	extra["TELESYNC_SESSION_ENCRYPTION_KEY"] = key

	cfg, err := appconfig.Load(path, env(extra))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Accounts.SessionKey) != secrets.KeySize {
		t.Fatalf("session key = %d bytes, want %d", len(cfg.Accounts.SessionKey), secrets.KeySize)
	}
	if _, err := cfg.Accounts.Encrypter(); err != nil {
		t.Fatalf("Encrypter() with a configured key: %v", err)
	}
}

// The seal must refuse rather than proceed, because the alternative is a
// session written with a key nobody holds — an authorization that looks saved
// and is unreadable.
func TestSessionSealRefusesWhenNoKeyIsConfigured(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, baseAccountsConfig)

	cfg, err := appconfig.Load(path, env(accountsEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	enc, err := cfg.Accounts.Encrypter()
	if err == nil {
		t.Fatalf("Encrypter() = %v, want an error when no session key is configured", enc)
	}
	if !strings.Contains(err.Error(), "TELESYNC_SESSION_ENCRYPTION_KEY") {
		t.Errorf("error should name the variable to set: %v", err)
	}
}

// Two sources for one key is an operator mistake worth naming rather than
// silently preferring one, which is how a rotation silently does nothing.
func TestSessionEncryptionKeyRefusesTwoSources(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "session.key")
	if err := os.WriteFile(keyPath, []byte("QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"accounts":{"session_encryption_key_path":"` + keyPath + `"},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"H","phone_env":"P"}]}`
	path := writeConfig(t, dir, body)

	extra := accountsEnv()
	extra["TELESYNC_SESSION_ENCRYPTION_KEY"] = "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="

	_, err := appconfig.Load(path, env(extra))
	if err == nil {
		t.Fatal("expected an error when the key is given twice")
	}
}

func TestSessionEncryptionKeyReadsFromAKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "session.key")
	if err := os.WriteFile(keyPath, []byte("QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"accounts":{"session_encryption_key_path":"` + keyPath + `"},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"H","phone_env":"P"}]}`
	path := writeConfig(t, dir, body)

	cfg, err := appconfig.Load(path, env(accountsEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Accounts.SessionKey) != secrets.KeySize {
		t.Fatalf("session key = %d bytes, want %d", len(cfg.Accounts.SessionKey), secrets.KeySize)
	}
	if _, err := cfg.Accounts.Encrypter(); err != nil {
		t.Fatalf("Encrypter() with a key file: %v", err)
	}
}

// A Telegram session is not a WordPress credential. If the two shared a key,
// rotating a site password would invalidate every stored Telegram
// authorization.
func TestSessionKeyIsSeparateFromTheWordPressKey(t *testing.T) {
	dir := t.TempDir()
	body := `{"api":{"wordpress":{"encryption_key_env":"WP_KEY"}},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"H","phone_env":"P"}]}`
	path := writeConfig(t, dir, body)

	wpKey := "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI="
	sessKey := "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="
	extra := accountsEnv()
	extra["WP_KEY"] = wpKey
	extra["TELESYNC_SESSION_ENCRYPTION_KEY"] = sessKey

	cfg, err := appconfig.Load(path, env(extra))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(cfg.API.WordPress.Key) == string(cfg.Accounts.SessionKey) {
		t.Fatal("the WordPress key and the session key resolved to the same bytes")
	}
}

// The session key authorizes someone's Telegram account. It must not reach a
// description, a log line or a JSON encoding — the same boundary WordPress
// credentials already respect.
func TestSessionEncryptionKeyNeverLeaks(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, baseAccountsConfig)

	const key = "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="
	extra := accountsEnv()
	extra["TELESYNC_SESSION_ENCRYPTION_KEY"] = key

	cfg, err := appconfig.Load(path, env(extra))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rendered := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(rendered, key) {
		t.Fatalf("configuration description leaks the session key: %s", rendered)
	}
	// The raw key bytes must not appear either, only their encoded form.
	if strings.Contains(rendered, string(cfg.Accounts.SessionKey)) {
		t.Fatal("configuration description leaks the decoded session key")
	}

	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), key) {
		t.Fatalf("JSON encoding leaks the session key: %s", encoded)
	}
	if strings.Contains(string(encoded), string(cfg.Accounts.SessionKey)) {
		t.Fatal("JSON encoding leaks the decoded session key")
	}
}

// A malformed key is a configuration error, not something to repair silently:
// accepting it would seal every session under a key weaker than AES-256.
func TestSessionEncryptionKeyRejectsAnInvalidKey(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, baseAccountsConfig)

	extra := accountsEnv()
	extra["TELESYNC_SESSION_ENCRYPTION_KEY"] = "too-short"

	_, err := appconfig.Load(path, env(extra))
	if err == nil {
		t.Fatal("expected an error for a key that is not 32 bytes")
	}
}
