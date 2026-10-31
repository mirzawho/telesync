package gateway_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

// Publishing is the one route that writes to Telegram's content. It must not
// be reachable without the publishing permission, it must not accept a source
// the caller cannot read or a destination it cannot see, and it must never
// send twice for one idempotency key.

type publishResponse struct {
	Data struct {
		Message     *rpc.Message `json:"message"`
		Destination struct {
			ClientKey string `json:"client_key"`
			ChannelID string `json:"channel_id"`
		} `json:"destination"`
	} `json:"data"`
}

func publishBody(localID int64, messageID int, text string) string {
	return fmt.Sprintf(`{
		"source": {"local_channel_id": %d, "message_id": %d},
		"destination": {"channel_id": "555"},
		"content": {"text": %q, "entities": []},
		"media": {"mode": "omit"},
		"options": {"silent": false, "disable_web_page_preview": false}
	}`, localID, messageID, text)
}

// publishEnv is an account suite with a source channel recorded and a worker
// that can both read it and send to the destination.
func publishEnv(t *testing.T, mutate ...func(*gateway.Config)) (*accountEnv, *fakeWorker, channelstore.Channel) {
	t.Helper()
	worker := readyWorker()
	worker.message = rpc.MessagePage{
		Channel:  rpc.Channel{ID: 555, Title: "Source"},
		Messages: []rpc.Message{{ID: 9, Text: "hello #news"}},
		Page:     rpc.PageInfo{Page: 1, PerPage: 1, Total: 1},
	}
	worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "555", Type: "channel", Title: "Destination",
		IsMember: true, Permissions: rpc.Permissions{New: true, Edit: true, Delete: true, Read: true},
	}}
	worker.send = rpc.SendResult{
		Message:   &rpc.Message{ID: 12345, Text: "outgoing"},
		ChannelID: "555",
	}
	env := newAccountEnv(t, registry{"official": worker}, mutate...)
	env.signedIn = true
	row := env.channelRow(t, "official", 555)
	return env, worker, row
}

// grantPublish adds the publishing permission to the suite's caller.
func grantPublish(t *testing.T, env *accountEnv, who identity) {
	t.Helper()
	current, err := env.store.PermissionsOf(t.Context(), who.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range current.Global {
		if p == channelstore.PermissionContentPublish {
			return
		}
	}
	current.Global = append(current.Global, channelstore.PermissionContentPublish)
	if err := env.store.ReplacePermissions(t.Context(), who.user.ID, current); err != nil {
		t.Fatal(err)
	}
}

func TestPublishNeedsACredential(t *testing.T) {
	env, _, row := publishEnv(t)
	resp := env.sendWith(t, "", http.MethodPost, "/api/v1/telegram/official/messages", nil,
		publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPublishNeedsThePublishingPermission(t *testing.T) {
	env, worker, row := publishEnv(t)
	// A caller with the ordinary rights but not the publishing one.
	plain := env.account(t, "plain", channelstore.PermissionChannelsView)
	resp := env.send(t, plain.token, http.MethodPost,
		"/api/v1/telegram/official/messages", publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "publish_forbidden" {
		t.Fatalf("code = %q, want publish_forbidden", code)
	}
	if worker.sendCalls != 0 {
		t.Fatal("a caller without content.publish must not reach the worker")
	}
}

func TestPublishSendsThePreparedTextThroughTheNamedConnection(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/official/messages", publishBody(row.ID, 9, "edited #news"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if worker.sendCalls != 1 {
		t.Fatalf("worker sends = %d", worker.sendCalls)
	}
	if worker.sentReq.ChannelID != 555 {
		t.Fatalf("destination channel = %d", worker.sentReq.ChannelID)
	}
	if worker.sentReq.Text != "edited #news" {
		t.Fatalf("sent text = %q", worker.sentReq.Text)
	}
	var out publishResponse
	decodeBody(t, resp, &out)
	if out.Data.Message == nil || out.Data.Message.ID != 12345 {
		t.Fatalf("message = %+v", out.Data.Message)
	}
	if out.Data.Destination.ClientKey != "official" || out.Data.Destination.ChannelID != "555" {
		t.Fatalf("destination = %+v", out.Data.Destination)
	}
}

func TestPublishRefusesASourceTheCallerCannotRead(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)
	// A per-record view deny on the source channel.
	env.grantChannels(t, env.defaults, channelstore.ResourcePermissions{
		ResourceID:  row.ID,
		Permissions: []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: channelstore.EffectDeny}},
	})

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/official/messages", publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "publish_source_forbidden" {
		t.Fatalf("code = %q", code)
	}
	if worker.sendCalls != 0 {
		t.Fatal("a refused source must not reach the worker")
	}
}

func TestPublishRefusesASourceMessageThatIsNotThere(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)
	worker.messageE = rpc.NewError(rpc.CodeNotFound, "not found", 0)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/official/messages", publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "publish_source_unavailable" {
		t.Fatalf("code = %q", code)
	}
}

func TestPublishRefusesAnUnknownDestinationConnection(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/missing/messages", publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "unknown_client" {
		t.Fatalf("code = %q", code)
	}
	if worker.sendCalls != 0 {
		t.Fatal("an unknown connection must not be answered by another one")
	}
}

func TestPublishRefusesADestinationTheAccountCannotPostTo(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)
	worker.channel.Channel.Permissions.New = false

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/official/messages", publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "publish_permission_denied" {
		t.Fatalf("code = %q", code)
	}
	if worker.sendCalls != 0 {
		t.Fatal("a destination the account cannot post to must be refused before sending")
	}
}

func TestPublishRefusesADestinationTheCallerCannotSee(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)
	// The destination is a different channel record the caller may not view.
	other := env.channelRow(t, "official", 777)
	env.grantChannels(t, env.defaults, channelstore.ResourcePermissions{
		ResourceID:  row.ID,
		Permissions: []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: channelstore.EffectAllow}},
	}, channelstore.ResourcePermissions{
		ResourceID:  other.ID,
		Permissions: []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: channelstore.EffectDeny}},
	})
	worker.channel.Channel.ID = "777"

	body := strings.Replace(publishBody(row.ID, 9, "hello"), `"channel_id": "555"`, `"channel_id": "777"`, 1)
	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/telegram/official/messages", body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "publish_destination_forbidden" {
		t.Fatalf("code = %q", code)
	}
	if worker.sendCalls != 0 {
		t.Fatal("a destination the caller cannot see must not be sent to")
	}
}

func TestPublishReplaysAConfirmedResultInsteadOfSendingAgain(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)
	body := publishBody(row.ID, 9, "hello")

	first := env.sendWith(t, defaultCaller, http.MethodPost, "/api/v1/telegram/official/messages",
		map[string]string{"Idempotency-Key": "op-1"}, body)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d", first.StatusCode)
	}
	var firstOut publishResponse
	decodeBody(t, first, &firstOut)

	again := env.sendWith(t, defaultCaller, http.MethodPost, "/api/v1/telegram/official/messages",
		map[string]string{"Idempotency-Key": "op-1"}, body)
	if again.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d", again.StatusCode)
	}
	if worker.sendCalls != 1 {
		t.Fatalf("worker sends = %d, want 1 — the retry must not post again", worker.sendCalls)
	}
	var secondOut publishResponse
	decodeBody(t, again, &secondOut)
	if secondOut.Data.Message == nil || secondOut.Data.Message.ID != firstOut.Data.Message.ID {
		t.Fatalf("replayed message = %+v", secondOut.Data.Message)
	}
}

func TestPublishRefusesAnIdempotencyKeyUsedForADifferentRequest(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)

	first := env.sendWith(t, defaultCaller, http.MethodPost, "/api/v1/telegram/official/messages",
		map[string]string{"Idempotency-Key": "op-1"}, publishBody(row.ID, 9, "hello"))
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d", first.StatusCode)
	}
	other := env.sendWith(t, defaultCaller, http.MethodPost, "/api/v1/telegram/official/messages",
		map[string]string{"Idempotency-Key": "op-1"}, publishBody(row.ID, 9, "different"))
	if other.StatusCode != http.StatusConflict {
		t.Fatalf("second status = %d, want 409", other.StatusCode)
	}
	if code := errorCode(t, other); code != "publish_idempotency_conflict" {
		t.Fatalf("code = %q", code)
	}
	if worker.sendCalls != 1 {
		t.Fatalf("worker sends = %d, want 1", worker.sendCalls)
	}
}

func TestPublishRejectsAnUnknownBodyField(t *testing.T) {
	env, _, row := publishEnv(t)
	grantPublish(t, env, env.defaults)
	body := strings.Replace(publishBody(row.ID, 9, "hello"),
		`"options"`, `"unexpected": true, "options"`, 1)
	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/telegram/official/messages", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// --- helpers ---

// sendWith sends with extra headers (the idempotency key).
func (e *accountEnv) sendWith(t *testing.T, who, method, path string, headers map[string]string, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	if who == defaultCaller {
		who = e.session
		if e.signedIn {
			who = e.defaults.token
		}
	}
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+who)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec.Result()
}

func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	return body.Error.Code
}

func TestChannelsCapabilityFilterKeepsOnlySendableChannels(t *testing.T) {
	worker := readyWorker()
	worker.channels = rpc.ChannelPage{
		Channels: []rpc.Channel{
			{ID: 1, Title: "Can post", Permissions: rpc.Permissions{New: true, Read: true}},
			{ID: 2, Title: "Read only", Permissions: rpc.Permissions{New: false, Read: true}},
			{ID: 3, Title: "Unknown", Permissions: rpc.Permissions{Read: true}},
		},
		Page: rpc.PageInfo{Page: 1, PerPage: 10, Total: 3},
	}
	env := newAccountEnv(t, registry{"official": worker})
	resp := env.send(t, defaultCaller, http.MethodGet,
		"/api/v1/telegram/official/channels?page=1&per_page=10&capability=send", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Channels []struct {
				ID int64 `json:"id"`
			} `json:"channels"`
		} `json:"data"`
	}
	decodeBody(t, resp, &out)
	if len(out.Data.Channels) != 1 || out.Data.Channels[0].ID != 1 {
		t.Fatalf("channels = %+v, want only the sendable one", out.Data.Channels)
	}
}

func TestChannelsRejectsAnUnknownCapability(t *testing.T) {
	worker := readyWorker()
	env := newAccountEnv(t, registry{"official": worker})
	resp := env.send(t, defaultCaller, http.MethodGet,
		"/api/v1/telegram/official/channels?capability=delete", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
