package webhooks_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/rpc"
)

func messageEvent(media *rpc.Media) automation.MessageEvent {
	views := 42
	return automation.MessageEvent{
		ID:                "evt_0123456789abcdef",
		Type:              automation.EventMessageCreated,
		NetworkKey:        "official",
		SourceClientKey:   "client-a",
		AccountID:         testAccountID,
		SourceAccountID:   testAccountID,
		ManagedChannelID:  7,
		TelegramChannelID: -1001234,
		MessageID:         91,
		OccurredAt:        time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		ReceivedAt:        time.Date(2026, 11, 5, 12, 0, 1, 0, time.UTC),
		Payload: automation.MessagePayload{
			Message: rpc.Message{
				ID:               91,
				Date:             time.Date(2026, 11, 5, 11, 59, 0, 0, time.UTC),
				Text:             "a new post",
				Sender:           &rpc.Sender{ID: 5, Name: "Editor", Username: "editor"},
				Media:            media,
				ReplyToMessageID: 90,
				Views:            &views,
				Reactions:        []rpc.Reaction{{Type: "emoji", Emoji: "👍", Count: 3}},
				Entities:         []rpc.Entity{{Type: "hashtag", Offset: 0, Length: 4}},
			},
			MediaURLPath: "/telegram/client-a/channels/-1001234/messages/91/media",
		},
	}.WithOwner(testOwnerUserID)
}

// The JSON keys are the contract with every receiver that already parses this
// feed, so they are pinned here and not merely read off the struct tags.
func TestSourceCarriesTheChannelsOwnNamingUnderStableKeys(t *testing.T) {
	env, err := webhooks.NewBuilder("https://telesync.example.com", "/admin").Build(
		messageEvent(nil),
		webhooks.Channel{Type: "group", Title: "گروه", Username: "group"},
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Data struct {
			Source map[string]any `json:"source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"managed_channel_id":  float64(7),
		"client_key":          "client-a",
		"telegram_channel_id": float64(-1001234),
		"type":                "group",
		"title":               "گروه",
		"username":            "group",
	} {
		if got := doc.Data.Source[key]; got != want {
			t.Fatalf("source.%s = %v (%T), want %v", key, got, got, want)
		}
	}
}

func TestAnUnknownChannelLeavesTheNamingEmptyAndKeepsTheIdentifiers(t *testing.T) {
	env, err := webhooks.NewBuilder("https://telesync.example.com", "/admin").Build(messageEvent(nil), webhooks.Channel{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if env.Data.Source.Type != "" || env.Data.Source.Title != "" {
		t.Fatalf("labels appeared from nowhere: %+v", env.Data.Source)
	}
	if env.Data.Source.ManagedChannelID != 7 || env.Data.Source.TelegramChannelID != -1001234 {
		t.Fatalf("the identifiers are what a receiver matches on: %+v", env.Data.Source)
	}
}

func TestEnvelopeIsCloudEventsShapedAndNamesItsSource(t *testing.T) {
	env, err := webhooks.NewBuilder("https://telesync.example.com", "/admin").Build(messageEvent(nil), webhooks.Channel{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if env.SpecVersion != "1.0" {
		t.Fatalf("specversion = %q", env.SpecVersion)
	}
	if env.ID != "evt_0123456789abcdef" {
		t.Fatalf("id = %q: it must be the stable event id", env.ID)
	}
	if env.Source != "telesync/telegram/channels/7" {
		t.Fatalf("source = %q", env.Source)
	}
	if env.Type != string(automation.EventMessageCreated) {
		t.Fatalf("type = %q", env.Type)
	}
	if env.DataContentType != "application/json" {
		t.Fatalf("datacontenttype = %q", env.DataContentType)
	}
	if env.Time != "2026-11-05T12:00:00Z" {
		t.Fatalf("time = %q", env.Time)
	}
	if env.Data.Source.ManagedChannelID != 7 || env.Data.Source.ClientKey != "client-a" ||
		env.Data.Source.TelegramChannelID != -1001234 {
		t.Fatalf("data.source = %+v", env.Data.Source)
	}
}

func TestEnvelopeCarriesTheMessageFieldsTheChannelMessagesAPIExposes(t *testing.T) {
	env, err := webhooks.NewBuilder("https://telesync.example.com", "/admin").Build(messageEvent(nil), webhooks.Channel{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	data := doc["data"].(map[string]any)
	msg := data["message"].(map[string]any)
	for _, key := range []string{"id", "date", "text", "sender", "reply_to_message_id", "views", "reactions", "entities"} {
		if _, ok := msg[key]; !ok {
			t.Fatalf("data.message is missing %q: %v", key, msg)
		}
	}
	if msg["id"].(float64) != 91 || msg["text"].(string) != "a new post" {
		t.Fatalf("message = %v", msg)
	}
	if msg["views"].(float64) != 42 {
		t.Fatalf("views = %v", msg["views"])
	}
	if reactions := msg["reactions"].([]any); len(reactions) != 1 {
		t.Fatalf("reactions = %v", reactions)
	}
}

func TestMediaIsMetadataPlusAURLAndNeverBytes(t *testing.T) {
	media := &rpc.Media{
		ID: "91:0", Type: "photo", Label: "Photo", Filename: "shot.jpg",
		MimeType: "image/jpeg", Size: 12345,
	}
	env, err := webhooks.NewBuilder("https://telesync.example.com", "/admin").Build(messageEvent(media), webhooks.Channel{})
	if err != nil {
		t.Fatal(err)
	}
	if env.Data.Message.Media == nil {
		t.Fatal("media was dropped")
	}
	got := env.Data.Message.Media
	want := "https://telesync.example.com/admin/api/v1/telegram/client-a/channels/-1001234/messages/91/media"
	if got.URL != want {
		t.Fatalf("media url = %q, want %q", got.URL, want)
	}
	if got.Type != "photo" || got.Size != 12345 || got.Filename != "shot.jpg" {
		t.Fatalf("media = %+v", got)
	}

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	// Nothing binary rides along: no bytes field, no base64 blob, no multipart.
	for _, forbidden := range []string{"base64", "data\":\"", "content_bytes", "multipart"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the payload looks like it carries bytes (%q): %s", forbidden, body)
		}
	}
	if len(raw) > 8<<10 {
		t.Fatalf("the payload is %d bytes: it is carrying more than metadata", len(raw))
	}
}

func TestMediaWithoutFetchableBytesHasNoURL(t *testing.T) {
	// A poll, a location and a link preview have nothing to download. Saying so
	// with a URL that 404s would be worse than saying nothing.
	for _, kind := range []string{"poll", "geo", "webpage", "other"} {
		media := &rpc.Media{ID: "91:0", Type: kind, Label: kind}
		env, err := webhooks.NewBuilder("https://telesync.example.com", "/admin").Build(messageEvent(media), webhooks.Channel{})
		if err != nil {
			t.Fatal(err)
		}
		if env.Data.Message.Media == nil {
			t.Fatalf("%s media was dropped", kind)
		}
		if env.Data.Message.Media.URL != "" {
			t.Fatalf("%s media grew a url: %q", kind, env.Data.Message.Media.URL)
		}
	}
}

func TestMediaURLJoinsThePublicOriginTheMountAndTheRelativePath(t *testing.T) {
	got, err := webhooks.MediaURL("https://telesync.example.com", "/admin",
		"/telegram/client-a/channels/-1001234/messages/91/media")
	if err != nil {
		t.Fatalf("MediaURL: %v", err)
	}
	want := "https://telesync.example.com/admin/api/v1/telegram/client-a/channels/-1001234/messages/91/media"
	if got != want {
		t.Fatalf("MediaURL = %q, want %q", got, want)
	}

	// The /api/v1 namespace comes from automation.APIMount, so a bare gateway
	// mount resolves the way the channel messages API serves the path.
	got, err = webhooks.MediaURL("https://telesync.example.com", "/",
		"/telegram/client-a/channels/-1001234/messages/91/media")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://telesync.example.com/api/v1/telegram/client-a/channels/-1001234/messages/91/media" {
		t.Fatalf("MediaURL at the root mount = %q", got)
	}

	// A media path that is not rooted at the API is refused rather than joined
	// into an address that would 404.
	if _, err := webhooks.MediaURL("https://telesync.example.com", "/admin",
		"telegram/client-a/messages/91/media"); !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("err = %v, want automation.ErrInvalid", err)
	}
}

func TestMediaURLRefusesToInventAnAddress(t *testing.T) {
	if _, err := webhooks.MediaURL("", "/admin", "/telegram/x/media"); !errors.Is(err, automation.ErrBaseURL) {
		t.Fatalf("err = %v, want ErrNoPublicBaseURL", err)
	}
	if _, err := webhooks.MediaURL("   ", "/admin", "/telegram/x/media"); !errors.Is(err, automation.ErrBaseURL) {
		t.Fatalf("err = %v, want ErrNoPublicBaseURL", err)
	}
	// The error must name the setting, so an operator knows what to write.
	_, err := webhooks.MediaURL("", "/admin", "/telegram/x/media")
	if !strings.Contains(err.Error(), "api.public_base_url") {
		t.Fatalf("err = %q, want it to name api.public_base_url", err)
	}
	if _, err := webhooks.MediaURL("not a url", "/admin", "/x"); err == nil {
		t.Fatal("a malformed origin was accepted")
	}
}

func TestTheTestPayloadIsFixedAndCarriesNoRealContent(t *testing.T) {
	first, err := webhooks.TestPayload()
	if err != nil {
		t.Fatal(err)
	}
	second, err := webhooks.TestPayload()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("the test payload is not fixed")
	}
	var env webhooks.Envelope
	if err := json.Unmarshal(first, &env); err != nil {
		t.Fatalf("the test payload is not an envelope: %v", err)
	}
	if env.Data.Source.ClientKey != "" || env.Data.Source.ManagedChannelID != 0 {
		t.Fatalf("the test payload names a real channel: %+v", env.Data.Source)
	}
	if strings.Contains(string(first), "client-") {
		t.Fatalf("the test payload names a client: %s", first)
	}
}
