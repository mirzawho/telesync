package rpc_test

import (
	"context"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
)

type editBackend struct {
	*fakeBackend
	got    rpc.EditRequest
	result rpc.EditResult
}

func (b *editBackend) EditMessage(_ context.Context, req rpc.EditRequest) (rpc.EditResult, error) {
	b.got = req
	return b.result, nil
}

func TestEditMessageRoundTripPreservesTheTargetAndReplacement(t *testing.T) {
	backend := &editBackend{
		fakeBackend: &fakeBackend{},
		result:      rpc.EditResult{Message: &rpc.Message{ID: 11, Text: "replacement"}},
	}
	client := rpcClient(t, backend, 0)
	got, err := client.EditMessage(context.Background(), rpc.EditRequest{
		ChannelID: 98,
		MessageID: 11,
		Text:      "replacement",
		Entities:  []rpc.Entity{{Type: "bold", Offset: 0, Length: 11}},
		NoWebpage: true,
	})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if backend.got.ChannelID != 98 || backend.got.MessageID != 11 {
		t.Fatalf("target = %d/%d, want channel 98 message 11", backend.got.ChannelID, backend.got.MessageID)
	}
	if backend.got.Text != "replacement" || len(backend.got.Entities) != 1 || backend.got.Entities[0].Type != "bold" {
		t.Fatalf("replacement = %+v", backend.got)
	}
	if !backend.got.NoWebpage {
		t.Fatal("NoWebpage was lost")
	}
	if got.Message == nil || got.Message.ID != 11 || got.Message.Text != "replacement" {
		t.Fatalf("result = %+v; edit must retain message ID 11", got)
	}
}

func TestEditMessageAttachmentTravelsBesideItsRequest(t *testing.T) {
	attachment := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	backend := &editBackend{fakeBackend: &fakeBackend{}}
	client := rpcClient(t, backend, 0)
	_, err := client.EditMessage(context.Background(), rpc.EditRequest{
		ChannelID: 98,
		MessageID: 11,
		Text:      "caption",
		Media: &rpc.SendMedia{
			Kind:     "photo",
			Name:     "replacement.png",
			MimeType: "image/png",
			Data:     attachment,
		},
	})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if backend.got.Media == nil || backend.got.Media.Kind != "photo" || backend.got.Media.MimeType != "image/png" {
		t.Fatalf("media = %+v", backend.got.Media)
	}
	if string(backend.got.Media.Data) != string(attachment) {
		t.Fatalf("media bytes = %v, want %v", backend.got.Media.Data, attachment)
	}
}
