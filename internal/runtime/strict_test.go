package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/runtime"
)

// A deployment whose WordPress key is broken must say so, not start with the
// WordPress routes switched off: that failure was only ever discovered by
// reading one error body in the panel.

func TestDaemonSaysWordPressIsDisabledWhenItsKeyIsBroken(t *testing.T) {
	// A loopback daemon starts — the rest of the API works — but the log says,
	// before anything is served, why the WordPress routes are off.
	var lines []string
	called := false
	err := runtime.RunDaemon(context.Background(), runtime.DaemonOptions{
		Config: appconfig.Config{
			API:     appconfig.APIConfig{ListenAddr: "127.0.0.1:0"},
			Clients: []appconfig.Client{{Key: "off", Type: appconfig.Official, Enabled: false}},
		},
		Listen:                func(string) (net.Listener, error) { called = true; return nil, errStop },
		Supervisor:            nil,
		WordPressUnconfigured: errors.New("secrets: key is not set: TELESYNC_WORDPRESS_KEY is not set"),
		Logf: func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	})
	if err == nil {
		t.Fatal("the daemon did not return after its listener failed")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "TELESYNC_WORDPRESS_KEY") {
		t.Fatalf("the log does not name the problem: %q err=%v", lines, err)
	}
	if !called {
		t.Fatal("the daemon never opened its listener")
	}
}

func TestDaemonRefusesANonLoopbackListenerWhenWordPressIsUnconfigured(t *testing.T) {
	// On a loopback listener the operator reads the log; on any other address
	// the daemon serves other people, who would only meet the WordPress
	// routes through their 503.
	err := runtime.RunDaemon(context.Background(), runtime.DaemonOptions{
		Config: appconfig.Config{
			API:     appconfig.APIConfig{ListenAddr: "0.0.0.0:0"},
			Clients: []appconfig.Client{{Key: "off", Type: appconfig.Official, Enabled: false}},
		},
		Listen:                func(string) (net.Listener, error) { return nil, errStop },
		WordPressUnconfigured: errors.New("secrets: key is not set"),
	})
	if err == nil || !strings.Contains(err.Error(), "api.wordpress") {
		t.Fatalf("err = %v, want a refusal naming api.wordpress", err)
	}
}

func TestDaemonStartsOnLoopbackWithoutWordPress(t *testing.T) {
	// The operator running a loopback daemon is told, in the log, and the rest
	// of the API keeps working.
	var logged bool
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	_ = runtime.RunDaemon(ctx, runtime.DaemonOptions{
		Config: appconfig.Config{
			API:     appconfig.APIConfig{ListenAddr: "127.0.0.1:0"},
			Clients: []appconfig.Client{{Key: "off", Type: appconfig.Official, Enabled: false}},
		},
		Listen:     func(string) (net.Listener, error) { return nil, errStop },
		Supervisor: nil,
		Logf: func(format string, args ...any) {
			if strings.Contains(fmt.Sprintf(format, args...), "wordpress") {
				logged = true
			}
		},
	})
	if !logged {
		t.Fatal("the daemon did not say WordPress is disabled")
	}
}

var errStop = errors.New("stop the test")
