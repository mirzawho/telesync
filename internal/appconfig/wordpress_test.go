package appconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/secrets"
)

// wordpressConfig is a minimal valid configuration plus one api key, written
// into dir so a relative key-file path resolves against it.
func wordpressConfig(t *testing.T, dir, api string) string {
	t.Helper()
	body := `{"api":{` + api + `},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`
	return writeConfig(t, dir, body)
}

// wpKey is a valid 32-byte WordPress encryption key, base64-encoded.
var wpKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func TestTheWordPressKeyIsResolvedFromItsVariable(t *testing.T) {
	path := wordpressConfig(t, t.TempDir(), `"wordpress":{"encryption_key_env":"WP_KEY"}`)
	cfg, err := appconfig.Load(path, env(map[string]string{"WP_KEY": wpKey}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.API.WordPress.Key) != 32 {
		t.Fatalf("key length = %d", len(cfg.API.WordPress.Key))
	}
	// The key never appears in the redacted description.
	rendered := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(rendered, wpKey) {
		t.Fatalf("the description leaks the key: %s", rendered)
	}
	if !strings.Contains(rendered, "WP_KEY") {
		t.Fatalf("the description should name the variable: %s", rendered)
	}
}

func TestTheWordPressKeyMayBeALiteralInTheFile(t *testing.T) {
	path := wordpressConfig(t, t.TempDir(), `"wordpress":{"encryption_key":"`+wpKey+`"}`)
	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.API.WordPress.Key) != 32 {
		t.Fatalf("key length = %d", len(cfg.API.WordPress.Key))
	}
	if !cfg.API.WordPress.KeyInFile {
		t.Fatal("the configuration does not say the key is in the file")
	}
	if strings.Contains(strings.Join(cfg.Describe(), "\n"), wpKey) {
		t.Fatal("the description leaks the key")
	}
}

func TestTheWordPressKeyIsOptional(t *testing.T) {
	path := wordpressConfig(t, t.TempDir(), `"jwt":{"secret_env":"JWT_SECRET"}`)
	cfg, err := appconfig.Load(path, env(map[string]string{"JWT_SECRET": strings.Repeat("k", 40)}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.API.WordPress.Key != nil {
		t.Fatal("a key was invented")
	}
	if !strings.Contains(strings.Join(cfg.Describe(), "\n"), "wordpress=disabled") {
		t.Fatalf("the description does not say WordPress is disabled: %v", cfg.Describe())
	}
}

func TestTheWordPressKeyIsRefusedWhenUnusable(t *testing.T) {
	cases := map[string]struct {
		api     string
		lookup  map[string]string
		message string
	}{
		"missing variable": {
			api:     `"wordpress":{"encryption_key_env":"WP_KEY"}`,
			message: "WP_KEY is not set",
		},
		"short variable": {
			api:     `"wordpress":{"encryption_key_env":"WP_KEY"}`,
			lookup:  map[string]string{"WP_KEY": "too-short"},
			message: "WP_KEY",
		},
		"short literal": {
			api:     `"wordpress":{"encryption_key":"too-short"}`,
			message: "api.wordpress.encryption_key",
		},
		"empty literal": {
			api:     `"wordpress":{"encryption_key":""}`,
			message: "api.wordpress.encryption_key",
		},
		"both forms": {
			api:     `"wordpress":{"encryption_key":"` + wpKey + `","encryption_key_env":"WP_KEY"}`,
			lookup:  map[string]string{"WP_KEY": wpKey},
			message: "not both",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := wordpressConfig(t, t.TempDir(), c.api)
			_, err := appconfig.Load(path, env(c.lookup))
			if err == nil {
				t.Fatal("the configuration was accepted")
			}
			if !strings.Contains(err.Error(), c.message) {
				t.Fatalf("err = %v, want it to mention %q", err, c.message)
			}
			// The error names the variable or field, never a value.
			if c.lookup != nil && strings.Contains(err.Error(), c.lookup["WP_KEY"]) {
				t.Fatalf("the error leaks the key: %v", err)
			}
		})
	}
}

func TestNewWordPressEncrypterFailsWithoutAKey(t *testing.T) {
	cfg := appconfig.Config{}
	if _, err := cfg.API.WordPress.Encrypter(); !errors.Is(err, secrets.ErrKeyNotSet) {
		t.Fatalf("err = %v, want ErrKeyNotSet", err)
	}
	cfg.API.WordPress.Key = make([]byte, 16)
	if _, err := cfg.API.WordPress.Encrypter(); !errors.Is(err, secrets.ErrKey) {
		t.Fatalf("err = %v, want ErrKey", err)
	}
	cfg.API.WordPress.Key = make([]byte, 32)
	if _, err := cfg.API.WordPress.Encrypter(); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestTheWordPressKeyMayComeFromAFile(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "wordpress.key")
	if err := os.WriteFile(keyFile, []byte(wpKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := wordpressConfig(t, dir, `"wordpress":{"encryption_key_path":"wordpress.key"}`)
	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.API.WordPress.Key) != 32 {
		t.Fatalf("key length = %d", len(cfg.API.WordPress.Key))
	}
	if !cfg.API.WordPress.KeyInFile {
		t.Fatal("the configuration does not say the key is in a file")
	}
	rendered := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(rendered, wpKey) {
		t.Fatalf("the description leaks the key: %s", rendered)
	}
	// A relative path resolves against the configuration file's directory.
	if !strings.Contains(rendered, "api.wordpress=key from "+keyFile) {
		t.Fatalf("the description does not name the file: %s", rendered)
	}
}

func TestTheWordPressKeyFileIsRefusedWhenUnusable(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short.key")
	if err := os.WriteFile(short, []byte("too-short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "word-key"), []byte(strings.Repeat("w", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ api, message string }{
		"missing file":      {`"wordpress":{"encryption_key_path":"absent.key"}`, "absent.key"},
		"empty file":        {`"wordpress":{"encryption_key_path":"empty.key"}`, "empty key"},
		"short file":        {`"wordpress":{"encryption_key_path":"short.key"}`, "32 bytes"},
		"directory":         {`"wordpress":{"encryption_key_path":"sub"}`, "regular file"},
		"not 32 bytes":      {`"wordpress":{"encryption_key_path":"word-key"}`, "32 bytes"},
		"file and variable": {`"wordpress":{"encryption_key_path":"short.key","encryption_key_env":"WP_KEY"}`, "not both"},
		"file and literal":  {`"wordpress":{"encryption_key_path":"short.key","encryption_key":"` + wpKey + `"}`, "not both"},
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.key"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := wordpressConfig(t, dir, c.api)
			_, err := appconfig.Load(path, env(map[string]string{"WP_KEY": wpKey}))
			if err == nil {
				t.Fatal("the configuration was accepted")
			}
			if !strings.Contains(err.Error(), c.message) {
				t.Fatalf("err = %v, want it to mention %q", err, c.message)
			}
			if strings.Contains(err.Error(), wpKey) {
				t.Fatalf("the error leaks the key: %v", err)
			}
		})
	}
}
