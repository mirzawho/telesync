package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/worker"
)

type editTelegram struct {
	*fakeTelegram
	gotRef tgapi.ChannelRef
	got    tgapi.EditRequest
	result tgapi.EditResult
}

func (e *editTelegram) EditMessage(_ context.Context, ref tgapi.ChannelRef, req tgapi.EditRequest) (tgapi.EditResult, error) {
	e.gotRef, e.got = ref, req
	return e.result, nil
}

func TestEditMessagePassesTargetAndReplacementToTheSelectedAdapter(t *testing.T) {
	tg := &editTelegram{
		fakeTelegram: &fakeTelegram{
			status:     tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 7}},
			dialogs:    []tgapi.Channel{{ID: 98}},
			oneMessage: tgapi.Message{ID: 11, Sender: &tgapi.Sender{ID: 7}},
		},
		result: tgapi.EditResult{Message: &tgapi.Message{ID: 11, Text: "replacement"}},
	}
	svc := worker.NewService(appconfig.Client{Key: "cat", Type: appconfig.Custom}, tg, worker.Options{})
	got, err := svc.EditMessage(context.Background(), rpc.EditRequest{
		ChannelID: 98,
		MessageID: 11,
		Text:      "replacement",
		Entities:  []rpc.Entity{{Type: "bold", Offset: 0, Length: 11}},
		Media: &rpc.SendMedia{
			Kind:     "voice",
			Name:     "",
			MimeType: "audio/ogg",
			Data:     []byte("ogg-bytes"),
			Duration: 5,
			Waveform: []byte{1, 2, 3},
		},
		NoWebpage: true,
	})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if tg.gotRef.ID != 98 || tg.got.MessageID != 11 || tg.got.Text != "replacement" {
		t.Fatalf("target/replacement = ref:%+v request:%+v", tg.gotRef, tg.got)
	}
	if len(tg.got.Entities) != 1 || tg.got.Entities[0].Type != "bold" {
		t.Fatalf("entities = %+v", tg.got.Entities)
	}
	if tg.got.Media == nil || tg.got.Media.Kind != tgapi.SendMediaVoice || string(tg.got.Media.Data) != "ogg-bytes" || tg.got.Media.Duration != 5 || string(tg.got.Media.Waveform) != string([]byte{1, 2, 3}) {
		t.Fatalf("media = %+v", tg.got.Media)
	}
	if !tg.got.NoWebpage {
		t.Fatal("NoWebpage was lost")
	}
	if got.Message == nil || got.Message.ID != 11 {
		t.Fatalf("result = %+v; an edit must retain the target ID", got)
	}
}

func TestEditMessagePassesExplicitMediaRetentionToTheSelectedAdapter(t *testing.T) {
	tg := &editTelegram{
		fakeTelegram: &fakeTelegram{
			status:     tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 7}},
			dialogs:    []tgapi.Channel{{ID: 98}},
			oneMessage: tgapi.Message{ID: 11, Sender: &tgapi.Sender{ID: 7}, Media: &tgapi.Media{Type: "photo"}},
		},
		result: tgapi.EditResult{Message: &tgapi.Message{ID: 11, Text: "caption", Media: &tgapi.Media{Type: "photo"}}},
	}
	_, err := worker.NewService(appconfig.Client{Key: "cat", Type: appconfig.Custom}, tg, worker.Options{}).EditMessage(context.Background(), rpc.EditRequest{
		ChannelID: 98, MessageID: 11, Text: "caption", KeepMedia: true,
	})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if !tg.got.KeepMedia || tg.got.Media != nil {
		t.Fatalf("adapter request = %+v; want explicit retention without a replacement upload", tg.got)
	}
}

func TestEditMessageAllowsChannelEditPermissionForAnotherAuthorsPost(t *testing.T) {
	tg := &editTelegram{
		fakeTelegram: &fakeTelegram{
			status:     tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 7}},
			dialogs:    []tgapi.Channel{{ID: 98, Permissions: tgapi.Permissions{Edit: true}}},
			oneMessage: tgapi.Message{ID: 11, Sender: &tgapi.Sender{ID: 8}},
		},
		result: tgapi.EditResult{Message: &tgapi.Message{ID: 11, Text: "replacement"}},
	}
	if _, err := worker.NewService(appconfig.Client{Key: "cat", Type: appconfig.Custom}, tg, worker.Options{}).EditMessage(context.Background(), rpc.EditRequest{
		ChannelID: 98, MessageID: 11, Text: "replacement",
	}); err != nil {
		t.Fatalf("channel edit permission should authorize editing another author's post: %v", err)
	}
	if tg.got.MessageID != 11 {
		t.Fatal("the channel-level edit permission path did not invoke Telegram")
	}
}

func TestEditMessageRejectsNonAuthorWithoutChannelEditPermission(t *testing.T) {
	tg := &editTelegram{fakeTelegram: &fakeTelegram{
		status:     tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 7}},
		dialogs:    []tgapi.Channel{{ID: 98}},
		oneMessage: tgapi.Message{ID: 11, Sender: &tgapi.Sender{ID: 8}},
	}}
	_, err := worker.NewService(appconfig.Client{Key: "cat", Type: appconfig.Custom}, tg, worker.Options{}).EditMessage(context.Background(), rpc.EditRequest{
		ChannelID: 98, MessageID: 11, Text: "replacement",
	})
	if !errors.Is(err, rpc.ErrMessageEditForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}
	if tg.got.MessageID != 0 {
		t.Fatal("Telegram edit called without author or channel edit rights")
	}
}

func TestEditMessageRefusesAnUnauthenticatedWorkerBeforeCallingTelegram(t *testing.T) {
	tg := &editTelegram{fakeTelegram: &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized}}}
	svc := worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official}, tg, worker.Options{})
	if _, err := svc.EditMessage(context.Background(), rpc.EditRequest{ChannelID: 1, MessageID: 2, Text: "x"}); err == nil {
		t.Fatal("an unauthorized worker edited a message")
	}
	if tg.gotRef.ID != 0 || tg.got.MessageID != 0 {
		t.Fatalf("Telegram was called while unauthorized: ref=%+v request=%+v", tg.gotRef, tg.got)
	}
}
