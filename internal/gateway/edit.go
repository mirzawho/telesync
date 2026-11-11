package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/editing"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/sending"
)

type editTarget struct {
	ClientKey string `json:"client_key"`
	ChannelID string `json:"channel_id"`
	MessageID int64  `json:"message_id"`
}

type editAccepted struct {
	OperationID string     `json:"operation_id"`
	Status      string     `json:"status"`
	AcceptedAt  string     `json:"accepted_at"`
	Target      editTarget `json:"target"`
	StatusURL   string     `json:"status_url"`
}

type editResult struct {
	MessageID int64  `json:"message_id"`
	MediaKind string `json:"media_kind,omitempty"`
}

type editError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type editStatusView struct {
	OperationID  string      `json:"operation_id"`
	Status       string      `json:"status"`
	Stage        string      `json:"stage,omitempty"`
	AttemptCount int         `json:"attempt_count"`
	Target       editTarget  `json:"target"`
	AcceptedAt   string      `json:"accepted_at"`
	CompletedAt  any         `json:"completed_at,omitempty"`
	NextAttempt  any         `json:"next_attempt_at,omitempty"`
	Result       *editResult `json:"result,omitempty"`
	Error        *editError  `json:"error,omitempty"`
}

// editPayload preserves whether `text` was present in JSON. An empty string is
// valid with an attachment, but an omitted text field is never a replacement
// instruction.
type editPayload struct {
	Text     *string                     `json:"text"`
	Entities []rpc.Entity                `json:"entities"`
	Media    *publishing.DirectSendMedia `json:"media"`
	Options  editPayloadOptions          `json:"options"`
}

type editPayloadOptions struct {
	DisableWebPagePreview bool `json:"disable_web_page_preview"`
}

func (s *Server) editTelegramMessage(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key, rawChannelID, rawMessageID, requestID string) {
	a := actorOf(r)
	if s.cfg.Edit == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeEditQueueUnavailable,
			"Message editing is not configured.", requestID)
		return
	}
	channelID, err := strconv.ParseInt(strings.TrimSpace(rawChannelID), 10, 64)
	if err != nil || channelID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "channel_id must be a positive Telegram channel id.", requestID)
		return
	}
	messageID, err := strconv.ParseInt(strings.TrimSpace(rawMessageID), 10, 32)
	if err != nil || messageID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "message_id must be a positive integer.", requestID)
		return
	}
	if a.user.ID <= 0 {
		// An edit outlives this HTTP request and must be tied to a real principal
		// whose current channel permissions can be checked again by the worker.
		s.fail(w, http.StatusForbidden, CodeMessageEditForbidden,
			"Message editing requires an account with channel edit access.", requestID)
		return
	}
	localID, found, err := s.editTargetLocalID(ctx, key, channelID)
	if err != nil {
		s.fail(w, http.StatusServiceUnavailable, CodeStoreError, "The destination channel could not be resolved.", requestID)
		return
	}
	if !visibleChannel(a.auth, localID) {
		s.fail(w, http.StatusForbidden, CodeMessageEditForbidden,
			"The destination channel may not be named by this account.", requestID)
		return
	}
	if !found || !a.auth.Allowed(channelstore.KindChannel, channelstore.ActionUpdate, localID) {
		s.fail(w, http.StatusForbidden, CodeMessageEditForbidden,
			"Editing this channel needs its channel-scoped update permission.", requestID)
		return
	}

	bodyLimit := int64(multipartSlack + maxSendPayload)
	if limit := s.cfg.Edit.StagingLimit(); limit > 0 {
		bodyLimit += limit
	}
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	in, err := s.readMessageEditRequest(w, r, key, channelID, messageID)
	if err != nil {
		s.editFailure(w, err, requestID)
		return
	}
	accepted, err := s.cfg.Edit.Accept(ctx, actorOfPublish(a), in)
	if err != nil {
		s.editFailure(w, err, requestID)
		return
	}
	statusURL := s.messageEditStatusPath(key, strconv.FormatInt(channelID, 10), messageID, accepted.Operation.PublicID)
	w.Header().Set("Location", statusURL)
	if !accepted.Created {
		w.Header().Set("Idempotent-Replay", "true")
	}
	writeJSON(w, http.StatusAccepted, envelope{Data: editAccepted{
		OperationID: accepted.Operation.PublicID,
		Status:      string(accepted.Operation.Status),
		AcceptedAt:  RFC3339String(accepted.Operation.CreatedAt),
		Target:      editTarget{ClientKey: key, ChannelID: strconv.FormatInt(channelID, 10), MessageID: messageID},
		StatusURL:   statusURL,
	}, Meta: metaFor(requestID, s.cfg.Now())})
}

func (s *Server) telegramMessageEditStatus(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key, rawChannelID, rawMessageID, operationID, requestID string) {
	if s.cfg.Edit == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeEditQueueUnavailable,
			"Message editing is not configured.", requestID)
		return
	}
	channelID, err := strconv.ParseInt(strings.TrimSpace(rawChannelID), 10, 64)
	if err != nil || channelID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "channel_id must be a positive Telegram channel id.", requestID)
		return
	}
	messageID, err := strconv.ParseInt(strings.TrimSpace(rawMessageID), 10, 32)
	if err != nil || messageID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "message_id must be a positive integer.", requestID)
		return
	}
	localID, _, lookupErr := s.editTargetLocalID(ctx, key, channelID)
	if lookupErr != nil {
		s.fail(w, http.StatusServiceUnavailable, CodeStoreError, "The destination channel could not be resolved.", requestID)
		return
	}
	if !visibleChannel(actorOf(r).auth, localID) {
		s.fail(w, http.StatusForbidden, CodeMessageEditForbidden,
			"The destination channel may not be named by this account.", requestID)
		return
	}
	op, err := s.cfg.Edit.Status(ctx, actorOfPublish(actorOf(r)), key,
		strconv.FormatInt(channelID, 10), messageID, operationID)
	if err != nil {
		s.editFailure(w, err, requestID)
		return
	}
	view := editStatusView{
		OperationID:  op.PublicID,
		Status:       string(op.Status),
		AttemptCount: op.AttemptCount,
		Target:       editTarget{ClientKey: op.ClientKey, ChannelID: op.DestinationChannelID, MessageID: op.TargetMessageID},
		AcceptedAt:   RFC3339String(op.CreatedAt),
		CompletedAt:  formatOptionalTime(op.CompletedAt),
		NextAttempt:  formatOptionalTime(op.NextAttemptAt),
	}
	if op.Status == channelstore.MessageEditProcessing || op.Status == channelstore.MessageEditRetrying {
		view.Stage = op.Stage
	}
	if op.Status == channelstore.MessageEditEdited {
		view.Result = &editResult{MessageID: op.TargetMessageID, MediaKind: op.ResultMediaKind}
	}
	if op.Status == channelstore.MessageEditFailed || op.Status == channelstore.MessageEditUnknown {
		view.Error = &editError{Code: op.LastErrorCode, Message: op.LastErrorMessage}
	}
	writeJSON(w, http.StatusOK, envelope{Data: view, Meta: metaFor(requestID, s.cfg.Now())})
}

func (s *Server) editTargetLocalID(ctx context.Context, key string, channelID int64) (localID int64, found bool, err error) {
	if s.cfg.Channels == nil {
		return 0, false, nil
	}
	row, ok, err := s.cfg.Channels.Get(ctx, key, channelID)
	if err != nil {
		return 0, false, err
	}
	if !ok {
		return 0, false, nil
	}
	return row.ID, true, nil
}

func (s *Server) messageEditStatusPath(key, channelID string, messageID int64, operationID string) string {
	return s.fullPath("/api/v1/telegram/" + key + "/channels/" + channelID + "/messages/" +
		strconv.FormatInt(messageID, 10) + "/edit/" + operationID)
}

func (s *Server) editFailure(w http.ResponseWriter, err error, requestID string) {
	var perr *publishing.Error
	if errors.As(err, &perr) {
		s.fail(w, perr.Status, perr.Code, perr.Message, requestID)
		return
	}
	s.fail(w, http.StatusInternalServerError, CodeStoreError,
		"The edit operation could not be processed.", requestID)
}

func (s *Server) readMessageEditRequest(w http.ResponseWriter, r *http.Request,
	key string, channelID, messageID int64) (editing.AcceptInput, error) {
	in := editing.AcceptInput{
		ClientKey: key, DestinationChannelID: channelID, TargetMessageID: messageID,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
	}
	var staged *sending.Staged
	keep := false
	defer func() {
		if !keep && staged != nil {
			s.cfg.Edit.DiscardStaged(staged)
		}
	}()
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return in, invalidEditRequest(http.StatusUnsupportedMediaType,
			"The request must be application/json or multipart/form-data.")
	}
	var payload editPayload
	switch mediaType {
	case "application/json":
		if err := decodeJSONBodyLimited(w, r, &payload, maxSendPayload); err != nil {
			return in, invalidEditRequest(statusOfBodyError(err), err.Error())
		}
	case "multipart/form-data":
		var payloadBytes []byte
		var sawPayload, sawFile bool
		mr, err := r.MultipartReader()
		if err != nil {
			return in, invalidEditRequest(http.StatusBadRequest, "The multipart body could not be read.")
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return in, invalidEditRequest(http.StatusBadRequest, "The multipart body ended early.")
			}
			switch part.FormName() {
			case sendPayloadField:
				if sawPayload {
					return in, invalidEditRequest(http.StatusBadRequest, "The payload field was sent more than once.")
				}
				sawPayload = true
				payloadBytes, err = io.ReadAll(io.LimitReader(part, maxSendPayload+1))
				if err != nil || len(payloadBytes) > maxSendPayload {
					return in, invalidEditRequest(http.StatusRequestEntityTooLarge, "The payload field is too large or unreadable.")
				}
			case sendFileField:
				if sawFile {
					return in, invalidEditRequest(http.StatusBadRequest, "Only one file may be sent.")
				}
				sawFile = true
				staged, err = s.cfg.Edit.StageAttachment(r.Context(), &sending.Attachment{
					Reader: part, Name: part.FileName(), MimeType: part.Header.Get("Content-Type"),
				})
				if err != nil {
					return in, err
				}
				in.Attachment = &sending.Attachment{Staged: staged, Name: part.FileName()}
			default:
				return in, invalidEditRequest(http.StatusBadRequest, fmt.Sprintf(
					"The multipart field %q is not accepted; use %q and %q.", part.FormName(), sendPayloadField, sendFileField))
			}
		}
		if !sawPayload {
			return in, invalidEditRequest(http.StatusBadRequest, "The payload field is required.")
		}
		decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return in, invalidEditRequest(http.StatusBadRequest, "The payload field is not valid JSON.")
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return in, invalidEditRequest(http.StatusBadRequest, "The payload field must contain exactly one JSON object.")
		}
		if sawFile && payload.Media == nil {
			return in, invalidEditRequest(http.StatusBadRequest, "media.kind is required when a file is sent.")
		}
		if !sawFile && payload.Media != nil {
			return in, invalidEditRequest(http.StatusBadRequest, "A file must be sent when media.kind is named.")
		}
	default:
		return in, invalidEditRequest(http.StatusUnsupportedMediaType,
			"The request must be application/json or multipart/form-data.")
	}
	if payload.Text == nil {
		return in, invalidEditRequest(http.StatusBadRequest, "The text field is required for a full replacement.")
	}
	in.Request = publishing.DirectSendRequest{
		Text: *payload.Text, Entities: payload.Entities, Media: payload.Media,
		Options: publishing.RequestOptions{DisableWebPagePreview: payload.Options.DisableWebPagePreview},
	}
	keep = true
	return in, nil
}

func invalidEditRequest(status int, message string) error {
	return &publishing.Error{Status: status, Code: CodeInvalidInput, Message: message}
}
