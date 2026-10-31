package gotdclient_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"

	"github.com/mirza/telesync/internal/tgapi"
)

const (
	// The custom emoji the scripted reaction set carries.
	emojiDoc = int64(5368324170671202286)
	// An id in no set: Telegram recycles documents, so this is a real
	// possibility and must not be an error.
	emojiDocGone = int64(6000000000000000002)
)

var emojiBytes = []byte("custom-emoji-webp-bytes")

// customEmojiServer answers messages.getStickerSet for Telegram's reaction
// collection (inputStickerSetEmojiGenericAnimations) and serves the one
// document's bytes from upload.getFile. calls counts the set lookups so a test
// can prove the answer is cached. An empty set answers as if Telegram did not
// serve it.
func customEmojiServer(t *testing.T, calls *atomic.Int32) func(*tgtest.Dispatcher) {
	t.Helper()
	return func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.MessagesGetStickerSetRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			calls.Add(1)
			var request tg.MessagesGetStickerSetRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			if _, ok := request.Stickerset.(*tg.InputStickerSetEmojiGenericAnimations); !ok {
				return server.SendErr(req, tgerr.New(400, "STICKERSET_INVALID"))
			}
			return server.SendResult(req, &tg.MessagesStickerSet{
				Set: tg.StickerSet{
					ID: 1, AccessHash: 2, Emojis: true,
					Title: "reactions", ShortName: "reactions", Count: 1, Hash: 3,
				},
				Packs: []tg.StickerPack{},
				Documents: []tg.DocumentClass{&tg.Document{
					ID:            emojiDoc,
					AccessHash:    7000,
					FileReference: []byte{4, 5, 6},
					MimeType:      "application/x-tgsticker",
					Size:          12,
					Attributes: []tg.DocumentAttributeClass{
						&tg.DocumentAttributeCustomEmoji{Alt: "🙂", Stickerset: &tg.InputStickerSetEmpty{}},
					},
				}},
			})
		})
		d.HandleFunc(tg.UploadGetFileRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.UploadGetFileRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			loc, ok := request.Location.(*tg.InputDocumentFileLocation)
			if !ok {
				return server.SendErr(req, tgerr.New(400, "expected a document location"))
			}
			if loc.ID != emojiDoc || loc.AccessHash != 7000 || string(loc.FileReference) != "\x04\x05\x06" {
				return server.SendErr(req, tgerr.New(400, "wrong document id, access hash or file reference"))
			}
			start := request.Offset
			if start >= int64(len(emojiBytes)) {
				return server.SendResult(req, &tg.UploadFile{Type: &tg.StorageFileUnknown{}})
			}
			end := start + int64(request.Limit)
			if end > int64(len(emojiBytes)) {
				end = int64(len(emojiBytes))
			}
			return server.SendResult(req, &tg.UploadFile{
				Type:  &tg.StorageFileUnknown{},
				Bytes: emojiBytes[start:end],
			})
		})
	}
}

func TestDownloadCustomEmojiReturnsItsBytes(t *testing.T) {
	var calls atomic.Int32
	l, key := startPanelServer(t, &tg.User{ID: 1}, &panelServer{}, customEmojiServer(t, &calls))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Prime the sticker-set cache through the not-found path, so the measured
	// lookups below are three downloads against one cached set — including
	// the not-found documents, which must not each ask Telegram again.
	if _, err := m.DownloadCustomEmoji(ctx, emojiDocGone); err != tgapi.ErrCustomEmojiNotFound {
		t.Fatalf("warm-up err = %v", err)
	}
	calls.Store(0)
	if _, err := m.DownloadCustomEmoji(ctx, emojiDocGone); err != tgapi.ErrCustomEmojiNotFound {
		t.Fatalf("second not-found err = %v", err)
	}

	for i := 0; i < 3; i++ {
		file, err := m.DownloadCustomEmoji(ctx, emojiDoc)
		if err != nil {
			t.Fatalf("DownloadCustomEmoji: %v", err)
		}
		if string(file.Data) != string(emojiBytes) {
			t.Fatalf("bytes = %q", file.Data)
		}
		if file.MimeType != "application/x-tgsticker" {
			t.Fatalf("mime = %q", file.MimeType)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("sticker set lookups after the warm-up = %d, want 0 (every download used the cache)", got)
	}
}

func TestDownloadCustomEmojiFetchesTheSetOnce(t *testing.T) {
	var calls atomic.Int32
	l, key := startPanelServer(t, &tg.User{ID: 1}, &panelServer{}, customEmojiServer(t, &calls))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		if _, err := m.DownloadCustomEmoji(ctx, emojiDoc); err != nil {
			t.Fatalf("DownloadCustomEmoji: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("sticker set lookups = %d, want 1", got)
	}
}

func TestDownloadCustomEmojiOfARecycledDocumentIsNotFound(t *testing.T) {
	var calls atomic.Int32
	l, key := startPanelServer(t, &tg.User{ID: 1}, &panelServer{}, customEmojiServer(t, &calls))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := m.DownloadCustomEmoji(ctx, emojiDocGone); err != tgapi.ErrCustomEmojiNotFound {
		t.Fatalf("err = %v, want ErrCustomEmojiNotFound", err)
	}
	if _, err := m.DownloadCustomEmoji(ctx, 0); err != tgapi.ErrCustomEmojiNotFound {
		t.Fatalf("zero id err = %v, want ErrCustomEmojiNotFound", err)
	}
}
