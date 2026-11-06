package gotdclient

import (
	"context"
	"time"

	"github.com/mirza/telesync/internal/automation/recovery"
)

// This file wires gotd's gap callbacks to the bounded recovery backfill.
//
// When the update manager cannot close a gap with updates.getChannelDifference
// — the channel changed too much while we were away — it signals OnChannelTooLong.
// That is the one moment automation is entitled to catch up: the account was
// already in the channel and already watching it. So we pull one bounded batch of
// the messages the gap left behind and ingest them, oldest first, and stop.
//
// It is deliberately bounded and incremental. A channel can hold years of
// history, and a reconnect must never turn into an archive download. One call
// moves one batch; the next call moves the next. And it never backfills a
// channel's past just because a workflow was enabled — only what the gap left.

// defaultBackfillBatch is one gap-backfill batch when the caller sets no bound.
const defaultBackfillBatch = 20

// backfillReader lists the message ids a gap left behind in one channel.
//
// It returns at most limit ids newer than afterID, oldest first. The bound is
// not cosmetic: it is what stops a reconnect from downloading a channel.
type backfillReader func(ctx context.Context, channelID, afterID int64, limit int) ([]int64, error)

// backfillAcceptor ingests one recovered message. It is the same hand-off the
// live update path uses, so a recovered message becomes exactly the same durable
// event a fresh one does.
type backfillAcceptor func(ctx context.Context, channelID, messageID int64) error

// backfiller runs the bounded gap backfill for a channel. It is what the update
// manager's OnChannelTooLong callback points at.
type backfiller struct {
	clientKey string
	store     recovery.CheckpointStore
	read      backfillReader
	accept    backfillAcceptor
	limit     int
	logf      func(format string, args ...any)
	// now bounds one backfill pass so a wedged read cannot hold the callback
	// forever. Zero uses DefaultBackfillTimeout.
	timeout time.Duration
}

// DefaultBackfillTimeout bounds one gap-backfill pass.
const DefaultBackfillTimeout = 30 * time.Second

// newBackfiller builds the gap backfill. store, read and accept must all be
// present for a backfill to run; any of them nil disables it and the gap is
// reported instead, never silently dropped.
func newBackfiller(clientKey string, store recovery.CheckpointStore,
	read backfillReader, accept backfillAcceptor,
	limit int, logf func(format string, args ...any)) *backfiller {

	if limit <= 0 {
		limit = defaultBackfillBatch
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &backfiller{
		clientKey: clientKey, store: store, read: read, accept: accept,
		limit: limit, logf: logf, timeout: DefaultBackfillTimeout,
	}
}

// onChannelTooLong runs one bounded backfill batch for a channel the manager
// could not recover on its own.
//
// It is the updates.Config.OnChannelTooLong callback. It never returns an error
// (the callback cannot), so a problem is reported and the gap is left for a
// later pass rather than being dropped.
func (b *backfiller) onChannelTooLong(channelID int64) {
	if b == nil || b.store == nil || b.read == nil || b.accept == nil {
		b.logf("gotdclient: channel %d has an unrecoverable gap; no backfill is configured", channelID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	read := func(ctx context.Context, afterID int64, limit int) ([]int64, error) {
		return b.read(ctx, channelID, afterID, limit)
	}
	accept := func(ctx context.Context, messageID int64) error {
		return b.accept(ctx, channelID, messageID)
	}
	delivered, checkpoint, err := recovery.Backfill{Store: b.store, Limit: b.limit}.
		Run(ctx, b.clientKey, channelID, read, accept)
	if err != nil {
		b.logf("gotdclient: backfill channel %d stopped at checkpoint %d after %d message(s): %v",
			channelID, checkpoint, delivered, err)
		return
	}
	b.logf("gotdclient: backfill channel %d delivered %d message(s) to checkpoint %d",
		channelID, delivered, checkpoint)
}

// onTooLong reports a common-sequence gap the manager could not recover. There
// is no per-message backfill for the shared sequence, so it is recorded as an
// unrecoverable gap rather than pretended away. Channel messages — the ones
// automation consumes — are recovered per channel via onChannelTooLong.
func (b *backfiller) onTooLong() {
	b.logf("gotdclient: %s: the common update gap is unrecoverable; channel messages are recovered per channel", b.clientKey)
}

// onLoadUserStateFailed reports that no stored update state was found, so the
// manager resynchronizes from the server. Messages missed while offline may need
// a per-channel backfill; this records the fact instead of hiding it.
func (b *backfiller) onLoadUserStateFailed() {
	b.logf("gotdclient: %s: no stored update state; resynchronizing from the server", b.clientKey)
}

// onLoadChannelStateFailed reports a channel whose stored state could not be
// loaded, so its gap recovery is skipped. The gap is recorded, never dropped
// silently.
func (b *backfiller) onLoadChannelStateFailed(channelID int64) {
	b.logf("gotdclient: %s: channel %d has no recoverable update state; its gap is not closed automatically", b.clientKey, channelID)
}
