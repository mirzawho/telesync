package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/events"
	"github.com/mirza/telesync/internal/rpc"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func source(t *testing.T, mutate ...func(*events.Source)) events.Source {
	t.Helper()
	s := events.Source{
		ClientKey:         "official",
		NetworkKey:        "official",
		ManagedChannelID:  7,
		TelegramChannelID: 123456789,
		Message: rpc.Message{
			ID:   36,
			Date: at(t, "2026-11-05T12:30:00Z"),
			Text: "Example message",
			Media: &rpc.Media{
				ID:       "123456789:36",
				Type:     "document",
				Filename: "example.apk",
				MimeType: "application/vnd.android.package-archive",
				Size:     59797754,
			},
			Views:     intPtr(45),
			Reactions: []rpc.Reaction{},
			Entities:  []rpc.Entity{},
		},
		ReceivedAt: at(t, "2026-11-05T12:31:00Z"),
	}
	for _, fn := range mutate {
		fn(&s)
	}
	return s
}

func intPtr(v int) *int { return &v }

// A stored event must be a faithful, stable snapshot of what the channel
// messages API reports, because it is the only copy left once the message has
// scrolled out of reach.
func TestAnObservationProducesOneNormalizedEvent(t *testing.T) {
	s := source(t)
	got := s.Event()

	if got.Type != automation.EventMessageCreated {
		t.Fatalf("type = %q, want %q", got.Type, automation.EventMessageCreated)
	}
	if got.MessageID != 36 || got.TelegramChannelID != 123456789 || got.ManagedChannelID != 7 {
		t.Fatalf("identity fields = message %d channel %d managed %d, want 36 123456789 7",
			got.MessageID, got.TelegramChannelID, got.ManagedChannelID)
	}
	if got.NetworkKey != "official" || got.SourceClientKey != "official" {
		t.Fatalf("network %q source %q, want both official", got.NetworkKey, got.SourceClientKey)
	}
	if !got.OccurredAt.Equal(at(t, "2026-11-05T12:30:00Z")) {
		t.Fatalf("occurred_at = %s, want the message's own date", got.OccurredAt)
	}
	if !got.ReceivedAt.Equal(at(t, "2026-11-05T12:31:00Z")) {
		t.Fatalf("received_at = %s, want when it was observed", got.ReceivedAt)
	}

	// Media metadata survives; bytes do not, and cannot.
	media := got.Payload.Message.Media
	if media == nil {
		t.Fatal("media metadata was dropped")
	}
	if media.Filename != "example.apk" || media.Size != 59797754 {
		t.Fatalf("media = %+v, want the filename and size preserved", *media)
	}
	if media.MimeType != "application/vnd.android.package-archive" {
		t.Fatalf("mime type = %q", media.MimeType)
	}
	if got.Payload.MediaURLPath == "" {
		t.Fatal("no media path was recorded")
	}
	want := "/telegram/official/channels/123456789/messages/36/media"
	if got.Payload.MediaURLPath != want {
		t.Fatalf("media path = %q, want %q", got.Payload.MediaURLPath, want)
	}
	if got.GroupedID != nil {
		t.Fatalf("grouped_id = %v, want nil for a standalone message", *got.GroupedID)
	}
}

func TestAMessageWithNoMediaRecordsNoMediaPath(t *testing.T) {
	s := source(t, func(s *events.Source) { s.Message.Media = nil })
	if got := s.Event(); got.Payload.MediaURLPath != "" {
		t.Fatalf("media path = %q, want empty for a text message", got.Payload.MediaURLPath)
	}
}

func TestAnAlbumMessageKeepsItsGroup(t *testing.T) {
	s := source(t, func(s *events.Source) { s.GroupedID = 9001 })
	got := s.Event()
	if got.GroupedID == nil || *got.GroupedID != 9001 {
		t.Fatalf("grouped_id = %v, want 9001", got.GroupedID)
	}
}

// The event id is derived from the identity, so the same message replayed after
// a crash lands on the same row instead of running twice.
func TestTheEventIdentityIsDeterministic(t *testing.T) {
	first := source(t).Event()
	second := source(t).Event()
	if first.ID != second.ID {
		t.Fatalf("identical observations produced %q and %q", first.ID, second.ID)
	}
	if first.ID == "" || len(first.ID) <= len(automation.EventIDPrefix) {
		t.Fatalf("id %q is not a derived id", first.ID)
	}
	if first.ID[:len(automation.EventIDPrefix)] != automation.EventIDPrefix {
		t.Fatalf("id %q does not carry the event prefix", first.ID)
	}
}

// The message id alone must never identify an event: the same number exists in
// every channel and on every network.
func TestTheIdentityCoversTheWholeMessageIdentity(t *testing.T) {
	base := source(t).Event().ID

	otherChannel := source(t, func(s *events.Source) { s.TelegramChannelID = 987654321 }).Event().ID
	if base == otherChannel {
		t.Fatal("two channels with the same message id produced one event id")
	}

	otherNetwork := source(t, func(s *events.Source) {
		s.NetworkKey = "custom@cat.example:443"
		s.ClientKey = "the-cat"
	}).Event().ID
	if base == otherNetwork {
		t.Fatal("two independent networks with the same channel and message id collided")
	}

	otherMessage := source(t, func(s *events.Source) { s.Message.ID = 37 }).Event().ID
	if base == otherMessage {
		t.Fatal("two messages in one channel produced one event id")
	}

	otherType := events.Identity("telegram.message.deleted", "official", 123456789, 36)
	if base == otherType {
		t.Fatal("two event types produced one event id")
	}
}

// Two accounts in the same channel on the same network describe one logical
// message. The observing connection must not be part of the identity, or the
// same message would run the automations twice.
func TestTwoAccountsWatchingOneChannelAreOneEvent(t *testing.T) {
	first := source(t).Event()
	second := source(t, func(s *events.Source) { s.ClientKey = "second-account" }).Event()
	if first.ID != second.ID {
		t.Fatalf("the observing account changed the identity: %q vs %q", first.ID, second.ID)
	}
	if second.SourceClientKey != "second-account" {
		t.Fatalf("the second observer was not recorded: %q", second.SourceClientKey)
	}
}

// --- ingestion ---

type fakeStore struct {
	inserted bool
	err      error
	got      []automation.MessageEvent
	sources  map[string][]string
}

func (f *fakeStore) InsertMessageEvent(_ context.Context, event automation.MessageEvent) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.got = append(f.got, event)
	if f.sources == nil {
		f.sources = map[string][]string{}
	}
	f.sources[event.ID] = append(f.sources[event.ID], event.SourceClientKey)
	return f.inserted, nil
}

type fakeJobs struct {
	webhooks  []int64
	workflows []int64
	err       error
}

func (f *fakeJobs) EnqueueWebhookDelivery(_ context.Context, eventID string, webhookID int64) error {
	if f.err != nil {
		return f.err
	}
	f.webhooks = append(f.webhooks, webhookID)
	return nil
}

func (f *fakeJobs) EnqueueWorkflowRun(_ context.Context, eventID string, workflowID int64) error {
	if f.err != nil {
		return f.err
	}
	f.workflows = append(f.workflows, workflowID)
	return nil
}

type fakeSubs struct {
	webhookIDs  []int64
	webhookErr  error
	workflowID  int64
	hasWorkflow bool
	workflowErr error
}

func (f *fakeSubs) WebhookIDsForChannel(_ context.Context, _ int64) ([]int64, error) {
	return f.webhookIDs, f.webhookErr
}

func (f *fakeSubs) WorkflowIDForChannel(_ context.Context, _ int64) (int64, bool, error) {
	return f.workflowID, f.hasWorkflow, f.workflowErr
}

type fakeOutbound struct {
	own    bool
	err    error
	marked []automation.OutboundMessage
}

func (f *fakeOutbound) IsOwnPublication(_ context.Context, _, _ string, _, _ int64) (bool, error) {
	return f.own, f.err
}

func (f *fakeOutbound) MarkPublished(_ context.Context, m automation.OutboundMessage) error {
	f.marked = append(f.marked, m)
	return nil
}

func TestANewMessageIsStoredAndScheduled(t *testing.T) {
	store := &fakeStore{inserted: true}
	jobs := &fakeJobs{}
	subs := &fakeSubs{webhookIDs: []int64{1, 2}, workflowID: 5, hasWorkflow: true}
	ingest := events.NewIngestor(store, jobs, subs, subs, &fakeOutbound{})

	outcome, err := ingest.Ingest(context.Background(), source(t).Event())
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome != events.OutcomeInserted {
		t.Fatalf("outcome = %q, want %q", outcome, events.OutcomeInserted)
	}
	if len(store.got) != 1 {
		t.Fatalf("stored %d events, want 1", len(store.got))
	}
	if len(jobs.webhooks) != 2 || jobs.webhooks[0] != 1 || jobs.webhooks[1] != 2 {
		t.Fatalf("webhook jobs = %v, want [1 2]", jobs.webhooks)
	}
	if len(jobs.workflows) != 1 || jobs.workflows[0] != 5 {
		t.Fatalf("workflow jobs = %v, want [5]", jobs.workflows)
	}
}

// A replayed update is expected, not exceptional. It must not schedule a second
// round of work.
func TestADuplicateObservationSchedulesNothing(t *testing.T) {
	store := &fakeStore{inserted: false}
	jobs := &fakeJobs{}
	subs := &fakeSubs{webhookIDs: []int64{1}, workflowID: 5, hasWorkflow: true}
	ingest := events.NewIngestor(store, jobs, subs, subs, &fakeOutbound{})

	outcome, err := ingest.Ingest(context.Background(), source(t).Event())
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome != events.OutcomeDuplicate {
		t.Fatalf("outcome = %q, want %q", outcome, events.OutcomeDuplicate)
	}
	if len(jobs.webhooks) != 0 || len(jobs.workflows) != 0 {
		t.Fatalf("a duplicate scheduled work: webhooks %v workflows %v", jobs.webhooks, jobs.workflows)
	}
	// The observer is still recorded, so a connection able to read the media is
	// not lost just because the event already existed.
	if len(store.got) != 1 || store.got[0].SourceClientKey == "" {
		t.Fatal("the second observer was not recorded against the existing event")
	}
}

// Without this, a workflow that publishes into a monitored channel feeds
// itself without limit.
func TestOurOwnPublicationIsSuppressedBeforeStorage(t *testing.T) {
	store := &fakeStore{inserted: true}
	jobs := &fakeJobs{}
	subs := &fakeSubs{webhookIDs: []int64{1}, workflowID: 5, hasWorkflow: true}
	ingest := events.NewIngestor(store, jobs, subs, subs, &fakeOutbound{own: true})

	outcome, err := ingest.Ingest(context.Background(), source(t).Event())
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome != events.OutcomeSuppressed {
		t.Fatalf("outcome = %q, want %q", outcome, events.OutcomeSuppressed)
	}
	if len(store.got) != 0 {
		t.Fatal("our own message was stored as an event")
	}
	if len(jobs.webhooks) != 0 || len(jobs.workflows) != 0 {
		t.Fatal("our own message was scheduled for automation")
	}
}

func TestAStoreFailureIsReportedAndNothingIsScheduled(t *testing.T) {
	boom := errors.New("disk full")
	ingest := events.NewIngestor(&fakeStore{err: boom}, &fakeJobs{}, &fakeSubs{}, &fakeSubs{}, nil)

	if _, err := ingest.Ingest(context.Background(), source(t).Event()); !errors.Is(err, boom) {
		t.Fatalf("ingest error = %v, want it to carry the store failure", err)
	}
}

// The event is durable before anything else. If scheduling one automation
// fails, the others must still be scheduled and the failure must name what was
// missed, so it can be repaired rather than discovered as a silent gap.
func TestAFanOutFailureStillSchedulesTheOthers(t *testing.T) {
	store := &fakeStore{inserted: true}
	jobs := &fanOutFailingJobs{failWorkflow: true}
	subs := &fakeSubs{webhookIDs: []int64{1}, workflowID: 5, hasWorkflow: true}
	ingest := events.NewIngestor(store, jobs, subs, subs, nil)

	outcome, err := ingest.Ingest(context.Background(), source(t).Event())
	if outcome != events.OutcomeInserted {
		t.Fatalf("outcome = %q, want the event to have been stored", outcome)
	}
	if err == nil {
		t.Fatal("a failed fan-out was reported as success")
	}
	if len(jobs.webhooks) != 1 {
		t.Fatalf("webhook jobs = %v, want the webhook to have been scheduled anyway", jobs.webhooks)
	}
}

type fanOutFailingJobs struct {
	webhooks     []int64
	failWorkflow bool
}

func (f *fanOutFailingJobs) EnqueueWebhookDelivery(_ context.Context, _ string, id int64) error {
	f.webhooks = append(f.webhooks, id)
	return nil
}

func (f *fanOutFailingJobs) EnqueueWorkflowRun(_ context.Context, _ string, _ int64) error {
	if f.failWorkflow {
		return errors.New("queue unavailable")
	}
	return nil
}

// A channel with no automations is the common case and must be a clean no-op.
func TestAMessageWithNoSubscribersIsStillStored(t *testing.T) {
	store := &fakeStore{inserted: true}
	jobs := &fakeJobs{}
	ingest := events.NewIngestor(store, jobs, &fakeSubs{}, &fakeSubs{}, nil)

	outcome, err := ingest.Ingest(context.Background(), source(t).Event())
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome != events.OutcomeInserted {
		t.Fatalf("outcome = %q", outcome)
	}
	if len(store.got) != 1 {
		t.Fatal("the event was not stored")
	}
	if len(jobs.webhooks) != 0 || len(jobs.workflows) != 0 {
		t.Fatal("work was scheduled for a channel with no automations")
	}
}

func TestTheIngestorResolvesSubscriptionsWithoutStoring(t *testing.T) {
	subs := &fakeSubs{webhookIDs: []int64{3}, workflowID: 9, hasWorkflow: true}
	ingest := events.NewIngestor(nil, nil, subs, subs, nil)

	webhooks, workflow, ok, err := ingest.JobsFor(context.Background(), 7)
	if err != nil {
		t.Fatalf("JobsFor: %v", err)
	}
	if len(webhooks) != 1 || webhooks[0] != 3 || workflow != 9 || !ok {
		t.Fatalf("JobsFor = %v %d %v, want [3] 9 true", webhooks, workflow, ok)
	}
}
