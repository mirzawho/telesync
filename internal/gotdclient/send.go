package gotdclient

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/tgapi"
)

// SendMessage sends one prepared post to a channel this account can reach.
//
// It is the one write to Telegram's content this adapter performs. The message
// it sends is the caller's edited text and attachment: nothing here reads,
// forwards or edits the source message that post was composed from.
//
// The destination is resolved from the account's own view of Telegram and is
// never substituted by another connection: a custom backend that refuses the
// call answers with an error, never with a send through a different client.
func (m *Manager) SendMessage(ctx context.Context, ref tgapi.ChannelRef, req tgapi.SendRequest) (tgapi.SendResult, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.SendResult{}, err
	}
	if ref.IsInvite() {
		// An invite hash names no channel a message could be posted to.
		return tgapi.SendResult{}, tgapi.ErrChannelNotFound
	}
	ch, err := m.knownChannel(ctx, ref.ID)
	if err != nil {
		return tgapi.SendResult{}, classifySendError(err)
	}
	peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}

	entities, dropped := InputEntities(req.Entities)
	randomID := req.RandomID
	if randomID == 0 {
		randomID = newRandomID()
	}

	var (
		updates tg.UpdatesClass
	)
	if req.Media != nil {
		file, err := uploader.NewUploader(api).FromBytes(ctx, req.Media.Name, req.Media.Data)
		if err != nil {
			return tgapi.SendResult{}, classifySendError(err)
		}
		input, err := sendInputMedia(req.Media, file)
		if err != nil {
			return tgapi.SendResult{}, err
		}
		updates, err = api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Peer:     peer,
			Media:    input,
			Message:  req.Text,
			RandomID: randomID,
			Entities: entities,
			Silent:   req.Silent,
		})
	} else {
		updates, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:      peer,
			Message:   req.Text,
			RandomID:  randomID,
			Entities:  entities,
			Silent:    req.Silent,
			NoWebpage: req.NoWebpage,
		})
	}
	if err != nil {
		return tgapi.SendResult{}, classifySendError(err)
	}

	sent := sentMessageOf(updates, req.Text)
	result := tgapi.SendResult{
		ChannelID:          ch.ID,
		UnsendableEntities: dropped,
	}
	if sent != nil {
		normalized := normalizeMessage(sent, nil)
		result.Message = &normalized
	}
	return result, nil
}

// sendInputMedia builds the MTProto media for one outgoing attachment.
//
// Each kind gets the attributes Telegram actually represents it with. There is
// deliberately no generic document constructor standing in for the others: a
// voice recording and an ordinary audio file differ by one boolean on one
// attribute, and both differ from a downloadable file in whether a filename
// attribute is present at all. Sending everything as a document is what turns a
// recording into a file that happens to be audio, so a kind this build cannot
// represent is refused rather than quietly downgraded into one.
func sendInputMedia(m *tgapi.SendMedia, file tg.InputFileClass) (tg.InputMediaClass, error) {
	switch m.Kind {
	case tgapi.SendMediaPhoto:
		return &tg.InputMediaUploadedPhoto{File: file}, nil

	case tgapi.SendMediaVideo:
		return &tg.InputMediaUploadedDocument{
			File:     file,
			MimeType: mimeTypeOr(m, "video/mp4"),
			Attributes: []tg.DocumentAttributeClass{
				// Dimensions are read from the file by the destination; none are
				// claimed here that this build has not measured.
				&tg.DocumentAttributeVideo{Duration: float64(m.Duration)},
				&tg.DocumentAttributeFilename{FileName: sendFileName(m)},
			},
		}, nil

	case tgapi.SendMediaAudio:
		return &tg.InputMediaUploadedDocument{
			File:     file,
			MimeType: mimeTypeOr(m, "audio/mpeg"),
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeAudio{Voice: false, Duration: m.Duration},
				&tg.DocumentAttributeFilename{FileName: sendFileName(m)},
			},
		}, nil

	case tgapi.SendMediaVoice:
		// A Telegram voice message is a document whose documentAttributeAudio
		// carries voice=true, and it has no filename attribute. That absence is
		// not an oversight: a name is what turns a recording into a file a
		// client offers to download instead of play.
		//
		// ForceFile is left unset for the same reason. gotd's own voice builder
		// — telegram/message's Voice(file) — is the reference shape here, and it
		// sets neither: force_file means "plain file, not media", which is
		// exactly the downloadable document this must not become. Only
		// UploadedDocument's File() helper sets it, and that is for a file.
		return &tg.InputMediaUploadedDocument{
			File:     file,
			MimeType: mimeTypeOr(m, "audio/ogg"),
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeAudio{
					Voice:    true,
					Duration: m.Duration,
					Waveform: m.Waveform,
				},
			},
		}, nil

	case tgapi.SendMediaDocument, "":
		return &tg.InputMediaUploadedDocument{
			File:     file,
			MimeType: m.MimeType,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: sendFileName(m)},
			},
			ForceFile: m.Kind == tgapi.SendMediaDocument,
		}, nil
	}
	return nil, fmt.Errorf("%w: media kind %q", tgapi.ErrSendUnsupported, m.Kind)
}

// mimeTypeOr is the content type an attachment is sent under. The caller's own
// type is what is used when it gave one; the fallback only fills a silence, and
// only for kinds where sending no type at all would misdescribe the bytes.
func mimeTypeOr(m *tgapi.SendMedia, fallback string) string {
	if t := strings.TrimSpace(m.MimeType); t != "" {
		return t
	}
	return fallback
}

// sendFileName gives the upload a file name Telegram can show. A blank name is
// replaced rather than sent as an empty filename.
func sendFileName(m *tgapi.SendMedia) string {
	if m.Name != "" {
		return m.Name
	}
	if m.MimeType == "image/jpeg" {
		return "photo.jpg"
	}
	return "attachment"
}

// newRandomID returns a non-zero client message id.
func newRandomID() int64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 1
	}
	id := int64(binary.LittleEndian.Uint64(buf[:]))
	if id == 0 {
		return 1
	}
	return id
}

// sentMessageOf finds the message a send actually produced.
//
// Telegram answers a send with an updates container; the message it confirms
// is in there as an update. When the backend answers with only the sent
// message's own id and date, that is built into the same shape. When it
// returns nothing that names one at all, the result reports the send as
// accepted without a message to show, rather than inventing an id or a date.
func sentMessageOf(updates tg.UpdatesClass, text string) *tg.Message {
	switch u := updates.(type) {
	case *tg.Updates:
		return messageIn(u.Updates)
	case *tg.UpdatesCombined:
		return messageIn(u.Updates)
	case *tg.UpdateShortSentMessage:
		return &tg.Message{ID: u.ID, Date: u.Date, Message: text}
	}
	return nil
}

// messageIn scans an updates list for the message a send produced.
func messageIn(updates []tg.UpdateClass) *tg.Message {
	for _, u := range updates {
		switch e := u.(type) {
		case *tg.UpdateNewChannelMessage:
			if msg, ok := e.Message.(*tg.Message); ok {
				return msg
			}
		case *tg.UpdateNewMessage:
			if msg, ok := e.Message.(*tg.Message); ok {
				return msg
			}
		}
	}
	return nil
}

// classifySendError maps a Telegram refusal onto the adapter's own errors, so
// the worker can answer with a code without parsing RPC strings.
func classifySendError(err error) error {
	if err == nil {
		return nil
	}
	if tgerr.IsCode(err, 401) {
		return errors.Join(tgapi.ErrNotAuthorized, err)
	}
	if tgerr.Is(err,
		"CHAT_FORBIDDEN", "CHANNEL_PRIVATE", "SEND_MESSAGE_FORBIDDEN",
		"USER_BANNED_IN_CHANNEL", "CHAT_WRITE_FORBIDDEN", "CHANNEL_PUBLIC_GROUP_NA",
		"USER_NOT_PARTICIPANT") {
		return errors.Join(tgapi.ErrSendForbidden, err)
	}
	if tgerr.Is(err,
		"MESSAGE_TOO_LONG", "TEXT_TOO_LONG", "CAPTION_TOO_LONG",
		"ENTITIES_TOO_LONG") {
		return errors.Join(tgapi.ErrMessageTooLong, err)
	}
	if tgerr.Is(err,
		"MEDIA_INVALID", "PHOTO_INVALID", "PHOTO_INVALID_DIMENSIONS",
		"DOCUMENT_INVALID", "STICKER_MIME_INVALID", "UPLOAD_NO_VOLUME",
		"FILE_PARTS_INVALID", "FILE_PART_X_INVALID") {
		return errors.Join(tgapi.ErrSendUnsupported, err)
	}
	return ClassifyError(err)
}
