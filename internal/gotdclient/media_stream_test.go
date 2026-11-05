package gotdclient_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"

	"github.com/mirza/telesync/internal/tgapi"
)

// downloadServer serves one document message and its bytes through
// upload.getFile, honouring the requested offset and limit the way Telegram
// does. It records the requests it saw so a test can prove the adapter only
// ever asks for the bytes it needs.
func downloadServer(t *testing.T, docBytes []byte, attrs []tg.DocumentAttributeClass) (
	func(*tgtest.Dispatcher), *requestLog) {

	t.Helper()
	return downloadServerReporting(t, docBytes, int64(len(docBytes)), attrs)
}

// downloadServerReporting serves one document whose reported size is not the
// size of the bytes it holds. The reported size is what the media route
// announces as Content-Length, so the mismatch is what a caller must not be
// handed back as a whole attachment.
func downloadServerReporting(t *testing.T, docBytes []byte, reportedSize int64, attrs []tg.DocumentAttributeClass) (
	func(*tgtest.Dispatcher), *requestLog) {

	t.Helper()
	log := &requestLog{}
	server := func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			log.lookup()
			return server.SendResult(req, &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
				&tg.Message{
					ID:     36,
					PeerID: &tg.PeerChannel{ChannelID: 500},
					Media: &tg.MessageMediaDocument{Document: &tg.Document{
						ID:            77,
						AccessHash:    88,
						FileReference: []byte{9},
						DCID:          2,
						MimeType:      "application/vnd.android.package-archive",
						Size:          reportedSize,
						Attributes:    attrs,
					}},
				},
			}})
		})
		d.HandleFunc(tg.UploadGetFileRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.UploadGetFileRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			log.get(request.Offset, request.Limit)
			// Telegram refuses a getFile whose limit is not a multiple of 4 KiB
			// or is larger than 1 MiB. Modelling that is the only way a test can
			// tell a part Telegram will serve from one it will not.
			if request.Limit <= 0 || request.Limit > 1024*1024 || request.Limit%(4*1024) != 0 {
				return server.SendErr(req, tgerr.New(400, "LIMIT_INVALID"))
			}
			start := request.Offset
			if start >= int64(len(docBytes)) {
				return server.SendResult(req, &tg.UploadFile{Type: &tg.StorageFilePartial{}, Bytes: nil})
			}
			end := start + int64(request.Limit)
			if end > int64(len(docBytes)) {
				end = int64(len(docBytes))
			}
			return server.SendResult(req, &tg.UploadFile{
				Type:  &tg.StorageFilePartial{},
				Bytes: docBytes[start:end],
			})
		})
	}
	return server, log
}

type requestLog struct {
	mu      sync.Mutex
	gets    [][2]int64
	lookups int
}

func (l *requestLog) get(offset int64, limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gets = append(l.gets, [2]int64{offset, int64(limit)})
}

func (l *requestLog) lookup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lookups++
}

func (l *requestLog) counts() (gets [][2]int64, messageLookups int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][2]int64(nil), l.gets...), l.lookups
}

func readStream(t *testing.T, s tgapi.MediaStream) string {
	t.Helper()
	defer s.Body.Close()
	data, err := io.ReadAll(s.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	return string(data)
}

func TestDownloadMediaStreamsTheWholeAttachment(t *testing.T) {
	docBytes := []byte("the whole apk")
	self := &tg.User{ID: 1, Username: "me"}
	server, log := downloadServer(t, docBytes, []tg.DocumentAttributeClass{
		&tg.DocumentAttributeFilename{FileName: "original-app-name.apk"},
	})
	l, key := startPanelServer(t, self, &panelServer{}, server)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{})
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if got.Filename != "original-app-name.apk" {
		t.Fatalf("filename = %q", got.Filename)
	}
	if got.MimeType != "application/vnd.android.package-archive" {
		t.Fatalf("mime = %q", got.MimeType)
	}
	if got.Size != int64(len(docBytes)) {
		t.Fatalf("size = %d, want %d", got.Size, len(docBytes))
	}
	if got.Offset != 0 || got.Length != int64(len(docBytes)) {
		t.Fatalf("offset = %d length = %d", got.Offset, got.Length)
	}
	if body := readStream(t, got); body != string(docBytes) {
		t.Fatalf("body = %q, want %q", body, docBytes)
	}
	gets, _ := log.counts()
	for _, g := range gets {
		if g[0] < 0 || g[1] <= 0 {
			t.Fatalf("bad getFile request %v", g)
		}
	}
}

func TestDownloadMediaServesAByteRange(t *testing.T) {
	docBytes := []byte("0123456789abcdef")
	self := &tg.User{ID: 1, Username: "me"}
	server, log := downloadServer(t, docBytes, nil)
	l, key := startPanelServer(t, self, &panelServer{}, server)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{Offset: 4, Limit: 6})
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if got.Offset != 4 || got.Length != 6 {
		t.Fatalf("offset = %d length = %d, want 4/6", got.Offset, got.Length)
	}
	// The total size is about the whole attachment, not about this slice: a
	// caller cannot build Content-Range from a slice length.
	if got.Size != int64(len(docBytes)) {
		t.Fatalf("size = %d, want %d", got.Size, len(docBytes))
	}
	if body := readStream(t, got); body != "456789" {
		t.Fatalf("body = %q, want %q", body, "456789")
	}
	gets, _ := log.counts()
	for _, g := range gets {
		// A seek must not drag the bytes before it across the wire.
		if g[0] < 4 {
			t.Fatalf("downloaded bytes before the requested range: %v", gets)
		}
		// Telegram serves no part smaller than one 4 KiB multiple, so reading six
		// bytes necessarily asks for a whole part. What must not happen is asking
		// for more than one: that is the line between the granularity Telegram
		// imposes and dragging the attachment across the wire.
		if g[1] > 512*1024 {
			t.Fatalf("asked for more than one part for a six byte range: %v", gets)
		}
	}
}

func TestDownloadMediaReturnsAnEmptySlicePastTheEnd(t *testing.T) {
	docBytes := []byte("tiny")
	self := &tg.User{ID: 1, Username: "me"}
	server, log := downloadServer(t, docBytes, nil)
	l, key := startPanelServer(t, self, &panelServer{}, server)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{Offset: 99, Limit: 10})
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if got.Length != 0 {
		t.Fatalf("length = %d, want 0", got.Length)
	}
	if body := readStream(t, got); body != "" {
		t.Fatalf("body = %q, want empty", body)
	}
	if gets, _ := log.counts(); len(gets) != 0 {
		t.Fatalf("a slice past the end must not reach Telegram at all: %v", gets)
	}
}

func TestDownloadMediaStreamsBeyondOneChunk(t *testing.T) {
	// Larger than the 512 KiB upload.getFile chunk, so the body can only arrive
	// as several requests. It also proves the whole file never has to be held at
	// once: the fixture would notice a single oversized request.
	docBytes := []byte(strings.Repeat("a", 1_200_000))
	self := &tg.User{ID: 1, Username: "me"}
	server, log := downloadServer(t, docBytes, nil)
	l, key := startPanelServer(t, self, &panelServer{}, server)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{})
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	body := readStream(t, got)
	if len(body) != len(docBytes) {
		t.Fatalf("body length = %d, want %d", len(body), len(docBytes))
	}
	gets, _ := log.counts()
	if len(gets) < 3 {
		t.Fatalf("expected several chunk requests, got %v", gets)
	}
	for _, g := range gets {
		if g[1] > 512<<10 {
			t.Fatalf("chunk of %d bytes is larger than Telegram's part size: %v", g[1], gets)
		}
	}
}

func TestDownloadMediaServesATailRange(t *testing.T) {
	docBytes := []byte("0123456789")
	self := &tg.User{ID: 1, Username: "me"}
	server, _ := downloadServer(t, docBytes, nil)
	l, key := startPanelServer(t, self, &panelServer{}, server)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{Tail: 3})
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if got.Offset != 7 || got.Length != 3 {
		t.Fatalf("offset = %d length = %d, want 7/3", got.Offset, got.Length)
	}
	if body := readStream(t, got); body != "789" {
		t.Fatalf("body = %q, want %q", body, "789")
	}
}

// TestDownloadMediaRefreshesAnExpiredFileReference pins the recovery Telegram
// needs in practice: a file reference goes stale while a message stays cached,
// and the only way forward is to read the message again for a fresh one.
func TestDownloadMediaRefreshesAnExpiredFileReference(t *testing.T) {
	docBytes := []byte("fresh bytes")
	self := &tg.User{ID: 1, Username: "me"}

	var mu sync.Mutex
	attempts := 0
	seenRefs := []string{}

	l, key := startPanelServer(t, self, &panelServer{}, func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			mu.Lock()
			ref := []byte{9}
			if attempts > 0 {
				ref = []byte{4, 2}
			}
			mu.Unlock()
			return server.SendResult(req, &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
				&tg.Message{
					ID:     36,
					PeerID: &tg.PeerChannel{ChannelID: 500},
					Media: &tg.MessageMediaDocument{Document: &tg.Document{
						ID: 77, AccessHash: 88, FileReference: ref, DCID: 2,
						MimeType: "application/pdf", Size: int64(len(docBytes)),
						Attributes: []tg.DocumentAttributeClass{
							&tg.DocumentAttributeFilename{FileName: "report.pdf"},
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
			loc := request.Location.(*tg.InputDocumentFileLocation)
			mu.Lock()
			attempts++
			seenRefs = append(seenRefs, string(loc.FileReference))
			stale := attempts == 1
			mu.Unlock()
			if stale {
				return server.SendErr(req, tgerr.New(400, "FILE_REFERENCE_EXPIRED"))
			}
			return server.SendResult(req, &tg.UploadFile{
				Type:  &tg.StorageFilePartial{},
				Bytes: docBytes,
			})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{})
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if body := readStream(t, got); body != string(docBytes) {
		t.Fatalf("body = %q, want %q", body, docBytes)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seenRefs) != 2 {
		t.Fatalf("getFile attempts = %v, want two", seenRefs)
	}
	if seenRefs[0] == seenRefs[1] {
		t.Fatalf("the retry reused the stale file reference: %q", seenRefs)
	}
}

func TestDownloadMediaFailsWhenTelegramSendsFewerBytesThanItReported(t *testing.T) {
	// The size Telegram reports is the length the media route announces as
	// Content-Length. When the bytes stop before it, the read must fail rather
	// than end cleanly: nothing else on the path can tell a whole attachment
	// from a truncated one, so a quiet end here is a corrupt download served as
	// if it were complete.
	docBytes := []byte("only half of it")
	const reportedSize = 30
	self := &tg.User{ID: 1, Username: "me"}
	server, _ := downloadServerReporting(t, docBytes, reportedSize, nil)
	l, key := startPanelServer(t, self, &panelServer{}, server)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{})
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	defer got.Body.Close()
	if got.Size != reportedSize {
		t.Fatalf("size = %d, want the reported %d", got.Size, reportedSize)
	}
	if got.Length != reportedSize {
		t.Fatalf("length = %d, want the reported %d", got.Length, reportedSize)
	}

	data, readErr := io.ReadAll(got.Body)
	if readErr == nil {
		t.Fatalf("reading a short attachment returned %d of %d bytes and no error: "+
			"it must fail instead of ending cleanly", len(data), reportedSize)
	}
}

func TestDownloadMediaStreamsAnUnknownSizeAttachmentToEnd(t *testing.T) {
	// With no reported size there is no length to be short of, and Telegram
	// stopping is the only way to learn the attachment is over. That must stay a
	// clean end: it is the one case where stopping is the success, not the
	// failure this task is about.
	docBytes := []byte("no size in the metadata")
	self := &tg.User{ID: 1, Username: "me"}
	server, _ := downloadServerReporting(t, docBytes, 0, nil)
	l, key := startPanelServer(t, self, &panelServer{}, server)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{})
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	defer got.Body.Close()
	if got.Size != 0 || got.Length != 0 {
		t.Fatalf("size = %d length = %d, want 0/0: with no reported size nothing was promised",
			got.Size, got.Length)
	}
	data, readErr := io.ReadAll(got.Body)
	if readErr != nil {
		t.Fatalf("read an unknown-size attachment: %v", readErr)
	}
	if string(data) != string(docBytes) {
		t.Fatalf("body = %q, want %q", data, docBytes)
	}
}

func TestDownloadMediaReadsAnAttachmentWhoseSizeIsNotAChunkMultiple(t *testing.T) {
	// Telegram serves a getFile only in parts whose limit is a multiple of 4 KiB,
	// so an attachment whose byte count is not one asks for a limit Telegram will
	// refuse. A whole file that is not a multiple fails on its first part and the
	// caller gets nothing; a larger one delivers whole parts and then dies on the
	// tail. Either way the download stops short of the size it promised.
	cases := []struct {
		name string
		size int
	}{
		{name: "a whole file smaller than one part", size: 5000},
		{name: "a tail after whole parts", size: 600_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docBytes := bytes.Repeat([]byte("a"), tc.size)
			self := &tg.User{ID: 1, Username: "me"}
			server, _ := downloadServer(t, docBytes, nil)
			l, key := startPanelServer(t, self, &panelServer{}, server)
			m := managerFor(t, l, key)
			waitForState(t, m, tgapi.StateReady)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			got, err := m.DownloadMedia(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 36, tgapi.MediaSlice{})
			if err != nil {
				t.Fatalf("DownloadMedia: %v", err)
			}
			defer got.Body.Close()
			if got.Length != int64(tc.size) {
				t.Fatalf("length = %d, want %d", got.Length, tc.size)
			}
			data, readErr := io.ReadAll(got.Body)
			if readErr != nil {
				t.Fatalf("read: %v", readErr)
			}
			if len(data) != tc.size {
				t.Fatalf("read %d of %d bytes: the download stopped short", len(data), tc.size)
			}
		})
	}
}
