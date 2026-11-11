package gotdclient_test

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"
	"github.com/mirza/telesync/internal/tgapi"
)

type albumProtocolCapture struct {
	mu            sync.Mutex
	calls         []string
	prepared      []tg.InputMediaClass
	items         []tg.InputMediaClass
	send          *tg.MessagesSendMultiMediaRequest
	failPrepareAt int
}

func (c *albumProtocolCapture) server(t *testing.T) func(*tgtest.Dispatcher) {
	t.Helper()
	return func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.UploadSaveFilePartRequestTypeID, func(s *tgtest.Server, r *tgtest.Request) error {
			return s.SendResult(r, &tg.BoolTrue{})
		})
		d.HandleFunc(tg.MessagesUploadMediaRequestTypeID, func(s *tgtest.Server, r *tgtest.Request) error {
			var req tg.MessagesUploadMediaRequest
			if err := req.Decode(r.Buf); err != nil {
				return err
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			c.calls = append(c.calls, "uploadMedia")
			c.items = append(c.items, req.Media)
			if c.failPrepareAt > 0 && len(c.items) == c.failPrepareAt {
				return s.SendErr(r, tgerr.New(400, "MEDIA_INVALID"))
			}
			switch req.Media.(type) {
			case *tg.InputMediaUploadedPhoto:
				photo := &tg.Photo{ID: int64(100 + len(c.items)), AccessHash: 200, FileReference: []byte{byte(len(c.items))}}
				c.prepared = append(c.prepared, &tg.InputMediaPhoto{ID: &tg.InputPhoto{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference}})
				return s.SendResult(r, &tg.MessageMediaPhoto{Photo: photo})
			case *tg.InputMediaUploadedDocument:
				doc := &tg.Document{ID: int64(100 + len(c.items)), AccessHash: 300, FileReference: []byte{byte(len(c.items))}}
				c.prepared = append(c.prepared, &tg.InputMediaDocument{ID: &tg.InputDocument{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}})
				return s.SendResult(r, &tg.MessageMediaDocument{Document: doc})
			default:
				return fmt.Errorf("unexpected uploaded input %T", req.Media)
			}
		})
		d.HandleFunc(tg.MessagesSendMultiMediaRequestTypeID, func(s *tgtest.Server, r *tgtest.Request) error {
			var req tg.MessagesSendMultiMediaRequest
			if err := req.Decode(r.Buf); err != nil {
				return err
			}
			c.mu.Lock()
			c.calls = append(c.calls, "sendMultiMedia")
			c.send = &req
			c.mu.Unlock()
			msg := &tg.Message{ID: 77, Date: 1700000000, Message: "caption", PeerID: &tg.PeerChannel{ChannelID: 500}}
			msg.SetGroupedID(987)
			return s.SendResult(r, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: msg}}})
		})
	}
}

func runAlbumProtocol(t *testing.T, capture *albumProtocolCapture, req tgapi.SendAlbumRequest) (*tgapi.SendAlbumResult, error) {
	t.Helper()
	l, key := startPanelServer(t, &tg.User{ID: 1, Username: "me"}, &panelServer{}, capture.server(t))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := m.SendAlbum(ctx, tgapi.ChannelRef{ID: 500}, req)
	if err != nil {
		return nil, err
	}
	return &got, nil
}

func TestSendAlbumPreparesItemsThenSendsOneNativeMultiMedia(t *testing.T) {
	capture := &albumProtocolCapture{}
	result, err := runAlbumProtocol(t, capture, tgapi.SendAlbumRequest{Items: []tgapi.SendAlbumItem{
		{Media: &tgapi.SendMedia{Kind: tgapi.SendMediaPhoto, Data: []byte("photo")}, Text: "one", Entities: []tgapi.Entity{{Type: tgapi.EntityBold, Offset: 0, Length: 3}}, RandomID: 101},
		{Media: &tgapi.SendMedia{Kind: tgapi.SendMediaVideo, Name: "clip.mp4", MimeType: "video/mp4", Data: []byte("video"), Duration: 8, Width: 640, Height: 360, SupportsStreaming: true}, Text: "two", Entities: []tgapi.Entity{{Type: tgapi.EntityItalic, Offset: 0, Length: 3}}, RandomID: 202},
	}})
	if err != nil {
		t.Fatalf("SendAlbum: %v", err)
	}
	if !reflect.DeepEqual(capture.calls, []string{"uploadMedia", "uploadMedia", "sendMultiMedia"}) {
		t.Fatalf("protocol calls = %v", capture.calls)
	}
	if got := len(capture.items); got != 2 {
		t.Fatalf("prepared item count = %d", got)
	}
	photo, ok := capture.items[0].(*tg.InputMediaUploadedPhoto)
	if !ok || photo.File == nil {
		t.Fatalf("first upload media = %#v", capture.items[0])
	}
	doc, ok := capture.items[1].(*tg.InputMediaUploadedDocument)
	if !ok || doc.MimeType != "video/mp4" || len(doc.Attributes) != 2 {
		t.Fatalf("second upload media = %#v", capture.items[1])
	}
	video, ok := doc.Attributes[0].(*tg.DocumentAttributeVideo)
	if !ok || video.Duration != 8 || video.W != 640 || video.H != 360 || !video.SupportsStreaming {
		t.Fatalf("video attributes = %#v", doc.Attributes)
	}
	if _, ok := doc.Attributes[1].(*tg.DocumentAttributeFilename); !ok {
		t.Fatalf("video filename attribute missing: %#v", doc.Attributes)
	}
	if capture.send == nil || len(capture.send.MultiMedia) != 2 {
		t.Fatalf("send request = %#v", capture.send)
	}
	for i, item := range capture.send.MultiMedia {
		if item.RandomID != []int64{101, 202}[i] || item.Message != []string{"one", "two"}[i] {
			t.Fatalf("multi_media[%d] = %#v", i, item)
		}
	}
	if _, ok := capture.send.MultiMedia[0].Media.(*tg.InputMediaPhoto); !ok {
		t.Fatalf("prepared photo media = %T", capture.send.MultiMedia[0].Media)
	}
	if _, ok := capture.send.MultiMedia[1].Media.(*tg.InputMediaDocument); !ok {
		t.Fatalf("prepared document media = %T", capture.send.MultiMedia[1].Media)
	}
	if len(capture.send.MultiMedia[0].Entities) != 1 {
		t.Fatalf("photo caption entities = %#v", capture.send.MultiMedia[0].Entities)
	}
	if len(capture.send.MultiMedia[1].Entities) != 1 {
		t.Fatalf("video caption entities = %#v", capture.send.MultiMedia[1].Entities)
	}
	if result.GroupedID != 987 || len(result.Messages) != 1 || result.Messages[0].ID != 77 {
		t.Fatalf("result = %+v", result)
	}
}

func TestSendAlbumPreparationFailureDoesNotSendMultiMedia(t *testing.T) {
	capture := &albumProtocolCapture{failPrepareAt: 2}
	l, key := startPanelServer(t, &tg.User{ID: 1, Username: "me"}, &panelServer{}, capture.server(t))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := m.SendAlbum(ctx, tgapi.ChannelRef{ID: 500}, tgapi.SendAlbumRequest{Items: []tgapi.SendAlbumItem{
		{Media: &tgapi.SendMedia{Kind: tgapi.SendMediaPhoto, Data: []byte("photo")}, RandomID: 1},
		{Media: &tgapi.SendMedia{Kind: tgapi.SendMediaPhoto, Data: []byte("photo2")}, RandomID: 2},
	}})
	if err == nil {
		t.Fatal("SendAlbum succeeded after preparation failure")
	}
	if !reflect.DeepEqual(capture.calls, []string{"uploadMedia", "uploadMedia"}) {
		t.Fatalf("protocol calls = %v, must not include sendMultiMedia", capture.calls)
	}
}
