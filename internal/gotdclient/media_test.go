package gotdclient_test

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"

	"github.com/mirza/telesync/internal/tgapi"
)

// photoServer answers getMessages with one post carrying a photo, and
// upload.getFile with the given bytes. It rejects a photo location that does not
// match what the adapter should send, so the test fails if the thumb size or
// file reference is wrong.
func photoServer(t *testing.T, photoBytes []byte, wantThumb string) func(*tgtest.Dispatcher) {
	return func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
				&tg.Message{
					ID:     179,
					PeerID: &tg.PeerChannel{ChannelID: 500},
					Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{
						ID:            9,
						AccessHash:    10,
						FileReference: []byte{1, 2, 3},
						DCID:          2,
						Sizes: []tg.PhotoSizeClass{
							&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 2},
							&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: 4},
						},
					}},
				},
			}})
		})
		d.HandleFunc(tg.UploadGetFileRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.UploadGetFileRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			loc, ok := request.Location.(*tg.InputPhotoFileLocation)
			if !ok {
				return server.SendErr(req, tgerr.New(400, "expected a photo location"))
			}
			if loc.ID != 9 || loc.AccessHash != 10 {
				return server.SendErr(req, tgerr.New(400, "wrong photo id or access hash"))
			}
			if string(loc.FileReference) != string([]byte{1, 2, 3}) {
				return server.SendErr(req, tgerr.New(400, "wrong file reference"))
			}
			if loc.ThumbSize != wantThumb {
				return server.SendErr(req, tgerr.New(400, "wrong thumb size: "+loc.ThumbSize))
			}
			start := request.Offset
			end := start + int64(request.Limit)
			if start >= int64(len(photoBytes)) {
				return server.SendResult(req, &tg.UploadFile{
					Type:  &tg.StorageFileJpeg{},
					Bytes: nil,
				})
			}
			if end > int64(len(photoBytes)) {
				end = int64(len(photoBytes))
			}
			return server.SendResult(req, &tg.UploadFile{
				Type:  &tg.StorageFileJpeg{},
				Bytes: photoBytes[start:end],
			})
		})
	}
}

func TestDownloadMediaReturnsPhotoBytes(t *testing.T) {
	photoBytes := []byte("jpeg-photo-bytes")
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, photoServer(t, photoBytes, "y"))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 179)
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if string(got.Data) != string(photoBytes) {
		t.Fatalf("bytes = %q, want %q", got.Data, photoBytes)
	}
	if got.MimeType != "image/jpeg" {
		t.Fatalf("mime = %q", got.MimeType)
	}
}

func TestDownloadMediaReportsAMessageWithoutMedia(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
				&tg.Message{
					ID:     100,
					PeerID: &tg.PeerChannel{ChannelID: 500},
					Media:  &tg.MessageMediaEmpty{},
				},
			}})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 100); err == nil {
		t.Fatal("expected an error for a message without media")
	}
}

func TestDownloadMediaRejectsANonPositiveMessageID(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500}, 0); err == nil {
		t.Fatal("message id 0 must be rejected")
	}
}

// TestDownloadMediaUsesTheChannelScopedLookup pins the fix for a real bug: a
// channel post is only reachable through channels.getMessages (it needs the
// channel's access hash). Using messages.getMessages returned no message, so
// every attachment looked like "no media" and the API answered 404.
func TestDownloadMediaUsesTheChannelScopedLookup(t *testing.T) {
	photoBytes := []byte("photo")
	self := &tg.User{ID: 1, Username: "me"}

	usedChannelScoped := false
	l, key := startPanelServer(t, self, &panelServer{}, func(d *tgtest.Dispatcher) {
		// Any request on the non-channel method is a regression.
		d.HandleFunc(tg.MessagesGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendErr(req, tgerr.New(400, "messages.getMessages cannot see channel posts"))
		})
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			usedChannelScoped = true
			return server.SendResult(req, &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
				&tg.Message{
					ID:     179,
					PeerID: &tg.PeerChannel{ChannelID: 500},
					Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{
						ID: 9, AccessHash: 10, FileReference: []byte{1},
						DCID: 2, Sizes: []tg.PhotoSizeClass{
							&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: int(len(photoBytes))},
						},
					}},
				},
			}})
		})
		d.HandleFunc(tg.UploadGetFileRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.UploadGetFileRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			return server.SendResult(req, &tg.UploadFile{
				Type: &tg.StorageFileJpeg{}, Bytes: photoBytes,
			})
		})
	})

	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 179)
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if !usedChannelScoped {
		t.Fatal("the adapter did not use channels.getMessages")
	}
	if string(got.Data) != string(photoBytes) {
		t.Fatalf("bytes = %q", got.Data)
	}
}
