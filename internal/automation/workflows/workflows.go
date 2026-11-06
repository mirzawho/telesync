// Package workflows is the workflow configuration the gateway manages and the
// engine that runs one workflow when a source message arrives.
//
// A workflow is one automation per managed channel: what to publish, where to
// publish it, and whether an AI polish is in the way. The configuration is
// ordinary data — this package validates it and refuses anything it cannot
// confirm — and the engine is where a queued run turns into publications.
//
// Two rules run through both halves:
//
//   - At most one workflow per managed channel, so one message publishes once.
//   - Nothing is invented. A WordPress term id is one the destination site
//     already has, a destination is one the account may name, and a run
//     publishes as the account that configured the workflow, with that
//     account's permissions as they are at execution time.
package workflows

import (
	"context"
	"errors"
	"fmt"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
)

// WorkflowStore is the durable side of a workflow: its configuration, its
// destinations and its run history.
type WorkflowStore interface {
	ListWorkflows(ctx context.Context, opts automation.WorkflowListOptions) (automation.WorkflowPage, error)
	GetWorkflow(ctx context.Context, id int64) (automation.Workflow, bool, error)
	CreateWorkflow(ctx context.Context, in automation.WorkflowInput) (automation.Workflow, error)
	UpdateWorkflow(ctx context.Context, id int64, in automation.WorkflowInput) (automation.Workflow, bool, error)
	DeleteWorkflow(ctx context.Context, id int64) (bool, error)
	ListRuns(ctx context.Context, workflowID int64, opts automation.RunListOptions) (automation.WorkflowRunPage, error)
	GetRun(ctx context.Context, workflowID, runID int64) (automation.WorkflowRun, bool, error)
}

// TermVerifier confirms that a WordPress destination's taxonomy selection
// belongs to the site it publishes to. *publishing.Service implements it.
type TermVerifier interface {
	VerifyTerms(ctx context.Context, siteID int64, taxonomy string, termIDs []int) error
}

// Service is the workflow configuration and run history the gateway calls.
//
// Authorization is decided before these methods are reached: the gateway checks
// the caller's permissions, and this service keeps the configuration honest.
type Service struct {
	Store WorkflowStore
	// Terms verifies a WordPress destination's term ids against the site. A
	// destination that names terms without one is refused rather than stored
	// unverified.
	Terms TermVerifier
	// Logf receives one line per refused configuration. Nil discards.
	Logf func(format string, args ...any)
}

func (s *Service) logf(format string, args ...any) {
	if s != nil && s.Logf != nil {
		s.Logf(format, args...)
	}
}

// ListWorkflows lists one page of workflows.
func (s *Service) ListWorkflows(ctx context.Context, opts automation.WorkflowListOptions) (automation.WorkflowPage, error) {
	if s.Store == nil {
		return automation.WorkflowPage{}, fmt.Errorf("%w: workflow storage is not configured", automation.ErrNotFound)
	}
	return s.Store.ListWorkflows(ctx, opts)
}

// GetWorkflow reads one workflow and its destinations.
func (s *Service) GetWorkflow(ctx context.Context, id int64) (automation.Workflow, bool, error) {
	if s.Store == nil {
		return automation.Workflow{}, false, fmt.Errorf("%w: workflow storage is not configured", automation.ErrNotFound)
	}
	return s.Store.GetWorkflow(ctx, id)
}

// CreateWorkflow stores one workflow for one source channel.
//
// in.CreatedBy is the account the configuration is made on behalf of. It is
// recorded as the workflow's author and is the identity every run publishes
// under; an update never reassigns it.
func (s *Service) CreateWorkflow(ctx context.Context, in automation.WorkflowInput) (automation.Workflow, error) {
	if s.Store == nil {
		return automation.Workflow{}, fmt.Errorf("%w: workflow storage is not configured", automation.ErrNotFound)
	}
	if err := s.verifyDestinations(ctx, in.Destinations); err != nil {
		return automation.Workflow{}, err
	}
	return s.Store.CreateWorkflow(ctx, in)
}

// UpdateWorkflow replaces one workflow's configuration and bumps its revision.
func (s *Service) UpdateWorkflow(ctx context.Context, id int64, in automation.WorkflowInput) (automation.Workflow, bool, error) {
	if s.Store == nil {
		return automation.Workflow{}, false, fmt.Errorf("%w: workflow storage is not configured", automation.ErrNotFound)
	}
	if err := s.verifyDestinations(ctx, in.Destinations); err != nil {
		return automation.Workflow{}, false, err
	}
	return s.Store.UpdateWorkflow(ctx, id, in)
}

// DeleteWorkflow removes one workflow, its destinations and its history.
func (s *Service) DeleteWorkflow(ctx context.Context, id int64) (bool, error) {
	if s.Store == nil {
		return false, fmt.Errorf("%w: workflow storage is not configured", automation.ErrNotFound)
	}
	return s.Store.DeleteWorkflow(ctx, id)
}

// ListRuns lists one page of a workflow's runs.
func (s *Service) ListRuns(ctx context.Context, workflowID int64, opts automation.RunListOptions) (automation.WorkflowRunPage, error) {
	if s.Store == nil {
		return automation.WorkflowRunPage{}, fmt.Errorf("%w: workflow storage is not configured", automation.ErrNotFound)
	}
	return s.Store.ListRuns(ctx, workflowID, opts)
}

// GetRun reads one run of one workflow, with what each destination achieved.
func (s *Service) GetRun(ctx context.Context, workflowID, runID int64) (automation.WorkflowRun, bool, error) {
	if s.Store == nil {
		return automation.WorkflowRun{}, false, fmt.Errorf("%w: workflow storage is not configured", automation.ErrNotFound)
	}
	return s.Store.GetRun(ctx, workflowID, runID)
}

// verifyDestinations confirms what a configuration claims before it is stored.
//
// A WordPress destination may name term ids, and those ids belong to one site.
// They are checked against the site itself: a term that is not there is
// refused here, so a workflow can never publish into a taxonomy selection that
// has been deleted or that was never the site's.
func (s *Service) verifyDestinations(ctx context.Context, dests []automation.WorkflowDestination) error {
	for _, dest := range dests {
		if dest.DestinationType != string(automation.DestinationWordPress) {
			continue
		}
		categories, tags, err := publishing.TermIDsOf(dest.Options)
		if err != nil {
			return configError(err)
		}
		if len(categories) == 0 && len(tags) == 0 {
			continue
		}
		if s.Terms == nil {
			return fmt.Errorf("%w: this deployment cannot confirm a WordPress term id, "+
				"so a destination may not name one", automation.ErrInvalid)
		}
		if err := s.Terms.VerifyTerms(ctx, dest.SiteID, publishing.TaxonomyCategories, categories); err != nil {
			return configError(err)
		}
		if err := s.Terms.VerifyTerms(ctx, dest.SiteID, publishing.TaxonomyTags, tags); err != nil {
			return configError(err)
		}
	}
	return nil
}

// configError restates a failure in the vocabulary the gateway maps onto an
// answer: a site that is not there is a missing resource, and anything else
// about a configuration is an invalid one.
func configError(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{automation.ErrConflict, automation.ErrNotFound, automation.ErrInvalid} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	if publishing.CodeOf(err) == publishing.CodePublishDestMissing {
		return fmt.Errorf("%w: %v", automation.ErrNotFound, err)
	}
	return fmt.Errorf("%w: %v", automation.ErrInvalid, err)
}
