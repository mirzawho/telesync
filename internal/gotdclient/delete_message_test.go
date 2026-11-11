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

func TestDeleteMessageUsesChannelsDeleteMessagesWithOneResolvedPeerAndID(t *testing.T) {
	var calls int
	var gotChannel *tg.InputChannel
	var gotIDs []int
	l, key := startPanelServer(t, &tg.User{ID: 1, Username: "me"}, &panelServer{}, func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsDeleteMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.ChannelsDeleteMessagesRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			calls++
			var ok bool
			gotChannel, ok = request.Channel.(*tg.InputChannel)
			if !ok {
				return server.SendErr(req, tgerr.New(400, "wrong channel type"))
			}
			gotIDs = append([]int(nil), request.ID...)
			return server.SendResult(req, &tg.MessagesAffectedMessages{Pts: 12, PtsCount: 1})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := m.DeleteMessage(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 11); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if calls != 1 {
		t.Fatalf("channels.deleteMessages calls = %d, want 1", calls)
	}
	if gotChannel == nil || gotChannel.ChannelID != 500 || gotChannel.AccessHash != 900 {
		t.Fatalf("InputChannel = %+v, want channel 500 with its access hash", gotChannel)
	}
	if len(gotIDs) != 1 || gotIDs[0] != 11 {
		t.Fatalf("message ids = %v, want [11]", gotIDs)
	}
}

func TestDeleteMessageClassifiesTelegramRefusal(t *testing.T) {
	l, key := startPanelServer(t, &tg.User{ID: 1, Username: "me"}, &panelServer{}, func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsDeleteMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendErr(req, tgerr.New(403, "MESSAGE_DELETE_FORBIDDEN"))
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := m.DeleteMessage(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 11)
	if !errors.Is(err, tgapi.ErrMessageDeleteForbidden) {
		t.Fatalf("error = %v, want ErrMessageDeleteForbidden", err)
	}
}

func TestDeleteMessageRejectsUnsupportedIDsBeforeCallingTelegram(t *testing.T) {
	calls := 0
	l, key := startPanelServer(t, &tg.User{ID: 1, Username: "me"}, &panelServer{}, func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsDeleteMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			calls++
			return server.SendResult(req, &tg.MessagesAffectedMessages{Pts: 1, PtsCount: 1})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, id := range []int{0, -1, 1 << 31} {
		if err := m.DeleteMessage(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, id); err == nil {
			t.Errorf("DeleteMessage(%d) unexpectedly succeeded", id)
		}
	}
	if calls != 0 {
		t.Fatalf("channels.deleteMessages calls = %d, want 0", calls)
	}
}
