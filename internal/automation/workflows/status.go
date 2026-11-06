package workflows

import (
	"context"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
)

// QueueStatus is the durable queue's own tally: how much work is waiting, how
// much is running, and what recently stopped for good. The counters cover every
// job kind, because webhooks and workflows share one queue.
type QueueStatus interface {
	AutomationJobCounts(ctx context.Context) (channelstore.AutomationJobCounts, error)
	RecentAutomationFailures(ctx context.Context, limit int) ([]automation.AutomationFailure, error)
}

// Status reports queue health for the automation status route. It is
// automation.AutomationStatusSource.
//
// It reports the queue as it is and nothing else: no channel content, no
// credentials, and no per-account facts. Enabled and Workers describe how the
// subsystem was configured and are reporting only.
type Status struct {
	Store QueueStatus
	// Enabled is whether the background subsystem is switched on.
	Enabled bool
	// Workers is how many jobs run at once across all job kinds.
	Workers int
	// RecentFailures bounds the failure list. Zero means ten.
	RecentFailures int
}

// defaultRecentFailures is how many terminal failures a status answer carries.
const defaultRecentFailures = 10

// AutomationStatus reports the queue's shape at one moment.
func (s *Status) AutomationStatus(ctx context.Context) (automation.AutomationStatus, error) {
	out := automation.AutomationStatus{
		Enabled:        s.Enabled,
		Workers:        s.Workers,
		RecentFailures: []automation.AutomationFailure{},
	}
	if s == nil || s.Store == nil {
		return out, nil
	}
	counts, err := s.Store.AutomationJobCounts(ctx)
	if err != nil {
		return automation.AutomationStatus{}, err
	}
	out.PendingJobs = counts.Pending
	out.RetryBacklog = counts.RetryWait
	out.Processing = counts.Processing

	limit := s.RecentFailures
	if limit <= 0 {
		limit = defaultRecentFailures
	}
	failures, err := s.Store.RecentAutomationFailures(ctx, limit)
	if err != nil {
		return automation.AutomationStatus{}, err
	}
	out.FailedRecent = len(failures)
	out.RecentFailures = failures
	if out.RecentFailures == nil {
		out.RecentFailures = []automation.AutomationFailure{}
	}
	return out, nil
}
