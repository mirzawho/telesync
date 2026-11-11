package rpc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
)

func TestDeleteMessageRoundTripCarriesTheSelectedChannelAndMessage(t *testing.T) {
	backend := &fakeBackend{deleted: rpc.DeleteMessageResult{Deleted: true}}
	client := rpcClient(t, backend, 0)
	want := rpc.DeleteMessageRequest{ChannelID: 98, MessageID: 11}

	got, err := client.DeleteMessage(context.Background(), want)
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if got != (rpc.DeleteMessageResult{Deleted: true}) {
		t.Fatalf("result = %+v", got)
	}
	if backend.gotDelete != want {
		t.Fatalf("request = %+v, want %+v", backend.gotDelete, want)
	}
}

func TestDeleteMessageRoundTripPreservesTypedTelegramRefusal(t *testing.T) {
	backend := &fakeBackend{deleteE: rpc.NewError(rpc.CodeMessageDeleteForbidden, "safe refusal", 0)}
	client := rpcClient(t, backend, 0)
	_, err := client.DeleteMessage(context.Background(), rpc.DeleteMessageRequest{ChannelID: 98, MessageID: 11})
	var coded *rpc.Error
	if !errors.As(err, &coded) || coded.Code != rpc.CodeMessageDeleteForbidden {
		t.Fatalf("error = %v, want typed %q", err, rpc.CodeMessageDeleteForbidden)
	}
}
