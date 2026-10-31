package gotdclient_test

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/exchange"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/transport"

	"github.com/mirza/telesync/internal/profile"
	"github.com/mirza/telesync/internal/routing"
	"github.com/mirza/telesync/internal/sessionstore"
)

// pipeListener hands out paired net.Pipe connections. The sandbox forbids
// binding sockets, and a pipe carries exactly the same MTProto bytes.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, io.EOF
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

// dial creates one connection and hands the server end to Accept.
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
		client.Close()
		server.Close()
		return nil, io.EOF
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// recordedDial counts and records the addresses the resolver dialed.
type recordedDial struct {
	l    *pipeListener
	mu   sync.Mutex
	seen []string
}

func (r *recordedDial) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	r.mu.Lock()
	r.seen = append(r.seen, network+" "+addr)
	r.mu.Unlock()
	return r.l.dial(ctx, network, addr)
}

// specForCustom builds a custom-client spec that dials an in-memory server.
func specForCustom(t *testing.T, _ *pipeListener, rec *recordedDial, key exchange.PublicKey) *profile.Spec {
	t.Helper()

	store, err := sessionstore.NewAt(filepath.Join(t.TempDir(), "sessions", "the-cat.json"))
	if err != nil {
		t.Fatal(err)
	}

	resolver := routing.New(routing.Options{
		Host:     "mt.backend.test",
		Port:     8443,
		DCID:     2,
		Policy:   routing.SingleDC,
		Protocol: transport.Intermediate,
		Dial:     rec.dial,
	})

	return &profile.Spec{
		Kind:        profile.Custom,
		Key:         "the-cat",
		AppID:       1,
		AppHash:     "test-app-hash",
		SessionPath: store.Path(),
		Session:     store,
		DC:          2,
		Endpoint:    resolver.Endpoint(),
		PublicKeys:  []exchange.PublicKey{key},
		Resolver:    resolver,
		DCList: dcs.List{Options: []tg.DCOption{{
			ID: 2, IPAddress: "mt.backend.test", Port: 8443, Static: true,
		}}},
	}
}
