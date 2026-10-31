package appconfig_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
)

func TestDefaultPathUsesTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := appconfig.DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(home, ".telesync", "config.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestCreateIfMissingWritesAStarterFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".telesync", "config.json")

	created, err := appconfig.CreateIfMissing(path)
	if err != nil {
		t.Fatalf("CreateIfMissing: %v", err)
	}
	if !created {
		t.Fatal("the first call must report that it created the file")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %o, want 700", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("the starter is not valid JSON:\n%s", data)
	}
	if !strings.Contains(string(data), "OFFICIAL_API_HASH") {
		t.Error("the starter should name the environment variables it expects")
	}
	// The starter must never contain anything that looks like a real secret.
	if strings.Contains(string(data), "api_hash\":") {
		t.Error("the starter must not have an api_hash field; secrets come from the environment")
	}
}

func TestCreateIfMissingNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const existing = `{"clients":[]}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := appconfig.CreateIfMissing(path)
	if err != nil {
		t.Fatalf("CreateIfMissing: %v", err)
	}
	if created {
		t.Fatal("an existing file must not be reported as created")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Fatalf("the existing file was modified:\n%s", data)
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0o644 {
		t.Errorf("an existing file's mode was changed: %o", info.Mode().Perm())
	}
}

func TestCreateIfMissingRejectsAnEmptyPath(t *testing.T) {
	if _, err := appconfig.CreateIfMissing(""); err == nil {
		t.Fatal("an empty path must be rejected")
	}
}

func TestCreateIfMissingFailsOnAnUnwritableParent(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file-not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := appconfig.CreateIfMissing(filepath.Join(blocked, "config.json")); err == nil {
		t.Fatal("a parent that is a file must be reported, not silently ignored")
	}
}

func TestStarterIsParseableAndHonest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if _, err := appconfig.CreateIfMissing(path); err != nil {
		t.Fatal(err)
	}

	// Parsing must succeed: the starter is valid JSON with the documented
	// fields, so a typo in the template would fail here.
	if _, err := appconfig.Load(path, func(string) string { return "" }); err == nil {
		t.Fatal("a fresh starter cannot be a complete configuration")
	} else if !strings.Contains(err.Error(), "app_id") {
		t.Fatalf("the starter's own validation should point at app_id, got: %v", err)
	}
}

func TestStarterNeverInventsCredentials(t *testing.T) {
	// app_id is a real credential; the starter must not make one up. Once the
	// user supplies it and the hash variable, the file loads unchanged.
	dir := t.TempDir()
	path := filepath.Join(dir, ".telesync", "config.json")
	if _, err := appconfig.CreateIfMissing(path); err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	clients := raw["clients"].([]any)
	first := clients[0].(map[string]any)
	if first["app_id"].(float64) != 0 {
		t.Fatalf("the starter invented an app_id: %v", first["app_id"])
	}
	first["app_id"] = 123456
	edited, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := appconfig.Load(path, func(k string) string {
		switch k {
		case "OFFICIAL_API_HASH":
			return "test-hash"
		case "TELESYNC_JWT_SECRET":
			return strings.Repeat("s", 32)
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Load after filling app_id and the secret: %v", err)
	}
	base := filepath.Join(dir, ".telesync")
	if cfg.Runtime.RunDir != filepath.Join(base, "run") {
		t.Errorf("run dir = %q, want it beside the config", cfg.Runtime.RunDir)
	}
	if cfg.Runtime.SessionDir != filepath.Join(base, "sessions") {
		t.Errorf("session dir = %q, want it beside the config", cfg.Runtime.SessionDir)
	}
	if cfg.Clients[0].Key != "official" || cfg.Clients[0].APIHash != "test-hash" {
		t.Errorf("client = %+v", cfg.Clients[0])
	}
	if cfg.API.JWT.SecretEnv != "TELESYNC_JWT_SECRET" || cfg.API.JWT.TTL != time.Hour {
		t.Errorf("jwt = %+v via %q, want the starter's one-hour token from that variable",
			cfg.API.JWT.TTL, cfg.API.JWT.SecretEnv)
	}
}
