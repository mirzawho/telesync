package publishing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// A direct send is a message a caller composed and wants in a channel. Nothing
// was copied: there is no source message, no source channel and no attachment
// that belongs to another post. It is the other way to produce an outgoing
// message, and it goes out through exactly the same door as a publish.
//
// This file is that door. It holds what the two have in common — the
// destination's own connection, its permissions, how a message is built and how
// a failure is classified — and nothing of where the content came from. The
// caller owns the durable record of an asynchronous send, because its lifecycle
// is longer than one call here.

// Media kinds a direct send accepts. They are what the caller says the file is,
// and they are never guessed from a file name or a content type: an OGG file is
// a voice recording or an ordinary audio file depending on what the sender
// meant, and only they know which.
const (
	DirectMediaPhoto    = "photo"
	DirectMediaVideo    = "video"
	DirectMediaAudio    = "audio"
	DirectMediaVoice    = "voice"
	DirectMediaDocument = "document"
)

// DirectSendMedia describes the attachment of a direct send.
//
// The bytes are not here. They are staged beside the request and are named by
// an opaque storage name; what this carries is how they are to be sent.
type DirectSendMedia struct {
	// Kind is one of the DirectMedia kinds above.
	Kind string `json:"kind"`
	// DurationSeconds is the length of a recording, for "audio" and "voice".
	// Zero means the caller did not say, and none is invented: a voice message
	// with no length shows none rather than a made-up one.
	DurationSeconds *int `json:"duration_seconds"`
}

// DirectSendRequest is one message a caller composed for a channel.
//
// It carries no source and no destination: both come from the route, and
// neither is a field a caller could point at somebody else's message.
type DirectSendRequest struct {
	// Text is the message body, or the caption of an attachment.
	Text string `json:"text"`
	// Entities are its formatting spans, in UTF-16 code units — the same
	// indexing Telegram uses, and the same one the publishing route accepts.
	Entities []rpc.Entity `json:"entities"`
	// Media names the attachment that was uploaded with this request. Nil is a
	// text-only message.
	Media *DirectSendMedia `json:"media"`
	// Options are the per-request switches.
	Options RequestOptions `json:"options"`
}

// maxDurationSeconds is a sanity bound on a reported recording length. It is
// not a Telegram limit and is not presented as one: it exists so an obviously
// wrong number is refused at the door instead of being written into an
// attribute.
const maxDurationSeconds = 24 * 60 * 60

// ValidateDirect checks a direct send before anything is staged or recorded.
//
// hasAttachment says whether the request arrived with a file. It is not derived
// from Media: a caller can name a kind and send no file, or send a file and
// name no kind, and those are two different mistakes with two different fixes.
func ValidateDirect(req DirectSendRequest, hasAttachment bool) error {
	return validateDirect(req, hasAttachment, false)
}

// ValidateDirectEdit validates one full message replacement. keepExistingMedia
// permits empty text only when the edit explicitly retains the target's
// existing attachment; direct-send validation remains unchanged.
func ValidateDirectEdit(req DirectSendRequest, hasAttachment, keepExistingMedia bool) error {
	if keepExistingMedia && (hasAttachment || req.Media != nil) {
		return errors.New("existing media cannot be kept while replacing media")
	}
	return validateDirect(req, hasAttachment, keepExistingMedia)
}

func validateDirect(req DirectSendRequest, hasAttachment, keepExistingMedia bool) error {
	if req.Media != nil && !knownDirectMediaKind(req.Media.Kind) {
		return fmt.Errorf("media.kind must be one of %q, %q, %q, %q or %q",
			DirectMediaPhoto, DirectMediaVideo, DirectMediaAudio, DirectMediaVoice, DirectMediaDocument)
	}
	switch {
	case hasAttachment && req.Media == nil:
		return errors.New("media.kind is required when a file is sent")
	case !hasAttachment && req.Media != nil:
		return errors.New("a file must be sent when media.kind is named")
	case hasAttachment:
		if err := validateDirectDuration(*req.Media); err != nil {
			return err
		}
	}

	// A message is text or an attachment or both. Neither at all is not a
	// message, and an empty body is not sent to say nothing.
	if strings.TrimSpace(req.Text) == "" && !hasAttachment && !keepExistingMedia {
		return errors.New("text must not be empty when no attachment is sent")
	}

	// The body of a message and the caption of an attachment are limited
	// differently, and the shorter one applies here. Nothing is truncated: an
	// over-long body is refused, because a caller who wrote it should be told
	// rather than have it cut without them seeing.
	limit, what := tgapi.MaxMessageRunes, "text"
	if hasAttachment {
		limit, what = tgapi.MaxCaptionRunes, "caption"
	}
	if runes := len([]rune(req.Text)); runes > limit {
		return fmt.Errorf("%s is %d characters; the limit is %d", what, runes, limit)
	}

	for _, e := range req.Entities {
		if !knownEntityType(e.Type) {
			return fmt.Errorf("entity type %q is not a supported formatting span", e.Type)
		}
		if !entityValid(req.Text, e) {
			return errors.New("an entity span falls outside text")
		}
	}
	return nil
}

// validateDirectDuration checks a reported recording length.
//
// A length is only meaningful beside audio. One named for a photograph is a
// mistake worth saying so about rather than carrying into an attribute that
// describes something the message does not contain.
func validateDirectDuration(media DirectSendMedia) error {
	if media.DurationSeconds == nil {
		return nil
	}
	seconds := *media.DurationSeconds
	if media.Kind != DirectMediaAudio && media.Kind != DirectMediaVoice {
		return fmt.Errorf("media.duration_seconds applies only to %q and %q", DirectMediaAudio, DirectMediaVoice)
	}
	if seconds < 0 {
		return errors.New("media.duration_seconds must not be negative")
	}
	if seconds > maxDurationSeconds {
		return fmt.Errorf("media.duration_seconds must be at most %d", maxDurationSeconds)
	}
	return nil
}

func knownDirectMediaKind(kind string) bool {
	switch kind {
	case DirectMediaPhoto, DirectMediaVideo, DirectMediaAudio, DirectMediaVoice, DirectMediaDocument:
		return true
	}
	return false
}

// knownEntityType reports whether a span is a kind of formatting Telegram has.
//
// A type this build cannot rebuild for the outgoing message is still a known
// type: it is sent and counted as lost by the adapter, exactly as it is for a
// publish. What is refused is a type nobody recognises, which would be written
// as a span describing nothing.
func knownEntityType(kind string) bool {
	switch kind {
	case tgapi.EntityMention, tgapi.EntityHashtag, tgapi.EntityBotCommand,
		tgapi.EntityURL, tgapi.EntityEmail, tgapi.EntityBold, tgapi.EntityItalic,
		tgapi.EntityCode, tgapi.EntityPre, tgapi.EntityTextURL, tgapi.EntityMentionName,
		tgapi.EntityCashtag, tgapi.EntityPhone, tgapi.EntityUnderline, tgapi.EntityStrike,
		tgapi.EntityBankCard, tgapi.EntitySpoiler, tgapi.EntityBlockquote, tgapi.EntityCustomEmoji:
		return true
	}
	return false
}

// ActorFor resolves the account a background send runs under, reading its
// permissions again rather than trusting anything recorded earlier.
//
// It is the same resolution a background publication uses, exposed because a
// send that runs after the request that asked for it needs an authorization
// context of its own — never the caller's bearer token, which is not a
// credential a later attempt can hold.
func (s *Service) ActorFor(ctx context.Context, userID int64) (Actor, error) {
	return s.actorFor(ctx, userID)
}

// DirectResult is what one direct send achieved.
//
// Outcome is set whether or not the send succeeded, because the way a failure
// is recorded depends on what it means rather than only on that it happened: a
// request that may have taken effect is not the same as one that certainly did
// not, and only this distinction stops a retry from posting twice.
type DirectResult struct {
	// Message is the message the destination returned, when it returned one.
	Message *rpc.Message
	// UnsendableEntities counts spans that could not be rebuilt for the
	// outgoing message. The text is sent regardless and the loss is reported.
	UnsendableEntities int
	// Outcome is how the attempt should be recorded: succeeded, failed, or
	// unknown when the request may have reached the destination and no answer
	// came back. An unknown outcome is never retried — the message may already
	// be there.
	Outcome channelstore.PublishStatus
	// RetryAfter is the delay the destination itself asked for before trying
	// again, such as a flood wait. Zero means it did not say, and whoever is
	// scheduling the next attempt applies its own policy.
	RetryAfter time.Duration
}

// SendDirect sends one prepared post to one destination.
//
// It is the whole of the outgoing path a source-based publish shares: the
// destination is resolved and checked exactly as it is there, the message goes
// out on that connection and no other, and a failure is classified into the
// same answers. What is absent is a source — nothing is read from anywhere, and
// the post is what the caller composed.
//
// The destination's channel id is taken from the argument and never from the
// request: what was authorized is what is sent to, so a prepared post cannot be
// pointed at a channel the caller was not cleared for.
//
// Nothing is recorded here. A synchronous publish records its attempt around
// this call; an asynchronous send's record lives across retries and restarts
// and belongs to whoever scheduled it.
func (s *Service) SendDirect(ctx context.Context, a Actor, clientKey string, destID int64, send rpc.SendRequest) (DirectResult, error) {
	if err := s.requirePublishPermission(a); err != nil {
		return DirectResult{Outcome: channelstore.PublishFailed}, err
	}
	worker, err := s.resolvePublishDestination(ctx, a, clientKey, destID)
	if err != nil {
		// Nothing was sent: the destination could not be resolved or would not
		// accept a post from this account. It is a failure and a clear one.
		return DirectResult{Outcome: channelstore.PublishFailed}, err
	}

	send.ChannelID = destID
	result, sendErr := worker.SendMessage(ctx, send)
	if sendErr != nil {
		// classifySend is the one place that says whether a failure means
		// "definitely not sent" or "we never heard back". An asynchronous send
		// leans on it more than anything else does, because the retry that would
		// duplicate a message is its own next step.
		status, failure := classifySend(sendErr, send.Media != nil)
		return DirectResult{Outcome: status, RetryAfter: sendRetryAfter(sendErr)}, failure
	}
	return DirectResult{
		Message:            result.Message,
		UnsendableEntities: result.UnsendableEntities,
		Outcome:            channelstore.PublishSucceeded,
	}, nil
}

// sendRetryAfter is the delay a destination asked for before another attempt.
//
// It is read from the failure that came back rather than from the classified
// answer, because the reason is part of what the connection said and the
// classified error deliberately carries only what a caller is told.
func sendRetryAfter(err error) time.Duration {
	var coded *rpc.Error
	if errors.As(err, &coded) && coded.RetryAfter > 0 {
		return time.Duration(coded.RetryAfter) * time.Second
	}
	var flood interface{ FloodSeconds() int }
	if errors.As(err, &flood) && flood.FloodSeconds() > 0 {
		return time.Duration(flood.FloodSeconds()) * time.Second
	}
	return 0
}
