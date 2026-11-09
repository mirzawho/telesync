package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
)

// Durable background work.
//
// The queue is a table, not a slice in a goroutine: a restart must not lose a
// job, two workers must never take the same one, and a job whose worker died
// must come back. Every one of those is a statement about this file.
//
// Invariant: package automation holds only shared contracts and must never
// import this package.

// ErrInvalidAutomationJob means the job record itself is not usable: no event,
// an unknown kind, or a target id Telegram's tables never issue.
var ErrInvalidAutomationJob = fmt.Errorf("channelstore: invalid automation job: %w", automation.ErrInvalid)

// ErrAutomationJobNotFound means no job has that id.
var ErrAutomationJobNotFound = fmt.Errorf("channelstore: automation job not found: %w", automation.ErrNotFound)

// ErrAutomationJobNotClaimed means the job exists but is not currently leased
// by the worker acting on it: it expired, another worker owns it, or it is no
// longer running at all. Acting on such a job would record an outcome for work
// this worker may never have done.
var ErrAutomationJobNotClaimed = fmt.Errorf("channelstore: automation job is not held by this worker: %w", automation.ErrConflict)

// AutomationJob is one unit of background work and everything the queue needs
// to decide what to do with it next.
type AutomationJob struct {
	ID       int64
	EventID  string
	Kind     automation.JobKind
	TargetID int64
	Status   automation.JobStatus
	// Attempt is how many times the job has been claimed. It is incremented on
	// claim, so a worker holding a lease has already spent an attempt.
	Attempt     int
	MaxAttempts int
	AvailableAt time.Time
	LockedBy    string
	// LockedUntil is nil when nothing holds the job.
	LockedUntil      *time.Time
	LastErrorCode    string
	LastErrorMessage string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

// Job is the shape handlers receive. Handlers see the attempt in flight and
// nothing about locks or scheduling.
func (j AutomationJob) Job() automation.Job {
	return automation.Job{
		ID:          j.ID,
		EventID:     j.EventID,
		Kind:        j.Kind,
		TargetID:    j.TargetID,
		Attempt:     j.Attempt,
		MaxAttempts: j.MaxAttempts,
		AvailableAt: j.AvailableAt,
		CreatedAt:   j.CreatedAt,
	}
}

const automationJobColumns = `id, event_id, job_type, target_id, status,
	attempt_count, max_attempts, available_at, locked_by, locked_until,
	last_error_code, last_error_message, created_at, updated_at, completed_at`

func scanAutomationJob(row scanner) (AutomationJob, error) {
	var (
		out         AutomationJob
		kind        string
		status      string
		availableAt string
		lockedUntil sql.NullString
		createdAt   string
		updatedAt   string
		completedAt sql.NullString
	)
	if err := row.Scan(&out.ID, &out.EventID, &kind, &out.TargetID, &status,
		&out.Attempt, &out.MaxAttempts, &availableAt, &out.LockedBy, &lockedUntil,
		&out.LastErrorCode, &out.LastErrorMessage, &createdAt, &updatedAt, &completedAt); err != nil {
		return AutomationJob{}, err
	}
	out.Kind = automation.JobKind(kind)
	out.Status = automation.JobStatus(status)
	out.AvailableAt = parseTime(availableAt)
	out.CreatedAt = parseTime(createdAt)
	out.UpdatedAt = parseTime(updatedAt)
	if lockedUntil.Valid {
		t := parseTime(lockedUntil.String)
		out.LockedUntil = &t
	}
	if completedAt.Valid {
		t := parseTime(completedAt.String)
		out.CompletedAt = &t
	}
	return out, nil
}

// EnqueueJobRequest is one durable unit of work to record.
type EnqueueJobRequest struct {
	EventID  string
	Kind     automation.JobKind
	TargetID int64
	// MaxAttempts bounds this job's tries. It is stored per row so changing the
	// configuration does not rewrite jobs already queued.
	MaxAttempts int
	// AvailableAt is when the job may first run; zero means immediately.
	AvailableAt time.Time
}

// knownJobKind reports whether a job kind is one this queue carries.
//
// The list is closed on purpose: a row whose kind nothing executes would be
// claimed and released forever by every worker, so a typo is refused at the
// door rather than becoming permanent background noise.
func knownJobKind(kind automation.JobKind) bool {
	switch kind {
	case automation.JobWebhookDelivery, automation.JobWorkflowRun, automation.JobTelegramMessageSend:
		return true
	}
	return false
}

// EnqueueAutomationJob records one unit of background work and reports whether
// it was new.
//
// (job_type, target_id, event_id) is unique, so a repeated fan-out after a
// partial failure reuses the existing row instead of delivering twice. A job
// that already ran and succeeded is never revived by that: the conflict is
// resolved in favour of the row that exists, whatever state it is in.
func (s *Store) EnqueueAutomationJob(ctx context.Context, req EnqueueJobRequest) (AutomationJob, bool, error) {
	if strings.TrimSpace(req.EventID) == "" {
		return AutomationJob{}, false, fmt.Errorf("%w: event id is empty", ErrInvalidAutomationJob)
	}
	if !knownJobKind(req.Kind) {
		return AutomationJob{}, false, fmt.Errorf("%w: unknown job kind %q", ErrInvalidAutomationJob, req.Kind)
	}
	if req.TargetID <= 0 {
		return AutomationJob{}, false, fmt.Errorf("%w: target id must be positive", ErrInvalidAutomationJob)
	}
	if req.MaxAttempts <= 0 {
		return AutomationJob{}, false, fmt.Errorf("%w: max_attempts must be positive", ErrInvalidAutomationJob)
	}

	now := s.automationNow()
	available := req.AvailableAt
	if available.IsZero() {
		available = now
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO automation_jobs (
	event_id, job_type, target_id, status, attempt_count, max_attempts,
	available_at, locked_by, locked_until, last_error_code, last_error_message,
	created_at, updated_at, completed_at
) VALUES (?, ?, ?, ?, 0, ?, ?, '', NULL, '', '', ?, ?, NULL)
ON CONFLICT (job_type, target_id, event_id) DO NOTHING`,
		req.EventID, string(req.Kind), req.TargetID, string(automation.JobPending), req.MaxAttempts,
		formatAutomationTime(available), formatAutomationTime(now), formatAutomationTime(now))
	if err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: enqueue automation job: %w", err)
	}
	if written, err := res.RowsAffected(); err == nil && written == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return AutomationJob{}, false, fmt.Errorf("channelstore: enqueue automation job id: %w", err)
		}
		job, found, err := s.AutomationJob(ctx, id)
		if err != nil || !found {
			return AutomationJob{}, false, fmt.Errorf("channelstore: read back automation job %d: %w", id, err)
		}
		return job, true, nil
	}

	job, found, err := s.automationJobByIdentity(ctx, string(req.Kind), req.TargetID, req.EventID)
	if err != nil {
		return AutomationJob{}, false, err
	}
	if !found {
		return AutomationJob{}, false, fmt.Errorf("channelstore: enqueue automation job: %w", ErrInvalidAutomationJob)
	}
	return job, false, nil
}

func (s *Store) automationJobByIdentity(ctx context.Context, kind string, targetID int64, eventID string) (AutomationJob, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+automationJobColumns+` FROM automation_jobs
		 WHERE job_type = ? AND target_id = ? AND event_id = ?`, kind, targetID, eventID)
	job, err := scanAutomationJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationJob{}, false, nil
	}
	if err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: read automation job: %w", err)
	}
	return job, true, nil
}

// ClaimJobRequest describes who is taking work and for how long.
type ClaimJobRequest struct {
	// Worker names the claiming process, so a lease can be attributed and
	// released. It is an identity for the lease, not a credential.
	Worker string
	// Lease is how long the claim stands before another worker may reclaim the
	// job. Zero or negative means the caller made a mistake.
	Lease time.Duration
}

// claimableSelect is the predicate for "this row may be handed out now", as
// the selection sees it. It correlates on the candidate's own job_type and
// target_id, so it needs no knowledge of which row will win.
//
// Ordering policy: a job that failed terminally is recorded and left alone, and
// it does not block the ones behind it. Serialising behind a job that can never
// run again would strand a target's whole future, which is a worse failure than
// running one message after a gap the record already explains. A job still
// pending, processing or waiting to be retried does hold its place in line.
const claimableSelect = `((
	(status IN ('pending', 'retry_wait') AND available_at <= ?)
	OR (status = 'processing' AND locked_until IS NOT NULL AND locked_until <= ?)
) AND NOT EXISTS (
	SELECT 1 FROM automation_jobs AS earlier
	WHERE earlier.job_type = automation_jobs.job_type
	  AND earlier.target_id = automation_jobs.target_id
	  AND earlier.id < automation_jobs.id
	  AND earlier.status IN ('pending', 'processing', 'retry_wait')
))`

// claimableGuard is the same predicate for the write, stated against a named
// row so the update cannot take a job the selection would have refused. The
// two are kept apart rather than concatenated because the subquery has to be
// correlated differently in each; the columns they test are identical on
// purpose, and any divergence between them is a bug. It binds five values: the
// current instant twice, then the chosen row's job_type, target_id and id.
const claimableGuard = `((
	(status IN ('pending', 'retry_wait') AND available_at <= ?)
	OR (status = 'processing' AND locked_until IS NOT NULL AND locked_until <= ?)
) AND NOT EXISTS (
	SELECT 1 FROM automation_jobs AS earlier
	WHERE earlier.job_type = ? AND earlier.target_id = ? AND earlier.id < ?
	  AND earlier.status IN ('pending', 'processing', 'retry_wait')
))`

// ClaimAutomationJob leases the next runnable job for worker, if there is one.
//
// The claim happens inside one transaction and the write is guarded by the same
// predicate that selected the row, so two workers polling the same database
// cannot both come away with it. A job is runnable when it is due, or when its
// lease has expired and the worker holding it is gone.
//
// Claiming increments attempt_count: the lease is what spends the attempt, and
// a job whose worker died must not be retried forever without that showing.
func (s *Store) ClaimAutomationJob(ctx context.Context, req ClaimJobRequest) (AutomationJob, bool, error) {
	if strings.TrimSpace(req.Worker) == "" {
		return AutomationJob{}, false, fmt.Errorf("%w: worker name is empty", ErrInvalidAutomationJob)
	}
	if req.Lease <= 0 {
		return AutomationJob{}, false, fmt.Errorf("%w: lease must be positive", ErrInvalidAutomationJob)
	}

	now := s.automationNow()
	nowStamp := formatAutomationTime(now)
	untilStamp := formatAutomationTime(now.Add(req.Lease))

	// The guarded update can lose a race to another process; try a few times
	// rather than report an empty queue that is not empty.
	for attempt := 0; attempt < 3; attempt++ {
		job, ok, err := s.tryClaimAutomationJob(ctx, req.Worker, nowStamp, untilStamp)
		if err != nil || ok {
			return job, ok, err
		}
	}
	return AutomationJob{}, false, nil
}

func (s *Store) tryClaimAutomationJob(ctx context.Context, worker, nowStamp, untilStamp string) (AutomationJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: claim automation job: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		id       int64
		kind     string
		targetID int64
	)
	err = tx.QueryRowContext(ctx, `
SELECT id, job_type, target_id FROM automation_jobs
WHERE `+claimableSelect+`
ORDER BY id LIMIT 1`, nowStamp, nowStamp).Scan(&id, &kind, &targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationJob{}, false, nil
	}
	if err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: claim automation job: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
UPDATE automation_jobs
SET status = ?, locked_by = ?, locked_until = ?,
	attempt_count = attempt_count + 1, updated_at = ?
WHERE id = ? AND `+claimableGuard,
		string(automation.JobProcessing), worker, untilStamp, nowStamp,
		id, nowStamp, nowStamp, kind, targetID, id)
	if err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: claim automation job %d: %w", id, err)
	}
	if written, err := res.RowsAffected(); err != nil || written == 0 {
		// Another worker took it between the read and the write. The caller
		// retries: reporting an empty queue here would be a lie.
		return AutomationJob{}, false, nil
	}

	job, err := scanAutomationJob(tx.QueryRowContext(ctx,
		`SELECT `+automationJobColumns+` FROM automation_jobs WHERE id = ?`, id))
	if err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: claim automation job %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: claim automation job %d: %w", id, err)
	}
	return job, true, nil
}

// CompleteAutomationJob records that the claim's work finished.
func (s *Store) CompleteAutomationJob(ctx context.Context, id int64, worker string) error {
	now := s.automationNow()
	res, err := s.db.ExecContext(ctx, `
UPDATE automation_jobs
SET status = ?, locked_by = '', locked_until = NULL,
	last_error_code = '', last_error_message = '',
	updated_at = ?, completed_at = ?
WHERE id = ? AND status = 'processing' AND locked_by = ?`,
		string(automation.JobSucceeded), formatAutomationTime(now), formatAutomationTime(now), id, worker)
	if err != nil {
		return fmt.Errorf("channelstore: complete automation job %d: %w", id, err)
	}
	if written, err := res.RowsAffected(); err != nil || written == 0 {
		return fmt.Errorf("%w: job %d", ErrAutomationJobNotClaimed, id)
	}
	return nil
}

// RecordAutomationJobFailure persists one attempt's outcome.
//
// When retry is true the job returns to retry_wait and becomes claimable again
// at nextAttempt; otherwise it is terminally failed and its error stays on the
// row for the UI to explain. The caller owns that policy; this method only
// writes what it is told.
func (s *Store) RecordAutomationJobFailure(ctx context.Context, id int64, worker string,
	jobErr automation.JobError, retry bool, nextAttempt time.Time) (automation.JobStatus, error) {
	now := s.automationNow()
	status := automation.JobFailed
	completed := any(formatAutomationTime(now))
	if retry {
		status = automation.JobRetryWait
		completed = nil
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE automation_jobs
SET status = ?, locked_by = '', locked_until = NULL,
	last_error_code = ?, last_error_message = ?,
	available_at = ?, updated_at = ?, completed_at = ?
WHERE id = ? AND status = 'processing' AND locked_by = ?`,
		string(status), jobErr.Code, boundedError(jobErr.Message),
		formatAutomationTime(nextAttempt), formatAutomationTime(now), completed, id, worker)
	if err != nil {
		return "", fmt.Errorf("channelstore: record automation job failure %d: %w", id, err)
	}
	if written, err := res.RowsAffected(); err != nil || written == 0 {
		return "", fmt.Errorf("%w: job %d", ErrAutomationJobNotClaimed, id)
	}
	return status, nil
}

// errorTextCap bounds what one handler's message may store. A handler that
// echoes a response body into its error must not be able to fill the database
// with it, and a truncated message is still enough to explain a failure.
const errorTextCap = 2000

func boundedError(msg string) string {
	if len(msg) <= errorTextCap {
		return msg
	}
	return msg[:errorTextCap]
}

// ReleaseAutomationJob gives a claimed job back without recording an outcome.
//
// It is what a graceful shutdown uses for work it holds but has not finished:
// the job is immediately claimable again rather than waiting out its lease. The
// attempt it spent stays spent, because the queue cannot know how far the
// handler got and pretending otherwise would let a broken job retry forever.
func (s *Store) ReleaseAutomationJob(ctx context.Context, id int64, worker string) error {
	now := s.automationNow()
	res, err := s.db.ExecContext(ctx, `
UPDATE automation_jobs
SET status = ?, locked_by = '', locked_until = NULL, updated_at = ?
WHERE id = ? AND status = 'processing' AND locked_by = ?`,
		string(automation.JobPending), formatAutomationTime(now), id, worker)
	if err != nil {
		return fmt.Errorf("channelstore: release automation job %d: %w", id, err)
	}
	if written, err := res.RowsAffected(); err != nil || written == 0 {
		return fmt.Errorf("%w: job %d", ErrAutomationJobNotClaimed, id)
	}
	return nil
}

// CancelAutomationJob stops a job that has not finished and returns the state
// the job is in afterwards. A job already succeeded is left alone: cancellation
// is not a rewind.
func (s *Store) CancelAutomationJob(ctx context.Context, id int64) (automation.JobStatus, error) {
	now := s.automationNow()
	if _, err := s.db.ExecContext(ctx, `
UPDATE automation_jobs
SET status = ?, locked_by = '', locked_until = NULL, updated_at = ?, completed_at = ?
WHERE id = ? AND status IN ('pending', 'retry_wait', 'processing')`,
		string(automation.JobCancelled), formatAutomationTime(now), formatAutomationTime(now), id); err != nil {
		return "", fmt.Errorf("channelstore: cancel automation job %d: %w", id, err)
	}
	job, found, err := s.AutomationJob(ctx, id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: job %d", ErrAutomationJobNotFound, id)
	}
	return job.Status, nil
}

// ReopenJobRequest names the work an operator has asked to try again.
type ReopenJobRequest struct {
	EventID  string
	Kind     automation.JobKind
	TargetID int64
	// MaxAttempts is the fresh budget. Zero keeps the row's own value.
	MaxAttempts int
}

// ReopenAutomationJob gives a job that is no longer running a fresh attempt
// budget and puts it back in the queue.
//
// EnqueueAutomationJob will not do this, and deliberately: it is idempotent so
// a replayed fan-out cannot deliver twice, which also means it never revives
// work that has run out of attempts. A person who looks at a failed delivery
// and presses retry is making a different decision — an explicit one — so it
// gets its own method rather than weakening the idempotency everything else
// depends on.
//
// A job that already succeeded is refused: retrying it could publish the same
// message a second time. A job in flight is refused too, because two workers
// on one delivery is how a duplicate happens.
func (s *Store) ReopenAutomationJob(ctx context.Context, req ReopenJobRequest) (AutomationJob, bool, error) {
	if strings.TrimSpace(req.EventID) == "" {
		return AutomationJob{}, false, fmt.Errorf("%w: event id is empty", ErrInvalidAutomationJob)
	}
	existing, found, err := s.automationJobByIdentity(ctx, string(req.Kind), req.TargetID, req.EventID)
	if err != nil {
		return AutomationJob{}, false, err
	}
	if !found {
		return AutomationJob{}, false, fmt.Errorf("%w: job %s/%d/%s",
			ErrAutomationJobNotFound, req.Kind, req.TargetID, req.EventID)
	}
	switch existing.Status {
	case automation.JobSucceeded:
		return AutomationJob{}, false, fmt.Errorf(
			"%w: job %d already succeeded and must not be repeated", automation.ErrConflict, existing.ID)
	case automation.JobProcessing:
		return AutomationJob{}, false, fmt.Errorf(
			"%w: job %d is running", automation.ErrConflict, existing.ID)
	}

	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = existing.MaxAttempts
	}
	now := s.automationNow()
	if _, err := s.db.ExecContext(ctx, `
UPDATE automation_jobs
SET status = ?, attempt_count = 0, max_attempts = ?, available_at = ?,
	locked_by = '', locked_until = NULL,
	last_error_code = '', last_error_message = '',
	updated_at = ?, completed_at = NULL
WHERE id = ?`,
		string(automation.JobPending), maxAttempts, formatAutomationTime(now),
		formatAutomationTime(now), existing.ID); err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: reopen automation job %d: %w", existing.ID, err)
	}
	return s.AutomationJob(ctx, existing.ID)
}

// AutomationJob returns one job by id.
func (s *Store) AutomationJob(ctx context.Context, id int64) (AutomationJob, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+automationJobColumns+` FROM automation_jobs WHERE id = ?`, id)
	job, err := scanAutomationJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationJob{}, false, nil
	}
	if err != nil {
		return AutomationJob{}, false, fmt.Errorf("channelstore: read automation job %d: %w", id, err)
	}
	return job, true, nil
}

// AutomationJobQuery selects jobs for diagnosis and tests.
type AutomationJobQuery struct {
	EventID string
	Kind    automation.JobKind
	// Status, when non-empty, narrows to one lifecycle state.
	Status automation.JobStatus
	Limit  int
}

// AutomationJobs lists matching jobs, oldest first.
func (s *Store) AutomationJobs(ctx context.Context, q AutomationJobQuery) ([]AutomationJob, error) {
	var conds []string
	var args []any
	if q.EventID != "" {
		conds = append(conds, `event_id = ?`)
		args = append(args, q.EventID)
	}
	if q.Kind != "" {
		conds = append(conds, `job_type = ?`)
		args = append(args, string(q.Kind))
	}
	if q.Status != "" {
		conds = append(conds, `status = ?`)
		args = append(args, string(q.Status))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+automationJobColumns+` FROM automation_jobs`+where+` ORDER BY id LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list automation jobs: %w", err)
	}
	defer rows.Close()
	out := []AutomationJob{}
	for rows.Next() {
		job, err := scanAutomationJob(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list automation jobs: %w", err)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list automation jobs: %w", err)
	}
	return out, nil
}

// AutomationJobCounts is the queue's shape at one moment, for the status route.
type AutomationJobCounts struct {
	Pending    int
	RetryWait  int
	Processing int
	Succeeded  int
	Failed     int
	Cancelled  int
}

// AutomationJobCounts returns how many jobs are in each lifecycle state.
func (s *Store) AutomationJobCounts(ctx context.Context) (AutomationJobCounts, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM automation_jobs GROUP BY status`)
	if err != nil {
		return AutomationJobCounts{}, fmt.Errorf("channelstore: count automation jobs: %w", err)
	}
	defer rows.Close()
	var out AutomationJobCounts
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return AutomationJobCounts{}, fmt.Errorf("channelstore: count automation jobs: %w", err)
		}
		switch automation.JobStatus(status) {
		case automation.JobPending:
			out.Pending = n
		case automation.JobRetryWait:
			out.RetryWait = n
		case automation.JobProcessing:
			out.Processing = n
		case automation.JobSucceeded:
			out.Succeeded = n
		case automation.JobFailed:
			out.Failed = n
		case automation.JobCancelled:
			out.Cancelled = n
		}
	}
	if err := rows.Err(); err != nil {
		return AutomationJobCounts{}, fmt.Errorf("channelstore: count automation jobs: %w", err)
	}
	return out, nil
}

// RecentAutomationFailures returns the most recent terminally failed jobs, for
// the status route. It reports what failed and why; it never carries a payload.
func (s *Store) RecentAutomationFailures(ctx context.Context, limit int) ([]automation.AutomationFailure, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT job_type, target_id, last_error_code, last_error_message, completed_at
FROM automation_jobs WHERE status = 'failed'
ORDER BY COALESCE(completed_at, updated_at) DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list automation failures: %w", err)
	}
	defer rows.Close()
	out := []automation.AutomationFailure{}
	for rows.Next() {
		var (
			failure   automation.AutomationFailure
			kind      string
			completed sql.NullString
		)
		if err := rows.Scan(&kind, &failure.TargetID, &failure.ErrorCode, &failure.ErrorMessage, &completed); err != nil {
			return nil, fmt.Errorf("channelstore: list automation failures: %w", err)
		}
		failure.JobType = kind
		if completed.Valid {
			failure.At = parseTime(completed.String)
		}
		out = append(out, failure)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list automation failures: %w", err)
	}
	return out, nil
}
