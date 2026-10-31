package gotdclient

import (
	"errors"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/tgapi"
)

// ClassifyError maps Telegram RPC errors onto tgapi errors so the worker can
// answer with the right code without parsing strings. Unknown errors are
// returned unchanged.
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	if tgerr.IsCode(err, 401) {
		return errors.Join(tgapi.ErrNotAuthorized, err)
	}
	if fw, ok := tgerr.AsFloodWait(err); ok {
		return tgapi.FloodWait{Seconds: int(fw.Seconds()), Op: "this operation"}
	}

	if tgerr.Is(err, "CHANNEL_INVALID", "CHANNEL_PRIVATE", "CHANNEL_PUBLIC_GROUP_NA",
		"USERNAME_NOT_OCCUPIED", "USERNAME_INVALID", "PEER_ID_INVALID") {
		return errors.Join(tgapi.ErrChannelNotFound, err)
	}
	return err
}

// MediaType classifies an attachment without downloading it.
func MediaType(media tg.MessageMediaClass) string {
	if media == nil {
		return ""
	}
	switch m := media.(type) {
	case *tg.MessageMediaEmpty:
		return ""
	case *tg.MessageMediaPhoto:
		return "photo"
	case *tg.MessageMediaDocument:
		doc := m.Document
		if _, empty := doc.(*tg.DocumentEmpty); empty {
			return ""
		}
		d, ok := doc.(*tg.Document)
		if !ok {
			return "document"
		}
		return documentType(d)
	case *tg.MessageMediaPoll:
		return "poll"
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
		return "geo"
	case *tg.MessageMediaWebPage:
		return "webpage"
	case *tg.MessageMediaContact:
		return "other"
	case *tg.MessageMediaDice:
		return "other"
	case *tg.MessageMediaGame:
		return "other"
	case *tg.MessageMediaStory:
		return "other"
	default:
		return "other"
	}
}

func documentType(d *tg.Document) string {
	for _, attr := range d.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeAnimated:
			return "gif"
		case *tg.DocumentAttributeSticker:
			return "sticker"
		case *tg.DocumentAttributeVideo:
			if a.RoundMessage {
				return "video"
			}
			return "video"
		case *tg.DocumentAttributeAudio:
			if a.Voice {
				return "voice"
			}
			return "audio"
		}
	}
	return "document"
}

// MediaLabel renders a Persian label for a media type.
func MediaLabel(kind string) string {
	switch kind {
	case "photo":
		return "عکس"
	case "video":
		return "ویدیو"
	case "document":
		return "فایل"
	case "audio":
		return "صدا"
	case "voice":
		return "پیام صوتی"
	case "sticker":
		return "استیکر"
	case "gif":
		return "گیف"
	case "poll":
		return "نظرسنجی"
	case "geo":
		return "موقعیت مکانی"
	case "webpage":
		return "پیوند"
	case "other":
		return "پیوست"
	default:
		return ""
	}
}

// Unread is Telegram's unread state for one dialog. A nil field means the
// panel has no report at all and must show "unknown" instead of zero.
type Unread struct {
	Count    *int
	Mentions *int
}

// UnreadFromDialog reads the unread fields of dialog#fc89f7f3.
//
// unread_count and unread_mentions_count are required fields of that
// constructor, so a decoded dialog always reports them; a nil dialog (no dialog
// entry for the peer) reports nothing rather than a made-up zero.
func UnreadFromDialog(d *tg.Dialog) Unread {
	if d == nil {
		return Unread{}
	}
	count, mentions := d.UnreadCount, d.UnreadMentionsCount
	return Unread{Count: &count, Mentions: &mentions}
}

// ChannelKind reports whether a channel is a broadcast channel or a group.
func ChannelKind(c *tg.Channel) string {
	if c != nil && c.Megagroup {
		return "group"
	}
	return "channel"
}

// SenderOf returns the author of a message when Telegram identifies a user.
//
// Anonymous channel posts have no from_id; the panel then shows no sender,
// which is the truth.
func SenderOf(msg *tg.Message, users map[int64]*tg.User) *tgapi.Sender {
	if msg == nil {
		return nil
	}
	peer, ok := msg.FromID.(*tg.PeerUser)
	if !ok {
		return nil
	}
	u, ok := users[peer.UserID]
	if !ok {
		return nil
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = u.Username
	}
	return &tgapi.Sender{ID: u.ID, Name: name, Username: u.Username}
}

// ReplyToMessageID returns the id of the message this one replies to, or zero
// when the message is not a reply.
//
// Only the id is exposed: the reply's author and text are not re-fetched, so a
// GET history call stays a single RPC.
func ReplyToMessageID(msg *tg.Message) int {
	if msg == nil {
		return 0
	}
	header, ok := msg.ReplyTo.(*tg.MessageReplyHeader)
	if !ok {
		return 0
	}
	if header.ReplyToMsgID <= 0 {
		return 0
	}
	return header.ReplyToMsgID
}

// Views returns Telegram's view counter for a post, or nil when the message
// carries no views field at all.
//
// message#9814f50a (and its 64-bit id twin) mark views as a conditional field:
// absent means "this backend did not report a count", while a present zero is a
// real count of zero. The two must not collapse into each other.
func Views(msg *tg.Message) *int {
	if msg == nil {
		return nil
	}
	views, ok := msg.GetViews()
	if !ok {
		return nil
	}
	return &views
}

// Reactions converts a post's reaction tallies into the library-independent
// shape. A message without the reactions constructor, or one that reported no
// tallies, yields an empty (non-nil) slice.
//
// Chosen comes from reactionCount#a3d1cb80's chosen_order: present means the
// connected account picked this reaction, absent means it did not.
func Reactions(msg *tg.Message) []tgapi.Reaction {
	out := []tgapi.Reaction{}
	if msg == nil {
		return out
	}
	reactions, ok := msg.GetReactions()
	if !ok {
		return out
	}
	for _, rc := range reactions.Results {
		r := tgapi.Reaction{Count: rc.Count}
		switch kind := rc.Reaction.(type) {
		case *tg.ReactionEmoji:
			r.Type, r.Emoji = "emoji", kind.Emoticon
		case *tg.ReactionCustomEmoji:
			r.Type, r.DocumentID = "custom_emoji", kind.DocumentID
		default:
			r.Type = "other"
		}
		if _, ok := rc.GetChosenOrder(); ok {
			chosen := true
			r.Chosen = &chosen
		}
		out = append(out, r)
	}
	return out
}

// HistoryOffset converts the panel's before_id cursor into getHistory's
// offset_id. Zero means "the newest page".
func HistoryOffset(beforeID int) int {
	if beforeID < 0 {
		return 0
	}
	return beforeID
}

// HistoryLimit clamps the requested page size.
func HistoryLimit(limit int) int {
	if limit <= 0 {
		return 30
	}
	if limit > 100 {
		return 100
	}
	return limit
}

// HasMore reports whether a page may have predecessors.
func HasMore(got, limit int) bool { return got >= limit }

// MessageDate converts a Telegram unix timestamp into a UTC time.
func MessageDate(unix int) time.Time { return time.Unix(int64(unix), 0).UTC() }

// MediaMimeType reports the content type of an attachment, when the backend
// names one. Photos are always JPEG; documents carry their own type.
func MediaMimeType(media tg.MessageMediaClass) string {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		if _, ok := m.Photo.(*tg.Photo); ok {
			return "image/jpeg"
		}
	case *tg.MessageMediaDocument:
		if doc, ok := m.Document.(*tg.Document); ok {
			return doc.MimeType
		}
	}
	return ""
}

// MediaSize reports the byte size of an attachment, when the backend reports it.
func MediaSize(media tg.MessageMediaClass) int64 {
	switch m := media.(type) {
	case *tg.MessageMediaDocument:
		if doc, ok := m.Document.(*tg.Document); ok {
			return int64(doc.Size)
		}
	}
	return 0
}
