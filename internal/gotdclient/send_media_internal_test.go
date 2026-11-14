package gotdclient

import (
	"errors"
	"testing"

	"github.com/gotd/td/tg"

	// Named for what it pins: telegram/message's Voice(file) is the reference
	// shape for a voice attachment, and DefaultVoiceMIME is the one constant of
	// its this test needs to agree with.
	"github.com/gotd/td/telegram/message"

	"github.com/mirza/telesync/internal/tgapi"
)

// The wire representation of an attachment is the whole difference between a
// message a client plays and one it offers to download. It is decided by the
// attributes below and by nothing else — not a file name, not a content type
// and not the bytes. These tests pin each kind to the construction Telegram
// actually draws it with.

func uploadedFile() tg.InputFileClass {
	return &tg.InputFile{ID: 1, Parts: 1, Name: "upload"}
}

// attributesOf is the attribute list of a document-shaped media.
func attributesOf(t *testing.T, input tg.InputMediaClass) []tg.DocumentAttributeClass {
	t.Helper()
	doc, ok := input.(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("media = %T, want an uploaded document", input)
	}
	return doc.Attributes
}

func audioAttribute(t *testing.T, attrs []tg.DocumentAttributeClass) *tg.DocumentAttributeAudio {
	t.Helper()
	for _, a := range attrs {
		if audio, ok := a.(*tg.DocumentAttributeAudio); ok {
			return audio
		}
	}
	t.Fatal("the media carries no documentAttributeAudio")
	return nil
}

func filenameAttribute(t *testing.T, attrs []tg.DocumentAttributeClass) string {
	t.Helper()
	for _, a := range attrs {
		if name, ok := a.(*tg.DocumentAttributeFilename); ok {
			return name.FileName
		}
	}
	return ""
}

// Test A. A photograph is a photograph: it is uploaded as a photo and carries
// no document attributes at all.
func TestAPhotoIsSentAsAPhotograph(t *testing.T) {
	input, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{Kind: tgapi.SendMediaPhoto, MimeType: "image/jpeg"})
	if err != nil {
		t.Fatalf("sendMediaInput: %v", err)
	}
	if _, ok := input.(*tg.InputMediaUploadedPhoto); !ok {
		t.Fatalf("media = %T, want an uploaded photo", input)
	}
}

// Test B. A recording is a voice message. Its audio attribute says voice, and
// it carries no filename attribute — that absence is what makes a client draw a
// play button instead of offering the file for download.
func TestBARecordingIsSentAsAVoiceMessage(t *testing.T) {
	input, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{
		Kind: tgapi.SendMediaVoice, MimeType: "audio/ogg",
		Duration: 5, Waveform: []byte{0x21, 0x45},
	})
	if err != nil {
		t.Fatalf("sendMediaInput: %v", err)
	}
	attrs := attributesOf(t, input)
	audio := audioAttribute(t, attrs)
	if !audio.Voice {
		t.Fatal("the recording is not marked as a voice message; the destination will draw a download card")
	}
	if audio.Duration != 5 {
		t.Fatalf("duration = %d, want the length that was given", audio.Duration)
	}
	if len(audio.Waveform) != 2 {
		t.Fatalf("waveform = %d bytes, want the samples that were given", len(audio.Waveform))
	}
	if name := filenameAttribute(t, attrs); name != "" {
		t.Fatalf("the recording carries the file name %q; a name turns it into a downloadable file", name)
	}
	doc := input.(*tg.InputMediaUploadedDocument)
	if doc.ForceFile {
		t.Fatal("force_file means \"plain file, not media\" and is what turns a recording into a downloadable document")
	}
	// This is gotd's own DefaultVoiceMIME, taken from the same place its voice
	// builder takes it. A recording goes out as the format Telegram's voice
	// messages are stored in, and nothing here re-encodes it.
	if doc.MimeType != message.DefaultVoiceMIME {
		t.Fatalf("mime type = %q, want %q", doc.MimeType, message.DefaultVoiceMIME)
	}
	// Nothing but the audio attribute. A second attribute is a change to the
	// shape, and the shape is the whole difference.
	if len(attrs) != 1 {
		t.Fatalf("the recording carries %d attributes, want exactly the audio one: %v", len(attrs), attrs)
	}
}

// Test C. An ordinary audio file stays ordinary audio. It is the same bytes and
// the same content type as a recording; only the attribute says otherwise.
func TestCMusicIsSentAsAudioAndNotAsVoice(t *testing.T) {
	input, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{
		Kind: tgapi.SendMediaAudio, Name: "song.mp3", MimeType: "audio/mpeg", Duration: 183,
	})
	if err != nil {
		t.Fatalf("sendMediaInput: %v", err)
	}
	attrs := attributesOf(t, input)
	audio := audioAttribute(t, attrs)
	if audio.Voice {
		t.Fatal("an ordinary audio file was sent as a voice message")
	}
	if audio.Duration != 183 {
		t.Fatalf("duration = %d, want the length that was given", audio.Duration)
	}
	if name := filenameAttribute(t, attrs); name != "song.mp3" {
		t.Fatalf("file name = %q, want the sender's own", name)
	}
	// An audio file is media too. Forcing it to be a plain file is the same
	// mistake as making a recording one, and it is not undone by the attribute.
	if input.(*tg.InputMediaUploadedDocument).ForceFile {
		t.Fatal("force_file was set on an audio attachment")
	}
}

// Test D. A document is a document, and keeps the name the sender gave it.
func TestDDocumentKeepsTheSendersFileName(t *testing.T) {
	input, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{
		Kind: tgapi.SendMediaDocument, Name: "report.pdf", MimeType: "application/pdf",
	})
	if err != nil {
		t.Fatalf("sendMediaInput: %v", err)
	}
	attrs := attributesOf(t, input)
	if name := filenameAttribute(t, attrs); name != "report.pdf" {
		t.Fatalf("file name = %q, want the sender's own", name)
	}
	for _, a := range attrs {
		if _, ok := a.(*tg.DocumentAttributeAudio); ok {
			t.Fatal("a document carries an audio attribute")
		}
	}
}

// Test E. A video is a video: it carries its own attribute and its own name,
// and is never sent as a plain document.
func TestEVideoIsSentWithItsOwnAttribute(t *testing.T) {
	input, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{
		Kind: tgapi.SendMediaVideo, Name: "clip.mp4", MimeType: "video/mp4", Duration: 12,
		Width: 1280, Height: 720, SupportsStreaming: true,
	})
	if err != nil {
		t.Fatalf("sendMediaInput: %v", err)
	}
	attrs := attributesOf(t, input)
	var video *tg.DocumentAttributeVideo
	for _, a := range attrs {
		if v, ok := a.(*tg.DocumentAttributeVideo); ok {
			video = v
		}
	}
	if video == nil {
		t.Fatal("the video carries no documentAttributeVideo; it would arrive as a plain file")
	}
	if video.Duration != 12 || video.W != 1280 || video.H != 720 || !video.SupportsStreaming {
		t.Fatalf("video metadata = duration %v, %dx%d, streaming %v; want 12s 1280x720 streaming",
			video.Duration, video.W, video.H, video.SupportsStreaming)
	}
	if doc, ok := input.(*tg.InputMediaUploadedDocument); !ok || doc.ForceFile {
		t.Fatalf("media = %#v; video must not be force_file", input)
	}
	if name := filenameAttribute(t, attrs); name != "clip.mp4" {
		t.Fatalf("file name = %q, want the sender's own", name)
	}
}

func TestRoundVideoHasRoundMessageAttribute(t *testing.T) {
	input, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{
		Kind: tgapi.SendMediaRoundVideo, MimeType: "video/mp4", Duration: 8, Width: 480, Height: 480,
	})
	if err != nil {
		t.Fatalf("sendMediaInput: %v", err)
	}
	attrs := attributesOf(t, input)
	var video *tg.DocumentAttributeVideo
	for _, attr := range attrs {
		if got, ok := attr.(*tg.DocumentAttributeVideo); ok {
			video = got
		}
	}
	if video == nil || !video.RoundMessage || video.W != 480 || video.H != 480 {
		t.Fatalf("round-video attribute = %#v", video)
	}
}

// Test F. A kind this build cannot represent is refused. It is never quietly
// sent as a document, which would be a message the caller did not ask for.
func TestFAnUnsupportedKindIsRefusedNotDowngraded(t *testing.T) {
	_, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{Kind: "sticker"})
	if err == nil {
		t.Fatal("an unsupported media kind was accepted")
	}
	if !errors.Is(err, tgapi.ErrSendUnsupported) {
		t.Fatalf("err = %v, want ErrSendUnsupported", err)
	}
}

// A sticker is a document on the wire, but the sticker attribute is what makes
// Telegram render it as a sticker. Sending the same bytes as a named generic
// document is not a valid fallback.
func TestStickerUsesNativeStickerAttribute(t *testing.T) {
	input, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{
		Kind: "sticker", MimeType: "image/webp", Name: "sticker.webp",
	})
	if err != nil {
		t.Fatalf("sendMediaInput: %v", err)
	}
	doc, ok := input.(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("media = %T, want uploaded document with sticker semantics", input)
	}
	if doc.ForceFile {
		t.Fatal("native sticker media must not be force_file")
	}
	for _, attr := range doc.Attributes {
		if _, ok := attr.(*tg.DocumentAttributeSticker); ok {
			return
		}
	}
	t.Fatalf("outgoing attributes = %#v, want documentAttributeSticker", doc.Attributes)
}

func TestStickerFormatsRetainNativeFormatAttributes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mime      string
		animated  bool
		video     bool
		wantExtra string
	}{
		{"static webp", "image/webp", false, false, "image_size"},
		{"animated tgs", "application/x-tgsticker", true, false, "animated"},
		{"video webm", "video/webm", false, true, "video"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{
				Kind: tgapi.SendMediaSticker, MimeType: tc.mime,
				StickerAnimated: tc.animated, StickerVideo: tc.video,
				Width: 512, Height: 512, Duration: 2,
				StickerMask: true,
				StickerMaskCoords: &tgapi.StickerMaskCoords{
					N: 2, X: 0.5, Y: -1.25, Zoom: 1.4,
				},
			})
			if err != nil {
				t.Fatalf("sendMediaInput: %v", err)
			}
			doc, ok := input.(*tg.InputMediaUploadedDocument)
			if !ok || doc.ForceFile || doc.MimeType != tc.mime {
				t.Fatalf("uploaded media = %#v; want native non-force-file sticker in %q", input, tc.mime)
			}
			seenSticker, seenExtra := false, false
			for _, attr := range doc.Attributes {
				switch attr := attr.(type) {
				case *tg.DocumentAttributeSticker:
					seenSticker = true
					if _, ok := attr.Stickerset.(*tg.InputStickerSetEmpty); !ok {
						t.Fatalf("sticker set = %T, want InputStickerSetEmpty", attr.Stickerset)
					}
					if !attr.Mask {
						t.Fatal("mask sticker metadata was lost")
					}
					coords, ok := attr.GetMaskCoords()
					if !ok || coords.N != 2 || coords.X != 0.5 || coords.Y != -1.25 || coords.Zoom != 1.4 {
						t.Fatalf("mask coordinates = %+v, present=%v", coords, ok)
					}
				case *tg.DocumentAttributeAnimated:
					seenExtra = tc.wantExtra == "animated"
				case *tg.DocumentAttributeVideo:
					seenExtra = tc.wantExtra == "video" && attr.W == 512 && attr.H == 512
				case *tg.DocumentAttributeImageSize:
					seenExtra = tc.wantExtra == "image_size" && attr.W == 512 && attr.H == 512
				case *tg.DocumentAttributeFilename:
					t.Fatal("sticker upload must not be downgraded by a generic filename attribute")
				}
			}
			if !seenSticker || !seenExtra {
				t.Fatalf("attributes = %#v, want sticker and %s attributes", doc.Attributes, tc.wantExtra)
			}
		})
	}
}

func TestUnsupportedStickerFormatIsRefused(t *testing.T) {
	_, err := sendMediaInput(uploadedFile(), &tgapi.SendMedia{
		Kind: tgapi.SendMediaSticker, MimeType: "image/png",
	})
	if !errors.Is(err, tgapi.ErrSendUnsupported) {
		t.Fatalf("err = %v, want ErrSendUnsupported for an unhandled sticker format", err)
	}
}

func TestStickerSuccessRequiresNativeStickerInReturnedMessage(t *testing.T) {
	native := &tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{
		MimeType: "image/webp",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeSticker{Alt: "🙂", Stickerset: &tg.InputStickerSetEmpty{}},
		},
	}}}
	if err := verifyStickerResult(native); err != nil {
		t.Fatalf("verify native sticker: %v", err)
	}

	generic := &tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{
		MimeType: "image/webp",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "sticker.webp"},
		},
	}}}
	if err := verifyStickerResult(generic); !errors.Is(err, tgapi.ErrSendOutcomeUnknown) {
		t.Fatalf("verify generic file: %v, want outcome unknown", err)
	}
	if err := verifyStickerResult(nil); !errors.Is(err, tgapi.ErrSendOutcomeUnknown) {
		t.Fatalf("verify missing result: %v, want outcome unknown", err)
	}
}

// Test G. The content type an attachment goes out under is the caller's when
// it gave one. A fallback only fills a silence and never replaces a decision.
func TestGTheDeclaredContentTypeIsUsedWhenThereIsOne(t *testing.T) {
	for _, tc := range []struct {
		name     string
		media    tgapi.SendMedia
		fallback string
		want     string
	}{
		{"the caller's type wins", tgapi.SendMedia{Kind: tgapi.SendMediaVoice, MimeType: "audio/ogg"}, "audio/mpeg", "audio/ogg"},
		{"a silence is filled", tgapi.SendMedia{Kind: tgapi.SendMediaVoice, MimeType: ""}, "audio/ogg", "audio/ogg"},
		{"whitespace is a silence", tgapi.SendMedia{Kind: tgapi.SendMediaVoice, MimeType: "   "}, "audio/ogg", "audio/ogg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mimeTypeOr(&tc.media, tc.fallback); got != tc.want {
				t.Fatalf("mimeTypeOr = %q, want %q", got, tc.want)
			}
		})
	}
}

// Test H. The voice flag is on the wire, not only in the struct.
//
// A field that is set and not flagged is a recording that arrives as neither:
// the encoder writes what SetFlags marks, so this is the assertion that
// actually reaches Telegram's decoder.
func TestHTheVoiceFlagSurvivesEncoding(t *testing.T) {
	attr := &tg.DocumentAttributeAudio{Voice: true, Duration: 5, Waveform: []byte{0x21, 0x45}}
	attr.SetFlags()
	if attr.Flags&(1<<10) == 0 {
		t.Fatalf("flags = %#x: voice is not marked, so the destination has nothing to draw a play button from", attr.Flags)
	}
	if attr.Flags&(1<<2) == 0 {
		t.Fatalf("flags = %#x: a waveform was set and is not flagged", attr.Flags)
	}

	plain := &tg.DocumentAttributeAudio{Duration: 5}
	plain.SetFlags()
	if plain.Flags&(1<<10) != 0 {
		t.Fatalf("flags = %#x: an ordinary audio file is marked as a voice message", plain.Flags)
	}
}
