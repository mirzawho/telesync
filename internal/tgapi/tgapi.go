// Package tgapi defines the Telegram data a worker exposes, independent of
// gotd/td, so the worker and its tests do not depend on the library.
package tgapi

import (
	"errors"
	"fmt"
	"time"
)

// State is the connection state reported for one client.
type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StateUnauthorized State = "unauthorized"
	StateReady        State = "ready"
	StateError        State = "error"
)

// Account identifies the connected Telegram account.
type Account struct {
	ID        int64
	Username  string
	FirstName string
	LastName  string
}

// Status is the current connection state.
type Status struct {
	State  State
	ThisDC int
	User   *Account
	Err    string
	// LastSuccess is when the last authorized RPC probe succeeded (zero if
	// none has). It is evidence, not a guess: the worker reports it verbatim.
	LastSuccess time.Time
}

// Channel is a Telegram channel as reported by a dialog.
type Channel struct {
	ID             int64
	AccessHash     int64
	Title          string
	Username       string
	Kind           string // "channel" or "group"
	HasPhoto       bool
	PhotoID        int64
	UnreadCount    *int
	UnreadMentions *int
	LastMessageID  int
	// Permissions are the account's channel-level rights as Telegram reported
	// them for this dialog. A dialog exists only for a channel the account is
	// in, so membership is implied; the rights themselves come from the
	// channel's admin/banned/default fields, never from a guess.
	Permissions Permissions
}

// Media describes one attachment without downloading it.
type Media struct {
	// Type is photo, video, document, audio, voice, sticker, gif, poll, geo,
	// webpage or other.
	Type  string
	Label string
	// MimeType and Size come from the backend when it reports them.
	MimeType string
	Size     int64
}

// MediaFile is the downloaded content of one attachment.
type MediaFile struct {
	MimeType string
	Data     []byte
}

// Sender identifies a message author.
type Sender struct {
	ID       int64
	Name     string
	Username string
}

// Message is one channel post normalized for the API.
type Message struct {
	ID     int
	Date   time.Time
	Text   string
	Sender *Sender
	Media  *Media
	// ReplyToMessageID is the message this one replies to, when Telegram
	// reported a reply header. Zero means "not a reply".
	ReplyToMessageID int
	// Views is Telegram's own view counter for the post. Nil means Telegram
	// did not report one; zero is a real count of zero.
	Views *int
	// Reactions is empty when the message has none. It is never nil after
	// normalization, so it serializes as [].
	Reactions []Reaction
	// Entities are the message's formatting spans, in Telegram's UTF-16 code
	// units. Empty when the message carries none. They are the source of truth
	// for hashtags and mentions: a lexer is only the fallback for a backend
	// that did not report any.
	Entities []Entity
}

// Reaction is one reaction tally on a message.
//
// Type keeps standard and custom reactions apart: a custom emoji has no
// standard emoji to fall back to, so it carries DocumentID and an empty Emoji.
type Reaction struct {
	// Type is "emoji" for a standard emoji, "custom_emoji" for a custom
	// emoji, or "other" for a reaction kind this application does not know
	// (a paid star reaction, for instance).
	Type string
	// Emoji is the standard emoji's character; empty for other types.
	Emoji string
	// DocumentID is the custom emoji's document id; zero for other types.
	DocumentID int64
	// Count is the number of reactions of this kind.
	Count int
	// Chosen is whether the connected account reacted this way; nil when
	// Telegram did not say.
	Chosen *bool
}

// EmojiFile is the downloaded content of one custom emoji.
type EmojiFile struct {
	MimeType string
	Data     []byte
}

// HistoryPage is one page of channel history.
type HistoryPage struct {
	Messages []Message
	HasMore  bool
}

// Errors classified by the adapter so the worker can answer with the right
// error without parsing RPC strings.
var (
	// ErrNotAuthorized means the connection works but the account is signed out.
	ErrNotAuthorized = errors.New("tgapi: the Telegram account is not authorized")
	// ErrNotConnected means there is no live connection.
	ErrNotConnected = errors.New("tgapi: no live Telegram connection")
	// ErrChannelNotFound means the peer could not be resolved.
	ErrChannelNotFound = errors.New("tgapi: channel not found")
	// ErrMediaNotFound means the message has no downloadable attachment.
	ErrMediaNotFound = errors.New("tgapi: this message has no downloadable media")
	// ErrMessageNotFound means the message id does not exist in that channel.
	ErrMessageNotFound = errors.New("tgapi: message not found")
	// ErrCustomEmojiNotFound means the custom emoji document id is not in
	// Telegram's reaction set (a recycled document, or a set this account
	// cannot read).
	ErrCustomEmojiNotFound = errors.New("tgapi: the custom emoji is not available")
)

// FloodWait is returned when Telegram asks the client to wait.
type FloodWait struct {
	Seconds int
	Op      string
}

func (e FloodWait) Error() string {
	return fmt.Sprintf("tgapi: %s is rate limited by Telegram for %d seconds", e.Op, e.Seconds)
}

// FloodSeconds reports the wait Telegram asked for.
func (e FloodWait) FloodSeconds() int { return e.Seconds }
