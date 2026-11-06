package gotdclient

import (
	"context"
	"testing"
)

// memCheckpoints is a per-channel high-water mark, standing in for the daemon's
// channel store.
type memCheckpoints struct{ m map[int64]int64 }

func (c *memCheckpoints) AutomationCheckpoint(_ context.Context, _ string, channelID int64) (int64, bool, error) {
	v, ok := c.m[channelID]
	return v, ok, nil
}

func (c *memCheckpoints) SetAutomationCheckpoint(_ context.Context, _ string, channelID, last int64) error {
	if c.m == nil {
		c.m = map[int64]int64{}
	}
	c.m[channelID] = last
	return nil
}

// When the manager cannot close a channel gap, it falls back to one bounded
// batch of history backfill: the messages the gap left behind are ingested
// oldest first and the checkpoint advances past them.
func TestAGapFallsBackToTheBoundedBackfill(t *testing.T) {
	cp := &memCheckpoints{m: map[int64]int64{500: 10}}
	var readCalls []int64
	var accepted []int64

	b := newBackfiller("client", cp,
		func(_ context.Context, channelID, afterID int64, limit int) ([]int64, error) {
			readCalls = append(readCalls, afterID)
			// A gap leaves messages 11..30 behind; the reader honors the bound.
			var ids []int64
			for id := afterID + 1; id <= 30 && len(ids) < limit; id++ {
				ids = append(ids, id)
			}
			return ids, nil
		},
		func(_ context.Context, _, messageID int64) error {
			accepted = append(accepted, messageID)
			return nil
		},
		5, nil)

	b.onChannelTooLong(500)

	if len(readCalls) != 1 {
		t.Fatalf("read %d times, want 1 bounded batch", len(readCalls))
	}
	// The batch is bounded: at most `limit` messages, not the whole gap.
	if len(accepted) != 5 {
		t.Fatalf("accepted %d messages, want the bound of 5 (got %v)", len(accepted), accepted)
	}
	// Oldest first.
	for i := 1; i < len(accepted); i++ {
		if accepted[i] <= accepted[i-1] {
			t.Fatalf("accepted out of order: %v", accepted)
		}
	}
	// The checkpoint advanced past what was accepted, so the next pass resumes
	// there instead of re-downloading.
	if cp.m[500] != accepted[len(accepted)-1] {
		t.Fatalf("checkpoint = %d, want %d", cp.m[500], accepted[len(accepted)-1])
	}
}

// A gap with no backfill configured is reported as unrecoverable, never dropped
// silently.
func TestAnUnrecoverableGapIsReportedNotDropped(t *testing.T) {
	var logged []string
	b := newBackfiller("client", nil, nil, nil, 0, func(format string, _ ...any) {
		logged = append(logged, format)
	})
	b.onChannelTooLong(500)
	if len(logged) == 0 {
		t.Fatal("an unrecoverable gap was dropped silently")
	}
}
