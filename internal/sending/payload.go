package sending

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/rpc"
)

// The request payload is stored as the request itself, as JSON. It is what the
// background worker sends later, so it is written down in the shape the caller
// composed it in rather than in a private one that would have to be translated
// both ways and could drift from what was asked for.

// payloadOf is the stored form of a request. The attachment is not in it: the
// bytes are staged on their own and are named by the operation beside this.
func payloadOf(req publishing.DirectSendRequest) string {
	raw, err := json.Marshal(req)
	if err != nil {
		// A request this process just parsed always marshals again. If it ever
		// does not, an empty payload would send an empty message, which is
		// worse than saying so.
		return ""
	}
	return string(raw)
}

// decodePayload is the request a stored operation describes.
func decodePayload(raw string) (publishing.DirectSendRequest, error) {
	var req publishing.DirectSendRequest
	if strings.TrimSpace(raw) == "" {
		return req, errors.New("the stored request is empty")
	}
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return req, fmt.Errorf("the stored request could not be read: %w", err)
	}
	return req, nil
}

// mediaKindOf is the attachment's kind, empty for a text-only send.
func mediaKindOf(req publishing.DirectSendRequest) string {
	if req.Media == nil {
		return ""
	}
	return req.Media.Kind
}

// requestDuration is a recording's length in seconds. Zero means the caller did
// not say, and it is sent as no length rather than as a guess.
func requestDuration(req publishing.DirectSendRequest) int {
	if req.Media == nil || req.Media.DurationSeconds == nil {
		return 0
	}
	return *req.Media.DurationSeconds
}

// channelIDOf reads the destination channel id back out of the decimal string
// it was stored as.
func channelIDOf(raw string) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return id
}

// formatChannelID keeps a channel id as decimal text.
//
// A Telegram channel id is wider than a JavaScript number, so an API that
// returns one as a number hands the browser a different value than the server
// has. Every id crosses this boundary as text for that reason and no other.
func formatChannelID(id int64) string { return strconv.FormatInt(id, 10) }

// newRandomID issues the client message id one send is performed under.
//
// It is generated once, before the first attempt, and every later attempt sends
// the same one: that is what tells the destination a resend is the same message
// rather than a second one. It is random per operation and never derived from
// the request, because two identical sends are two messages and must not share
// an id — the destination would deduplicate the second away.
func newRandomID() (int64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, fmt.Errorf("sending: send identity: %w", err)
	}
	id := int64(binary.LittleEndian.Uint64(raw[:]))
	if id == 0 {
		return 1, nil
	}
	return id, nil
}

// messageOf is the sentence a caller is shown for a failure.
func messageOf(err error) string {
	if err == nil {
		return ""
	}
	var perr *publishing.Error
	if errors.As(err, &perr) && perr.Message != "" {
		return perr.Message
	}
	var coded *rpc.Error
	if errors.As(err, &coded) && coded.Message != "" {
		return coded.Message
	}
	return err.Error()
}

// retryTimeOf is when the next attempt becomes possible, when that is known.
//
// A zero time is not "now": it is "the queue decides". Only a delay the remote
// side itself named is written down here, because any other number would be a
// second guess at a policy the queue already owns.
func retryTimeOf(delay time.Duration) time.Time {
	if delay <= 0 {
		return time.Time{}
	}
	return time.Now().UTC().Add(delay)
}
