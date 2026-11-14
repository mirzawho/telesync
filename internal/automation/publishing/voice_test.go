package publishing_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// A voice recording is not an attachment that happens to be an OGG file. It is
// a document whose documentAttributeAudio carries voice=true, and that flag is
// what makes Telegram draw the round play button instead of a download card.
// These tests pin the whole way through the shared publishing pipeline: the
// source's own attributes decide what the destination is sent, no caller is
// ever asked to say "this is voice", and a publication that cannot keep the
// semantics fails rather than quietly posting a file.

// voiceBytes is the OGG/Opus recording the tests publish. Telegram's real voice
// messages are OGG/Opus and stay that way: nothing here re-encodes.
var voiceBytes = []byte("OggS\x00\x02-opus-voice-recording")

// voiceSource gives the world's source message a real voice attachment, exactly
// as a worker reports one: classified "voice" because the source document's
// documentAttributeAudio said so.
func voiceSource(w *world) {
	w.worker.page = rpc.MessagePage{
		Channel: rpc.Channel{ID: 555},
		Messages: []rpc.Message{{
			ID: 9, Text: "",
			Media: &rpc.Media{
				ID: "555:9", Type: "voice", Label: "پیام صوتی",
				MimeType: "audio/ogg", Size: int64(len(voiceBytes)),
			},
		}},
	}
	w.worker.media = voiceBytes
	w.worker.mediaType = "audio/ogg"
}

// requestFor is the manual publishing request for the world's source message,
// with media.mode=preserve_source — the option the "ارسال این پیام" dialog and
// the API both send.
func requestFor(w *world) publishing.Request {
	return publishing.Request{
		Source: publishing.RequestSource{
			LocalChannelID: w.source.ID, MessageID: 9,
		},
		Destination:    publishing.RequestDestination{ChannelID: "777"},
		Content:        publishing.RequestContent{Text: "hello", Entities: []rpc.Entity{}},
		Media:          publishing.RequestMedia{Mode: publishing.MediaPreserveSource},
		Options:        publishing.RequestOptions{},
		IdempotencyKey: "publish-voice-1",
	}
}

// actorFor is the account the publication runs as, with the grants it holds
// right now — the same thing the gateway builds from an authenticated request.
func actorFor(t *testing.T, w *world) publishing.Actor {
	t.Helper()
	auth, err := w.store.AuthorizerFor(context.Background(), w.user.ID)
	if err != nil {
		t.Fatalf("AuthorizerFor: %v", err)
	}
	return publishing.Actor{UserID: w.user.ID, Auth: auth}
}

// A source voice message is published as a voice message. The media mode asks
// for the source attachment to be preserved, and preserving it means preserving
// what it is — not just which bytes it has.
func TestManualPublishingKeepsARecordedVoiceMessageVoice(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	voiceSource(w)

	if _, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", requestFor(w)); err != nil {
		t.Fatalf("PublishTelegram: %v", err)
	}
	if len(w.worker.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(w.worker.sends))
	}
	got := w.worker.sends[0].Media
	if got == nil {
		t.Fatal("no attachment was sent")
	}
	if got.Kind != "voice" {
		t.Fatalf("kind = %q, want voice — a recorded voice message must not go out as %q", got.Kind, got.Kind)
	}
	if got.MimeType != "audio/ogg" {
		t.Fatalf("mime = %q, want audio/ogg", got.MimeType)
	}
	if !bytes.Equal(got.Data, voiceBytes) {
		t.Fatalf("data = %d bytes, want the original recording", len(got.Data))
	}
}

func TestManualPublishingSendsSelectedSourceAlbumAsOneNativeAlbum(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	w.worker.page = rpc.MessagePage{Channel: rpc.Channel{ID: 555}, Messages: []rpc.Message{
		{ID: 9, Text: "edited lead caption", GroupedID: "987", Entities: []rpc.Entity{{Type: "bold", Offset: 0, Length: 6}}, Media: &rpc.Media{ID: "555:9", Type: "photo", MimeType: "image/jpeg"}},
		{ID: 10, Text: "second caption", GroupedID: "987", Media: &rpc.Media{ID: "555:10", Type: "video", MimeType: "video/mp4", Duration: 3}},
	}}
	w.worker.media = []byte("source-media")
	w.worker.mediaType = "application/octet-stream"
	w.worker.albumResult = rpc.SendAlbumResult{ChannelID: "777", GroupedID: "44", Messages: []rpc.Message{{ID: 90}, {ID: 91}}}
	req := requestFor(w)
	req.Source.MessageIDs = []int{10, 9}
	req.Content.Text = "edited lead caption"
	req.Content.Entities = []rpc.Entity{{Type: "bold", Offset: 0, Length: 6}}
	result, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req)
	if err != nil {
		t.Fatalf("PublishTelegram: %v", err)
	}
	if len(w.worker.albums) != 1 || len(w.worker.sends) != 0 {
		t.Fatalf("native albums=%d standalone sends=%d", len(w.worker.albums), len(w.worker.sends))
	}
	items := w.worker.albums[0].Items
	if len(items) != 2 || items[0].Media.Kind != "photo" || items[1].Media.Kind != "video" ||
		items[0].Text != "edited lead caption" || items[1].Text != "second caption" ||
		items[0].Entities[0].Type != "bold" || items[0].RandomID == items[1].RandomID {
		t.Fatalf("album items = %+v", items)
	}
	if len(result.Messages) != 2 || result.GroupedID != "44" {
		t.Fatalf("result = %+v", result)
	}
}

// The publishing API takes no voice flag and must never need one. Whether the
// attachment is a voice recording is a fact about the source message, read from
// the source's own attributes, and a caller that says nothing about it is
// answered correctly.
func TestTheCallerIsNeverAskedToSayTheAttachmentIsVoice(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	voiceSource(w)

	req := requestFor(w)
	// The request carries no media kind at all — only the mode.
	req.Media = publishing.RequestMedia{Mode: publishing.MediaPreserveSource}

	if _, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req); err != nil {
		t.Fatalf("PublishTelegram: %v", err)
	}
	if got := w.worker.sends[0].Media.Kind; got != "voice" {
		t.Fatalf("kind = %q, want voice", got)
	}
}

// A background workflow publishes through the same pipeline as an interactive
// caller. There is one publisher here, and the voice semantics survive it; the
// AI polish that rewrites the text has no say over what the attachment is.
func TestWorkflowPublishingKeepsARecordedVoiceMessageVoice(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	voiceSource(w)

	req := w.request()
	// The workflow engine hands over text the AI polished. It changed the
	// words, and it must change nothing about the attachment.
	req.Text = "متن بازنویسی‌شده توسط هوش مصنوعی"

	if _, err := w.service.Publish(context.Background(), req); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(w.worker.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(w.worker.sends))
	}
	if w.worker.sends[0].Text != req.Text {
		t.Fatalf("text = %q, want the polished text", w.worker.sends[0].Text)
	}
	got := w.worker.sends[0].Media
	if got == nil {
		t.Fatal("no attachment was sent")
	}
	if got.Kind != "voice" {
		t.Fatalf("kind = %q, want voice", got.Kind)
	}
}

// The source and the destination are different connections: different
// accounts, different sessions, different file storage. The bytes come from the
// source connection and the upload goes out on the destination's, and the
// voice metadata travels with them.
func TestACrossConnectionTransferReadsSourceAndUploadsVoiceOntoTheDestination(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	voiceSource(w)

	source, dest := &fakeWorker{}, w.worker
	source.page = w.worker.page
	source.media, source.mediaType = voiceBytes, "audio/ogg"
	w.service.Workers = keyedRegistry{w.source.AccountID: source, "dest": dest}

	if _, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", requestFor(w)); err != nil {
		t.Fatalf("PublishTelegram: %v", err)
	}
	if source.mediaReads != 1 {
		t.Fatalf("source media reads = %d, want 1 — the bytes come from the source connection", source.mediaReads)
	}
	if dest.mediaReads != 0 {
		t.Fatalf("destination media reads = %d, want 0 — the destination is never asked for the source", dest.mediaReads)
	}
	if len(dest.sends) != 1 {
		t.Fatalf("destination sends = %d, want 1", len(dest.sends))
	}
	if len(source.sends) != 0 {
		t.Fatalf("source sends = %d, want 0 — only the destination posts", len(source.sends))
	}
	got := dest.sends[0].Media
	if got == nil {
		t.Fatal("no attachment was sent")
	}
	if got.Kind != "voice" {
		t.Fatalf("kind = %q, want voice", got.Kind)
	}
	if !bytes.Equal(got.Data, voiceBytes) {
		t.Fatal("the destination was not sent the source bytes")
	}
}

// A destination that will not take a voice message is told so. Publishing never
// answers a refusal by sending the recording again as a plain document: that
// would be a silent downgrade reported as a success.
func TestAVoiceSendThatIsRefusedIsAFailureNotADocument(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	voiceSource(w)
	w.worker.sendErr = rpc.NewError(rpc.CodeSendUnsupported, "this content cannot be sent to that destination", 0)

	_, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", requestFor(w))
	if err == nil {
		t.Fatal("a destination that refuses the voice message must not report a successful publication")
	}
	if code := publishing.CodeOf(err); code != publishing.CodePublishUnsupported {
		t.Fatalf("code = %q, want %q", code, publishing.CodePublishUnsupported)
	}
	if w.worker.sendCalls != 1 {
		t.Fatalf("send attempts = %d, want 1 — no second attempt as a plain document", w.worker.sendCalls)
	}
	if got := w.worker.sends[0].Media.Kind; got != "voice" {
		t.Fatalf("kind = %q, want voice: the attempt must be the voice send itself", got)
	}
}

// The classification is the source's own, and only voice is voice. A music file
// is audio, an ordinary upload is a document, and nothing that merely has an
// audio MIME type is turned into a voice recording.
func TestOnlyARecordingIsSentAsVoice(t *testing.T) {
	cases := []struct {
		name  string
		media rpc.Media
		want  string
	}{
		{name: "a recorded voice message is voice",
			media: rpc.Media{ID: "555:9", Type: "voice", MimeType: "audio/ogg"},
			want:  "voice"},
		{name: "a music file stays audio",
			media: rpc.Media{ID: "555:9", Type: "audio", Filename: "track-01.mp3", MimeType: "audio/mpeg"},
			want:  "audio"},
		{name: "an ordinary upload stays a document",
			media: rpc.Media{ID: "555:9", Type: "document", Filename: "report.pdf", MimeType: "application/pdf"},
			want:  "document"},
		{name: "a photo stays a photo",
			media: rpc.Media{ID: "555:9", Type: "photo", MimeType: "image/jpeg"},
			want:  "photo"},
		{name: "a video stays a video",
			media: rpc.Media{ID: "555:9", Type: "video", MimeType: "video/mp4", Duration: 12,
				Width: 1280, Height: 720, SupportsStreaming: true},
			want: "video"},
		{name: "an OGG file that is not voice is not voice",
			// The MIME type is exactly the one a voice recording has. The
			// source said "document", so it is a document.
			media: rpc.Media{ID: "555:9", Type: "document", Filename: "voice.ogg", MimeType: "audio/ogg"},
			want:  "document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
			w.worker.page = rpc.MessagePage{
				Channel:  rpc.Channel{ID: 555},
				Messages: []rpc.Message{{ID: 9, Text: "hello", Media: &tc.media}},
			}
			w.worker.media = voiceBytes
			w.worker.mediaType = tc.media.MimeType

			if _, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", requestFor(w)); err != nil {
				t.Fatalf("PublishTelegram: %v", err)
			}
			if len(w.worker.sends) != 1 {
				t.Fatalf("sends = %d, want 1", len(w.worker.sends))
			}
			if got := w.worker.sends[0].Media.Kind; got != tc.want {
				t.Fatalf("kind = %q, want %q", got, tc.want)
			}
			if tc.media.Type == "video" {
				got := w.worker.sends[0].Media
				if got.Duration != 12 || got.Width != 1280 || got.Height != 720 || !got.SupportsStreaming {
					t.Fatalf("video metadata = duration %d, %dx%d, streaming %v; want source metadata",
						got.Duration, got.Width, got.Height, got.SupportsStreaming)
				}
			}
		})
	}
}

// Dropping the attachment leaves the text alone and sends nothing at all; it is
// never a way to send a voice recording as text.
func TestAnOmittedMediaSendsNoAttachmentAndKeepsTheText(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	voiceSource(w)
	req := requestFor(w)
	req.Media = publishing.RequestMedia{Mode: publishing.MediaOmit}

	if _, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req); err != nil {
		t.Fatalf("PublishTelegram: %v", err)
	}
	if w.worker.sends[0].Media != nil {
		t.Fatal("media.mode=omit must not send an attachment")
	}
	if w.worker.sends[0].Text != "hello" {
		t.Fatalf("text = %q, want hello", w.worker.sends[0].Text)
	}
}

// The idempotency key is what makes a retry safe, and it is derived from the
// request — so re-publishing the same voice message under the same key posts
// once, whatever the media turned out to be.
func TestARetryUnderTheSameIdempotencyKeyPostsTheVoiceMessageOnce(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	voiceSource(w)
	req := requestFor(w)

	if _, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req); err != nil {
		t.Fatalf("first PublishTelegram: %v", err)
	}
	if _, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req); err != nil {
		t.Fatalf("second PublishTelegram: %v", err)
	}
	if w.worker.sendCalls != 1 {
		t.Fatalf("send attempts = %d, want 1 — a retry must not post the voice message twice", w.worker.sendCalls)
	}
}

// A source attachment the connection cannot read is a source failure and only
// that: nothing is sent, and the destination is not blamed.
func TestAnUnreadableSourceAttachmentSendsNothing(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	voiceSource(w)
	w.worker.mediaErr = errors.New("the source connection is gone")

	_, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", requestFor(w))
	if err == nil {
		t.Fatal("a source attachment that cannot be read must not publish")
	}
	if code := publishing.CodeOf(err); code != publishing.CodePublishSourceMedia {
		t.Fatalf("code = %q, want %q", code, publishing.CodePublishSourceMedia)
	}
	if w.worker.sendCalls != 0 {
		t.Fatalf("send attempts = %d, want 0", w.worker.sendCalls)
	}
}

// "other" is not enough semantic metadata to build an outgoing attachment.
// Even if a byte stream can be read, it must not become an empty post or be
// guessed into a generic document.
func TestMediaOnlyOtherMetadataReturnsTypedErrorWithoutSendingBlankMessage(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	var err error
	w.source, err = w.store.Upsert(context.Background(), channelstore.Channel{
		ClientKey: "source", ChannelID: 1004276399, PeerType: channelstore.PeerChannel,
		Title: "Source", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("Upsert source: %v", err)
	}
	w.worker.page = rpc.MessagePage{
		Channel: rpc.Channel{ID: 1004276399},
		Messages: []rpc.Message{{
			ID: 55, Text: "",
			Media: &rpc.Media{ID: "1004276399:55", Type: "other", Label: "پیوست", Size: 0},
		}},
	}
	w.worker.media = []byte("retrievable source attachment")
	w.worker.mediaType = "application/octet-stream"

	req := requestFor(w)
	req.Source.MessageID = 55
	req.Destination.ChannelID = "154"
	req.Content = publishing.RequestContent{Text: "", Entities: []rpc.Entity{}}
	req.Media = publishing.RequestMedia{Mode: publishing.MediaPreserveSource}

	_, err = w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req)
	if err == nil {
		t.Fatal("unclassified source media must not be reported as a successful empty message")
	}
	if code := publishing.CodeOf(err); code != publishing.CodePublishUnsupported {
		t.Fatalf("error code = %q, want %q: %v", code, publishing.CodePublishUnsupported, err)
	}
	if w.worker.sendCalls != 0 {
		t.Fatalf("send calls = %d, want 0; no blank message may be sent", w.worker.sendCalls)
	}
}

func TestCaptionlessMediaWithUnknownCachedSizeNeedsNoAttachmentID(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	attachment := []byte("actual document bytes")
	w.worker.page = rpc.MessagePage{
		Channel: rpc.Channel{ID: 555},
		Messages: []rpc.Message{{
			ID: 9, Text: "",
			Media: &rpc.Media{ID: "555:9", Type: "document", Size: 0, MimeType: "application/pdf"},
		}},
	}
	w.worker.media = attachment
	w.worker.mediaType = "application/pdf"
	req := requestFor(w)
	req.Content = publishing.RequestContent{Text: "", Entities: []rpc.Entity{}}
	req.Media = publishing.RequestMedia{Mode: publishing.MediaPreserveSource}

	result, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req)
	if err != nil {
		t.Fatalf("PublishTelegram: %v", err)
	}
	if len(w.worker.sends) != 1 || w.worker.sends[0].Media == nil {
		t.Fatalf("send = %+v, want source attachment", w.worker.sends)
	}
	if got := w.worker.sends[0].Media; got.Kind != "document" || string(got.Data) != string(attachment) {
		t.Fatalf("outgoing media = %+v, want original document bytes", got)
	}
	if result.Message == nil || result.Message.ID != 4321 {
		t.Fatalf("destination result = %+v, want confirmed destination message", result)
	}
}

func TestZeroByteMediaCannotBePublishedAsAnEmptyMessage(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	w.worker.page = rpc.MessagePage{Channel: rpc.Channel{ID: 555}, Messages: []rpc.Message{{
		ID: 9, Media: &rpc.Media{ID: "555:9", Type: "document", Size: 0},
	}}}
	w.worker.media = []byte{}
	req := requestFor(w)
	req.Content = publishing.RequestContent{Text: "", Entities: []rpc.Entity{}}
	req.Media = publishing.RequestMedia{Mode: publishing.MediaPreserveSource}

	_, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req)
	if err == nil || publishing.CodeOf(err) != publishing.CodePublishSourceMedia {
		t.Fatalf("error = %v, code = %q; want source-media unavailable", err, publishing.CodeOf(err))
	}
	if w.worker.sendCalls != 0 {
		t.Fatalf("send calls = %d, want 0", w.worker.sendCalls)
	}
}

func TestCaptionlessStickerKeepsNativeMetadataThroughPublishing(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	w.worker.page = rpc.MessagePage{
		Channel: rpc.Channel{ID: 555},
		Messages: []rpc.Message{{
			ID: 9,
			Media: &rpc.Media{
				ID: "555:9", Type: "sticker", MimeType: "image/webp",
				Width: 512, Height: 512, StickerEmoji: "🙂", StickerMask: true,
				StickerMaskCoords: &rpc.StickerMaskCoords{N: 2, X: 0.5, Y: -1.25, Zoom: 1.4},
			},
		}},
	}
	w.worker.media = []byte("original webp sticker bytes")
	w.worker.mediaType = "image/webp"
	req := requestFor(w)
	req.Content = publishing.RequestContent{Text: "", Entities: []rpc.Entity{}}

	if _, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req); err != nil {
		t.Fatalf("PublishTelegram: %v", err)
	}
	if len(w.worker.sends) != 1 {
		t.Fatalf("send calls = %d, want 1", len(w.worker.sends))
	}
	got := w.worker.sends[0].Media
	if got == nil || got.Kind != "sticker" || got.MimeType != "image/webp" ||
		got.StickerEmoji != "🙂" || !got.StickerMask || got.StickerMaskCoords == nil ||
		got.StickerMaskCoords.N != 2 || got.Width != 512 || got.Height != 512 {
		t.Fatalf("outgoing sticker media = %+v", got)
	}
}

func TestUnverifiedStickerResultIsUnknownAndIsNotRetried(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	w.worker.page = rpc.MessagePage{Channel: rpc.Channel{ID: 555}, Messages: []rpc.Message{{
		ID: 9, Media: &rpc.Media{ID: "555:9", Type: "sticker", MimeType: "image/webp", StickerEmoji: "🙂"},
	}}}
	w.worker.media = []byte("webp sticker")
	w.worker.mediaType = "image/webp"
	w.worker.sendErr = rpc.NewError(rpc.CodeSendOutcomeUnknown,
		"The destination send result could not be verified.", 0)
	req := requestFor(w)
	req.Content = publishing.RequestContent{Text: "", Entities: []rpc.Entity{}}

	_, err := w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req)
	if err == nil || publishing.CodeOf(err) != publishing.CodePublishUnknown {
		t.Fatalf("first error = %v, code = %q, want publish_outcome_unknown", err, publishing.CodeOf(err))
	}
	_, err = w.service.PublishTelegram(context.Background(), actorFor(t, w), "dest", req)
	if err == nil || publishing.CodeOf(err) != publishing.CodePublishUnknown {
		t.Fatalf("retry error = %v, code = %q, want existing unknown operation", err, publishing.CodeOf(err))
	}
	if w.worker.sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1; an ambiguous sticker send is never retried blindly", w.worker.sendCalls)
	}
}
