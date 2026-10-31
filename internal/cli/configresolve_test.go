package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/cli"
)

// homeEnv points HOME and TELESYNC_CONFIG at a temp tree.
type homeEnv struct {
	home   string
	stdout strings.Builder
	stderr strings.Builder
	env    map[string]string
}

func newHomeEnv(t *testing.T) *homeEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return &homeEnv{home: home, env: map[string]string{}}
}

func (h *homeEnv) deps() cli.Deps {
	return cli.Deps{
		Lookup: func(k string) string {
			if v, ok := h.env[k]; ok {
				return v
			}
			switch k {
			case "HOME":
				return h.home
			case "OFFICIAL_API_HASH":
				return "test-hash"
			}
			return ""
		},
		Stdout: &h.stdout,
		Stderr: &h.stderr,
	}
}

func (h *homeEnv) defaultConfig() string {
	return filepath.Join(h.home, ".telesync", "config.json")
}

func TestFirstRunCreatesTheDefaultConfig(t *testing.T) {
	h := newHomeEnv(t)
	code := cli.New(h.deps()).Run(context.Background(), []string{"config", "validate"})
	if code != 1 {
		// The starter has app_id 0, so validation fails; what matters is that
		// the file now exists and the message says what to do.
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if _, err := os.Stat(h.defaultConfig()); err != nil {
		t.Fatalf("the default config was not created: %v", err)
	}
	if !strings.Contains(h.stderr.String(), h.defaultConfig()) {
		t.Fatalf("the error should name the file that was created: %s", h.stderr.String())
	}
}

func TestFirstRunPrintsWhereTheFileWasWritten(t *testing.T) {
	h := newHomeEnv(t)
	code := cli.New(h.deps()).Run(context.Background(), []string{"config", "validate"})
	if code == 0 {
		t.Fatal("a fresh starter cannot validate")
	}
	out := h.stderr.String()
	if !strings.Contains(out, "created") {
		t.Fatalf("the first run should say it created the file: %s", out)
	}
	if !strings.Contains(out, "app_id") {
		t.Fatalf("it should explain what to fill in: %s", out)
	}
}

func TestSecondRunDoesNotRewriteTheFile(t *testing.T) {
	h := newHomeEnv(t)
	app := cli.New(h.deps())
	app.Run(context.Background(), []string{"config", "validate"})

	path := h.defaultConfig()
	// The user fills it in. The documentation directory is theirs to point at,
	// so the test writes the tree it names.
	docs := docsTree(t, h.home)
	body := fmt.Sprintf(`{
	  "api": {"docs_dir": %q},
	  "clients": [{"key":"official","type":"official","app_id":42,"app_hash_env":"OFFICIAL_API_HASH"}]
	}`, docs)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	code := app.Run(context.Background(), []string{"config", "validate"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("the existing config was rewritten:\n%s", data)
	}
}

func TestExplicitConfigPathIsNeverCreated(t *testing.T) {
	h := newHomeEnv(t)
	missing := filepath.Join(t.TempDir(), "deliberate.json")
	h.env["TELESYNC_CONFIG"] = missing

	code := cli.New(h.deps()).Run(context.Background(), []string{"config", "validate"})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("an explicitly configured path must not be created behind the user's back")
	}
	if !strings.Contains(h.stderr.String(), missing) {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestExplicitFlagPathIsNeverCreated(t *testing.T) {
	h := newHomeEnv(t)
	missing := filepath.Join(t.TempDir(), "flagged.json")

	code := cli.New(h.deps()).Run(context.Background(), []string{"config", "validate", "--config", missing})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("a --config path must not be created behind the user's back")
	}
}

func TestDaemonCreatesTheDefaultConfigToo(t *testing.T) {
	h := newHomeEnv(t)
	h.env["TELESYNC_CONFIG"] = ""
	daemonCalled := false
	deps := h.deps()
	deps.Runtime.Daemon = func(context.Context, cli.RuntimeRequest) error {
		daemonCalled = true
		return nil
	}
	_ = daemonCalled
	// The starter has app_id 0, so the daemon must refuse to start, but it must
	// have created the file first so the user can fill it in.
	code := cli.New(deps).Run(context.Background(), []string{"daemon"})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(h.defaultConfig()); err != nil {
		t.Fatalf("daemon did not create the default config: %v", err)
	}
}

func TestDefaultConfigIsValidJSONWithExpectedShape(t *testing.T) {
	h := newHomeEnv(t)
	cli.New(h.deps()).Run(context.Background(), []string{"config", "validate"})

	data, err := os.ReadFile(h.defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		API struct {
			ListenAddr string `json:"listen_addr"`
		} `json:"api"`
		Runtime struct {
			RunDir     string `json:"run_dir"`
			SessionDir string `json:"session_dir"`
		} `json:"runtime"`
		Clients []struct {
			Key        string `json:"key"`
			Type       string `json:"type"`
			Enabled    bool   `json:"enabled"`
			AppHashEnv string `json:"app_hash_env"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("the created config is not valid JSON: %v", err)
	}
	if raw.API.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("listen_addr = %q; the API must default to loopback", raw.API.ListenAddr)
	}
	if len(raw.Clients) != 1 || raw.Clients[0].Key != "official" || !raw.Clients[0].Enabled {
		t.Fatalf("clients = %+v", raw.Clients)
	}
}

func TestBareInvocationCreatesTheDefaultConfig(t *testing.T) {
	// Running `telesync` with no arguments is the most likely first thing a
	// user does; it must not leave them without a configuration.
	h := newHomeEnv(t)
	code := cli.New(h.deps()).Run(context.Background(), nil)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if _, err := os.Stat(h.defaultConfig()); err != nil {
		t.Fatalf("a bare run did not create the default config: %v", err)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "telesync daemon") {
		t.Errorf("the bare run should still print usage:\n%s", out)
	}
	if !strings.Contains(out, h.defaultConfig()) {
		t.Errorf("the bare run should name the configuration file:\n%s", out)
	}
}

func TestBareInvocationReportsAnExistingConfig(t *testing.T) {
	h := newHomeEnv(t)
	path := h.defaultConfig()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"clients":[{"key":"official","type":"official","app_id":42,"app_hash_env":"OFFICIAL_API_HASH"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cli.New(h.deps()).Run(context.Background(), nil)
	out := h.stdout.String()
	if strings.Contains(out, "created a starter") {
		t.Errorf("an existing config must not be reported as created:\n%s", out)
	}
	if !strings.Contains(out, path) {
		t.Errorf("the bare run should name the existing configuration:\n%s", out)
	}
	// The command is read-only here: it must not start the daemon.
	if data, err := os.ReadFile(path); err != nil || string(data) != body {
		t.Fatal("the bare run modified the existing config")
	}
}

func TestBareInvocationDoesNotCreateAnExplicitPath(t *testing.T) {
	h := newHomeEnv(t)
	missing := filepath.Join(t.TempDir(), "typed.json")
	h.env["TELESYNC_CONFIG"] = missing

	code := cli.New(h.deps()).Run(context.Background(), nil)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("a TELESYNC_CONFIG path must not be created by a bare run")
	}
	if !strings.Contains(h.stderr.String(), missing) {
		t.Errorf("the bare run should say the configured file is missing:\n%s", h.stderr.String())
	}
}

func TestHelpFlagDoesNotCreateAnything(t *testing.T) {
	h := newHomeEnv(t)
	code := cli.New(h.deps()).Run(context.Background(), []string{"--help"})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".telesync")); !os.IsNotExist(err) {
		t.Fatal("--help must be side-effect free")
	}
}
