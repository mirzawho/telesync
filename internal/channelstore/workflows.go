package channelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
)

// Workflow records: the one automation attached to one managed source channel,
// the destinations it publishes to, and what each run achieved.
//
// The record types are the ones the HTTP gateway renders, from the shared
// automation package, so a stored row and an API answer cannot drift apart.
// Secrets never appear in them: a workflow destination names a connection and a
// channel, or a registered WordPress site, and never a credential.

// A managed channel carries at most one workflow. The schema enforces it with a
// UNIQUE column, and a second attempt is answered with the conflict the
// gateway maps onto a 409 rather than with a raw SQL error.
var (
	// ErrWorkflowExists means a workflow is already attached to that channel.
	ErrWorkflowExists = fmt.Errorf("%w: a workflow already exists for that source channel", automation.ErrConflict)
	// ErrInvalidWorkflow means the configuration itself is not usable.
	ErrInvalidWorkflow = fmt.Errorf("%w: workflow configuration", automation.ErrInvalid)
	// ErrWorkflowNotFound means no workflow has that id.
	ErrWorkflowNotFound = fmt.Errorf("%w: workflow", automation.ErrNotFound)
)

// validateWorkflowDestinations checks the target list before anything is
// written.
//
// These rules repeat the schema's CHECK constraint on purpose: a caller is
// told which field is wrong instead of being handed a database error, and a
// destination can never name two kinds of target at once.
func validateWorkflowDestinations(dests []automation.WorkflowDestination) (enabled int, err error) {
	for i, d := range dests {
		where := fmt.Sprintf("destinations[%d]", i)
		switch d.DestinationType {
		case string(automation.DestinationTelegram):
			if strings.TrimSpace(d.ClientKey) == "" {
				return 0, fmt.Errorf("%w: %s must name a client_key", ErrInvalidWorkflow, where)
			}
			if strings.TrimSpace(d.AccountID) == "" {
				return 0, fmt.Errorf("%w: %s must name an account_id", ErrInvalidWorkflow, where)
			}
			if d.ChannelID == 0 {
				return 0, fmt.Errorf("%w: %s must name a channel_id", ErrInvalidWorkflow, where)
			}
			if d.SiteID != 0 {
				return 0, fmt.Errorf("%w: %s is a telegram destination and may not name a site_id",
					ErrInvalidWorkflow, where)
			}
		case string(automation.DestinationWordPress):
			if d.SiteID <= 0 {
				return 0, fmt.Errorf("%w: %s must name a site_id", ErrInvalidWorkflow, where)
			}
			if strings.TrimSpace(d.ClientKey) != "" || d.ChannelID != 0 {
				return 0, fmt.Errorf("%w: %s is a wordpress destination and may not name a client_key or channel_id",
					ErrInvalidWorkflow, where)
			}
		default:
			return 0, fmt.Errorf("%w: %s must be telegram or wordpress", ErrInvalidWorkflow, where)
		}
		if d.Enabled {
			enabled++
		}
	}
	return enabled, nil
}

// encodeOptions stores a destination's options as a JSON object.
func encodeOptions(options map[string]any) (string, error) {
	if len(options) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return "", fmt.Errorf("%w: destination options are not encodable: %v", ErrInvalidWorkflow, err)
	}
	return string(raw), nil
}

// decodeOptions reads a destination's stored options. A row holding something
// that is not an object reads as empty rather than as a broken workflow.
func decodeOptions(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

const workflowColumns = `w.id, w.managed_channel_id, w.enabled, w.ai_enabled, w.ai_prompt,
	w.ai_send_images, w.ai_publish_on_failure, w.config_revision, w.created_at, w.updated_at,
	COALESCE(w.owner_user_id, 0), COALESCE(w.account_id, '')`

// scanWorkflowRow reads one workflow row. Destinations are read separately.
func scanWorkflowRow(scan func(dest ...any) error) (automation.Workflow, error) {
	var (
		out                       automation.Workflow
		enabled, aiEnabled        int
		sendImages, publishOnFail int
		createdAt, updatedAt      string
	)
	if err := scan(&out.ID, &out.ManagedChannelID, &enabled, &aiEnabled, &out.AIPrompt,
		&sendImages, &publishOnFail, &out.ConfigRevision, &createdAt, &updatedAt,
		&out.OwnerUserID, &out.AccountID); err != nil {
		return automation.Workflow{}, err
	}
	out.Enabled = enabled != 0
	out.AIEnabled = aiEnabled != 0
	out.AISendImages = sendImages != 0
	out.AIPublishOnFailure = publishOnFail != 0
	out.CreatedAt = parseTime(createdAt)
	out.UpdatedAt = parseTime(updatedAt)
	return out, nil
}

// destinationsOf reads one workflow's targets in their configured order.
func (s *Store) destinationsOf(ctx context.Context, workflowID int64) ([]automation.WorkflowDestination, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.workflow_id, d.destination_type, d.client_key,
	d.channel_id, d.site_id, d.account_id, d.options, d.position, d.enabled
	FROM workflow_destinations d
	WHERE d.workflow_id = ? ORDER BY d.position, d.id`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: read workflow destinations: %w", err)
	}
	defer rows.Close()

	out := []automation.WorkflowDestination{}
	for rows.Next() {
		var (
			d         automation.WorkflowDestination
			options   string
			enabled   int
			clientK   string
			destType  string
			accountID sql.NullString
		)
		if err := rows.Scan(&d.ID, &d.WorkflowID, &destType, &clientK, &d.ChannelID,
			&d.SiteID, &accountID, &options, &d.Position, &enabled); err != nil {
			return nil, fmt.Errorf("channelstore: read workflow destination: %w", err)
		}
		d.DestinationType = destType
		d.ClientKey = clientK
		d.AccountID = accountID.String
		d.Options = decodeOptions(options)
		d.Enabled = enabled != 0
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: read workflow destinations: %w", err)
	}
	return out, nil
}

// withDestinations attaches one workflow's targets.
func (s *Store) withDestinations(ctx context.Context, wf automation.Workflow) (automation.Workflow, error) {
	dests, err := s.destinationsOf(ctx, wf.ID)
	if err != nil {
		return automation.Workflow{}, err
	}
	wf.Destinations = dests
	return wf, nil
}

// CreateWorkflow stores one workflow for one source channel.
//
// in.CreatedBy is the account the configuration is made on behalf of: it is
// recorded as the workflow's author and is the identity every run publishes
// under. The gateway fills it from the authenticated caller; this package never
// invents one. An update never reassigns it.
//
// It may be zero. The operator token is a credential and not an account, so a
// workflow made through it has no owner: it is governed by the global
// permission alone and is not removed when any account is.
func (s *Store) CreateWorkflow(ctx context.Context, in automation.WorkflowInput) (automation.Workflow, error) {
	if in.ManagedChannelID <= 0 {
		return automation.Workflow{}, fmt.Errorf("%w: managed_channel_id must be a positive local channel id",
			ErrInvalidWorkflow)
	}
	enabled, err := validateWorkflowDestinations(in.Destinations)
	if err != nil {
		return automation.Workflow{}, err
	}
	// A workflow is stored disabled until its configuration is complete: an
	// enabled one must already name somewhere to publish, or every message
	// would run a workflow that does nothing and reports itself as done.
	if in.Enabled && enabled == 0 {
		return automation.Workflow{}, fmt.Errorf("%w: a workflow needs at least one enabled destination "+
			"before it can be enabled", ErrInvalidWorkflow)
	}

	// The source channel's registration is the authority on which account a
	// workflow runs under. It is read here rather than taken from the request:
	// a workflow is one per user-owned managed channel registration, and a
	// caller who could name the account would be able to publish as somebody
	// else's Telegram identity. An account id the caller did supply is treated
	// as a claim and checked against the registration, never believed.
	var sourceOwner sql.NullInt64
	var sourceAccount string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(a.owner_user_id, 0), COALESCE(mc.account_id, '')
		 FROM managed_channels mc
		 LEFT JOIN telegram_accounts a ON a.id = mc.account_id
		 WHERE mc.id = ?`, in.ManagedChannelID).Scan(&sourceOwner, &sourceAccount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return automation.Workflow{}, fmt.Errorf("%w: no managed channel has id %d",
				ErrInvalidWorkflow, in.ManagedChannelID)
		}
		return automation.Workflow{}, fmt.Errorf("channelstore: check workflow source: %w", err)
	}
	if sourceAccount == "" {
		// A channel nobody owns is a legacy registration. There is no session
		// a workflow may legitimately publish through, so it stays unwritten
		// rather than being attached to whichever account happens to exist.
		return automation.Workflow{}, fmt.Errorf(
			"%w: that channel is not registered under a personal account", ErrInvalidWorkflow)
	}
	if in.AccountID != "" && in.AccountID != sourceAccount {
		// The channel is registered under a different account. Refusing is what
		// keeps one user's workflow from being attached to another's channel.
		return automation.Workflow{}, fmt.Errorf(
			"%w: that channel is not registered under this account", ErrInvalidWorkflow)
	}
	in.AccountID = sourceAccount
	if in.CreatedBy > 0 && sourceOwner.Int64 != 0 && sourceOwner.Int64 != in.CreatedBy {
		return automation.Workflow{}, fmt.Errorf(
			"%w: that channel belongs to another account", ErrInvalidWorkflow)
	}
	ownerUserID := in.CreatedBy
	if sourceOwner.Int64 != 0 {
		ownerUserID = sourceOwner.Int64
	}

	// Every Telegram destination must be one this same user owns, on an account
	// they hold. A workflow may publish across two of its owner's accounts, and
	// never to an account belonging to somebody else: a destination is the one
	// place a background run picks up a Telegram identity that is not its own.
	for _, d := range in.Destinations {
		if d.DestinationType != string(automation.DestinationTelegram) {
			continue
		}
		if err := s.requireOwnedDestination(ctx, ownerUserID, d); err != nil {
			return automation.Workflow{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return automation.Workflow{}, fmt.Errorf("channelstore: create workflow: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now()
	res, err := tx.ExecContext(ctx, `INSERT INTO workflows (
	managed_channel_id, enabled, ai_enabled, ai_prompt, ai_send_images, ai_publish_on_failure,
	config_revision, created_by, created_at, updated_at, owner_user_id, account_id)
	VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		in.ManagedChannelID, boolInt(in.Enabled), boolInt(in.AIEnabled), in.AIPrompt,
		boolInt(in.AISendImages), boolInt(in.AIPublishOnFailure), nullableUserID(in.CreatedBy),
		formatTime(now), formatTime(now),
		nullableOwnerID(ownerUserID), nullableText(in.AccountID))
	if err != nil {
		if isUniqueViolation(err) {
			return automation.Workflow{}, ErrWorkflowExists
		}
		return automation.Workflow{}, fmt.Errorf("channelstore: create workflow: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return automation.Workflow{}, fmt.Errorf("channelstore: create workflow id: %w", err)
	}
	if err := replaceWorkflowDestinations(ctx, tx, id, now, in.Destinations); err != nil {
		return automation.Workflow{}, err
	}
	if err := tx.Commit(); err != nil {
		return automation.Workflow{}, fmt.Errorf("channelstore: create workflow: %w", err)
	}
	return s.getWorkflowOrReport(ctx, id)
}

// requireOwnedDestination refuses an unresolved, unregistered, or foreign
// Telegram destination. AccountID is an explicit claim and must match the exact
// managed registration; server key and channel alone never select an account.
func (s *Store) requireOwnedDestination(ctx context.Context, ownerUserID int64,
	d automation.WorkflowDestination) error {

	if ownerUserID <= 0 || strings.TrimSpace(d.AccountID) == "" {
		return fmt.Errorf("%w: the Telegram destination needs an explicit owned account", ErrInvalidWorkflow)
	}
	var destOwner sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT a.owner_user_id
		 FROM managed_channels mc
		 JOIN telegram_accounts a ON a.id = mc.account_id
		 WHERE mc.client_key = ? AND mc.channel_id = ? AND mc.account_id = ?`,
		d.ClientKey, d.ChannelID, d.AccountID).Scan(&destOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: the selected account does not own a registered destination", ErrInvalidWorkflow)
	}
	if err != nil {
		return fmt.Errorf("channelstore: check workflow destination: %w", err)
	}
	if !destOwner.Valid || destOwner.Int64 != ownerUserID {
		return fmt.Errorf("%w: that destination belongs to another account", ErrInvalidWorkflow)
	}
	return nil
}

// AccountOwner resolves the owner of one exact account identity. A missing,
// quarantined, or ownerless legacy account is not a personal destination.
func (s *Store) AccountOwner(ctx context.Context, accountID string) (int64, bool, error) {
	if strings.TrimSpace(accountID) == "" {
		return 0, false, nil
	}
	var owner sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT owner_user_id FROM telegram_accounts WHERE id = ?`, accountID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !owner.Valid) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("channelstore: resolve account owner: %w", err)
	}
	return owner.Int64, true, nil
}

// replaceWorkflowDestinations writes one workflow's whole target list.
//
// The list is replaced rather than patched: what a run publishes is read from
// these rows, so keeping a destination the configuration dropped would publish
// somewhere the operator no longer named.
func replaceWorkflowDestinations(ctx context.Context, tx *sql.Tx, workflowID int64, now time.Time,
	dests []automation.WorkflowDestination) error {

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM workflow_destinations WHERE workflow_id = ?`, workflowID); err != nil {
		return fmt.Errorf("channelstore: clear workflow destinations: %w", err)
	}
	for i, d := range dests {
		options, err := encodeOptions(d.Options)
		if err != nil {
			return err
		}
		position := d.Position
		if position == 0 {
			position = i
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_destinations (
	workflow_id, destination_type, client_key, channel_id, site_id, account_id, options, position, enabled,
	created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			workflowID, d.DestinationType, strings.TrimSpace(d.ClientKey), d.ChannelID, d.SiteID,
			nullableText(d.AccountID),
			options, position, boolInt(d.Enabled), formatTime(now), formatTime(now)); err != nil {
			return fmt.Errorf("channelstore: store workflow destination: %w", err)
		}
	}
	return nil
}

// UpdateWorkflow replaces one workflow's configuration and bumps its revision.
//
// The revision is what a run records, so history says which configuration
// produced it even after the workflow has been edited again.
func (s *Store) UpdateWorkflow(ctx context.Context, id int64, in automation.WorkflowInput) (automation.Workflow, bool, error) {
	if in.ManagedChannelID <= 0 {
		return automation.Workflow{}, false, fmt.Errorf("%w: managed_channel_id must be a positive local channel id",
			ErrInvalidWorkflow)
	}
	enabled, err := validateWorkflowDestinations(in.Destinations)
	if err != nil {
		return automation.Workflow{}, false, err
	}
	if in.Enabled && enabled == 0 {
		return automation.Workflow{}, false, fmt.Errorf("%w: a workflow needs at least one enabled destination "+
			"before it can be enabled", ErrInvalidWorkflow)
	}

	// The ownership binding is read, checked and never rewritten. An update
	// changes what a run does and never who it runs as: moving a workflow onto
	// another account's channel would hand one user's automation another's
	// Telegram identity.
	existing, found, err := s.GetWorkflow(ctx, id)
	if err != nil {
		return automation.Workflow{}, false, err
	}
	if !found {
		return automation.Workflow{}, false, ErrWorkflowNotFound
	}
	if in.AccountID != "" && in.AccountID != existing.AccountID {
		return automation.Workflow{}, false, fmt.Errorf(
			"%w: a workflow's account cannot be reassigned", ErrInvalidWorkflow)
	}
	for _, d := range in.Destinations {
		if d.DestinationType != string(automation.DestinationTelegram) {
			continue
		}
		if err := s.requireOwnedDestination(ctx, existing.OwnerUserID, d); err != nil {
			return automation.Workflow{}, false, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return automation.Workflow{}, false, fmt.Errorf("channelstore: update workflow: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now()
	res, err := tx.ExecContext(ctx, `UPDATE workflows SET
	managed_channel_id = ?, enabled = ?, ai_enabled = ?, ai_prompt = ?, ai_send_images = ?,
	ai_publish_on_failure = ?, config_revision = config_revision + 1, updated_at = ? WHERE id = ?`,
		in.ManagedChannelID, boolInt(in.Enabled), boolInt(in.AIEnabled), in.AIPrompt,
		boolInt(in.AISendImages), boolInt(in.AIPublishOnFailure), formatTime(now), id)
	if err != nil {
		if isUniqueViolation(err) {
			return automation.Workflow{}, false, ErrWorkflowExists
		}
		return automation.Workflow{}, false, fmt.Errorf("channelstore: update workflow: %w", err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return automation.Workflow{}, false, ErrWorkflowNotFound
	}
	if err := replaceWorkflowDestinations(ctx, tx, id, now, in.Destinations); err != nil {
		return automation.Workflow{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return automation.Workflow{}, false, fmt.Errorf("channelstore: update workflow: %w", err)
	}
	updated, err := s.getWorkflowOrReport(ctx, id)
	if err != nil {
		return automation.Workflow{}, false, err
	}
	return updated, true, nil
}

// getWorkflowOrReport reads one workflow or reports it as missing.
func (s *Store) getWorkflowOrReport(ctx context.Context, id int64) (automation.Workflow, error) {
	wf, found, err := s.GetWorkflow(ctx, id)
	if err != nil {
		return automation.Workflow{}, err
	}
	if !found {
		return automation.Workflow{}, ErrWorkflowNotFound
	}
	return wf, nil
}

// GetWorkflow reads one workflow and its destinations.
func (s *Store) GetWorkflow(ctx context.Context, id int64) (automation.Workflow, bool, error) {
	wf, err := scanWorkflowRow(s.db.QueryRowContext(ctx,
		`SELECT `+workflowColumns+` FROM workflows w WHERE w.id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.Workflow{}, false, nil
	}
	if err != nil {
		return automation.Workflow{}, false, fmt.Errorf("channelstore: get workflow: %w", err)
	}
	wf, err = s.withDestinations(ctx, wf)
	if err != nil {
		return automation.Workflow{}, false, err
	}
	return wf, true, nil
}

// WorkflowByChannel reads the workflow attached to one source channel.
func (s *Store) WorkflowByChannel(ctx context.Context, managedChannelID int64) (automation.Workflow, bool, error) {
	wf, err := scanWorkflowRow(s.db.QueryRowContext(ctx,
		`SELECT `+workflowColumns+` FROM workflows w WHERE w.managed_channel_id = ?`, managedChannelID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return automation.Workflow{}, false, nil
	}
	if err != nil {
		return automation.Workflow{}, false, fmt.Errorf("channelstore: get workflow by channel: %w", err)
	}
	wf, err = s.withDestinations(ctx, wf)
	if err != nil {
		return automation.Workflow{}, false, err
	}
	return wf, true, nil
}

// WorkflowIDForChannel answers the queue's question: which workflow reacts to
// one source channel. It satisfies automation.WorkflowLookup, and "none
// configured" is a normal answer rather than an error.
func (s *Store) WorkflowIDForChannel(ctx context.Context, managedChannelID int64) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM workflows WHERE managed_channel_id = ?`, managedChannelID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("channelstore: workflow for channel: %w", err)
	}
	return id, true, nil
}

// WorkflowAuthor reports the account a workflow publishes under.
//
// It is the account that created the workflow and it is never reassigned: a run
// resolves that account's permissions again at execution time, so a workflow
// never publishes with more authority than the person who configured it has
// right now.
func (s *Store) WorkflowAuthor(ctx context.Context, workflowID int64) (int64, bool, error) {
	// Nullable: a workflow made through the operator token has no owning
	// account. found still reports that the row exists; the id is zero to say
	// nobody owns it.
	var createdBy sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT created_by FROM workflows WHERE id = ?`, workflowID).Scan(&createdBy)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("channelstore: workflow author: %w", err)
	}
	return createdBy.Int64, true, nil
}

// ListWorkflows lists one page of workflows.
//
// The query matches the workflow id or the source channel's name, so an
// operator can find a workflow by the channel it watches.
//
// opts.Visible is the caller's own visibility restriction: a workflow belongs to
// exactly one source channel and is listed only with it. As in ListWebhooks it
// decides which rows are in the collection at all, so the whole matching set is
// read before a page is cut and total counts what the caller may see.
func (s *Store) ListWorkflows(ctx context.Context, opts automation.WorkflowListOptions) (automation.WorkflowPage, error) {
	conds := []string{"1 = 1"}
	args := []any{}
	// A workflow collection is one tenant's. Scoping before the page is cut is
	// what keeps the total honest and keeps one page from being everybody's.
	if opts.OwnerUserID > 0 {
		conds = append(conds, "w.owner_user_id = ?")
		args = append(args, opts.OwnerUserID)
	}
	if opts.Enabled != nil {
		conds = append(conds, "w.enabled = ?")
		args = append(args, boolInt(*opts.Enabled))
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		conds = append(conds,
			"(CAST(w.id AS TEXT) LIKE ? ESCAPE '\\' OR c.title LIKE ? ESCAPE '\\' OR c.username LIKE ? ESCAPE '\\')")
		like := "%" + escapeLike(q) + "%"
		args = append(args, like, like, like)
	}
	where := " WHERE " + strings.Join(conds, " AND ")

	if opts.Visible != nil {
		all, _, err := s.readWorkflows(ctx, where, args, -1, 0)
		if err != nil {
			return automation.WorkflowPage{}, err
		}
		kept := make([]automation.Workflow, 0, len(all))
		for _, wf := range all {
			if opts.Visible(wf.ManagedChannelID) {
				kept = append(kept, wf)
			}
		}
		limit, offset := pageBounds(opts.Page, opts.PerPage, 50)
		page := kept
		if offset < len(page) {
			page = page[offset:]
		} else {
			page = nil
		}
		if len(page) > limit {
			page = page[:limit]
		}
		if page == nil {
			page = []automation.Workflow{}
		}
		if err := s.fillWorkflowLastRuns(ctx, page); err != nil {
			return automation.WorkflowPage{}, err
		}
		return automation.WorkflowPage{Workflows: page, Total: len(kept)}, nil
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workflows w LEFT JOIN managed_channels c ON c.id = w.managed_channel_id`+where,
		args...).Scan(&total); err != nil {
		return automation.WorkflowPage{}, fmt.Errorf("channelstore: count workflows: %w", err)
	}

	limit, offset := pageBounds(opts.Page, opts.PerPage, 50)
	out, _, err := s.readWorkflows(ctx, where, args, limit, offset)
	if err != nil {
		return automation.WorkflowPage{}, err
	}
	if err := s.fillWorkflowLastRuns(ctx, out); err != nil {
		return automation.WorkflowPage{}, err
	}
	return automation.WorkflowPage{Workflows: out, Total: total}, nil
}

// readWorkflows reads matching workflows with their destinations. A negative
// limit reads the whole matching set.
func (s *Store) readWorkflows(ctx context.Context, where string, args []any,
	limit, offset int) ([]automation.Workflow, []int64, error) {

	query := `SELECT ` + workflowColumns + ` FROM workflows w
	LEFT JOIN managed_channels c ON c.id = w.managed_channel_id` + where +
		` ORDER BY w.created_at DESC, w.id DESC`
	callArgs := args
	if limit >= 0 {
		query += ` LIMIT ? OFFSET ?`
		callArgs = append(append([]any{}, args...), limit, offset)
	}
	rows, err := s.db.QueryContext(ctx, query, callArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("channelstore: list workflows: %w", err)
	}
	defer rows.Close()

	out := []automation.Workflow{}
	ids := []int64{}
	for rows.Next() {
		wf, err := scanWorkflowRow(rows.Scan)
		if err != nil {
			return nil, nil, fmt.Errorf("channelstore: list workflows: %w", err)
		}
		out = append(out, wf)
		ids = append(ids, wf.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("channelstore: list workflows: %w", err)
	}
	for i := range out {
		filled, err := s.withDestinations(ctx, out[i])
		if err != nil {
			return nil, nil, err
		}
		out[i] = filled
	}
	return out, ids, nil
}

// fillWorkflowLastRuns adds the "last run" cell to a page of workflows. A
// workflow with no history yet keeps a nil summary rather than an empty one.
func (s *Store) fillWorkflowLastRuns(ctx context.Context, workflows []automation.Workflow) error {
	if len(workflows) == 0 {
		return nil
	}
	ids := make([]any, 0, len(workflows))
	marks := make([]string, 0, len(workflows))
	for _, wf := range workflows {
		ids = append(ids, wf.ID)
		marks = append(marks, "?")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workflow_id, id, status, created_at, completed_at
	FROM workflow_runs WHERE workflow_id IN (`+strings.Join(marks, ",")+`)
	ORDER BY workflow_id, created_at DESC, id DESC`, ids...)
	if err != nil {
		return fmt.Errorf("channelstore: read last workflow runs: %w", err)
	}
	defer rows.Close()

	seen := map[int64]bool{}
	for rows.Next() {
		var (
			workflowID  int64
			summary     automation.WorkflowRunSummary
			createdAt   string
			completedAt sql.NullString
		)
		if err := rows.Scan(&workflowID, &summary.ID, &summary.Status, &createdAt, &completedAt); err != nil {
			return fmt.Errorf("channelstore: read last workflow run: %w", err)
		}
		if seen[workflowID] {
			continue
		}
		seen[workflowID] = true
		summary.CreatedAt = parseTime(createdAt)
		if completedAt.Valid {
			t := parseTime(completedAt.String)
			summary.CompletedAt = &t
		}
		for i := range workflows {
			if workflows[i].ID == workflowID {
				last := summary
				workflows[i].LastRun = &last
			}
		}
	}
	return rows.Err()
}

// DeleteWorkflow removes one workflow. Its destinations and its run history go
// with it, through the schema's cascades; the source channel is untouched.
func (s *Store) DeleteWorkflow(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM workflows WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("channelstore: delete workflow: %w", err)
	}
	changed, _ := res.RowsAffected()
	return changed > 0, nil
}
