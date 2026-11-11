package rpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
)

// A voice message's semantics are a handful of small facts — it is voice, how
// long it is, what its waveform is — and they must survive every boundary the
// publication crosses: the source message descriptor that leaves the worker,
// the JSON document beside the attachment bytes, and the outgoing request that
// arrives at the destination worker. The bytes alone are not enough: a
// destination given the same OGG file without the classification sends a file.

var theWaveform = []byte{0x21, 0x45, 0x68, 0x8c, 0xa1, 0x88}

// waveformBase64 is how those bytes appear in a JSON document: Go encodes a
// byte string as base64, and that is what the panel and any other client read.
const waveformBase64 = "IUVojKGI"

// Test D. The voice metadata crosses the RPC boundary with the attachment.
func TestASendCarriesTheVoiceMetadataAcrossTheRPCBoundary(t *testing.T) {
	payload := []byte("OggS\x00\x02-opus-voice-recording")
	backend := &sendBackend{fakeBackend: &fakeBackend{}}
	client := rpcClient(t, backend, 0)

	if _, err := client.SendMessage(context.Background(), rpc.SendRequest{
		ChannelID: 7,
		Text:      "",
		Media: &rpc.SendMedia{
			Kind:     "voice",
			MimeType: "audio/ogg",
			Data:     payload,
			Duration: 5,
			Waveform: theWaveform,
		},
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := backend.got.Media
	if got == nil {
		t.Fatal("no attachment arrived")
	}
	if got.Kind != "voice" {
		t.Fatalf("kind = %q, want voice", got.Kind)
	}
	if got.Duration != 5 {
		t.Fatalf("duration = %d, want 5", got.Duration)
	}
	if !bytes.Equal(got.Waveform, theWaveform) {
		t.Fatalf("waveform = %v, want %v", got.Waveform, theWaveform)
	}
	if got.MimeType != "audio/ogg" {
		t.Fatalf("mime = %q, want audio/ogg", got.MimeType)
	}
	if !bytes.Equal(got.Data, payload) {
		t.Fatal("the recording's bytes did not arrive")
	}
}

// A voice recording that the sender never named is sent unnamed, and that is
// carried across too: an empty name is not replaced by one this build made up.
func TestASendCarriesTheAbsenceOfAFilename(t *testing.T) {
	backend := &sendBackend{fakeBackend: &fakeBackend{}}
	client := rpcClient(t, backend, 0)

	if _, err := client.SendMessage(context.Background(), rpc.SendRequest{
		ChannelID: 7,
		Media: &rpc.SendMedia{
			Kind:     "voice",
			MimeType: "audio/ogg",
			Data:     []byte("OggS"),
			Duration: 5,
		},
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if name := backend.got.Media.Name; name != "" {
		t.Fatalf("name = %q, want none: a recording with no name must not gain one", name)
	}
}

// Test D. The source message descriptor keeps the classification and the audio
// attributes over its own JSON encoding, which is what the gateway and the
// publishing pipeline read.
func TestAMediaDescriptorKeepsItsVoiceSemanticsOverJSON(t *testing.T) {
	in := rpc.Media{
		ID:       "555:9",
		Type:     "voice",
		Label:    "پیام صوتی",
		MimeType: "audio/ogg",
		Size:     1331,
		Duration: 5,
		Waveform: theWaveform,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// The waveform is a byte string and JSON sends it base64. That is the wire
	// shape every client reads, so it is pinned rather than assumed.
	want := `"waveform":"` + waveformBase64 + `"`
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("the waveform is not base64 in the JSON: want %s in %s", want, raw)
	}
	var out rpc.Media
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Type != "voice" {
		t.Fatalf("type = %q, want voice", out.Type)
	}
	if out.Duration != 5 {
		t.Fatalf("duration = %d, want 5", out.Duration)
	}
	if !bytes.Equal(out.Waveform, theWaveform) {
		t.Fatalf("waveform = %v, want %v", out.Waveform, theWaveform)
	}
	if out.MimeType != "audio/ogg" {
		t.Fatalf("mime = %q, want audio/ogg", out.MimeType)
	}
	// The size is a byte count and the duration a length in time. They are
	// different facts and neither may stand in for the other.
	if out.Size != 1331 {
		t.Fatalf("size = %d, want 1331", out.Size)
	}
}

func TestVideoMetadataAndOpaqueAlbumIDSurviveJSON(t *testing.T) {
	in := rpc.Message{
		ID:        101,
		GroupedID: "9007199254740993",
		Media: &rpc.Media{
			Type: "video", MimeType: "video/mp4", Duration: 17,
			Width: 1920, Height: 1080, SupportsStreaming: true,
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"grouped_id":"9007199254740993"`)) {
		t.Fatalf("grouped id was not preserved as a string: %s", raw)
	}
	var out rpc.Message
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.GroupedID != in.GroupedID || out.Media == nil || out.Media.Type != "video" ||
		out.Media.Width != 1920 || out.Media.Height != 1080 || !out.Media.SupportsStreaming || out.Media.Duration != 17 {
		t.Fatalf("round trip = %+v", out)
	}
}

// A message that is not a voice recording reports none, and the JSON carries no
// empty fields claiming otherwise.
func TestAMediaDescriptorWithoutVoiceReportsNone(t *testing.T) {
	raw, err := json.Marshal(rpc.Media{ID: "555:9", Type: "document", Filename: "report.pdf",
		MimeType: "application/pdf", Size: 4096})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if bytes.Contains(raw, []byte("waveform")) {
		t.Fatalf("a document's JSON claims a waveform: %s", raw)
	}
	var out rpc.Media
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Type != "document" {
		t.Fatalf("type = %q, want document", out.Type)
	}
	if out.Duration != 0 || out.Waveform != nil {
		t.Fatalf("duration = %d, waveform = %v, want neither", out.Duration, out.Waveform)
	}
}

// An OGG file that is not voice keeps no voice semantics anywhere: the
// classification travels as the type it was given and nothing in the encoding
// turns it into a recording.
func TestAnAudioDescriptorIsNotTurnedIntoVoiceByTheEncoding(t *testing.T) {
	raw, err := json.Marshal(rpc.Media{ID: "555:9", Type: "audio", Filename: "track-01.mp3",
		MimeType: "audio/mpeg", Duration: 214})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out rpc.Media
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Type != "audio" {
		t.Fatalf("type = %q, want audio", out.Type)
	}
	if out.Duration != 214 {
		t.Fatalf("duration = %d, want 214", out.Duration)
	}
	if out.Waveform != nil {
		t.Fatalf("waveform = %v, want none", out.Waveform)
	}
}
