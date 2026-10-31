package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/worker"
)

// logSink is a worker log a test can read while requests run.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// TestDiscoverFailuresAreLoggedBeforeTheyBecomeInternal covers the case the
// gateway cannot explain: the worker maps an adapter error onto the generic
// "internal" code, so the response says only "The worker failed to complete the
// request." The worker's own log must keep the real error, or nothing does.
func TestDiscoverFailuresAreLoggedBeforeTheyBecomeInternal(t *testing.T) {
	code, logs := avatarCallWithError(t, errors.New("rpc error: rpc error code 400: CHANNEL_INVALID"))
	if code != rpc.CodeInternal {
		t.Fatalf("code = %q, want %q", code, rpc.CodeInternal)
	}
	if !strings.Contains(logs.joined(), "CHANNEL_INVALID") {
		t.Fatalf("the worker log does not carry the adapter error; logged %q", logs.joined())
	}
}

// TestDiscoverErrorsStillMapToTheirCodes keeps classified errors as they are:
// the log gains a line, the wire answer does not change.
func TestDiscoverErrorsStillMapToTheirCodes(t *testing.T) {
	code, logs := avatarCallWithError(t, tgapi.ErrNumericIDUnresolved)
	if code != rpc.CodePeerUnresolved {
		t.Fatalf("code = %q, want %q", code, rpc.CodePeerUnresolved)
	}
	if !strings.Contains(logs.joined(), "peer_unresolved") {
		t.Fatalf("the worker log does not name the mapped code; logged %q", logs.joined())
	}
}

// avatarCallWithError runs one avatar RPC whose adapter fails with err, and
// returns the wire code and what the worker logged.
func avatarCallWithError(t *testing.T, err error) (string, *logSink) {
	t.Helper()
	logs := &logSink{}
	tg := &scriptedTelegram{
		status:    tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		avatarErr: err,
	}
	svc := worker.NewService(appconfig.Client{Key: "cat"}, tg, worker.Options{},
		worker.WithLogf(logs.logf))
	api := serveWorker(t, svc)

	resp := post(t, api, "/avatar", `{"channel_id":27}`)
	defer resp.Body.Close()
	var body rpc.ErrorBody
	if decodeErr := json.NewDecoder(resp.Body).Decode(&body); decodeErr != nil {
		t.Fatalf("decode: %v", decodeErr)
	}
	if body.Error == nil {
		t.Fatalf("a failed avatar must carry an error body")
	}
	return body.Error.Code, logs
}

// scriptedTelegram is a Telegram client whose discovery calls fail on demand.
type scriptedTelegram struct {
	status    tgapi.Status
	avatarErr error
}

func (s *scriptedTelegram) Status(context.Context) tgapi.Status { return s.status }
func (s *scriptedTelegram) Dialogs(context.Context) ([]tgapi.Channel, error) {
	return nil, errors.New("not scripted")
}
func (s *scriptedTelegram) History(context.Context, tgapi.Channel, int, int) (tgapi.HistoryPage, error) {
	return tgapi.HistoryPage{}, errors.New("not scripted")
}
func (s *scriptedTelegram) DownloadMedia(context.Context, tgapi.Channel, int) (tgapi.MediaFile, error) {
	return tgapi.MediaFile{}, errors.New("not scripted")
}
func (s *scriptedTelegram) Message(context.Context, tgapi.Channel, int) (tgapi.Message, error) {
	return tgapi.Message{}, errors.New("not scripted")
}
func (s *scriptedTelegram) FindChannels(context.Context, tgapi.ChannelQuery) ([]tgapi.ChannelInfo, error) {
	return nil, errors.New("not scripted")
}
func (s *scriptedTelegram) ResolveChannel(context.Context, tgapi.ChannelRef) (tgapi.ChannelInfo, error) {
	return tgapi.ChannelInfo{}, errors.New("not scripted")
}
func (s *scriptedTelegram) JoinChannel(context.Context, tgapi.ChannelRef) (tgapi.JoinResult, error) {
	return tgapi.JoinResult{}, errors.New("not scripted")
}
func (s *scriptedTelegram) DownloadChannelAvatar(context.Context, tgapi.ChannelRef) (tgapi.AvatarFile, error) {
	return tgapi.AvatarFile{}, s.avatarErr
}
func (s *scriptedTelegram) DownloadCustomEmoji(context.Context, int64) (tgapi.EmojiFile, error) {
	return tgapi.EmojiFile{}, errors.New("not scripted")
}
func (s *scriptedTelegram) LeaveChannel(context.Context, tgapi.ChannelRef) error {
	return errors.New("not scripted")
}
func (s *scriptedTelegram) SendMessage(context.Context, tgapi.ChannelRef, tgapi.SendRequest) (tgapi.SendResult, error) {
	return tgapi.SendResult{}, errors.New("not scripted")
}

// serveWorker puts a worker service behind the RPC handler on a pipe.
func serveWorker(t *testing.T, backend rpc.Backend) *http.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	srv := &http.Server{Handler: rpc.NewHandler(backend)}
	done := make(chan struct{})
	ln := newOneShot(serverConn)
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = clientConn.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
			DisableKeepAlives: false,
		},
		Timeout: 5 * time.Second,
	}
}

func post(t *testing.T, client *http.Client, path, body string) *http.Response {
	t.Helper()
	resp, err := client.Post("http://worker"+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
