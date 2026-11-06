package channelstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// One run per event: the same source message never runs the same workflow
// twice, however often the queue redelivers it. What each destination achieved
// is recorded per destination, so a retry only ever repeats what failed.

// wfRun is a workflow with one event waiting behind it.
type wfRun struct {
	*wfEnv
	workflow automation.Workflow
	eventID  string
}

func newWFRun(t *testing.T) *wfRun {
	t.Helper()
	env := newWFEnv(t)
	ctx := context.Background()
	wf, err := env.store.CreateWorkflow(ctx, env.input())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	return &wfRun{wfEnv: env, workflow: wf, eventID: env.event(t, 9)}
}

// event records one source message as a stored automation event, which is what
// a run hangs off.
func (e *wfEnv) event(t *testing.T, messageID int64) string {
	t.Helper()
	now := time.Now().UTC()
	id := "evt_wf_" + time.Now().Format("150405.000000000")
	inserted, err := e.store.InsertMessageEvent(context.Background(), automation.MessageEvent{
		ID:                id,
		Type:              automation.EventMessageCreated,
		NetworkKey:        "official",
		SourceClientKey:   "official",
		ManagedChannelID:  e.channel.ID,
		TelegramChannelID: 555,
		MessageID:         messageID,
		OccurredAt:        now,
		ReceivedAt:        now,
		Payload: automation.MessagePayload{
			Message: rpc.Message{ID: int(messageID), Text: "hello #news"},
		},
	})
	if err != nil {
		t.Fatalf("InsertMessageEvent: %v", err)
	}
	if !inserted {
		t.Fatalf("event %s was expected to be new", id)
	}
	return id
}

func TestBeginWorkflowRunCreatesOneRunPerEvent(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()

	run, created, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello #news",
	})
	if err != nil || !created {
		t.Fatalf("BeginWorkflowRun: created=%v err=%v", created, err)
	}
	if run.Status != channelstore.WorkflowRunQueued {
		t.Fatalf("status = %q, want %q", run.Status, channelstore.WorkflowRunQueued)
	}
	if run.SourceMessageID != 9 || run.OriginalContent != "hello #news" {
		t.Fatalf("run = %+v", run)
	}
	if run.AIStatus != channelstore.WorkflowAINotUsed {
		t.Fatalf("ai status = %q", run.AIStatus)
	}
	if run.CreatedAt.IsZero() {
		t.Fatal("a run must record when it was queued")
	}

	again, created, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello #news",
	})
	if err != nil {
		t.Fatalf("second BeginWorkflowRun: %v", err)
	}
	if created {
		t.Fatal("one event must not start two runs")
	}
	if again.ID != run.ID {
		t.Fatalf("second run id = %d, want %d", again.ID, run.ID)
	}
}

// A run executes the configuration it was queued under. The revision is copied
// into the run so history says which one that was after the workflow is edited.
func TestBeginWorkflowRunSnapshotsTheConfigRevision(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, created, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil || !created {
		t.Fatalf("BeginWorkflowRun: created=%v err=%v", created, err)
	}
	if run.ConfigRevision != 1 {
		t.Fatalf("config revision = %d, want 1", run.ConfigRevision)
	}

	update := r.input()
	update.AIPrompt = "changed"
	if _, found, err := r.store.UpdateWorkflow(ctx, r.workflow.ID, update); err != nil || !found {
		t.Fatalf("UpdateWorkflow: found=%v err=%v", found, err)
	}
	kept, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if kept.ConfigRevision != 1 {
		t.Fatalf("a queued run must keep the revision it was queued under, got %d", kept.ConfigRevision)
	}

	other := r.event(t, 10)
	latest, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: other, SourceMessageID: 10, OriginalContent: "second",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if latest.ConfigRevision != 2 {
		t.Fatalf("new run revision = %d, want 2", latest.ConfigRevision)
	}
}

func TestBeginWorkflowRunRefusesAnUnknownWorkflow(t *testing.T) {
	r := newWFRun(t)
	_, _, err := r.store.BeginWorkflowRun(context.Background(), channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID + 999, EventID: r.eventID, SourceMessageID: 9,
	})
	if !errors.Is(err, automation.ErrNotFound) {
		t.Fatalf("error = %v, want an automation.ErrNotFound", err)
	}
}

func TestStartWorkflowRunMarksProcessingAndCountsTheAttempt(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	// A retry of a crashed run is the same run, one more attempt.
	for want := 1; want <= 2; want++ {
		started, err := r.store.StartWorkflowRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("StartWorkflowRun: %v", err)
		}
		if started.Status != channelstore.WorkflowRunProcessing {
			t.Fatalf("status = %q, want %q", started.Status, channelstore.WorkflowRunProcessing)
		}
		if started.AttemptCount != want {
			t.Fatalf("attempt count = %d, want %d", started.AttemptCount, want)
		}
		if started.StartedAt == nil {
			t.Fatal("a started run must record when it started")
		}
	}
}

func TestRecordWorkflowRunAIKeepsOriginalAndPolishedApart(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello #news",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if _, err := r.store.StartWorkflowRun(ctx, run.ID); err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	if err := r.store.RecordWorkflowRunAI(ctx, run.ID, channelstore.WorkflowRunAIUpdate{
		Status: channelstore.WorkflowAISucceeded, Polished: "A polished hello.",
		Model: "test-model", InputTokens: 12, OutputTokens: 34, Duration: 2 * time.Second,
	}); err != nil {
		t.Fatalf("RecordWorkflowRunAI: %v", err)
	}

	stored, found, err := r.store.GetWorkflowRun(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowRun: found=%v err=%v", found, err)
	}
	if stored.OriginalContent != "hello #news" || stored.PolishedContent != "A polished hello." {
		t.Fatalf("content = %+v", stored)
	}
	if stored.AIStatus != channelstore.WorkflowAISucceeded || stored.AIModel != "test-model" {
		t.Fatalf("ai = %+v", stored)
	}
	if stored.AIInputTokens != 12 || stored.AIOutputTokens != 34 || stored.AIDurationMS != 2000 {
		t.Fatalf("ai accounting = %+v", stored)
	}
}

func TestRecordWorkflowRunAIFailureIsNotASuccess(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if err := r.store.RecordWorkflowRunAI(ctx, run.ID, channelstore.WorkflowRunAIUpdate{
		Status: channelstore.WorkflowAIFallbackUsed, Polished: "hello",
		FallbackUsed: true, Model: "test-model", ErrorCode: "ai_timeout",
		ErrorMessage: "the model did not answer in time",
	}); err != nil {
		t.Fatalf("RecordWorkflowRunAI: %v", err)
	}
	stored, _, err := r.store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if stored.AIStatus != channelstore.WorkflowAIFallbackUsed || !stored.AIFallbackUsed {
		t.Fatalf("ai = %+v", stored)
	}
	if stored.AIErrorCode != "ai_timeout" || stored.AIErrorMessage == "" {
		t.Fatalf("ai error = %+v", stored)
	}
}

func TestCompleteWorkflowRunRecordsTheOutcome(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if _, err := r.store.StartWorkflowRun(ctx, run.ID); err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	if err := r.store.CompleteWorkflowRun(ctx, run.ID, channelstore.WorkflowRunPublished,
		"", ""); err != nil {
		t.Fatalf("CompleteWorkflowRun: %v", err)
	}
	stored, _, err := r.store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if stored.Status != channelstore.WorkflowRunPublished {
		t.Fatalf("status = %q", stored.Status)
	}
	if stored.CompletedAt == nil {
		t.Fatal("a finished run must record when it finished")
	}

	// A failure says why.
	other, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.event(t, 11), SourceMessageID: 11, OriginalContent: "second",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if err := r.store.CompleteWorkflowRun(ctx, other.ID, channelstore.WorkflowRunFailed,
		"publish_failed", "the destination refused the message"); err != nil {
		t.Fatalf("CompleteWorkflowRun: %v", err)
	}
	stored, _, err = r.store.GetWorkflowRun(ctx, other.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if stored.LastErrorCode != "publish_failed" || stored.LastErrorMessage == "" {
		t.Fatalf("failure = %+v", stored)
	}
}

// A destination that already published is never published again: the row is
// the proof, and a retry reuses it.
func TestEnsureWorkflowPublicationIsStableAcrossRetries(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	dest := automation.WorkflowPublication{
		DestinationType: string(automation.DestinationTelegram),
		ClientKey:       "official", ChannelID: 555,
	}
	first, created, err := r.store.EnsureWorkflowPublication(ctx, run.ID, dest)
	if err != nil || !created {
		t.Fatalf("EnsureWorkflowPublication: created=%v err=%v", created, err)
	}
	if first.Status != channelstore.WorkflowPublicationPending {
		t.Fatalf("status = %q, want %q", first.Status, channelstore.WorkflowPublicationPending)
	}
	if err := r.store.FinishWorkflowPublication(ctx, first.ID, channelstore.WorkflowPublicationPublished,
		321, 0, "", "", ""); err != nil {
		t.Fatalf("FinishWorkflowPublication: %v", err)
	}

	again, created, err := r.store.EnsureWorkflowPublication(ctx, run.ID, dest)
	if err != nil {
		t.Fatalf("EnsureWorkflowPublication: %v", err)
	}
	if created {
		t.Fatal("one destination must not be published twice for one run")
	}
	if again.ID != first.ID || again.Status != channelstore.WorkflowPublicationPublished {
		t.Fatalf("publication = %+v, want the recorded one", again)
	}
	if again.RemoteMessageID != 321 {
		t.Fatalf("remote message id = %d", again.RemoteMessageID)
	}
}

func TestFinishWorkflowPublicationRecordsTheRemoteIdentity(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	wp, _, err := r.store.EnsureWorkflowPublication(ctx, run.ID, automation.WorkflowPublication{
		DestinationType: string(automation.DestinationWordPress), SiteID: 3,
	})
	if err != nil {
		t.Fatalf("EnsureWorkflowPublication: %v", err)
	}
	if err := r.store.FinishWorkflowPublication(ctx, wp.ID, channelstore.WorkflowPublicationPublished,
		0, 42, "https://site.example.com/hello/", "", ""); err != nil {
		t.Fatalf("FinishWorkflowPublication: %v", err)
	}
	all, err := r.store.WorkflowPublications(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowPublications: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("publications = %+v", all)
	}
	got := all[0]
	if got.RemotePostID != 42 || got.RemoteURL != "https://site.example.com/hello/" {
		t.Fatalf("publication = %+v", got)
	}
	if got.CompletedAt == nil {
		t.Fatal("a finished publication must record when it finished")
	}
}

func TestGetRunIsScopedToItsWorkflow(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if _, found, err := r.store.GetRun(ctx, r.workflow.ID, run.ID); err != nil || !found {
		t.Fatalf("GetRun: found=%v err=%v", found, err)
	}
	if _, found, err := r.store.GetRun(ctx, r.workflow.ID+1, run.ID); err != nil || found {
		t.Fatalf("a run of another workflow must not be readable here: found=%v err=%v", found, err)
	}
}

func TestListRunsFiltersByStatusAndPages(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	first, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "one",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	second, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.event(t, 12), SourceMessageID: 12, OriginalContent: "two",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if err := r.store.CompleteWorkflowRun(ctx, second.ID, channelstore.WorkflowRunFailed,
		"publish_failed", "nope"); err != nil {
		t.Fatalf("CompleteWorkflowRun: %v", err)
	}

	page, err := r.store.ListRuns(ctx, r.workflow.ID, automation.RunListOptions{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if page.Total != 2 || len(page.Runs) != 2 {
		t.Fatalf("page = %+v", page)
	}
	for _, run := range page.Runs {
		if run.WorkflowID != r.workflow.ID {
			t.Fatalf("run = %+v", run)
		}
		if run.ID == second.ID && len(run.Publications) != 0 {
			t.Fatalf("publications = %+v", run.Publications)
		}
	}

	failed, err := r.store.ListRuns(ctx, r.workflow.ID,
		automation.RunListOptions{Status: channelstore.WorkflowRunFailed})
	if err != nil {
		t.Fatalf("ListRuns status: %v", err)
	}
	if failed.Total != 1 || len(failed.Runs) != 1 || failed.Runs[0].ID != second.ID {
		t.Fatalf("failed page = %+v", failed)
	}
	_ = first

	one, err := r.store.ListRuns(ctx, r.workflow.ID, automation.RunListOptions{Page: 1, PerPage: 1})
	if err != nil {
		t.Fatalf("ListRuns page: %v", err)
	}
	if len(one.Runs) != 1 || one.Total != 2 {
		t.Fatalf("paged = %+v", one)
	}
}

// Deleting the workflow takes its history with it, through the schema's
// cascade: nothing is left behind pointing at a configuration that is gone.
func TestDeletingAWorkflowRemovesItsRuns(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if _, _, err := r.store.EnsureWorkflowPublication(ctx, run.ID, automation.WorkflowPublication{
		DestinationType: string(automation.DestinationTelegram), ClientKey: "official", ChannelID: 555,
	}); err != nil {
		t.Fatalf("EnsureWorkflowPublication: %v", err)
	}
	if _, err := r.store.DeleteWorkflow(ctx, r.workflow.ID); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	if _, found, err := r.store.GetWorkflowRun(ctx, run.ID); err != nil || found {
		t.Fatalf("a run must follow its workflow: found=%v err=%v", found, err)
	}
}

// A destination that has not answered yet is still readable: the history shows
// the work in progress rather than failing to render it.
func TestAnUnfinishedPublicationIsReadable(t *testing.T) {
	r := newWFRun(t)
	ctx := context.Background()
	run, _, err := r.store.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID: r.workflow.ID, EventID: r.eventID, SourceMessageID: 9, OriginalContent: "hello",
	})
	if err != nil {
		t.Fatalf("BeginWorkflowRun: %v", err)
	}
	if _, _, err := r.store.EnsureWorkflowPublication(ctx, run.ID, automation.WorkflowPublication{
		DestinationType: string(automation.DestinationTelegram), ClientKey: "official", ChannelID: 555,
	}); err != nil {
		t.Fatalf("EnsureWorkflowPublication: %v", err)
	}

	stored, found, err := r.store.GetWorkflowRun(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowRun: found=%v err=%v", found, err)
	}
	if len(stored.Publications) != 1 || stored.Publications[0].CompletedAt != nil {
		t.Fatalf("publications = %+v, want one still running", stored.Publications)
	}
	page, err := r.store.ListRuns(ctx, r.workflow.ID, automation.RunListOptions{})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(page.Runs) != 1 || len(page.Runs[0].Publications) != 1 {
		t.Fatalf("page = %+v", page.Runs)
	}
}
