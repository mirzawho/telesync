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
//
// The lookup is account-scoped and always has been in effect: a channel is
// registered against exactly one account, so the row a remote channel resolves
// to is the row that account subscribed. accountID is what makes that explicit,
// so two users watching one remote channel resolve to their own rows rather
// than to whichever was inserted first.
type ManagedChannelResolver interface {
	ManagedChannelFor(ctx context.Context, accountID, clientKey string, telegramChannelID int64) (registration ChannelRegistration, ok bool, err error)
}

// ChannelRegistration is the local row an observation resolved to, and the
// ownership that row carries.
type ChannelRegistration struct {
	// ManagedChannelID is the local managed_channels.id.
	ManagedChannelID int64
	// AccountID is the account that owns the registration. It is the event's
	// owning account and scopes the event identity.
	AccountID string
	// OwnerUserID is the Telesync user behind that account, so the work
	// scheduled from this event is bound to an owner without a second lookup.
	OwnerUserID int64
}

// Observation is one message a worker saw, before it is tied to a managed
// channel.
//
// It is the domain shape of the wire's IngestMessage. The identifiers are not
// interchangeable: TelegramChannelID and Message.ID are Telegram's own numbers
// and are meaningful only inside one NetworkKey, while the local managed id is
// resolved later and is the only one a permission check may use.
type Observation struct {
	ClientKey string
	// AccountID is the user-owned account the observing connection belongs to.
	AccountID         string
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
	reg, ok, err := resolve.ManagedChannelFor(ctx, obs.AccountID, obs.ClientKey, obs.TelegramChannelID)
	if err != nil {
		return "", false, fmt.Errorf("events: resolve channel %d of %s: %w",
			obs.TelegramChannelID, obs.ClientKey, err)
	}
	if !ok {
		return OutcomeUnmanaged, false, nil
	}

	src := Source{
		ClientKey:         obs.ClientKey,
		AccountID:         reg.AccountID,
		OwnerUserID:       reg.OwnerUserID,
		NetworkKey:        obs.NetworkKey,
		ManagedChannelID:  reg.ManagedChannelID,
		TelegramChannelID: obs.TelegramChannelID,
		Message:           obs.Message,
		GroupedID:         obs.GroupedID,
		ReceivedAt:        obs.ReceivedAt,
	}
	outcome, err := i.Ingest(ctx, src.Event())
	return outcome, true, err
}

// ChannelLookupFunc adapts a channel registration lookup to
// ManagedChannelResolver.
//
// It is the shape of the daemon's account-scoped registration lookup, so this
// package does not depend on the store. A channel the lookup does not return is
// not managed by that account.
type ChannelLookupFunc func(ctx context.Context, accountID, clientKey string, channelID int64) (ChannelRegistration, bool, error)

// ManagedChannelFor implements ManagedChannelResolver for a single channel.
func (f ChannelLookupFunc) ManagedChannelFor(ctx context.Context, accountID, clientKey string, telegramChannelID int64) (ChannelRegistration, bool, error) {
	return f(ctx, accountID, clientKey, telegramChannelID)
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
			AccountID:         msg.AccountID,
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
	case OutcomeQuarantined:
		// The wire's vocabulary is the older four. A quarantined observation is
		// reported as unmanaged to the worker: nothing was scheduled, and the
		// worker has no action to take either way. The daemon's own logs and the
		// operator view say quarantined.
		return rpc.IngestUnmanaged
	default:
		return rpc.IngestInserted
	}
}
