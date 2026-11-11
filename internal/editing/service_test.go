package editing_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/editing"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/sending"
	"github.com/mirza/telesync/internal/staging"
)

type editJobs struct {
	enqueued []struct {
		id     string
		target int64
	}
}

func (j *editJobs) EnqueueMessageEdit(_ context.Context, id string, target int64) error {
	j.enqueued = append(j.enqueued, struct {
		id     string
		target int64
	}{id, target})
	return nil
}

type editSender struct {
	actor publishing.Actor
	calls int
}

func (s *editSender) ActorFor(context.Context, int64) (publishing.Actor, error) {
	s.calls++
	return s.actor, nil
}

type editEditor struct {
	target       rpc.Message
	editResult   rpc.EditResult
	editErr      error
	editCalls    int
	messageCalls int
	lastRequest  rpc.EditRequest
}

func (e *editEditor) EditMessage(_ context.Context, _ string, req rpc.EditRequest) (rpc.EditResult, error) {
	e.editCalls++
	e.lastRequest = req
	e.target.Text = req.Text
	e.target.Entities = append([]rpc.Entity(nil), req.Entities...)
	if req.Media != nil {
		e.target.Media = &rpc.Media{Type: req.Media.Kind}
	} else {
		e.target.Media = nil
	}
	result := e.target
	e.editResult = rpc.EditResult{Message: &result}
	return e.editResult, e.editErr
}

func (e *editEditor) Message(_ context.Context, _ string, channelID int64, messageID int) (rpc.MessagePage, error) {
	e.messageCalls++
	if channelID != 98 || messageID != e.target.ID {
		return rpc.MessagePage{}, nil
	}
	return rpc.MessagePage{Messages: []rpc.Message{e.target}}, nil
}

func editService(t *testing.T, editor *editEditor) (*editing.Service, *channelstore.Store, *editJobs, publishing.Actor, int64) {
	t.Helper()
	ctx := context.Background()
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user, err := store.CreateUser(ctx, "Editor", "editor", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	channel, err := store.Upsert(ctx, channelstore.Channel{ClientKey: "cat", ChannelID: 98, PeerType: channelstore.PeerChannel, MembershipStatus: channelstore.StatusMember})
	if err != nil {
		t.Fatalf("upsert channel: %v", err)
	}
	actor := publishing.Actor{UserID: user.ID, Auth: channelstore.NewAuthorizerFor(user.ID,
		[]channelstore.GlobalPermission{channelstore.PermissionChannelsView}, nil)}
	area, err := staging.Open(filepath.Join(t.TempDir(), "edit-staging"), 1<<20)
	if err != nil {
		t.Fatalf("open staging: %v", err)
	}
	jobs := &editJobs{}
	sender := &editSender{actor: actor}
	svc := &editing.Service{
		Sender: sender, Editor: editor, Channels: store, Operations: store, Staging: area, Jobs: jobs,
		HistoryRetention: 30 * 24 * time.Hour,
	}
	return svc, store, jobs, actor, channel.ID
}

func TestAcceptDurablyEnqueuesAFullReplacementWithoutReadingTelegram(t *testing.T) {
	ctx := context.Background()
	editor := &editEditor{}
	svc, _, jobs, actor, _ := editService(t, editor)
	accepted, err := svc.Accept(ctx, actor, editing.AcceptInput{
		ClientKey: "cat", DestinationChannelID: 98, TargetMessageID: 11,
		IdempotencyKey: "replace-11", Request: publishing.DirectSendRequest{Text: "replacement", Entities: []rpc.Entity{}, Options: publishing.RequestOptions{}},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !accepted.Created || accepted.Operation.PublicID == "" || accepted.Operation.Status != channelstore.MessageEditQueued || accepted.Operation.TargetMessageID != 11 {
		t.Fatalf("accepted = %+v; want queued operation for message 11", accepted)
	}
	if editor.messageCalls != 0 || editor.editCalls != 0 {
		t.Fatalf("Accept contacted Telegram: message calls=%d edit calls=%d", editor.messageCalls, editor.editCalls)
	}
	if len(jobs.enqueued) != 1 || jobs.enqueued[0].id != accepted.Operation.PublicID || jobs.enqueued[0].target <= 0 {
		t.Fatalf("enqueued = %+v; want one durable edit job with a target key", jobs.enqueued)
	}
}

func TestAcceptReplaysIdenticalIdempotencyKeyWithoutEnqueueingAgain(t *testing.T) {
	ctx := context.Background()
	svc, _, jobs, actor, _ := editService(t, &editEditor{})
	in := editing.AcceptInput{
		ClientKey: "cat", DestinationChannelID: 98, TargetMessageID: 11,
		IdempotencyKey: "replace-11", Request: publishing.DirectSendRequest{Text: "replacement", Entities: []rpc.Entity{}, Options: publishing.RequestOptions{}},
	}
	first, err := svc.Accept(ctx, actor, in)
	if err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	second, err := svc.Accept(ctx, actor, in)
	if err != nil {
		t.Fatalf("replay Accept: %v", err)
	}
	if !first.Created || second.Created || first.Operation.PublicID != second.Operation.PublicID || len(jobs.enqueued) != 1 {
		t.Fatalf("first=%+v second=%+v queued=%d; want one existing operation", first, second, len(jobs.enqueued))
	}
}

func TestAcceptStagesAttachmentAndIncludesItsBytesInDurableFingerprint(t *testing.T) {
	ctx := context.Background()
	svc, store, _, actor, _ := editService(t, &editEditor{})
	in := editing.AcceptInput{
		ClientKey: "cat", DestinationChannelID: 98, TargetMessageID: 11,
		IdempotencyKey: "with-photo", Request: publishing.DirectSendRequest{Text: "caption", Entities: []rpc.Entity{}, Media: &publishing.DirectSendMedia{Kind: publishing.DirectMediaPhoto}, Options: publishing.RequestOptions{}},
		Attachment: &sending.Attachment{Reader: bytes.NewReader([]byte("\x89PNG\r\n\x1a\nphoto-data")), Name: "photo.png", MimeType: "image/png"},
	}
	accepted, err := svc.Accept(ctx, actor, in)
	if err != nil {
		t.Fatalf("Accept attachment: %v", err)
	}
	if accepted.Operation.MediaStagingName == "" || accepted.Operation.MediaSHA256 == "" || accepted.Operation.MediaSize == 0 {
		t.Fatalf("attachment metadata not persisted: %+v", accepted.Operation)
	}
	replayed, found, err := store.MessageEditOperationFor(ctx, actor.UserID, "with-photo")
	if err != nil || !found {
		t.Fatalf("read durable edit: found=%v err=%v", found, err)
	}
	if accepted.Operation.RequestFingerprint == "" || replayed.MediaSHA256 != accepted.Operation.MediaSHA256 {
		t.Fatal("attachment fingerprint or digest was not persisted")
	}
	if err := svc.Staging.Remove(accepted.Operation.MediaStagingName); err != nil {
		t.Fatalf("remove staged attachment: %v", err)
	}
	if err := svc.Cleanup(ctx); err != nil {
		t.Fatalf("restart cleanup: %v", err)
	}
	failed, found, err := store.MessageEditOperation(ctx, accepted.Operation.PublicID)
	if err != nil || !found || failed.Status != channelstore.MessageEditFailed || failed.LastErrorCode != editing.CodeEditMediaMissing {
		t.Fatalf("missing media recovery: operation=%+v found=%v err=%v", failed, found, err)
	}
	held, err := svc.HeldMediaNames(ctx)
	if err != nil || len(held) != 0 {
		t.Fatalf("held media after missing-file recovery: %v, err=%v", held, err)
	}
}

func TestExecuteRevalidatesUpdatePermissionAndEditsTheExistingMessage(t *testing.T) {
	ctx := context.Background()
	editor := &editEditor{target: rpc.Message{ID: 11, Text: "old", Entities: []rpc.Entity{}}}
	svc, store, jobs, actor, localChannelID := editService(t, editor)
	actor.Auth = channelstore.NewAuthorizerFor(actor.UserID, []channelstore.GlobalPermission{channelstore.PermissionChannelsView}, []channelstore.Grant{
		{UserID: actor.UserID, Kind: channelstore.KindChannel, ResourceID: localChannelID, Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
	})
	svc.Sender = &editSender{actor: actor}
	accepted, err := svc.Accept(ctx, actor, editing.AcceptInput{ClientKey: "cat", DestinationChannelID: 98, TargetMessageID: 11, IdempotencyKey: "execute", Request: publishing.DirectSendRequest{Text: "new", Entities: []rpc.Entity{}, Options: publishing.RequestOptions{DisableWebPagePreview: true}}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := svc.Execute(ctx, automation.Job{EventID: accepted.Operation.PublicID, Kind: automation.JobTelegramMessageEdit, TargetID: jobs.enqueued[0].target, Attempt: 1}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if editor.editCalls != 1 || editor.lastRequest.MessageID != 11 || editor.lastRequest.Text != "new" || !editor.lastRequest.NoWebpage {
		t.Fatalf("native edit calls=%d request=%+v", editor.editCalls, editor.lastRequest)
	}
	got, found, err := store.MessageEditOperation(ctx, accepted.Operation.PublicID)
	if err != nil || !found {
		t.Fatalf("read operation: found=%v err=%v", found, err)
	}
	if got.Status != channelstore.MessageEditEdited || got.RemoteMessageID != 11 {
		t.Fatalf("operation after native edit = %+v", got)
	}
}

func TestExecuteFailsWhenCurrentAppUpdatePermissionWasWithdrawn(t *testing.T) {
	ctx := context.Background()
	editor := &editEditor{target: rpc.Message{ID: 11, Text: "old"}}
	svc, _, jobs, actor, _ := editService(t, editor)
	svc.Sender = &editSender{actor: actor}
	accepted, err := svc.Accept(ctx, actor, editing.AcceptInput{ClientKey: "cat", DestinationChannelID: 98, TargetMessageID: 11, IdempotencyKey: "revoked", Request: publishing.DirectSendRequest{Text: "new", Entities: []rpc.Entity{}}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := svc.Execute(ctx, automation.Job{EventID: accepted.Operation.PublicID, TargetID: jobs.enqueued[0].target, Attempt: 1}); err == nil {
		t.Fatal("Execute succeeded after ActionUpdate was withdrawn")
	}
	if editor.editCalls != 0 {
		t.Fatalf("Telegram edit called %d times after permission withdrawal", editor.editCalls)
	}
}

func TestExecuteRecordsRateLimitAsRetryWithoutCreatingAnotherMessage(t *testing.T) {
	ctx := context.Background()
	editor := &editEditor{target: rpc.Message{ID: 11, Text: "old"}, editErr: rpc.NewError(rpc.CodeRateLimited, "rate limited", 7)}
	svc, store, jobs, actor, localChannelID := editService(t, editor)
	actor.Auth = channelstore.NewAuthorizerFor(actor.UserID, []channelstore.GlobalPermission{channelstore.PermissionChannelsView}, []channelstore.Grant{{UserID: actor.UserID, Kind: channelstore.KindChannel, ResourceID: localChannelID, Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow}})
	svc.Sender = &editSender{actor: actor}
	accepted, err := svc.Accept(ctx, actor, editing.AcceptInput{ClientKey: "cat", DestinationChannelID: 98, TargetMessageID: 11, IdempotencyKey: "rate-limit", Request: publishing.DirectSendRequest{Text: "new", Entities: []rpc.Entity{}}})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	err = svc.Execute(ctx, automation.Job{EventID: accepted.Operation.PublicID, TargetID: jobs.enqueued[0].target, Attempt: 1})
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) || !jobErr.Retryable || jobErr.RetryAfter != 7*time.Second {
		t.Fatalf("Execute error=%v, want retry after 7s", err)
	}
	got, found, readErr := store.MessageEditOperation(ctx, accepted.Operation.PublicID)
	if readErr != nil || !found || got.Status != channelstore.MessageEditRetrying || got.NextAttemptAt == nil {
		t.Fatalf("retry state: op=%+v found=%v err=%v", got, found, readErr)
	}
	if editor.editCalls != 1 {
		t.Fatalf("native edit called %d times", editor.editCalls)
	}
}
