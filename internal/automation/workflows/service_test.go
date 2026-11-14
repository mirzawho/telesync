package workflows_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/workflows"
	"github.com/mirza/telesync/internal/channelstore"
)

// The workflow configuration is ordinary data, and this is where it is kept
// honest: one workflow per channel, and a WordPress taxonomy selection that
// belongs to the site it publishes to.

// fakeTerms confirms the term ids it is told to, and refuses the rest.
type fakeTerms struct {
	calls    []string
	refuseID int
}

func (f *fakeTerms) VerifyTerms(_ context.Context, siteID int64, taxonomy string, termIDs []int) error {
	f.calls = append(f.calls, fmt.Sprintf("%d/%s/%v", siteID, taxonomy, termIDs))
	for _, id := range termIDs {
		if id == f.refuseID {
			return fmt.Errorf("%w: term %d is not one of this site's %s", automation.ErrInvalid, id, taxonomy)
		}
	}
	return nil
}

// configWorld is a store with one account and one source channel, and the
// workflow service wired to it.
type configWorld struct {
	store   *channelstore.Store
	user    channelstore.User
	account string
	channel channelstore.Channel
	terms   *fakeTerms
	service *workflows.Service
}

func newConfigWorld(t *testing.T) *configWorld {
	t.Helper()
	ctx := context.Background()
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state", "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	user, err := store.CreateUser(ctx, "Owner", "owner", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// A workflow names the account its source channel is registered under, so
	// the fixture creates one and registers the channel through it.
	accID, err := accountid.New()
	if err != nil {
		t.Fatalf("accountid: %v", err)
	}
	if _, err := store.Create(ctx, channelstore.Account{
		ID: accID, OwnerUserID: user.ID, ServerKey: "source",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 1,
		Status: channelstore.AccountStatusConnected,
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	channel, err := store.UpsertFor(ctx, accID, channelstore.Channel{
		ClientKey: "source", ChannelID: 555, PeerType: channelstore.PeerChannel,
		Title: "Source", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("UpsertFor: %v", err)
	}
	terms := &fakeTerms{}
	return &configWorld{
		store: store, user: user, account: accID, channel: channel, terms: terms,
		service: &workflows.Service{Store: store, Terms: terms},
	}
}

// wordpressInput is a workflow publishing to one WordPress site.
func (c *configWorld) wordpressInput(options map[string]any) automation.WorkflowInput {
	return automation.WorkflowInput{
		ManagedChannelID: c.channel.ID,
		AccountID:        c.account,
		CreatedBy:        c.user.ID,
		Destinations: []automation.WorkflowDestination{{
			DestinationType: string(automation.DestinationWordPress),
			SiteID:          3,
			Enabled:         true,
			Options:         options,
		}},
	}
}

func TestASecondWorkflowForOneChannelIsRefused(t *testing.T) {
	c := newConfigWorld(t)
	ctx := context.Background()
	in := automation.WorkflowInput{
		ManagedChannelID: c.channel.ID, AccountID: c.account, CreatedBy: c.user.ID,
		Destinations: []automation.WorkflowDestination{{
			DestinationType: string(automation.DestinationTelegram),
			ClientKey:       "official", ChannelID: 777, AccountID: c.account, Enabled: true,
		}},
	}
	if _, err := c.store.UpsertFor(ctx, c.account, channelstore.Channel{
		ClientKey: "official", ChannelID: 777, PeerType: channelstore.PeerChannel,
		Title: "Destination", MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatalf("register destination: %v", err)
	}
	if _, err := c.service.CreateWorkflow(ctx, in); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	_, err := c.service.CreateWorkflow(ctx, in)
	if !errors.Is(err, automation.ErrConflict) {
		t.Fatalf("second CreateWorkflow = %v, want a conflict", err)
	}
}

// A WordPress term id is one the destination site already has. A workflow never
// invents one, and never stores one the site does not report.
func TestAWorkflowRefusesAWordPressTermTheSiteDoesNotHave(t *testing.T) {
	c := newConfigWorld(t)
	c.terms.refuseID = 404

	_, err := c.service.CreateWorkflow(context.Background(), c.wordpressInput(map[string]any{
		"category_ids": []any{float64(404)},
	}))
	if !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("error = %v, want the unknown term refused", err)
	}
	page, err := c.store.ListWorkflows(context.Background(), automation.WorkflowListOptions{})
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("workflows = %d, want none stored", page.Total)
	}
}

func TestAWorkflowTakesATermTheSiteReports(t *testing.T) {
	c := newConfigWorld(t)
	c.terms.refuseID = -1

	wf, err := c.service.CreateWorkflow(context.Background(), c.wordpressInput(map[string]any{
		"category_ids": []any{float64(7)},
		"tag_ids":      []any{float64(11), float64(12)},
		"thumbnail":    "source",
	}))
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if len(wf.Destinations) != 1 {
		t.Fatalf("destinations = %+v", wf.Destinations)
	}
	// Both taxonomies are confirmed against the site, and only once each.
	if len(c.terms.calls) != 2 {
		t.Fatalf("verifications = %v, want one per taxonomy", c.terms.calls)
	}
	if c.terms.calls[0] != "3/categories/[7]" || c.terms.calls[1] != "3/tags/[11 12]" {
		t.Fatalf("verifications = %v", c.terms.calls)
	}
}

// A term id is a number, not something to guess at: a value that is not one is
// refused before the site is even asked.
func TestAWorkflowRefusesATermIDThatIsNotANumber(t *testing.T) {
	c := newConfigWorld(t)
	_, err := c.service.CreateWorkflow(context.Background(), c.wordpressInput(map[string]any{
		"tag_ids": []any{"technology"},
	}))
	if !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("error = %v, want a refusal", err)
	}
	if len(c.terms.calls) != 0 {
		t.Fatalf("verifications = %v, want none for a value that is not a term id", c.terms.calls)
	}
}

// A destination that names no terms needs no confirmation, so a deployment
// without a verifier can still publish text.
func TestAWorkflowWithNoTermsDoesNotNeedAVerifier(t *testing.T) {
	c := newConfigWorld(t)
	c.service.Terms = nil

	if _, err := c.service.CreateWorkflow(context.Background(), c.wordpressInput(map[string]any{
		"thumbnail": "source",
	})); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	// One that does name a term is refused rather than stored unverified.
	_, err := c.service.CreateWorkflow(context.Background(), c.wordpressInput(map[string]any{
		"tag_ids": []any{float64(3)},
	}))
	if !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("error = %v, want a refusal without a verifier", err)
	}
}
