package publishing_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// A direct send is the other way to get a message into a channel: one the
// caller wrote, with an attachment they uploaded, and no source message behind
// it. It shares the outgoing door with a publish — the destination, its
// permissions, the message that is built and the meaning of a failure — and it
// shares those completely. What it does not have is a source, and nothing here
// may quietly invent one.

// Test A. A message a caller composed is sent, with no source message anywhere
// in the request.
func TestADirectSendNeedsNoSourceMessage(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)

	got, err := w.service.SendDirect(context.Background(), actorFor(t, w), "dest", w.dest.ChannelID,
		rpc.SendRequest{Text: "a message that came from nowhere"})
	if err != nil {
		t.Fatalf("SendDirect: %v", err)
	}
	if got.Outcome != channelstore.PublishSucceeded {
		t.Fatalf("outcome = %q, want %q", got.Outcome, channelstore.PublishSucceeded)
	}
	if len(w.worker.sends) != 1 {
		t.Fatalf("%d sends were made, want 1", len(w.worker.sends))
	}
	if w.worker.sends[0].Text != "a message that came from nowhere" {
		t.Fatalf("sent text = %q", w.worker.sends[0].Text)
	}
	if got.Message == nil || got.Message.ID == 0 {
		t.Fatal("the destination's message was not reported back")
	}
}

// Test B. What is sent is what was authorized. The channel is taken from the
// argument that was checked, never from the prepared post beside it, so a post
// cannot be pointed somewhere the caller was never cleared for.
func TestBTheAuthorizedDestinationIsTheOneThatReceivesIt(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)

	_, err := w.service.SendDirect(context.Background(), actorFor(t, w), "dest", w.dest.ChannelID,
		rpc.SendRequest{ChannelID: 999999, Text: "here"})
	if err != nil {
		t.Fatalf("SendDirect: %v", err)
	}
	if len(w.worker.sends) != 1 {
		t.Fatalf("%d sends were made, want 1", len(w.worker.sends))
	}
	if got := w.worker.sends[0].ChannelID; got != w.dest.ChannelID {
		t.Fatalf("the message went to channel %d; %d was authorized", got, w.dest.ChannelID)
	}
}

// Test C. A recording stays a recording. The kind is the caller's and it
// reaches the wire unchanged — which is the difference between a play button
// and a download card on the far end.
func TestCTheAttachmentGoesOutAsTheKindItWasSentAs(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)

	_, err := w.service.SendDirect(context.Background(), actorFor(t, w), "dest", w.dest.ChannelID,
		rpc.SendRequest{
			Text: "",
			Media: &rpc.SendMedia{
				Kind: "voice", MimeType: "audio/ogg", Data: []byte("OggS"),
				Duration: 5,
			},
		})
	if err != nil {
		t.Fatalf("SendDirect: %v", err)
	}
	if len(w.worker.sends) != 1 {
		t.Fatalf("%d sends were made, want 1", len(w.worker.sends))
	}
	media := w.worker.sends[0].Media
	if media == nil {
		t.Fatal("the attachment was not sent")
	}
	if media.Kind != "voice" {
		t.Fatalf("kind = %q, want %q — a recording must not travel as a document", media.Kind, "voice")
	}
	if media.Duration != 5 {
		t.Fatalf("duration = %d, want the length the caller gave", media.Duration)
	}
}

// Test D. A destination that refuses the message is a failure with the reason
// it gave. It is never reported as a success that has not happened yet.
func TestDARefusalIsAFailureWithTheReasonItGave(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	w.worker.sendErr = rpc.ErrSendForbidden

	_, err := w.service.SendDirect(context.Background(), actorFor(t, w), "dest", w.dest.ChannelID,
		rpc.SendRequest{Text: "hello"})
	if err == nil {
		t.Fatal("a refused send was reported as a success")
	}
	var perr *publishing.Error
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want a publishing error", err)
	}
	if perr.Code != publishing.CodePublishDenied {
		t.Fatalf("code = %q, want %q", perr.Code, publishing.CodePublishDenied)
	}
}

// Test E. A send that never got an answer is unknown, and unknown is not
// failed. Calling it failed is what invites the retry that posts the message a
// second time.
func TestEAnUnansweredSendIsUnknownNotFailed(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)
	w.worker.sendErr = rpc.ErrUnavailable

	got, err := w.service.SendDirect(context.Background(), actorFor(t, w), "dest", w.dest.ChannelID,
		rpc.SendRequest{Text: "hello"})
	if err == nil {
		t.Fatal("a send with no answer was reported as a success")
	}
	if got.Outcome != channelstore.PublishUnknown {
		t.Fatalf("outcome = %q, want %q — the message may already be there", got.Outcome, channelstore.PublishUnknown)
	}
	if got.Outcome == channelstore.PublishFailed {
		t.Fatal("an unconfirmed send was recorded as certainly not delivered")
	}
}

// Test F. Sending is not a privileged path. Without the publishing permission
// nothing is sent, whatever the caller can see.
func TestFWithoutThePublishingPermissionNothingIsSent(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionChannelsView)

	_, err := w.service.SendDirect(context.Background(), actorFor(t, w), "dest", w.dest.ChannelID,
		rpc.SendRequest{Text: "hello"})
	if err == nil {
		t.Fatal("a caller with no publishing permission sent a message")
	}
	if len(w.worker.sends) != 0 {
		t.Fatalf("%d sends were made", len(w.worker.sends))
	}
	var perr *publishing.Error
	if !errors.As(err, &perr) || perr.Code != publishing.CodePublishForbidden {
		t.Fatalf("err = %v, want %q", err, publishing.CodePublishForbidden)
	}
}

// Test G. A destination the account may not name is refused before anything is
// sent, and the connection is never reached.
func TestGAnUnauthorizedDestinationIsRefusedBeforeAnySend(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish)

	_, err := w.service.SendDirect(context.Background(), actorFor(t, w), "dest", w.dest.ChannelID,
		rpc.SendRequest{Text: "hello"})
	if err == nil {
		t.Fatal("a message went to a channel the account may not name")
	}
	if len(w.worker.sends) != 0 {
		t.Fatalf("%d sends were made", len(w.worker.sends))
	}
	var perr *publishing.Error
	if !errors.As(err, &perr) || perr.Code != publishing.CodePublishDestDenied {
		t.Fatalf("err = %v, want %q", err, publishing.CodePublishDestDenied)
	}
}

// Test H. An unknown connection is an error, never another client's: the key
// names one account and a missing one is not a gap to fill in.
func TestHAnUnknownConnectionIsAnErrorNotAFallback(t *testing.T) {
	w := newWorld(t)
	w.grant(t, channelstore.PermissionContentPublish, channelstore.PermissionChannelsView)

	// A registry that serves exactly one key, so "there is no such client" is a
	// fact rather than a lookup that always succeeds.
	service := &publishing.Service{
		Workers:   keyedRegistry{"dest": w.worker},
		Channels:  w.store,
		Publishes: w.store,
		Actors:    w.store,
	}

	_, err := service.SendDirect(context.Background(), actorFor(t, w), "no-such-client", w.dest.ChannelID,
		rpc.SendRequest{Text: "hello"})
	if err == nil {
		t.Fatal("an unknown connection was answered by another one")
	}
	if len(w.worker.sends) != 0 {
		t.Fatalf("%d sends were made on the wrong connection", len(w.worker.sends))
	}
}

// --- validation ---------------------------------------------------------

func intPtr(v int) *int { return &v }

// A request is checked before anything is staged or recorded, so a caller who
// sent something that cannot become a message is told now rather than after an
// operation they cannot cancel exists.

func TestIValidDirectRequestsAreAccepted(t *testing.T) {
	cases := []struct {
		name    string
		req     publishing.DirectSendRequest
		hasFile bool
	}{
		{"text alone", publishing.DirectSendRequest{Text: "hello"}, false},
		{"text and options", publishing.DirectSendRequest{Text: "hello",
			Options: publishing.RequestOptions{Silent: true, DisableWebPagePreview: true}}, false},
		{"a captioned photograph", publishing.DirectSendRequest{Text: "a picture",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaPhoto}}, true},
		{"a document", publishing.DirectSendRequest{Text: "",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaDocument}}, true},
		{"a recording with a length", publishing.DirectSendRequest{Text: "",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaVoice, DurationSeconds: intPtr(5)}}, true},
		{"music with a length", publishing.DirectSendRequest{Text: "",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaAudio, DurationSeconds: intPtr(183)}}, true},
		{"a video", publishing.DirectSendRequest{Text: "clip",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaVideo}}, true},
		{"formatting inside the text", publishing.DirectSendRequest{Text: "hello",
			Entities: []rpc.Entity{{Type: tgapi.EntityBold, Offset: 0, Length: 5}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := publishing.ValidateDirect(tc.req, tc.hasFile); err != nil {
				t.Fatalf("ValidateDirect: %v", err)
			}
		})
	}
}

func TestJInvalidDirectRequestsAreRefusedWithAReason(t *testing.T) {
	long := strings.Repeat("x", tgapi.MaxCaptionRunes+1)
	cases := []struct {
		name    string
		req     publishing.DirectSendRequest
		hasFile bool
	}{
		{"neither text nor an attachment", publishing.DirectSendRequest{Text: "   "}, false},
		{"a file with no kind", publishing.DirectSendRequest{Text: "hi"}, true},
		{"a kind with no file", publishing.DirectSendRequest{Text: "hi",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaPhoto}}, false},
		{"a kind nobody recognises", publishing.DirectSendRequest{Text: "hi",
			Media: &publishing.DirectSendMedia{Kind: "sticker"}}, true},
		{"a caption longer than a caption may be", publishing.DirectSendRequest{Text: long,
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaDocument}}, true},
		{"a body longer than a body may be", publishing.DirectSendRequest{
			Text: strings.Repeat("x", tgapi.MaxMessageRunes+1)}, false},
		{"a span outside the text", publishing.DirectSendRequest{Text: "hi",
			Entities: []rpc.Entity{{Type: tgapi.EntityBold, Offset: 0, Length: 9}}}, false},
		{"a span starting beyond the text", publishing.DirectSendRequest{Text: "hi",
			Entities: []rpc.Entity{{Type: tgapi.EntityBold, Offset: 5, Length: 1}}}, false},
		{"a span of a kind nobody recognises", publishing.DirectSendRequest{Text: "hi",
			Entities: []rpc.Entity{{Type: "sparkle", Offset: 0, Length: 2}}}, false},
		{"a length beside a photograph", publishing.DirectSendRequest{Text: "",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaPhoto, DurationSeconds: intPtr(5)}}, true},
		{"a negative length", publishing.DirectSendRequest{Text: "",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaVoice, DurationSeconds: intPtr(-1)}}, true},
		{"a length longer than a day", publishing.DirectSendRequest{Text: "",
			Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaVoice,
				DurationSeconds: intPtr(24*60*60 + 1)}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := publishing.ValidateDirect(tc.req, tc.hasFile); err == nil {
				t.Fatal("ValidateDirect accepted a request that cannot become a message")
			}
		})
	}
}

// Test K. A caption is not a message body. The shorter limit is the one that
// applies beside an attachment, and the reason says which one was exceeded so
// the fix is obvious.
func TestKACaptionIsHeldToTheCaptionLimit(t *testing.T) {
	atBodyLimit := strings.Repeat("x", tgapi.MaxMessageRunes)
	err := publishing.ValidateDirect(publishing.DirectSendRequest{Text: atBodyLimit,
		Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaDocument}}, true)
	if err == nil {
		t.Fatal("a body-length caption was accepted beside an attachment")
	}
	if !strings.Contains(err.Error(), "caption") {
		t.Fatalf("the reason is %q; it should name the limit that was exceeded", err)
	}
}

// Test L. A caption exactly at the caption limit is a caption, not an
// over-long one.
func TestLACaptionAtTheLimitIsAccepted(t *testing.T) {
	atLimit := strings.Repeat("x", tgapi.MaxCaptionRunes)
	if err := publishing.ValidateDirect(publishing.DirectSendRequest{Text: atLimit,
		Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaDocument}}, true); err != nil {
		t.Fatalf("ValidateDirect: %v", err)
	}
}
