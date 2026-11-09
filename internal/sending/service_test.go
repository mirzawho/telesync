package sending_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/automation/queue"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/sending"
	"github.com/mirza/telesync/internal/staging"
)

// An accepted send and a delivered message are two different moments. This
// package is the whole distance between them: what survives a restart, what
// makes a repeated request safe, and what stops a retry from posting the same
// message twice.

// voiceBytes is an OGG/Opus recording. Telegram's own voice messages are stored
// this way and stay this way: nothing here re-encodes.
var voiceBytes = []byte("OggS\x00\x02-opus-a-voice-recording")

// jpegBytes sniffs as image/jpeg, which is what makes a photograph a photograph
// to the checks below.
var jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")

// pdfBytes sniffs as application/pdf: a document, and never a photograph.
var pdfBytes = []byte("%PDF-1.7\n1 0 obj\n<< >>\nendobj\n")

// sendWorker is one Telegram connection that records what it was asked to send.
type sendWorker struct {
	sends   []rpc.SendRequest
	sendErr error
	// sendsAfter is the error to answer with once sends have reached this many,
	// so a test can fail the first attempt and succeed the second.
	failFirst int
}

func (w *sendWorker) Message(context.Context, int64, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{}, errors.New("not used")
}

func (w *sendWorker) Media(context.Context, int64, int, int, rpc.MediaSlice) (rpc.MediaStream, error) {
	return rpc.MediaStream{}, errors.New("not used")
}

func (w *sendWorker) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	return rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Permissions: rpc.Permissions{New: true, Read: true},
	}}, nil
}

func (w *sendWorker) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	w.sends = append(w.sends, req)
	if w.sendErr != nil {
		return rpc.SendResult{}, w.sendErr
	}
	if w.failFirst > 0 && len(w.sends) <= w.failFirst {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeRateLimited, "slow down", 30)
	}
	return rpc.SendResult{
		Message:   &rpc.Message{ID: 1234, Text: req.Text},
		ChannelID: strconv.FormatInt(req.ChannelID, 10),
	}, nil
}

// harness is a store, a staging area and one connection, wired the way the
// daemon wires them.
type harness struct {
	store   *channelstore.Store
	dir     string
	stage   *staging.Staging
	worker  *sendWorker
	sender  *publishing.Service
	service *sending.Service
	user    channelstore.User
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessLimit(t, 1<<20)
}

// newHarnessLimit is the same harness with the upload limit the deployment
// chose. The limit lives on the staging area because that is what applies it.
func newHarnessLimit(t *testing.T, max int64) *harness {
	t.Helper()
	ctx := context.Background()
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state", "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	user, err := store.CreateUser(ctx, "Sender", "sender", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "cat", ChannelID: 98, PeerType: channelstore.PeerChannel,
		Title: "Destination", MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := store.ReplacePermissions(ctx, user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{
			channelstore.PermissionContentPublish, channelstore.PermissionChannelsView,
		},
	}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "send-staging")
	stagingArea, err := staging.Open(dir, max)
	if err != nil {
		t.Fatalf("staging.Open: %v", err)
	}

	worker := &sendWorker{}
	sender := &publishing.Service{
		Workers: publishing.RegistryFunc(func(key string) (publishing.Worker, bool) {
			if key != "cat" {
				return nil, false
			}
			return worker, true
		}),
		Channels:  store,
		Publishes: store,
		Actors:    store,
	}
	return &harness{
		store: store, dir: dir, stage: stagingArea, worker: worker, sender: sender,
		service: &sending.Service{
			Sender:           sender,
			Operations:       store,
			Staging:          stagingArea,
			Jobs:             queue.NewEnqueuer(store, 5),
			HistoryRetention: 30 * 24 * time.Hour,
			Logf:             t.Logf,
		},
		user: user,
	}
}

func (h *harness) actor(t *testing.T) publishing.Actor {
	t.Helper()
	authr, err := h.store.AuthorizerFor(context.Background(), h.user.ID)
	if err != nil {
		t.Fatalf("AuthorizerFor: %v", err)
	}
	return publishing.Actor{UserID: h.user.ID, Auth: authr}
}

// jobFor is the queue's claim of one accepted send, as a worker receives it.
func jobFor(op channelstore.SendOperation, attempt int) automation.Job {
	return automation.Job{
		ID: 1, EventID: op.PublicID, Kind: automation.JobTelegramMessageSend,
		Attempt: attempt, MaxAttempts: 5,
	}
}

func (h *harness) accept(t *testing.T, in sending.AcceptInput) sending.Accepted {
	t.Helper()
	got, err := h.service.Accept(context.Background(), h.actor(t), in)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	return got
}

func textSend(text string) sending.AcceptInput {
	return sending.AcceptInput{ClientKey: "cat", DestinationChannelID: 98,
		Request: publishing.DirectSendRequest{Text: text}}
}

func mediaSend(kind string, body []byte) sending.AcceptInput {
	return sending.AcceptInput{
		ClientKey: "cat", DestinationChannelID: 98,
		Request:    publishing.DirectSendRequest{Text: "a caption", Media: &publishing.DirectSendMedia{Kind: kind}},
		Attachment: &sending.Attachment{Reader: bytes.NewReader(body), Name: "file.bin", MimeType: "application/octet-stream"},
	}
}

// --- acceptance ---------------------------------------------------------

// Test A. A text-only message is accepted and scheduled. Nothing is sent during
// the request: the answer is an operation, not a delivery.
func TestATextOnlySendIsAcceptedAndScheduledNotSent(t *testing.T) {
	h := newHarness(t)

	got := h.accept(t, textSend("hello"))
	if !got.Created {
		t.Fatal("a new send was reported as already known")
	}
	if got.Operation.Status != channelstore.SendQueued {
		t.Fatalf("status = %q, want %q", got.Operation.Status, channelstore.SendQueued)
	}
	if got.Operation.PublicID == "" {
		t.Fatal("the caller was given no operation to poll")
	}
	if len(h.worker.sends) != 0 {
		t.Fatalf("%d messages were sent during the request", len(h.worker.sends))
	}
}

// Test B. An attachment is on disk before the send is accepted. A 202 is a
// promise that the work survives this process, and a file that only exists in
// the request that carried it does not.
func TestBAnAttachmentIsStagedBeforeTheSendIsAccepted(t *testing.T) {
	h := newHarness(t)

	got := h.accept(t, mediaSend("document", pdfBytes))
	names, err := h.stage.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("%d staged attachments, want 1", len(names))
	}
	if got.Operation.MediaStagingName != names[0] {
		t.Fatalf("the operation names %q and the file is %q", got.Operation.MediaStagingName, names[0])
	}
	if got.Operation.MediaFilename != "file.bin" {
		t.Fatalf("the sender's file name is %q; it must survive as metadata", got.Operation.MediaFilename)
	}
	if got.Operation.MediaSHA256 == "" {
		t.Fatal("the attachment was staged without a digest to recognise it by")
	}
}

// Test C. The same request under the same key is the same send. It is answered
// with the operation that already exists and nothing else is created: no second
// file, no second job, no second message.
func TestCTheSameKeyAndContentIsTheSameSend(t *testing.T) {
	h := newHarness(t)
	in := mediaSend("photo", jpegBytes)
	in.IdempotencyKey = "photo-001"

	first := h.accept(t, in)
	second := h.accept(t, withKey(mediaSend("photo", jpegBytes), "photo-001"))

	if second.Created {
		t.Fatal("the repeated request created a second send")
	}
	if second.Operation.PublicID != first.Operation.PublicID {
		t.Fatalf("the repeated request is %q, not %q", second.Operation.PublicID, first.Operation.PublicID)
	}
	names, err := h.stage.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("%d staged attachments, want 1 — the repeat staged a second copy", len(names))
	}
}

// Test D. The same key with different content is a conflict. Two uploads with
// the same file name are two requests and this is what says so.
func TestDTheSameKeyWithDifferentBytesConflicts(t *testing.T) {
	h := newHarness(t)
	h.accept(t, withKey(mediaSend("document", pdfBytes), "doc-001"))

	_, err := h.service.Accept(context.Background(), h.actor(t),
		withKey(mediaSend("document", []byte("%PDF-1.4 another file entirely")), "doc-001"))
	if err == nil {
		t.Fatal("the same key with different content was accepted")
	}
	var perr *publishing.Error
	if !errors.As(err, &perr) || perr.Code != publishing.CodePublishConflict {
		t.Fatalf("err = %v, want %q", err, publishing.CodePublishConflict)
	}
}

// Test E. The same key with different text is a conflict too: the text is part
// of what the request is.
func TestETheSameKeyWithDifferentTextConflicts(t *testing.T) {
	h := newHarness(t)
	h.accept(t, withKey(textSend("one"), "text-001"))

	_, err := h.service.Accept(context.Background(), h.actor(t), withKey(textSend("two"), "text-001"))
	if err == nil {
		t.Fatal("the same key with different text was accepted")
	}
}

// Test F. A message is text or an attachment or both. Neither at all is not a
// message and is refused before anything is recorded.
func TestFNothingToSendIsRefused(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Accept(context.Background(), h.actor(t), textSend("   "))
	if err == nil {
		t.Fatal("an empty request was accepted")
	}
	if ops, _ := h.store.SendOperationsHoldingMedia(context.Background()); len(ops) != 0 {
		t.Fatal("a refused request left a record behind")
	}
}

// Test G. A photograph is a photograph. A file whose bytes are a PDF is refused
// rather than sent as a picture, and the request is not quietly downgraded into
// a document either.
func TestGAFileThatIsNotAPhotographIsRefusedAsAPhotograph(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Accept(context.Background(), h.actor(t), mediaSend("photo", pdfBytes))
	if err == nil {
		t.Fatal("a PDF was accepted as a photograph")
	}
	var perr *publishing.Error
	if !errors.As(err, &perr) || perr.Code != sending.CodeInvalidMediaKind {
		t.Fatalf("err = %v, want %q", err, sending.CodeInvalidMediaKind)
	}
}

// Test H. A recording is accepted as a recording. OGG/Opus is what a voice
// message is stored as, and it is not refused for being unclassifiable.
func TestHARecordingIsAcceptedAsVoice(t *testing.T) {
	h := newHarness(t)
	in := mediaSend("voice", voiceBytes)
	in.Attachment.MimeType = "audio/ogg"

	if got := h.accept(t, in); got.Operation.MediaKind != "voice" {
		t.Fatalf("kind = %q, want %q", got.Operation.MediaKind, "voice")
	}
}

// Test I. An attachment above the limit is refused while it arrives, and is
// never staged, recorded or queued.
func TestIAnOversizedAttachmentIsRefusedAndKeptNowhere(t *testing.T) {
	h := newHarnessLimit(t, 16)

	_, err := h.service.Accept(context.Background(), h.actor(t), mediaSend("document", bytes.Repeat([]byte("x"), 64)))
	if err == nil {
		t.Fatal("an oversized attachment was accepted")
	}
	var perr *publishing.Error
	if !errors.As(err, &perr) || perr.Status != 413 {
		t.Fatalf("err = %v, want 413", err)
	}
	names, _ := h.stage.Names()
	if len(names) != 0 {
		t.Fatalf("a refused upload left %d file(s) behind", len(names))
	}
}

// --- delivery -----------------------------------------------------------

// Test J. The message goes out when a worker claims the job, not before. The
// destination's message id is what the caller polls for and it is written down.
func TestJTheBackgroundSendPostsTheMessageAndRecordsItsID(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))

	if err := h.service.Execute(context.Background(), jobFor(got.Operation, 1)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(h.worker.sends) != 1 {
		t.Fatalf("%d messages were sent, want 1", len(h.worker.sends))
	}
	if h.worker.sends[0].Text != "hello" {
		t.Fatalf("sent text = %q", h.worker.sends[0].Text)
	}
	if h.worker.sends[0].ChannelID != 98 {
		t.Fatalf("the message went to channel %d", h.worker.sends[0].ChannelID)
	}

	op, _, err := h.store.SendOperation(context.Background(), got.Operation.PublicID)
	if err != nil {
		t.Fatalf("SendOperation: %v", err)
	}
	if op.Status != channelstore.SendSent {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendSent)
	}
	if op.RemoteMessageID != 1234 {
		t.Fatalf("remote message id = %d, want the one the destination confirmed", op.RemoteMessageID)
	}
}

// Test K. The attachment that was staged is the attachment that is sent, with
// the kind it was asked for.
func TestKTheStagedAttachmentIsSentUnderItsOwnKind(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, mediaSend("video", append([]byte("\x00\x00\x00\x18ftypmp42"), make([]byte, 40)...)))

	if err := h.service.Execute(context.Background(), jobFor(got.Operation, 1)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(h.worker.sends) != 1 {
		t.Fatalf("%d messages were sent, want 1", len(h.worker.sends))
	}
	media := h.worker.sends[0].Media
	if media == nil {
		t.Fatal("the attachment was not sent")
	}
	if media.Kind != "video" {
		t.Fatalf("kind = %q, want %q", media.Kind, "video")
	}
}

// Test L. A recording arrives as a voice message and not as a file that happens
// to be audio. Its length goes with it; no name is invented for it, because a
// name is what turns a recording into a download.
func TestLARecordingIsSentAsAVoiceMessage(t *testing.T) {
	h := newHarness(t)
	in := mediaSend("voice", voiceBytes)
	in.Attachment.MimeType = "audio/ogg"
	in.Request.Media.DurationSeconds = intPtr(5)
	got := h.accept(t, in)

	if err := h.service.Execute(context.Background(), jobFor(got.Operation, 1)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	media := h.worker.sends[0].Media
	if media == nil {
		t.Fatal("the recording was not sent")
	}
	if media.Kind != "voice" {
		t.Fatalf("kind = %q, want %q — a recording must not travel as a document", media.Kind, "voice")
	}
	if media.Duration != 5 {
		t.Fatalf("duration = %d, want the length the caller gave", media.Duration)
	}
	if !bytes.Equal(media.Data, voiceBytes) {
		t.Fatal("the recording's bytes were not carried through")
	}
}

// Test M. Every attempt sends the same client message id. It is what tells the
// destination a resend is the same message, and it is written down before the
// first attempt so a retry cannot invent a new identity and post twice.
func TestMEveryAttemptReusesTheSameSendIdentity(t *testing.T) {
	h := newHarness(t)
	h.worker.failFirst = 1
	got := h.accept(t, textSend("hello"))

	// The first attempt is refused and the second goes through, which is the
	// only way to see the two attempts a retry actually makes.
	if err := h.service.Execute(context.Background(), jobFor(got.Operation, 1)); err == nil {
		t.Fatal("the first attempt reported success")
	}
	if err := h.service.Execute(context.Background(), jobFor(got.Operation, 2)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(h.worker.sends) != 2 {
		t.Fatalf("%d sends, want 2", len(h.worker.sends))
	}
	if h.worker.sends[0].RandomID == 0 {
		t.Fatal("the send carried no client message id")
	}
	if h.worker.sends[0].RandomID != h.worker.sends[1].RandomID {
		t.Fatalf("the retry sent a new identity (%d then %d), so the destination sees two messages",
			h.worker.sends[0].RandomID, h.worker.sends[1].RandomID)
	}
}

// Test N. A failure the destination answered is worth another try, and the
// operation says so rather than reporting a delivery that did not happen.
func TestNARefusalTheDestinationAnsweredMayBeTriedAgain(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))
	h.worker.sendErr = rpc.NewError(rpc.CodeRateLimited, "slow down", 30)

	err := h.service.Execute(context.Background(), jobFor(got.Operation, 1))
	if err == nil {
		t.Fatal("a failed send reported success")
	}
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err = %v, want a job error", err)
	}
	if !jobErr.Retryable {
		t.Fatal("a rate-limited send was given up on")
	}
	if jobErr.RetryAfter != 30*time.Second {
		t.Fatalf("retry after = %v, want the delay the destination asked for", jobErr.RetryAfter)
	}

	op, _, _ := h.store.SendOperation(context.Background(), got.Operation.PublicID)
	if op.Status != channelstore.SendRetrying {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendRetrying)
	}
	if op.CompletedAt != nil {
		t.Fatal("a send that will be tried again was recorded as finished")
	}
}

// Test O. A failure that will never succeed is not retried. The operation ends
// and says why, instead of spending the attempt budget on the same refusal.
func TestOAPermanentRefusalEndsTheSend(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))
	h.worker.sendErr = rpc.ErrSendForbidden

	err := h.service.Execute(context.Background(), jobFor(got.Operation, 1))
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err = %v, want a job error", err)
	}
	if jobErr.Retryable {
		t.Fatal("a refused send was marked for another attempt")
	}

	op, _, _ := h.store.SendOperation(context.Background(), got.Operation.PublicID)
	if op.Status != channelstore.SendFailed {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendFailed)
	}
	if op.LastErrorCode == "" {
		t.Fatal("the failure was recorded with no reason")
	}
}

// Test P. A send that never got an answer is unknown, and unknown is never
// retried. The message may already be in the channel and trying again is the
// one thing certain to make a second one.
func TestPAnUnansweredSendIsUnknownAndNeverRepeated(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))
	h.worker.sendErr = rpc.ErrUnavailable

	err := h.service.Execute(context.Background(), jobFor(got.Operation, 1))
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err = %v, want a job error", err)
	}
	if jobErr.Retryable {
		t.Fatal("an unconfirmed send was scheduled for another attempt")
	}

	op, _, _ := h.store.SendOperation(context.Background(), got.Operation.PublicID)
	if op.Status != channelstore.SendUnknown {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendUnknown)
	}
	if op.RemoteMessageID != 0 {
		t.Fatalf("remote message id = %d; nothing was confirmed so nothing may be claimed", op.RemoteMessageID)
	}
}

// Test Q. A grant withdrawn after the request was accepted still stops the
// send. Authorization is read again when the work happens, not believed from
// when it was asked for.
func TestQAPermissionWithdrawnAfterAcceptanceStopsTheSend(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))
	if err := h.store.ReplacePermissions(context.Background(), h.user.ID, channelstore.PermissionSet{}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}

	err := h.service.Execute(context.Background(), jobFor(got.Operation, 1))
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err = %v, want a job error", err)
	}
	if jobErr.Retryable {
		t.Fatal("a withdrawn grant was marked for another attempt")
	}
	if len(h.worker.sends) != 0 {
		t.Fatalf("%d messages were sent after the permission was withdrawn", len(h.worker.sends))
	}

	op, _, _ := h.store.SendOperation(context.Background(), got.Operation.PublicID)
	if op.Status != channelstore.SendFailed {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendFailed)
	}
}

// Test R. An attachment that has gone missing is a failure and never a
// delivery. Nothing is sent and the record says what is wrong.
func TestRAMissingAttachmentIsAFailureNotADelivery(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, mediaSend("document", pdfBytes))
	if err := h.stage.Remove(got.Operation.MediaStagingName); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	err := h.service.Execute(context.Background(), jobFor(got.Operation, 1))
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err = %v, want a job error", err)
	}
	if jobErr.Retryable {
		t.Fatal("a missing attachment was marked for another attempt; the bytes are not coming back")
	}
	if len(h.worker.sends) != 0 {
		t.Fatalf("%d messages were sent without their attachment", len(h.worker.sends))
	}

	op, _, _ := h.store.SendOperation(context.Background(), got.Operation.PublicID)
	if op.Status != channelstore.SendFailed {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendFailed)
	}
	if op.LastErrorCode != sending.CodeSendAttachmentMissing {
		t.Fatalf("error code = %q, want %q", op.LastErrorCode, sending.CodeSendAttachmentMissing)
	}
	if op.RemoteMessageID != 0 {
		t.Fatal("a send that never happened claimed a destination message")
	}
}

// Test S. A send that is already answered is not answered again. A second
// worker picking it up is an expired lease, not new work.
func TestSAnAnsweredSendIsNotSentTwice(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))
	if err := h.service.Execute(context.Background(), jobFor(got.Operation, 1)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := h.service.Execute(context.Background(), jobFor(got.Operation, 1)); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if len(h.worker.sends) != 1 {
		t.Fatalf("%d messages were sent, want 1 — a completed send was performed again", len(h.worker.sends))
	}
}

// --- status -------------------------------------------------------------

// Test T. Polling reads and writes nothing. It never sends a message, whatever
// the operation's state.
func TestTPollingTheStatusSendsNothing(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))

	op, err := h.service.Status(context.Background(), h.actor(t), "cat", "98", got.Operation.PublicID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if op.Status != channelstore.SendQueued {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendQueued)
	}
	if len(h.worker.sends) != 0 {
		t.Fatalf("%d messages were sent by a status read", len(h.worker.sends))
	}
	if strings.Contains(op.MediaStagingName, string(os.PathSeparator)) {
		t.Fatalf("the answer exposed a staging path: %q", op.MediaStagingName)
	}
}

// Test U. An operation is its owner's. A caller who names an id that is not
// theirs is told nothing exists — knowing an id must not confirm that it does.
func TestUAnOperationIsItsOwnersAlone(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))

	other, err := h.store.CreateUser(context.Background(), "Other", "other", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	authr, err := h.store.AuthorizerFor(context.Background(), other.ID)
	if err != nil {
		t.Fatalf("AuthorizerFor: %v", err)
	}

	_, err = h.service.Status(context.Background(), publishing.Actor{UserID: other.ID, Auth: authr},
		"cat", "98", got.Operation.PublicID)
	if err == nil {
		t.Fatal("another account read an operation that is not its own")
	}
	var perr *publishing.Error
	if !errors.As(err, &perr) || perr.Status != 404 {
		t.Fatalf("err = %v, want 404", err)
	}
}

// Test V. An operation belongs to one destination. Naming it under another
// channel answers as though it were never there.
func TestVAnOperationBelongsToOneDestination(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, textSend("hello"))

	_, err := h.service.Status(context.Background(), h.actor(t), "cat", "99", got.Operation.PublicID)
	if err == nil {
		t.Fatal("an operation was read back under a channel it was never sent to")
	}
}

// --- cleanup ------------------------------------------------------------

// Test W. A crash between writing the bytes and recording what owns them leaves
// a file nothing claims. The sweep removes it and keeps the one a queued send
// is about to need.
func TestWCleanupRemovesWhatNothingClaimsAndKeepsWhatIsStillNeeded(t *testing.T) {
	h := newHarness(t)
	needed := h.accept(t, mediaSend("document", pdfBytes))
	orphan, err := h.stage.Write(context.Background(), strings.NewReader("nobody claims this"),
		staging.Meta{OriginalName: "orphan.bin"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	report, err := h.service.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if report.AbandonedAttachments != 1 {
		t.Fatalf("abandoned = %d, want 1", report.AbandonedAttachments)
	}
	if _, err := h.stage.ReadAll(orphan.Name); !errors.Is(err, staging.ErrNotFound) {
		t.Fatalf("the orphan is still there: %v", err)
	}
	if _, err := h.stage.ReadAll(needed.Operation.MediaStagingName); err != nil {
		t.Fatalf("the attachment a queued send needs was removed: %v", err)
	}
}

// Test X. An operation whose attachment is gone is stopped, and is never
// reported as delivered.
func TestXCleanupStopsAnOperationWhoseAttachmentIsGone(t *testing.T) {
	h := newHarness(t)
	got := h.accept(t, mediaSend("document", pdfBytes))
	if err := h.stage.Remove(got.Operation.MediaStagingName); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	report, err := h.service.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if report.IncompleteOperations != 1 {
		t.Fatalf("incomplete = %d, want 1", report.IncompleteOperations)
	}

	op, _, _ := h.store.SendOperation(context.Background(), got.Operation.PublicID)
	if op.Status != channelstore.SendFailed {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendFailed)
	}
	if op.RemoteMessageID != 0 {
		t.Fatal("an operation that never sent anything claimed a destination message")
	}
}

// Test Y. A finished send does not keep its attachment forever. Retention
// removes the operation and the file with it, and leaves live work alone.
func TestYRetentionRemovesFinishedSendAndTheirAttachments(t *testing.T) {
	h := newHarness(t)
	h.service.HistoryRetention = time.Nanosecond

	finished := h.accept(t, mediaSend("document", pdfBytes))
	if err := h.service.Execute(context.Background(), jobFor(finished.Operation, 1)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	live := h.accept(t, mediaSend("document", pdfBytes))

	time.Sleep(2 * time.Millisecond)
	if _, err := h.service.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, found, _ := h.store.SendOperation(context.Background(), finished.Operation.PublicID); found {
		t.Fatal("a finished send past its window is still recorded")
	}
	if _, found, _ := h.store.SendOperation(context.Background(), live.Operation.PublicID); !found {
		t.Fatal("a live send was deleted by the retention sweep")
	}
	if _, err := h.stage.ReadAll(live.Operation.MediaStagingName); err != nil {
		t.Fatalf("the attachment a live send needs was removed: %v", err)
	}
}

// --- helpers ------------------------------------------------------------

// Test Z. An idempotency key is stored and indexed, so it is bounded. A key is
// a request's identity, not a place to put an arbitrarily large header.
func TestZAnIdempotencyKeyIsBounded(t *testing.T) {
	h := newHarness(t)
	in := textSend("hello")
	in.IdempotencyKey = strings.Repeat("k", sending.MaxIdempotencyKey+1)

	_, err := h.service.Accept(context.Background(), h.actor(t), in)
	if err == nil {
		t.Fatal("an unbounded idempotency key was accepted")
	}
	var perr *publishing.Error
	if !errors.As(err, &perr) || perr.Status != 400 {
		t.Fatalf("err = %v, want 400", err)
	}
}

func intPtr(v int) *int { return &v }

// withKey is the same request carrying an idempotency key.
func withKey(in sending.AcceptInput, key string) sending.AcceptInput {
	in.IdempotencyKey = key
	return in
}
