package workflows_test

import (
	"context"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/workflows"
	"github.com/mirza/telesync/internal/channelstore"
)

// The status route reports how much work the queue is holding and what recently
// stopped for good. It never names a channel and never carries a credential.

// fakeQueue is the durable queue's own tally.
type fakeQueue struct {
	counts   channelstore.AutomationJobCounts
	failures []automation.AutomationFailure
	limit    int
}

func (f *fakeQueue) AutomationJobCounts(context.Context) (channelstore.AutomationJobCounts, error) {
	return f.counts, nil
}

func (f *fakeQueue) RecentAutomationFailures(_ context.Context, limit int) ([]automation.AutomationFailure, error) {
	f.limit = limit
	return f.failures, nil
}

func TestAutomationStatusReportsTheQueueShape(t *testing.T) {
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	queue := &fakeQueue{
		counts: channelstore.AutomationJobCounts{Pending: 3, RetryWait: 2, Processing: 1},
		failures: []automation.AutomationFailure{{
			JobType: string(automation.JobWorkflowRun), TargetID: 4,
			ErrorCode: "publish_failed", ErrorMessage: "the destination refused the message", At: at,
		}},
	}
	status := &workflows.Status{Store: queue, Enabled: true, Workers: 2}

	got, err := status.AutomationStatus(context.Background())
	if err != nil {
		t.Fatalf("AutomationStatus: %v", err)
	}
	if !got.Enabled || got.Workers != 2 {
		t.Fatalf("subsystem = %+v", got)
	}
	if got.PendingJobs != 3 || got.RetryBacklog != 2 || got.Processing != 1 {
		t.Fatalf("counters = %+v", got)
	}
	if got.FailedRecent != 1 || len(got.RecentFailures) != 1 {
		t.Fatalf("failures = %+v", got)
	}
	if got.RecentFailures[0].JobType != string(automation.JobWorkflowRun) ||
		got.RecentFailures[0].ErrorCode != "publish_failed" {
		t.Fatalf("failure = %+v", got.RecentFailures[0])
	}
	if queue.limit != 10 {
		t.Fatalf("failure limit = %d, want the default", queue.limit)
	}
}

// A queue that cannot be read is not an empty queue: the caller is told.
func TestAutomationStatusReportsAnEmptyQueueWithoutNilSlices(t *testing.T) {
	got, err := (&workflows.Status{Store: &fakeQueue{}}).AutomationStatus(context.Background())
	if err != nil {
		t.Fatalf("AutomationStatus: %v", err)
	}
	if got.RecentFailures == nil {
		t.Fatal("a status answer must carry an empty list rather than nil")
	}
	if got.PendingJobs != 0 || got.FailedRecent != 0 {
		t.Fatalf("status = %+v", got)
	}
}
