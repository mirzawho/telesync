package gotdclient_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"

	"github.com/mirza/telesync/internal/tgapi"
)

func TestEditMessageUsesNativeMethodAndReadsBackTheSameTarget(t *testing.T) {
	var edited tg.MessagesEditMessageRequest
	var editCalls, readbackCalls int
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, editChannelResolver(), func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.MessagesEditMessageRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			editCalls++
			if err := edited.Decode(req.Buf); err != nil {
				return err
			}
			return server.SendResult(req, &tg.Updates{})
		})
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			readbackCalls++
			var got tg.ChannelsGetMessagesRequest
			if err := got.Decode(req.Buf); err != nil {
				return err
			}
			channel, channelOK := got.Channel.(*tg.InputChannel)
			var message *tg.InputMessageID
			if len(got.ID) == 1 {
				message, _ = got.ID[0].(*tg.InputMessageID)
			}
			if !channelOK || channel.ChannelID != 500 || message == nil || message.ID != 11 {
				t.Errorf("readback request = %+v, want channel 500 message 11", got)
			}
			return server.SendResult(req, &tg.MessagesChannelMessages{
				Messages: []tg.MessageClass{&tg.Message{
					ID: 11, Message: "replacement",
					PeerID:   &tg.PeerChannel{ChannelID: 500},
					Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 11}},
				}},
			})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := m.EditMessage(ctx, tgapi.ChannelRef{ID: 500}, tgapi.EditRequest{
		MessageID: 11,
		Text:      "replacement",
		Entities:  []tgapi.Entity{{Type: "bold", Offset: 0, Length: 11}},
		NoWebpage: true,
	})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	peer, ok := edited.Peer.(*tg.InputPeerChannel)
	if !ok || peer.ChannelID != 500 || peer.AccessHash != 900 {
		t.Fatalf("peer = %#v, want InputPeerChannel(500,900)", edited.Peer)
	}
	if edited.ID != 11 || edited.Message != "replacement" || !edited.NoWebpage {
		t.Fatalf("edit request = %+v", edited)
	}
	if len(edited.Entities) != 1 {
		t.Fatalf("entities = %+v", edited.Entities)
	}
	if _, ok := edited.Media.(*tg.InputMediaEmpty); !ok {
		t.Fatalf("media = %T, want explicit InputMediaEmpty for full replacement", edited.Media)
	}
	if editCalls != 1 || readbackCalls != 1 {
		t.Fatalf("calls: edit=%d readback=%d, want one each", editCalls, readbackCalls)
	}
	if got.Message == nil || got.Message.ID != 11 || got.Message.Text != "replacement" {
		t.Fatalf("readback result = %+v; original ID 11 must remain", got)
	}
}

func TestEditMessageVoiceReplacementPreservesTheVoiceDocumentAttribute(t *testing.T) {
	var edited tg.MessagesEditMessageRequest
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, editChannelResolver(), func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.UploadSaveFilePartRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.BoolTrue{})
		})
		d.HandleFunc(tg.MessagesEditMessageRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			if err := edited.Decode(req.Buf); err != nil {
				return err
			}
			return server.SendResult(req, &tg.Updates{})
		})
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.MessagesChannelMessages{
				Messages: []tg.MessageClass{&tg.Message{
					ID: 11, Message: "recording",
					PeerID: &tg.PeerChannel{ChannelID: 500},
					Media: &tg.MessageMediaDocument{Document: &tg.Document{
						ID: 77, AccessHash: 88, DCID: 2, MimeType: "audio/ogg", Size: 9,
						Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true, Duration: 5, Waveform: []byte{1, 2, 3}}},
					}},
				}},
				Chats: []tg.ChatClass{&tg.Channel{ID: 500, AccessHash: 900, Photo: &tg.ChatPhotoEmpty{}}},
			})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got, err := m.EditMessage(ctx, tgapi.ChannelRef{ID: 500}, tgapi.EditRequest{
		MessageID: 11,
		Text:      "recording",
		Media: &tgapi.SendMedia{
			Kind: tgapi.SendMediaVoice, MimeType: "audio/ogg", Duration: 5,
			Waveform: []byte{1, 2, 3}, Data: []byte("ogg-bytes"),
		},
	})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	doc, ok := edited.Media.(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("media = %T, want uploaded document for Telegram voice", edited.Media)
	}
	if doc.ForceFile {
		t.Fatal("ForceFile is set; Telegram would publish the voice as a generic file")
	}
	voice := false
	for _, attr := range doc.Attributes {
		if a, ok := attr.(*tg.DocumentAttributeAudio); ok {
			voice = a.Voice && a.Duration == 5 && string(a.Waveform) == string([]byte{1, 2, 3})
		}
	}
	if !voice {
		t.Fatalf("document attributes = %+v, want voice=true with duration and waveform", doc.Attributes)
	}
	if got.Message == nil || got.Message.ID != 11 || got.Message.Media == nil || got.Message.Media.Type != "voice" {
		t.Fatalf("readback = %+v; voice edit must preserve target ID and voice type", got)
	}
}

func TestEditMessageMapsRejectedMediaRemovalToSpecificUnsupportedError(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, editChannelResolver(), func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.MessagesEditMessageRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendErr(req, &tgerr.Error{Code: 400, Message: "MEDIA_EMPTY"})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := m.EditMessage(ctx, tgapi.ChannelRef{ID: 500}, tgapi.EditRequest{MessageID: 11, Text: "text only"})
	if !errors.Is(err, tgapi.ErrEditMediaRemovalUnsupported) {
		t.Fatalf("error = %v, want media removal unsupported", err)
	}
}

func editChannelResolver() func(*tgtest.Dispatcher) {
	return func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsGetChannelsRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Channel{ID: 500, AccessHash: 900, Photo: &tg.ChatPhotoEmpty{}}}})
		})
	}
}
