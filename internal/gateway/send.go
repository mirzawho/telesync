package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/sending"
)

// A direct send is accepted here and performed elsewhere. Everything this file
// does happens before the answer: it authenticates the caller, checks the
// destination, reads the request, takes the attachment, and hands the whole
// thing to the sending service, which records it and schedules it. Nothing here
// talks to Telegram, and nothing here waits for one.
//
// That is the point of the 202. A caller is told the send is safe with us, not
// that it has happened — and by the time the answer is written, the work
// survives this process ending.

// maxSendPayload is the largest JSON payload a send may carry. The text is the
// longest field; an attachment travels as its own multipart part and is bounded
// by the staging limit instead.
const maxSendPayload = 256 << 10

// multipartSlack is how much a multipart body may carry beyond the payload and
// the attachment: boundaries, part headers and the like. It bounds the request
// as a whole so an unbounded body cannot be used to exhaust this process.
const multipartSlack = 64 << 10

// Field names a multipart send accepts.
const (
	sendPayloadField = "payload"
	sendFileField    = "file"
)

// sendDestination is where a send is going, as the API reports it.
type sendDestination struct {
	ClientKey string `json:"client_key"`
	ChannelID string `json:"channel_id"`
}

// sendAccepted is the body of a 202.
//
// It describes the operation, not the message. Nothing in it claims the
// message was sent, that an attachment reached Telegram, or that a destination
// message exists — those are answers to poll for, and this is the point at
// which they are all still false.
type sendAccepted struct {
	OperationID string          `json:"operation_id"`
	Status      string          `json:"status"`
	AcceptedAt  string          `json:"accepted_at"`
	Destination sendDestination `json:"destination"`
	StatusURL   string          `json:"status_url"`
}

// sendStatusView is the body of a status read.
type sendStatusView struct {
	OperationID   string          `json:"operation_id"`
	Status        string          `json:"status"`
	Stage         string          `json:"stage,omitempty"`
	AttemptCount  int             `json:"attempt_count"`
	Destination   sendDestination `json:"destination"`
	AcceptedAt    string          `json:"accepted_at"`
	CompletedAt   any             `json:"completed_at,omitempty"`
	NextAttemptAt any             `json:"next_attempt_at,omitempty"`
	Result        *sendResult     `json:"result,omitempty"`
	Error         *sendError      `json:"error,omitempty"`
}

// sendResult is the message a completed send produced. It is present only once
// the destination has confirmed one.
type sendResult struct {
	MessageID int64 `json:"message_id"`
}

// sendError is why a send ended the way it did. It is a code and a sentence a
// caller can act on, and nothing about this deployment.
type sendError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// sendTelegramMessage serves POST /api/v1/telegram/{client_key}/channels/{channel_id}/messages/send.
//
// It accepts a message a caller composed — text they wrote and, optionally, a
// file they uploaded — and schedules it for delivery. There is no source
// message anywhere in it: nothing is copied and nothing is read back from
// Telegram to build the post.
func (s *Server) sendTelegramMessage(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key, channelID, requestID string) {

	a := actorOf(r)
	if !a.auth.Global(channelstore.PermissionContentPublish) {
		s.fail(w, http.StatusForbidden, CodePublishForbidden,
			"Sending a message needs the content.publish permission.", requestID)
		return
	}
	if s.cfg.Send == nil {
		s.fail(w, http.StatusServiceUnavailable, sending.CodeSendQueueUnavailable,
			"Direct message sending is not configured.", requestID)
		return
	}

	destID, err := strconv.ParseInt(strings.TrimSpace(channelID), 10, 64)
	if err != nil || destID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"channel_id must be a positive Telegram channel id.", requestID)
		return
	}

	if !s.sendDestinationVisible(r.Context(), a, key, destID) {
		s.fail(w, http.StatusForbidden, CodePublishDestDenied,
			"The destination channel may not be named by this account.", requestID)
		return
	}

	// The body is bounded before a byte of it is read: this endpoint takes
	// files, and a request that will not fit is refused at the door rather than
	// after it has been stored somewhere.
	bodyLimit := int64(multipartSlack + maxSendPayload)
	if limit := s.cfg.Send.StagingLimit(); limit > 0 {
		bodyLimit += limit
	}
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)

	in, err := s.readSendRequest(w, r, key, channelID, destID)
	if err != nil {
		s.sendFailure(w, err, requestID)
		return
	}

	accepted, err := s.cfg.Send.Accept(ctx, actorOfPublish(a), in)
	if err != nil {
		s.sendFailure(w, err, requestID)
		return
	}

	statusURL := s.sendStatusPath(key, channelID, accepted.Operation.PublicID)
	w.Header().Set("Location", statusURL)
	if !accepted.Created {
		// The same request under the same key is the same send. Nothing was
		// staged, queued or sent now, and saying so is what makes a retry safe
		// to make.
		w.Header().Set("Idempotent-Replay", "true")
	}

	answer := envelope{Data: sendAccepted{
		OperationID: accepted.Operation.PublicID,
		Status:      string(accepted.Operation.Status),
		AcceptedAt:  RFC3339String(accepted.Operation.CreatedAt),
		Destination: sendDestination{ClientKey: key, ChannelID: channelID},
		StatusURL:   statusURL,
	}, Meta: metaFor(requestID, s.cfg.Now())}
	writeJSON(w, http.StatusAccepted, answer)
}

// telegramSendStatus serves
// GET /api/v1/telegram/{client_key}/channels/{channel_id}/messages/send/{operation_id}.
//
// Reading is reading. Nothing here sends, retries or advances an operation, and
// polling a send has never produced a second message.
func (s *Server) telegramSendStatus(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key, channelID, operationID, requestID string) {

	a := actorOf(r)
	if s.cfg.Send == nil {
		s.fail(w, http.StatusServiceUnavailable, sending.CodeSendQueueUnavailable,
			"Direct message sending is not configured.", requestID)
		return
	}
	destID, err := strconv.ParseInt(strings.TrimSpace(channelID), 10, 64)
	if err != nil || destID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"channel_id must be a positive Telegram channel id.", requestID)
		return
	}
	// The operation is read through its channel, so the channel is checked
	// first: a caller who may not see the channel learns nothing about the
	// sends that went to it, whatever id they present.
	if !s.sendDestinationVisible(r.Context(), a, key, destID) {
		s.fail(w, http.StatusForbidden, CodePublishDestDenied,
			"The destination channel may not be named by this account.", requestID)
		return
	}
	op, err := s.cfg.Send.Status(ctx, actorOfPublish(a), key, channelID, operationID)
	if err != nil {
		s.sendFailure(w, err, requestID)
		return
	}

	view := sendStatusView{
		OperationID:   op.PublicID,
		Status:        string(op.Status),
		AttemptCount:  op.AttemptCount,
		Destination:   sendDestination{ClientKey: key, ChannelID: channelID},
		AcceptedAt:    RFC3339String(op.CreatedAt),
		CompletedAt:   formatOptionalTime(op.CompletedAt),
		NextAttemptAt: formatOptionalTime(op.NextAttemptAt),
	}
	// The stage is only said while there is something to say: a finished send
	// reports its outcome, not the last thing it happened to be doing.
	if op.Status == channelstore.SendProcessing || op.Status == channelstore.SendRetrying {
		view.Stage = op.Stage
	}
	if op.Status == channelstore.SendSent && op.RemoteMessageID > 0 {
		view.Result = &sendResult{MessageID: op.RemoteMessageID}
	}
	if op.Status == channelstore.SendFailed || op.Status == channelstore.SendUnknown {
		view.Error = &sendError{Code: op.LastErrorCode, Message: op.LastErrorMessage}
	}

	writeJSON(w, http.StatusOK, envelope{Data: view, Meta: metaFor(requestID, s.cfg.Now())})
}

// sendDestinationVisible is the channel rule for the send routes.
//
// It is the same rule every channel route applies: denied by default, an
// explicit deny wins over the global view, and a write needs the view. The
// Telegram peer id is resolved to its local record first, so the permission is
// asked about the record and never about a number no assignment can name.
//
// Telegram's own answer is asked for again when the send runs. Seeing a channel
// is not being able to post to it, and the two are decided at different times.
func (s *Server) sendDestinationVisible(ctx context.Context, a *actor, key string, destID int64) bool {
	localID := int64(0)
	if s.cfg.Channels != nil {
		if row, ok, err := s.cfg.Channels.Get(ctx, key, destID); err == nil && ok {
			localID = row.ID
		}
	}
	return visibleChannel(a.auth, localID)
}

// sendStatusPath is the address of one operation's status, as the caller sees
// it: under the base path, beside the route that accepted it.
func (s *Server) sendStatusPath(key, channelID, operationID string) string {
	return s.fullPath("/api/v1/telegram/" + key + "/channels/" + channelID + "/messages/send/" + operationID)
}

// sendFailure answers a failed send request with the status and code the
// failure names. An error nothing classified is a store failure.
func (s *Server) sendFailure(w http.ResponseWriter, err error, requestID string) {
	var perr *publishing.Error
	if errors.As(err, &perr) {
		s.fail(w, perr.Status, perr.Code, perr.Message, requestID)
		return
	}
	s.fail(w, http.StatusInternalServerError, CodeStoreError, err.Error(), requestID)
}

// readSendRequest turns one request body into the send it describes.
//
// Two shapes are accepted on the same route. A JSON body is a message with no
// attachment. A multipart body carries the message as a JSON part and the file
// beside it, and the file may come first: a part is spent by the time the next
// one is read, so it is written down as it arrives rather than held while the
// rest of the body is read.
func (s *Server) readSendRequest(w http.ResponseWriter, r *http.Request, key, channelID string, destID int64) (sending.AcceptInput, error) {
	in := sending.AcceptInput{
		ClientKey:            key,
		DestinationChannelID: destID,
		IdempotencyKey:       strings.TrimSpace(r.Header.Get("Idempotency-Key")),
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return in, &publishing.Error{Status: http.StatusUnsupportedMediaType, Code: CodeInvalidInput,
			Message: "The request must be application/json or multipart/form-data."}
	}

	switch mediaType {
	case "application/json":
		var req publishing.DirectSendRequest
		if err := decodeJSONBodyLimited(w, r, &req, maxSendPayload); err != nil {
			return in, &publishing.Error{Status: statusOfBodyError(err), Code: CodeInvalidInput, Message: err.Error()}
		}
		in.Request = req
		return in, nil

	case "multipart/form-data":
		return s.readSendMultipart(w, r, in)
	}
	return in, &publishing.Error{Status: http.StatusUnsupportedMediaType, Code: CodeInvalidInput,
		Message: "The request must be application/json or multipart/form-data."}
}

// readSendMultipart reads one multipart send.
//
// The two fields are exactly "payload" and "file". Each may appear once: a
// second of either is refused rather than silently resolved by taking the first
// or the last, because a client that sent two means one of them is a mistake
// and it is not this side's to guess which.
func (s *Server) readSendMultipart(w http.ResponseWriter, r *http.Request, in sending.AcceptInput) (sending.AcceptInput, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: "The multipart body could not be read."}
	}

	var (
		payload    []byte
		sawPayload bool
		sawFile    bool
		staged     *sending.Staged
	)
	// Anything that leaves this function without an accepted send gives the
	// staged file back, so a request that turns out to be unusable never leaves
	// an attachment behind for nobody.
	keep := false
	defer func() {
		if !keep && staged != nil {
			s.cfg.Send.DiscardStaged(staged)
		}
	}()

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
				Message: "The multipart body ended early."}
		}

		switch part.FormName() {
		case sendPayloadField:
			if sawPayload {
				return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
					Message: "The payload field was sent more than once."}
			}
			sawPayload = true
			payload, err = io.ReadAll(io.LimitReader(part, maxSendPayload+1))
			if err != nil {
				return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
					Message: "The payload field could not be read."}
			}
			if len(payload) > maxSendPayload {
				return in, &publishing.Error{Status: http.StatusRequestEntityTooLarge, Code: publishing.CodePublishMediaTooLarge,
					Message: "The payload field is too large."}
			}

		case sendFileField:
			if sawFile {
				return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
					Message: "Only one attachment may be sent; the file field appeared more than once."}
			}
			sawFile = true
			// The part is written down now rather than after the rest of the
			// body has been read: its reader is spent by the next call to
			// NextPart, and holding a file in memory until then is exactly what
			// this endpoint must not do.
			kept, err := s.cfg.Send.StageAttachment(r.Context(), &sending.Attachment{
				Reader:   part,
				Name:     part.FileName(),
				MimeType: part.Header.Get("Content-Type"),
			})
			if err != nil {
				return in, err
			}
			staged = kept
			in.Attachment = &sending.Attachment{Staged: kept, Name: part.FileName()}

		default:
			return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
				Message: fmt.Sprintf("The multipart body has an unexpected field %q; only %q and %q are accepted.",
					part.FormName(), sendPayloadField, sendFileField)}
		}
	}

	if !sawPayload {
		return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: "The payload field is required."}
	}
	var req publishing.DirectSendRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: "The payload field is not valid JSON."}
	}
	in.Request = req

	if sawFile && req.Media == nil {
		return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: "media.kind is required when a file is sent."}
	}
	if !sawFile && req.Media != nil {
		return in, &publishing.Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: "A file must be sent when media.kind is named."}
	}

	keep = true
	return in, nil
}

// formatOptionalTime renders a timestamp that may not be there, omitting it
// rather than inventing one.
func formatOptionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return RFC3339String(*t)
}
