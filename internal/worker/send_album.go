package worker

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

type telegramAlbumSender interface {
	SendAlbum(context.Context, tgapi.ChannelRef, tgapi.SendAlbumRequest) (tgapi.SendAlbumResult, error)
}

// SendAlbum implements the optional rpc.AlbumBackend capability.
func (s *Service) SendAlbum(ctx context.Context, req rpc.SendAlbumRequest) (rpc.SendAlbumResult, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.SendAlbumResult{}, err
	}
	if len(req.Items) < 2 || len(req.Items) > 10 {
		return rpc.SendAlbumResult{}, rpc.NewError(rpc.CodeInvalidInput, "an album needs 2-10 items", 0)
	}
	for i, item := range req.Items {
		if item.Media == nil || len(item.Media.Data) == 0 {
			return rpc.SendAlbumResult{}, rpc.NewError(rpc.CodeInvalidInput, fmt.Sprintf("album item %d has no media", i), 0)
		}
		if len([]rune(item.Text)) > tgapi.MaxCaptionRunes {
			return rpc.SendAlbumResult{}, rpc.NewError(rpc.CodeMessageTooLong, fmt.Sprintf("album item %d caption is too long", i), 0)
		}
	}
	sender, ok := s.tg.(telegramAlbumSender)
	if !ok {
		return rpc.SendAlbumResult{}, rpc.NewError(rpc.CodeSendUnsupported, "this connection does not support native albums", 0)
	}
	in := tgapi.SendAlbumRequest{Silent: req.Silent, Items: make([]tgapi.SendAlbumItem, 0, len(req.Items))}
	for _, item := range req.Items {
		in.Items = append(in.Items, tgapi.SendAlbumItem{
			Media: sendMediaFromWire(item.Media), Text: item.Text,
			Entities: entitiesFromWire(item.Entities), RandomID: item.RandomID,
		})
	}
	out, err := sender.SendAlbum(ctx, tgapi.ChannelRef{ID: req.ChannelID}, in)
	if err != nil {
		return rpc.SendAlbumResult{}, s.mapSendError(err)
	}
	result := rpc.SendAlbumResult{
		ChannelID:          strconv.FormatInt(out.ChannelID, 10),
		UnsendableEntities: out.UnsendableEntities,
		Messages:           make([]rpc.Message, 0, len(out.Messages)),
	}
	if out.GroupedID != 0 {
		result.GroupedID = strconv.FormatInt(out.GroupedID, 10)
	}
	for _, msg := range out.Messages {
		result.Messages = append(result.Messages, s.messageDTO(out.ChannelID, msg))
	}
	return result, nil
}
