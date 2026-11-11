package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// EditMessage implements rpc.Backend. The target is read on this worker's own
// Telegram connection before the edit, then the adapter returns a readback of
// the same message after the native edit attempt.
func (s *Service) EditMessage(ctx context.Context, req rpc.EditRequest) (rpc.EditResult, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.EditResult{}, err
	}
	if req.ChannelID <= 0 || req.MessageID <= 0 || req.MessageID > tgapi.MaxMessageID {
		return rpc.EditResult{}, rpc.NewError(rpc.CodeInvalidInput,
			"channel_id and message_id must be within their supported positive ranges.", 0)
	}
	if req.Text == "" && req.Media == nil && !req.KeepMedia {
		return rpc.EditResult{}, rpc.NewError(rpc.CodeInvalidInput,
			"the replacement must include text or a new attachment.", 0)
	}
	if req.KeepMedia && req.Media != nil {
		return rpc.EditResult{}, rpc.NewError(rpc.CodeInvalidInput,
			"existing media cannot be kept while replacing media.", 0)
	}
	limit, label := tgapi.MaxMessageRunes, "message text"
	if req.Media != nil {
		limit, label = tgapi.MaxCaptionRunes, "caption"
		if len(req.Media.Data) == 0 {
			return rpc.EditResult{}, rpc.NewError(rpc.CodeInvalidInput, "the replacement attachment is empty.", 0)
		}
		if len(req.Media.Data) > rpc.MaxSendMediaBytes {
			return rpc.EditResult{}, rpc.ErrMediaTooLarge
		}
		if !editMediaKind(req.Media.Kind) {
			return rpc.EditResult{}, rpc.NewError(rpc.CodeMessageEditUnsupported,
				"The requested attachment type is not supported for edits.", 0)
		}
	}
	if runes := len([]rune(req.Text)); runes > limit {
		return rpc.EditResult{}, rpc.NewError(rpc.CodeMessageTooLong,
			fmt.Sprintf("The %s is %d characters; the limit is %d.", label, runes, limit), 0)
	}

	channel, ok := s.channelByID(ctx, req.ChannelID)
	if !ok {
		return rpc.EditResult{}, rpc.ErrChannelNotFound
	}
	target, err := s.tg.Message(ctx, channel, req.MessageID)
	if err != nil {
		if errors.Is(err, tgapi.ErrMessageNotFound) {
			return rpc.EditResult{}, rpc.NewError(rpc.CodeNotFound, "No message with that id exists in this channel.", 0)
		}
		return rpc.EditResult{}, s.mapEditError(err)
	}
	status := s.tg.Status(ctx)
	isAuthor := status.User != nil && target.Sender != nil && target.Sender.ID == status.User.ID
	if !isAuthor && !channel.Permissions.Edit {
		return rpc.EditResult{}, rpc.ErrMessageEditForbidden
	}

	media := sendMediaFromWire(req.Media)
	updated, err := s.tg.EditMessage(ctx, tgapi.ChannelRef{ID: req.ChannelID}, tgapi.EditRequest{
		MessageID: req.MessageID,
		Text:      req.Text,
		Entities:  entitiesFromWire(req.Entities),
		Media:     media,
		KeepMedia: req.KeepMedia,
		NoWebpage: req.NoWebpage,
	})
	if err != nil {
		return rpc.EditResult{}, s.mapEditError(err)
	}
	if updated.Message == nil {
		return rpc.EditResult{}, rpc.NewError(rpc.CodeMessageEditOutcomeUnknown,
			"Telegram accepted the edit but the target message could not be read back.", 0)
	}
	if updated.Message.ID != req.MessageID {
		return rpc.EditResult{}, rpc.NewError(rpc.CodeMessageEditOutcomeUnknown,
			"The message returned after editing did not match the requested target.", 0)
	}
	out := s.messageDTO(req.ChannelID, *updated.Message)
	return rpc.EditResult{Message: &out}, nil
}

func editMediaKind(kind string) bool {
	switch kind {
	case tgapi.SendMediaPhoto, tgapi.SendMediaVideo, tgapi.SendMediaAudio,
		tgapi.SendMediaVoice, tgapi.SendMediaDocument:
		return true
	default:
		return false
	}
}

func (s *Service) mapEditError(err error) error {
	switch {
	case errors.Is(err, tgapi.ErrMessageEditForbidden):
		return rpc.ErrMessageEditForbidden
	case errors.Is(err, tgapi.ErrMessageEditTimeExpired):
		return rpc.ErrMessageEditTimeExpired
	case errors.Is(err, tgapi.ErrMessageEditUnsupported):
		return rpc.ErrMessageEditUnsupported
	case errors.Is(err, tgapi.ErrEditMediaRemovalUnsupported):
		return rpc.ErrEditMediaRemovalUnsupported
	case errors.Is(err, tgapi.ErrMessageEditOutcomeUnknown):
		return rpc.ErrMessageEditOutcomeUnknown
	case errors.Is(err, tgapi.ErrMessageNotFound):
		return rpc.NewError(rpc.CodeNotFound, "No message with that id exists in this channel.", 0)
	case errors.Is(err, tgapi.ErrMessageTooLong):
		return rpc.ErrMessageTooLong
	}
	var flood interface{ FloodSeconds() int }
	if errors.As(err, &flood) {
		return rpc.NewError(rpc.CodeRateLimited, "Telegram is rate limiting this client.", flood.FloodSeconds())
	}
	return s.mapDiscoveryError(err)
}
