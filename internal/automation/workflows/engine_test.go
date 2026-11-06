package workflows_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/automation/workflows"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// One source message runs one workflow once, publishes to every destination it
// is configured with, and never publishes to a destination twice. What the AI
// decided, and what each destination did, is recorded as it happens so a retry
// after a crash resumes instead of starting again.

// --- doubles ---

// fakePublisher is the publishing service the engine drives. It records every
// request and answers from the result or error named for that destination.
type fakePublisher struct {
	calls   []automation.PublishRequest
	results map[string]automation.PublishResult
	errors  map[string]error
}

func newFakePublisher() *fakePublisher {
	return &fakePublisher{results: map[string]automation.PublishResult{}, errors: map[string]error{}}
}

// address is how one destination is named in the doubles.
func address(req automation.PublishRequest) string {
	return req.Destination.ClientKey + "/" + strconv.FormatInt(req.Destination.ChannelID, 10) +
		"/" + strconv.FormatInt(req.Destination.SiteID, 10)
}

func (f *fakePublisher) Publish(_ context.Context, req automation.PublishRequest) (automation.PublishResult, error) {
	f.calls = append(f.calls, req)
	key := address(req)
	res, known := f.results[key]
	if !known {
		res = automation.PublishResult{Outcome: automation.PublishSucceeded, RemoteMessageID: 1000 + int64(len(f.calls))}
	}
	if err := f.errors[key]; err != nil {
		if !known {
			res = automation.PublishResult{Outcome: automation.PublishFailed, ErrorCode: "publish_failed"}
		}
		return res, err
	}
	return res, nil
}

// on wires the answer for one destination. A nil error clears an earlier one.
func (f *fakePublisher) on(dest string, res automation.PublishResult, err error) {
	f.results[dest] = res
	if err == nil {
		delete(f.errors, dest)
		return
	}
	f.errors[dest] = err
}

func (f *fakePublisher) texts() []string {
	out := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		out = append(out, call.Text)
	}
	return out
}

// fakeAI is one polishing service.
type fakeAI struct {
	calls  []automation.PolishInput
	result automation.PolishResult
	err    error
}

func (f *fakeAI) Polish(_ context.Context, in automation.PolishInput) (automation.PolishResult, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return automation.PolishResult{}, f.err
	}
	return f.result, nil
}

// fakeMarker is the loop-prevention record.
type fakeMarker struct {
	messages []automation.OutboundMessage
}

func (f *fakeMarker) IsOwnPublication(context.Context, string, string, int64, int64) (bool, error) {
	return false, nil
}

func (f *fakeMarker) MarkPublished(_ context.Context, m automation.OutboundMessage) error {
	f.messages = append(f.messages, m)
	return nil
}

// fakeNetworks names the network each configured client belongs to.
type fakeNetworks map[string]string

func (n fakeNetworks) NetworkKey(clientKey string) (string, bool) {
	key, ok := n[clientKey]
	return key, ok
}

// fakeWorker is one Telegram connection, used to read the source attachment.
type fakeWorker struct {
	media     []byte
	mediaType string
}

func (w *fakeWorker) Message(context.Context, int64, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{}, errors.New("not used")
}

func (w *fakeWorker) Media(context.Context, int64, int, int, rpc.MediaSlice) (rpc.MediaStream, error) {
	return rpc.MediaStream{
		MimeType: w.mediaType,
		Size:     int64(len(w.media)),
		Body:     &byteReader{data: w.media},
	}, nil
}

func (w *fakeWorker) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	return rpc.ChannelResult{}, errors.New("not used")
}

func (w *fakeWorker) SendMessage(context.Context, rpc.SendRequest) (rpc.SendResult, error) {
	return rpc.SendResult{}, errors.New("not used")
}

// fakeWorkers is a registry of them.
type fakeWorkers map[string]*fakeWorker

func (r fakeWorkers) Worker(key string) (publishing.Worker, bool) {
	worker, ok := r[key]
	if !ok {
		return nil, false
	}
	return worker, true
}

// byteReader is the body of a media stream.
type byteReader struct {
	data []byte
	pos  int
}

func (b *byteReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}

func (b *byteReader) Close() error { return nil }

// --- world ---

// env is one workflow on one source channel, with a real store underneath: the
// run and publication rows are the ones production writes.
type env struct {
	t        *testing.T
	store    *channelstore.Store
	user     channelstore.User
	channel  channelstore.Channel
	workflow automation.Workflow
	eventID  string
	handler  *workflows.Handler
	publish  *fakePublisher
	ai       *fakeAI
	marker   *fakeMarker
	workers  fakeWorkers
}

func newEnv(t *testing.T, mutate ...func(*automation.WorkflowInput)) *env {
	t.Helper()
	ctx := context.Background()
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state", "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	user, err := store.CreateUser(ctx, "Workflow Owner", "wf-owner", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	channel, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "source", ChannelID: 555, PeerType: channelstore.PeerChannel,
		Title: "Source", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	in := automation.WorkflowInput{
		ManagedChannelID: channel.ID,
		CreatedBy:        user.ID,
		Enabled:          true,
		Destinations: []automation.WorkflowDestination{
			{DestinationType: string(automation.DestinationTelegram), ClientKey: "dest-a", ChannelID: 111, Enabled: true},
			{DestinationType: string(automation.DestinationTelegram), ClientKey: "dest-b", ChannelID: 222, Enabled: true},
		},
	}
	for _, fn := range mutate {
		fn(&in)
	}
	workflow, err := store.CreateWorkflow(ctx, in)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	e := &env{
		t: t, store: store, user: user, channel: channel, workflow: workflow,
		eventID: insertEvent(t, store, channel.ID, 9, "hello #news", nil),
		publish: newFakePublisher(),
		ai:      &fakeAI{result: automation.PolishResult{Text: "A polished hello.", Title: "Polished", Model: "test-model"}},
		marker:  &fakeMarker{},
		workers: fakeWorkers{"source": {media: []byte("bytes"), mediaType: "image/jpeg"}},
	}
	e.handler = &workflows.Handler{
		Workflows: store,
		Runs:      store,
		Events:    store,
		Publisher: e.publish,
		AI:        e.ai,
		Outbound:  e.marker,
		Networks:  fakeNetworks{"dest-a": "official", "dest-b": "custom@endpoint"},
		Workers:   e.workers,
	}
	return e
}

// insertEvent records one source message as a stored automation event.
func insertEvent(t *testing.T, store *channelstore.Store, channelID, messageID int64, text string,
	media *rpc.Media) string {

	t.Helper()
	now := time.Now().UTC()
	id := "evt_wf_" + time.Now().Format("150405.000000000") + "_" + strconv.FormatInt(messageID, 10)
	inserted, err := store.InsertMessageEvent(context.Background(), automation.MessageEvent{
		ID: id, Type: automation.EventMessageCreated, NetworkKey: "official", SourceClientKey: "source",
		ManagedChannelID: channelID, TelegramChannelID: 555, MessageID: messageID,
		OccurredAt: now, ReceivedAt: now,
		Payload: automation.MessagePayload{Message: rpc.Message{ID: int(messageID), Text: text, Media: media,
			Entities: []rpc.Entity{{Type: "hashtag", Offset: 6, Length: 5}}}},
	})
	if err != nil || !inserted {
		t.Fatalf("InsertMessageEvent: inserted=%v err=%v", inserted, err)
	}
	return id
}

func (e *env) job() automation.Job {
	return automation.Job{ID: 1, EventID: e.eventID, Kind: automation.JobWorkflowRun,
		TargetID: e.workflow.ID, Attempt: 1, MaxAttempts: 5}
}

func (e *env) run(t *testing.T) automation.WorkflowRun {
	t.Helper()
	page, err := e.store.ListRuns(context.Background(), e.workflow.ID, automation.RunListOptions{})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(page.Runs) != 1 {
		t.Fatalf("runs = %d, want exactly one", len(page.Runs))
	}
	return page.Runs[0]
}

func (e *env) handle() error {
	return e.handler.HandleJob(context.Background(), e.job())
}

// withSourceMedia gives the source message an attachment to publish.
func (e *env) withSourceMedia() {
	e.eventID = insertEvent(e.t, e.store, e.channel.ID, 12, "a caption",
		&rpc.Media{ID: "555:12", Type: "photo", MimeType: "image/jpeg", Filename: "photo.jpg", Size: 5})
}

// --- the source content ---

func TestARunPublishesTheSourceContentToEveryDestination(t *testing.T) {
	e := newEnv(t)
	if err := e.handle(); err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if len(e.publish.calls) != 2 {
		t.Fatalf("publications = %d, want one per destination", len(e.publish.calls))
	}
	if e.publish.calls[0].Text != "hello #news" || e.publish.calls[1].Text != "hello #news" {
		t.Fatalf("texts = %v, want the source content", e.publish.texts())
	}
	// The source's own formatting travels with the source content.
	if len(e.publish.calls[0].Entities) != 1 {
		t.Fatalf("entities = %v", e.publish.calls[0].Entities)
	}
	// Each destination is addressed on its own connection.
	if e.publish.calls[0].Destination.ClientKey != "dest-a" || e.publish.calls[1].Destination.ClientKey != "dest-b" {
		t.Fatalf("clients = %q, %q", e.publish.calls[0].Destination.ClientKey, e.publish.calls[1].Destination.ClientKey)
	}
	run := e.run(t)
	if run.Status != channelstore.WorkflowRunPublished {
		t.Fatalf("status = %q, want %q", run.Status, channelstore.WorkflowRunPublished)
	}
	if run.OriginalContent != "hello #news" || run.PolishedContent != "" {
		t.Fatalf("content = %+v", run)
	}
	if run.AIStatus != channelstore.WorkflowAINotUsed {
		t.Fatalf("ai status = %q, want %q", run.AIStatus, channelstore.WorkflowAINotUsed)
	}
	if len(e.ai.calls) != 0 {
		t.Fatal("a workflow with AI off must not call the model")
	}
}

// An AI that writes the text changes what is published and nothing else: the
// source message keeps its own words, and the polished text is kept apart.
func TestPolishedContentIsPublishedAndTheSourceIsUnchanged(t *testing.T) {
	e := newEnv(t, func(in *automation.WorkflowInput) {
		in.AIEnabled = true
		in.AIPrompt = "polish it"
	})
	if err := e.handle(); err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if len(e.ai.calls) != 1 {
		t.Fatalf("AI calls = %d, want 1", len(e.ai.calls))
	}
	if e.ai.calls[0].Text != "hello #news" || e.ai.calls[0].Prompt != "polish it" {
		t.Fatalf("AI input = %+v", e.ai.calls[0])
	}
	if len(e.publish.calls) != 2 || e.publish.calls[0].Text != "A polished hello." {
		t.Fatalf("published = %v", e.publish.texts())
	}
	// A polished text is new text: it never inherits the source's spans.
	if len(e.publish.calls[0].Entities) != 0 {
		t.Fatalf("entities = %v, want none", e.publish.calls[0].Entities)
	}
	run := e.run(t)
	if run.OriginalContent != "hello #news" || run.PolishedContent != "A polished hello." {
		t.Fatalf("content = %+v", run)
	}
	if run.AIStatus != channelstore.WorkflowAISucceeded || run.AIModel != "test-model" {
		t.Fatalf("ai = %+v", run)
	}
}

// --- AI failure policy ---

func TestAnAIFailureBlocksPublicationUnlessTheWorkflowAllowsTheFallback(t *testing.T) {
	failure := &automation.JobError{Code: "ai_timeout", Message: "the model did not answer in time", Retryable: true}

	t.Run("the default blocks publication", func(t *testing.T) {
		e := newEnv(t, func(in *automation.WorkflowInput) { in.AIEnabled = true })
		e.ai.err = failure

		err := e.handle()
		if err == nil {
			t.Fatal("a blocked run must report itself to the queue")
		}
		var jobErr *automation.JobError
		if !errors.As(err, &jobErr) || jobErr.Code != "ai_timeout" {
			t.Fatalf("error = %v, want the AI's own classification", err)
		}
		if !jobErr.Retryable {
			t.Fatal("a retryable AI failure must stay retryable")
		}
		if len(e.publish.calls) != 0 {
			t.Fatal("a failed AI stage must not publish anything")
		}
		run := e.run(t)
		if run.Status != channelstore.WorkflowRunFailed {
			t.Fatalf("status = %q, want %q", run.Status, channelstore.WorkflowRunFailed)
		}
		if run.AIStatus != channelstore.WorkflowAIFailed {
			t.Fatalf("ai status = %q, want %q", run.AIStatus, channelstore.WorkflowAIFailed)
		}
		if run.AIErrorCode != "ai_timeout" || run.AIErrorMessage == "" {
			t.Fatalf("ai error = %+v", run)
		}
	})

	t.Run("opting in publishes the original and never calls the AI a success", func(t *testing.T) {
		e := newEnv(t, func(in *automation.WorkflowInput) {
			in.AIEnabled = true
			in.AIPublishOnFailure = true
		})
		e.ai.err = failure

		if err := e.handle(); err != nil {
			t.Fatalf("HandleJob: %v", err)
		}
		if len(e.publish.calls) != 2 || e.publish.calls[0].Text != "hello #news" {
			t.Fatalf("published = %v, want the original content", e.publish.texts())
		}
		run := e.run(t)
		if run.Status != channelstore.WorkflowRunPublishedWithFallback {
			t.Fatalf("status = %q, want %q", run.Status, channelstore.WorkflowRunPublishedWithFallback)
		}
		if run.AIStatus != channelstore.WorkflowAIFallbackUsed || !run.AIFallbackUsed {
			t.Fatalf("ai = %+v, want the fallback recorded and never a success", run)
		}
		if run.PolishedContent != "" {
			t.Fatalf("polished content = %q, want empty", run.PolishedContent)
		}
	})
}

// A model that already answered is never asked twice: the saved text is reused
// when the run comes back to reach the destinations it has not reached.
func TestARetryReusesThePolishedOutputInsteadOfCallingTheAIAgain(t *testing.T) {
	e := newEnv(t, func(in *automation.WorkflowInput) { in.AIEnabled = true })
	e.publish.on("dest-a/111/0", automation.PublishResult{Outcome: automation.PublishSucceeded, RemoteMessageID: 1}, nil)
	e.publish.on("dest-b/222/0", automation.PublishResult{
		Outcome: automation.PublishFailed, ErrorCode: "publish_failed", ErrorMessage: "nope",
	}, errors.New("nope"))

	if err := e.handle(); err == nil {
		t.Fatal("a run that did not reach every destination must report itself")
	}
	if len(e.ai.calls) != 1 {
		t.Fatalf("AI calls = %d, want 1", len(e.ai.calls))
	}

	e.publish.on("dest-b/222/0", automation.PublishResult{Outcome: automation.PublishSucceeded, RemoteMessageID: 2}, nil)
	if err := e.handle(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(e.ai.calls) != 1 {
		t.Fatalf("AI calls = %d, want 1 — a retry must not ask the model again", len(e.ai.calls))
	}
	if len(e.publish.calls) != 3 {
		t.Fatalf("publications = %d, want 3 (2 first, 1 retry)", len(e.publish.calls))
	}
	if got := e.publish.calls[2].Text; got != "A polished hello." {
		t.Fatalf("retry published %q, want the saved polished text", got)
	}
	run := e.run(t)
	if run.Status != channelstore.WorkflowRunPublished {
		t.Fatalf("status = %q, want %q after the retry", run.Status, channelstore.WorkflowRunPublished)
	}
	if run.PolishedContent != "A polished hello." {
		t.Fatalf("polished = %q", run.PolishedContent)
	}
}

// --- per-destination outcomes ---

func TestAPublishedDestinationIsNeverPublishedAgain(t *testing.T) {
	e := newEnv(t)
	e.publish.on("dest-b/222/0", automation.PublishResult{
		Outcome: automation.PublishFailed, ErrorCode: "publish_failed", ErrorMessage: "the destination refused",
	}, errors.New("refused"))

	if err := e.handle(); err == nil {
		t.Fatal("a run with a failed destination must report itself")
	}
	run := e.run(t)
	if run.Status != channelstore.WorkflowRunPartiallyPublished {
		t.Fatalf("status = %q, want %q", run.Status, channelstore.WorkflowRunPartiallyPublished)
	}
	published := 0
	for _, pub := range run.Publications {
		if pub.Status == channelstore.WorkflowPublicationPublished {
			published++
		}
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1 of %d", published, len(run.Publications))
	}

	e.publish.on("dest-b/222/0", automation.PublishResult{Outcome: automation.PublishSucceeded, RemoteMessageID: 9}, nil)
	if err := e.handle(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(e.publish.calls) != 3 {
		t.Fatalf("publications = %d, want 3 — only the failed destination may be retried", len(e.publish.calls))
	}
	if retried := e.publish.calls[2]; retried.Destination.ClientKey != "dest-b" {
		t.Fatalf("retry went to %q, want the destination that failed", retried.Destination.ClientKey)
	}
	run = e.run(t)
	if run.Status != channelstore.WorkflowRunPublished {
		t.Fatalf("status = %q, want %q", run.Status, channelstore.WorkflowRunPublished)
	}
}

// The key one destination publishes under names the run and the destination, so
// a retry reuses the stored attempt instead of posting again.
func TestTheIdempotencyKeyIsStableAcrossRetries(t *testing.T) {
	e := newEnv(t)
	e.publish.on("dest-a/111/0", automation.PublishResult{
		Outcome: automation.PublishUnknown, ErrorCode: "publish_outcome_unknown",
		ErrorMessage: "the outcome is not confirmed",
	}, errors.New("unknown"))
	e.publish.on("dest-b/222/0", automation.PublishResult{
		Outcome: automation.PublishUnknown, ErrorCode: "publish_outcome_unknown",
	}, errors.New("unknown"))

	if err := e.handle(); err == nil {
		t.Fatal("an unconfirmed outcome must report itself")
	}
	first := e.publish.calls[0].IdempotencyKey
	if first == "" {
		t.Fatal("a publication must carry an idempotency key")
	}
	run := e.run(t)
	for _, pub := range run.Publications {
		if pub.Status != channelstore.WorkflowPublicationUnknown {
			t.Fatalf("publication = %+v, want the outcome recorded as unknown", pub)
		}
	}

	e.publish.on("dest-a/111/0", automation.PublishResult{Outcome: automation.PublishSucceeded, RemoteMessageID: 71}, nil)
	e.publish.on("dest-b/222/0", automation.PublishResult{Outcome: automation.PublishSucceeded, RemoteMessageID: 72}, nil)
	if err := e.handle(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if e.publish.calls[2].IdempotencyKey != first {
		t.Fatalf("retry key = %q, want %q", e.publish.calls[2].IdempotencyKey, first)
	}
	if run = e.run(t); run.Status != channelstore.WorkflowRunPublished {
		t.Fatalf("status = %q", run.Status)
	}
}

// --- loop prevention ---

// What this installation published is recorded under the destination's own
// network, so the message is recognised when it comes back and does not run the
// workflow again.
func TestAPublishedMessageIsRecordedUnderTheDestinationsNetwork(t *testing.T) {
	e := newEnv(t)
	if err := e.handle(); err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if len(e.marker.messages) != 2 {
		t.Fatalf("outbound records = %d, want one per published Telegram message", len(e.marker.messages))
	}
	first := e.marker.messages[0]
	if first.NetworkKey != "official" || first.ClientKey != "dest-a" || first.ChannelID != 111 {
		t.Fatalf("outbound = %+v", first)
	}
	if first.Origin != "workflow" {
		t.Fatalf("origin = %q, want workflow", first.Origin)
	}
	run := e.run(t)
	if first.OriginID != run.ID {
		t.Fatalf("origin id = %d, want the run id %d", first.OriginID, run.ID)
	}
	if first.RemoteMessageID == 0 {
		t.Fatal("the record must carry the message id the send confirmed")
	}
	// A cross-network publish is recorded under the network it landed on.
	if second := e.marker.messages[1]; second.NetworkKey != "custom@endpoint" {
		t.Fatalf("network = %q, want the destination's own", second.NetworkKey)
	}
}

// --- media ---

// Images the AI never saw are still published: excluding them from the model
// never excludes them from the destination.
func TestExcludingImagesFromTheAIStillPublishesThem(t *testing.T) {
	e := newEnv(t, func(in *automation.WorkflowInput) {
		in.AIEnabled = true
		in.AISendImages = false
	})
	e.withSourceMedia()

	if err := e.handle(); err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if len(e.ai.calls) != 1 || len(e.ai.calls[0].Images) != 0 {
		t.Fatalf("AI images = %d, want none", len(e.ai.calls[0].Images))
	}
	for _, call := range e.publish.calls {
		if call.Options.MediaMode == "omit" {
			t.Fatalf("the published request drops the media: %+v", call.Options)
		}
	}
}

func TestImagesAreSentToTheAIOnlyWhenTheWorkflowAsks(t *testing.T) {
	e := newEnv(t, func(in *automation.WorkflowInput) {
		in.AIEnabled = true
		in.AISendImages = true
	})
	e.withSourceMedia()

	if err := e.handle(); err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if len(e.ai.calls) != 1 {
		t.Fatalf("AI calls = %d", len(e.ai.calls))
	}
	images := e.ai.calls[0].Images
	if len(images) != 1 {
		t.Fatalf("AI images = %d, want 1", len(images))
	}
	if images[0].MimeType != "image/jpeg" || string(images[0].Data) != "bytes" {
		t.Fatalf("image = %+v", images[0])
	}
}

// --- configuration and lifecycle ---

// A workflow an operator switched off after the work was queued publishes
// nothing, and says why.
func TestARunIsCancelledWhenTheWorkflowIsDisabledAfterEnqueue(t *testing.T) {
	e := newEnv(t)
	input := automation.WorkflowInput{
		ManagedChannelID: e.channel.ID, CreatedBy: e.user.ID, Enabled: false,
		Destinations: e.workflow.Destinations,
	}
	if _, found, err := e.store.UpdateWorkflow(context.Background(), e.workflow.ID, input); err != nil || !found {
		t.Fatalf("UpdateWorkflow: found=%v err=%v", found, err)
	}

	if err := e.handle(); err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if len(e.publish.calls) != 0 {
		t.Fatal("a disabled workflow must not publish")
	}
	run := e.run(t)
	if run.Status != channelstore.WorkflowRunCancelled {
		t.Fatalf("status = %q, want %q", run.Status, channelstore.WorkflowRunCancelled)
	}
	if run.LastErrorCode != "workflow_disabled" || run.LastErrorMessage == "" {
		t.Fatalf("reason = %+v, want the switch-off recorded", run)
	}
}

// One event runs one workflow once, however often the queue redelivers it.
func TestOneEventRunsOneWorkflowOnce(t *testing.T) {
	e := newEnv(t)
	if err := e.handle(); err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if err := e.handle(); err != nil {
		t.Fatalf("second HandleJob: %v", err)
	}
	page, err := e.store.ListRuns(context.Background(), e.workflow.ID, automation.RunListOptions{})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("runs = %d, want 1", page.Total)
	}
	if len(e.publish.calls) != 2 {
		t.Fatalf("publications = %d, want 2 — a redelivered job must not publish again", len(e.publish.calls))
	}
}

// A run works from the destinations it recorded, so editing the workflow after
// work was queued cannot change what the queued run does.
func TestAQueuedRunKeepsTheDestinationsItWasQueuedWith(t *testing.T) {
	e := newEnv(t)
	e.publish.on("dest-b/222/0", automation.PublishResult{
		Outcome: automation.PublishFailed, ErrorCode: "publish_failed", ErrorMessage: "refused",
	}, errors.New("refused"))
	if err := e.handle(); err == nil {
		t.Fatal("a run with a failed destination must report itself")
	}

	// The operator removes the destination that failed.
	updated := automation.WorkflowInput{
		ManagedChannelID: e.channel.ID, CreatedBy: e.user.ID, Enabled: true,
		Destinations: e.workflow.Destinations[:1],
	}
	if _, found, err := e.store.UpdateWorkflow(context.Background(), e.workflow.ID, updated); err != nil || !found {
		t.Fatalf("UpdateWorkflow: found=%v err=%v", found, err)
	}

	e.publish.on("dest-b/222/0", automation.PublishResult{Outcome: automation.PublishSucceeded, RemoteMessageID: 5}, nil)
	if err := e.handle(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(e.publish.calls) != 3 {
		t.Fatalf("publications = %d, want 3 — the recorded destination is retried even though it was removed",
			len(e.publish.calls))
	}
	if e.publish.calls[2].Destination.ClientKey != "dest-b" {
		t.Fatalf("retry went to %q", e.publish.calls[2].Destination.ClientKey)
	}
}

// A run whose workflow no longer exists is closed with its reason rather than
// left to be retried forever.
func TestARunWhoseWorkflowIsGoneReportsWhy(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.DeleteWorkflow(context.Background(), e.workflow.ID); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}

	err := e.handle()
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) || jobErr.Code != "workflow_not_found" {
		t.Fatalf("error = %v, want the missing-workflow reason", err)
	}
	if jobErr.Retryable {
		t.Fatal("a deleted workflow cannot come back by retrying")
	}
}

func TestARunPublishesUnderTheAccountThatCreatedTheWorkflow(t *testing.T) {
	e := newEnv(t)
	if err := e.handle(); err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	for _, call := range e.publish.calls {
		if call.UserID != e.user.ID {
			t.Fatalf("published as user %d, want %d", call.UserID, e.user.ID)
		}
	}
}

// A workflow made through the operator token has no owning account. Its runs
// stop with that reason recorded instead of publishing under authority no
// account holds.
func TestARunWithoutAnOwningAccountIsRefusedWithItsReason(t *testing.T) {
	e := newEnv(t, func(in *automation.WorkflowInput) { in.CreatedBy = 0 })

	err := e.handle()
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) || jobErr.Code != "workflow_author_missing" {
		t.Fatalf("error = %v, want the missing-author reason", err)
	}
	if jobErr.Retryable {
		t.Fatal("an unowned workflow cannot gain an author by retrying")
	}
	if len(e.publish.calls) != 0 {
		t.Fatalf("publications = %d, want 0", len(e.publish.calls))
	}
	run := e.run(t)
	if run.Status != channelstore.WorkflowRunFailed {
		t.Fatalf("status = %q", run.Status)
	}
	if run.LastErrorCode != "workflow_author_missing" || run.LastErrorMessage == "" {
		t.Fatalf("reason = %+v", run)
	}
}
