package gotdclient_test

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/tgapi"
)

func TestReplyToMessageIDExposesOnlyTheID(t *testing.T) {
	msg := &tg.Message{
		ID:      10,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 7},
	}
	if got := gotdclient.ReplyToMessageID(msg); got != 7 {
		t.Fatalf("reply = %d, want 7", got)
	}

	notReply := &tg.Message{ID: 11, PeerID: &tg.PeerChannel{ChannelID: 500}}
	if got := gotdclient.ReplyToMessageID(notReply); got != 0 {
		t.Fatalf("a non-reply reported %d", got)
	}
	if got := gotdclient.ReplyToMessageID(nil); got != 0 {
		t.Fatalf("nil message reported %d", got)
	}
}

func TestHistoryCarriesReplyMetadataWithoutAnExtraRequest(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	p := &panelServer{}
	l, key := startPanelServer(t, self, p)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	page, err := m.History(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 0, 30)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("messages = %d", len(page.Messages))
	}
	// The in-memory server reports message 120 as a reply to 100.
	if got := page.Messages[0].ReplyToMessageID; got != 100 {
		t.Fatalf("reply = %d, want 100", got)
	}
	if p.wasRead() {
		t.Fatal("read history was called while loading a page")
	}
}
