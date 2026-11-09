package cli_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/cli"
)

// serve-webhook is a receiver for the delivery engine and nothing else: it
// needs no configuration file, no database and no Telegram client, so it runs
// whatever else the binary is able to do.

func webhookEnv() map[string]string {
	return map[string]string{
		"TELESYNC_TEST_WEBHOOK_HMAC_SECRET":   "test-hmac-secret",
		"TELESYNC_TEST_WEBHOOK_BEARER_TOKEN":  "test-bearer-token",
		"TELESYNC_TEST_WEBHOOK_HEADER_NAME":   "X-API-Key",
		"TELESYNC_TEST_WEBHOOK_HEADER_SECRET": "test-header-secret",
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestServeWebhookIsACommandOfItsOwn(t *testing.T) {
	h := &runtimeHarness{env: webhookEnv()}
	app := cli.New(h.deps())
	if got := app.Run(context.Background(), []string{"help"}); got != 0 {
		t.Fatalf("help exited %d", got)
	}
	if !strings.Contains(h.stdout.String(), "serve-webhook") {
		t.Fatalf("the usage does not mention serve-webhook:\n%s", h.stdout.String())
	}
	// The other commands are still there: this one is added to the command
	// line, not swapped in for it.
	for _, command := range []string{"daemon", "worker", "auth", "config validate", "admin", "hash-password"} {
		if !strings.Contains(h.stdout.String(), command) {
			t.Fatalf("the usage no longer mentions %q:\n%s", command, h.stdout.String())
		}
	}
}

func TestServeWebhookRefusesArgumentsItDoesNotKnow(t *testing.T) {
	for _, args := range [][]string{
		{"serve-webhook", "--nonsense"},
		{"serve-webhook", "--host"},
		{"serve-webhook", "--port"},
		{"serve-webhook", "--output-dir"},
		{"serve-webhook", "--port", "not-a-number"},
		{"serve-webhook", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := &runtimeHarness{env: webhookEnv()}
			if code := cli.New(h.deps()).Run(context.Background(), args); code != 2 {
				t.Fatalf("exit = %d, want 2\n%s", code, h.stderr.String())
			}
			if strings.TrimSpace(h.stderr.String()) == "" {
				t.Fatal("an argument error was reported silently")
			}
		})
	}
}

func TestServeWebhookReportsAnInvalidPortClearly(t *testing.T) {
	h := &runtimeHarness{env: webhookEnv()}
	code := cli.New(h.deps()).Run(context.Background(), []string{"serve-webhook", "--port", "70000"})
	if code == 0 {
		t.Fatal("a port outside the TCP range was accepted")
	}
	if !strings.Contains(h.stderr.String(), "70000") {
		t.Fatalf("the error does not name the port:\n%s", h.stderr.String())
	}
}

func TestServeWebhookRefusesToStartWithoutItsTestCredentials(t *testing.T) {
	h := &runtimeHarness{env: map[string]string{}}
	code := cli.New(h.deps()).Run(context.Background(), []string{"serve-webhook"})
	if code == 0 {
		t.Fatal("the receiver started with no credential configured")
	}
	out := h.stderr.String()
	for _, name := range []string{
		"TELESYNC_TEST_WEBHOOK_HMAC_SECRET",
		"TELESYNC_TEST_WEBHOOK_BEARER_TOKEN",
		"TELESYNC_TEST_WEBHOOK_HEADER_NAME",
		"TELESYNC_TEST_WEBHOOK_HEADER_SECRET",
	} {
		if !strings.Contains(out, name) {
			t.Fatalf("the error does not name %s:\n%s", name, out)
		}
	}
}

func TestServeWebhookBindsTheHostAndPortItWasGivenAndStopsOnInterrupt(t *testing.T) {
	port := freeTCPPort(t)
	h := &runtimeHarness{env: webhookEnv()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan int, 1)
	go func() {
		done <- cli.New(h.deps()).Run(ctx,
			[]string{"serve-webhook", "--host", "127.0.0.1", "--port", fmt.Sprint(port)})
	}()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(h.stdout.String(), fmt.Sprint(port)) {
		t.Fatalf("the receiver did not announce the port it bound:\n%s", h.stdout.String())
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d\n%s", code, h.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver did not stop on interrupt")
	}
	if !strings.Contains(h.stdout.String(), "Save responses: disabled") {
		t.Fatalf("the receiver did not describe itself:\n%s", h.stdout.String())
	}
}

func TestServeWebhookCanWriteRecordsAndSaysWhere(t *testing.T) {
	dir := t.TempDir()
	port := freeTCPPort(t)
	h := &runtimeHarness{env: webhookEnv()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan int, 1)
	go func() {
		done <- cli.New(h.deps()).Run(ctx, []string{
			"serve-webhook", "--port", fmt.Sprint(port),
			"--save-response", "--output-dir", dir,
		})
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "Save responses: enabled") {
		t.Fatalf("the receiver did not say it was saving:\n%s", out)
	}
	if !strings.Contains(out, dir) {
		t.Fatalf("the receiver did not say where it saves:\n%s", out)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver did not stop")
	}
}

func TestOtherCommandsAreStillWired(t *testing.T) {
	// A command that does not exist is still refused, and the runtime commands
	// still reach the same code they did: adding one command did not move the
	// command line out from under the others.
	h := &runtimeHarness{env: map[string]string{"HOME": t.TempDir()}}
	if code := cli.New(h.deps()).Run(context.Background(), []string{"nonsense"}); code != 2 {
		t.Fatalf("an unknown command exited %d", code)
	}
	if !strings.Contains(h.stderr.String(), `unknown command "nonsense"`) {
		t.Fatalf("an unknown command was not reported as such:\n%s", h.stderr.String())
	}

	// The daemon still gets as far as loading its configuration. It stops here
	// only because a starter file is not a runnable configuration, which is
	// about the fixture and not about the command line.
	h = &runtimeHarness{env: map[string]string{"HOME": t.TempDir()}}
	if code := cli.New(h.deps()).Run(context.Background(), []string{"daemon"}); code == 0 {
		t.Fatal("the daemon started from a starter configuration")
	}
	if strings.Contains(h.stderr.String(), "unknown command") {
		t.Fatalf("the daemon command is no longer dispatched:\n%s", h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "configuration is invalid") {
		t.Fatalf("the daemon command did not reach configuration loading:\n%s", h.stderr.String())
	}
}
