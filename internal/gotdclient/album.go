package gotdclient

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/mirza/telesync/internal/tgapi"
)

// SendAlbum sends a native Telegram media group. It never falls back to
// individual messages: once preparation starts, any failed item aborts it.
func (m *Manager) SendAlbum(ctx context.Context, ref tgapi.ChannelRef, req tgapi.SendAlbumRequest) (tgapi.SendAlbumResult, error) {
	if len(req.Items) < 2 || len(req.Items) > 10 {
		return tgapi.SendAlbumResult{}, fmt.Errorf("%w: album requires 2-10 items", tgapi.ErrSendUnsupported)
	}
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.SendAlbumResult{}, err
	}
	if ref.IsInvite() {
		return tgapi.SendAlbumResult{}, tgapi.ErrChannelNotFound
	}
	ch, err := m.knownChannel(ctx, ref.ID)
	if err != nil {
		return tgapi.SendAlbumResult{}, classifySendError(err)
	}
	peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}

	ids := make(map[int64]struct{}, len(req.Items))
	group := ""
	for i := range req.Items {
		item := &req.Items[i]
		if item.Media == nil || len(item.Media.Data) == 0 {
			return tgapi.SendAlbumResult{}, fmt.Errorf("%w: album item %d has no media", tgapi.ErrSendUnsupported, i)
		}
		kind := item.Media.Kind
		bucket := kind
		if kind == tgapi.SendMediaPhoto || kind == tgapi.SendMediaVideo || kind == tgapi.SendMediaRoundVideo {
			bucket = "visual"
		}
		if kind != tgapi.SendMediaPhoto && kind != tgapi.SendMediaVideo && kind != tgapi.SendMediaRoundVideo && kind != tgapi.SendMediaDocument {
			return tgapi.SendAlbumResult{}, fmt.Errorf("%w: album media kind %q", tgapi.ErrSendUnsupported, kind)
		}
		if i == 0 {
			group = bucket
		} else if bucket != group {
			return tgapi.SendAlbumResult{}, fmt.Errorf("%w: incompatible album media kinds", tgapi.ErrSendUnsupported)
		}
		if item.RandomID == 0 {
			item.RandomID = newRandomID()
		}
		if _, ok := ids[item.RandomID]; ok {
			return tgapi.SendAlbumResult{}, fmt.Errorf("%w: duplicate album random id", tgapi.ErrSendUnsupported)
		}
		ids[item.RandomID] = struct{}{}
	}

	multi := make([]tg.InputSingleMedia, 0, len(req.Items))
	dropped := 0
	for _, item := range req.Items {
		file, err := uploader.NewUploader(api).FromBytes(ctx, item.Media.Name, item.Media.Data)
		if err != nil {
			return tgapi.SendAlbumResult{}, classifySendError(err)
		}
		upload, err := sendMediaInput(file, item.Media)
		if err != nil {
			return tgapi.SendAlbumResult{}, err
		}
		prepared, err := api.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{Peer: peer, Media: upload})
		if err != nil {
			return tgapi.SendAlbumResult{}, classifySendError(fmt.Errorf("prepare album media: %w", err))
		}
		var media tg.InputMediaClass
		switch v := prepared.(type) {
		case *tg.MessageMediaPhoto:
			photo, ok := v.Photo.(*tg.Photo)
			if !ok {
				return tgapi.SendAlbumResult{}, fmt.Errorf("prepare album media: unexpected photo %T", v.Photo)
			}
			media = &tg.InputMediaPhoto{ID: &tg.InputPhoto{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference}}
		case *tg.MessageMediaDocument:
			doc, ok := v.Document.(*tg.Document)
			if !ok {
				return tgapi.SendAlbumResult{}, fmt.Errorf("prepare album media: unexpected document %T", v.Document)
			}
			media = &tg.InputMediaDocument{ID: &tg.InputDocument{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}}
		default:
			return tgapi.SendAlbumResult{}, fmt.Errorf("prepare album media: unsupported result %T", prepared)
		}
		entities, n := InputEntities(item.Entities)
		dropped += n
		multi = append(multi, tg.InputSingleMedia{Media: media, RandomID: item.RandomID, Message: item.Text, Entities: entities})
	}
	updates, err := api.MessagesSendMultiMedia(ctx, &tg.MessagesSendMultiMediaRequest{Peer: peer, MultiMedia: multi, Silent: req.Silent})
	if err != nil {
		return tgapi.SendAlbumResult{}, classifySendError(err)
	}
	result := tgapi.SendAlbumResult{ChannelID: ch.ID, UnsendableEntities: dropped}
	switch u := updates.(type) {
	case *tg.Updates:
		collectAlbumMessages(u.Updates, &result)
	case *tg.UpdatesCombined:
		collectAlbumMessages(u.Updates, &result)
	}
	return result, nil
}

func collectAlbumMessages(updates []tg.UpdateClass, result *tgapi.SendAlbumResult) {
	for _, update := range updates {
		switch u := update.(type) {
		case *tg.UpdateNewChannelMessage:
			if msg, ok := u.Message.(*tg.Message); ok {
				appendAlbumMessage(msg, result)
			}
		case *tg.UpdateNewMessage:
			if msg, ok := u.Message.(*tg.Message); ok {
				appendAlbumMessage(msg, result)
			}
		}
	}
}

func appendAlbumMessage(msg *tg.Message, result *tgapi.SendAlbumResult) {
	if result.GroupedID == 0 && msg.GroupedID != 0 {
		result.GroupedID = msg.GroupedID
	}
	result.Messages = append(result.Messages, normalizeMessage(msg, nil))
}
