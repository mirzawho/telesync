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

// Workflow run records: what one source message did through one workflow.
//
// The run is created when the queued work is first executed and is never
// recreated, so a retry resumes the same row: the content that was prepared,
// the AI outcome and every destination's result survive a restart. That is what
// makes "never publish to a destination twice" a fact and not an intention.

// Workflow run lifecycle states. A run that reached any of the last four is
// finished; queued and processing are the ones work continues from.
const (
	WorkflowRunQueued                = "queued"
	WorkflowRunProcessing            = "processing"
	WorkflowRunPublished             = "published"
	WorkflowRunPublishedWithFallback = "published_with_fallback"
	WorkflowRunPartiallyPublished    = "partially_published"
	WorkflowRunFailed                = "failed"
	WorkflowRunCancelled             = "cancelled"
)

// Workflow publication states, one per destination of a run.
const (
	WorkflowPublicationPending   = "pending"
	WorkflowPublicationPublished = "published"
	WorkflowPublicationFailed    = "failed"
	// WorkflowPublicationUnknown means the request left this process without a
	// confirmed outcome. It is never retried as if it had failed.
	WorkflowPublicationUnknown = "unknown"
)

// Workflow AI states, one per run.
const (
	WorkflowAINotUsed      = "not_used"
	WorkflowAISucceeded    = "succeeded"
	WorkflowAIFailed       = "failed"
	WorkflowAIFallbackUsed = "fallback_used"
)

var (
	// ErrWorkflowRunNotFound means no run has that id.
	ErrWorkflowRunNotFound = fmt.Errorf("%w: workflow run", automation.ErrNotFound)
	// ErrInvalidWorkflowRun means the run request itself is not usable.
	ErrInvalidWorkflowRun = fmt.Errorf("%w: workflow run", automation.ErrInvalid)
)

// BeginWorkflowRunRequest is the one execution the queue is starting.
type BeginWorkflowRunRequest struct {
	WorkflowID      int64
	EventID         string
	SourceMessageID int64
	// OriginalContent is the source message's own text. It is kept verbatim and
	// is never overwritten by a polished version.
	OriginalContent string
}

// WorkflowRunAIUpdate is what one AI stage reported.
type WorkflowRunAIUpdate struct {
	// Status is one of the WorkflowAI constants.
	Status       string
	Polished     string
	FallbackUsed bool
	Model        string
	ErrorCode    string
	ErrorMessage string
	InputTokens  int
	OutputTokens int
	Duration     time.Duration
}

const workflowRunColumns = `id, workflow_id, event_id, status, source_message_id, original_content,
	polished_content, ai_status, ai_fallback_used, ai_model, ai_error_code, ai_error_message,
	ai_input_tokens, ai_output_tokens, ai_duration_ms, config_revision, attempt_count,
	last_error_code, last_error_message, created_at, started_at, completed_at`

// scanWorkflowRunRow reads one run row. Publications are read separately.
func scanWorkflowRunRow(scan func(dest ...any) error) (automation.WorkflowRun, error) {
	var (
		out                    automation.WorkflowRun
		status, aiStatus       string
		fallbackUsed           int
		createdAt              string
		startedAt, completedAt sql.NullString
	)
	err := scan(&out.ID, &out.WorkflowID, &out.EventID, &status, &out.SourceMessageID,
		&out.OriginalContent, &out.PolishedContent, &aiStatus, &fallbackUsed, &out.AIModel,
		&out.AIErrorCode, &out.AIErrorMessage, &out.AIInputTokens, &out.AIOutputTokens,
		&out.AIDurationMS, &out.ConfigRevision, &out.AttemptCount, &out.LastErrorCode,
		&out.LastErrorMessage, &createdAt, &startedAt, &completedAt)
	if err != nil {
		return automation.WorkflowRun{}, err
	}
	out.Status = status
	out.AIStatus = aiStatus
	out.AIFallbackUsed = fallbackUsed != 0
	out.CreatedAt = parseTime(createdAt)
	if startedAt.Valid {
		t := parseTime(startedAt.String)
		out.StartedAt = &t
	}
	if completedAt.Valid {
		t := parseTime(completedAt.String)
		out.CompletedAt = &t
	}
	return out, nil
}

// BeginWorkflowRun records one execution of one workflow for one source event.
//
// created is false when the run already exists, which is the normal answer for a
// redelivered event: one event runs one workflow once.
//
// The run copies the workflow's configuration revision, so history says which
// configuration produced it after the workflow has been edited again. Retries
// of a run work from what this row and its publication rows already hold, so a
// later edit cannot change what a queued run does.
func (s *Store) BeginWorkflowRun(ctx context.Context, in BeginWorkflowRunRequest) (automation.WorkflowRun, bool, error) {
	if in.WorkflowID <= 0 {
		return automation.WorkflowRun{}, false, fmt.Errorf("%w: workflow_id must be positive", ErrInvalidWorkflowRun)
	}
	if strings.TrimSpace(in.EventID) == "" {
		return automation.WorkflowRun{}, false, fmt.Errorf("%w: event id is empty", ErrInvalidWorkflowRun)
	}
	if in.SourceMessageID <= 0 {
		return automation.WorkflowRun{}, false, fmt.Errorf("%w: source_message_id must be positive", ErrInvalidWorkflowRun)
	}

	now := formatTime(s.now())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO workflow_runs (
	workflow_id, event_id, status, source_message_id, original_content,
	ai_status, config_revision, attempt_count, created_at)
SELECT id, ?, ?, ?, ?, ?, config_revision, 0, ? FROM workflows WHERE id = ?`,
		in.EventID, WorkflowRunQueued, in.SourceMessageID, in.OriginalContent,
		WorkflowAINotUsed, now, in.WorkflowID)
	if err != nil {
		if isUniqueViolation(err) {
			// The event already has its run. That is the normal answer for a
			// redelivered event, and it is never a second run.
			run, _, err := s.workflowRunByIdentity(ctx, in.WorkflowID, in.EventID)
			return run, false, err
		}
		return automation.WorkflowRun{}, false, fmt.Errorf("channelstore: begin workflow run: %w", err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		// Either the workflow is gone or the run already exists. The identity is
		// checked first: a redelivered event is normal, a missing workflow is
		// not.
		if run, found, err := s.workflowRunByIdentity(ctx, in.WorkflowID, in.EventID); err != nil || found {
			return run, false, err
		}
		return automation.WorkflowRun{}, false, fmt.Errorf("%w: no workflow has id %d", ErrWorkflowRunNotFound, in.WorkflowID)
	}
	run, found, err := s.workflowRunByIdentity(ctx, in.WorkflowID, in.EventID)
	if err != nil {
		return automation.WorkflowRun{}, false, err
	}
	if !found {
		return automation.WorkflowRun{}, false, fmt.Errorf("%w: the run just recorded is not readable", ErrWorkflowRunNotFound)
	}
	return run, true, nil
}

// workflowRunByIdentity reads the run one event has on one workflow.
func (s *Store) workflowRunByIdentity(ctx context.Context, workflowID int64, eventID string) (automation.WorkflowRun, bool, error) {
	run, err := scanWorkflowRunRow(s.db.QueryRowContext(ctx,
		`SELECT `+workflowRunColumns+` FROM workflow_runs WHERE workflow_id = ? AND event_id = ?`,
		workflowID, eventID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.WorkflowRun{}, false, nil
	}
	if err != nil {
		return automation.WorkflowRun{}, false, fmt.Errorf("channelstore: get workflow run: %w", err)
	}
	return run, true, nil
}

// GetWorkflowRun reads one run and its per-destination results.
func (s *Store) GetWorkflowRun(ctx context.Context, id int64) (automation.WorkflowRun, bool, error) {
	run, err := scanWorkflowRunRow(s.db.QueryRowContext(ctx,
		`SELECT `+workflowRunColumns+` FROM workflow_runs WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.WorkflowRun{}, false, nil
	}
	if err != nil {
		return automation.WorkflowRun{}, false, fmt.Errorf("channelstore: get workflow run: %w", err)
	}
	pubs, err := s.WorkflowPublications(ctx, run.ID)
	if err != nil {
		return automation.WorkflowRun{}, false, err
	}
	run.Publications = pubs
	return run, true, nil
}

// GetRun reads one run of one workflow. A run belonging to another workflow is
// not readable here: it is not this workflow's history.
func (s *Store) GetRun(ctx context.Context, workflowID, runID int64) (automation.WorkflowRun, bool, error) {
	run, err := scanWorkflowRunRow(s.db.QueryRowContext(ctx,
		`SELECT `+workflowRunColumns+` FROM workflow_runs WHERE id = ? AND workflow_id = ?`,
		runID, workflowID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.WorkflowRun{}, false, nil
	}
	if err != nil {
		return automation.WorkflowRun{}, false, fmt.Errorf("channelstore: get workflow run: %w", err)
	}
	pubs, err := s.WorkflowPublications(ctx, run.ID)
	if err != nil {
		return automation.WorkflowRun{}, false, err
	}
	run.Publications = pubs
	return run, true, nil
}

// ListRuns lists one page of a workflow's runs, newest first.
func (s *Store) ListRuns(ctx context.Context, workflowID int64, opts automation.RunListOptions) (automation.WorkflowRunPage, error) {
	conds := []string{"workflow_id = ?"}
	args := []any{workflowID}
	if status := strings.TrimSpace(opts.Status); status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	where := " WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs`+where, args...).Scan(&total); err != nil {
		return automation.WorkflowRunPage{}, fmt.Errorf("channelstore: count workflow runs: %w", err)
	}

	limit, offset := pageBounds(opts.Page, opts.PerPage, 50)
	rows, err := s.db.QueryContext(ctx, `SELECT `+workflowRunColumns+` FROM workflow_runs`+where+
		` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return automation.WorkflowRunPage{}, fmt.Errorf("channelstore: list workflow runs: %w", err)
	}
	defer rows.Close()

	out := []automation.WorkflowRun{}
	ids := []int64{}
	for rows.Next() {
		run, err := scanWorkflowRunRow(rows.Scan)
		if err != nil {
			return automation.WorkflowRunPage{}, fmt.Errorf("channelstore: list workflow runs: %w", err)
		}
		out = append(out, run)
		ids = append(ids, run.ID)
	}
	if err := rows.Err(); err != nil {
		return automation.WorkflowRunPage{}, fmt.Errorf("channelstore: list workflow runs: %w", err)
	}
	if err := s.fillRunPublications(ctx, out, ids); err != nil {
		return automation.WorkflowRunPage{}, err
	}
	return automation.WorkflowRunPage{Runs: out, Total: total}, nil
}

// fillRunPublications attaches every destination result to the page of runs
// that carries them, in one query for the whole page.
func (s *Store) fillRunPublications(ctx context.Context, runs []automation.WorkflowRun, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	marks := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		marks = append(marks, "?")
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+publicationColumns+` FROM workflow_publications
	WHERE workflow_run_id IN (`+strings.Join(marks, ",")+`) ORDER BY workflow_run_id, id`, args...)
	if err != nil {
		return fmt.Errorf("channelstore: read workflow publications: %w", err)
	}
	defer rows.Close()

	byRun := map[int64][]automation.WorkflowPublication{}
	for rows.Next() {
		pub, runID, err := scanPublicationRow(rows.Scan)
		if err != nil {
			return fmt.Errorf("channelstore: read workflow publication: %w", err)
		}
		byRun[runID] = append(byRun[runID], pub)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("channelstore: read workflow publications: %w", err)
	}
	for i := range runs {
		if pubs, ok := byRun[runs[i].ID]; ok {
			runs[i].Publications = pubs
		}
	}
	return nil
}

// StartWorkflowRun marks a run as executing and counts one attempt.
//
// A retry of the same run is the same row, one more attempt: the content the
// run prepared and what each destination already did are both kept.
func (s *Store) StartWorkflowRun(ctx context.Context, runID int64) (automation.WorkflowRun, error) {
	now := formatTime(s.now())
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_runs
	SET status = ?, started_at = COALESCE(started_at, ?), attempt_count = attempt_count + 1 WHERE id = ?`,
		WorkflowRunProcessing, now, runID)
	if err != nil {
		return automation.WorkflowRun{}, fmt.Errorf("channelstore: start workflow run %d: %w", runID, err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return automation.WorkflowRun{}, ErrWorkflowRunNotFound
	}
	run, found, err := s.GetWorkflowRun(ctx, runID)
	if err != nil {
		return automation.WorkflowRun{}, err
	}
	if !found {
		return automation.WorkflowRun{}, ErrWorkflowRunNotFound
	}
	return run, nil
}

// RecordWorkflowRunAI keeps the AI stage's outcome, and the polished text,
// apart from the source content. The source text is never touched: it is what
// the message said, and it is what a fallback publishes.
func (s *Store) RecordWorkflowRunAI(ctx context.Context, runID int64, update WorkflowRunAIUpdate) error {
	switch update.Status {
	case WorkflowAINotUsed, WorkflowAISucceeded, WorkflowAIFailed, WorkflowAIFallbackUsed:
	default:
		return fmt.Errorf("%w: %q is not an AI status", ErrInvalidWorkflowRun, update.Status)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_runs SET
	ai_status = ?, polished_content = ?, ai_fallback_used = ?, ai_model = ?, ai_error_code = ?,
	ai_error_message = ?, ai_input_tokens = ?, ai_output_tokens = ?, ai_duration_ms = ? WHERE id = ?`,
		update.Status, update.Polished, boolInt(update.FallbackUsed), update.Model, update.ErrorCode,
		update.ErrorMessage, update.InputTokens, update.OutputTokens, update.Duration.Milliseconds(), runID)
	if err != nil {
		return fmt.Errorf("channelstore: record workflow AI %d: %w", runID, err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return ErrWorkflowRunNotFound
	}
	return nil
}

// CompleteWorkflowRun records a finished run's outcome and the reason for a
// failure. The reason is what an operator reads; it never carries a credential.
func (s *Store) CompleteWorkflowRun(ctx context.Context, runID int64, status, lastErrorCode, lastErrorMessage string) error {
	switch status {
	case WorkflowRunPublished, WorkflowRunPublishedWithFallback, WorkflowRunPartiallyPublished,
		WorkflowRunFailed, WorkflowRunCancelled:
	default:
		return fmt.Errorf("%w: %q is not a finished run status", ErrInvalidWorkflowRun, status)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_runs SET
	status = ?, last_error_code = ?, last_error_message = ?, completed_at = ? WHERE id = ?`,
		status, lastErrorCode, lastErrorMessage, formatTime(s.now()), runID)
	if err != nil {
		return fmt.Errorf("channelstore: complete workflow run %d: %w", runID, err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return ErrWorkflowRunNotFound
	}
	return nil
}

const publicationColumns = `id, workflow_run_id, destination_type, client_key, channel_id, site_id,
	status, remote_message_id, remote_post_id, remote_url, attempt_count,
	last_error_code, last_error_message, created_at, completed_at`

// scanPublicationRow reads one destination result. The second return is the run
// it belongs to, so a batched read can group without a second column list.
func scanPublicationRow(scan func(dest ...any) error) (automation.WorkflowPublication, int64, error) {
	var (
		pub              automation.WorkflowPublication
		destType, status string
		clientKey        string
		createdAt        string
		completedAt      sql.NullString
	)
	err := scan(&pub.ID, &pub.WorkflowRunID, &destType, &clientKey, &pub.ChannelID, &pub.SiteID,
		&status, &pub.RemoteMessageID, &pub.RemotePostID, &pub.RemoteURL, &pub.AttemptCount,
		&pub.LastErrorCode, &pub.LastErrorMessage, &createdAt, &completedAt)
	if err != nil {
		return automation.WorkflowPublication{}, 0, err
	}
	pub.DestinationType = destType
	pub.ClientKey = clientKey
	pub.Status = status
	if completedAt.Valid {
		t := parseTime(completedAt.String)
		pub.CompletedAt = &t
	}
	return pub, pub.WorkflowRunID, nil
}

// EnsureWorkflowPublication returns one destination's result row for a run,
// creating it as pending when this is the first attempt.
//
// The row is what makes "never publish to a destination twice" true: a retry
// reads it back and repeats only what did not succeed. created is false when
// the destination already has a row, whatever state that row is in.
func (s *Store) EnsureWorkflowPublication(ctx context.Context, runID int64,
	dest automation.WorkflowPublication) (automation.WorkflowPublication, bool, error) {

	if runID <= 0 {
		return automation.WorkflowPublication{}, false, fmt.Errorf("%w: workflow_run_id must be positive",
			ErrInvalidWorkflowRun)
	}
	switch dest.DestinationType {
	case string(automation.DestinationTelegram), string(automation.DestinationWordPress):
	default:
		return automation.WorkflowPublication{}, false, fmt.Errorf("%w: %q is not a destination type",
			ErrInvalidWorkflowRun, dest.DestinationType)
	}

	existing, found, err := s.workflowPublicationByIdentity(ctx, runID, dest)
	if err != nil {
		return automation.WorkflowPublication{}, false, err
	}
	if found {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE workflow_publications SET attempt_count = attempt_count + 1 WHERE id = ?`, existing.ID); err != nil {
			return automation.WorkflowPublication{}, false, fmt.Errorf("channelstore: retry workflow publication: %w", err)
		}
		existing.AttemptCount++
		return existing, false, nil
	}

	now := formatTime(s.now())
	res, err := s.db.ExecContext(ctx, `INSERT INTO workflow_publications (
	workflow_run_id, destination_type, client_key, channel_id, site_id, status, attempt_count, created_at)
	VALUES (?, ?, ?, ?, ?, ?, 1, ?)`,
		runID, dest.DestinationType, strings.TrimSpace(dest.ClientKey), dest.ChannelID, dest.SiteID,
		WorkflowPublicationPending, now)
	if err != nil {
		if isUniqueViolation(err) {
			pub, _, err := s.workflowPublicationByIdentity(ctx, runID, dest)
			return pub, false, err
		}
		return automation.WorkflowPublication{}, false, fmt.Errorf("channelstore: record workflow publication: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return automation.WorkflowPublication{}, false, fmt.Errorf("channelstore: record workflow publication id: %w", err)
	}
	return automation.WorkflowPublication{
		ID: id, WorkflowRunID: runID, DestinationType: dest.DestinationType,
		ClientKey: strings.TrimSpace(dest.ClientKey), ChannelID: dest.ChannelID, SiteID: dest.SiteID,
		Status: WorkflowPublicationPending, AttemptCount: 1,
	}, true, nil
}

// workflowPublicationByIdentity reads one destination's result row for a run.
func (s *Store) workflowPublicationByIdentity(ctx context.Context, runID int64,
	dest automation.WorkflowPublication) (automation.WorkflowPublication, bool, error) {

	pub, gotRun, err := scanPublicationRow(s.db.QueryRowContext(ctx,
		`SELECT `+publicationColumns+` FROM workflow_publications
	WHERE workflow_run_id = ? AND destination_type = ? AND client_key = ? AND channel_id = ? AND site_id = ?`,
		runID, dest.DestinationType, strings.TrimSpace(dest.ClientKey), dest.ChannelID, dest.SiteID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.WorkflowPublication{}, false, nil
	}
	if err != nil {
		return automation.WorkflowPublication{}, false, fmt.Errorf("channelstore: get workflow publication: %w", err)
	}
	_ = gotRun
	return pub, true, nil
}

// FinishWorkflowPublication records what one destination confirmed.
//
// The remote ids are the destination's own, as it reported them: they are how a
// later lookup finds the message or the post again, and how a publication is
// tied to the outbound record that stops it re-triggering automation.
func (s *Store) FinishWorkflowPublication(ctx context.Context, id int64, status string,
	remoteMessageID, remotePostID int64, remoteURL, errorCode, errorMessage string) error {

	switch status {
	case WorkflowPublicationPending, WorkflowPublicationPublished,
		WorkflowPublicationFailed, WorkflowPublicationUnknown:
	default:
		return fmt.Errorf("%w: %q is not a publication status", ErrInvalidWorkflowRun, status)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_publications SET
	status = ?, remote_message_id = ?, remote_post_id = ?, remote_url = ?,
	last_error_code = ?, last_error_message = ?, completed_at = ? WHERE id = ?`,
		status, remoteMessageID, remotePostID, remoteURL, errorCode, errorMessage,
		formatTime(s.now()), id)
	if err != nil {
		return fmt.Errorf("channelstore: finish workflow publication %d: %w", id, err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return ErrWorkflowRunNotFound
	}
	return nil
}

// WorkflowPublications lists every destination result of one run.
func (s *Store) WorkflowPublications(ctx context.Context, runID int64) ([]automation.WorkflowPublication, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+publicationColumns+`
	FROM workflow_publications WHERE workflow_run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: read workflow publications: %w", err)
	}
	defer rows.Close()

	out := []automation.WorkflowPublication{}
	for rows.Next() {
		pub, _, err := scanPublicationRow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("channelstore: read workflow publication: %w", err)
		}
		out = append(out, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: read workflow publications: %w", err)
	}
	return out, nil
}
