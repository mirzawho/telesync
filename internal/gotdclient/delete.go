package gotdclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/tgapi"
)

// DeleteMessage deletes exactly one post from the supplied, already resolved
// channel on this Manager's Telegram connection.
func (m *Manager) DeleteMessage(ctx context.Context, channel tgapi.Channel, messageID int) error {
	if channel.ID <= 0 || channel.AccessHash == 0 {
		return tgapi.ErrChannelNotFound
	}
	if messageID <= 0 || messageID > tgapi.MaxMessageID {
		return fmt.Errorf("message id must be between 1 and %d", tgapi.MaxMessageID)
	}
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return err
	}
	result, err := api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash},
		ID:      []int{messageID},
	})
	if err != nil {
		return classifyDeleteError(err)
	}
	if result == nil {
		return errors.New("channels.deleteMessages returned no result")
	}
	return nil
}

func classifyDeleteError(err error) error {
	switch {
	case tgerr.Is(err, "CHAT_ADMIN_REQUIRED", "MESSAGE_DELETE_FORBIDDEN"):
		return errors.Join(tgapi.ErrMessageDeleteForbidden, err)
	case tgerr.Is(err, "MSG_ID_INVALID", "MESSAGE_ID_INVALID"):
		return errors.Join(tgapi.ErrMessageNotFound, err)
	default:
		return ClassifyError(err)
	}
}
