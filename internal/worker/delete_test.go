package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

func TestDeleteMessagePreflightsAndDeletesExactlyOneResolvedMessage(t *testing.T) {
	tg := &fakeTelegram{
		status:     tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs:    []tgapi.Channel{{ID: 500, AccessHash: 900}},
		oneMessage: tgapi.Message{ID: 11},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}

	result, err := s.DeleteMessage(context.Background(), rpc.DeleteMessageRequest{ChannelID: 500, MessageID: 11})
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if !result.Deleted {
		t.Fatalf("result = %+v, want deletion confirmation", result)
	}
	if tg.oneMessageCalls != 1 || tg.oneMessageChannel.AccessHash != 900 || tg.oneMessageID != 11 {
		t.Fatalf("preflight = calls %d channel %+v message %d", tg.oneMessageCalls, tg.oneMessageChannel, tg.oneMessageID)
	}
	if tg.deleteCalls != 1 || tg.deleteChannel.AccessHash != 900 || tg.deleteMessageID != 11 {
		t.Fatalf("delete = calls %d channel %+v message %d", tg.deleteCalls, tg.deleteChannel, tg.deleteMessageID)
	}
}

func TestDeleteMessageDoesNotDeleteWhenPreflightFindsNoMessage(t *testing.T) {
	tg := &fakeTelegram{
		status:        tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs:       []tgapi.Channel{{ID: 500}},
		oneMessageErr: tgapi.ErrMessageNotFound,
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	_, err := s.DeleteMessage(context.Background(), rpc.DeleteMessageRequest{ChannelID: 500, MessageID: 11})
	var coded *rpc.Error
	if !errors.As(err, &coded) || coded.Code != rpc.CodeNotFound {
		t.Fatalf("error = %v, want not_found", err)
	}
	if tg.deleteCalls != 0 {
		t.Fatalf("delete calls = %d, want 0", tg.deleteCalls)
	}
}

func TestDeleteMessageRequiresAuthorization(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized}}
	s := newService(t, tg)
	if _, err := s.DeleteMessage(context.Background(), rpc.DeleteMessageRequest{ChannelID: 500, MessageID: 11}); !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Fatalf("error = %v, want ErrNeedsAuth", err)
	}
	if tg.oneMessageCalls != 0 || tg.deleteCalls != 0 {
		t.Fatalf("Telegram calls = preflight %d delete %d, want none", tg.oneMessageCalls, tg.deleteCalls)
	}
}
