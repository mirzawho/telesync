package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/cli"
	"github.com/mirza/telesync/internal/login"
)

// runtimeHarness drives the multi-instance commands with injected runtime
// dependencies, so no process is spawned and no socket is bound.
type runtimeHarness struct {
	env    map[string]string
	stdout bytes.Buffer
	stderr bytes.Buffer

	daemonCalled     bool
	daemonConfigPath string
	workerConfigPath string
	workerKey        string
	daemonErr        error
	workerErr        error
	loginKey         string
	loginErr         error
	loginResult      login.Result
	// loginBlock makes the fake login wait for its context, modelling a
	// connection that never becomes ready.
	loginBlock bool
}

func (h *runtimeHarness) deps() cli.Deps {
	return cli.Deps{
		Lookup: func(k string) string { return h.env[k] },
		Stdout: &h.stdout,
		Stderr: &h.stderr,
		Runtime: cli.RuntimeDeps{
			Daemon: func(ctx context.Context, req cli.RuntimeRequest) error {
				h.daemonCalled = true
				h.daemonConfigPath = req.ConfigPath
				return h.daemonErr
			},
			Worker: func(ctx context.Context, req cli.RuntimeRequest) error {
				h.workerKey = req.Client.Key
				h.workerConfigPath = req.ConfigPath
				return h.workerErr
			},
			Login: func(ctx context.Context, req cli.RuntimeRequest) (login.Result, error) {
				h.loginKey = req.Client.Key
				if h.loginBlock {
					<-ctx.Done()
					return login.Result{}, ctx.Err()
				}
				return h.loginResult, h.loginErr
			},
		},
	}
}

// writeRuntimeConfig writes a config with the given clients and returns its
// path plus the environment its secrets need.
func writeRuntimeConfig(t *testing.T, clients string) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	body := fmt.Sprintf(`{
	  "api": {"listen_addr": "127.0.0.1:8080", "docs_dir": %q},
	  "runtime": {"run_dir": %q, "session_dir": %q},
	  "clients": [%s]
	}`, docsTree(t, dir), filepath.Join(dir, "run"), filepath.Join(dir, "sessions"), clients)
	path := filepath.Join(dir, "telesync.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, map[string]string{
		"TELESYNC_CONFIG": path,
		"OFFICIAL_HASH":   "00112233445566778899aabbccddeeff",
	}
}

const officialClient = `{"key":"official","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}`

// docsTree writes the smallest documentation tree the configuration accepts
// and returns its path.
func docsTree(t *testing.T, dir string) string {
	t.Helper()
	docs := filepath.Join(dir, "docs", "api")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(docs, "README.md")
	if err := os.WriteFile(page, []byte("<!-- title: test -->\n# Test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return docs
}

func TestConfigValidateAcceptsAValidFile(t *testing.T) {
	path, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env}
	app := cli.New(h.deps())

	code := app.Run(context.Background(), []string{"config", "validate"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "official") {
		t.Fatalf("output does not mention the client: %s", h.stdout.String())
	}
	if strings.Contains(h.stdout.String(), env["OFFICIAL_HASH"]) {
		t.Fatal("the output leaks the API hash")
	}
	_ = path
	if strings.Contains(h.stdout.String(), "OFFICIAL_HASH") == false {
		t.Error("the output should name the environment variable holding the hash")
	}
}

func TestConfigValidateReportsProblemsWithExitCode1(t *testing.T) {
	_, env := writeRuntimeConfig(t, `{"key":"bad","type":"official","app_id":0,"app_hash_env":"OFFICIAL_HASH"}`)
	h := &runtimeHarness{env: env}
	code := cli.New(h.deps()).Run(context.Background(), []string{"config", "validate"})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(h.stderr.String(), "app_id") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestConfigValidateWithNoPathCreatesTheDefaultAndExplainsIt(t *testing.T) {
	// With neither --config nor TELESYNC_CONFIG, the command falls back to
	// $HOME/.telesync/config.json and writes a starter there on first run.
	home := t.TempDir()
	h := &runtimeHarness{env: map[string]string{"HOME": home}}
	code := cli.New(h.deps()).Run(context.Background(), []string{"config", "validate"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 because the starter has no app_id", code)
	}
	created := filepath.Join(home, ".telesync", "config.json")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("the starter was not created: %v", err)
	}
	if !strings.Contains(h.stderr.String(), created) {
		t.Fatalf("stderr should name the created file: %s", h.stderr.String())
	}
}

func TestDaemonStartsWithTheConfiguredClients(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env}
	code := cli.New(h.deps()).Run(context.Background(), []string{"daemon"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if !h.daemonCalled {
		t.Fatal("the daemon was not started")
	}
}

func TestDaemonReportsAnError(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env, daemonErr: errors.New("listen: address in use")}
	code := cli.New(h.deps()).Run(context.Background(), []string{"daemon"})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(h.stderr.String(), "address in use") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestWorkerRequiresAnEnabledKnownKey(t *testing.T) {
	_, env := writeRuntimeConfig(t, `{"key":"official","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH","enabled":false}`)
	h := &runtimeHarness{env: env}
	code := cli.New(h.deps()).Run(context.Background(), []string{"worker", "--key", "official"})
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a disabled client", code)
	}
	if h.workerKey != "" {
		t.Fatal("a disabled client must never be started")
	}

	h = &runtimeHarness{env: env}
	code = cli.New(h.deps()).Run(context.Background(), []string{"worker", "--key", "nope"})
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for an unknown key", code)
	}
}

func TestWorkerStartsTheRequestedClient(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env}
	code := cli.New(h.deps()).Run(context.Background(), []string{"worker", "--key", "official"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if h.workerKey != "official" {
		t.Fatalf("worker key = %q", h.workerKey)
	}
}

func TestWorkerUsage(t *testing.T) {
	h := &runtimeHarness{env: map[string]string{}}
	code := cli.New(h.deps()).Run(context.Background(), []string{"worker"})
	if code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(h.stderr.String(), "--key") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestAuthRunsTheInteractiveFlowForOneClient(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env, loginResult: login.Result{Authenticated: true, UserID: 7, Username: "me"}}
	code := cli.New(h.deps()).Run(context.Background(), []string{"auth", "--key", "official"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if h.loginKey != "official" {
		t.Fatalf("login key = %q", h.loginKey)
	}
	if !strings.Contains(h.stdout.String(), "me") {
		t.Fatalf("stdout = %s", h.stdout.String())
	}
}

func TestAuthFailsWhenTheSessionIsStillUnauthorized(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env, loginResult: login.Result{}}
	code := cli.New(h.deps()).Run(context.Background(), []string{"auth", "--key", "official"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "not authorized") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestAuthRejectsAnUnknownKey(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env}
	code := cli.New(h.deps()).Run(context.Background(), []string{"auth", "--key", "wrong"})
	if code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if h.loginKey != "" {
		t.Fatal("an unknown key must never start a login")
	}
}

func TestHelpMentionsTheRuntimeCommands(t *testing.T) {
	h := &runtimeHarness{env: map[string]string{}}
	cli.New(h.deps()).Run(context.Background(), []string{"help"})
	for _, want := range []string{"daemon", "worker", "auth", "config validate"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("help does not mention %q:\n%s", want, h.stdout.String())
		}
	}
}

func TestAuthTimesOutWhenTheConnectionNeverBecomesReady(t *testing.T) {
	// Without a bound, an unreachable network makes the auth command retry
	// forever instead of reporting that it never connected.
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env}
	h.loginBlock = true
	app := cli.New(h.deps())

	start := time.Now()
	code := app.Run(context.Background(), []string{"auth", "--key", "official", "--timeout", "80ms"})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("auth was not bounded: %s", elapsed)
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "timed out") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestAuthRejectsANonPositiveTimeout(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env}
	code := cli.New(h.deps()).Run(context.Background(), []string{"auth", "--key", "official", "--timeout", "0s"})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if h.loginKey != "" {
		t.Fatal("an invalid timeout must not start a login")
	}
}

func TestConfigPathComesFromTheEnvironment(t *testing.T) {
	// A missing file must be reported by name, not panic or default silently.
	h := &runtimeHarness{env: map[string]string{"TELESYNC_CONFIG": "/nonexistent/telesync.json"}}
	code := cli.New(h.deps()).Run(context.Background(), []string{"config", "validate"})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(h.stderr.String(), "/nonexistent/telesync.json") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestConfigValidateEmitsJSONOnRequest(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &runtimeHarness{env: env}
	code := cli.New(h.deps()).Run(context.Background(), []string{"config", "validate", "--json"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	var out struct {
		Valid   bool     `json:"valid"`
		Clients []string `json:"clients"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, h.stdout.String())
	}
	if !out.Valid || len(out.Clients) != 1 || out.Clients[0] != "official" {
		t.Fatalf("out = %+v", out)
	}
	if strings.Contains(h.stdout.String(), env["OFFICIAL_HASH"]) {
		t.Fatal("the JSON output leaks the API hash")
	}
}
