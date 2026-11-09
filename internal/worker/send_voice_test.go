package worker_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// Test D, at the worker boundary. The wire request is decoded into the
// library-independent shape the adapter is handed, and the voice facts must
// arrive with it: the kind, the length and the waveform are what the
// destination's media request is built from, and the bytes alone are not enough
// — the same OGG file without them is published as a plain file.

func TestServiceSendMessageCarriesTheVoiceSemanticsToTheAdapter(t *testing.T) {
	recording := []byte("OggS\x00\x02-opus-a-real-voice-recording")
	waveform := []byte{0x21, 0x45, 0x68, 0x8c, 0xa1, 0x88}
	tg := &sendTelegram{
		fakeTelegram: &fakeTelegram{status: readyStatus()},
		result:       tgapi.SendResult{ChannelID: 555},
	}
	svc := sendService(tg)

	if _, err := svc.SendMessage(context.Background(), rpc.SendRequest{
		ChannelID: 555,
		Media: &rpc.SendMedia{
			Kind:     "voice",
			MimeType: "audio/ogg",
			Data:     recording,
			Duration: 5,
			Waveform: waveform,
		},
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := tg.got.Media
	if got == nil {
		t.Fatal("no attachment reached the adapter")
	}
	if got.Kind != "voice" {
		t.Fatalf("kind = %q, want voice", got.Kind)
	}
	if got.Duration != 5 {
		t.Fatalf("duration = %d, want 5", got.Duration)
	}
	if !bytes.Equal(got.Waveform, waveform) {
		t.Fatalf("waveform = %v, want %v", got.Waveform, waveform)
	}
	if got.MimeType != "audio/ogg" {
		t.Fatalf("mime = %q, want audio/ogg", got.MimeType)
	}
	if !bytes.Equal(got.Data, recording) {
		t.Fatal("the recording's bytes did not reach the adapter")
	}
	if got.Name != "" {
		t.Fatalf("name = %q, want none: a recording with no name gains none in transit", got.Name)
	}
}

// A music file keeps its own kind and gains no voice semantics anywhere in the
// crossing.
func TestServiceSendMessageLeavesANonVoiceAudioFileAlone(t *testing.T) {
	tg := &sendTelegram{
		fakeTelegram: &fakeTelegram{status: readyStatus()},
		result:       tgapi.SendResult{ChannelID: 555},
	}
	svc := sendService(tg)

	if _, err := svc.SendMessage(context.Background(), rpc.SendRequest{
		ChannelID: 555,
		Media: &rpc.SendMedia{
			Kind:     "audio",
			Name:     "track-01.mp3",
			MimeType: "audio/mpeg",
			Data:     []byte{0x01, 0x02},
			Duration: 214,
		},
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := tg.got.Media
	if got.Kind != "audio" {
		t.Fatalf("kind = %q, want audio", got.Kind)
	}
	if got.Name != "track-01.mp3" {
		t.Fatalf("name = %q, want track-01.mp3", got.Name)
	}
	if got.Waveform != nil {
		t.Fatalf("waveform = %v, want none", got.Waveform)
	}
}
