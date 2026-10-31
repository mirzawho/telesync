package gateway_test

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mirza/telesync/internal/avatarcache"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

// logSink is a gateway log that a test can read while requests run.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) find(needles ...string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		found := true
		for _, needle := range needles {
			if !strings.Contains(line, needle) {
				found = false
				break
			}
		}
		if found {
			return line
		}
	}
	return ""
}

// TestLocalChannelAvatarUpstreamFailureIsReportedWithItsCause covers the avatar
// route when the worker answers with an error the gateway does not classify: the
// caller gets a 502 whose body names nothing upstream, and the daemon's log must
// carry the real cause, because that log is the only place it appears.
func TestLocalChannelAvatarUpstreamFailureIsReportedWithItsCause(t *testing.T) {
	logs := &logSink{}
	reg := registry{}
	worker := readyWorker()
	reg["official"] = worker
	cache, err := avatarcache.Open(filepath.Join(t.TempDir(), "avatars"), avatarcache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	env := newAccountEnv(t, reg, func(c *gateway.Config) {
		c.Avatars = cache
		c.Logf = logs.logf
	})
	env.signedIn = true
	row := env.localRow(t, "official", 500, "first", "")
	worker.avatarE = errors.New("rpc error: rpc error code 400: FILE_REFERENCE_EXPIRED")

	resp := env.send(t, defaultCaller, http.MethodGet,
		fmt.Sprintf("%s/%d/avatar", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("avatar = %d, want 502", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "worker_error" {
		t.Fatalf("code = %q, want worker_error", body.Error.Code)
	}
	if strings.Contains(body.Error.Message, "FILE_REFERENCE") {
		t.Fatalf("the upstream detail leaked into the response: %q", body.Error.Message)
	}
	if line := logs.find("op=local_channel_avatar", "FILE_REFERENCE_EXPIRED"); line == "" {
		t.Fatalf("the daemon log does not name the upstream cause; logged %q", logs.lines)
	}
}

// TestLocalChannelAvatarWithoutAPhotoIs404 keeps "no photo" out of the 502
// path on the local route, where the row still says it has one.
func TestLocalChannelAvatarWithoutAPhotoIs404(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	workers["official"].avatarE = rpc.NewError(rpc.CodeNoAvatar, "none", 0)

	resp := env.send(t, defaultCaller, http.MethodGet,
		fmt.Sprintf("%s/%d/avatar", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("avatar without a photo = %d, want 404", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != gateway.CodeNotFound {
		t.Fatalf("code = %q, want not_found", body.Error.Code)
	}
}
