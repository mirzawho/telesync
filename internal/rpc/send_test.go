package rpc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
)

// sendBackend records what a send actually carried.
type sendBackend struct {
	*fakeBackend
	got    rpc.SendRequest
	result rpc.SendResult
	err    error
}

func (s *sendBackend) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	s.got = req
	return s.result, s.err
}

func TestSendMessageRoundTripCarriesTheWholeRequest(t *testing.T) {
	backend := &sendBackend{
		fakeBackend: &fakeBackend{},
		result: rpc.SendResult{
			Message:   &rpc.Message{ID: 42, Text: "hello #news"},
			ChannelID: "123456789",
		},
	}
	client := rpcClient(t, backend, 0)

	got, err := client.SendMessage(context.Background(), rpc.SendRequest{
		ChannelID: 123456789,
		Text:      "hello #news",
		Entities:  []rpc.Entity{{Type: "hashtag", Offset: 6, Length: 4}},
		Silent:    true,
		NoWebpage: true,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if backend.got.ChannelID != 123456789 {
		t.Fatalf("channel = %d", backend.got.ChannelID)
	}
	if backend.got.Text != "hello #news" {
		t.Fatalf("text = %q", backend.got.Text)
	}
	if len(backend.got.Entities) != 1 || backend.got.Entities[0].Type != "hashtag" {
		t.Fatalf("entities = %+v", backend.got.Entities)
	}
	if !backend.got.Silent || !backend.got.NoWebpage {
		t.Fatalf("options = %+v", backend.got)
	}
	if got.Message == nil || got.Message.ID != 42 {
		t.Fatalf("result = %+v", got)
	}
	if got.ChannelID != "123456789" {
		t.Fatalf("channel id = %q", got.ChannelID)
	}
}

func TestSendMessageTravelsWithItsAttachmentBesideTheJSON(t *testing.T) {
	payload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	backend := &sendBackend{fakeBackend: &fakeBackend{}}
	client := rpcClient(t, backend, 0)

	_, err := client.SendMessage(context.Background(), rpc.SendRequest{
		ChannelID: 7,
		Text:      "caption",
		Media: &rpc.SendMedia{
			Kind:     "photo",
			Name:     "photo.jpg",
			MimeType: "image/jpeg",
			Data:     payload,
		},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if backend.got.Media == nil {
		t.Fatal("the attachment did not arrive")
	}
	if string(backend.got.Media.Data) != string(payload) {
		t.Fatalf("attachment bytes = %v", backend.got.Media.Data)
	}
	if backend.got.Media.Kind != "photo" || backend.got.Media.MimeType != "image/jpeg" {
		t.Fatalf("attachment = %+v", backend.got.Media)
	}
}

func TestSendMessageWithoutAnAttachmentSendsTextOnly(t *testing.T) {
	backend := &sendBackend{fakeBackend: &fakeBackend{}}
	client := rpcClient(t, backend, 0)
	if _, err := client.SendMessage(context.Background(), rpc.SendRequest{ChannelID: 7, Text: "plain"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if backend.got.Media != nil {
		t.Fatalf("media = %+v, want none", backend.got.Media)
	}
}

func TestSendMessageReportsAnOversizedAttachment(t *testing.T) {
	backend := &sendBackend{fakeBackend: &fakeBackend{}}
	client := rpcClient(t, backend, 0)
	big := make([]byte, rpc.MaxSendMediaBytes+1)
	_, err := client.SendMessage(context.Background(), rpc.SendRequest{
		ChannelID: 7,
		Media:     &rpc.SendMedia{Kind: "document", MimeType: "application/octet-stream", Data: big},
	})
	if !errors.Is(err, rpc.ErrMediaTooLarge) {
		t.Fatalf("err = %v, want ErrMediaTooLarge", err)
	}
}

func TestSendMessageRejectsARequestMethodOtherThanPost(t *testing.T) {
	raw := serveRPC(t, &sendBackend{fakeBackend: &fakeBackend{}})
	resp, err := raw.Get("http://worker/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("GET must not send a message")
	}
}

func TestSendMessageRejectsAPlainJSONBody(t *testing.T) {
	// The send route is multipart: a JSON body would be the unbounded path the
	// transfer is designed to avoid.
	raw := serveRPC(t, &sendBackend{fakeBackend: &fakeBackend{}})
	resp, err := raw.Post("http://worker/messages", "application/json",
		strings.NewReader(`{"channel_id":7,"text":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("a JSON body must not be accepted on the send route")
	}
}

func TestSendMessageMapsARefusalToAForeignCode(t *testing.T) {
	backend := &sendBackend{fakeBackend: &fakeBackend{}, err: rpc.ErrSendForbidden}
	client := rpcClient(t, backend, 0)
	_, err := client.SendMessage(context.Background(), rpc.SendRequest{ChannelID: 7})
	if !errors.Is(err, rpc.ErrSendForbidden) {
		t.Fatalf("err = %v, want ErrSendForbidden", err)
	}
}

func TestSendMessageMapsAFloodWait(t *testing.T) {
	backend := &sendBackend{fakeBackend: &fakeBackend{}, err: floodWait{seconds: 30}}
	client := rpcClient(t, backend, 0)
	_, err := client.SendMessage(context.Background(), rpc.SendRequest{ChannelID: 7})
	var coded *rpc.Error
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	if coded.Code != rpc.CodeRateLimited || coded.RetryAfter != 30 {
		t.Fatalf("error = %+v", coded)
	}
}

// floodWait stands in for the adapter's typed flood error.
type floodWait struct{ seconds int }

func (f floodWait) Error() string     { return "flood wait" }
func (f floodWait) FloodSeconds() int { return f.seconds }
