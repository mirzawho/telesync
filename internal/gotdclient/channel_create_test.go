package gotdclient_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/exchange"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"

	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/tgapi"
)

func TestCreateBroadcastChannelRequiresKnownHumanAccount(t *testing.T) {
	tests := []struct {
		name string
		self tg.UserClass
		want bool
	}{
		{name: "bot", self: &tg.User{ID: 1, Bot: true}},
		{name: "unknown user variant", self: &tg.UserEmpty{ID: 1}},
		{name: "human", self: &tg.User{ID: 1}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createCalls := 0
			self, _ := tt.self.(*tg.User)
			var l *pipeListener
			var key exchange.PublicKey
			if self != nil {
				l, key = startPanelServer(t, self, &panelServer{}, func(d *tgtest.Dispatcher) {
					d.HandleFunc(tg.ChannelsCreateChannelRequestTypeID, createHandler(t, &createCalls))
				})
			} else {
				l, key = startPanelServer(t, nil, &panelServer{}, func(d *tgtest.Dispatcher) {
					d.HandleFunc(tg.UsersGetUsersRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
						return server.SendVector(req, tt.self)
					})
					d.HandleFunc(tg.ChannelsCreateChannelRequestTypeID, createHandler(t, &createCalls))
				})
			}
			m := managerFor(t, l, key)
			if self == nil {
				waitForState(t, m, tgapi.StateError)
			} else {
				waitForState(t, m, tgapi.StateReady)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			info, err := m.CreateBroadcastChannel(ctx, "created", "description")
			if tt.want {
				if errors.Is(err, gotdclient.ErrChannelCreateRequiresHumanAccount) || createCalls == 0 {
					t.Fatalf("CreateBroadcastChannel error=%v calls=%d, expected human account to reach create RPC", err, createCalls)
				}
				_ = info
				return
			}
			if !errors.Is(err, gotdclient.ErrChannelCreateRequiresHumanAccount) {
				t.Fatalf("CreateBroadcastChannel error = %v, want human-account restriction", err)
			}
			if createCalls != 0 {
				t.Fatalf("channels.createChannel calls = %d, want 0", createCalls)
			}
		})
	}
}

func createHandler(t *testing.T, calls *int) func(*tgtest.Server, *tgtest.Request) error {
	t.Helper()
	return func(server *tgtest.Server, req *tgtest.Request) error {
		(*calls)++
		var request tg.ChannelsCreateChannelRequest
		if err := request.Decode(req.Buf); err != nil {
			return err
		}
		if !request.Broadcast || request.Megagroup {
			return server.SendErr(req, tgerr.New(400, "WRONG_CHANNEL_FLAGS"))
		}
		return server.SendErr(req, tgerr.New(400, "CHANNELS_TOO_MUCH"))
	}
}

func TestSetBroadcastChannelUsernameFalseIsNotAssigned(t *testing.T) {
	l, key := startPanelServer(t, &tg.User{ID: 1}, &panelServer{}, func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsUpdateUsernameRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.BoolFalse{})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := m.SetBroadcastChannelUsername(ctx, 500, "public_name")
	if !errors.Is(err, gotdclient.ErrChannelUsernameNotAssigned) {
		t.Fatalf("SetBroadcastChannelUsername error = %v, want ErrChannelUsernameNotAssigned", err)
	}
}

func TestSetBroadcastChannelAvatarRejectsInvalidBytesBeforeRPC(t *testing.T) {
	tests := []struct {
		name string
		mime string
		data []byte
	}{
		{name: "empty", mime: "image/jpeg"},
		{name: "unsupported", mime: "image/svg+xml", data: []byte("<svg/>")},
		{name: "oversized", mime: "image/jpeg", data: make([]byte, 32<<20+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			l, key := startPanelServer(t, &tg.User{ID: 1}, &panelServer{}, func(d *tgtest.Dispatcher) {
				d.HandleFunc(tg.UploadSaveFilePartRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
					calls++
					return server.SendResult(req, &tg.BoolTrue{})
				})
			})
			m := managerFor(t, l, key)
			waitForState(t, m, tgapi.StateReady)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := m.SetBroadcastChannelAvatar(ctx, 500, "avatar", tt.mime, tt.data); err == nil {
				t.Fatal("SetBroadcastChannelAvatar unexpectedly succeeded")
			}
			if calls != 0 {
				t.Fatalf("upload RPC calls = %d, want 0", calls)
			}
		})
	}
}

func TestSetBroadcastChannelAvatarEditsExactResolvedChannel(t *testing.T) {
	var uploaded, edited bool
	l, key := startPanelServer(t, &tg.User{ID: 1}, &panelServer{}, func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.UploadSaveFilePartRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			uploaded = true
			return server.SendResult(req, &tg.BoolTrue{})
		})
		d.HandleFunc(tg.ChannelsEditPhotoRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.ChannelsEditPhotoRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			ch, ok := request.Channel.(*tg.InputChannel)
			if !ok || ch.ChannelID != 500 || ch.AccessHash != 900 {
				return server.SendErr(req, tgerr.New(400, "WRONG_CHANNEL"))
			}
			photo, ok := request.Photo.(*tg.InputChatUploadedPhoto)
			if !ok || photo.File == nil {
				return server.SendErr(req, tgerr.New(400, "WRONG_PHOTO"))
			}
			edited = true
			return server.SendResult(req, &tg.Updates{Chats: []tg.ChatClass{&tg.Channel{
				ID: 500, AccessHash: 900, Title: "sample", Broadcast: true,
				Photo: &tg.ChatPhoto{PhotoID: 1234, DCID: 2},
			}}})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	version, err := m.SetBroadcastChannelAvatar(ctx, 500, "avatar.png", "image/png", []byte("not-a-real-png"))
	if err != nil {
		t.Fatalf("SetBroadcastChannelAvatar: %v", err)
	}
	if !uploaded || !edited {
		t.Fatalf("uploaded=%v edited=%v, want both true", uploaded, edited)
	}
	if version != 1234 {
		t.Fatalf("photo version/id = %d, want Telegram-reported 1234", version)
	}
}
