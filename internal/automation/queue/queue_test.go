package queue_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/events"
	"github.com/mirza/telesync/internal/automation/queue"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// clock is a hand-moved clock. Lease expiry and retry backoff are about elapsed
// time, and a test that slept for real would be slow and flaky rather than
// rigorous.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// harness is one queue over a real SQLite store, so claiming, leasing and
// uniqueness are exercised as the database actually implements them.
type harness struct {
	t             *testing.T
	store         *channelstore.Store
	clock         *clock
	path          string
	managedChanID int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clk := newClock()
	path := filepath.Join(t.TempDir(), "state", "channels.db")
	store, err := channelstore.Open(path, channelstore.Options{Now: clk.Now})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// An event names a managed channel, so one has to exist. Without this the
	// foreign key would reject every event and the tests would be measuring the
	// schema rather than the queue.
	row, err := store.Upsert(context.Background(), channelstore.Channel{
		ClientKey:        "official",
		ChannelID:        1000,
		PeerType:         "channel",
		MembershipStatus: "member",
		Title:            "Source",
	})
	if err != nil {
		t.Fatalf("seed managed channel: %v", err)
	}
	return &harness{t: t, store: store, clock: clk, path: path, managedChanID: row.ID}
}

// addEvent stores one event a job can point at. Jobs carry a foreign key, so
// there is no such thing as work with no provenance.
func (h *harness) addEvent(messageID int) string {
	h.t.Helper()
	ev := events.Source{
		ClientKey:         "official",
		NetworkKey:        "official",
		ManagedChannelID:  h.managedChanID,
		TelegramChannelID: 1000,
		Message:           rpc.Message{ID: messageID, Date: h.clock.Now(), Text: "hello"},
		ReceivedAt:        h.clock.Now(),
	}.Event()
	if _, err := h.store.InsertMessageEvent(context.Background(), ev); err != nil {
		h.t.Fatalf("insert event: %v", err)
	}
	return ev.ID
}

func (h *harness) enqueue(eventID string, kind automation.JobKind, target int64, maxAttempts int) automation.Job {
	h.t.Helper()
	job, _, err := h.store.EnqueueAutomationJob(context.Background(), channelstore.EnqueueJobRequest{
		EventID: eventID, Kind: kind, TargetID: target, MaxAttempts: maxAttempts,
	})
	if err != nil {
		h.t.Fatalf("enqueue: %v", err)
	}
	return job.Job()
}

func (h *harness) job(id int64) channelstore.AutomationJob {
	h.t.Helper()
	job, ok, err := h.store.AutomationJob(context.Background(), id)
	if err != nil || !ok {
		h.t.Fatalf("read job %d: ok=%v err=%v", id, ok, err)
	}
	return job
}

// recordingHandler records each job it saw and applies a fixed result.
type recordingHandler struct {
	mu   sync.Mutex
	seen []automation.Job
	fn   func(automation.Job) error
}

func (h *recordingHandler) handle(_ context.Context, job automation.Job) error {
	h.mu.Lock()
	h.seen = append(h.seen, job)
	h.mu.Unlock()
	if h.fn != nil {
		return h.fn(job)
	}
	return nil
}

func (h *recordingHandler) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.seen)
}

func testConfig() queue.Config {
	return queue.Config{
		Workers:      1,
		PollInterval: time.Millisecond,
		RetryDelay:   10 * time.Second,
		Lease:        2 * time.Minute,
		MaxAttempts:  3,
		MaxBackoff:   time.Hour,
	}
}

// A job is durable the moment it is enqueued, and the same job enqueued twice
// is one job: a fan-out replayed after a partial failure must not deliver
// twice.
func TestEnqueueingTheSameWorkTwiceRecordsOneJob(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)

	first := h.enqueue(eventID, automation.JobWebhookDelivery, 4, 5)
	second := h.enqueue(eventID, automation.JobWebhookDelivery, 4, 5)
	if first.ID != second.ID {
		t.Fatalf("the same work became two jobs: %d and %d", first.ID, second.ID)
	}

	// A different target is genuinely different work.
	other := h.enqueue(eventID, automation.JobWebhookDelivery, 5, 5)
	if other.ID == first.ID {
		t.Fatal("two different webhooks shared one job")
	}
	// And so is a different kind of work for the same target.
	kind := h.enqueue(eventID, automation.JobWorkflowRun, 4, 5)
	if kind.ID == first.ID {
		t.Fatal("a delivery and a workflow run shared one job")
	}
}

// Two workers must never run the same job. The claim is a database
// transaction, not a mutex in one process.
func TestTwoWorkersNeverClaimTheSameJob(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	for i := 0; i < 20; i++ {
		h.enqueue(eventID, automation.JobWebhookDelivery, int64(i+1), 5)
	}

	const workers = 8
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			worker := "w" + string(rune('a'+w))
			for {
				job, ok, err := h.store.ClaimAutomationJob(context.Background(),
					channelstore.ClaimJobRequest{Worker: worker, Lease: time.Minute})
				if err != nil || !ok {
					return
				}
				mu.Lock()
				seen[job.ID]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	if len(seen) != 20 {
		t.Fatalf("claimed %d distinct jobs, want all 20", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %d was claimed %d times", id, n)
		}
	}
}

// A worker that dies holds a lease that must expire, or the job is stranded
// forever. Reclaiming is safe because the handlers are idempotent by contract.
func TestAnExpiredLeaseIsReclaimedByAnotherWorker(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	job := h.enqueue(eventID, automation.JobWorkflowRun, 1, 5)

	claimed, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "crashed", Lease: 2 * time.Minute})
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job %d, want %d", claimed.ID, job.ID)
	}

	// While the lease is live the job is nobody else's.
	if _, ok, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "other", Lease: time.Minute}); ok {
		t.Fatal("a live lease did not hold the job")
	}

	h.clock.Advance(3 * time.Minute)
	taken, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "other", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("reclaim after expiry: ok=%v err=%v", ok, err)
	}
	if taken.ID != job.ID {
		t.Fatalf("reclaimed job %d, want %d", taken.ID, job.ID)
	}
	if taken.Attempt != 2 {
		t.Fatalf("attempt after reclaim = %d, want 2 so the budget is not reset", taken.Attempt)
	}
}

func TestASuccessfulJobIsCompleted(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	job := h.enqueue(eventID, automation.JobWebhookDelivery, 1, 5)
	handler := &recordingHandler{}

	runner := queue.New(h.store, automation.JobHandlers{automation.JobWebhookDelivery: handler.handle},
		testConfig(), queue.WithClock(h.clock.Now), queue.WithWorkerName("w1"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The runner exits on a cancelled context before it claims, so run one step
	// the way the worker loop does: claim and execute.
	claimed, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := handler.handle(context.Background(), claimed.Job()); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if err := h.store.CompleteAutomationJob(context.Background(), claimed.ID, "w1"); err != nil {
		t.Fatalf("complete: %v", err)
	}

	got := h.job(job.ID)
	if got.Status != automation.JobSucceeded {
		t.Fatalf("status = %q, want succeeded", got.Status)
	}
	if got.CompletedAt == nil {
		t.Fatal("a completed job recorded no completion time")
	}
	if got.LastErrorMessage != "" {
		t.Fatalf("a successful job kept an error: %q", got.LastErrorMessage)
	}
	_ = runner
}

// A retryable failure is not the end: the job comes back after a bounded delay.
func TestARetryableFailureSchedulesAnotherAttempt(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	job := h.enqueue(eventID, automation.JobWebhookDelivery, 1, 3)

	claimed, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if claimed.Attempt != 1 {
		t.Fatalf("attempt = %d, want the claim to spend one", claimed.Attempt)
	}

	jobErr := automation.JobError{Code: "http_502", Message: "bad gateway", Retryable: true}
	status, err := h.store.RecordAutomationJobFailure(context.Background(), claimed.ID, "w1",
		jobErr, true, h.clock.Now().Add(10*time.Second))
	if err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if status != automation.JobRetryWait {
		t.Fatalf("status = %q, want retry_wait", status)
	}

	got := h.job(job.ID)
	if got.Status != automation.JobRetryWait {
		t.Fatalf("job status = %q", got.Status)
	}
	if got.LastErrorCode != "http_502" || got.LastErrorMessage != "bad gateway" {
		t.Fatalf("stored error = %q %q, want it kept for the UI", got.LastErrorCode, got.LastErrorMessage)
	}
	if got.CompletedAt != nil {
		t.Fatal("a job waiting to be retried was marked completed")
	}

	// Not available before its time...
	if _, ok, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w2", Lease: time.Minute}); ok {
		t.Fatal("a job waiting for its backoff was claimed early")
	}
	// ...and available after it.
	h.clock.Advance(11 * time.Second)
	if _, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w2", Lease: time.Minute}); err != nil || !ok {
		t.Fatalf("claim after the backoff: ok=%v err=%v", ok, err)
	}
}

// A permanently failed job is recorded and left alone. It must not be retried,
// and it must not block the work behind it.
func TestAPermanentFailureIsTerminal(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	job := h.enqueue(eventID, automation.JobWebhookDelivery, 1, 3)

	claimed, _, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute})
	jobErr := automation.JobError{Code: "http_404", Message: "gone", Retryable: false}
	status, err := h.store.RecordAutomationJobFailure(context.Background(), claimed.ID, "w1",
		jobErr, false, h.clock.Now())
	if err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if status != automation.JobFailed {
		t.Fatalf("status = %q, want failed", status)
	}

	h.clock.Advance(time.Hour)
	if _, ok, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w2", Lease: time.Minute}); ok {
		t.Fatal("a terminally failed job was handed out again")
	}
	got := h.job(job.ID)
	if got.Status != automation.JobFailed || got.CompletedAt == nil {
		t.Fatalf("job = %+v, want a terminal failure with a completion time", got)
	}
}

// The attempt budget is what makes "never retry indefinitely" true.
func TestTheAttemptBudgetStopsARetryableFailure(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	job := h.enqueue(eventID, automation.JobWebhookDelivery, 1, 3)

	for attempt := 1; attempt <= 3; attempt++ {
		claimed, ok, err := h.store.ClaimAutomationJob(context.Background(),
			channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute})
		if err != nil || !ok {
			t.Fatalf("claim attempt %d: ok=%v err=%v", attempt, ok, err)
		}
		if claimed.Attempt != attempt {
			t.Fatalf("claim %d spent attempt %d", attempt, claimed.Attempt)
		}
		retry := attempt < 3
		if _, err := h.store.RecordAutomationJobFailure(context.Background(), claimed.ID, "w1",
			automation.JobError{Code: "boom", Message: "still failing", Retryable: true},
			retry, h.clock.Now().Add(time.Second)); err != nil {
			t.Fatalf("record failure %d: %v", attempt, err)
		}
		if !retry {
			break
		}
		h.clock.Advance(2 * time.Second)
	}

	got := h.job(job.ID)
	if got.Status != automation.JobFailed {
		t.Fatalf("status = %q, want the budget to end in a terminal failure", got.Status)
	}
	if got.Attempt != 3 {
		t.Fatalf("attempts = %d, want 3 and no more", got.Attempt)
	}
	h.clock.Advance(time.Hour)
	if _, ok, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w2", Lease: time.Minute}); ok {
		t.Fatal("the job was handed out again after its budget ran out")
	}
}

// One workflow has one source channel, and its messages must reach the
// destinations in the order they arrived. A job is only claimable when nothing
// earlier for the same target is still outstanding.
func TestAWorkflowsMessagesAreProcessedInOrder(t *testing.T) {
	h := newHarness(t)
	first := h.enqueue(h.addEvent(1), automation.JobWorkflowRun, 1, 5)
	second := h.enqueue(h.addEvent(2), automation.JobWorkflowRun, 1, 5)
	third := h.enqueue(h.addEvent(3), automation.JobWorkflowRun, 1, 5)

	claimed, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if claimed.ID != first.ID {
		t.Fatalf("claimed %d, want the oldest (%d)", claimed.ID, first.ID)
	}

	// The second must not jump the queue while the first is unfinished.
	if jumped, ok, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w2", Lease: time.Minute}); ok {
		t.Fatalf("job %d ran while %d was still outstanding", jumped.ID, first.ID)
	}

	if err := h.store.CompleteAutomationJob(context.Background(), first.ID, "w1"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	next, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w2", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("claim after completion: ok=%v err=%v", ok, err)
	}
	if next.ID != second.ID {
		t.Fatalf("claimed %d, want %d next", next.ID, second.ID)
	}
	_ = third
}

// Different channels are independent. One channel being stuck must not stop
// another from publishing.
func TestIndependentTargetsRunInParallel(t *testing.T) {
	h := newHarness(t)
	stuck := h.enqueue(h.addEvent(1), automation.JobWorkflowRun, 1, 5)
	free := h.enqueue(h.addEvent(2), automation.JobWorkflowRun, 2, 5)

	if _, ok, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute}); !ok {
		t.Fatal("nothing was claimable")
	}
	got, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w2", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("claim for the second channel: ok=%v err=%v", ok, err)
	}
	if got.ID != free.ID {
		t.Fatalf("claimed %d, want the other channel's job %d", got.ID, free.ID)
	}
	_ = stuck
}

func TestMessageEditsSerializeOneTargetButLeaveOtherTargetsRunnable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	user, err := h.store.CreateUser(ctx, "Editor", "editor", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", true)
	if err != nil {
		t.Fatalf("create operation owner: %v", err)
	}
	enqueuer := queue.NewEnqueuer(h.store, 5)
	edit := func(targetMessageID int64, fingerprint string) channelstore.MessageEditOperation {
		h.t.Helper()
		op, _, err := h.store.BeginMessageEditOperation(ctx, channelstore.MessageEditOperation{
			UserID: user.ID, ClientKey: "cat", DestinationChannelID: "98", TargetMessageID: targetMessageID,
			RequestPayload: `{"text":"replacement"}`, RequestFingerprint: fingerprint,
		})
		if err != nil {
			h.t.Fatalf("begin edit operation: %v", err)
		}
		return op
	}

	first := edit(11, "first")
	second := edit(11, "second")
	other := edit(12, "other")
	if err := enqueuer.EnqueueMessageEdit(ctx, first.PublicID, 9001); err != nil {
		t.Fatalf("enqueue first edit: %v", err)
	}
	if err := enqueuer.EnqueueMessageEdit(ctx, second.PublicID, 9001); err != nil {
		t.Fatalf("enqueue second edit: %v", err)
	}
	if err := enqueuer.EnqueueMessageEdit(ctx, other.PublicID, 9002); err != nil {
		t.Fatalf("enqueue unrelated edit: %v", err)
	}

	claimed, ok, err := h.store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute})
	if err != nil || !ok || claimed.EventID != first.PublicID {
		t.Fatalf("first claim = %+v, ok=%v err=%v; want first target edit", claimed, ok, err)
	}
	next, ok, err := h.store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{Worker: "w2", Lease: time.Minute})
	if err != nil || !ok || next.EventID != other.PublicID {
		t.Fatalf("while first target is held, claim = %+v ok=%v err=%v; want unrelated target", next, ok, err)
	}
	if err := h.store.CompleteAutomationJob(ctx, claimed.ID, "w1"); err != nil {
		t.Fatalf("complete first edit: %v", err)
	}
	if err := h.store.CompleteAutomationJob(ctx, next.ID, "w2"); err != nil {
		t.Fatalf("complete unrelated edit: %v", err)
	}
	last, ok, err := h.store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{Worker: "w3", Lease: time.Minute})
	if err != nil || !ok || last.EventID != second.PublicID {
		t.Fatalf("last claim = %+v ok=%v err=%v; want next edit of same target", last, ok, err)
	}
}

// A job whose kind this process does not handle must be put back untouched, not
// failed: another process may own it, and burning an attempt would be a lie
// about what happened.
func TestAnUnhandledJobKindIsReleasedUntouched(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(h.addEvent(1), automation.JobWorkflowRun, 1, 5)

	claimed, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "webhooks-only", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := h.store.ReleaseAutomationJob(context.Background(), claimed.ID, "webhooks-only"); err != nil {
		t.Fatalf("release: %v", err)
	}

	got := h.job(job.ID)
	if got.Status != automation.JobPending {
		t.Fatalf("status = %q, want the job left pending for a process that owns it", got.Status)
	}
	if got.Attempt != 1 {
		t.Fatalf("attempt = %d, want the claim to have been spent but the job undamaged", got.Attempt)
	}
}

// Cancelling is for work an operator has decided should not run.
func TestACancelledJobIsNeverHandedOut(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(h.addEvent(1), automation.JobWebhookDelivery, 1, 5)

	status, err := h.store.CancelAutomationJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if status != automation.JobCancelled {
		t.Fatalf("status = %q, want cancelled", status)
	}
	if _, ok, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute}); ok {
		t.Fatal("a cancelled job was handed out")
	}
}

// The backoff grows and is capped, and a provider's own delay is preferred
// because a flood wait is the remote side telling us when to return.
func TestRetryDelayGrowsAndIsCapped(t *testing.T) {
	h := newHarness(t)
	cfg := testConfig()
	cfg.RetryDelay = 10 * time.Second
	cfg.MaxBackoff = 30 * time.Second
	runner := queue.New(h.store, nil, cfg, queue.WithClock(h.clock.Now))

	for _, tc := range []struct {
		attempt   int
		provider  time.Duration
		wantDelay time.Duration
	}{
		{attempt: 1, wantDelay: 10 * time.Second},
		{attempt: 2, wantDelay: 20 * time.Second},
		{attempt: 3, wantDelay: 30 * time.Second},                             // capped
		{attempt: 9, wantDelay: 30 * time.Second},                             // still capped
		{attempt: 1, provider: 90 * time.Second, wantDelay: 30 * time.Second}, // provider clamped
		{attempt: 1, provider: 5 * time.Second, wantDelay: 5 * time.Second},   // provider wins
	} {
		start := h.clock.Now()
		got := runner.NextAttempt(tc.attempt, tc.provider)
		if delay := got.Sub(start); delay != tc.wantDelay {
			t.Fatalf("attempt %d provider %s: delay = %s, want %s",
				tc.attempt, tc.provider, delay, tc.wantDelay)
		}
	}
}

// An error the handler did not classify is treated as retryable: an
// unclassified failure is not proof the remote side refused the request. It is
// still bounded by the attempt budget.
func TestAnUntypedErrorIsRetriedNotSwallowed(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	job := h.enqueue(eventID, automation.JobWebhookDelivery, 1, 2)

	handler := &recordingHandler{fn: func(automation.Job) error { return errors.New("something broke") }}
	cfg := testConfig()
	cfg.MaxAttempts = 2
	runner := queue.New(h.store,
		automation.JobHandlers{automation.JobWebhookDelivery: handler.handle},
		cfg, queue.WithClock(h.clock.Now), queue.WithWorkerName("w1"))

	claimed, ok, err := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	runner.Execute(context.Background(), "w1", claimed)

	got := h.job(job.ID)
	if got.Status != automation.JobRetryWait {
		t.Fatalf("status = %q, want an untyped error to be retried", got.Status)
	}
	if got.LastErrorCode == "" {
		t.Fatal("the failure recorded no error code")
	}
}

// A handler that returns nothing leaves no error behind.
func TestASuccessRecordsNoError(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	job := h.enqueue(eventID, automation.JobWebhookDelivery, 1, 5)

	handler := &recordingHandler{}
	runner := queue.New(h.store,
		automation.JobHandlers{automation.JobWebhookDelivery: handler.handle},
		testConfig(), queue.WithClock(h.clock.Now), queue.WithWorkerName("w1"))

	claimed, _, _ := h.store.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "w1", Lease: time.Minute})
	runner.Execute(context.Background(), "w1", claimed)

	got := h.job(job.ID)
	if got.Status != automation.JobSucceeded {
		t.Fatalf("status = %q, want succeeded", got.Status)
	}
	if got.LastErrorCode != "" || got.LastErrorMessage != "" {
		t.Fatalf("a success kept an error: %q %q", got.LastErrorCode, got.LastErrorMessage)
	}
}

// The enqueuer copies the budget onto the row, so changing the configuration
// later does not rewrite work already queued.
func TestTheEnqueuerCarriesItsOwnAttemptBudget(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	enqueuer := queue.NewEnqueuer(h.store, 7)

	if err := enqueuer.EnqueueWebhookDelivery(context.Background(), eventID, 3); err != nil {
		t.Fatalf("EnqueueWebhookDelivery: %v", err)
	}
	if err := enqueuer.EnqueueWorkflowRun(context.Background(), eventID, 9); err != nil {
		t.Fatalf("EnqueueWorkflowRun: %v", err)
	}

	jobs, err := h.store.AutomationJobs(context.Background(), channelstore.AutomationJobQuery{EventID: eventID})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("queued %d jobs, want 2", len(jobs))
	}
	for _, job := range jobs {
		if job.MaxAttempts != 7 {
			t.Fatalf("job %d has max_attempts %d, want 7", job.ID, job.MaxAttempts)
		}
	}
}

// A crash leaves no in-memory truth to consult. Everything the queue knows is
// in the database, so a fresh process picks up exactly where the old one lost
// the thread.
func TestAQueueReloadsItsWorkFromTheDatabase(t *testing.T) {
	h := newHarness(t)
	eventID := h.addEvent(1)
	h.enqueue(eventID, automation.JobWebhookDelivery, 1, 5)

	// A second store over the same file stands in for the restarted process.
	reopened, err := channelstore.Open(h.path, channelstore.Options{Now: h.clock.Now})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	claimed, ok, err := reopened.ClaimAutomationJob(context.Background(),
		channelstore.ClaimJobRequest{Worker: "after-restart", Lease: time.Minute})
	if err != nil || !ok {
		t.Fatalf("claim after restart: ok=%v err=%v", ok, err)
	}
	if claimed.TargetID != 1 {
		t.Fatalf("claimed target %d, want the queued work", claimed.TargetID)
	}
}
