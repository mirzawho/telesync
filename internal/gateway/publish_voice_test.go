package gateway_test

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// A recorded voice message stays a voice message when it is published through
// the HTTP route the "ارسال این پیام" dialog and the API both use. The route
// takes media.mode and never a media kind: what the attachment is is a fact
// about the source message, and the server reads it from the source rather than
// asking the caller.

var voiceRecording = []byte("OggS\x00\x02-opus-a-real-voice-recording")

var voiceWaveform = []byte{0x21, 0x45, 0x68, 0x8c, 0xa1, 0x88}

// voicePublishEnv is a publishing suite whose source message is a genuine voice
// recording: classified "voice" because the source document's
// documentAttributeAudio said so.
func voicePublishEnv(t *testing.T) (*accountEnv, *fakeWorker, channelstore.Channel) {
	t.Helper()
	env, worker, row := publishEnv(t)
	worker.mediaBytes = voiceRecording
	worker.mediaType = "audio/ogg"
	worker.mediaSize = int64(len(voiceRecording))
	worker.message = rpc.MessagePage{
		Channel: rpc.Channel{ID: 555, Title: "Source"},
		Messages: []rpc.Message{{
			ID: 9,
			Media: &rpc.Media{
				ID: "555:9", Type: "voice", Label: "پیام صوتی",
				MimeType: "audio/ogg", Size: int64(len(voiceRecording)),
				Duration: 5, Waveform: voiceWaveform,
			},
		}},
		Page: rpc.PageInfo{Page: 1, PerPage: 1, Total: 1},
	}
	return env, worker, row
}

// publishPath is the route a prepared post is published through.
//
// The publishing endpoint is being renamed in this tree while these tests run
// (POST /api/v1/telegram/{key}/messages -> /messages/copy), so the voice
// behaviour is pinned against whichever address the gateway actually serves it
// on. The behaviour under test is what the shared publisher does with the
// attachment, and it is the same behind either name.
func publishPath(t *testing.T, env *accountEnv, clientKey, body string) string {
	t.Helper()
	for _, path := range []string{
		"/api/v1/telegram/" + clientKey + "/messages/copy",
		"/api/v1/telegram/" + clientKey + "/messages",
	} {
		resp := env.send(t, defaultCaller, http.MethodPost, path, body)
		if resp.StatusCode != http.StatusNotFound {
			return path
		}
	}
	t.Fatal("no publishing route is registered")
	return ""
}

// voicePublishBody is the request body the dialog sends: media.mode is
// preserve_source and nothing says what kind of attachment it is.
func voicePublishBody(localID int64) string {
	return fmt.Sprintf(`{
		"source": {"local_channel_id": %d, "message_id": 9},
		"destination": {"channel_id": "555"},
		"content": {"text": "", "entities": []},
		"media": {"mode": "preserve_source"},
		"options": {"silent": false, "disable_web_page_preview": false}
	}`, localID)
}

// Test F. The publishing route preserves a voice message as a voice message,
// with its duration and waveform, without the caller ever naming a kind.
func TestPublishRouteKeepsARecordedVoiceMessageVoice(t *testing.T) {
	env, worker, row := voicePublishEnv(t)
	grantPublish(t, env, env.defaults)

	path := publishPath(t, env, "official", voicePublishBody(row.ID))
	resp := env.send(t, defaultCaller, http.MethodPost, path, voicePublishBody(row.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	got := worker.sentReq.Media
	if got == nil {
		t.Fatal("no attachment was published")
	}
	if got.Kind != "voice" {
		t.Fatalf("kind = %q, want voice — a recorded voice message must not be published as %q", got.Kind, got.Kind)
	}
	if got.Duration != 5 {
		t.Fatalf("duration = %d, want 5", got.Duration)
	}
	if !bytes.Equal(got.Waveform, voiceWaveform) {
		t.Fatalf("waveform = %v, want %v", got.Waveform, voiceWaveform)
	}
	if got.MimeType != "audio/ogg" {
		t.Fatalf("mime = %q, want audio/ogg — the recording is not re-encoded", got.MimeType)
	}
	if !bytes.Equal(got.Data, voiceRecording) {
		t.Fatal("the original recording was not what was published")
	}
}

// A recording the sender never named is published unnamed. The route must not
// put "پیام صوتی" on it as a file name: that is the label describing the media
// kind, and a name nobody chose is not a filename.
func TestPublishRouteNamesNoFileTheSenderNeverNamed(t *testing.T) {
	env, worker, row := voicePublishEnv(t)
	grantPublish(t, env, env.defaults)

	path := publishPath(t, env, "official", voicePublishBody(row.ID))
	resp := env.send(t, defaultCaller, http.MethodPost, path, voicePublishBody(row.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if name := worker.sentReq.Media.Name; name != "" {
		t.Fatalf("name = %q, want none", name)
	}
}

// The attachment is read through the source connection and sent through the
// destination's, and the voice metadata goes with it.
func TestPublishRouteSendsVoiceOntoTheDestinationConnection(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	source.mediaBytes = voiceRecording
	source.mediaType = "audio/ogg"
	source.mediaSize = int64(len(voiceRecording))
	source.message.Messages = []rpc.Message{{
		ID: 9,
		Media: &rpc.Media{
			ID: "555:9", Type: "voice", Label: "پیام صوتی",
			MimeType: "audio/ogg", Size: int64(len(voiceRecording)),
			Duration: 5, Waveform: voiceWaveform,
		},
	}}
	grantPublish(t, env, env.defaults)

	body := publishCrossBody(row.ID, mediaModePreserve, "")
	path := publishPath(t, env, "cat", body)
	resp := env.send(t, defaultCaller, http.MethodPost, path, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if dest.mediaCalls != 0 {
		t.Fatalf("destination media reads = %d, want 0 — the destination never reads the source",
			dest.mediaCalls)
	}
	got := dest.sentReq.Media
	if got == nil {
		t.Fatal("no attachment was published")
	}
	if got.Kind != "voice" {
		t.Fatalf("kind = %q, want voice", got.Kind)
	}
	if got.Duration != 5 {
		t.Fatalf("duration = %d, want 5", got.Duration)
	}
}

// An ordinary upload is published as a document and gains nothing from the
// voice path. A photo stays a photo.
func TestPublishRouteLeavesOtherMediaAlone(t *testing.T) {
	cases := []struct {
		name     string
		media    rpc.Media
		wantKind string
	}{
		{name: "a photo stays a photo",
			media:    rpc.Media{ID: "555:9", Type: "photo", MimeType: "image/jpeg"},
			wantKind: "photo"},
		{name: "an ordinary upload stays a document",
			media:    rpc.Media{ID: "555:9", Type: "document", Filename: "report.pdf", MimeType: "application/pdf"},
			wantKind: "document"},
		{name: "a music file stays audio",
			media:    rpc.Media{ID: "555:9", Type: "audio", Filename: "track-01.mp3", MimeType: "audio/mpeg", Duration: 214},
			wantKind: "audio"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, worker, row := publishEnv(t)
			worker.mediaBytes = []byte{0x01, 0x02, 0x03}
			worker.mediaType = tc.media.MimeType
			worker.message.Messages = []rpc.Message{{ID: 9, Media: &tc.media}}
			grantPublish(t, env, env.defaults)

			body := voicePublishBody(row.ID)
			path := publishPath(t, env, "official", body)
			resp := env.send(t, defaultCaller, http.MethodPost, path, body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
			}
			if got := worker.sentReq.Media.Kind; got != tc.wantKind {
				t.Fatalf("kind = %q, want %q", got, tc.wantKind)
			}
		})
	}
}
