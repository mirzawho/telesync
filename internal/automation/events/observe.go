package events

import (
	"context"
	"fmt"
	"time"

	"github.com/mirza/telesync/internal/rpc"
)

// This file is the daemon half of the ingestion bridge.
//
// A worker observes a Telegram channel message and hands over its metadata; it
// holds no database and cannot know which channels this installation manages.
// Observe resolves the remote channel to its local managed row and, only then,
// turns the observation into a durable event. A channel this installation does
// not manage produces nothing at all: automation reacts to managed source
// channels, and an unmanaged message is not a failure to record.

// ManagedChannelResolver maps a remote channel to the local managed row.
//
// managedID is a local managed_channels.id and is the only value a permission
// check may use. ok is false when this installation does not manage the channel,
// which is a normal answer and not an error.
type ManagedChannelResolver interface {
	ManagedChannelFor(ctx context.Context, clientKey string, telegramChannelID int64) (managedID int64, ok bool, err error)
}

// Observation is one message a worker saw, before it is tied to a managed
// channel.
//
// It is the domain shape of the wire's IngestMessage. The identifiers are not
// interchangeable: TelegramChannelID and Message.ID are Telegram's own numbers
// and are meaningful only inside one NetworkKey, while the local managed id is
// resolved later and is the only one a permission check may use.
type Observation struct {
	ClientKey         string
	NetworkKey        string
	TelegramChannelID int64
	Message           rpc.Message
	// GroupedID is the album the message belongs to, zero when it stands alone.
	GroupedID int64
	// ReceivedAt is when the worker observed it, which is not the message's own
	// date: a recovered message is old and newly seen.
	ReceivedAt time.Time
}

// Observe resolves one observation to its managed channel and ingests it.
//
// The third return reports whether the channel is managed at all. When it is
// not, nothing is stored and the outcome is OutcomeUnmanaged — a channel this
// installation does not manage is simply not automation's business, and must not
// be recorded as if it were.
func (i *Ingestor) Observe(ctx context.Context, resolve ManagedChannelResolver, obs Observation) (Outcome, bool, error) {
	managedID, ok, err := resolve.ManagedChannelFor(ctx, obs.ClientKey, obs.TelegramChannelID)
	if err != nil {
		return "", false, fmt.Errorf("events: resolve channel %d of %s: %w",
			obs.TelegramChannelID, obs.ClientKey, err)
	}
	if !ok {
		return OutcomeUnmanaged, false, nil
	}

	src := Source{
		ClientKey:         obs.ClientKey,
		NetworkKey:        obs.NetworkKey,
		ManagedChannelID:  managedID,
		TelegramChannelID: obs.TelegramChannelID,
		Message:           obs.Message,
		GroupedID:         obs.GroupedID,
		ReceivedAt:        obs.ReceivedAt,
	}
	outcome, err := i.Ingest(ctx, src.Event())
	return outcome, true, err
}

// LocalIDsFunc adapts a batch id lookup to ManagedChannelResolver.
//
// It is the shape of channelstore.Store.LocalIDsByChannelID, so the daemon can
// resolve an observation's remote channel to its local managed row without this
// package depending on the store. A channel the lookup does not return is not
// managed.
type LocalIDsFunc func(ctx context.Context, clientKey string, channelIDs []int64) (map[int64]int64, error)

// ManagedChannelFor implements ManagedChannelResolver for a single channel.
func (f LocalIDsFunc) ManagedChannelFor(ctx context.Context, clientKey string, telegramChannelID int64) (int64, bool, error) {
	m, err := f(ctx, clientKey, []int64{telegramChannelID})
	if err != nil {
		return 0, false, err
	}
	id, ok := m[telegramChannelID]
	return id, ok, nil
}

// NewRPCIngestBackend adapts an Ingestor to the daemon half of the automation
// bridge. It is what the daemon serves on its private automation socket; the
// worker calls it over the wire.
//
// resolve answers which managed channel an observation belongs to. Either piece
// may be nil, in which case the backend reports that ingestion is unavailable
// rather than silently dropping observations.
func NewRPCIngestBackend(ingest *Ingestor, resolve ManagedChannelResolver) rpc.IngestBackend {
	if ingest == nil || resolve == nil {
		return nil
	}
	return rpc.IngestBackendFunc(func(ctx context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
		outcome, _, err := ingest.Observe(ctx, resolve, Observation{
			ClientKey:         msg.ClientKey,
			NetworkKey:        msg.NetworkKey,
			TelegramChannelID: msg.TelegramChannelID,
			Message:           msg.Message,
			GroupedID:         msg.GroupedID,
			ReceivedAt:        msg.ReceivedAt,
		})
		if err != nil {
			return "", err
		}
		return wireOutcome(outcome), nil
	})
}

func wireOutcome(o Outcome) rpc.IngestOutcome {
	switch o {
	case OutcomeInserted:
		return rpc.IngestInserted
	case OutcomeDuplicate:
		return rpc.IngestDuplicate
	case OutcomeSuppressed:
		return rpc.IngestSuppressed
	case OutcomeUnmanaged:
		return rpc.IngestUnmanaged
	default:
		return rpc.IngestInserted
	}
}
