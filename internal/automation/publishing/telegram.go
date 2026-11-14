package publishing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// TelegramResult is what one Telegram destination confirmed about the sent
// message.
type TelegramResult struct {
	// Message is the message Telegram returned for the send. It is nil when the
	// backend accepted the message but returned nothing that describes it.
	Message *rpc.Message
	// Messages and GroupedID are populated for confirmed native albums.
	Messages  []rpc.Message
	GroupedID string
	// DestinationChannelID is the destination channel id as the route reports
	// it, a decimal string so JavaScript cannot truncate it.
	DestinationChannelID string
	// UnsendableEntities counts formatting spans that could not be rebuilt for
	// the outgoing message. The text is sent regardless; the loss is reported
	// rather than hidden.
	UnsendableEntities int
	// Replayed is true when this answer is an earlier confirmed result being
	// returned again, and nothing was sent now.
	Replayed bool
	// Payload is the response's "data" object as JSON. It is what an idempotent
	// replay answers with, so the same request always gets the same answer.
	Payload json.RawMessage
}

// PublishTelegram sends one prepared post to one Telegram destination and
// records the attempt.
//
// The actor is the account the post is published as. It is resolved again at
// execution time for a background run, so publishing is never done on a grant
// that has been withdrawn.
//
// An idempotency key makes a retry safe: a confirmed success is replayed and
// never repeated, and an attempt whose outcome is unknown is not retried behind
// the caller's back. A request without a key is simply sent.
func (s *Service) PublishTelegram(ctx context.Context, a Actor, clientKey string, req Request) (TelegramResult, error) {
	if err := s.requirePublishPermission(a); err != nil {
		return TelegramResult{}, err
	}
	if err := Validate(req); err != nil {
		return TelegramResult{}, &Error{http.StatusBadRequest, CodeInvalidInput, err.Error()}
	}

	source, err := s.resolvePublishSource(ctx, a, req)
	if err != nil {
		return TelegramResult{}, err
	}
	destID, err := strconv.ParseInt(req.Destination.ChannelID, 10, 64)
	if err != nil || destID <= 0 {
		return TelegramResult{}, &Error{http.StatusBadRequest, CodeInvalidInput,
			"destination.channel_id must be a positive Telegram channel id."}
	}
	accountID := strings.TrimSpace(req.Destination.AccountID)
	if accountID == "" {
		// Interactive callers carry the selected account in their authenticated
		// actor. Workflow callers carry the destination's separately persisted
		// account, which may differ from the source account.
		accountID = strings.TrimSpace(a.AccountID)
	}
	destination, err := s.resolvePublishDestination(ctx, a, clientKey, accountID, destID)
	if err != nil {
		return TelegramResult{}, err
	}

	operation, err := s.beginPublish(ctx, a, req.IdempotencyKey,
		accountID, accountID+":"+clientKey+":"+req.Destination.ChannelID, requestHash(req))
	if err != nil {
		return TelegramResult{}, err
	}
	if operation != nil && operation.Status == channelstore.PublishSucceeded {
		// A confirmed success is replayed, never repeated.
		return TelegramResult{
			DestinationChannelID: req.Destination.ChannelID,
			Replayed:             true,
			Payload:              storedPayload(operation.Result),
		}, nil
	}

	if len(source.members) > 0 {
		album, buildErr := s.buildAlbumSend(ctx, source, req)
		if buildErr != nil {
			s.finishPublish(ctx, operation, channelstore.PublishFailed, "", CodeOf(buildErr))
			return TelegramResult{}, buildErr
		}
		albumWorker, ok := destination.(AlbumWorker)
		if !ok {
			unsupported := &Error{http.StatusBadRequest, CodePublishUnsupported,
				"The destination connection does not support native media albums."}
			s.finishPublish(ctx, operation, channelstore.PublishFailed, "", unsupported.Code)
			return TelegramResult{}, unsupported
		}
		result, sendErr := albumWorker.SendAlbum(ctx, album)
		if sendErr != nil {
			status, failure := classifySend(sendErr, true)
			s.finishPublish(ctx, operation, status, "", failure.Code)
			return TelegramResult{}, failure
		}
		if len(result.Messages) != len(album.Items) || result.GroupedID == "" {
			failure := &Error{http.StatusConflict, CodePublishUnknown,
				"The destination accepted the album request but did not confirm every grouped message; it will not be resent automatically."}
			s.finishPublish(ctx, operation, channelstore.PublishUnknown, "", failure.Code)
			return TelegramResult{}, failure
		}
		body := map[string]any{
			"messages":    result.Messages,
			"grouped_id":  result.GroupedID,
			"destination": map[string]any{"client_key": clientKey, "channel_id": req.Destination.ChannelID},
		}
		if result.UnsendableEntities > 0 {
			body["unsendable_entities"] = result.UnsendableEntities
		}
		payload, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			s.finishPublish(ctx, operation, channelstore.PublishUnknown, "", CodeStoreError)
			return TelegramResult{}, &Error{http.StatusInternalServerError, CodeStoreError, "The album result could not be encoded."}
		}
		s.finishPublish(ctx, operation, channelstore.PublishSucceeded, string(payload), "")
		return TelegramResult{
			Messages: result.Messages, GroupedID: result.GroupedID,
			DestinationChannelID: req.Destination.ChannelID,
			UnsendableEntities:   result.UnsendableEntities, Payload: payload,
		}, nil
	}
	send, err := s.buildSend(ctx, source, req)
	if err != nil {
		// Nothing has been sent: the draft failed while it was being prepared,
		// which is the one stage that is always safe to try again.
		s.finishPublish(ctx, operation, channelstore.PublishFailed, "", CodeOf(err))
		return TelegramResult{}, err
	}
	// The post goes out on the destination's connection, and only there: the
	// source connection is how the attachment was read and sends nothing.
	result, sendErr := destination.SendMessage(ctx, send)
	if sendErr != nil {
		status, failure := classifySend(sendErr, send.Media != nil)
		if send.Media != nil && send.Media.Kind == "sticker" && failure.Code == CodePublishUnsupported {
			failure.Code = CodePublishStickerUnsupported
			failure.Status = http.StatusUnprocessableEntity
			failure.Message = messageFor(sendErr, failure.Code)
		}
		s.finishPublish(ctx, operation, status, "", failure.Code)
		return TelegramResult{}, failure
	}

	body := map[string]any{
		"message":     result.Message,
		"destination": map[string]any{"client_key": clientKey, "channel_id": req.Destination.ChannelID},
	}
	if result.UnsendableEntities > 0 {
		body["unsendable_entities"] = result.UnsendableEntities
	}
	payload, err := json.Marshal(body)
	if err != nil {
		s.finishPublish(ctx, operation, channelstore.PublishSucceeded, "", "")
		return TelegramResult{}, &Error{http.StatusInternalServerError, CodeStoreError,
			"The result could not be encoded."}
	}
	s.finishPublish(ctx, operation, channelstore.PublishSucceeded, string(payload), "")
	return TelegramResult{
		Message:              result.Message,
		DestinationChannelID: req.Destination.ChannelID,
		UnsendableEntities:   result.UnsendableEntities,
		Payload:              payload,
	}, nil
}

// beginPublish records the attempt, or reports the state of an earlier one.
//
// destinationID identifies the target for the record: "client_key:channel_id"
// for a Telegram destination, "wp:site_id" for a WordPress one. A nil operation
// with a nil error means "no idempotency key, run it now".
func (s *Service) beginPublish(ctx context.Context, a Actor, key, accountID, destinationID, hash string) (*channelstore.PublishOperation, error) {
	if key == "" {
		return nil, nil
	}
	if s.Publishes == nil {
		return nil, &Error{http.StatusServiceUnavailable, CodeStoreError,
			"The publishing record store is unavailable."}
	}
	destType := "telegram"
	if strings.HasPrefix(destinationID, "wp:") {
		destType = "wordpress"
	}
	op, created, err := s.Publishes.BeginPublishOperation(ctx, channelstore.PublishOperation{
		UserID:          a.UserID,
		AccountID:       accountID,
		IdempotencyKey:  key,
		RequestHash:     hash,
		DestinationType: destType,
		DestinationID:   destinationID,
	})
	if errors.Is(err, channelstore.ErrIdempotencyConflict) {
		return nil, &Error{http.StatusConflict, CodePublishConflict,
			"This idempotency key was already used for a different request."}
	}
	if err != nil {
		return nil, &Error{http.StatusInternalServerError, CodeStoreError, err.Error()}
	}
	if created {
		return &op, nil
	}
	switch op.Status {
	case channelstore.PublishSucceeded:
		return &op, nil
	case channelstore.PublishUnknown:
		return nil, &Error{http.StatusConflict, CodePublishUnknown,
			"The earlier attempt with this key has no confirmed outcome and is not retried."}
	default:
		return nil, &Error{http.StatusConflict, CodePublishConflict,
			"The earlier attempt with this key is still in flight."}
	}
}

// finishPublish records what the attempt achieved. A missing operation is not
// an error: the request had no idempotency key.
func (s *Service) finishPublish(ctx context.Context, op *channelstore.PublishOperation,
	status channelstore.PublishStatus, result, errorCode string) {

	if op == nil || s.Publishes == nil {
		return
	}
	_ = s.Publishes.FinishPublishOperation(ctx, op.ID, status, result, errorCode)
}

// storedPayload is the "data" object an earlier attempt recorded.
//
// v0.0.2 stored the whole HTTP envelope in the operation row. It is unwrapped
// here so a replay of a row written before this release answers with the same
// object a new one would, instead of nesting it one level deeper.
func storedPayload(raw string) json.RawMessage {
	if strings.TrimSpace(raw) == "" {
		return json.RawMessage("{}")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err == nil {
		if data, ok := probe["data"]; ok {
			return data
		}
	}
	return json.RawMessage(raw)
}
