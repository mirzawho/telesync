package channelstore_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
)

// The queue's promises are all about what happens when things go wrong at the
// same time: two workers polling together, a worker that dies holding a lease,
// a job that can never succeed. Each test below is one of those promises.

// testClock is the store's clock, moved forward by hand so lease expiry is a
// fact rather than a sleep.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// jobStore opens a store with one managed channel and one event to attach jobs
// to, driven by a clock the test moves forward by hand.
func jobStore(t *testing.T) (*channelstore.Store, string, *testClock) {
	t.Helper()
	clock := newTestClock()
	store := open(t, func(o *channelstore.Options) { o.Now = clock.Now })
	return store, insertJobEvent(t, store), clock
}

// insertJobEvent stores one event that jobs can reference.
func insertJobEvent(t *testing.T, store *channelstore.Store) string {
	t.Helper()
	ctx := context.Background()
	ch, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey:        "official",
		ChannelID:        1000,
		PeerType:         channelstore.PeerChannel,
		Title:            "Source",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("upsert channel: %v", err)
	}
	ev := eventFor(ch.ID)
	ev.ID = "evt_job_source"
	if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return ev.ID
}

// insertJobEventN stores one more event with its own identity, so a test can
// queue several distinct jobs for one target. A job is unique per
// (kind, target, event), so two jobs for one target need two events: without
// this the second enqueue is the same job, which is exactly the idempotency
// the table enforces.
func insertJobEventN(t *testing.T, store *channelstore.Store, n int) string {
	t.Helper()
	ctx := context.Background()
	ch, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey:        "official",
		ChannelID:        1000,
		PeerType:         channelstore.PeerChannel,
		Title:            "Source",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("upsert channel: %v", err)
	}
	ev := eventFor(ch.ID)
	ev.ID = fmt.Sprintf("evt_job_source_%d", n)
	ev.MessageID = int64(100 + n)
	ev.Payload.Message.ID = 100 + n
	if _, err := store.InsertMessageEvent(ctx, ev); err != nil {
		t.Fatalf("insert event %d: %v", n, err)
	}
	return ev.ID
}

func enqueueJob(t *testing.T, store *channelstore.Store, eventID string, target int64) channelstore.AutomationJob {
	t.Helper()
	job, created, err := store.EnqueueAutomationJob(context.Background(), channelstore.EnqueueJobRequest{
		EventID:     eventID,
		Kind:        automation.JobWebhookDelivery,
		TargetID:    target,
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !created {
		t.Fatal("a first enqueue must be reported as created")
	}
	return job
}

func claim(t *testing.T, store *channelstore.Store, worker string, clock *testClock) (channelstore.AutomationJob, bool) {
	t.Helper()
	job, ok, err := store.ClaimAutomationJob(context.Background(), channelstore.ClaimJobRequest{
		Worker: worker,
		Lease:  time.Minute,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return job, ok
}

func TestAQueuedJobIsClaimedExactlyOnce(t *testing.T) {
	store, eventID, clock := jobStore(t)
	enqueueJob(t, store, eventID, 1)

	first, ok := claim(t, store, "worker-a", clock)
	if !ok {
		t.Fatal("a queued job must be claimable")
	}
	if first.Status != automation.JobProcessing {
		t.Fatalf("status = %q, want processing", first.Status)
	}
	if first.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", first.Attempt)
	}

	if _, ok := claim(t, store, "worker-b", clock); ok {
		t.Fatal("a claimed job must not be claimable by a second worker")
	}
}

func TestConcurrentWorkersClaimDisjointJobs(t *testing.T) {
	store, eventID, clock := jobStore(t)
	for i := int64(1); i <= 8; i++ {
		enqueueJob(t, store, eventID, i)
	}

	var (
		mu     sync.Mutex
		taken  = map[int64]string{}
		wg     sync.WaitGroup
		worker = func(name string) {
			defer wg.Done()
			for {
				job, ok := claim(t, store, name, clock)
				if !ok {
					return
				}
				mu.Lock()
				if other, dup := taken[job.ID]; dup {
					mu.Unlock()
					t.Errorf("job %d was claimed by both %s and %s", job.ID, other, name)
					return
				}
				taken[job.ID] = name
				mu.Unlock()
			}
		}
	)
	for _, name := range []string{"worker-a", "worker-b", "worker-c"} {
		wg.Add(1)
		go worker(name)
	}
	wg.Wait()

	if len(taken) != 8 {
		t.Fatalf("claimed %d jobs, want 8", len(taken))
	}
}

func TestALeaseThatExpiresIsReclaimable(t *testing.T) {
	store, eventID, clock := jobStore(t)
	enqueueJob(t, store, eventID, 1)

	first, ok := claim(t, store, "worker-a", clock)
	if !ok {
		t.Fatal("a queued job must be claimable")
	}
	if _, ok := claim(t, store, "worker-b", clock); ok {
		t.Fatal("a leased job must not be claimable before its lease expires")
	}

	clock.Advance(2 * time.Minute)
	second, ok := claim(t, store, "worker-b", clock)
	if !ok {
		t.Fatal("a job whose lease expired must be claimable again")
	}
	if second.ID != first.ID {
		t.Fatalf("claimed job %d, want the expired one %d", second.ID, first.ID)
	}
	if second.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2 after the reclaim", second.Attempt)
	}
	if second.LockedBy != "worker-b" {
		t.Fatalf("locked_by = %q, want worker-b", second.LockedBy)
	}
}

func TestAJobAnExpiredLeaseGaveAwayCannotBeCompletedByItsOldHolder(t *testing.T) {
	store, eventID, clock := jobStore(t)
	enqueueJob(t, store, eventID, 1)

	first, _ := claim(t, store, "worker-a", clock)
	clock.Advance(2 * time.Minute)
	if _, ok := claim(t, store, "worker-b", clock); !ok {
		t.Fatal("the expired lease must be reclaimable")
	}

	err := store.CompleteAutomationJob(context.Background(), first.ID, "worker-a")
	if !errors.Is(err, channelstore.ErrAutomationJobNotClaimed) {
		t.Fatalf("err = %v, want ErrAutomationJobNotClaimed", err)
	}
}

func TestAFailedAttemptReturnsTheJobToTheQueueAtTheRequestedTime(t *testing.T) {
	store, eventID, clock := jobStore(t)
	enqueueJob(t, store, eventID, 1)
	job, _ := claim(t, store, "worker-a", clock)

	status, err := store.RecordAutomationJobFailure(context.Background(), job.ID, "worker-a",
		automation.JobError{Code: "http_502", Message: "bad gateway", Retryable: true},
		true, clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if status != automation.JobRetryWait {
		t.Fatalf("status = %q, want retry_wait", status)
	}

	if _, ok := claim(t, store, "worker-b", clock); ok {
		t.Fatal("a job waiting for its backoff must not be claimable yet")
	}
	clock.Advance(2 * time.Hour)
	again, ok := claim(t, store, "worker-b", clock)
	if !ok {
		t.Fatal("a job must be claimable once its backoff has elapsed")
	}
	if again.ID != job.ID || again.Attempt != 2 {
		t.Fatalf("reclaimed job = %+v", again)
	}
	stored, _, err := store.AutomationJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if stored.LastErrorCode != "http_502" || stored.LastErrorMessage != "bad gateway" {
		t.Fatalf("stored error = %q/%q", stored.LastErrorCode, stored.LastErrorMessage)
	}
}

func TestAJobThatStopsRetryingIsRecordedAsFailed(t *testing.T) {
	store, eventID, clock := jobStore(t)
	job := enqueueJob(t, store, eventID, 1)
	claimed, _ := claim(t, store, "worker-a", clock)

	status, err := store.RecordAutomationJobFailure(context.Background(), claimed.ID, "worker-a",
		automation.JobError{Code: "http_400", Message: "bad request"}, false, time.Time{})
	if err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if status != automation.JobFailed {
		t.Fatalf("status = %q, want failed", status)
	}
	stored, ok, err := store.AutomationJob(context.Background(), job.ID)
	if err != nil || !ok {
		t.Fatalf("read job: ok=%v err=%v", ok, err)
	}
	if stored.CompletedAt == nil {
		t.Fatal("a terminally failed job must record when it stopped")
	}

	clock.Advance(24 * time.Hour)
	if _, ok := claim(t, store, "worker-b", clock); ok {
		t.Fatal("a terminally failed job must never be claimed again")
	}
}

func TestATerminallyFailedJobDoesNotBlockTheOnesBehindIt(t *testing.T) {
	store, _, clock := jobStore(t)
	first := enqueueJob(t, store, insertJobEventN(t, store, 1), 1)
	second := enqueueJob(t, store, insertJobEventN(t, store, 2), 1)

	claimed, _ := claim(t, store, "worker-a", clock)
	if claimed.ID != first.ID {
		t.Fatalf("claimed job %d, want the first one %d", claimed.ID, first.ID)
	}
	if _, err := store.RecordAutomationJobFailure(context.Background(), claimed.ID, "worker-a",
		automation.JobError{Code: "gone", Message: "webhook deleted"}, false, time.Time{}); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	next, ok := claim(t, store, "worker-a", clock)
	if !ok {
		t.Fatal("a terminally failed job must not strand the work behind it")
	}
	if next.ID != second.ID {
		t.Fatalf("claimed job %d, want %d", next.ID, second.ID)
	}
}

func TestAnEarlierOutstandingJobBlocksTheOnesBehindIt(t *testing.T) {
	store, _, clock := jobStore(t)
	first := enqueueJob(t, store, insertJobEventN(t, store, 1), 1)
	second := enqueueJob(t, store, insertJobEventN(t, store, 2), 1)

	claimed, _ := claim(t, store, "worker-a", clock)
	if claimed.ID != first.ID {
		t.Fatalf("claimed job %d, want %d", claimed.ID, first.ID)
	}
	if _, ok := claim(t, store, "worker-b", clock); ok {
		t.Fatal("a later job for one target must wait for the earlier one")
	}

	// The earlier job is scheduled for a retry that is not due yet; the later
	// one is still held back, because a job waiting for its retry keeps its
	// place in line rather than being overtaken.
	if _, err := store.RecordAutomationJobFailure(context.Background(), claimed.ID, "worker-a",
		automation.JobError{Code: "http_500", Message: "boom"}, true, clock.Now().Add(time.Minute)); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if _, ok := claim(t, store, "worker-b", clock); ok {
		t.Fatal("a job still waiting to be retried keeps its place in line")
	}

	// The retry itself is what runs next, not the job behind it.
	clock.Advance(time.Minute)
	again, ok := claim(t, store, "worker-b", clock)
	if !ok {
		t.Fatal("the earlier job must be claimable again once its retry is due")
	}
	if again.ID != first.ID {
		t.Fatalf("claimed job %d, want the earlier one %d", again.ID, first.ID)
	}
	if err := store.CompleteAutomationJob(context.Background(), again.ID, "worker-b"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	next, ok := claim(t, store, "worker-b", clock)
	if !ok {
		t.Fatal("the next job must run once the earlier one is done")
	}
	if next.ID != second.ID {
		t.Fatalf("claimed job %d, want %d", next.ID, second.ID)
	}
}

func TestJobsForDifferentTargetsDoNotBlockEachOther(t *testing.T) {
	store, eventID, clock := jobStore(t)
	for target := int64(1); target <= 3; target++ {
		enqueueJob(t, store, eventID, target)
	}
	// Hold the first target's job and never finish it.
	if _, ok := claim(t, store, "worker-a", clock); !ok {
		t.Fatal("the first job must be claimable")
	}

	seen := map[int64]bool{}
	for i := 0; i < 2; i++ {
		job, ok := claim(t, store, "worker-b", clock)
		if !ok {
			t.Fatal("jobs for other targets must not be blocked")
		}
		if seen[job.TargetID] {
			t.Fatalf("target %d claimed twice", job.TargetID)
		}
		seen[job.TargetID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("claimed targets %v, want two", seen)
	}
}

func TestEnqueueingTheSameWorkTwiceReusesTheRow(t *testing.T) {
	store, eventID, _ := jobStore(t)
	first := enqueueJob(t, store, eventID, 1)

	again, created, err := store.EnqueueAutomationJob(context.Background(), channelstore.EnqueueJobRequest{
		EventID:     eventID,
		Kind:        automation.JobWebhookDelivery,
		TargetID:    1,
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if created {
		t.Fatal("a repeated fan-out must not create a second job")
	}
	if again.ID != first.ID {
		t.Fatalf("job id = %d, want %d", again.ID, first.ID)
	}
	total, err := store.AutomationJobs(context.Background(), channelstore.AutomationJobQuery{EventID: eventID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(total) != 1 {
		t.Fatalf("stored %d jobs, want 1", len(total))
	}
}

func TestCancellingAQueuedJobStopsIt(t *testing.T) {
	store, eventID, clock := jobStore(t)
	job := enqueueJob(t, store, eventID, 1)

	status, err := store.CancelAutomationJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if status != automation.JobCancelled {
		t.Fatalf("status = %q, want cancelled", status)
	}
	if _, ok := claim(t, store, "worker-a", clock); ok {
		t.Fatal("a cancelled job must never be claimed")
	}
}

func TestCancellingAJobAnotherWorkerHoldsStopsItToo(t *testing.T) {
	store, eventID, clock := jobStore(t)
	enqueueJob(t, store, eventID, 1)
	claimed, _ := claim(t, store, "worker-a", clock)

	status, err := store.CancelAutomationJob(context.Background(), claimed.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if status != automation.JobCancelled {
		t.Fatalf("status = %q, want cancelled", status)
	}
	clock.Advance(10 * time.Minute)
	if _, ok := claim(t, store, "worker-b", clock); ok {
		t.Fatal("a cancelled job must not come back when its lease expires")
	}
}

func TestCancellationDoesNotRewindWorkThatAlreadyFinished(t *testing.T) {
	store, eventID, clock := jobStore(t)
	job := enqueueJob(t, store, eventID, 1)
	claimed, ok := claim(t, store, "worker-a", clock)
	if !ok {
		t.Fatal("a queued job must be claimable")
	}
	if err := store.CompleteAutomationJob(context.Background(), claimed.ID, "worker-a"); err != nil {
		t.Fatalf("complete: %v", err)
	}

	status, err := store.CancelAutomationJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if status != automation.JobSucceeded {
		t.Fatalf("status = %q, want the finished state to be preserved", status)
	}
}

func TestGracefulReleaseMakesTheJobImmediatelyClaimable(t *testing.T) {
	store, eventID, clock := jobStore(t)
	enqueueJob(t, store, eventID, 1)
	claimed, _ := claim(t, store, "worker-a", clock)

	if err := store.ReleaseAutomationJob(context.Background(), claimed.ID, "worker-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	next, ok := claim(t, store, "worker-b", clock)
	if !ok {
		t.Fatal("a released job must be claimable without waiting out its lease")
	}
	if next.ID != claimed.ID {
		t.Fatalf("claimed job %d, want %d", next.ID, claimed.ID)
	}
}

func TestAnEnqueuedJobNeedsAnEvent(t *testing.T) {
	store, _, _ := jobStore(t)
	_, _, err := store.EnqueueAutomationJob(context.Background(), channelstore.EnqueueJobRequest{
		EventID:     "evt_missing",
		Kind:        automation.JobWorkflowRun,
		TargetID:    1,
		MaxAttempts: 3,
	})
	if err == nil {
		t.Fatal("a job must not be queued against an event that does not exist")
	}
}

// A person who looks at a failed delivery and presses retry is making an
// explicit decision to try again. That is not the same as a replayed fan-out,
// which the idempotent enqueue must refuse to turn into a second delivery.
func TestReopeningAFailedJobGivesItAFreshBudget(t *testing.T) {
	store, _, clock := jobStore(t)
	job := enqueueJob(t, store, insertJobEventN(t, store, 1), 1)

	claimed, _ := claim(t, store, "worker-a", clock)
	if _, err := store.RecordAutomationJobFailure(context.Background(), claimed.ID, "worker-a",
		automation.JobError{Code: "http_410", Message: "gone"}, false, clock.Now()); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if got := mustJob(t, store, job.ID); got.Status != automation.JobFailed {
		t.Fatalf("status = %q, want the job to have stopped", got.Status)
	}

	reopened, created, err := store.ReopenAutomationJob(context.Background(), channelstore.ReopenJobRequest{
		EventID: job.EventID, Kind: job.Kind, TargetID: job.TargetID,
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !created {
		t.Fatal("a reopened job must report its new state")
	}
	if reopened.ID != job.ID {
		t.Fatalf("reopened job %d, want the same row %d", reopened.ID, job.ID)
	}
	if reopened.Status != automation.JobPending {
		t.Fatalf("status = %q, want it back in the queue", reopened.Status)
	}
	if reopened.Attempt != 0 {
		t.Fatalf("attempts = %d, want the budget reset", reopened.Attempt)
	}
	if reopened.LastErrorCode != "" || reopened.LastErrorMessage != "" {
		t.Fatalf("a reopened job kept %q %q; the new attempt starts clean",
			reopened.LastErrorCode, reopened.LastErrorMessage)
	}
	if reopened.CompletedAt != nil {
		t.Fatal("a reopened job still reads as completed")
	}

	// And it is genuinely runnable again.
	again, ok := claim(t, store, "worker-b", clock)
	if !ok || again.ID != job.ID {
		t.Fatalf("claim after reopen = %v %v, want the reopened job", again.ID, ok)
	}
}

// Retrying something that already succeeded could publish the same message
// twice. Idempotency protects that, and an operator's retry button must not be
// the hole in it.
func TestReopeningASucceededJobIsRefused(t *testing.T) {
	store, _, clock := jobStore(t)
	job := enqueueJob(t, store, insertJobEventN(t, store, 1), 1)

	claimed, _ := claim(t, store, "worker-a", clock)
	if err := store.CompleteAutomationJob(context.Background(), claimed.ID, "worker-a"); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if _, _, err := store.ReopenAutomationJob(context.Background(), channelstore.ReopenJobRequest{
		EventID: job.EventID, Kind: job.Kind, TargetID: job.TargetID,
	}); !errors.Is(err, automation.ErrConflict) {
		t.Fatalf("reopen a succeeded job = %v, want a conflict", err)
	}
}

// Two workers on one delivery is how a duplicate happens.
func TestReopeningAJobThatIsRunningIsRefused(t *testing.T) {
	store, _, clock := jobStore(t)
	job := enqueueJob(t, store, insertJobEventN(t, store, 1), 1)

	if _, ok := claim(t, store, "worker-a", clock); !ok {
		t.Fatal("nothing was claimable")
	}
	if _, _, err := store.ReopenAutomationJob(context.Background(), channelstore.ReopenJobRequest{
		EventID: job.EventID, Kind: job.Kind, TargetID: job.TargetID,
	}); !errors.Is(err, automation.ErrConflict) {
		t.Fatalf("reopen a running job = %v, want a conflict", err)
	}
}

func TestReopeningWorkThatWasNeverQueuedIsReportedMissing(t *testing.T) {
	store, _, _ := jobStore(t)
	if _, _, err := store.ReopenAutomationJob(context.Background(), channelstore.ReopenJobRequest{
		EventID: "evt_job_source_missing", Kind: automation.JobWebhookDelivery, TargetID: 1,
	}); !errors.Is(err, automation.ErrNotFound) {
		t.Fatalf("reopen unknown work = %v, want not found", err)
	}
}

// mustJob reads one job and fails the test if it is missing.
func mustJob(t *testing.T, store *channelstore.Store, id int64) channelstore.AutomationJob {
	t.Helper()
	job, found, err := store.AutomationJob(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("read job %d: found=%v err=%v", id, found, err)
	}
	return job
}
