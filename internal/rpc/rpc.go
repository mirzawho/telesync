// Package rpc defines the private contract between a Telegram worker and the
// HTTP gateway.
//
// The transport is HTTP/1.1 with JSON over the worker's own Unix domain socket.
// Nothing in this package is exposed on a network interface: the gateway is the
// only client, and it reaches the socket through the filesystem.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Worker lifecycle states reported by the worker itself.
type WorkerState string

const (
	StateStarting     WorkerState = "starting"
	StateConnected    WorkerState = "connected"
	StateDisconnected WorkerState = "disconnected"
	StateNeedsAuth    WorkerState = "needs_auth"
	StateError        WorkerState = "error"
	StateStopping     WorkerState = "stopping"
)

// Authorization states reported for the Telegram account.
const (
	AuthAuthorized = "authorized"
	AuthNeedsAuth  = "needs_auth"
	AuthUnknown    = "unknown"
)

// Error codes carried on the wire.
const (
	CodeInvalidInput     = "invalid_input"
	CodeNeedsAuth        = "needs_auth"
	CodeNotConnected     = "not_connected"
	CodeChannelNotFound  = "channel_not_found"
	CodeRateLimited      = "rate_limited"
	CodeInternal         = "internal"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeNotFound         = "not_found"
	// CodeInviteInvalid means the invite link is malformed, revoked or expired.
	CodeInviteInvalid = "invite_invalid"
	// CodeMembershipLimit means the account cannot join more channels.
	CodeMembershipLimit = "membership_limit"
	// CodePeerUnresolved means a numeric channel id could not be resolved
	// because the worker has no peer information (an access hash) for it.
	CodePeerUnresolved = "peer_unresolved"
	// CodeNoAvatar means the channel exists but has no profile photo.
	CodeNoAvatar = "no_avatar"
	// CodeSendForbidden means Telegram refused to accept the message: the
	// account may not post in this channel, or the channel is no longer
	// reachable to it.
	CodeSendForbidden = "send_forbidden"
	// CodeSendUnsupported means the destination cannot take this content — a
	// media type it does not accept, or a method this backend does not
	// implement.
	CodeSendUnsupported = "send_unsupported"
	// CodeMediaTooLarge means the attachment is above the transfer limit.
	CodeMediaTooLarge = "media_too_large"
	// CodeMessageTooLong means the text is above the destination's limit.
	CodeMessageTooLong = "message_too_long"
)

// Sentinel errors the worker maps onto wire codes.
var (
	ErrNeedsAuth       = errors.New("rpc: the Telegram account is not authorized")
	ErrNotConnected    = errors.New("rpc: no live Telegram connection")
	ErrChannelNotFound = errors.New("rpc: channel not found")
	ErrUnavailable     = errors.New("rpc: worker is unavailable")
	ErrTimeout         = errors.New("rpc: worker did not answer in time")
	// ErrInviteInvalid means the invite link is malformed, revoked or expired.
	ErrInviteInvalid = errors.New("rpc: the invite link is not usable")
	// ErrMembershipLimit means the account cannot join more channels.
	ErrMembershipLimit = errors.New("rpc: the account cannot join more channels")
	// ErrPeerUnresolved means a numeric channel id has no peer information.
	ErrPeerUnresolved = errors.New("rpc: this numeric channel id has no peer information")
	// ErrNoAvatar means the channel has no profile photo.
	ErrNoAvatar = errors.New("rpc: the channel has no avatar")
	// ErrSendForbidden means the destination refused the message.
	ErrSendForbidden = errors.New("rpc: the destination refused the message")
	// ErrSendUnsupported means this content cannot be sent there.
	ErrSendUnsupported = errors.New("rpc: this content cannot be sent to the destination")
	// ErrMediaTooLarge means the attachment is above the transfer limit.
	ErrMediaTooLarge = errors.New("rpc: the attachment is too large to send")
	// ErrMessageTooLong means the text is above the destination's limit.
	ErrMessageTooLong = errors.New("rpc: the message text is too long")
)

// Error is a coded error on the wire.
type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

// Error implements error.
func (e *Error) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s: %s (retry after %ds)", e.Code, e.Message, e.RetryAfter)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewError builds a coded error.
func NewError(code, message string, retryAfter int) *Error {
	return &Error{Code: code, Message: message, RetryAfter: retryAfter}
}

// ErrorBody is the JSON shape of a failed response.
type ErrorBody struct {
	Error *Error `json:"error"`
}

// Health is the worker's self-report.
type Health struct {
	Key            string      `json:"key"`
	Type           string      `json:"type"`
	State          WorkerState `json:"state"`
	Authorized     bool        `json:"authorized"`
	Transport      string      `json:"transport"`
	RPCReady       bool        `json:"rpc_ready"`
	PID            int         `json:"pid"`
	StartedAt      time.Time   `json:"started_at"`
	UptimeSeconds  int64       `json:"uptime_seconds"`
	ThisDC         int         `json:"this_dc,omitempty"`
	LastSuccessAt  *time.Time  `json:"last_success_at,omitempty"`
	LastError      string      `json:"last_error,omitempty"`
	SessionPresent bool        `json:"session_present"`
}

// Account is the authorized Telegram identity.
type Account struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Profile is the authorized account and its client identity.
type Profile struct {
	Key                string   `json:"key"`
	Type               string   `json:"type"`
	Authorized         bool     `json:"authorized"`
	AuthorizationState string   `json:"authorization_state"`
	User               *Account `json:"user,omitempty"`
	ThisDC             int      `json:"this_dc,omitempty"`
	Endpoint           string   `json:"endpoint,omitempty"`
}

// Channel is one dialog the account can read.
type Channel struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Username      string `json:"username,omitempty"`
	Kind          string `json:"kind"`
	HasAvatar     bool   `json:"has_avatar"`
	UnreadCount   *int   `json:"unread_count,omitempty"`
	LastMessageID int    `json:"last_message_id,omitempty"`
	// Permissions are the account's channel-level rights, as Telegram reported
	// them for the dialog. They come from the channel's own admin/banned
	// fields, not from the fact that a dialog exists.
	Permissions Permissions `json:"permissions"`
}

// PageInfo describes a page of a larger collection.
type PageInfo struct {
	Page     int  `json:"page"`
	PerPage  int  `json:"per_page"`
	Total    int  `json:"total"`
	HasMore  bool `json:"has_more"`
	NextPage int  `json:"next_page,omitempty"`
}

// ChannelPage is a page of channels.
type ChannelPage struct {
	Channels []Channel `json:"channels"`
	Page     PageInfo  `json:"page"`
}

// Sender identifies a message author.
type Sender struct {
	ID       int64  `json:"id"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
}

// Media describes one attachment. The bytes are fetched separately through
// MediaFile, so listing a page never downloads images.
type Media struct {
	// ID identifies the attachment within its message, e.g. "500:9" for the
	// photo of message 500 at index 9. It is opaque to callers: pass it back
	// (or the id in the media URL) to fetch the bytes.
	ID string `json:"id"`
	// Type is one of photo, video, document, audio, voice, sticker, gif, poll,
	// geo, webpage, other.
	Type string `json:"type"`
	// Label is a human-readable description.
	Label string `json:"label,omitempty"`
	// Filename is the name the sender gave the file, exactly as Telegram
	// reported it. It is empty when Telegram reported none, and is omitted from
	// the wire rather than replaced by a name this build made up. It is a
	// different fact from Label, which describes the media kind and is not a
	// name at all.
	Filename string `json:"filename,omitempty"`
	// MimeType is the content type the backend reported, when known.
	MimeType string `json:"mime_type,omitempty"`
	// Size is the byte size the backend reported, when known.
	Size int64 `json:"size,omitempty"`
}

// MediaFile is the downloaded content of one attachment.
type MediaFile struct {
	MediaID  string `json:"media_id"`
	MimeType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// MediaSlice names the part of one attachment to fetch.
//
// It mirrors the two shapes an HTTP Range header takes: a run of bytes counted
// from the start of the attachment, and a suffix counted from its end. Exactly
// one shape is used at a time — Tail greater than zero selects the suffix and
// makes the other two fields meaningless.
type MediaSlice struct {
	// Offset is the first byte wanted, counted from the start of the attachment.
	Offset int64 `json:"offset,omitempty"`
	// Limit is the most bytes wanted after Offset; zero means "to the end".
	Limit int64 `json:"limit,omitempty"`
	// Tail is how many of the attachment's last bytes are wanted; zero means
	// the slice is counted from Offset instead.
	Tail int64 `json:"tail,omitempty"`
}

// MediaStream is one attachment's content, streamed from the worker.
//
// The bytes travel as the response body rather than inside a JSON document: a
// 60 MB attachment must reach the caller without ever being held in one buffer.
// The facts the URL cannot carry travel as response headers.
type MediaStream struct {
	// MimeType is the content type the backend reported.
	MimeType string
	// Filename is the name the sender gave the file, empty when Telegram
	// reported none. It is never invented.
	Filename string
	// Size is the byte size of the whole attachment, zero when unknown. It is
	// not the size of the slice Body yields.
	Size int64
	// Offset is the attachment byte offset Body starts at.
	Offset int64
	// Length is the number of bytes Body will yield, zero when the total is
	// unknown and the body simply runs to the end of the attachment.
	Length int64
	// Body is the slice itself. The caller closes it, including when it stops
	// reading early.
	Body io.ReadCloser
}

// ReadAll drains the stream into one buffer.
//
// Only callers that must hand the attachment on as a single buffer use it —
// preparing a post, uploading to a site. Answering an HTTP reader never does:
// that path streams. The buffer is bounded by MaxSendMediaBytes, the same limit
// a send already carries.
func (s MediaStream) ReadAll() ([]byte, error) {
	defer s.Body.Close()
	data, err := io.ReadAll(io.LimitReader(s.Body, MaxSendMediaBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSendMediaBytes {
		return nil, ErrMediaTooLarge
	}
	return data, nil
}

// Response headers carrying a MediaStream's metadata alongside its bytes.
const (
	// HeaderMediaFilename is the sender's filename, percent-encoded so it stays
	// printable ASCII in a header. Absent when Telegram reported no name.
	HeaderMediaFilename = "X-Telesync-Filename"
	// HeaderMediaSize is the whole attachment's size in bytes, or 0.
	HeaderMediaSize = "X-Telesync-Size"
	// HeaderMediaOffset is the attachment byte offset the body starts at.
	HeaderMediaOffset = "X-Telesync-Offset"
)

// Message is one normalized channel post.
type Message struct {
	ID               int       `json:"id"`
	Date             time.Time `json:"date"`
	Text             string    `json:"text"`
	Sender           *Sender   `json:"sender,omitempty"`
	Media            *Media    `json:"media,omitempty"`
	ReplyToMessageID int       `json:"reply_to_message_id,omitempty"`
	// Views is Telegram's own view counter. It is always present: null when
	// Telegram did not report one, the real count (including 0) otherwise.
	Views *int `json:"views"`
	// Reactions is always present, an empty array when there are none. The
	// worker fills it, and the client fills an empty list for anything that
	// decoded as null, so no reader of this type sees nil.
	//
	// There is deliberately no MarshalJSON on Message: gateway.messageView
	// embeds it, and encoding/json would promote such a method to the outer
	// struct, replacing the whole message with the bare fields of Message and
	// dropping the media URL.
	Reactions []Reaction `json:"reactions"`
	// Entities are the message's formatting spans, measured in UTF-16 code
	// units exactly as Telegram reports them. Always present, empty when the
	// message carries none. They are how a reader finds hashtags and mentions
	// without guessing at the text.
	Entities []Entity `json:"entities"`
}

// Entity is one formatting span of a message text.
//
// Offset and Length are UTF-16 code units, not Go rune or byte indexes: "🙂"
// is one rune and two units. Use tgapi.UTF16Len and tgapi.UTF16Slice to move
// between the two.
type Entity struct {
	// Type is one of mention, hashtag, bot_command, url, email, bold, italic,
	// code, pre, text_url, mention_name, cashtag, phone, underline, strike,
	// bank_card, spoiler, blockquote, custom_emoji, or "unknown".
	Type string `json:"type"`
	// Offset is the span's start in UTF-16 code units.
	Offset int `json:"offset"`
	// Length is the span's length in UTF-16 code units.
	Length int `json:"length"`
	// URL is the target of a text_url entity; omitted otherwise.
	URL string `json:"url,omitempty"`
	// Language is the fenced language of a pre entity; omitted otherwise.
	Language string `json:"language,omitempty"`
	// UserID is the referenced user of a mention_name entity; omitted otherwise.
	UserID int64 `json:"user_id,omitempty"`
	// DocumentID is the custom emoji's document id; omitted otherwise.
	DocumentID int64 `json:"document_id,omitempty"`
}

// Reaction is one reaction tally on a message.
type Reaction struct {
	// Type is "emoji", "custom_emoji", or "other" for a kind this
	// application does not model (a paid star reaction, say).
	Type string `json:"type"`
	// Emoji is the standard emoji; omitted for custom and other kinds.
	Emoji string `json:"emoji,omitempty"`
	// DocumentID is a custom emoji's document id; omitted for other kinds.
	// It is what keeps a custom emoji from being mistaken for a standard one.
	DocumentID int64 `json:"document_id,omitempty"`
	// Count is the number of reactions of this kind.
	Count int `json:"count"`
	// Chosen is whether the connected account reacted this way; null when
	// Telegram did not say.
	Chosen *bool `json:"chosen"`
}

// MessagePage is a page of channel history.
type MessagePage struct {
	Channel  Channel   `json:"channel"`
	Messages []Message `json:"messages"`
	Page     PageInfo  `json:"page"`
}

// SendRequest is one prepared outgoing post for a channel on this worker's
// connection.
//
// The text is what the user prepared, never a copy of a source message: the
// source is read to build it and is never modified by sending.
type SendRequest struct {
	// ChannelID is the Telegram channel id of the destination.
	ChannelID int64 `json:"channel_id"`
	// Text is the outgoing body, or the caption of an attachment.
	Text string `json:"text"`
	// Entities are the formatting spans of Text, in UTF-16 code units.
	Entities []Entity `json:"entities"`
	// Media is the attachment to send with the message. Nil sends text only.
	Media *SendMedia `json:"media,omitempty"`
	// Silent suppresses the notification for the destination's subscribers.
	Silent bool `json:"silent,omitempty"`
	// NoWebpage suppresses the link preview Telegram would otherwise build.
	NoWebpage bool `json:"no_web_page,omitempty"`
	// RandomID is the client message id Telegram uses to recognise a resend.
	// A publishing operation derives one from its idempotency key so a retry
	// cannot post twice.
	RandomID int64 `json:"random_id,omitempty"`
}

// SendMedia is the attachment of an outgoing post.
//
// The bytes are not part of the JSON body: they travel beside it in the
// request, so a large attachment never rides a text document.
type SendMedia struct {
	// Kind is "photo" or "document".
	Kind string `json:"kind"`
	// Name is the file name Telegram shows for a document.
	Name string `json:"name,omitempty"`
	// MimeType is the attachment's content type.
	MimeType string `json:"mime_type"`
	// Data is the file's bytes. It is filled from the transfer beside the
	// request and never serialized into it.
	Data []byte `json:"-"`
}

// SendResult is what the destination confirmed about the sent message.
type SendResult struct {
	// Message is the message Telegram returned for the send. It is nil when
	// the backend accepted the message but returned nothing that describes it:
	// the send is then reported as confirmed without a message to show.
	Message *Message `json:"message,omitempty"`
	// ChannelID is the destination channel id, as a decimal string.
	ChannelID string `json:"channel_id"`
	// UnsendableEntities counts formatting spans that could not be rebuilt
	// for the outgoing message. The text is sent regardless; the loss is
	// reported rather than hidden.
	UnsendableEntities int `json:"unsendable_entities,omitempty"`
}

// MaxSendMediaBytes is the largest attachment one send may carry. It matches
// the download limit, so content that can be read can be sent on.
const MaxSendMediaBytes = 32 << 20

// Backend is the worker-side implementation behind the RPC server.
type Backend interface {
	Health(ctx context.Context) Health
	Profile(ctx context.Context) (Profile, error)
	Channels(ctx context.Context, page, perPage int) (ChannelPage, error)
	Messages(ctx context.Context, channelID int64, page, perPage int) (MessagePage, error)
	// Message returns exactly one post, or ErrNotFound.
	Message(ctx context.Context, channelID int64, messageID int) (MessagePage, error)
	// Media streams one attachment of message messageID in channelID, or the
	// slice of it the request names. photoIndex selects the thumbnail size for
	// photos (0 = the largest). The returned body is the caller's to close.
	Media(ctx context.Context, channelID int64, messageID, photoIndex int, slice MediaSlice) (MediaStream, error)
	// CustomEmoji returns the bytes of one custom emoji by document id, or
	// CodeNotFound when Telegram does not serve it.
	CustomEmoji(ctx context.Context, documentID int64) (MediaFile, error)

	// Find resolves one discovery query against Telegram.
	Find(ctx context.Context, req FindRequest) (FindResult, error)
	// Channel resolves one channel reference to its current Telegram state.
	Channel(ctx context.Context, ref ChannelRef) (ChannelResult, error)
	// Join joins the channel when needed and reports what Telegram confirmed.
	// It never changes local storage: persistence belongs to the gateway, which
	// owns the database.
	Join(ctx context.Context, ref ChannelRef) (JoinResult, error)
	// Avatar downloads the channel's profile photo.
	Avatar(ctx context.Context, ref ChannelRef) (AvatarFile, error)
	// Leave makes the account leave the channel. It reports Telegram's
	// confirmation and writes nothing locally: the record belongs to the
	// gateway, which owns the database.
	Leave(ctx context.Context, ref ChannelRef) (LeaveResult, error)
	// SendMessage sends one prepared post to a channel this worker's account
	// can reach. It is the one write this contract makes to Telegram's
	// content, and it is only ever reached from a publishing request the user
	// made explicitly.
	SendMessage(ctx context.Context, req SendRequest) (SendResult, error)
}

// Pagination bounds shared by the worker and the gateway.
const (
	DefaultPage    = 1
	DefaultPerPage = 20
	MaxPerPage     = 100
)

// NormalizePage clamps a requested page and page size.
func NormalizePage(page, perPage int) (int, int) {
	if page < DefaultPage {
		page = DefaultPage
	}
	if perPage < 1 {
		perPage = DefaultPerPage
	}
	if perPage > MaxPerPage {
		perPage = MaxPerPage
	}
	return page, perPage
}

// --- errors between worker and gateway ---

// classify turns an internal error into a wire error.
func classify(err error) (*Error, int) {
	var coded *Error
	if errors.As(err, &coded) {
		return coded, statusForCode(coded.Code)
	}
	var flood interface{ FloodSeconds() int }
	if errors.As(err, &flood) {
		return NewError(CodeRateLimited, "Telegram is rate limiting this client.", flood.FloodSeconds()),
			http.StatusTooManyRequests
	}
	switch {
	case errors.Is(err, ErrNeedsAuth):
		return NewError(CodeNeedsAuth, "The Telegram account is not authorized.", 0), http.StatusConflict
	case errors.Is(err, ErrNotConnected):
		return NewError(CodeNotConnected, "The Telegram connection is not available.", 0),
			http.StatusServiceUnavailable
	case errors.Is(err, ErrChannelNotFound):
		return NewError(CodeChannelNotFound, "The channel was not found.", 0), http.StatusNotFound
	case errors.Is(err, ErrInviteInvalid):
		return NewError(CodeInviteInvalid, "The invite link is not valid or has expired.", 0),
			http.StatusBadRequest
	case errors.Is(err, ErrMembershipLimit):
		return NewError(CodeMembershipLimit, "This account cannot join more channels.", 0),
			http.StatusConflict
	case errors.Is(err, ErrPeerUnresolved):
		return NewError(CodePeerUnresolved,
			"This numeric channel id cannot be resolved without prior peer information; "+
				"search for the channel by username or link first.", 0), http.StatusBadRequest
	case errors.Is(err, ErrNoAvatar):
		return NewError(CodeNoAvatar, "This channel has no avatar.", 0), http.StatusNotFound
	case errors.Is(err, ErrSendForbidden):
		return NewError(CodeSendForbidden,
				"The destination refused the message: this account may not post there.", 0),
			http.StatusForbidden
	case errors.Is(err, ErrSendUnsupported):
		return NewError(CodeSendUnsupported, "This content cannot be sent to that destination.", 0),
			http.StatusBadRequest
	case errors.Is(err, ErrMediaTooLarge):
		return NewError(CodeMediaTooLarge,
				fmt.Sprintf("The attachment is larger than the %d byte limit.", MaxSendMediaBytes), 0),
			http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrMessageTooLong):
		return NewError(CodeMessageTooLong, "The message text is too long to send.", 0),
			http.StatusBadRequest
	default:
		return NewError(CodeInternal, "The worker failed to complete the request.", 0),
			http.StatusInternalServerError
	}
}

func statusForCode(code string) int {
	switch code {
	case CodeInvalidInput, CodeInviteInvalid, CodePeerUnresolved, CodeSendUnsupported, CodeMessageTooLong:
		return http.StatusBadRequest
	case CodeNeedsAuth:
		return http.StatusConflict
	case CodeNotConnected:
		return http.StatusServiceUnavailable
	case CodeChannelNotFound, CodeNotFound, CodeNoAvatar:
		return http.StatusNotFound
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeMembershipLimit:
		return http.StatusConflict
	case CodeSendForbidden:
		return http.StatusForbidden
	case CodeMediaTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	default:
		return http.StatusInternalServerError
	}
}

// wireError maps a wire error back onto the matching sentinel so callers can
// use errors.Is without inspecting codes.
func (e *Error) wire() error {
	switch e.Code {
	case CodeNeedsAuth:
		return errors.Join(e, ErrNeedsAuth)
	case CodeNotConnected:
		return errors.Join(e, ErrNotConnected)
	case CodeChannelNotFound:
		return errors.Join(e, ErrChannelNotFound)
	case CodeInviteInvalid:
		return errors.Join(e, ErrInviteInvalid)
	case CodeMembershipLimit:
		return errors.Join(e, ErrMembershipLimit)
	case CodePeerUnresolved:
		return errors.Join(e, ErrPeerUnresolved)
	case CodeNoAvatar:
		return errors.Join(e, ErrNoAvatar)
	case CodeSendForbidden:
		return errors.Join(e, ErrSendForbidden)
	case CodeSendUnsupported:
		return errors.Join(e, ErrSendUnsupported)
	case CodeMediaTooLarge:
		return errors.Join(e, ErrMediaTooLarge)
	case CodeMessageTooLong:
		return errors.Join(e, ErrMessageTooLong)
	default:
		return e
	}
}

// --- server ---

// Handler serves the private worker API over any http.Server.
func NewHandler(backend Backend) http.Handler {
	return &handler{backend: backend}
}

type handler struct {
	backend Backend
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	switch {
	case path == "/health":
		if r.Method != http.MethodGet {
			h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
			return
		}
		writeJSON(w, http.StatusOK, h.backend.Health(r.Context()))
	case path == "/profile":
		if r.Method != http.MethodGet {
			h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
			return
		}
		out, err := h.backend.Profile(r.Context())
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case path == "/channels":
		if r.Method != http.MethodGet {
			h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
			return
		}
		page, perPage, err := parsePaging(r.URL.Query())
		if err != nil {
			h.fail(w, err)
			return
		}
		out, err := h.backend.Channels(r.Context(), page, perPage)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case path == "/find":
		var req FindRequest
		if !h.decode(w, r, &req) {
			return
		}
		out, err := h.backend.Find(r.Context(), req)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case path == "/channel":
		var ref ChannelRef
		if !h.decode(w, r, &ref) {
			return
		}
		out, err := h.backend.Channel(r.Context(), ref)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case path == "/join":
		var ref ChannelRef
		if !h.decode(w, r, &ref) {
			return
		}
		out, err := h.backend.Join(r.Context(), ref)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case path == "/avatar":
		var ref ChannelRef
		if !h.decode(w, r, &ref) {
			return
		}
		out, err := h.backend.Avatar(r.Context(), ref)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case path == "/leave":
		var ref ChannelRef
		if !h.decode(w, r, &ref) {
			return
		}
		out, err := h.backend.Leave(r.Context(), ref)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case path == "/messages":
		req, ok := h.decodeSend(w, r)
		if !ok {
			return
		}
		out, err := h.backend.SendMessage(r.Context(), req)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case strings.HasPrefix(path, "/custom_emoji/"):
		if r.Method != http.MethodGet {
			h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
			return
		}
		id, convErr := strconv.ParseInt(strings.TrimPrefix(path, "/custom_emoji/"), 10, 64)
		if convErr != nil || id <= 0 {
			h.fail(w, NewError(CodeInvalidInput, "document_id must be a positive integer", 0))
			return
		}
		out, err := h.backend.CustomEmoji(r.Context(), id)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	default:
		channelID, messageID, kind := messageRoute(path)
		switch kind {
		case routeNotFound:
			http.NotFound(w, r)
			return
		case routeBadChannel:
			h.fail(w, NewError(CodeInvalidInput, "channel_id and message_id must be positive integers", 0))
			return
		case routeMessage:
			if r.Method != http.MethodGet {
				h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
				return
			}
			out, err := h.backend.Message(r.Context(), channelID, messageID)
			if err != nil {
				h.fail(w, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		case routeMedia:
			if r.Method != http.MethodGet {
				h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
				return
			}
			photoIndex := 0
			if raw := r.URL.Query().Get("photo_index"); raw != "" {
				v, convErr := strconv.Atoi(raw)
				if convErr != nil || v < 0 {
					h.fail(w, NewError(CodeInvalidInput, "photo_index must be a non-negative integer", 0))
					return
				}
				photoIndex = v
			}
			slice, sliceErr := parseMediaSlice(r.URL.Query())
			if sliceErr != nil {
				h.fail(w, sliceErr)
				return
			}
			out, err := h.backend.Media(r.Context(), channelID, messageID, photoIndex, slice)
			if err != nil {
				h.fail(w, err)
				return
			}
			writeMediaStream(w, out)
			return
		}
		if r.Method != http.MethodGet {
			h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
			return
		}
		page, perPage, err := parsePaging(r.URL.Query())
		if err != nil {
			h.fail(w, err)
			return
		}
		out, err := h.backend.Messages(r.Context(), channelID, page, perPage)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

type routeKind int

const (
	routeNotFound routeKind = iota
	routeOK
	routeBadChannel
	routeMedia
	routeMessage
)

// messageRoute matches /channels/{id}/messages and
// /channels/{id}/messages/{message_id}/media.
func messageRoute(path string) (channelID int64, messageID int, kind routeKind) {
	rest, ok := strings.CutPrefix(path, "/channels/")
	if !ok {
		return 0, 0, routeNotFound
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "messages" {
		return 0, 0, routeNotFound
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, 0, routeBadChannel
	}
	switch len(parts) {
	case 2:
		return id, 0, routeOK
	case 3:
		msgID, err := strconv.Atoi(parts[2])
		if err != nil || msgID <= 0 {
			return 0, 0, routeBadChannel
		}
		return id, msgID, routeMessage
	case 4:
		if parts[3] != "media" {
			return 0, 0, routeNotFound
		}
		msgID, err := strconv.Atoi(parts[2])
		if err != nil || msgID <= 0 {
			return 0, 0, routeBadChannel
		}
		return id, msgID, routeMedia
	default:
		return 0, 0, routeNotFound
	}
}

// parsePaging validates the page/per_page query. Unknown parameters are
// rejected so a typo cannot silently fall back to a default page.
func parsePaging(q map[string][]string) (int, int, error) {
	page, perPage := DefaultPage, DefaultPerPage
	for key, values := range q {
		if len(values) > 1 {
			return 0, 0, NewError(CodeInvalidInput, fmt.Sprintf("%s must be given once", key), 0)
		}
		raw := values[0]
		switch key {
		case "page":
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 {
				return 0, 0, NewError(CodeInvalidInput, "page must be an integer of at least 1", 0)
			}
			page = v
		case "per_page":
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > MaxPerPage {
				return 0, 0, NewError(CodeInvalidInput,
					fmt.Sprintf("per_page must be an integer between 1 and %d", MaxPerPage), 0)
			}
			perPage = v
		default:
			return 0, 0, NewError(CodeInvalidInput, fmt.Sprintf("unknown query parameter %q", key), 0)
		}
	}
	return page, perPage, nil
}

// parseMediaSlice reads the slice of an attachment a media request wants.
//
// Every value must be a non-negative integer: a value that is neither is
// refused rather than silently rounded to something the caller did not ask for.
func parseMediaSlice(q url.Values) (MediaSlice, error) {
	var slice MediaSlice
	for key, dst := range map[string]*int64{
		"offset": &slice.Offset,
		"limit":  &slice.Limit,
		"tail":   &slice.Tail,
	} {
		raw := q.Get(key)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return MediaSlice{}, NewError(CodeInvalidInput,
				fmt.Sprintf("%s must be a non-negative integer", key), 0)
		}
		*dst = v
	}
	return slice, nil
}

// writeMediaStream sends one attachment as the response body.
//
// The bytes are forwarded as they arrive rather than collected into a JSON
// document: a 60 MB attachment must reach the caller without ever being held
// in one buffer. The facts the URL cannot carry travel as response headers, and
// nothing else about the worker does.
func writeMediaStream(w http.ResponseWriter, stream MediaStream) {
	// A long transfer must outlive the server's per-request write deadline.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	defer stream.Body.Close()

	contentType := stream.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if stream.Filename != "" {
		w.Header().Set(HeaderMediaFilename, url.QueryEscape(stream.Filename))
	}
	w.Header().Set(HeaderMediaSize, strconv.FormatInt(stream.Size, 10))
	w.Header().Set(HeaderMediaOffset, strconv.FormatInt(stream.Offset, 10))
	w.Header().Set("Content-Type", contentType)
	if stream.Length > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(stream.Length, 10))
	}
	w.WriteHeader(http.StatusOK)
	// Headers are flushed before the first byte: a caller must learn what it is
	// being given — the type, the length, where the slice starts — without
	// waiting for the attachment to arrive.
	http.NewResponseController(w).Flush()
	_, _ = io.Copy(w, stream.Body)
}

// decodeSend reads the multipart body of a send request: one "request" part
// carrying the JSON description and an optional "media" part carrying the
// attachment bytes.
//
// The body is bounded by MaxSendMediaBytes so a single send cannot exhaust the
// worker's memory. The bytes never enter the JSON document.
func (h *handler) decodeSend(w http.ResponseWriter, r *http.Request) (SendRequest, bool) {
	if r.Method != http.MethodPost {
		h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
		return SendRequest{}, false
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		h.fail(w, NewError(CodeInvalidInput,
			"the request body must be multipart/form-data with a request part and an optional media part", 0))
		return SendRequest{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxSendMediaBytes+64<<10)
	reader := multipart.NewReader(r.Body, params["boundary"])

	var (
		req        SendRequest
		sawRequest bool
		mediaData  []byte
		sawMedia   bool
	)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.fail(w, NewError(CodeInvalidInput, "the multipart body is not readable", 0))
			return SendRequest{}, false
		}
		switch part.FormName() {
		case "request":
			if sawRequest {
				h.fail(w, NewError(CodeInvalidInput, "the request part must appear once", 0))
				return SendRequest{}, false
			}
			dec := json.NewDecoder(io.LimitReader(part, 1<<20))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				h.fail(w, NewError(CodeInvalidInput, "the request part is not valid", 0))
				return SendRequest{}, false
			}
			sawRequest = true
		case "media":
			if sawMedia {
				h.fail(w, NewError(CodeInvalidInput, "the media part must appear once", 0))
				return SendRequest{}, false
			}
			data, err := io.ReadAll(io.LimitReader(part, MaxSendMediaBytes+1))
			if err != nil {
				h.fail(w, NewError(CodeMediaTooLarge, "the attachment could not be read", 0))
				return SendRequest{}, false
			}
			if len(data) > MaxSendMediaBytes {
				h.fail(w, NewError(CodeMediaTooLarge,
					fmt.Sprintf("the attachment is larger than the %d byte limit", MaxSendMediaBytes), 0))
				return SendRequest{}, false
			}
			mediaData, sawMedia = data, true
		default:
			h.fail(w, NewError(CodeInvalidInput,
				fmt.Sprintf("unknown multipart part %q", part.FormName()), 0))
			return SendRequest{}, false
		}
	}
	if !sawRequest {
		h.fail(w, NewError(CodeInvalidInput, "the request part is missing", 0))
		return SendRequest{}, false
	}
	if sawMedia {
		if req.Media == nil {
			h.fail(w, NewError(CodeInvalidInput,
				"the media part needs the request part to describe it", 0))
			return SendRequest{}, false
		}
		// The bytes came from the file part; the description came from the
		// request part. Both are needed to name what is being sent.
		req.Media.Data = mediaData
	}
	return req, true
}

// decode reads a JSON request body, rejecting anything but a POST.
func (h *handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Method != http.MethodPost {
		h.fail(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
		return false
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		h.fail(w, NewError(CodeInvalidInput, "the request body is not valid", 0))
		return false
	}
	return true
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	coded, status := classify(err)
	writeJSON(w, status, ErrorBody{Error: coded})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- client ---

// DialFunc opens a connection to the worker.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Client is the gateway's handle on one worker socket.
type Client struct {
	socketPath string
	timeout    time.Duration
	http       *http.Client
	// stream is the client for calls whose answer is a body to read, not a
	// document to decode. Its own timeout is zero because a 60 MB attachment
	// legitimately takes longer than any per-request budget: the caller's
	// context is what ends a transfer. The transport still bounds how long the
	// worker may take to start answering.
	stream *http.Client
}

// NewClient builds a client for the Unix socket at path.
func NewClient(socketPath string, timeout time.Duration) *Client {
	return NewClientWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}, timeout)
}

// NewClientWithDialer builds a client over an injected dialer (tests).
func NewClientWithDialer(dial DialFunc, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	transport := &http.Transport{
		DialContext:           dial,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &Client{
		timeout: timeout,
		http:    &http.Client{Transport: transport, Timeout: timeout},
		stream:  &http.Client{Transport: transport},
	}
}

// Close releases idle connections.
func (c *Client) Close() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

// Health asks the worker for its state.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var out Health
	err := c.get(ctx, "/health", &out)
	return out, err
}

// Profile asks for the authorized account.
func (c *Client) Profile(ctx context.Context) (Profile, error) {
	var out Profile
	err := c.get(ctx, "/profile", &out)
	return out, err
}

// Channels asks for a page of channels.
func (c *Client) Channels(ctx context.Context, page, perPage int) (ChannelPage, error) {
	var out ChannelPage
	err := c.get(ctx, withPaging("/channels", page, perPage), &out)
	return out, err
}

// Messages asks for a page of channel history.
func (c *Client) Messages(ctx context.Context, channelID int64, page, perPage int) (MessagePage, error) {
	path := withPaging("/channels/"+strconv.FormatInt(channelID, 10)+"/messages", page, perPage)
	return c.messagePage(ctx, path)
}

// Message fetches exactly one post.
func (c *Client) Message(ctx context.Context, channelID int64, messageID int) (MessagePage, error) {
	return c.messagePage(ctx, fmt.Sprintf("/channels/%d/messages/%d", channelID, messageID))
}

// messagePage decodes a message page and maps a null reaction list to an
// empty one, so the gateway renders [] rather than null.
func (c *Client) messagePage(ctx context.Context, path string) (MessagePage, error) {
	var out MessagePage
	err := c.get(ctx, path, &out)
	for i := range out.Messages {
		if out.Messages[i].Reactions == nil {
			out.Messages[i].Reactions = []Reaction{}
		}
		if out.Messages[i].Entities == nil {
			out.Messages[i].Entities = []Entity{}
		}
	}
	return out, err
}

// CustomEmoji fetches the bytes of one custom emoji by document id.
func (c *Client) CustomEmoji(ctx context.Context, documentID int64) (MediaFile, error) {
	var out MediaFile
	err := c.get(ctx, "/custom_emoji/"+strconv.FormatInt(documentID, 10), &out)
	return out, err
}

// Media streams one attachment, or the slice of it slice names.
//
// The answer is a body rather than a document, so it is read from the worker as
// it arrives. The caller closes the body, including when it stops reading early.
func (c *Client) Media(ctx context.Context, channelID int64, messageID, photoIndex int, slice MediaSlice) (MediaStream, error) {
	path := fmt.Sprintf("/channels/%d/messages/%d/media", channelID, messageID)
	query := url.Values{}
	if photoIndex > 0 {
		query.Set("photo_index", strconv.Itoa(photoIndex))
	}
	if slice.Offset > 0 {
		query.Set("offset", strconv.FormatInt(slice.Offset, 10))
	}
	if slice.Limit > 0 {
		query.Set("limit", strconv.FormatInt(slice.Limit, 10))
	}
	if slice.Tail > 0 {
		query.Set("tail", strconv.FormatInt(slice.Tail, 10))
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://worker"+path, nil)
	if err != nil {
		return MediaStream{}, err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return MediaStream{}, requestError(ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		if readErr != nil {
			return MediaStream{}, fmt.Errorf("%w: %w", ErrUnavailable, readErr)
		}
		return MediaStream{}, decodeErrorBody(resp.StatusCode, body)
	}

	offset, _ := strconv.ParseInt(resp.Header.Get(HeaderMediaOffset), 10, 64)
	size, _ := strconv.ParseInt(resp.Header.Get(HeaderMediaSize), 10, 64)
	filename := ""
	if raw := resp.Header.Get(HeaderMediaFilename); raw != "" {
		if name, decodeErr := url.QueryUnescape(raw); decodeErr == nil {
			filename = name
		}
	}
	// Content-Length is the exact length of the slice when the worker knew it;
	// without one the body runs on until the attachment ends.
	length, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return MediaStream{
		MimeType: resp.Header.Get("Content-Type"),
		Filename: filename,
		Size:     size,
		Offset:   offset,
		Length:   length,
		Body:     resp.Body,
	}, nil
}

func withPaging(path string, page, perPage int) string {
	page, perPage = NormalizePage(page, perPage)
	return fmt.Sprintf("%s?page=%d&per_page=%d", path, page, perPage)
}

func (c *Client) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://worker"+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, dst)
}
