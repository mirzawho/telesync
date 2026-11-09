package sending

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/staging"
)

// sniffBytes is how much of an attachment is looked at to tell what it is.
// It is the size the content-type sniffing rules are written against, and it is
// the only part of a file that is ever held in memory here.
const sniffBytes = 512

// MaxIdempotencyKey is the longest idempotency key a send accepts. It is the
// same limit the publishing route documents, and it exists because a key is
// stored and indexed: an unbounded header value is not a request identity, it
// is a way to fill a table.
const MaxIdempotencyKey = 128

// AcceptInput is one request as it arrived, already authenticated and already
// pointed at a destination.
//
// The attachment is a stream and stays one: it is written straight to the
// staging area and is never collected into a buffer, so a large file costs disk
// and not memory.
type AcceptInput struct {
	// ClientKey and DestinationChannelID name where the message goes. The
	// channel id is the Telegram id the connection reports, never a local row id.
	ClientKey            string
	DestinationChannelID int64
	// Request is the message as the caller composed it.
	Request publishing.DirectSendRequest
	// IdempotencyKey is the caller's own key, empty when it sent none. It makes
	// a repeated request safe and is never part of the request's identity.
	IdempotencyKey string
	// Attachment is the uploaded file. Nil is a text-only send.
	Attachment *Attachment
}

// Attachment is one uploaded file.
type Attachment struct {
	// Reader is the file's bytes. It is consumed exactly once, during
	// acceptance, and is not held afterwards.
	Reader io.Reader
	// Staged is set when the file has already been written down, which is what
	// a multipart body that carries its file before the JSON describing it
	// forces: a part's reader is spent by the time the next part is read, so the
	// file cannot be held while the rest of the body arrives. Exactly one of
	// Reader and Staged is used.
	Staged *Staged
	// Name is the file name the client supplied. It is kept as metadata so the
	// message can show the name the sender chose, and is never a path.
	Name string
	// MimeType is the content type the client declared. It is a claim: what the
	// bytes say is checked against it.
	MimeType string
}

// Staged is an attachment that is already in the staging area.
type Staged struct {
	File staging.File
	// ContentType is what the bytes turned out to be, decided when they were
	// written down. It is kept so the kind check sees the same answer whichever
	// order the request arrived in.
	ContentType string
}

// requestIdentity is everything that makes two requests the same request.
//
// The attachment is identified by its digest and not by its file name: two
// uploads called "report.pdf" are the same request only if they are the same
// bytes. The caller's idempotency key is not here either — it names the
// request, it does not describe it, so a caller that renames its key still gets
// the same request recognised.
type requestIdentity struct {
	ClientKey            string                    `json:"client_key"`
	DestinationChannelID int64                     `json:"destination_channel_id"`
	Text                 string                    `json:"text"`
	Entities             []json.RawMessage         `json:"entities"`
	Options              publishing.RequestOptions `json:"options"`
	MediaKind            string                    `json:"media_kind"`
	MediaSHA256          string                    `json:"media_sha256"`
	MediaSize            int64                     `json:"media_size"`
}

// fingerprint is the identity of one effective request.
//
// Entities are carried as their own JSON rather than through a struct of this
// package, so a change to how a span is represented changes the fingerprint the
// way it should: two requests whose formatting differs are two requests.
func fingerprint(in AcceptInput, digest string, size int64) (string, error) {
	spans := make([]json.RawMessage, 0, len(in.Request.Entities))
	for _, e := range in.Request.Entities {
		raw, err := json.Marshal(e)
		if err != nil {
			return "", fmt.Errorf("sending: request identity: %w", err)
		}
		spans = append(spans, raw)
	}
	kind := ""
	if in.Request.Media != nil {
		kind = in.Request.Media.Kind
	}
	id := requestIdentity{
		ClientKey:            in.ClientKey,
		DestinationChannelID: in.DestinationChannelID,
		Text:                 in.Request.Text,
		Entities:             spans,
		Options:              in.Request.Options,
		MediaKind:            kind,
		MediaSHA256:          digest,
		MediaSize:            size,
	}
	raw, err := json.Marshal(id)
	if err != nil {
		return "", fmt.Errorf("sending: request identity: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// mediaTypesAccepted is the content an attachment of each kind may actually be.
//
// A kind is a promise about how the destination will draw the file, and a file
// that cannot keep that promise is refused rather than sent as something else.
// "document" is absent on purpose: a document is what anything left over
// becomes, so nothing is refused for being unclassifiable.
var mediaTypesAccepted = map[string][]string{
	"photo": {"image/"},
	"video": {"video/"},
	"audio": {"audio/", "application/ogg"},
	// A voice recording is stored as OGG/Opus, which the sniffing rules call
	// application/ogg. It is audio and is accepted as audio.
	"voice": {"audio/", "application/ogg"},
}

// acceptableMedia reports whether the content type an attachment turns out to
// have is something its kind can carry.
func acceptableMedia(kind, contentType string) bool {
	accepted, ok := mediaTypesAccepted[kind]
	if !ok {
		// A document takes anything, including a file nothing here recognises.
		return true
	}
	contentType = baseType(contentType)
	if contentType == "" {
		return false
	}
	for _, prefix := range accepted {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	return false
}

// effectiveContentType is what an attachment is taken to be.
//
// A client's declared type is a claim and is not taken on faith: the first
// bytes are sniffed and what they say wins whenever they say something
// specific. When they say nothing — and most audio formats are not in the
// sniffing table at all — the claim stands, because refusing a perfectly good
// MP3 for being unrecognisable would be worse than believing a sender about
// their own file. The bytes are never re-encoded and never trusted to be
// executable: they are only classified, and only here.
func effectiveContentType(declared string, head []byte) string {
	sniffed := baseType(http.DetectContentType(head))
	if sniffed != "" && sniffed != "application/octet-stream" {
		return sniffed
	}
	return baseType(declared)
}

// baseType drops the parameters of a content type, keeping the type itself.
func baseType(contentType string) string {
	if contentType == "" {
		return ""
	}
	parsed, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	return strings.ToLower(parsed)
}

// sniffingReader hands back the head of a stream after looking at it, so an
// attachment can be classified and still be staged whole.
//
// It reads at most sniffBytes more than it is asked for and puts them back.
// Nothing else about the file is inspected: it is not unpacked, not executed
// and not interpreted past its first bytes.
func sniffingReader(r io.Reader) (head []byte, combined io.Reader, err error) {
	if r == nil {
		return nil, nil, nil
	}
	head = make([]byte, sniffBytes)
	n, readErr := io.ReadFull(r, head)
	head = head[:n]
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return head, nil, readErr
	}
	return head, io.MultiReader(bytes.NewReader(head), r), nil
}
