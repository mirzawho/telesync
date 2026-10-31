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

// readyStatus is a connection that is up and signed in.
func readyStatus() tgapi.Status {
	return tgapi.Status{State: tgapi.StateReady, ThisDC: 2}
}

// sendService builds a worker service over the recording adapter.
func sendService(tg worker.Telegram) *worker.Service {
	return worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official}, tg, worker.Options{})
}

// sendTelegram records what a send carried and answers with a scripted result.
type sendTelegram struct {
	*fakeTelegram
	got    tgapi.SendRequest
	gotRef tgapi.ChannelRef
	result tgapi.SendResult
	err    error
	calls  int
}

func (s *sendTelegram) SendMessage(_ context.Context, ref tgapi.ChannelRef, req tgapi.SendRequest) (tgapi.SendResult, error) {
	s.calls++
	s.got, s.gotRef = req, ref
	return s.result, s.err
}

func TestServiceSendMessagePassesThePreparedMessageThrough(t *testing.T) {
	tg := &sendTelegram{
		fakeTelegram: &fakeTelegram{status: readyStatus()},
		result: tgapi.SendResult{
			Message:   &tgapi.Message{ID: 42, Text: "hello"},
			ChannelID: 555,
		},
	}
	svc := sendService(tg)

	got, err := svc.SendMessage(context.Background(), rpc.SendRequest{
		ChannelID: 555,
		Text:      "hello",
		Entities:  []rpc.Entity{{Type: "bold", Offset: 0, Length: 5}},
		Media: &rpc.SendMedia{
			Kind:     "photo",
			Name:     "p.jpg",
			MimeType: "image/jpeg",
			Data:     []byte("bytes"),
		},
		Silent:    true,
		NoWebpage: true,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if tg.calls != 1 {
		t.Fatalf("adapter calls = %d", tg.calls)
	}
	if tg.gotRef.ID != 555 {
		t.Fatalf("ref = %+v", tg.gotRef)
	}
	if tg.got.Text != "hello" || len(tg.got.Entities) != 1 {
		t.Fatalf("request = %+v", tg.got)
	}
	if tg.got.Media == nil || tg.got.Media.Kind != "photo" || string(tg.got.Media.Data) != "bytes" {
		t.Fatalf("media = %+v", tg.got.Media)
	}
	if !tg.got.Silent || !tg.got.NoWebpage {
		t.Fatalf("options = %+v", tg.got)
	}
	if got.Message == nil || got.Message.ID != 42 {
		t.Fatalf("result = %+v", got)
	}
	if got.ChannelID != "555" {
		t.Fatalf("channel id = %q, want the destination's decimal id", got.ChannelID)
	}
}

func TestServiceSendMessageRefusesWorkWithoutAnAuthorizedAccount(t *testing.T) {
	tg := &sendTelegram{fakeTelegram: &fakeTelegram{
		status: tgapi.Status{State: tgapi.StateUnauthorized},
	}}
	svc := sendService(tg)
	if _, err := svc.SendMessage(context.Background(), rpc.SendRequest{ChannelID: 1}); !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Fatalf("err = %v, want ErrNeedsAuth", err)
	}
	if tg.calls != 0 {
		t.Fatal("an unauthorized worker must not touch Telegram")
	}
}

func TestServiceSendMessageReportsARefusedDestination(t *testing.T) {
	tg := &sendTelegram{
		fakeTelegram: &fakeTelegram{status: readyStatus()},
		err:          tgapi.ErrSendForbidden,
	}
	svc := sendService(tg)
	_, err := svc.SendMessage(context.Background(), rpc.SendRequest{ChannelID: 1})
	if codeOf(t, err) != rpc.CodeSendForbidden {
		t.Fatalf("err = %v, want code %q", err, rpc.CodeSendForbidden)
	}
}

func TestServiceSendMessageReportsAFloodWait(t *testing.T) {
	tg := &sendTelegram{
		fakeTelegram: &fakeTelegram{status: readyStatus()},
		err:          tgapi.FloodWait{Seconds: 12, Op: "messages.sendMessage"},
	}
	svc := sendService(tg)
	_, err := svc.SendMessage(context.Background(), rpc.SendRequest{ChannelID: 1})
	var coded *rpc.Error
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	if coded.Code != rpc.CodeRateLimited || coded.RetryAfter != 12 {
		t.Fatalf("error = %+v", coded)
	}
}

// codeOf reads the wire code a classified error carries.
func codeOf(t *testing.T, err error) string {
	t.Helper()
	var coded *rpc.Error
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	return coded.Code
}

func TestServiceSendMessageReportsAnUnresolvableDestination(t *testing.T) {
	tg := &sendTelegram{
		fakeTelegram: &fakeTelegram{status: readyStatus()},
		err:          tgapi.ErrChannelNotFound,
	}
	svc := sendService(tg)
	_, err := svc.SendMessage(context.Background(), rpc.SendRequest{ChannelID: 1})
	if codeOf(t, err) != rpc.CodeChannelNotFound {
		t.Fatalf("err = %v, want code %q", err, rpc.CodeChannelNotFound)
	}
}

func TestServiceSendMessageReportsATooLongMessage(t *testing.T) {
	tg := &sendTelegram{
		fakeTelegram: &fakeTelegram{status: readyStatus()},
		err:          tgapi.ErrMessageTooLong,
	}
	svc := sendService(tg)
	_, err := svc.SendMessage(context.Background(), rpc.SendRequest{ChannelID: 1})
	if codeOf(t, err) != rpc.CodeMessageTooLong {
		t.Fatalf("err = %v, want code %q", err, rpc.CodeMessageTooLong)
	}
}
