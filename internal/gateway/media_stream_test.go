package gateway_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/gateway"
)

// A media download is the one long transfer the public API makes: the bytes are
// pulled from Telegram a part at a time and forwarded as they arrive, so a 60 MB
// document never lands in this process. These tests hold the route to that
// promise. The route announces Content-Length before the first byte, so a
// transfer that stops early is not a smaller file — it is a broken one — and
// whatever else is bounded, the download itself must not be cut off part-way.

// pacedBody yields one byte at a time with a delay between bytes and stops as
// soon as ctx is done. It models the worker's HTTP body, which becomes
// unreadable the moment the context the request was made with expires.
type pacedBody struct {
	ctx   context.Context
	data  []byte
	off   int
	delay time.Duration
}

func (b *pacedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	time.Sleep(b.delay)
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	p[0] = b.data[b.off]
	b.off++
	return 1, nil
}

func (b *pacedBody) Close() error { return nil }

// brokenBody yields everything it holds and then fails. It is what a worker
// whose Telegram fetch died part-way hands back.
type brokenBody struct {
	data []byte
	off  int
	fail error
}

func (b *brokenBody) Read(p []byte) (int, error) {
	if b.off >= len(b.data) {
		return 0, b.fail
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

func (b *brokenBody) Close() error { return nil }

// streamEnv is a worker whose body the test supplies, so the test decides how
// long the transfer takes and where it stops.
func streamEnv(t *testing.T, mutate ...func(*gateway.Config)) (*testEnv, *fakeWorker) {
	t.Helper()
	official := readyWorker()
	official.mediaName = "clip.mp4"
	official.mediaType = "video/mp4"
	return newEnv(t, registry{"cat": official}, mutate...), official
}

func TestMediaDownloadOutlivesTheRequestTimeout(t *testing.T) {
	// RequestTimeout bounds one upstream call. A download is not one call: it is
	// a transfer that legitimately runs for minutes, so the budget that answers
	// a listing must not end it after the first few kilobytes.
	payload := []byte("0123456789abcdefghij")
	env, official := streamEnv(t, func(c *gateway.Config) {
		c.RequestTimeout = 40 * time.Millisecond
	})
	official.mediaSize = int64(len(payload))
	official.mediaBody = func(ctx context.Context) io.ReadCloser {
		return &pacedBody{ctx: ctx, data: payload, delay: 25 * time.Millisecond}
	}

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body := readAll(t, resp); body != string(payload) {
		t.Fatalf("body = %q, want %q: the request timeout cut the download short",
			body, payload)
	}
}

func TestMediaDownloadOutlivesTheServerWriteTimeout(t *testing.T) {
	// The same transfer must outlive the server's per-request write deadline.
	// The worker already clears its own for exactly this reason; the public
	// server's answer has to as well.
	payload := []byte("0123456789abcdefghij")
	env, official := streamEnv(t)
	official.mediaSize = int64(len(payload))
	official.mediaBody = func(ctx context.Context) io.ReadCloser {
		return &pacedBody{ctx: ctx, data: payload, delay: 25 * time.Millisecond}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler:           env.handler,
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      40 * time.Millisecond,
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	req, err := http.NewRequest(http.MethodGet,
		"http://"+ln.Addr().String()+"/api/v1/telegram/cat/channels/6/messages/24/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	got, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("read body: %v: the write deadline cut the transfer", readErr)
	}
	if string(got) != string(payload) {
		t.Fatalf("body = %q, want %q: the write deadline cut the transfer", got, payload)
	}
}

func TestMediaDownloadReportsATruncatedStream(t *testing.T) {
	// Once the headers are out the status cannot change, so a transfer that dies
	// can only be reported. The caller must be told, with the byte counts that
	// say how far it got — otherwise a broken download is indistinguishable from
	// a complete one.
	payload := []byte("0123456789")
	var mu sync.Mutex
	var lines []string
	env, official := streamEnv(t, func(c *gateway.Config) {
		c.Logf = func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		}
	})
	official.mediaSize = int64(len(payload))
	official.mediaBody = func(context.Context) io.ReadCloser {
		return &brokenBody{data: payload[:4], fail: errors.New("worker: the attachment ended early")}
	}

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media")
	if body := readAll(t, resp); body != string(payload[:4]) {
		t.Fatalf("body = %q, want %q", body, payload[:4])
	}
	if got := resp.Header.Get("Content-Length"); got != fmt.Sprint(len(payload)) {
		t.Fatalf("content-length = %q, want %q", got, fmt.Sprint(len(payload)))
	}

	mu.Lock()
	defer mu.Unlock()
	report := strings.Join(lines, "\n")
	if !strings.Contains(report, "op=media") {
		t.Fatalf("the truncation does not name the operation; log:\n%s", report)
	}
	if !strings.Contains(report, "4 of 10 bytes") {
		t.Fatalf("the truncation does not report the byte counts; log:\n%s", report)
	}
}

func TestMediaDownloadReportsAShortBodyThatDidNotFail(t *testing.T) {
	// A body that simply ends early is exactly as much a truncated transfer as
	// one that fails, and it is the quieter of the two: nothing returned an
	// error. It must be reported just the same.
	payload := []byte("0123456789")
	var mu sync.Mutex
	var lines []string
	env, official := streamEnv(t, func(c *gateway.Config) {
		c.Logf = func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		}
	})
	official.mediaSize = int64(len(payload))
	official.mediaBody = func(context.Context) io.ReadCloser {
		return io.NopCloser(strings.NewReader(string(payload[:4])))
	}

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media")
	if body := readAll(t, resp); body != string(payload[:4]) {
		t.Fatalf("body = %q, want %q", body, payload[:4])
	}

	mu.Lock()
	defer mu.Unlock()
	report := strings.Join(lines, "\n")
	if !strings.Contains(report, "op=media") {
		t.Fatalf("a short body was served as if it were whole; log:\n%s", report)
	}
}
