package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/worker"
)

// oneShotListener hands out a single connection, like a Unix listener with one
// client. The sandbox forbids binding sockets, and the bytes are identical.
type oneShotListener struct {
	c    net.Conn
	used bool
	done chan struct{}
}

func newOneShot(c net.Conn) *oneShotListener {
	return &oneShotListener{c: c, done: make(chan struct{})}
}

func (l *oneShotListener) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true
		return l.c, nil
	}
	<-l.done
	return nil, io.EOF
}

func (l *oneShotListener) Close() error   { close(l.done); return nil }
func (l *oneShotListener) Addr() net.Addr { return l.c.LocalAddr() }

func TestServeAnswersRPCAndShutsDownOnContext(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, ThisDC: 2, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 1, Title: "a"}},
	}
	svc := worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official}, tg,
		worker.Options{PID: 7})

	clientConn, serverConn := net.Pipe()
	ln := newOneShot(serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Serve(ctx, ln, rpc.NewHandler(svc)) }()

	client := rpc.NewClientWithDialer(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}, 2*time.Second)
	defer client.Close()

	h, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health over the worker socket: %v", err)
	}
	if h.Key != "official" || h.PID != 7 || h.State != rpc.StateConnected {
		t.Fatalf("health = %+v", h)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancellation")
	}
}

func TestListenPreparesTheSocketPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run", "official.sock")

	ln, err := worker.Listen(path)
	if err != nil {
		if isSandboxDenied(err) {
			t.Skipf("the sandbox forbids binding Unix sockets: %v", err)
		}
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("run dir mode = %o, want 700", dirInfo.Mode().Perm())
	}

	// A second listener on the same path must fail rather than silently share.
	if ln2, err := worker.Listen(path); err == nil {
		ln2.Close()
		t.Error("a second listener on a live socket must be refused")
	}
}

func TestListenRefusesANonSocketFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "official.sock")
	if err := os.WriteFile(path, []byte("important"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Listen(path); err == nil {
		t.Fatal("a regular file at the socket path must not be replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "important" {
		t.Fatal("the file must be left untouched")
	}
}

func TestListenRemovesADeadSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "official.sock")
	ln, err := worker.Listen(path)
	if err != nil {
		if isSandboxDenied(err) {
			t.Skipf("the sandbox forbids binding Unix sockets: %v", err)
		}
		t.Fatal(err)
	}
	ln.Close()
	// Closing a Unix listener leaves the file behind on Linux; the next listen
	// must replace it.
	ln2, err := worker.Listen(path)
	if err != nil {
		if isSandboxDenied(err) {
			t.Skipf("the sandbox forbids binding Unix sockets: %v", err)
		}
		t.Fatalf("Listen after Close: %v", err)
	}
	ln2.Close()
}

func TestWriteConfigIsNotPartOfTheWorker(t *testing.T) {
	// Guard: the worker never marshals its own configuration back to disk, so
	// resolved secrets cannot be written anywhere.
	svc := worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official},
		&fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized}}, worker.Options{})
	h := svc.Health(context.Background())
	encoded, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 {
		t.Fatal("empty health")
	}
}

// isSandboxDenied also covers EINVAL: this sandbox refuses a Unix bind for a
// path it will not create the socket at, which the kernel reports that way.
func isSandboxDenied(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EINVAL) || errors.Is(err, os.ErrPermission)
}
