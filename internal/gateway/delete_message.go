package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

type deletedMessage struct {
	Deleted    bool   `json:"deleted"`
	AccountID  string `json:"account_id,omitempty"`
	Connection struct {
		ClientKey string `json:"client_key"`
	} `json:"connection"`
	ChannelID string `json:"channel_id"`
	MessageID int    `json:"message_id"`
}

// deleteTelegramMessage handles DELETE /channels/{channel_id}/messages/{message_id}.
func (s *Server) deleteTelegramMessage(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key string, worker Worker, rawChannelID, rawMessageID, requestID string) {
	channelID, err := strconv.ParseInt(rawChannelID, 10, 64)
	if err != nil || channelID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"channel_id must be a positive Telegram channel id.", requestID)
		return
	}
	messageID, err := strconv.ParseInt(rawMessageID, 10, 32)
	if err != nil || messageID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"message_id must be a positive 32-bit Telegram message id.", requestID)
		return
	}

	target, err := s.targetOf(ctx, key, channelID)
	if err != nil {
		s.storeError(w, "delete_message_channel_lookup", err, requestID)
		return
	}
	if !target.found {
		s.notFound(w, requestID)
		return
	}
	if !visible(actorAuth(r), channelstore.KindChannel, channelstore.ActionDelete, target.localID) {
		s.logf("gateway: client=%s op=delete_message request_id=%s channel_id=%d message_id=%d result=permission_denied",
			key, requestID, channelID, messageID)
		s.forbidden(w, requestID)
		return
	}

	started := time.Now()
	result, err := worker.DeleteMessage(ctx, rpc.DeleteMessageRequest{
		ChannelID: channelID,
		MessageID: int(messageID),
	})
	if err != nil {
		s.logf("gateway: client=%s op=delete_message request_id=%s channel_id=%d message_id=%d duration=%s result=error",
			key, requestID, channelID, messageID, time.Since(started))
		s.upstreamError(w, key, "delete_message", err, requestID)
		return
	}
	if !result.Deleted {
		s.fail(w, http.StatusBadGateway, CodeWorkerError,
			"The Telegram client did not confirm message deletion.", requestID)
		return
	}
	s.logf("gateway: client=%s op=delete_message request_id=%s channel_id=%d message_id=%d duration=%s result=deleted",
		key, requestID, channelID, messageID, time.Since(started))
	data := deletedMessage{
		Deleted: true, AccountID: accountRouteIDForResponse(r),
		ChannelID: strconv.FormatInt(channelID, 10), MessageID: int(messageID),
	}
	data.Connection.ClientKey = key
	writeJSON(w, http.StatusOK, envelope{
		Data: data,
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}
