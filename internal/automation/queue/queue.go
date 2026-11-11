// Package queue drains the durable background job queue.
//
// The queue is a table and a claiming protocol, not a message broker: a worker
// claims a row inside a transaction, holds a lease while it works, and either
// completes the row or records why it did not. Everything that survives a
// restart lives in SQLite, so a crash between two steps is answered by replay
// and the uniqueness constraints rather than by anything held in memory.
package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
)

// Store is the persistence the runner needs. It is satisfied by
// *channelstore.Store and named here so the runner can be tested without a
// database.
type Store interface {
	ClaimAutomationJob(ctx context.Context, req channelstore.ClaimJobRequest) (channelstore.AutomationJob, bool, error)
	CompleteAutomationJob(ctx context.Context, id int64, worker string) error
	RecordAutomationJobFailure(ctx context.Context, id int64, worker string,
		jobErr automation.JobError, retry bool, nextAttempt time.Time) (automation.JobStatus, error)
	ReleaseAutomationJob(ctx context.Context, id int64, worker string) error
	EnqueueAutomationJob(ctx context.Context, req channelstore.EnqueueJobRequest) (channelstore.AutomationJob, bool, error)
	ReopenAutomationJob(ctx context.Context, req channelstore.ReopenJobRequest) (channelstore.AutomationJob, bool, error)
}

// Config bounds the runner. Every field has a safe interpretation: a zero
// duration means the default rather than "unlimited", so a partially filled
// configuration cannot turn into a busy loop or an infinite retry.
type Config struct {
	// Workers is how many jobs run at once.
	Workers int
	// PollInterval is how long an idle worker waits before looking again.
	PollInterval time.Duration
	// RetryDelay is the base of the exponential backoff between attempts.
	RetryDelay time.Duration
	// Lease is how long a claim stands before another worker may take the job.
	Lease time.Duration
	// MaxAttempts bounds one job's tries when the enqueuer did not say.
	MaxAttempts int
	// PublishInterval is the minimum gap between two jobs for the same target,
	// so a recovered backlog cannot burst one channel.
	PublishInterval time.Duration
	// MaxBackoff caps the computed retry delay.
	MaxBackoff time.Duration
}

func (c Config) withDefaults() Config {
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 10 * time.Second
	}
	if c.Lease <= 0 {
		c.Lease = 2 * time.Minute
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	// PublishInterval is not given a default here. Zero means "no courtesy
	// gap", which is what a test and a single-worker deployment want; the
	// configuration supplies the deployment's own throttle. Per-destination
	// ordering is what actually bounds one channel's rate, and that is enforced
	// at claim time whatever this is set to.
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 15 * time.Minute
	}
	return c
}

// Option adjusts a Runner.
type Option func(*Runner)

// WithClock replaces the clock, so a test can move time without sleeping.
func WithClock(now func() time.Time) Option {
	return func(r *Runner) { r.now = now }
}

// WithLogf receives one line per worker decision worth recording.
func WithLogf(logf func(format string, args ...any)) Option {
	return func(r *Runner) { r.logf = logf }
}

// WithWorkerName sets the lease identity. It defaults to the host and pid,
// which is enough to tell two processes apart and is not a credential.
func WithWorkerName(name string) Option {
	return func(r *Runner) { r.worker = name }
}

// Runner claims and executes jobs until its context is cancelled.
type Runner struct {
	store    Store
	handlers automation.JobHandlers
	cfg      Config
	now      func() time.Time
	logf     func(format string, args ...any)
	worker   string
}

// New builds a runner. Handlers are the job kinds this process can execute; a
// claimed job with no handler is released untouched and reported, rather than
// failed, because another process may be the one that owns that kind.
func New(store Store, handlers automation.JobHandlers, cfg Config, opts ...Option) *Runner {
	r := &Runner{
		store:    store,
		handlers: handlers,
		cfg:      cfg.withDefaults(),
		now:      func() time.Time { return time.Now().UTC() },
		logf:     func(string, ...any) {},
		worker:   fmt.Sprintf("%s-%d", hostname(), os.Getpid()),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func hostname() string {
	if name, err := os.Hostname(); err == nil && name != "" {
		return name
	}
	return "worker"
}

// Run claims work until ctx is cancelled, then waits for the jobs in flight.
//
// It returns nil on a clean shutdown: a cancelled context is the normal way to
// stop, not a failure. A job interrupted by shutdown is released rather than
// failed, so it is retried after the restart instead of being recorded as an
// error the operator has to explain.
func (r *Runner) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < r.cfg.Workers; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			r.work(ctx, fmt.Sprintf("%s/%d", r.worker, slot))
		}(i)
	}
	wg.Wait()
	return nil
}

// work is one worker's loop.
func (r *Runner) work(ctx context.Context, worker string) {
	for {
		if ctx.Err() != nil {
			return
		}
		ran, err := r.step(ctx, worker)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			r.logf("queue: worker=%s claim failed: %v", worker, err)
		}
		if ran {
			// Look for the next job immediately: there is a backlog to drain.
			continue
		}
		if !sleepCtx(ctx, r.cfg.PollInterval) {
			return
		}
	}
}

// step claims and runs at most one job. It reports whether it ran one.
func (r *Runner) step(ctx context.Context, worker string) (bool, error) {
	job, ok, err := r.store.ClaimAutomationJob(ctx, channelstore.ClaimJobRequest{
		Worker: worker,
		Lease:  r.cfg.Lease,
	})
	if err != nil || !ok {
		return false, err
	}
	return true, r.Execute(ctx, worker, job)
}

// Execute runs one already-claimed job and records its outcome.
//
// It is exported because a caller can hold a claim without being this runner's
// own loop — a test, or a supervisor that assigns work deliberately — and that
// caller must get the same result policy, or "what happens when a handler
// fails" would have two answers.
func (r *Runner) Execute(ctx context.Context, worker string, job channelstore.AutomationJob) error {
	handler, known := r.handlers[job.Kind]
	if !known {
		// Not this process's kind. Put it back untouched so a process that does
		// own it can take it, and say so rather than burning an attempt.
		if err := r.store.ReleaseAutomationJob(ctx, job.ID, worker); err != nil {
			r.logf("queue: worker=%s release job %d: %v", worker, job.ID, err)
		}
		return nil
	}

	if r.cfg.PublishInterval > 0 {
		if !sleepCtx(ctx, r.cfg.PublishInterval) {
			// Shutdown arrived before the job ran: hand it back rather than
			// spend an attempt on work that never started.
			if err := r.store.ReleaseAutomationJob(ctx, job.ID, worker); err != nil {
				r.logf("queue: worker=%s release job %d: %v", worker, job.ID, err)
			}
			return ctx.Err()
		}
	}

	runErr := handler(ctx, job.Job())
	if runErr == nil {
		if err := r.store.CompleteAutomationJob(ctx, job.ID, worker); err != nil {
			r.logf("queue: worker=%s complete job %d: %v", worker, job.ID, err)
		}
		return nil
	}
	r.finishFailure(ctx, worker, job, runErr)
	return nil
}

// finishFailure records a handler's failure and decides whether there will be
// another attempt.
//
// The policy is deliberately conservative about ambiguity: an error the handler
// did not classify is treated as retryable, because an unclassified failure is
// not proof the remote side refused. It is bounded by the attempt budget, so
// being generous about one retry cannot become an infinite loop.
func (r *Runner) finishFailure(ctx context.Context, worker string, job channelstore.AutomationJob, runErr error) {
	jobErr := automation.JobError{}
	var typed *automation.JobError
	if errors.As(runErr, &typed) && typed != nil {
		jobErr = *typed
	}
	if jobErr.Code == "" {
		jobErr.Code = "automation_error"
	}
	if jobErr.Message == "" {
		jobErr.Message = runErr.Error()
	}
	if typed == nil {
		// Untyped: assume the call may have taken effect and try again.
		jobErr.Retryable = true
	}

	retry := jobErr.Retryable && job.Attempt < job.MaxAttempts
	next := r.nextAttempt(job.Attempt, jobErr.RetryAfter)
	if _, err := r.store.RecordAutomationJobFailure(ctx, job.ID, worker, jobErr, retry, next); err != nil {
		r.logf("queue: worker=%s record failure job %d: %v", worker, job.ID, err)
	}
	if retry {
		r.logf("queue: job %d attempt %d/%d failed (%s), retrying at %s",
			job.ID, job.Attempt, job.MaxAttempts, jobErr.Code, next.Format(time.RFC3339))
		return
	}
	r.logf("queue: job %d failed permanently after %d attempt(s): %s", job.ID, job.Attempt, jobErr.Code)
}

// NextAttempt reports when a job would become available again after failing on
// the given attempt. The policy is stated here and nowhere else, so the delay a
// test asserts and the delay the queue applies cannot drift apart.
func (r *Runner) NextAttempt(attempt int, providerDelay time.Duration) time.Time {
	return r.nextAttempt(attempt, providerDelay)
}

// nextAttempt computes the delay before another try. A provider-supplied delay
// wins, because a flood wait or a Retry-After is the remote side telling us
// when to come back; the exponential backoff is only the default. Both are
// capped so neither can park a job forever.
func (r *Runner) nextAttempt(attempt int, providerDelay time.Duration) time.Time {
	delay := providerDelay
	if delay <= 0 {
		delay = r.cfg.RetryDelay
		for i := 1; i < attempt && delay < r.cfg.MaxBackoff; i++ {
			delay *= 2
		}
	}
	if delay > r.cfg.MaxBackoff {
		delay = r.cfg.MaxBackoff
	}
	if delay < 0 {
		delay = r.cfg.RetryDelay
	}
	return r.now().Add(delay)
}

// Enqueuer turns stored events into durable work. It is the only way jobs
// enter the queue, so the uniqueness of (kind, target, event) is enforced in
// one place.
type Enqueuer struct {
	store       Store
	maxAttempts int
}

// NewEnqueuer builds an enqueuer. maxAttempts is copied onto each row, so
// changing the configuration later does not rewrite work already queued.
func NewEnqueuer(store Store, maxAttempts int) *Enqueuer {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	return &Enqueuer{store: store, maxAttempts: maxAttempts}
}

// EnqueueWebhookDelivery schedules one delivery of one event to one webhook.
func (e *Enqueuer) EnqueueWebhookDelivery(ctx context.Context, eventID string, webhookID int64) error {
	return e.enqueue(ctx, eventID, automation.JobWebhookDelivery, webhookID)
}

// EnqueueWorkflowRun schedules one workflow execution for one event.
func (e *Enqueuer) EnqueueWorkflowRun(ctx context.Context, eventID string, workflowID int64) error {
	return e.enqueue(ctx, eventID, automation.JobWorkflowRun, workflowID)
}

// EnqueueMessageSend schedules one accepted send for background delivery.
//
// operationID is the send operation's public id and channelID the destination
// channel it is going to. Naming the channel as the job's target is what makes
// one channel's sends claim in order and pace themselves, instead of a
// recovered backlog arriving at a channel all at once.
func (e *Enqueuer) EnqueueMessageSend(ctx context.Context, operationID string, channelID int64) error {
	return e.enqueue(ctx, operationID, automation.JobTelegramMessageSend, channelID)
}

// EnqueueMessageEdit schedules one accepted edit of an existing message.
// targetID is a stable key for (client, channel, message), so the generic
// durable queue runs edits of the same message in order while allowing edits
// of unrelated targets to proceed concurrently.
func (e *Enqueuer) EnqueueMessageEdit(ctx context.Context, operationID string, targetID int64) error {
	return e.enqueue(ctx, operationID, automation.JobTelegramMessageEdit, targetID)
}

// Reopen gives already-queued work a fresh attempt budget.
//
// This is not what Enqueue does, and the difference is deliberate. Enqueue is
// idempotent so a replayed fan-out cannot deliver twice — which also means it
// will never revive a job that has run out of attempts. A person who looks at a
// failed delivery and presses retry is making an explicit decision to try
// again, and that deserves its own method rather than weakening the idempotency
// everything else depends on.
//
// A job that already succeeded is refused: repeating it could publish the same
// message twice.
func (e *Enqueuer) Reopen(ctx context.Context, eventID string, kind automation.JobKind, targetID int64) error {
	_, _, err := e.store.ReopenAutomationJob(ctx, channelstore.ReopenJobRequest{
		EventID:     eventID,
		Kind:        kind,
		TargetID:    targetID,
		MaxAttempts: e.maxAttempts,
	})
	return err
}

func (e *Enqueuer) enqueue(ctx context.Context, eventID string, kind automation.JobKind, targetID int64) error {
	_, _, err := e.store.EnqueueAutomationJob(ctx, channelstore.EnqueueJobRequest{
		EventID:     eventID,
		Kind:        kind,
		TargetID:    targetID,
		MaxAttempts: e.maxAttempts,
	})
	return err
}

// sleepCtx waits for d, reporting false if the context ended first. It exists
// so a test can run the queue with a cancelled-after-one-step context and never
// actually sleep.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
