package workflows

import (
	"context"
	"errors"
	"strings"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// EventReader reads one stored automation event.
type EventReader interface {
	AutomationEvent(ctx context.Context, id string) (automation.MessageEvent, bool, error)
}

// WorkflowReader reads the workflow a run executes, and the account it
// publishes under.
type WorkflowReader interface {
	GetWorkflow(ctx context.Context, id int64) (automation.Workflow, bool, error)
	WorkflowAuthor(ctx context.Context, workflowID int64) (int64, bool, error)
}

// RunStore is the run history the engine writes as it works.
type RunStore interface {
	BeginWorkflowRun(ctx context.Context, in channelstore.BeginWorkflowRunRequest) (automation.WorkflowRun, bool, error)
	StartWorkflowRun(ctx context.Context, runID int64) (automation.WorkflowRun, error)
	RecordWorkflowRunAI(ctx context.Context, runID int64, update channelstore.WorkflowRunAIUpdate) error
	CompleteWorkflowRun(ctx context.Context, runID int64, status, lastErrorCode, lastErrorMessage string) error
	EnsureWorkflowPublication(ctx context.Context, runID int64,
		dest automation.WorkflowPublication) (automation.WorkflowPublication, bool, error)
	FinishWorkflowPublication(ctx context.Context, id int64, status string,
		remoteMessageID, remotePostID int64, remoteURL, errorCode, errorMessage string) error
	WorkflowPublications(ctx context.Context, runID int64) ([]automation.WorkflowPublication, error)
}

// NetworkKeys resolves the Telegram network one configured client belongs to.
//
// It is what keeps a cross-network publish recorded under the network the
// message actually landed on: the loop-prevention record must match the network
// ingestion will see it under, not the one the source came from.
type NetworkKeys interface {
	NetworkKey(clientKey string) (string, bool)
}

// Handler executes one queued workflow run. It is the automation.JobHandler for
// automation.JobWorkflowRun, where the job's target is the workflow and its
// event is the source message.
//
// A run is one row and one row only: the content it prepared, its AI outcome
// and every destination's result are recorded as they happen, so a retry after
// a crash resumes rather than starting again. Nothing here sends to Telegram or
// to WordPress directly — every publication goes through the publishing
// service, exactly as an interactive caller's does.
type Handler struct {
	Workflows WorkflowReader
	Runs      RunStore
	Events    EventReader
	Publisher automation.PublishingService
	// AI polishes one message. Nil means this deployment has no AI, which is
	// reported as an AI failure when a workflow asks for one: a run never
	// publishes polished content it did not get.
	AI automation.AIProcessor
	// Outbound records the messages this installation put into a Telegram
	// channel. Ingestion consults it, which is what stops a workflow from
	// reacting to its own publications.
	Outbound automation.OutboundMarker
	// Networks resolves a destination client's network for that record. Nil
	// leaves the loop-prevention record unwritten, which is logged.
	Networks NetworkKeys
	// Workers reads the source attachment when a workflow sends images to the
	// AI. Images excluded from the AI never leave the destination without them.
	Workers publishing.Registry
	// Logf receives one line per stage that failed without failing the run.
	Logf func(format string, args ...any)
}

func (h *Handler) logf(format string, args ...any) {
	if h != nil && h.Logf != nil {
		h.Logf(format, args...)
	}
}

// stopStatuses are the run states work never continues from.
//
// A run that published everywhere is done, and one an operator switched the
// workflow off for is cancelled on purpose. A run that failed, or that reached
// only some of its destinations, is resumable: a later attempt publishes what
// did not succeed and never repeats what did.
var stopStatuses = map[string]bool{
	channelstore.WorkflowRunPublished:             true,
	channelstore.WorkflowRunPublishedWithFallback: true,
	channelstore.WorkflowRunCancelled:             true,
}

// HandleJob executes one queued workflow run.
//
// The sequence is fixed: load the workflow, record the run, prepare what is to
// be published, polish it if the workflow asked for that, then publish to each
// destination and record what each one did. A failure is recorded against the
// run and returned to the queue as a classified error, so the queue decides
// about another attempt and never this code.
func (h *Handler) HandleJob(ctx context.Context, job automation.Job) error {
	workflow, found, err := h.Workflows.GetWorkflow(ctx, job.TargetID)
	if err != nil {
		return err
	}
	if !found {
		return &automation.JobError{Code: "workflow_not_found",
			Message: "The workflow this job was queued for no longer exists.", Retryable: false}
	}

	event, found, err := h.Events.AutomationEvent(ctx, job.EventID)
	if err != nil {
		return err
	}
	if !found {
		return &automation.JobError{Code: "event_not_found",
			Message: "The source message this run was queued for is no longer recorded.", Retryable: false}
	}

	run, created, err := h.Runs.BeginWorkflowRun(ctx, channelstore.BeginWorkflowRunRequest{
		WorkflowID:      workflow.ID,
		EventID:         event.ID,
		SourceMessageID: event.MessageID,
		OriginalContent: event.Payload.Message.Text,
	})
	if err != nil {
		return err
	}
	if !created && stopStatuses[run.Status] {
		// A previous attempt already decided this run. Redelivering the job must
		// not publish again, whatever the outcome was.
		return nil
	}

	// A workflow switched off after work was queued publishes nothing. The run
	// is closed with the reason rather than left open.
	if !workflow.Enabled {
		_ = h.Runs.CompleteWorkflowRun(ctx, run.ID, channelstore.WorkflowRunCancelled,
			"workflow_disabled", "The workflow was disabled before this run published.")
		return nil
	}

	if _, err := h.Runs.StartWorkflowRun(ctx, run.ID); err != nil {
		return err
	}

	prepared := h.prepare(ctx, workflow, run, event)
	if prepared.blocked != nil {
		_ = h.Runs.CompleteWorkflowRun(ctx, run.ID, channelstore.WorkflowRunFailed,
			prepared.blocked.Code, prepared.blocked.Message)
		return prepared.blocked
	}

	return h.publishAll(ctx, workflow, run, event, prepared)
}

// prepared is what a run will publish, and how it got that way.
type prepared struct {
	content string
	// entities are the source message's formatting spans. They are carried only
	// when the source text itself is published: a polished text is new text and
	// never inherits the spans of the old one.
	entities  []rpc.Entity
	aiStatus  string
	fallback  bool
	aiTitle   string
	aiModel   string
	aiErrCode string
	aiMessage string
	// blocked is set when the run must stop here and publish nothing.
	blocked *automation.JobError
}

// prepare decides what this run publishes: the source content, or the polished
// text the AI produced.
//
// The AI is called at most once per message. A run that already has its polished
// text keeps it across retries, and one that already fell back keeps falling
// back: a retry never asks the model again and never changes its mind about the
// policy.
func (h *Handler) prepare(ctx context.Context, workflow automation.Workflow, run automation.WorkflowRun,
	event automation.MessageEvent) prepared {

	out := prepared{content: run.OriginalContent, entities: event.Payload.Message.Entities}

	switch {
	case run.AIStatus == channelstore.WorkflowAISucceeded && run.PolishedContent != "":
		out.content = run.PolishedContent
		out.entities = nil
		out.aiStatus = channelstore.WorkflowAISucceeded
		out.aiModel = run.AIModel
		return out
	case run.AIStatus == channelstore.WorkflowAIFallbackUsed:
		// The model failed once and the workflow allowed the original through.
		// It stays that way: the failure is recorded and the source is published.
		out.aiStatus = channelstore.WorkflowAIFallbackUsed
		out.fallback = true
		out.aiModel = run.AIModel
		out.aiErrCode = run.AIErrorCode
		out.aiMessage = run.AIErrorMessage
		return out
	case !workflow.AIEnabled:
		out.aiStatus = channelstore.WorkflowAINotUsed
		return out
	}

	result, err := h.polish(ctx, workflow, run, event)
	if err != nil {
		failure := aiFailure(err)
		out.aiErrCode = failure.Code
		out.aiMessage = failure.Message
		if !workflow.AIPublishOnFailure {
			// The default: an AI that failed blocks publication. The run is
			// finished as failed and the source content is not sent anywhere.
			out.aiStatus = channelstore.WorkflowAIFailed
			out.blocked = failure
			_ = h.Runs.RecordWorkflowRunAI(ctx, run.ID, channelstore.WorkflowRunAIUpdate{
				Status: channelstore.WorkflowAIFailed, Model: out.aiModel,
				ErrorCode: failure.Code, ErrorMessage: failure.Message,
			})
			return out
		}
		// The workflow opted into the fallback: the source content is published
		// as it is, and the AI stage is never reported as a success.
		out.aiStatus = channelstore.WorkflowAIFallbackUsed
		out.fallback = true
		_ = h.Runs.RecordWorkflowRunAI(ctx, run.ID, channelstore.WorkflowRunAIUpdate{
			Status: channelstore.WorkflowAIFallbackUsed, FallbackUsed: true, Model: out.aiModel,
			ErrorCode: failure.Code, ErrorMessage: failure.Message,
		})
		return out
	}

	out.content = result.Text
	out.entities = nil
	out.aiStatus = channelstore.WorkflowAISucceeded
	out.aiTitle = result.Title
	out.aiModel = result.Model
	_ = h.Runs.RecordWorkflowRunAI(ctx, run.ID, channelstore.WorkflowRunAIUpdate{
		Status: channelstore.WorkflowAISucceeded, Polished: result.Text, Model: result.Model,
		InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, Duration: result.Duration,
	})
	return out
}

// polish calls the AI for one message. Images are offered only when the
// workflow opted in, and a source attachment the AI never sees is still
// published to the destinations.
func (h *Handler) polish(ctx context.Context, workflow automation.Workflow, run automation.WorkflowRun,
	event automation.MessageEvent) (automation.PolishResult, error) {

	if h.AI == nil {
		return automation.PolishResult{}, &automation.JobError{Code: "ai_not_configured",
			Message: "This deployment has no AI polishing service configured.", Retryable: false}
	}
	input := automation.PolishInput{
		WorkflowID: workflow.ID,
		Prompt:     workflow.AIPrompt,
		Text:       run.OriginalContent,
	}
	if workflow.AISendImages {
		input.Images = h.imagesFor(ctx, event)
	}
	return h.AI.Polish(ctx, input)
}

// imagesFor reads the source message's attachment as AI input.
//
// The bytes are read through the connection that recorded the message and are
// held only for this call. A read that fails is logged and the request goes
// without the image: the destination still publishes the attachment, which is
// decided somewhere else entirely.
func (h *Handler) imagesFor(ctx context.Context, event automation.MessageEvent) []automation.ImageInput {
	media := event.Payload.Message.Media
	if media == nil || !imageMedia(media) {
		return nil
	}
	if h.Workers == nil {
		return nil
	}
	worker, ok := h.Workers.Worker(event.SourceClientKey)
	if !ok {
		h.logf("workflows: run=%s cannot read source media: client %s is unavailable",
			event.ID, event.SourceClientKey)
		return nil
	}
	stream, err := worker.Media(ctx, event.TelegramChannelID, int(event.MessageID),
		largestPhotoSize, rpc.MediaSlice{})
	if err != nil {
		h.logf("workflows: run=%s source media download failed: client=%s channel=%d message=%d: %v",
			event.ID, event.SourceClientKey, event.TelegramChannelID, event.MessageID, err)
		return nil
	}
	data, err := stream.ReadAll()
	if err != nil {
		h.logf("workflows: run=%s source media read failed: client=%s channel=%d message=%d: %v",
			event.ID, event.SourceClientKey, event.TelegramChannelID, event.MessageID, err)
		return nil
	}
	return []automation.ImageInput{{
		MimeType: stream.MimeType,
		Filename: media.Filename,
		Data:     data,
	}}
}

// largestPhotoSize is the photo size an attachment is read at: 0 is the
// largest, exactly as every other reader of an attachment uses.
const largestPhotoSize = 0

// imageMedia reports whether an attachment is something an image-capable model
// can be shown.
func imageMedia(media *rpc.Media) bool {
	if media == nil {
		return false
	}
	if media.Type == "photo" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(media.MimeType), "image/")
}

// aiFailure is the classified AI error the run records and the queue reads.
//
// An error the AI processor already classified keeps its category and its
// retryable flag; one that arrived unclassified is retryable, because an
// unclassified failure is not proof that the model refused.
func aiFailure(err error) *automation.JobError {
	var jobErr *automation.JobError
	if errors.As(err, &jobErr) && jobErr != nil {
		return jobErr
	}
	return &automation.JobError{Code: "ai_unknown", Message: errMessage(err), Retryable: true}
}

func errMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
