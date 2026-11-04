package gotdclient

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/tgapi"
)

// Message returns exactly one post.
//
// Channel posts require the channel-scoped lookup (channels.getMessages); the
// non-channel messages.getMessages cannot see them.
func (m *Manager) Message(ctx context.Context, channel tgapi.Channel, messageID int) (tgapi.Message, error) {
	msg, err := m.channelMessage(ctx, channel, messageID)
	if err != nil {
		return tgapi.Message{}, err
	}
	return normalizeMessage(msg, nil), nil
}

// channelMessage fetches one post and reports a missing one distinctly from a
// failed request.
func (m *Manager) channelMessage(ctx context.Context, channel tgapi.Channel, messageID int) (*tg.Message, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return nil, err
	}
	if messageID <= 0 {
		return nil, fmt.Errorf("message id must be positive")
	}

	msgs, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return nil, ClassifyError(err)
	}
	msg := findMessage(msgs, messageID)
	if msg == nil {
		return nil, tgapi.ErrMessageNotFound
	}
	return msg, nil
}

// normalizeMessage converts one post into the library-independent shape.
func normalizeMessage(msg *tg.Message, users map[int64]*tg.User) tgapi.Message {
	out := tgapi.Message{
		ID:               msg.ID,
		Date:             MessageDate(msg.Date),
		Text:             msg.Message,
		Sender:           SenderOf(msg, users),
		ReplyToMessageID: ReplyToMessageID(msg),
		Views:            Views(msg),
		Reactions:        Reactions(msg),
		Entities:         Entities(msg),
	}
	if kind := MediaType(msg.Media); kind != "" {
		out.Media = &tgapi.Media{
			Type:     kind,
			Label:    MediaLabel(kind),
			Filename: MediaFileName(msg.Media),
			MimeType: MediaMimeType(msg.Media),
			Size:     MediaSize(msg.Media),
		}
	}
	return out
}

// findMessage picks the message by id out of a getMessages result.
func findMessage(msgs tg.MessagesMessagesClass, id int) *tg.Message {
	var list []tg.MessageClass
	switch v := msgs.(type) {
	case *tg.MessagesMessages:
		list = v.Messages
	case *tg.MessagesMessagesSlice:
		list = v.Messages
	case *tg.MessagesChannelMessages:
		list = v.Messages
	default:
		return nil
	}
	for _, mc := range list {
		if msg, ok := mc.(*tg.Message); ok && msg.ID == id {
			return msg
		}
	}
	return nil
}

// mediaLocation returns a download location and content type for one attachment.
func mediaLocation(media tg.MessageMediaClass) (tg.InputFileLocationClass, string, error) {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok {
			return nil, "", tgapi.ErrMediaNotFound
		}
		thumb, ok := largestPhotoSize(photo.Sizes)
		if !ok {
			return nil, "", tgapi.ErrMediaNotFound
		}
		loc := &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     thumb,
		}
		// Telegram does not name a MIME type for photos; the bytes are JPEG for
		// every size that has byte data.
		return loc, "image/jpeg", nil

	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok {
			return nil, "", tgapi.ErrMediaNotFound
		}
		loc := &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
		}
		mime := doc.MimeType
		if mime == "" {
			mime = "application/octet-stream"
		}
		return loc, mime, nil

	default:
		return nil, "", tgapi.ErrMediaNotFound
	}
}

// largestPhotoSize returns the thumbnail type of the biggest PhotoSize that
// carries dimensions, which is what InputPhotoFileLocation.ThumbSize wants.
func largestPhotoSize(sizes []tg.PhotoSizeClass) (string, bool) {
	kind, _, ok := largestPhoto(sizes)
	return kind, ok
}

// dialogsPageSize is Telegram's maximum for messages.getDialogs.
const dialogsPageSize = 100

// maxDialogPages bounds a full dialog walk so a pathological account cannot
// keep the worker in an endless loop.
const maxDialogPages = 50

// Dialogs returns every channel and supergroup the account is a member of.
//
// Only chats Telegram reports as channels/megagroups are returned; private
// chats and small groups are outside the API's scope.
func (m *Manager) Dialogs(ctx context.Context) ([]tgapi.Channel, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return nil, err
	}

	var (
		out        []tgapi.Channel
		offsetID   int
		offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
	)
	seen := map[int64]bool{}

	for page := 0; page < maxDialogPages; page++ {
		res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: 0,
			OffsetID:   offsetID,
			OffsetPeer: offsetPeer,
			Limit:      dialogsPageSize,
			Hash:       0,
		})
		if err != nil {
			return nil, ClassifyError(err)
		}

		dialogs, chats, _, total, err := splitDialogs(res)
		if err != nil {
			return nil, err
		}

		chatByID := indexChats(chats)
		var lastID int
		var lastPeer tg.PeerClass

		for _, d := range dialogs {
			dlg, ok := d.(*tg.Dialog)
			if !ok {
				continue
			}
			peer, ok := dlg.Peer.(*tg.PeerChannel)
			if !ok {
				continue
			}
			ch, ok := chatByID[peer.ChannelID]
			if !ok || ch.Min {
				continue
			}
			if seen[ch.ID] {
				continue
			}
			seen[ch.ID] = true
			unread := UnreadFromDialog(dlg)
			out = append(out, tgapi.Channel{
				ID:             ch.ID,
				AccessHash:     ch.AccessHash,
				Title:          ch.Title,
				Username:       ch.Username,
				Kind:           ChannelKind(ch),
				HasPhoto:       hasChatPhoto(ch),
				PhotoID:        chatPhotoID(ch),
				UnreadCount:    unread.Count,
				UnreadMentions: unread.Mentions,
				LastMessageID:  dlg.TopMessage,
				Permissions:    tgapi.NormalizePermissions(channelAccess(ch, tgapi.MembershipMember)),
			})
			lastID = dlg.TopMessage
			lastPeer = dlg.Peer
		}

		if len(dialogs) < dialogsPageSize || len(out) >= total {
			break
		}
		if lastID == 0 || lastPeer == nil {
			break
		}
		offsetID = lastID
		offsetPeer = toInputPeer(lastPeer, chatByID)
		if offsetPeer == nil {
			break
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

func splitDialogs(res tg.MessagesDialogsClass) (dialogs []tg.DialogClass, chats []tg.ChatClass, users []tg.UserClass, total int, err error) {
	switch v := res.(type) {
	case *tg.MessagesDialogs:
		return v.Dialogs, v.Chats, v.Users, len(v.Dialogs), nil
	case *tg.MessagesDialogsSlice:
		return v.Dialogs, v.Chats, v.Users, v.Count, nil
	case *tg.MessagesDialogsNotModified:
		return nil, nil, nil, 0, nil
	default:
		return nil, nil, nil, 0, fmt.Errorf("unexpected getDialogs result %T", res)
	}
}

func indexChats(chats []tg.ChatClass) map[int64]*tg.Channel {
	out := make(map[int64]*tg.Channel, len(chats))
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok {
			out[ch.ID] = ch
		}
	}
	return out
}

func hasChatPhoto(c *tg.Channel) bool {
	switch c.Photo.(type) {
	case *tg.ChatPhoto:
		return true
	default:
		return false
	}
}

func chatPhotoID(c *tg.Channel) int64 {
	if p, ok := c.Photo.(*tg.ChatPhoto); ok {
		return p.PhotoID
	}
	return 0
}

func toInputPeer(p tg.PeerClass, chats map[int64]*tg.Channel) tg.InputPeerClass {
	switch v := p.(type) {
	case *tg.PeerChannel:
		ch, ok := chats[v.ChannelID]
		if !ok {
			return nil
		}
		return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	default:
		return nil
	}
}

// History returns messages older than beforeID (0 = newest).
//
// It never marks anything as read: getHistory has no read side effect.
func (m *Manager) History(ctx context.Context, channel tgapi.Channel, beforeID, limit int) (tgapi.HistoryPage, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.HistoryPage{}, err
	}
	limit = HistoryLimit(limit)
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:       &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash},
		OffsetID:   HistoryOffset(beforeID),
		OffsetDate: 0,
		AddOffset:  0,
		Limit:      limit,
		MaxID:      0,
		MinID:      0,
		Hash:       0,
	})
	if err != nil {
		return tgapi.HistoryPage{}, ClassifyError(err)
	}

	var (
		msgs  []tg.MessageClass
		users []tg.UserClass
	)
	switch v := res.(type) {
	case *tg.MessagesChannelMessages:
		msgs, users = v.Messages, v.Users
	case *tg.MessagesMessages:
		msgs, users = v.Messages, v.Users
	case *tg.MessagesMessagesSlice:
		msgs, users = v.Messages, v.Users
	case *tg.MessagesMessagesNotModified:
		return tgapi.HistoryPage{}, nil
	default:
		return tgapi.HistoryPage{}, fmt.Errorf("unexpected getHistory result %T", res)
	}

	userByID := make(map[int64]*tg.User, len(users))
	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			userByID[user.ID] = user
		}
	}

	page := tgapi.HistoryPage{HasMore: HasMore(len(msgs), limit)}
	for _, mc := range msgs {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		page.Messages = append(page.Messages, normalizeMessage(msg, userByID))
	}
	return page, nil
}
