package gotdclient_test

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgtest"

	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/tgapi"
)

// The original filename lives in one place only: documentAttributeFilename.
// Everything else this adapter knows about a document (its MIME type, its
// size) is a fact about the bytes, not about the name the sender gave them, so
// a document that never carried a name must report no name at all.

func TestMediaFileNameReadsTheDocumentAttribute(t *testing.T) {
	doc := &tg.Document{
		MimeType: "application/vnd.android.package-archive",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "original-app-name.apk"},
		},
	}
	got := gotdclient.MediaFileName(&tg.MessageMediaDocument{Document: doc})
	if got != "original-app-name.apk" {
		t.Fatalf("MediaFileName = %q, want %q", got, "original-app-name.apk")
	}
}

func TestMediaFileNameIsPreservedExactly(t *testing.T) {
	// Spaces, Persian characters and a leading dot are all part of the name the
	// sender chose; normalising them would be inventing a different name.
	for _, want := range []string{
		"my app v2 (final).apk",
		"گزارش سالانه.pdf",
		".hidden-config",
		"UPPERCASE.APK",
		" padded .apk ",
	} {
		doc := &tg.Document{Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: want},
		}}
		if got := gotdclient.MediaFileName(&tg.MessageMediaDocument{Document: doc}); got != want {
			t.Errorf("MediaFileName = %q, want %q", got, want)
		}
	}
}

func TestMediaFileNameIsAbsentWithoutTheAttribute(t *testing.T) {
	// A MIME type and a size say nothing about a name: none is reported.
	doc := &tg.Document{
		MimeType: "application/pdf",
		Size:     4096,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAnimated{},
		},
	}
	if got := gotdclient.MediaFileName(&tg.MessageMediaDocument{Document: doc}); got != "" {
		t.Fatalf("MediaFileName = %q, want empty", got)
	}
}

func TestMediaFileNameIsAbsentForAPhoto(t *testing.T) {
	photo := &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}
	if got := gotdclient.MediaFileName(photo); got != "" {
		t.Fatalf("MediaFileName(photo) = %q, want empty", got)
	}
	if got := gotdclient.MediaFileName(nil); got != "" {
		t.Fatalf("MediaFileName(nil) = %q, want empty", got)
	}
}

func TestMediaFileNameIsEmptyWhenTelegramSendsAnEmptyName(t *testing.T) {
	// An empty attribute is not a filename; treating it as one would put a blank
	// name into Content-Disposition later.
	doc := &tg.Document{Attributes: []tg.DocumentAttributeClass{
		&tg.DocumentAttributeFilename{FileName: "   "},
	}}
	if got := gotdclient.MediaFileName(&tg.MessageMediaDocument{Document: doc}); got != "" {
		t.Fatalf("MediaFileName = %q, want empty", got)
	}
}

// documentServer answers getMessages with one post carrying a document built
// from the given attributes, so the whole extraction path is exercised rather
// than the attribute reader alone.
func documentServer(t *testing.T, messageID int, mime string, attrs ...tg.DocumentAttributeClass) func(*tgtest.Dispatcher) {
	t.Helper()
	return func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
				&tg.Message{
					ID:     messageID,
					PeerID: &tg.PeerChannel{ChannelID: 500},
					Media: &tg.MessageMediaDocument{Document: &tg.Document{
						ID:            77,
						AccessHash:    88,
						FileReference: []byte{9},
						DCID:          2,
						MimeType:      mime,
						Size:          12,
						Attributes:    attrs,
					}},
				},
			}})
		})
	}
}

func TestMessageCarriesTheOriginalFilename(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, documentServer(t, 36,
		"application/vnd.android.package-archive",
		&tg.DocumentAttributeFilename{FileName: "original-app-name.apk"},
	))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.Message(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Media == nil {
		t.Fatal("media is missing")
	}
	if got.Media.Filename != "original-app-name.apk" {
		t.Fatalf("filename = %q, want %q", got.Media.Filename, "original-app-name.apk")
	}
	// The label stays the localised description of the media kind; the filename
	// never replaces it.
	if got.Media.Label != "فایل" {
		t.Fatalf("label = %q, want %q", got.Media.Label, "فایل")
	}
	if got.Media.MimeType != "application/vnd.android.package-archive" {
		t.Fatalf("mime = %q", got.Media.MimeType)
	}
	if got.Media.Size != 12 {
		t.Fatalf("size = %d", got.Media.Size)
	}
}

func TestMessageAudioCarriesTheOriginalFilename(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, documentServer(t, 24, "audio/mpeg",
		&tg.DocumentAttributeAudio{Duration: 30},
		&tg.DocumentAttributeFilename{FileName: "track-01.mp3"},
	))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.Message(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 24)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Media == nil {
		t.Fatal("media is missing")
	}
	if got.Media.Type != "audio" {
		t.Fatalf("type = %q, want audio", got.Media.Type)
	}
	if got.Media.Filename != "track-01.mp3" {
		t.Fatalf("filename = %q, want %q", got.Media.Filename, "track-01.mp3")
	}
}

func TestMessageWithoutAFilenameReportsNone(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, documentServer(t, 36,
		"application/pdf",
		&tg.DocumentAttributeFilename{}, // present but empty: still no name
	))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.Message(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Media == nil {
		t.Fatal("media is missing")
	}
	if got.Media.Filename != "" {
		t.Fatalf("filename = %q, want empty: nothing may invent a name", got.Media.Filename)
	}
}
