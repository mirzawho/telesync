package gateway_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/mirza/telesync/internal/gateway"
)

// The loopback exception at the API. The backend is authoritative: a request
// that asks for it is refused when the deployment does not offer it, and an
// update to some other field leaves the setting exactly where it was.

func allowLocalhostEnv(t *testing.T) *autoEnv {
	t.Helper()
	return newAutoEnv(t, func(c *gateway.Config) { c.WebhookAllowLocalhost = true })
}

func TestAWebhookMayAskForLocalhostWhenTheDeploymentAllowsIt(t *testing.T) {
	e := allowLocalhostEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)
	path := autoAPI + "/webhooks"

	rec := e.call(t, defaultCaller, http.MethodPost, path, autoJSON(t, map[string]any{
		"name":               "Local Development Hook",
		"enabled":            true,
		"url":                "http://localhost:3000/webhook",
		"method":             "POST",
		"auth_mode":          "none",
		"allow_localhost":    true,
		"timeout_ms":         5000,
		"max_attempts":       3,
		"source_channel_ids": []int64{source},
	}))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	if !created.Webhook.AllowLocalhost {
		t.Fatal("the setting was not stored")
	}

	// GET returns the stored value.
	at := path + "/" + strconv.FormatInt(created.Webhook.ID, 10)
	rec = e.call(t, defaultCaller, http.MethodGet, at, "")
	autoOK(t, rec)
	var got autoWebhookBody
	autoDecode(t, rec, &got)
	if !got.Webhook.AllowLocalhost {
		t.Fatal("the setting did not survive a read")
	}

	// An edit that says nothing about it leaves it alone.
	rec = e.call(t, defaultCaller, http.MethodPatch, at, autoJSON(t, map[string]any{
		"name": "Local Development Hook (renamed)",
	}))
	autoOK(t, rec)
	rec = e.call(t, defaultCaller, http.MethodGet, at, "")
	autoOK(t, rec)
	var renamed autoWebhookBody
	autoDecode(t, rec, &renamed)
	if !renamed.Webhook.AllowLocalhost {
		t.Fatal("renaming the webhook reset the setting")
	}

	// And it can be turned off.
	rec = e.call(t, defaultCaller, http.MethodPatch, at, autoJSON(t, map[string]any{
		"allow_localhost": false,
	}))
	autoOK(t, rec)
	rec = e.call(t, defaultCaller, http.MethodGet, at, "")
	autoOK(t, rec)
	var off autoWebhookBody
	autoDecode(t, rec, &off)
	if off.Webhook.AllowLocalhost {
		t.Fatal("the setting could not be turned off")
	}
}

func TestLocalhostIsOffForAWebhookThatNeverAsked(t *testing.T) {
	e := allowLocalhostEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
		"name":               "An ordinary one",
		"enabled":            true,
		"url":                autoWebhookURL,
		"method":             "POST",
		"auth_mode":          "none",
		"timeout_ms":         5000,
		"max_attempts":       3,
		"source_channel_ids": []int64{source},
	}))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	if created.Webhook.AllowLocalhost {
		t.Fatal("a webhook that never asked for the exception was given it")
	}
}

func TestTheDeploymentCanRefuseTheLocalhostException(t *testing.T) {
	// newAutoEnv does not turn the switch on, so this is the default: a client
	// that posts allow_localhost=true is refused rather than quietly ignored.
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
		"name":               "Should not be",
		"enabled":            true,
		"url":                "http://localhost:3000/webhook",
		"method":             "POST",
		"auth_mode":          "none",
		"allow_localhost":    true,
		"timeout_ms":         5000,
		"max_attempts":       3,
		"source_channel_ids": []int64{source},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
