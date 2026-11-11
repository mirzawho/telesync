package gotdclient

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"strings"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/tgapi"
)

// ErrChannelUsernameNotAssigned means channels.updateUsername returned false.
var ErrChannelUsernameNotAssigned = errors.New("gotdclient: Telegram did not assign the channel username")

// ErrChannelCreateRequiresHumanAccount means this connection has no confirmed
// human-user Self profile, which is required before creating a channel.
var ErrChannelCreateRequiresHumanAccount = errors.New("gotdclient: channel creation requires a confirmed human Telegram account")

var supportedChannelAvatarMIMEs = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
	"image/gif":  {},
}

// CreateBroadcastChannel creates a Telegram broadcast channel through this
// manager's live, selected connection. It verifies a human account using Self
// on that same client before issuing the non-idempotent create RPC.
func (m *Manager) CreateBroadcastChannel(ctx context.Context, title, description string) (tgapi.ChannelInfo, error) {
	client, api, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.ChannelInfo{}, err
	}
	self, err := client.Self(ctx)
	if err != nil {
		return tgapi.ChannelInfo{}, errors.Join(ErrChannelCreateRequiresHumanAccount, classifyChannelCreateError(err))
	}
	if self == nil || self.ID <= 0 || self.Bot {
		return tgapi.ChannelInfo{}, ErrChannelCreateRequiresHumanAccount
	}
	updates, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
		Broadcast: true,
		Megagroup: false,
		Title:     title,
		About:     description,
	})
	if err != nil {
		return tgapi.ChannelInfo{}, classifyChannelCreateError(err)
	}
	ch, ok := channelFromCreateUpdates(updates)
	if !ok {
		return tgapi.ChannelInfo{}, tgapi.ErrChannelNotFound
	}
	m.remember(ch)
	return channelInfoFromChannel(ch, membershipOf(ch)), nil
}

// SetBroadcastChannelUsername assigns a username to the exact channel ID
// created/resolved by this manager. It never resolves the name or substitutes a
// different peer.
func (m *Manager) SetBroadcastChannelUsername(ctx context.Context, channelID int64, username string) error {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return err
	}
	ch, err := m.knownChannel(ctx, channelID)
	if err != nil {
		return err
	}
	if !ch.Broadcast || ch.Megagroup {
		return tgapi.ErrChannelNotFound
	}
	assigned, err := api.ChannelsUpdateUsername(ctx, &tg.ChannelsUpdateUsernameRequest{
		Channel:  &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		Username: username,
	})
	if err != nil {
		return classifyChannelCreateError(err)
	}
	if !assigned {
		return ErrChannelUsernameNotAssigned
	}
	ch.Username = username
	m.remember(ch)
	return nil
}

// SetBroadcastChannelAvatar uploads and applies an image to the exact
// broadcast channel. It returns the PhotoID Telegram reported in the edit
// result, or zero when Telegram did not return one.
func (m *Manager) SetBroadcastChannelAvatar(ctx context.Context, channelID int64, name, mimeType string, data []byte) (int64, error) {
	if len(data) == 0 {
		return 0, errors.New("gotdclient: channel avatar is empty")
	}
	if len(data) > maxSmallFileBytes {
		return 0, fmt.Errorf("gotdclient: channel avatar exceeds %d bytes", maxSmallFileBytes)
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(mimeType))
	if err != nil {
		return 0, fmt.Errorf("gotdclient: invalid channel avatar MIME type: %w", err)
	}
	if _, ok := supportedChannelAvatarMIMEs[strings.ToLower(mediaType)]; !ok {
		return 0, fmt.Errorf("gotdclient: unsupported channel avatar MIME type %q", mediaType)
	}
	if strings.TrimSpace(name) == "" {
		return 0, errors.New("gotdclient: channel avatar filename is empty")
	}
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return 0, err
	}
	ch, err := m.knownChannel(ctx, channelID)
	if err != nil {
		return 0, classifyChannelCreateError(err)
	}
	if !ch.Broadcast || ch.Megagroup {
		return 0, tgapi.ErrChannelNotFound
	}
	file, err := uploader.NewUploader(api).FromBytes(ctx, name, data)
	if err != nil {
		return 0, classifyChannelCreateError(err)
	}
	updates, err := api.ChannelsEditPhoto(ctx, &tg.ChannelsEditPhotoRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		Photo:   &tg.InputChatUploadedPhoto{File: file},
	})
	if err != nil {
		return 0, classifyChannelCreateError(err)
	}
	updated, ok := channelFromCreateUpdates(updates)
	if !ok || updated.ID != ch.ID {
		return 0, nil
	}
	m.remember(updated)
	return chatPhotoID(updated), nil
}

// channelFromCreateUpdates selects the broadcast channel from the complete
// update result rather than assuming the channel is the first chat/update.
func channelFromCreateUpdates(result tg.UpdatesClass) (*tg.Channel, bool) {
	var chats []tg.ChatClass
	switch updates := result.(type) {
	case *tg.Updates:
		chats = updates.Chats
	case *tg.UpdatesCombined:
		chats = updates.Chats
	default:
		return nil, false
	}
	var found *tg.Channel
	for _, chat := range chats {
		ch, ok := chat.(*tg.Channel)
		if !ok || ch.Min || ch.ID <= 0 || ch.AccessHash == 0 || !ch.Broadcast || ch.Megagroup {
			continue
		}
		if found != nil && found.ID != ch.ID {
			// Ambiguous result: do not guess which remote object was created.
			return nil, false
		}
		found = ch
	}
	return found, found != nil
}

// classifyChannelCreateError preserves unrecognized Telegram errors verbatim
// while making the expected creation/username refusals distinguishable.
func classifyChannelCreateError(err error) error {
	if err == nil {
		return nil
	}
	if tgerr.IsCode(err, 401) {
		return errors.Join(tgapi.ErrNotAuthorized, err)
	}
	if fw, ok := tgerr.AsFloodWait(err); ok {
		return tgapi.FloodWait{Seconds: int(fw.Seconds()), Op: "channel creation or username assignment"}
	}
	if tgerr.Is(err, "CHANNELS_TOO_MUCH", "USER_CHANNELS_TOO_MUCH", "CHANNELS_ADMIN_PUBLIC_TOO_MUCH") {
		return fmt.Errorf("Telegram channel creation limit: %w", err)
	}
	if tgerr.Is(err, "USERNAME_OCCUPIED", "USERNAME_INVALID", "USERNAME_PURCHASE_AVAILABLE", "CHANNELS_ADMIN_PUBLIC_TOO_MUCH") {
		return fmt.Errorf("Telegram rejected the channel username: %w", err)
	}
	if tgerr.Is(err, "CHAT_TITLE_EMPTY", "CHAT_TITLE_TOO_LONG", "ABOUT_TOO_LONG") {
		return fmt.Errorf("Telegram rejected channel metadata: %w", err)
	}
	return err
}
