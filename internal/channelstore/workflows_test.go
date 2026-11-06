package channelstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
)

// A channel carries at most one workflow. That is a schema rule and an
// application rule at once: a second workflow for the same source would make
// one incoming message publish twice, and no code path may create one.

// wfEnv is a store with one account and one managed source channel, which is
// the smallest world a workflow can exist in.
type wfEnv struct {
	store   *channelstore.Store
	user    channelstore.User
	channel channelstore.Channel
}

func newWFEnv(t *testing.T) *wfEnv {
	t.Helper()
	store := open(t)
	ctx := context.Background()
	user, err := store.CreateUser(ctx, "Workflow Owner", "wf-owner", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	row, err := store.Upsert(ctx, channel("official", 555))
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return &wfEnv{store: store, user: user, channel: row}
}

// telegramDest is one configured Telegram destination. channel_id is the
// Telegram peer id, exactly as the publisher wants it.
func telegramDest(channelID int64) automation.WorkflowDestination {
	return automation.WorkflowDestination{
		DestinationType: string(automation.DestinationTelegram),
		ClientKey:       "official",
		ChannelID:       channelID,
		Enabled:         true,
		Options:         map[string]any{"silent": true},
	}
}

// input is a complete workflow configuration on this environment's channel.
func (e *wfEnv) input() automation.WorkflowInput {
	return automation.WorkflowInput{
		ManagedChannelID: e.channel.ID,
		CreatedBy:        e.user.ID,
		Destinations:     []automation.WorkflowDestination{telegramDest(555)},
	}
}

func TestCreateWorkflowStoresTheConfiguration(t *testing.T) {
	env := newWFEnv(t)
	wf, err := env.store.CreateWorkflow(context.Background(), env.input())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if wf.ID == 0 || wf.ManagedChannelID != env.channel.ID {
		t.Fatalf("workflow = %+v", wf)
	}
	if wf.ConfigRevision != 1 {
		t.Fatalf("config revision = %d, want 1", wf.ConfigRevision)
	}
	if wf.CreatedAt.IsZero() || wf.UpdatedAt.IsZero() {
		t.Fatalf("timestamps = %+v", wf)
	}
	if len(wf.Destinations) != 1 || wf.Destinations[0].ClientKey != "official" ||
		wf.Destinations[0].ChannelID != 555 {
		t.Fatalf("destinations = %+v", wf.Destinations)
	}
	if silent, ok := wf.Destinations[0].Options["silent"].(bool); !ok || !silent {
		t.Fatalf("options = %+v", wf.Destinations[0].Options)
	}
}

// The schema refuses a second workflow for one channel; the store answers with
// the conflict error the gateway maps onto a 409, never with a raw SQL error.
func TestCreateWorkflowRefusesASecondWorkflowForOneChannel(t *testing.T) {
	env := newWFEnv(t)
	if _, err := env.store.CreateWorkflow(context.Background(), env.input()); err != nil {
		t.Fatalf("first CreateWorkflow: %v", err)
	}
	_, err := env.store.CreateWorkflow(context.Background(), env.input())
	if !errors.Is(err, automation.ErrConflict) {
		t.Fatalf("second CreateWorkflow error = %v, want an automation.ErrConflict", err)
	}
}

// A workflow is stored disabled until its configuration is complete, and an
// enabled one must already name somewhere to publish.
func TestCreateWorkflowRefusesAnEnabledWorkflowWithoutDestinations(t *testing.T) {
	env := newWFEnv(t)
	draft := automation.WorkflowInput{
		ManagedChannelID: env.channel.ID, CreatedBy: env.user.ID, Enabled: true,
	}
	if _, err := env.store.CreateWorkflow(context.Background(), draft); !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("error = %v, want an automation.ErrInvalid", err)
	}

	draft.Enabled = false
	wf, err := env.store.CreateWorkflow(context.Background(), draft)
	if err != nil {
		t.Fatalf("disabled draft: %v", err)
	}
	if wf.Enabled {
		t.Fatal("a workflow with no destination must be stored disabled")
	}
}

func TestCreateWorkflowKeepsTheAIDefaults(t *testing.T) {
	env := newWFEnv(t)
	wf, err := env.store.CreateWorkflow(context.Background(), env.input())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if wf.AIEnabled || wf.AISendImages || wf.AIPublishOnFailure || wf.AIPrompt != "" {
		t.Fatalf("AI defaults = %+v", wf)
	}
}

// A workflow made through the operator token has no owning account: the
// operator is a credential, not a person. It is stored without one rather than
// refused, and no account's deletion takes it with it.
func TestCreateWorkflowAcceptsOneWithNoOwningAccount(t *testing.T) {
	env := newWFEnv(t)
	in := env.input()
	in.CreatedBy = 0
	wf, err := env.store.CreateWorkflow(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if author, found, err := env.store.WorkflowAuthor(context.Background(), wf.ID); err != nil || !found || author != 0 {
		t.Fatalf("author = %d found=%v err=%v, want an unowned workflow read back as zero", author, found, err)
	}
}

// A destination names exactly one kind of target. The schema refuses a row
// that mixes them, and so does the store, before the database is reached.
func TestCreateWorkflowRefusesADestinationThatMixesTypes(t *testing.T) {
	env := newWFEnv(t)
	mixed := env.input()
	mixed.Destinations[0].SiteID = 4
	if _, err := env.store.CreateWorkflow(context.Background(), mixed); !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("mixed destination error = %v, want an automation.ErrInvalid", err)
	}

	wordpress := automation.WorkflowInput{
		ManagedChannelID: env.channel.ID,
		CreatedBy:        env.user.ID,
		Destinations: []automation.WorkflowDestination{{
			DestinationType: string(automation.DestinationWordPress),
			SiteID:          1,
			Enabled:         true,
		}},
	}
	if _, err := env.store.CreateWorkflow(context.Background(), wordpress); err != nil {
		t.Fatalf("wordpress destination: %v", err)
	}
}

func TestCreateWorkflowRefusesAnUnknownSourceChannel(t *testing.T) {
	env := newWFEnv(t)
	in := env.input()
	in.ManagedChannelID = env.channel.ID + 999
	if _, err := env.store.CreateWorkflow(context.Background(), in); !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("error = %v, want an automation.ErrInvalid", err)
	}
}

func TestUpdateWorkflowBumpsTheConfigRevisionAndReplacesDestinations(t *testing.T) {
	env := newWFEnv(t)
	ctx := context.Background()
	wf, err := env.store.CreateWorkflow(ctx, env.input())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	update := env.input()
	update.Enabled = true
	update.AIEnabled = true
	update.AIPrompt = "polish it"
	update.CreatedBy = 4242 // an update must never reassign the author
	update.Destinations = []automation.WorkflowDestination{{
		DestinationType: string(automation.DestinationWordPress),
		SiteID:          7,
		Enabled:         true,
		Position:        3,
	}}

	updated, found, err := env.store.UpdateWorkflow(ctx, wf.ID, update)
	if err != nil || !found {
		t.Fatalf("UpdateWorkflow: found=%v err=%v", found, err)
	}
	if updated.ConfigRevision != 2 {
		t.Fatalf("config revision = %d, want 2", updated.ConfigRevision)
	}
	if !updated.Enabled || !updated.AIEnabled || updated.AIPrompt != "polish it" {
		t.Fatalf("updated = %+v", updated)
	}
	if len(updated.Destinations) != 1 || updated.Destinations[0].SiteID != 7 {
		t.Fatalf("destinations = %+v, want the replacement only", updated.Destinations)
	}

	// The author stays the creator's: the workflow is still theirs, and goes
	// away with them, however the update was labelled.
	if err := env.store.DeleteUser(ctx, env.user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, found, err := env.store.GetWorkflow(ctx, wf.ID); err != nil || found {
		t.Fatalf("a workflow must follow its author: found=%v err=%v", found, err)
	}
}

func TestUpdateWorkflowReportsAMissingWorkflow(t *testing.T) {
	env := newWFEnv(t)
	_, found, err := env.store.UpdateWorkflow(context.Background(), 4044, env.input())
	if found || !errors.Is(err, automation.ErrNotFound) {
		t.Fatalf("found=%v err=%v, want a missing workflow", found, err)
	}
}

func TestGetWorkflowAndWorkflowByChannelAgree(t *testing.T) {
	env := newWFEnv(t)
	ctx := context.Background()
	created, err := env.store.CreateWorkflow(ctx, env.input())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	byID, found, err := env.store.GetWorkflow(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("GetWorkflow: found=%v err=%v", found, err)
	}
	byChannel, found, err := env.store.WorkflowByChannel(ctx, env.channel.ID)
	if err != nil || !found {
		t.Fatalf("WorkflowByChannel: found=%v err=%v", found, err)
	}
	if byID.ID != created.ID || byChannel.ID != created.ID {
		t.Fatalf("ids = %d, %d, want %d", byID.ID, byChannel.ID, created.ID)
	}
	if _, found, err := env.store.WorkflowByChannel(ctx, env.channel.ID+1); err != nil || found {
		t.Fatalf("an unconfigured channel: found=%v err=%v", found, err)
	}
}

// The queue asks for the one workflow that reacts to a source channel, so the
// answer is an id and the missing case is "not configured", not an error.
func TestWorkflowIDForChannelReportsTheSingleWorkflow(t *testing.T) {
	env := newWFEnv(t)
	ctx := context.Background()
	id, found, err := env.store.WorkflowIDForChannel(ctx, env.channel.ID)
	if err != nil || found {
		t.Fatalf("unconfigured: id=%d found=%v err=%v", id, found, err)
	}
	created, err := env.store.CreateWorkflow(ctx, env.input())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	id, found, err = env.store.WorkflowIDForChannel(ctx, env.channel.ID)
	if err != nil || !found || id != created.ID {
		t.Fatalf("id=%d found=%v err=%v, want %d", id, found, err, created.ID)
	}
}

func TestListWorkflowsPagesAndFilters(t *testing.T) {
	env := newWFEnv(t)
	ctx := context.Background()
	other, err := env.store.Upsert(ctx, channel("official", 777))
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := env.store.CreateWorkflow(ctx, env.input()); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	second := env.input()
	second.ManagedChannelID = other.ID
	second.Enabled = true
	if _, err := env.store.CreateWorkflow(ctx, second); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	page, err := env.store.ListWorkflows(ctx, automation.WorkflowListOptions{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	if page.Total != 2 || len(page.Workflows) != 2 {
		t.Fatalf("page = %+v", page)
	}
	for _, wf := range page.Workflows {
		if wf.ID == 0 || len(wf.Destinations) != 1 {
			t.Fatalf("workflow row = %+v", wf)
		}
	}

	enabled := true
	only, err := env.store.ListWorkflows(ctx, automation.WorkflowListOptions{Enabled: &enabled})
	if err != nil {
		t.Fatalf("ListWorkflows enabled: %v", err)
	}
	if only.Total != 1 || len(only.Workflows) != 1 {
		t.Fatalf("enabled page = %+v", only)
	}

	first, err := env.store.ListWorkflows(ctx, automation.WorkflowListOptions{Page: 1, PerPage: 1})
	if err != nil {
		t.Fatalf("ListWorkflows page: %v", err)
	}
	if len(first.Workflows) != 1 || first.Total != 2 {
		t.Fatalf("first page = %+v", first)
	}
}

func TestDeleteWorkflowRemovesTheWorkflowAndItsDestinations(t *testing.T) {
	env := newWFEnv(t)
	ctx := context.Background()
	wf, err := env.store.CreateWorkflow(ctx, env.input())
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	deleted, err := env.store.DeleteWorkflow(ctx, wf.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteWorkflow: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := env.store.GetWorkflow(ctx, wf.ID); err != nil || found {
		t.Fatalf("after delete: found=%v err=%v", found, err)
	}
	// The channel is free for a new workflow again.
	if _, err := env.store.CreateWorkflow(ctx, env.input()); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if deleted, err := env.store.DeleteWorkflow(ctx, wf.ID); err != nil || deleted {
		t.Fatalf("second DeleteWorkflow: deleted=%v err=%v", deleted, err)
	}
}
