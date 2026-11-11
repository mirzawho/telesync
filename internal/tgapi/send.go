package tgapi

import "errors"

// SendRequest is one prepared outgoing post.
//
// It is built from a source message the user edited; sending it never modifies
// that source. The text and its spans are what will appear on the destination,
// exactly as the caller composed them.
type SendRequest struct {
	// Text is the outgoing body, or the caption of an attachment.
	Text string
	// Entities are the formatting spans of Text, in UTF-16 code units.
	Entities []Entity
	// Media is the attachment to send with the message. Nil sends text only.
	Media *SendMedia
	// Silent suppresses the notification for the destination's subscribers.
	Silent bool
	// NoWebpage suppresses the link preview Telegram would otherwise build.
	NoWebpage bool
	// RandomID is the client message id Telegram uses to recognise a resend.
	// A publishing operation derives one from its idempotency key so a retry
	// cannot post twice. Zero means "pick one now", which deduplicates only
	// within this call.
	RandomID int64
}

// SendMedia is the attachment of an outgoing post.
type SendMedia struct {
	// Kind is "photo", "video", "document", "audio" or "voice". A voice
	// recording is its own kind and never a document: what the destination
	// draws — a play button or a download card — is decided by it alone.
	Kind string
	// Name is the file name presented for a document. A voice recording
	// carries no name the sender chose, and none is invented for it.
	Name string
	// MimeType is the attachment's content type. It is the content type of the
	// bytes as they are sent: nothing here re-encodes.
	MimeType string
	// Data is the file's bytes.
	Data []byte
	// Duration is the recording length in seconds, for "audio" and "voice".
	// Zero means unknown and is sent as no length rather than as a guess.
	Duration int
	// Waveform is the voice message's own bitpacked amplitude samples, for
	// "voice" only. Nil sends none; a waveform is never made up.
	Waveform []byte
}

// Media kinds an outgoing attachment can be sent as.
const (
	SendMediaPhoto    = "photo"
	SendMediaVideo    = "video"
	SendMediaDocument = "document"
	SendMediaAudio    = "audio"
	SendMediaVoice    = "voice"
)

// SendResult is what the destination confirmed about the sent message.
type SendResult struct {
	// Message is the message Telegram returned for the send. It is nil when
	// the backend accepted the message but returned nothing that describes it.
	Message *Message
	// ChannelID is the destination channel's Telegram id.
	ChannelID int64
	// UnsendableEntities counts formatting spans that could not be rebuilt
	// for the outgoing message. The text is sent regardless; the loss is
	// reported rather than hidden.
	UnsendableEntities int
}

// EditRequest completely replaces one existing message. A nil Media means the
// requested final state has no attachment; it never means retain the old one.
type EditRequest struct {
	MessageID int
	Text      string
	Entities  []Entity
	Media     *SendMedia
	NoWebpage bool
}

// EditResult is the target message read back from Telegram after the edit.
type EditResult struct {
	Message *Message
}

// Send errors classified by the adapter so the worker can answer with the right
// error without parsing RPC strings.
var (
	// ErrSendForbidden means the destination refused the message: the account
	// may not post there, or the channel is no longer reachable to it.
	ErrSendForbidden = errors.New("tgapi: the destination refused the message")
	// ErrSendUnsupported means this content cannot be sent to that destination.
	ErrSendUnsupported = errors.New("tgapi: this content cannot be sent to the destination")
	// ErrMessageTooLong means the text is above the destination's limit.
	ErrMessageTooLong              = errors.New("tgapi: the message text is too long")
	ErrMessageEditForbidden        = errors.New("tgapi: Telegram refused to edit this message")
	ErrMessageEditTimeExpired      = errors.New("tgapi: Telegram's edit window has passed")
	ErrMessageEditUnsupported      = errors.New("tgapi: Telegram does not support this edit")
	ErrEditMediaRemovalUnsupported = errors.New("tgapi: Telegram did not remove the message media")
	ErrMessageEditOutcomeUnknown   = errors.New("tgapi: the result of the message edit is unknown")
)

// MaxMessageRunes is the longest message text Telegram accepts.
const MaxMessageRunes = 4096

// MaxCaptionRunes is the longest caption Telegram accepts beside an
// attachment. It is shorter than a message body and is a limit of its own: a
// caption the caller wrote in good faith must be refused here with the reason,
// not truncated into something they did not write and not sent to be rejected
// later by the destination.
const MaxCaptionRunes = 1024
