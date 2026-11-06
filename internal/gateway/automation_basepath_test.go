package gateway_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/webhooks"
)

// The panel is mounted at /admin and the API it calls is therefore at
// /admin/api/v1. The path constants are stated without the mount and matched
// against the mount plus themselves, so the same handlers answer at the root and
// under the prefix, with nothing registered twice and nothing double-prefixed.

// TestAutomationRoutesResolveUnderTheBasePath proves both forms reach the same
// handlers and that the mount is applied exactly once.
func TestAutomationRoutesResolveUnderTheBasePath(t *testing.T) {
	e := newAutoEnv(t)
	bare := e.handler
	e.basePath = autoBasePath
	e.rewire(t)
	mounted := e.handler

	// The same handler answers both forms of the same route.
	bareRec := autoDo(bare, http.MethodGet, "/api/v1/local/automation/status", operatorToken, "")
	if bareRec.Code != http.StatusOK {
		t.Fatalf("the root form answered %d: %s", bareRec.Code, bareRec.Body.String())
	}
	mountedRec := autoDo(mounted, http.MethodGet,
		autoBasePath+"/api/v1/local/automation/status", operatorToken, "")
	if mountedRec.Code != http.StatusOK {
		t.Fatalf("the mounted form answered %d: %s", mountedRec.Code, mountedRec.Body.String())
	}
	if bareRec.Body.String() != mountedRec.Body.String() {
		t.Errorf("the two forms answer differently:\n root:   %s\n mounted:%s",
			bareRec.Body.String(), mountedRec.Body.String())
	}

	// Every automation route answers under the mount, not only the status one.
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)
	created := autoDo(mounted, http.MethodPost, autoBasePath+autoAPI+"/webhooks",
		operatorToken, autoWebhookPOST("Mounted", source))
	if created.Code != http.StatusCreated {
		t.Fatalf("the mounted create answered %d: %s", created.Code, created.Body.String())
	}
	var hook autoWebhookBody
	autoDecode(t, created, &hook)

	for _, p := range []autoProbe{
		{http.MethodGet, autoAPI + "/status"},
		{http.MethodGet, autoAPI + "/webhooks"},
		{http.MethodGet, autoAPI + "/webhooks/" + strconv.FormatInt(hook.Webhook.ID, 10)},
		{http.MethodGet, autoAPI + "/webhooks/" + strconv.FormatInt(hook.Webhook.ID, 10) + "/deliveries"},
		{http.MethodGet, autoAPI + "/workflows"},
	} {
		rec := autoDo(mounted, p.method, autoBasePath+p.path, operatorToken, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s%s answered %d: %s", p.method, autoBasePath, p.path, rec.Code, rec.Body.String())
		}
	}

	// Nothing is registered twice: the mount is applied once, so a doubled
	// prefix is not a route.
	for _, path := range []string{
		autoBasePath + autoBasePath + "/api/v1/local/automation/status",
		autoBasePath + autoAPI + autoAPI + "/status",
	} {
		rec := autoDo(mounted, http.MethodGet, path, operatorToken, "")
		if rec.Code == http.StatusOK {
			t.Errorf("%s answered 200: the mount is applied more than once", path)
		}
	}

	// And the mounted handler does not quietly serve the un-prefixed form as a
	// second copy of the same routes. What it does answer is recorded rather
	// than asserted: a request that omits the mount is outside this server, and
	// serveLocal's own fallback currently answers 405 for it instead of 404.
	// That fallback lives in users.go and is reported, not changed here.
	away := autoDo(mounted, http.MethodGet, "/api/v1/local/automation/status", operatorToken, "")
	if away.Code == http.StatusOK {
		t.Errorf("the mounted handler also serves the un-prefixed form: the routes are duplicated (%s)",
			away.Body.String())
	}
	t.Logf("un-prefixed form under a %s mount answers %d: %s",
		autoBasePath, away.Code, strings.TrimSpace(away.Body.String()))
}

// TestWebhookPayloadMediaURLIsAbsoluteAndCorrect is the join the mission names:
// api.public_base_url + BasePath + /api/v1 + the media endpoint the channel
// messages API emits. It is checked on the bytes that actually left the process.
func TestWebhookPayloadMediaURLIsAbsoluteAndCorrect(t *testing.T) {
	for _, tc := range []struct {
		name     string
		basePath string
		want     string
	}{
		{"mounted under /admin", autoBasePath,
			"https://telesync.example/admin/api/v1/telegram/official/channels/1001/messages/42/media"},
		{"served at the root", "",
			"https://telesync.example/api/v1/telegram/official/channels/1001/messages/42/media"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newAutoEnv(t)
			e.basePath = tc.basePath
			e.rewire(t)

			source := e.channelWithID(t, 1001)
			e.grantChannelView(t, e.defaults, source)
			rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoWebhookPOST("Media", source))
			autoCreated(t, rec)
			var hook autoWebhookBody
			autoDecode(t, rec, &hook)

			e.deliver(t, hook.Webhook.ID, e.seedEvent(t, source, 1001, 42))
			sent := e.transport.sentBodies()
			if len(sent) != 1 {
				t.Fatalf("receiver saw %d payloads, want 1", len(sent))
			}

			var env struct {
				Data struct {
					Source struct {
						ManagedChannelID  int64  `json:"managed_channel_id"`
						ClientKey         string `json:"client_key"`
						TelegramChannelID int64  `json:"telegram_channel_id"`
					} `json:"source"`
					Message struct {
						ID    int `json:"id"`
						Media *struct {
							Type     string `json:"type"`
							Filename string `json:"filename"`
							MimeType string `json:"mime_type"`
							Size     int64  `json:"size"`
							URL      string `json:"url"`
						} `json:"media"`
					} `json:"message"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(sent[0]), &env); err != nil {
				t.Fatalf("decode the payload: %v\n%s", err, sent[0])
			}
			if env.Data.Message.Media == nil {
				t.Fatalf("the payload carries no media: %s", sent[0])
			}
			if got := env.Data.Message.Media.URL; got != tc.want {
				t.Errorf("media.url = %q, want %q", got, tc.want)
			}
			if env.Data.Source.ClientKey != "official" || env.Data.Source.TelegramChannelID != 1001 ||
				env.Data.Source.ManagedChannelID != source {
				t.Errorf("payload source = %+v", env.Data.Source)
			}
			// The link is absolute, so a receiver can open it without knowing
			// anything about this deployment's mount.
			if !strings.HasPrefix(env.Data.Message.Media.URL, "https://telesync.example/") {
				t.Errorf("media.url is not absolute: %q", env.Data.Message.Media.URL)
			}
		})
	}
}

// TestMediaURLThePayloadNamesIsServed closes the loop: the address a receiver is
// handed resolves to the media route rather than to a 404.
func TestMediaURLThePayloadNamesIsServed(t *testing.T) {
	e := newAutoEnv(t)
	e.basePath = autoBasePath
	e.rewire(t)

	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)
	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoWebhookPOST("Served", source))
	autoCreated(t, rec)
	var hook autoWebhookBody
	autoDecode(t, rec, &hook)
	e.deliver(t, hook.Webhook.ID, e.seedEvent(t, source, 1001, 42))

	var env struct {
		Data struct {
			Message struct {
				Media struct {
					URL string `json:"url"`
				} `json:"media"`
			} `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(e.transport.sentBodies()[0]), &env); err != nil {
		t.Fatal(err)
	}
	// Strip the public origin: the request is made against the handler itself.
	path := strings.TrimPrefix(env.Data.Message.Media.URL, autoPublicBaseURL)
	got := autoDo(e.handler, http.MethodGet, path, e.defaults.token, "")
	if got.Code == http.StatusNotFound {
		t.Fatalf("the media URL the payload names answers 404: %s", path)
	}
	if got.Code != http.StatusOK {
		t.Fatalf("the media URL the payload names answers %d: %s", got.Code, got.Body.String())
	}
}

// TestMediaURLConstructionAgreesWithTheMessagesAPI is the small contract behind
// the join: the path a channel message reports and the path a webhook payload
// resolves come from the same function.
func TestMediaURLConstructionAgreesWithTheMessagesAPI(t *testing.T) {
	path := automation.MediaURLPath("official", 1001, 42)
	if path != "/telegram/official/channels/1001/messages/42/media" {
		t.Fatalf("MediaURLPath = %q", path)
	}
	for _, tc := range []struct{ basePath, want string }{
		{"", "/api/v1"},
		{"/admin", "/admin/api/v1"},
		{"/admin/", "/admin/api/v1"},
	} {
		if got := automation.APIMount(tc.basePath); got != tc.want {
			t.Errorf("APIMount(%q) = %q, want %q", tc.basePath, got, tc.want)
		}
		absolute, ok := automation.AbsoluteMediaURL("https://telesync.example", automation.APIMount(tc.basePath), path)
		if !ok {
			t.Fatalf("AbsoluteMediaURL reported no address for %q", tc.basePath)
		}
		want := "https://telesync.example" + tc.want + path
		if absolute != want {
			t.Errorf("AbsoluteMediaURL(%q) = %q, want %q", tc.basePath, absolute, want)
		}
		// The webhook builder joins the same three pieces.
		built, err := webhooks.MediaURL("https://telesync.example", tc.basePath, path)
		if err != nil {
			t.Fatalf("MediaURL(%q): %v", tc.basePath, err)
		}
		if built != want {
			t.Errorf("webhooks.MediaURL(%q) = %q, want %q", tc.basePath, built, want)
		}
	}

	// A deployment that names no origin gets no address at all, rather than a
	// guess a receiver would fail to open.
	if _, ok := automation.AbsoluteMediaURL("", automation.APIMount("/admin"), path); ok {
		t.Error("AbsoluteMediaURL invented an address with no public origin")
	}
}
