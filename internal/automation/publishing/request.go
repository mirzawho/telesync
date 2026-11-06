package publishing

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mirza/telesync/internal/rpc"
)

// Media modes a publishing request may name.
const (
	// MediaPreserveSource sends the source attachment along with the post.
	MediaPreserveSource = "preserve_source"
	// MediaOmit sends the text alone.
	MediaOmit = "omit"
)

// RequestSource names the message a post was composed from.
//
// The source is read to build the post and is never written to: this request
// creates a new message somewhere else. Its attachment is read through the
// connection that recorded it, never through the destination's.
type RequestSource struct {
	// LocalChannelID is the managed_channels row, never a Telegram id.
	LocalChannelID int64 `json:"local_channel_id"`
	MessageID      int   `json:"message_id"`
}

// RequestDestination names where the post goes. ChannelID is the Telegram
// channel id on the connection named beside the request; it must come from that
// connection's own channel data.
type RequestDestination struct {
	ChannelID string `json:"channel_id"`
}

// RequestContent is the outgoing body exactly as it is to be sent.
type RequestContent struct {
	Text string `json:"text"`
	// Entities are its formatting spans, in UTF-16 code units.
	Entities []rpc.Entity `json:"entities"`
}

// RequestMedia names the attachment to send with the post.
type RequestMedia struct {
	// Mode is "preserve_source" to send the source attachment along, or "omit"
	// for text only.
	Mode string `json:"mode"`
	// AttachmentID is the media id of the attachment to send, exactly as the
	// message listing reported it. It is compared against the source's own id
	// and never read as a coordinate. Empty names no attachment, and the
	// message's own is sent.
	AttachmentID string `json:"attachment_id,omitempty"`
}

// RequestOptions are the per-request publishing switches.
type RequestOptions struct {
	Silent                bool `json:"silent"`
	DisableWebPagePreview bool `json:"disable_web_page_preview"`
}

// Request is the prepared outgoing post a client submits to the Telegram
// publishing route, and the same request the workflow engine adapts a
// background publication into. Keeping one shape is what stops background and
// interactive publishing from drifting apart.
type Request struct {
	Source      RequestSource      `json:"source"`
	Destination RequestDestination `json:"destination"`
	Content     RequestContent     `json:"content"`
	Media       RequestMedia       `json:"media"`
	Options     RequestOptions     `json:"options"`
	// IdempotencyKey makes a retry safe. It is transport, not content: it is
	// never part of the fingerprint a stored attempt is compared against, so a
	// caller that renames its key still gets the same request recognised.
	IdempotencyKey string `json:"-"`
}

// WPPost is the prepared WordPress post body.
type WPPost struct {
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	Content string `json:"content"`
	Status  string `json:"status"`
	// Tags are tag names, resolved against the site by the route that accepts
	// them from a client. A workflow never sends names: a workflow publishes
	// only term ids the destination site already has, and never creates one.
	Tags []string `json:"tags"`
	// TagIDs and CategoryIDs are term ids on the destination site. They are
	// used as they are: a term id is never invented, and a name is never sent
	// where an id belongs.
	TagIDs      []int `json:"tag_ids"`
	CategoryIDs []int `json:"categories"`
}

// WPOptions are the per-request WordPress switches.
type WPOptions struct {
	CommentStatus string `json:"comment_status"`
}

// WordPressPost is a prepared post for a registered WordPress site.
type WordPressPost struct {
	Source  RequestSource `json:"source"`
	Post    WPPost        `json:"post"`
	Media   RequestMedia  `json:"media"`
	Options WPOptions     `json:"options"`
	// IdempotencyKey makes a retry safe. It is transport, never content.
	IdempotencyKey string `json:"-"`
}

// Validate checks the shape of a Telegram publishing request before anything is
// resolved or sent.
func Validate(req Request) error {
	if req.Source.LocalChannelID <= 0 {
		return errors.New("source.local_channel_id must be a positive local channel id")
	}
	if req.Source.MessageID <= 0 {
		return errors.New("source.message_id must be a positive message id")
	}
	if req.Media.Mode != MediaPreserveSource && req.Media.Mode != MediaOmit {
		return fmt.Errorf("media.mode must be %q or %q", MediaPreserveSource, MediaOmit)
	}
	if len(req.Content.Text) == 0 && req.Media.Mode == MediaOmit {
		return errors.New("content.text must not be empty when no media is sent")
	}
	for _, e := range req.Content.Entities {
		if !entityValid(req.Content.Text, e) {
			return errors.New("an entity span falls outside content.text")
		}
	}
	return nil
}

// ValidateWordPress checks the shape of a prepared WordPress post.
//
// It checks what a post is, not where it came from: a source that does not
// resolve is answered by the source lookup with the reason it is missing, which
// is a different failure with a different remedy.
func ValidateWordPress(req WordPressPost) error {
	if strings.TrimSpace(req.Post.Title) == "" {
		return errors.New("post.title must not be empty.")
	}
	if req.Media.Mode != MediaPreserveSource && req.Media.Mode != MediaOmit {
		return fmt.Errorf("media.mode must be %q or %q.", MediaPreserveSource, MediaOmit)
	}
	return nil
}

// entityValid reports whether a span lies inside the text it describes,
// measured in Telegram's UTF-16 code units.
func entityValid(text string, e rpc.Entity) bool {
	if e.Offset < 0 || e.Length < 0 {
		return false
	}
	end := e.Offset + e.Length
	return end >= e.Offset && end <= utf16Len(text)
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
			continue
		}
		n++
	}
	return n
}

// validUTF8Cut keeps a name to something a document can carry.
func validUTF8Cut(name string, max int) string {
	if len(name) > max {
		// Cut on a rune boundary: a name is UTF-8, and half a rune is not a
		// name a receiver can restore.
		name = name[:max]
		for len(name) > 0 && !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name
}
