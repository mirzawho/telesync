package gotdclient

import (
	"context"
	"errors"
	"reflect"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/tgapi"
)

// EditMessage replaces one existing message through Telegram's native
// messages.editMessage method. The final response is read back from the same
// channel and message ID; there is no send/delete fallback.
func (m *Manager) EditMessage(ctx context.Context, ref tgapi.ChannelRef, req tgapi.EditRequest) (tgapi.EditResult, error) {
	if req.MessageID <= 0 {
		return tgapi.EditResult{}, tgapi.ErrMessageNotFound
	}
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.EditResult{}, err
	}
	if ref.IsInvite() {
		return tgapi.EditResult{}, tgapi.ErrChannelNotFound
	}
	ch, err := m.knownChannel(ctx, ref.ID)
	if err != nil {
		return tgapi.EditResult{}, classifyEditError(err)
	}
	peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	entities, dropped := InputEntities(req.Entities)
	if dropped != 0 {
		return tgapi.EditResult{}, tgapi.ErrMessageEditUnsupported
	}

	var media tg.InputMediaClass
	if !req.KeepMedia {
		media = &tg.InputMediaEmpty{}
	}
	if req.Media != nil {
		if req.KeepMedia {
			return tgapi.EditResult{}, tgapi.ErrMessageEditUnsupported
		}
		file, err := uploader.NewUploader(api).FromBytes(ctx, req.Media.Name, req.Media.Data)
		if err != nil {
			return tgapi.EditResult{}, classifyEditError(err)
		}
		media, err = sendMediaInput(file, req.Media)
		if err != nil {
			return tgapi.EditResult{}, classifyEditError(err)
		}
	}

	_, editErr := api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer:      peer,
		ID:        req.MessageID,
		Message:   req.Text,
		Media:     media,
		Entities:  entities,
		NoWebpage: req.NoWebpage,
	})
	notModified := tgerr.Is(editErr, "MESSAGE_NOT_MODIFIED")
	if editErr != nil && !notModified {
		if req.Media == nil && !req.KeepMedia && tgerr.Is(editErr, "MEDIA_EMPTY", "MEDIA_INVALID", "MEDIA_PREV_INVALID") {
			return tgapi.EditResult{}, errors.Join(tgapi.ErrEditMediaRemovalUnsupported, editErr)
		}
		return tgapi.EditResult{}, classifyEditError(editErr)
	}

	// Updates may omit the edited message or carry only a short update. Read the
	// target back through this manager's own authenticated client so success is
	// about the original post, not merely an accepted RPC.
	updated, err := m.Message(ctx, tgapi.Channel{ID: ch.ID, AccessHash: ch.AccessHash}, req.MessageID)
	if err != nil {
		if errors.Is(err, tgapi.ErrMessageNotFound) {
			return tgapi.EditResult{}, tgapi.ErrMessageEditOutcomeUnknown
		}
		return tgapi.EditResult{}, tgapi.ErrMessageEditOutcomeUnknown
	}
	if updated.ID != req.MessageID {
		return tgapi.EditResult{}, tgapi.ErrMessageEditOutcomeUnknown
	}
	if updated.Text != req.Text || !sameEntities(updated.Entities, req.Entities) {
		return tgapi.EditResult{}, tgapi.ErrMessageEditOutcomeUnknown
	}
	if req.KeepMedia {
		// The absent media flag means retain the existing media. The worker's
		// readback verifies it remained unchanged.
	} else if req.Media == nil {
		if updated.Media != nil {
			return tgapi.EditResult{}, tgapi.ErrEditMediaRemovalUnsupported
		}
	} else {
		if updated.Media == nil || updated.Media.Type != req.Media.Kind {
			return tgapi.EditResult{}, tgapi.ErrMessageEditUnsupported
		}
		// MESSAGE_NOT_MODIFIED cannot establish that an uploaded replacement has
		// the requested bytes: the old attachment may share its media kind. Keep
		// that ambiguous result terminal instead of claiming a full replacement.
		if notModified {
			return tgapi.EditResult{}, tgapi.ErrMessageEditOutcomeUnknown
		}
	}
	return tgapi.EditResult{Message: &updated}, nil
}

func classifyEditError(err error) error {
	if err == nil {
		return nil
	}
	if tgerr.Is(err, "MESSAGE_AUTHOR_REQUIRED", "CHAT_ADMIN_REQUIRED", "CHAT_WRITE_FORBIDDEN", "USER_BANNED_IN_CHANNEL") {
		return errors.Join(tgapi.ErrMessageEditForbidden, err)
	}
	if tgerr.Is(err, "MESSAGE_EDIT_TIME_EXPIRED", "EDIT_MESSAGE_EXPIRED") {
		return errors.Join(tgapi.ErrMessageEditTimeExpired, err)
	}
	if tgerr.Is(err, "MESSAGE_ID_INVALID", "MSG_ID_INVALID") {
		return errors.Join(tgapi.ErrMessageNotFound, err)
	}
	if tgerr.Is(err,
		"MEDIA_GROUPED_INVALID", "MEDIA_EMPTY", "MEDIA_INVALID", "MEDIA_NEW_INVALID", "MEDIA_PREV_INVALID",
		"PHOTO_INVALID", "PHOTO_INVALID_DIMENSIONS", "DOCUMENT_INVALID", "FILE_PARTS_INVALID") {
		return errors.Join(tgapi.ErrMessageEditUnsupported, err)
	}
	if tgerr.Is(err, "MEDIA_CAPTION_TOO_LONG", "MESSAGE_TOO_LONG", "TEXT_TOO_LONG") {
		return errors.Join(tgapi.ErrMessageTooLong, err)
	}
	if errors.Is(err, tgapi.ErrSendUnsupported) {
		return errors.Join(tgapi.ErrMessageEditUnsupported, err)
	}
	return ClassifyError(err)
}

func sameEntities(a, b []tgapi.Entity) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}
