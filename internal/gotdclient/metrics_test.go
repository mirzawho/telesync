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

func TestViewsIsNilWhenTelegramDidNotReportIt(t *testing.T) {
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 500}}
	if got := gotdclient.Views(msg); got != nil {
		t.Fatalf("views = %v, want nil", *got)
	}

	zero := &tg.Message{ID: 2, PeerID: &tg.PeerChannel{ChannelID: 500}}
	zero.SetViews(0) // FLAG set, value 0: a real count of zero, not "unknown"
	got := gotdclient.Views(zero)
	if got == nil || *got != 0 {
		t.Fatalf("views = %v, want a real 0", got)
	}

	some := &tg.Message{ID: 3, PeerID: &tg.PeerChannel{ChannelID: 500}}
	some.SetViews(1520)
	if got := gotdclient.Views(some); got == nil || *got != 1520 {
		t.Fatalf("views = %v, want 1520", got)
	}
	if got := gotdclient.Views(nil); got != nil {
		t.Fatalf("nil message reported views %v", *got)
	}
}

func TestReactionsPreserveKindAndChosenState(t *testing.T) {
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 500}}
	chosen := tg.ReactionCount{Reaction: &tg.ReactionEmoji{Emoticon: "❤️"}, Count: 8}
	chosen.SetChosenOrder(3)
	msg.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{
		{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 12},
		chosen,
		{Reaction: &tg.ReactionCustomEmoji{DocumentID: 5368324170671202286}, Count: 2},
		{Reaction: &tg.ReactionPaid{}},
	}})

	got := gotdclient.Reactions(msg)
	if len(got) != 4 {
		t.Fatalf("reactions = %+v", got)
	}
	if got[0].Type != "emoji" || got[0].Emoji != "👍" || got[0].Count != 12 {
		t.Fatalf("reaction 0 = %+v", got[0])
	}
	if got[0].Chosen != nil {
		t.Fatalf("an unchosen reaction must report chosen=null, got %v", *got[0].Chosen)
	}
	if got[1].Chosen == nil || !*got[1].Chosen {
		t.Fatalf("reaction 1 chosen = %v, want true", got[1].Chosen)
	}
	if got[2].Type != "custom_emoji" || got[2].DocumentID != 5368324170671202286 || got[2].Emoji != "" {
		t.Fatalf("custom reaction = %+v", got[2])
	}
	if got[3].Type != "other" || got[3].Emoji != "" {
		t.Fatalf("a paid reaction must not be reported as an emoji: %+v", got[3])
	}
}

func TestReactionsAreEmptyWithoutTheConstructor(t *testing.T) {
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 500}}
	if got := gotdclient.Reactions(msg); got == nil || len(got) != 0 {
		t.Fatalf("reactions = %#v, want an empty slice", got)
	}
	empty := &tg.Message{ID: 2, PeerID: &tg.PeerChannel{ChannelID: 500}}
	empty.SetReactions(tg.MessageReactions{})
	if got := gotdclient.Reactions(empty); got == nil || len(got) != 0 {
		t.Fatalf("reactions = %#v, want an empty slice", got)
	}
	if got := gotdclient.Reactions(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil message reactions = %#v", got)
	}
}

// messageServer answers channels.getMessages with one scripted message.
func messageServer(msg *tg.Message) func(*tgtest.Dispatcher) {
	return func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.MessagesChannelMessages{
				Messages: []tg.MessageClass{msg},
			})
		})
	}
}

// The in-memory history server reports message 120 with no views field.
func TestHistoryWithoutViewsReportsNullAndEmptyReactions(t *testing.T) {
	l, key := startPanelServer(t, &tg.User{ID: 1, Username: "me"}, &panelServer{})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	page, err := m.History(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 0, 30)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	got := page.Messages[0]
	if got.Views != nil {
		t.Fatalf("views = %v, want nil", *got.Views)
	}
	if got.Reactions == nil || len(got.Reactions) != 0 {
		t.Fatalf("reactions = %#v, want []", got.Reactions)
	}
}

func TestSingleMessageCarriesViewsAndReactions(t *testing.T) {
	msg := &tg.Message{ID: 9, PeerID: &tg.PeerChannel{ChannelID: 500}}
	msg.SetViews(7)
	mine := tg.ReactionCount{Reaction: &tg.ReactionEmoji{Emoticon: "🔥"}, Count: 1}
	mine.SetChosenOrder(1)
	msg.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{mine}})
	l, key := startPanelServer(t, &tg.User{ID: 1}, &panelServer{}, messageServer(msg))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.Message(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 9)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Views == nil || *got.Views != 7 {
		t.Fatalf("views = %v", got.Views)
	}
	if len(got.Reactions) != 1 || got.Reactions[0].Emoji != "🔥" ||
		got.Reactions[0].Chosen == nil || !*got.Reactions[0].Chosen {
		t.Fatalf("reactions = %+v", got.Reactions)
	}
}
