package gotdclient_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgtest"

	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/tgapi"
)

// A Telegram voice message is a document whose documentAttributeAudio carries
// voice=true. That one flag is the whole difference between a round play button
// and a download card, so it is read from the source and written back out on the
// destination. Nothing here looks at a MIME type, a file extension or a label to
// decide: audio/ogg is what a voice recording is stored as, and it is also what
// an ordinary OGG file is stored as.

var recording = []byte("OggS\x00\x02-opus-a-real-voice-recording")

// theWaveform is the source message's bitpacked amplitude samples. They are
// carried across untouched and never invented.
var theWaveform = []byte{0x21, 0x45, 0x68, 0x8c, 0xa1, 0x88}

// Test A. A recording is recognised from the voice flag on its audio attribute,
// and its duration and waveform come with it.
func TestMessageARecordedVoiceKeepsItsVoiceSemantics(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, documentServer(t, 51, "audio/ogg",
		&tg.DocumentAttributeAudio{Voice: true, Duration: 5, Waveform: theWaveform},
	))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.Message(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 51)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Media == nil {
		t.Fatal("media is missing")
	}
	if got.Media.Type != "voice" {
		t.Fatalf("type = %q, want voice — the voice flag, not the MIME type, decides", got.Media.Type)
	}
	if got.Media.Label != "پیام صوتی" {
		t.Fatalf("label = %q, want پیام صوتی", got.Media.Label)
	}
	if got.Media.MimeType != "audio/ogg" {
		t.Fatalf("mime = %q, want audio/ogg", got.Media.MimeType)
	}
	if got.Media.Duration != 5 {
		t.Fatalf("duration = %d, want 5", got.Media.Duration)
	}
	if !bytes.Equal(got.Media.Waveform, theWaveform) {
		t.Fatalf("waveform = %v, want %v", got.Media.Waveform, theWaveform)
	}
}

// Test B. A music file carries the same attribute with voice=false, and is
// audio — not a voice message, and not a plain file either.
func TestMessageAnAudioFileWithoutTheVoiceFlagStaysAudio(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, documentServer(t, 24, "audio/mpeg",
		&tg.DocumentAttributeAudio{Duration: 214},
		&tg.DocumentAttributeFilename{FileName: "track-01.mp3"},
	))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.Message(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 24)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Media == nil {
		t.Fatal("media is missing")
	}
	if got.Media.Type != "audio" {
		t.Fatalf("type = %q, want audio", got.Media.Type)
	}
	if got.Media.Duration != 214 {
		t.Fatalf("duration = %d, want 214", got.Media.Duration)
	}
	if got.Media.Waveform != nil {
		t.Fatalf("waveform = %v, want none: a music file carries no waveform", got.Media.Waveform)
	}
}

// Test C. An ordinary upload has no audio attribute at all and stays a
// document — even when its bytes happen to be an OGG file called voice.ogg.
func TestMessageAnOrdinaryDocumentStaysADocument(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, documentServer(t, 12, "audio/ogg",
		&tg.DocumentAttributeFilename{FileName: "voice.ogg"},
	))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := m.Message(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 12)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Media == nil {
		t.Fatal("media is missing")
	}
	if got.Media.Type != "document" {
		t.Fatalf("type = %q, want document: the name voice.ogg is not a voice message", got.Media.Type)
	}
	if got.Media.Duration != 0 {
		t.Fatalf("duration = %d, want none", got.Media.Duration)
	}
}

// The attribute is read directly as well, so the rule is pinned on its own.
func TestMediaDurationAndWaveformComeFromTheAudioAttribute(t *testing.T) {
	doc := &tg.MessageMediaDocument{Document: &tg.Document{
		MimeType: "audio/ogg",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "voice-message"},
			&tg.DocumentAttributeAudio{Voice: true, Duration: 5, Waveform: theWaveform},
		},
	}}
	if got := gotdclient.MediaDuration(doc); got != 5 {
		t.Fatalf("MediaDuration = %d, want 5", got)
	}
	if got := gotdclient.MediaWaveform(doc); !bytes.Equal(got, theWaveform) {
		t.Fatalf("MediaWaveform = %v, want %v", got, theWaveform)
	}
}

// A photo or a message with no audio attribute reports nothing rather than a
// made-up zero-length recording.
func TestMediaDurationIsAbsentWithoutAnAudioAttribute(t *testing.T) {
	photo := &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}
	if got := gotdclient.MediaDuration(photo); got != 0 {
		t.Fatalf("MediaDuration(photo) = %d, want 0", got)
	}
	if got := gotdclient.MediaWaveform(photo); got != nil {
		t.Fatalf("MediaWaveform(photo) = %v, want nil", got)
	}
	doc := &tg.MessageMediaDocument{Document: &tg.Document{
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{}},
	}}
	if got := gotdclient.MediaDuration(doc); got != 0 {
		t.Fatalf("MediaDuration(video) = %d, want 0", got)
	}
}

// capturedSend is the media request the fake destination actually received.
type capturedSend struct {
	mu      sync.Mutex
	request tg.MessagesSendMediaRequest
	got     bool
}

func (c *capturedSend) take() (tg.MessagesSendMediaRequest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request, c.got
}

// sendServer stands in for a destination MTProto server: it accepts the upload
// and records the media request exactly as it arrived on the wire.
func sendServer(t *testing.T, capture *capturedSend) func(*tgtest.Dispatcher) {
	t.Helper()
	return func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.UploadSaveFilePartRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.BoolTrue{})
		})
		d.HandleFunc(tg.MessagesSendMediaRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.MessagesSendMediaRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			capture.mu.Lock()
			capture.request, capture.got = request, true
			capture.mu.Unlock()
			return server.SendResult(req, &tg.UpdateShortSentMessage{ID: 4321, Date: 1700000000})
		})
	}
}

// sendThrough posts one attachment to the fake destination and returns the media
// request that arrived there.
func sendThrough(t *testing.T, media *tgapi.SendMedia) tg.InputMediaClass {
	t.Helper()
	capture := &capturedSend{}
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, sendServer(t, capture))
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := m.SendMessage(ctx, tgapi.ChannelRef{ID: 500}, tgapi.SendRequest{
		Text:  "",
		Media: media,
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	request, ok := capture.take()
	if !ok {
		t.Fatal("the destination received no media request")
	}
	return request.Media
}

// Test E. The outgoing MTProto request for a recording carries the voice flag,
// the duration and the waveform. The file name is not what makes it a voice
// message: this one is called "attachment" and is still a voice message.
func TestSendAVoiceMessageCarriesTheVoiceAttribute(t *testing.T) {
	input := sendThrough(t, &tgapi.SendMedia{
		Kind:     "voice",
		Name:     "attachment",
		MimeType: "audio/ogg",
		Data:     recording,
		Duration: 5,
		Waveform: theWaveform,
	})

	doc, ok := input.(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("media = %T, want *tg.InputMediaUploadedDocument", input)
	}
	if doc.ForceFile {
		t.Fatal("force_file is set: a voice message would arrive as a downloadable file")
	}
	if doc.MimeType != "audio/ogg" {
		t.Fatalf("mime = %q, want audio/ogg — the recording is not re-encoded", doc.MimeType)
	}
	attr := audioAttribute(t, doc.Attributes)
	if !attr.Voice {
		t.Fatal("voice is not set: the destination will show a download card instead of a voice message")
	}
	if attr.Duration != 5 {
		t.Fatalf("duration = %d, want 5", attr.Duration)
	}
	if !bytes.Equal(attr.Waveform, theWaveform) {
		t.Fatalf("waveform = %v, want %v", attr.Waveform, theWaveform)
	}
}

// A voice message carries no filename the sender never chose. Telegram's own
// voice messages have no documentAttributeFilename, and inventing one is what
// turns the recording into a file with a name.
func TestSendAVoiceMessageInventsNoFileName(t *testing.T) {
	input := sendThrough(t, &tgapi.SendMedia{
		Kind:     "voice",
		MimeType: "audio/ogg",
		Data:     recording,
		Duration: 5,
	})
	doc := input.(*tg.InputMediaUploadedDocument)
	for _, a := range doc.Attributes {
		if _, ok := a.(*tg.DocumentAttributeFilename); ok {
			t.Fatalf("a filename attribute was invented for a recording that had none: %v", a)
		}
	}
	if audioAttribute(t, doc.Attributes).Voice != true {
		t.Fatal("voice is not set")
	}
}

// Test J. Music is sent as audio and never as a voice message, whatever its
// container is.
func TestSendMusicCarriesAnAudioAttributeWithoutTheVoiceFlag(t *testing.T) {
	input := sendThrough(t, &tgapi.SendMedia{
		Kind:     "audio",
		Name:     "track-01.mp3",
		MimeType: "audio/mpeg",
		Data:     recording,
		Duration: 214,
	})
	doc, ok := input.(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("media = %T, want *tg.InputMediaUploadedDocument", input)
	}
	if doc.ForceFile {
		t.Fatal("force_file is set: music would arrive as a downloadable file")
	}
	attr := audioAttribute(t, doc.Attributes)
	if attr.Voice {
		t.Fatal("voice is set on a music file: a regular audio file must not become a voice message")
	}
	if attr.Duration != 214 {
		t.Fatalf("duration = %d, want 214", attr.Duration)
	}
}

// Test J. An ordinary upload is sent as a file, and gains no audio attribute.
func TestSendADocumentIsSentAsAFile(t *testing.T) {
	input := sendThrough(t, &tgapi.SendMedia{
		Kind:     "document",
		Name:     "report.pdf",
		MimeType: "application/pdf",
		Data:     recording,
	})
	doc, ok := input.(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("media = %T, want *tg.InputMediaUploadedDocument", input)
	}
	if !doc.ForceFile {
		t.Fatal("force_file is not set: an ordinary upload must stay a downloadable file")
	}
	for _, a := range doc.Attributes {
		if _, ok := a.(*tg.DocumentAttributeAudio); ok {
			t.Fatalf("an ordinary document was given an audio attribute: %v", a)
		}
	}
}

// Test J. A photo is sent as a photo and never as a document.
func TestSendAPhotoIsSentAsAPhoto(t *testing.T) {
	input := sendThrough(t, &tgapi.SendMedia{
		Kind:     "photo",
		Name:     "photo.jpg",
		MimeType: "image/jpeg",
		Data:     recording,
	})
	if _, ok := input.(*tg.InputMediaUploadedPhoto); !ok {
		t.Fatalf("media = %T, want *tg.InputMediaUploadedPhoto", input)
	}
}

// audioAttribute finds the documentAttributeAudio among an upload's attributes.
func audioAttribute(t *testing.T, attrs []tg.DocumentAttributeClass) *tg.DocumentAttributeAudio {
	t.Helper()
	for _, a := range attrs {
		if attr, ok := a.(*tg.DocumentAttributeAudio); ok {
			return attr
		}
	}
	t.Fatalf("no documentAttributeAudio in %v: this upload says nothing about what it is", attrs)
	return nil
}

// The reference implementation gotd ships is what the schema means; the
// attribute this adapter writes is checked against the same shape.
func TestTheVoiceAttributeMatchesTheSchemaGotdBuilds(t *testing.T) {
	attr := &tg.DocumentAttributeAudio{Voice: true, Duration: 5, Waveform: theWaveform}
	attr.SetFlags()
	if !attr.Flags.Has(10) {
		t.Fatal("flag 10 (voice) is not set: the wire encoding would not say voice")
	}
	// A music file sets none of the voice bits.
	music := &tg.DocumentAttributeAudio{Duration: 214}
	music.SetFlags()
	if music.Flags.Has(10) {
		t.Fatal("flag 10 (voice) is set on a music file")
	}
}
