package gateway_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/gateway"
)

// The security invariants of the automation area. Each one is a property the API
// must have whatever a caller sends, and each is checked against the answer and
// against the row behind it.

// TestAutomationNeverReturnsAWebhookSecret is the write-only rule: the secret is
// sent in, sealed, and never read back. The only fact a reader gets is whether
// one exists.
func TestAutomationNeverReturnsAWebhookSecret(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks",
		autoWebhookPOST("Secretive", source))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	if !created.Webhook.HasSecret {
		t.Error("has_secret = false, want true")
	}
	autoNoSecret(t, rec.Body.String(), "the create answer")
	if strings.Contains(rec.Body.String(), `"secret"`) {
		t.Errorf("the create answer echoes a secret field: %s", rec.Body.String())
	}

	id := created.Webhook.ID
	at := autoAPI + "/webhooks/" + strconv.FormatInt(id, 10)
	for _, p := range []autoProbe{
		{http.MethodGet, at},
		{http.MethodGet, autoAPI + "/webhooks"},
	} {
		rec := e.call(t, defaultCaller, p.method, p.path, "")
		autoOK(t, rec)
		autoNoSecret(t, rec.Body.String(), p.method+" "+p.path)
	}

	// The stored row holds a sealed box, never the plaintext.
	box, found, err := e.store.WebhookSecretBox(t.Context(), id)
	if err != nil || !found {
		t.Fatalf("read the sealed secret: found=%v err=%v", found, err)
	}
	if string(box.Ciphertext) == autoSecret || strings.Contains(string(box.Ciphertext), autoSecret) {
		t.Error("the stored secret box holds the plaintext")
	}
}

// TestAutomationHistoryNeverRecordsACredential checks the delivery history: the
// request that left the process really carried the credential, and the record
// that survives it does not.
func TestAutomationHistoryNeverRecordsACredential(t *testing.T) {
	for _, mode := range []string{"hmac", "bearer", "header"} {
		t.Run(mode, func(t *testing.T) {
			e := newAutoEnv(t)
			source := e.channelWithID(t, 1001)
			e.grantChannelView(t, e.defaults, source)

			body := map[string]any{
				"name": "Redacted", "url": autoWebhookURL, "method": "POST",
				"auth_mode": mode, "secret": autoSecret, "enabled": true,
				"source_channel_ids": []int64{source},
			}
			if mode == "header" {
				body["auth_header"] = "X-API-Key"
			}
			rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, body))
			autoCreated(t, rec)
			var created autoWebhookBody
			autoDecode(t, rec, &created)
			id := created.Webhook.ID

			stored := e.deliver(t, id, e.seedEvent(t, source, 1001, 42))

			// The request that left the process really was authenticated.
			sent := e.transport.seen()
			if len(sent) != 1 {
				t.Fatalf("receiver saw %d requests, want 1", len(sent))
			}
			switch mode {
			case "bearer":
				if got := sent[0].Header.Get("Authorization"); got != "Bearer "+autoSecret {
					t.Errorf("the outgoing Authorization = %q, want the bearer secret", got)
				}
			case "header":
				if got := sent[0].Header.Get("X-API-Key"); got != autoSecret {
					t.Errorf("the outgoing X-API-Key = %q, want the secret", got)
				}
			case "hmac":
				if sent[0].Header.Get("webhook-signature") == "" {
					t.Error("the outgoing request carries no signature")
				}
			}

			// The history that survives it does not.
			rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/webhooks/"+
				strconv.FormatInt(id, 10)+"/deliveries/"+strconv.FormatInt(stored.ID, 10), "")
			autoOK(t, rec)
			raw := rec.Body.String()
			autoNoSecret(t, raw, "the delivery detail")

			var one autoDeliveryBody
			autoDecode(t, rec, &one)
			if one.Delivery.Request != nil {
				assertRedacted(t, one.Delivery.Request.Headers, mode)
			}
			for _, att := range one.Delivery.Attempts {
				assertRedacted(t, att.RequestHeaders, mode)
			}
		})
	}
}

// assertRedacted checks one recorded header map: the credential is gone behind a
// placeholder and no value holds it.
func assertRedacted(t *testing.T, headers map[string]string, mode string) {
	t.Helper()
	for name, value := range headers {
		if strings.Contains(value, autoSecret) {
			t.Errorf("header %s records the secret: %q", name, value)
		}
	}
	want := ""
	switch mode {
	case "bearer":
		want = "Authorization"
	case "header":
		want = "X-API-Key"
	}
	if want == "" {
		return
	}
	for name, value := range headers {
		if !strings.EqualFold(name, want) {
			continue
		}
		if value != "[redacted]" {
			t.Errorf("%s is recorded as %q, want [redacted]", name, value)
		}
		return
	}
	t.Errorf("the redacted record drops the %s header entirely: %v", want, headers)
}

// TestAutomationRefusesAnAuthenticationWithoutASecret: a mode that sends a
// credential must have one to send.
func TestAutomationRefusesAnAuthenticationWithoutASecret(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	for _, mode := range []string{"hmac", "bearer", "header"} {
		for name, fields := range map[string]map[string]any{
			"empty secret":  {"secret": ""},
			"absent secret": {},
		} {
			t.Run(mode+" with an "+name, func(t *testing.T) {
				body := map[string]any{
					"name": "No secret", "url": autoWebhookURL, "method": "POST",
					"auth_mode": mode, "source_channel_ids": []int64{source},
				}
				for k, v := range fields {
					body[k] = v
				}
				if mode == "header" {
					body["auth_header"] = "X-API-Key"
				}
				rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, body))
				autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
			})
		}
	}

	// Explicitly choosing no authentication is accepted without one.
	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
		"name": "Open", "url": autoWebhookURL, "method": "POST",
		"auth_mode": "none", "source_channel_ids": []int64{source},
	}))
	autoCreated(t, rec)

	// And an edit that would leave a signed webhook with nothing to sign with is
	// refused too: removing the secret is only allowed alongside a mode that
	// sends none.
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks",
		autoWebhookPOST("Signed", source))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	at := autoAPI + "/webhooks/" + strconv.FormatInt(created.Webhook.ID, 10)

	rec = e.call(t, defaultCaller, http.MethodPatch, at, autoJSON(t, map[string]any{"secret": ""}))
	autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)

	// Dropping the secret together with the mode that needed it is accepted.
	rec = e.call(t, defaultCaller, http.MethodPatch, at,
		autoJSON(t, map[string]any{"secret": "", "auth_mode": "none"}))
	autoOK(t, rec)
	var after autoWebhookBody
	autoDecode(t, rec, &after)
	if after.Webhook.HasSecret {
		t.Error("has_secret = true after the stored secret was removed")
	}
}

// TestAutomationRefusesAReservedAuthenticationHeader: a custom header may not
// overwrite a transport header or the webhook signature headers themselves.
func TestAutomationRefusesAReservedAuthenticationHeader(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	reserved := []string{
		"Host", "Content-Length", "Connection", "Transfer-Encoding",
		"Content-Type", "Upgrade", "TE", "Trailer", "Authorization",
		"Proxy-Authorization",
		"webhook-id", "webhook-timestamp", "webhook-signature",
		"Webhook-Anything", "webhook-", "WEBHOOK-SIGNATURE",
	}
	for _, header := range reserved {
		rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
			"name": "Smuggler", "url": autoWebhookURL, "method": "POST",
			"auth_mode": "header", "auth_header": header, "secret": autoSecret,
			"source_channel_ids": []int64{source},
		}))
		autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
	}

	// An ordinary custom header is accepted.
	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
		"name": "Ordinary", "url": autoWebhookURL, "method": "POST",
		"auth_mode": "header", "auth_header": "X-API-Key", "secret": autoSecret,
		"source_channel_ids": []int64{source},
	}))
	autoCreated(t, rec)
}

// TestAutomationNeedsAPublicBaseURL is the rule that keeps a payload honest: no
// origin, no webhook that would have to send a link nobody can open.
func TestAutomationNeedsAPublicBaseURL(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	// A webhook is made while the deployment still names an origin, then the
	// origin is taken away — the deployment is rewired exactly as it would be
	// after the setting is cleared.
	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
		"name": "Originless", "url": autoWebhookURL, "method": "POST",
		"auth_mode": "none", "enabled": false, "source_channel_ids": []int64{source},
	}))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	id := created.Webhook.ID

	e.publicBaseURL = ""
	e.rewire(t)
	at := autoAPI + "/webhooks/" + strconv.FormatInt(id, 10)

	// Enabling it is refused, with the code that names the setting.
	rec = e.call(t, defaultCaller, http.MethodPatch, at, autoJSON(t, map[string]any{"enabled": true}))
	autoFail(t, rec, http.StatusBadRequest, gateway.CodeAutomationBaseURLRequired)

	// And a brand new one is refused outright, enabled or not: every payload it
	// would send needs an address a receiver can open.
	for _, enabled := range []bool{true, false} {
		rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
			"name": "No origin", "url": autoWebhookURL, "method": "POST",
			"auth_mode": "none", "enabled": enabled, "source_channel_ids": []int64{source},
		}))
		autoFail(t, rec, http.StatusBadRequest, gateway.CodeAutomationBaseURLRequired)
	}

	// The one that already exists is untouched and still readable.
	rec = e.call(t, defaultCaller, http.MethodGet, at, "")
	autoOK(t, rec)
	var one autoWebhookBody
	autoDecode(t, rec, &one)
	if one.Webhook.Enabled {
		t.Error("a refused enable left the webhook enabled")
	}
}

// TestAutomationRefusesASecondWorkflowForOneChannel: the schema allows one
// workflow per source channel and the API says so with a conflict rather than a
// database error.
func TestAutomationRefusesASecondWorkflowForOneChannel(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	body := autoJSON(t, map[string]any{
		"managed_channel_id": source, "enabled": false, "destinations": []any{},
	})
	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/workflows", body)
	autoCreated(t, rec)
	var first autoWorkflowBody
	autoDecode(t, rec, &first)

	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/workflows", body)
	autoFail(t, rec, http.StatusConflict, gateway.CodeAutomationConflict)

	// The existing workflow is the one that survived.
	rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/workflows", "")
	autoOK(t, rec)
	var list autoWorkflowsBody
	autoDecode(t, rec, &list)
	if len(list.Workflows) != 1 {
		t.Fatalf("workflows = %d, want 1", len(list.Workflows))
	}
	if list.Workflows[0].ID != first.Workflow.ID {
		t.Errorf("workflow id = %d, want the first one %d", list.Workflows[0].ID, first.Workflow.ID)
	}
}

// TestAutomationRefusesToMoveAWorkflowBetweenChannels: a workflow is the
// automation of one channel and its source is fixed at creation.
func TestAutomationRefusesToMoveAWorkflowBetweenChannels(t *testing.T) {
	e := newAutoEnv(t)
	first := e.channelWithID(t, 1001)
	second := e.channelWithID(t, 1002)
	e.grantChannelView(t, e.defaults, first, second)

	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": first, "enabled": false, "destinations": []any{},
	}))
	autoCreated(t, rec)
	var created autoWorkflowBody
	autoDecode(t, rec, &created)

	rec = e.call(t, defaultCaller, http.MethodPatch,
		autoAPI+"/workflows/"+strconv.FormatInt(created.Workflow.ID, 10),
		autoJSON(t, map[string]any{"managed_channel_id": second}))
	autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
	if e.storeWorkflow(t, created.Workflow.ID).ManagedChannelID != first {
		t.Error("a refused move changed the source channel")
	}
}

// TestAutomationRefusesAMalformedWebhookBody covers the request shapes the store
// cannot check: the method, the mode and the bounds.
func TestAutomationRefusesAMalformedWebhookBody(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	base := map[string]any{
		"name": "Shaped", "url": autoWebhookURL, "method": "POST",
		"auth_mode": "none", "source_channel_ids": []int64{source},
	}
	for name, patch := range map[string]map[string]any{
		"method is required":   {"method": nil},
		"method is not a verb": {"method": "GET"},
		"mode is unknown":      {"auth_mode": "digest"},
		"timeout is too small": {"timeout_ms": 10},
		"timeout is too large": {"timeout_ms": 400_000},
		"attempts is too many": {"max_attempts": 500},
		"attempts is too few":  {"max_attempts": 0},
		"url is relative":      {"url": "/hooks/telesync"},
		"name is missing":      {"name": ""},
		"source id is zero":    {"source_channel_ids": []int64{0}},
	} {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		for k, v := range patch {
			if v == nil {
				delete(body, k)
				continue
			}
			body[k] = v
		}
		rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}

	// An auth_header is only meaningful with auth_mode header.
	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
		"name": "Misused", "url": autoWebhookURL, "method": "POST",
		"auth_mode": "none", "auth_header": "X-API-Key", "source_channel_ids": []int64{source},
	}))
	autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)

	// A body that is not one JSON object at all.
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", `[]`)
	autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", `{"name": "x"}`)
	autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
}
