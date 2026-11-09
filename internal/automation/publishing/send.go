package publishing

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/rpc"
)

// photoSizeOfPhoto is the photo size an attachment is read at. photoIndex in the
// worker's Media call is not an attachment selector: it picks which of a photo's
// sizes is wanted, and 0 is the largest. The gateway's own media route reads at
// 0 for the same reason, and so must a publish.
const largestPhotoSize = 0

// buildSend assembles the outgoing request from the source message and the
// caller's edits.
//
// It resolves the source attachment before anything is sent, so a draft that
// needs its attachment is never published as text alone and then discovered to
// be missing a picture. The bytes are read from the source connection and
// carried across as content: a file reference from one account is not a
// capability on another, so nothing about the source's storage is reused.
func (s *Service) buildSend(ctx context.Context, source sourceMessage, req Request) (rpc.SendRequest, error) {

	send := rpc.SendRequest{
		ChannelID: mustChannelID(req.Destination.ChannelID),
		Text:      req.Content.Text,
		Entities:  req.Content.Entities,
		Silent:    req.Options.Silent,
		NoWebpage: req.Options.DisableWebPagePreview,
		RandomID:  randomIDFor(req),
	}
	if req.Media.Mode != MediaPreserveSource {
		return send, nil
	}
	if source.message.Media == nil || !mediaHasBytes(source.message.Media.Type) {
		// Nothing to preserve: a poll, a location or a message with no
		// attachment is sent as the text it is.
		return send, nil
	}
	if err := namedAttachment(req.Media.AttachmentID, source.message.Media); err != nil {
		return rpc.SendRequest{}, err
	}
	stream, err := source.worker.Media(ctx, source.row.ChannelID, source.message.ID, largestPhotoSize, rpc.MediaSlice{})
	if err != nil {
		return rpc.SendRequest{}, s.sourceMediaFailure(source, err)
	}
	// A post is sent as one buffer, so the stream is collected here. This is the
	// one place that is allowed: answering a reader streams instead.
	data, err := stream.ReadAll()
	if err != nil {
		if errors.Is(err, rpc.ErrMediaTooLarge) {
			s.logSourceMedia(source, err)
			return rpc.SendRequest{}, &Error{http.StatusRequestEntityTooLarge, CodePublishMediaTooLarge,
				"The attachment is too large to send."}
		}
		return rpc.SendRequest{}, s.sourceMediaFailure(source, err)
	}
	send.Media = &rpc.SendMedia{
		Kind:     sendKind(source.message.Media),
		Name:     sendFileName(source.message.Media),
		MimeType: stream.MimeType,
		Data:     data,
		Duration: source.message.Media.Duration,
		Waveform: source.message.Media.Waveform,
	}
	return send, nil
}

// sendKind is the outgoing media kind of a source attachment.
//
// It is the source's own classification, taken from the document attributes the
// source connection reported and never from the MIME type, the file name or the
// label: audio/ogg is what a voice recording is stored as and also what an
// ordinary OGG file is stored as. A recording is "voice" and only a recording
// is, so a destination draws a play button for one and a download card for the
// other — which is the whole difference between the two messages.
//
// Anything this build does not describe sends as a document, which is what it
// already did: photos are photos, audio is audio, voice is voice, and every
// other attachment keeps its existing behaviour.
func sendKind(m *rpc.Media) string {
	if m == nil {
		return "document"
	}
	switch m.Type {
	case "photo":
		return "photo"
	case "voice":
		return "voice"
	case "audio":
		return "audio"
	default:
		return "document"
	}
}

// sourceMediaFailure is the answer for an attachment the source connection
// could not read.
//
// It is a source failure and only that. The destination is neither named nor
// blamed: nothing was sent anywhere, and the draft is exactly as the caller
// left it. The message keeps its original wording because it is the one a
// caller is told to act on — publish without media, or retry — but it is now
// only ever produced by a genuine source read.
func (s *Service) sourceMediaFailure(source sourceMessage, err error) error {
	s.logSourceMedia(source, err)
	return &Error{http.StatusBadGateway, CodePublishSourceMedia,
		"The source attachment could not be read; publish without media or retry."}
}

// logSourceMedia records which connection and which message the read failed
// for, so a failing stage can be followed. The bytes and the file reference are
// never logged.
func (s *Service) logSourceMedia(source sourceMessage, err error) {
	s.logf("publishing: source media download failed: client=%s channel=%d message=%d: %v",
		source.row.ClientKey, source.row.ChannelID, source.message.ID, err)
}

// mediaHasBytes reports whether an attachment of this kind has bytes to send. A
// poll, a location or a web page preview is not an attachment the destination
// can receive, so there is nothing to preserve.
func mediaHasBytes(mediaType string) bool {
	switch mediaType {
	case "photo", "video", "document", "audio", "voice", "sticker", "gif":
		return true
	default:
		return false
	}
}

// sendFileName is the name the outgoing attachment is sent under.
//
// It is the name the sender gave the file, when Telegram reported one: a
// document that arrived as "original-app-name.apk" must not go out as "فایل".
// The label is a description of the media kind, and is only ever a fallback for
// an attachment Telegram never named.
//
// A voice recording is the exception. Telegram's own voice messages carry no
// documentAttributeFilename at all, and putting a name on one is what turns a
// recording into a file that happens to be named "پیام صوتی". So a recording
// keeps the name the sender gave it, if any, and is otherwise sent unnamed.
func sendFileName(m *rpc.Media) string {
	if m == nil {
		return "attachment"
	}
	if m.Filename != "" {
		return sanitiseFileName(m.Filename)
	}
	if m.Type == "voice" {
		return ""
	}
	if m.Label != "" {
		return sanitiseFileName(m.Label)
	}
	return "attachment"
}

// sanitiseFileName keeps a file name to something a document can carry.
func sanitiseFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "attachment"
	}
	return validUTF8Cut(name, 64)
}

// randomIDFor gives the send the client message id Telegram uses to recognise a
// resend. A request that carries an idempotency key gets a stable id, so a retry
// cannot post twice even if the operation record is lost.
func randomIDFor(req Request) int64 {
	key := requestHash(req)
	sum := sha256.Sum256([]byte(key))
	id := int64(binary.LittleEndian.Uint64(sum[:8]))
	if id == 0 {
		return 1
	}
	return id
}

// requestHash is the fingerprint a stored operation is checked against.
func requestHash(req Request) string {
	raw, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:16])
}

// sourceRequestHash is the fingerprint of a request that names only its source,
// which is what a WordPress post is recorded under: the site's answer is
// recorded against the message the post was composed from.
func sourceRequestHash(localChannelID int64, messageID int) string {
	var req Request
	req.Source.LocalChannelID = localChannelID
	req.Source.MessageID = messageID
	req.Media.Mode = MediaPreserveSource
	return requestHash(req)
}

func mustChannelID(raw string) int64 {
	id, _ := strconv.ParseInt(raw, 10, 64)
	return id
}

// namedAttachment refuses a request that names an attachment other than the one
// the source message carries.
//
// The media id is opaque. This build's worker writes "channel:message" into it,
// and the API documents it as something to hand back, not to read — so it is
// compared against the source's own id and never decoded into anything. A
// request that names a different attachment is refused rather than quietly
// answered with this one.
//
// An id left empty on either side is not a claim and is not checked: a caller
// that does not name one gets the attachment the message has.
func namedAttachment(want string, media *rpc.Media) error {
	if want == "" || media == nil || media.ID == "" || want == media.ID {
		return nil
	}
	return &Error{http.StatusBadRequest, CodeInvalidInput,
		"media.attachment_id is not an attachment of the source message."}
}
