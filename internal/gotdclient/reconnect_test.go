package gotdclient_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/exchange"
	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/tgapi"
)

// eventLog collects the manager's connection lines.
type eventLog struct {
	mu    sync.Mutex
	lines []string
}

func (e *eventLog) add(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lines = append(e.lines, fmt.Sprintf(format, args...))
}

func (e *eventLog) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.lines...)
}

// waitFor blocks until a line contains substring.
func (e *eventLog) waitFor(t *testing.T, substring string, within time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, line := range e.all() {
			if strings.Contains(line, substring) {
				return e.all()
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no event containing %q within %s; got %v", substring, within, e.all())
	return nil
}

func (e *eventLog) contains(substring string) bool {
	for _, line := range e.all() {
		if strings.Contains(line, substring) {
			return true
		}
	}
	return false
}

// managerWithOptions builds a Manager over the in-memory server with explicit
// options and stops it with the test.
func managerWithOptions(t *testing.T, l *pipeListener, key exchange.PublicKey,
	opts gotdclient.ManagerOptions) *gotdclient.Manager {
	t.Helper()
	rec := &recordedDial{l: l}
	spec := specForCustom(t, l, rec, key)
	m := gotdclient.NewManager(spec, opts)

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	t.Cleanup(func() {
		cancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer waitCancel()
		_ = m.Wait(waitCtx)
	})
	return m
}

func TestManagerLogsConnectAndDisconnect(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{})

	events := &eventLog{}
	m := managerWithOptions(t, l, key, gotdclient.ManagerOptions{
		// Long enough that the watchdog never rebuilds a healthy client here.
		Reconnect: time.Hour,
		OnEvent:   events.add,
	})

	waitForState(t, m, tgapi.StateReady)
	events.waitFor(t, "connected to", 10*time.Second)
	if !events.contains("client the-cat: connected to mt.backend.test:8443") {
		t.Fatalf("events = %v", events.all())
	}
	if events.contains("disconnected") {
		t.Fatalf("a healthy connection must not log a disconnect: %v", events.all())
	}
	for _, line := range events.all() {
		if strings.Contains(strings.ToLower(line), "hash") {
			t.Fatalf("an event leaked a credential: %q", line)
		}
	}
}

func TestManagerDefaultReconnectIsTenSeconds(t *testing.T) {
	if gotdclient.DefaultReconnect != 10*time.Second {
		t.Fatalf("DefaultReconnect = %s", gotdclient.DefaultReconnect)
	}
}
