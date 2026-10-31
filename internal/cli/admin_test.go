package cli_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/cli"
)

// adminHarness drives "telesync admin" with piped credentials and a fake
// implementation, so the prompts, the confirmation and the exit codes are
// tested without a database.
type adminHarness struct {
	stdin  string
	stdout strings.Builder
	stderr strings.Builder
	env    map[string]string

	called  bool
	req     cli.AdminRequest
	user    string
	name    string
	pass    string
	result  cli.AdminResult
	err     error
	blocked bool
}

func (h *adminHarness) app() *cli.App {
	return cli.New(cli.Deps{
		Lookup: func(k string) string { return h.env[k] },
		Stdin:  strings.NewReader(h.stdin),
		Stdout: &h.stdout,
		Stderr: &h.stderr,
		Runtime: cli.RuntimeDeps{
			Admin: &cli.AdminDeps{
				CreateOrPromote: func(_ context.Context, req cli.AdminRequest, username, fullname, password string) (cli.AdminResult, error) {
					h.called, h.req, h.user, h.name, h.pass = true, req, username, fullname, password
					return h.result, h.err
				},
			},
		},
	})
}

func TestAdminAsksForCredentialsAndCreatesTheAccount(t *testing.T) {
	path, env := writeRuntimeConfig(t, officialClient)
	h := &adminHarness{
		stdin: "alice\nAlice Admin\ns3cret-password\ns3cret-password\n",
		env:   env,
		result: cli.AdminResult{
			Username: "alice", UserID: 1, Created: true,
			Permissions: channelstore.GlobalPermissions(),
		},
	}
	code := h.app().Run(context.Background(), []string{"admin"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if !h.called || h.user != "alice" || h.name != "Alice Admin" || h.pass != "s3cret-password" {
		t.Fatalf("called=%v user=%q name=%q", h.called, h.user, h.name)
	}
	if strings.Contains(h.stdout.String(), "s3cret-password") || strings.Contains(h.stderr.String(), "s3cret-password") {
		t.Fatal("the password was echoed")
	}
	out := h.stdout.String()
	for _, want := range []string{"created administrator", "alice", "users.manage", "channels.view", "categories.create", "/api/v1/auth/login"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	// The database is the one the configuration's runtime.data_dir names.
	if !strings.HasSuffix(h.req.DatabasePath, "/data/db/channels.db") || !filepath.IsAbs(h.req.DatabasePath) {
		t.Errorf("database path = %q, want <config dir>/data/db/channels.db", h.req.DatabasePath)
	}
	_ = path
}

func TestAdminDefaultsTheFullNameToTheUsername(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &adminHarness{stdin: "bob\n\nlong-enough\nlong-enough\n", env: env}
	if code := h.app().Run(context.Background(), []string{"admin"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, h.stderr.String())
	}
	if h.name != "bob" {
		t.Fatalf("full name = %q, want the username", h.name)
	}
}

func TestAdminRefusesMismatchedPasswords(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &adminHarness{stdin: "alice\nAlice\nfirst-password\nsecond-password\n", env: env}
	code := h.app().Run(context.Background(), []string{"admin"})
	if code != 1 || h.called {
		t.Fatalf("exit = %d, called = %v", code, h.called)
	}
	if !strings.Contains(h.stderr.String(), "do not match") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestAdminReportsAnInvalidAccountAsAUsageError(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &adminHarness{
		stdin: "alice\nAlice\nshort\nshort\n",
		env:   env,
		err:   channelstore.ErrInvalidUser,
	}
	if code := h.app().Run(context.Background(), []string{"admin"}); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(h.stderr.String(), "invalid user") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestAdminReportsAMissingDatabaseAsAFailure(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &adminHarness{stdin: "alice\nAlice\nlong-enough\nlong-enough\n", env: env, err: errors.New("no database")}
	code := h.app().Run(context.Background(), []string{"admin"})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(h.stderr.String(), "long-enough") {
		t.Fatal("the password reached stderr")
	}
}

func TestAdminSaysWhenItResetAnExistingAccount(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &adminHarness{
		stdin: "alice\nAlice\nlong-enough\nlong-enough\n",
		env:   env,
		// The implementation reports an existing username as not created.
		result: cli.AdminResult{Username: "alice", UserID: 7,
			Permissions: channelstore.GlobalPermissions()},
	}
	if code := h.app().Run(context.Background(), []string{"admin"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(h.stdout.String(), "promoted the existing account") ||
		!strings.Contains(h.stdout.String(), "password was reset") {
		t.Fatalf("stdout = %s", h.stdout.String())
	}
}

func TestAdminRejectsUnknownArguments(t *testing.T) {
	_, env := writeRuntimeConfig(t, officialClient)
	h := &adminHarness{env: env}
	if code := h.app().Run(context.Background(), []string{"admin", "--nope"}); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if h.called {
		t.Fatal("an unknown flag still ran the command")
	}
}

func TestHelpMentionsAdmin(t *testing.T) {
	h := &adminHarness{env: map[string]string{"HOME": t.TempDir()}}
	if code := h.app().Run(context.Background(), []string{"help"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(h.stdout.String(), "telesync admin") {
		t.Fatalf("help does not mention admin:\n%s", h.stdout.String())
	}
}
