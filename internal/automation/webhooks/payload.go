package webhooks

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/rpc"
)

// The payload is CloudEvents shaped: a small envelope of well-known context
// fields around a data object. A receiver that already understands CloudEvents
// can consume it without a bespoke parser, and the event id in the envelope is
// the same id the webhook-id header carries, so one logical event can be
// deduplicated either way.
//
// Media is metadata plus a URL and nothing else. The bytes stay behind the
// media endpoint, which applies its own authorization: a webhook receiver gets
// an address, never an attachment.

// Envelope is the document one webhook receives.
type Envelope struct {
	SpecVersion     string `json:"specversion"`
	ID              string `json:"id"`
	Source          string `json:"source"`
	Type            string `json:"type"`
	Time            string `json:"time"`
	DataContentType string `json:"datacontenttype"`
	Data            Data   `json:"data"`
}

// Data is the envelope's payload: which channel the message came from and the
// message itself.
type Data struct {
	Source  Source  `json:"source"`
	Message Message `json:"message"`
}

// Source identifies where the message came from.
//
// ManagedChannelID is the local managed_channels.id and the only value that may
// be used for a permission check. TelegramChannelID is Telegram's own peer id
// and is only meaningful within one network. They are different numbers and
// both are reported, because a receiver correlating this feed with its own
// Telegram state needs the latter while an operator reading history needs the
// former.
type Source struct {
	ManagedChannelID  int64  `json:"managed_channel_id"`
	ClientKey         string `json:"client_key"`
	TelegramChannelID int64  `json:"telegram_channel_id"`
}

// Message is one source post, in the field set the channel messages API
// already exposes. Receivers see the same facts through the webhook as they
// would through the API, so the two cannot drift.
type Message struct {
	ID               int            `json:"id"`
	Date             time.Time      `json:"date"`
	Text             string         `json:"text"`
	Sender           *rpc.Sender    `json:"sender,omitempty"`
	Media            *Media         `json:"media,omitempty"`
	ReplyToMessageID int            `json:"reply_to_message_id,omitempty"`
	Views            *int           `json:"views"`
	Reactions        []rpc.Reaction `json:"reactions"`
	Entities         []rpc.Entity   `json:"entities"`
}

// Media is one attachment's metadata. URL is the only address in the whole
// payload and it is empty for anything the media endpoint cannot fetch: a poll,
// a location, a link preview or a deleted document.
type Media struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Label    string `json:"label,omitempty"`
	Filename string `json:"filename,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	URL      string `json:"url,omitempty"`
	// Path is where the media endpoint lives on this installation, from its
	// root: "/admin/api/v1/telegram/…/media". It is always present whenever the
	// bytes are fetchable at all, so a receiver can build its own address by
	// prefixing the origin it already knows. URL is only present when the
	// deployment named one in api.public_base_url — an address is never
	// invented, and never left looking absolute when it is not.
	Path string `json:"path,omitempty"`
}

// Builder turns one stored message event into the envelope a webhook receives.
//
// It holds the deployment's own public address and mount, so a media URL is
// built from configuration this installation actually serves. When no public
// address is configured the builder refuses to build one rather than inventing
// an address a receiver would then fail to open.
type Builder struct {
	publicBaseURL string
	basePath      string
}

// NewBuilder prepares a payload builder. It does not check the public base URL:
// a deployment may run webhooks that are still disabled and only learn the
// setting later. Build is what refuses.
func NewBuilder(publicBaseURL, basePath string) *Builder {
	return &Builder{
		publicBaseURL: strings.TrimSpace(publicBaseURL),
		basePath:      strings.TrimSpace(basePath),
	}
}

// Build renders one message event as a delivery envelope.
func (b *Builder) Build(event automation.MessageEvent) (Envelope, error) {
	env := Envelope{
		SpecVersion:     "1.0",
		ID:              event.ID,
		Source:          fmt.Sprintf("telesync/telegram/channels/%d", event.ManagedChannelID),
		Type:            string(event.Type),
		Time:            event.OccurredAt.UTC().Format(time.RFC3339),
		DataContentType: "application/json",
		Data: Data{
			Source: Source{
				ManagedChannelID:  event.ManagedChannelID,
				ClientKey:         event.SourceClientKey,
				TelegramChannelID: event.TelegramChannelID,
			},
			Message: Message{
				ID:               event.Payload.Message.ID,
				Date:             event.Payload.Message.Date,
				Text:             event.Payload.Message.Text,
				Sender:           event.Payload.Message.Sender,
				ReplyToMessageID: event.Payload.Message.ReplyToMessageID,
				Views:            event.Payload.Message.Views,
				Reactions:        nonNilReactions(event.Payload.Message.Reactions),
				Entities:         nonNilEntities(event.Payload.Message.Entities),
			},
		},
	}
	if env.Type == "" {
		env.Type = string(automation.EventMessageCreated)
	}
	media, err := b.mediaOf(event)
	if err != nil {
		return Envelope{}, err
	}
	env.Data.Message.Media = media
	return env, nil
}

// mediaOf renders one attachment's metadata. The path is always there whenever
// the bytes are fetchable; the absolute URL is added only when the deployment
// named an origin to build it from.
func (b *Builder) mediaOf(event automation.MessageEvent) (*Media, error) {
	src := event.Payload.Message.Media
	if src == nil {
		return nil, nil
	}
	out := &Media{
		ID:       src.ID,
		Type:     src.Type,
		Label:    src.Label,
		Filename: src.Filename,
		MimeType: src.MimeType,
		Size:     src.Size,
	}
	// Only an attachment whose bytes the media route can fetch gets an address.
	// This is the same test the channel messages API applies, so the two agree
	// about what is downloadable.
	if !mediaHasBytes(src.Type) || strings.TrimSpace(event.Payload.MediaURLPath) == "" {
		return out, nil
	}
	out.Path = automation.APIMount(b.basePath) + event.Payload.MediaURLPath

	// With no origin named there is no honest absolute address to send, so none
	// is invented: the receiver gets the path and prefixes whatever origin it
	// already knows this installation by. Naming one that is malformed is still
	// an error — that is a configuration mistake, not an absence.
	if strings.TrimSpace(b.publicBaseURL) == "" {
		return out, nil
	}
	absolute, err := MediaURL(b.publicBaseURL, b.basePath, event.Payload.MediaURLPath)
	if err != nil {
		return nil, err
	}
	out.URL = absolute
	return out, nil
}

// MediaURL builds the absolute address a receiver fetches a message's media
// from.
//
// publicBaseURL is api.public_base_url, basePath is the gateway's mount
// (gateway.Config.BasePath, for example "/admin") and mediaURLPath is the
// relative path the channel messages API emits, which automation.MediaURLPath
// states once for both of them.
//
// There is one join in this codebase and it is automation.AbsoluteMediaURL over
// automation.APIMount: the media path lives in the /api/v1 namespace, so it must
// be resolved against the mount with that namespace and not the bare BasePath.
// Nothing here re-implements that, so a payload URL cannot drift from the one
// the channel messages API serves.
//
// Without a public origin there is nothing to join: the error names
// api.public_base_url rather than inventing an address a receiver would fail to
// open.
func MediaURL(publicBaseURL, basePath, mediaURLPath string) (string, error) {
	origin := strings.TrimSpace(publicBaseURL)
	if origin == "" {
		return "", fmt.Errorf("%w: set api.public_base_url so receivers can open the media links this payload carries",
			automation.ErrBaseURL)
	}
	u, err := url.Parse(strings.TrimRight(origin, "/"))
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("%w: api.public_base_url %q is not an absolute http(s) address",
			automation.ErrBaseURL, publicBaseURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("%w: api.public_base_url %q must use http or https",
			automation.ErrBaseURL, publicBaseURL)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: api.public_base_url must not carry credentials", automation.ErrBaseURL)
	}
	rel := strings.TrimSpace(mediaURLPath)
	if rel == "" {
		return "", fmt.Errorf("%w: the media path is empty", automation.ErrInvalid)
	}
	if !strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%w: the media path %q must be rooted at the API", automation.ErrInvalid, mediaURLPath)
	}

	absolute, ok := automation.AbsoluteMediaURL(origin, automation.APIMount(basePath), rel)
	if !ok {
		return "", fmt.Errorf("%w: there is no address to name", automation.ErrBaseURL)
	}
	return absolute, nil
}

// mediaHasBytes reports whether the media route can return this media type's
// content. It is the same test the channel messages API applies.
func mediaHasBytes(mediaType string) bool {
	switch mediaType {
	case "photo", "video", "document", "audio", "voice", "sticker", "gif":
		return true
	default:
		return false
	}
}

func nonNilReactions(in []rpc.Reaction) []rpc.Reaction {
	if in == nil {
		return []rpc.Reaction{}
	}
	return in
}

func nonNilEntities(in []rpc.Entity) []rpc.Entity {
	if in == nil {
		return []rpc.Entity{}
	}
	return in
}

// TestPayload is the fixed body a test send transmits.
//
// It is a constant, not a sample drawn from this installation's data: a test
// must prove the receiver answers, and must not be usable as a way to read a
// channel through somebody else's endpoint. It carries no client key, no
// channel id and no message content.
func TestPayload() ([]byte, error) {
	occurred := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	env := Envelope{
		SpecVersion:     "1.0",
		ID:              "evt_test",
		Source:          "telesync/telegram/channels/0",
		Type:            "telesync.webhook.test",
		Time:            occurred.Format(time.RFC3339),
		DataContentType: "application/json",
		Data: Data{
			Source: Source{},
			Message: Message{
				ID:        0,
				Date:      occurred,
				Text:      "This is a test delivery from Telesync. It carries no channel content.",
				Reactions: []rpc.Reaction{},
				Entities:  []rpc.Entity{},
			},
		},
	}
	return json.Marshal(env)
}
