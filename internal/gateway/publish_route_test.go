package gateway_test

// The copy route's contract, stated at the address itself.
//
// publish_test.go and publish_media_test.go pin what a copy does to Telegram.
// This file pins the address it is served at: that /messages/copy answers, that
// the old /messages address is gone, that the mount prefix reaches it, and that
// moving the route does not invalidate a stored idempotency record.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
)

// copyPath is where a copy of an existing message is requested. The old
// address, without "/copy", is asserted below to be gone.
const copyPath = "/api/v1/telegram/official/messages/copy"

// oldCopyPath is the address this route used to be served at.
const oldCopyPath = "/api/v1/telegram/official/messages"

func TestCopyRouteAcceptsPost(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)

	resp := env.send(t, defaultCaller, http.MethodPost, copyPath, publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if worker.sendCalls != 1 {
		t.Fatalf("worker sends = %d, want 1", worker.sendCalls)
	}
	var out publishResponse
	decodeBody(t, resp, &out)
	if out.Data.Destination.ClientKey != "official" || out.Data.Destination.ChannelID != "555" {
		t.Fatalf("destination = %+v, want the named connection and channel", out.Data.Destination)
	}
}

// TestCopyRouteRequiresContentPublish is the permission the copy route owes:
// an account that may read channels but not publish is refused before anything
// is resolved, and nothing reaches a worker.
func TestCopyRouteRequiresContentPublish(t *testing.T) {
	env, worker, row := publishEnv(t)
	plain := env.account(t, "plain", channelstore.PermissionChannelsView)

	resp := env.send(t, plain.token, http.MethodPost, copyPath, publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "publish_forbidden" {
		t.Fatalf("code = %q, want publish_forbidden", code)
	}
	if worker.sendCalls != 0 {
		t.Fatalf("worker sends = %d, want 0 — a caller without content.publish must not reach a worker", worker.sendCalls)
	}
}

// TestCopyRouteConsumesSourceMessage is what makes this a copy rather than a
// send: the request names an existing message, and one that names none is
// refused outright instead of being sent as free text.
func TestCopyRouteConsumesSourceMessage(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)

	// The copy is built from the named source message.
	resp := env.send(t, defaultCaller, http.MethodPost, copyPath, publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if worker.sendCalls != 1 {
		t.Fatalf("worker sends = %d, want 1", worker.sendCalls)
	}

	// A request that names no source is not a copy of anything and is refused.
	// This is the shape the copy route will keep when a separate send route is
	// added: no source, no copy.
	noSource := `{
		"destination": {"channel_id": "555"},
		"content": {"text": "free text", "entities": []},
		"media": {"mode": "omit"},
		"options": {"silent": false, "disable_web_page_preview": false}
	}`
	second := env.send(t, defaultCaller, http.MethodPost, copyPath, noSource)
	if second.StatusCode != http.StatusBadRequest {
		t.Fatalf("a request with no source: status = %d, want 400", second.StatusCode)
	}
	if code := errorCode(t, second); code != "invalid_input" {
		t.Fatalf("a request with no source: code = %q, want invalid_input", code)
	}
	if worker.sendCalls != 1 {
		t.Fatalf("worker sends = %d, want 1 — a sourceless request must not be sent", worker.sendCalls)
	}
}

// TestCopyRouteIsVisibleUnderAdminMount: the panel mounts the API under
// /admin, and the copy route must be reachable there exactly as it is at the
// root. The mount strips the prefix before routing, so this is one route, not
// two.
func TestCopyRouteIsVisibleUnderAdminMount(t *testing.T) {
	env, worker, row := publishEnv(t, func(c *gateway.Config) { c.BasePath = "/admin" })
	grantPublish(t, env, env.defaults)

	resp := env.send(t, defaultCaller, http.MethodPost, "/admin"+copyPath, publishBody(row.ID, 9, "hello"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 under the /admin mount", resp.StatusCode)
	}
	if worker.sendCalls != 1 {
		t.Fatalf("worker sends = %d, want 1", worker.sendCalls)
	}
}

// TestIdempotencySurvivesRouteRename: a stored attempt is fingerprinted on the
// request body and the operation key, never on the path it arrived at. Moving
// the route must therefore leave an earlier confirmed result replayable, and a
// replay must not post again.
func TestIdempotencySurvivesRouteRename(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)
	payload := publishBody(row.ID, 9, "hello")

	first := env.sendWith(t, defaultCaller, http.MethodPost, copyPath,
		map[string]string{"Idempotency-Key": "rename-1"}, payload)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode)
	}
	if first.Header.Get("Idempotent-Replay") != "" {
		t.Fatalf("the first send must not report a replay: %q", first.Header.Get("Idempotent-Replay"))
	}

	again := env.sendWith(t, defaultCaller, http.MethodPost, copyPath,
		map[string]string{"Idempotency-Key": "rename-1"}, payload)
	if again.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want 200", again.StatusCode)
	}
	if got := again.Header.Get("Idempotent-Replay"); got != "true" {
		t.Fatalf("Idempotent-Replay = %q, want true — the same key and body must replay", got)
	}
	if worker.sendCalls != 1 {
		t.Fatalf("worker sends = %d, want 1 — a replay must not post again", worker.sendCalls)
	}
}

// TestCopyRouteAnswersMethodNotAllowed is the copy route's own method rule: it
// writes, so it is POST, and another verb is refused at the route rather than
// being routed somewhere else.
func TestCopyRouteAnswersMethodNotAllowed(t *testing.T) {
	env, _, row := publishEnv(t)
	grantPublish(t, env, env.defaults)

	resp := env.send(t, defaultCaller, http.MethodGet, copyPath, "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", allow)
	}
	if code := errorCode(t, resp); code != "method_not_allowed" {
		t.Fatalf("code = %q, want method_not_allowed", code)
	}
	_ = row
}

// TestOldPublishRouteIsGone: the address this route used to live at answers
// nothing at all. It is not a redirect, not an alias and not a compatibility
// shim — a caller sent there is told the path is unknown and where the copy
// route is, which is how this gateway already answers a misspelled resource.
//
// It answers 404 rather than 405 for every verb. A 405 carries an Allow header
// naming the methods the path does support, and none are supported here: an
// Allow of POST beside a refusal of POST, or of GET beside a GET that would
// 404, is a claim about a path that has no routes.
func TestOldPublishRouteIsGone(t *testing.T) {
	env, worker, row := publishEnv(t)
	grantPublish(t, env, env.defaults)
	payload := publishBody(row.ID, 9, "hello")

	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		resp := env.send(t, defaultCaller, method, oldCopyPath, payload)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s: status = %d, want 404", method, oldCopyPath, resp.StatusCode)
		}
		got := decode[errorEnvelope](t, resp)
		if got.Error.Code != "not_found" {
			t.Fatalf("%s %s: code = %q, want not_found", method, oldCopyPath, got.Error.Code)
		}
		if !strings.Contains(got.Error.Message, "/messages/copy") {
			t.Fatalf("%s %s: the message should name the copy route: %q",
				method, oldCopyPath, got.Error.Message)
		}
	}
	if worker.callCount() != 0 {
		t.Fatalf("worker calls = %d, want 0 — a removed route must never reach a worker", worker.callCount())
	}
}
