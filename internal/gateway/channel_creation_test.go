package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// Creation routes are static channel resources and must be dispatched before
// the existing /channels/{channel_id} detail route.
func TestCreateChannelRouteIsRegistered(t *testing.T) {
	env := newEnv(t, registry{"official": readyWorker()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/official/channels", strings.NewReader(
		`{"title":"","description":"توضیح","visibility":"private"}`,
	))
	req.Header.Set("Authorization", "Bearer "+env.session)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_channel_title"`) {
		t.Fatalf("invalid title response = %d %s, want 400 invalid_channel_title", rec.Code, rec.Body.String())
	}
}

func TestCreateChannelPersistsAndReplaysIdempotently(t *testing.T) {
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	worker := readyWorker()
	worker.createResult = rpc.CreateChannelResult{ChannelCreated: true, Channel: &rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "1671633563", Type: "channel", Title: "Example", MembershipStatus: "member",
		IsMember: true, Permissions: rpc.Permissions{New: true, Edit: true, Delete: true, Read: true},
	}}}
	worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "1671633563", Type: "channel", Title: "Example", MembershipStatus: "member",
		IsMember: true, Permissions: rpc.Permissions{New: true, Edit: true, Delete: true, Read: true},
	}}
	env := newEnv(t, registry{"official": worker}, func(cfg *gateway.Config) {
		cfg.Channels = store
		cfg.ChannelCreations = store
	})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/official/channels", strings.NewReader(
			`{"title":"Example","description":"","visibility":"private"}`,
		))
		req.Header.Set("Authorization", "Bearer "+env.session)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "create-example-1")
		rec := httptest.NewRecorder()
		env.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"remote_channel_id":"1671633563"`) || !strings.Contains(rec.Body.String(), `"local_channel_id":`) {
			t.Fatalf("response does not distinguish remote/local ids: %s", rec.Body.String())
		}
		for _, permission := range []string{`"new":true`, `"edit":true`, `"delete":true`, `"read":true`} {
			if !strings.Contains(rec.Body.String(), permission) {
				t.Errorf("attempt %d created/replayed channel lost permission %s: %s", i+1, permission, rec.Body.String())
			}
		}
	}
	worker.mu.Lock()
	calls := worker.createCalls
	worker.mu.Unlock()
	if calls != 1 {
		t.Fatalf("remote create calls=%d, want 1", calls)
	}
	created, exists, err := store.Get(context.Background(), "official", 1671633563)
	if err != nil || !exists {
		t.Fatalf("created channel exists=%v error=%v, want local registration", exists, err)
	}
	if !created.PermissionsKnown || created.Permissions != (tgapi.Permissions{New: true, Edit: true, Delete: true, Read: true}) {
		t.Errorf("persisted created-channel permissions = %+v known=%v, want all Telegram capabilities known", created.Permissions, created.PermissionsKnown)
	}
	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/local/channels/1", nil)
	detailReq.Header.Set("Authorization", "Bearer "+env.session)
	detailRec := httptest.NewRecorder()
	env.handler.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("local channel detail status=%d body=%s", detailRec.Code, detailRec.Body.String())
	}
	for _, permission := range []string{`"new":true`, `"edit":true`, `"delete":true`, `"read":true`} {
		if !strings.Contains(detailRec.Body.String(), permission) {
			t.Errorf("local channel detail lost permission %s: %s", permission, detailRec.Body.String())
		}
	}
}

func TestUsernameAvailabilityStaticRouteDoesNotResolveAsChannelID(t *testing.T) {
	env := newEnv(t, registry{"official": readyWorker()})
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/telegram/official/channels/username-availability?username=example_channel", nil)
	req.Header.Set("Authorization", "Bearer "+env.session)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("availability route status=%d body=%s; static route must not reach channel-id parsing", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"username":"example_channel"`) ||
		!strings.Contains(rec.Body.String(), `"status":"unclaimed"`) {
		t.Fatalf("unexpected availability response: %s", rec.Body.String())
	}
}
