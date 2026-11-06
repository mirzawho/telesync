// Package events turns observed Telegram channel messages into durable
// automation events and fans them out to the automations that subscribe to
// them.
//
// Everything here is pure or takes a small interface, because the interesting
// behaviour — one event per message, no double automation for one logical
// message, no re-triggering of our own posts — has to be provable without a
// Telegram connection.
package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/rpc"
)

// Identity derives the stable id of one event from what makes it unique.
//
// The identity is (type, network, channel, message) and deliberately excludes
// the observing connection. Two accounts watching the same channel on the same
// network describe one logical message, and an id that included the observer
// would let the same message run an automation twice. A different network is a
// different identity even when its channel and message numbers coincide, which
// is why the network is in there.
//
// The id is derived and not generated, so a replayed update produces the same
// value and the database refuses it a second time.
func Identity(eventType automation.EventType, networkKey string, telegramChannelID, messageID int64) string {
	var b strings.Builder
	b.WriteString(string(eventType))
	b.WriteByte(0)
	b.WriteString(networkKey)
	b.WriteByte(0)
	b.WriteString(strconv.FormatInt(telegramChannelID, 10))
	b.WriteByte(0)
	b.WriteString(strconv.FormatInt(messageID, 10))
	sum := sha256.Sum256([]byte(b.String()))
	return automation.EventIDPrefix + hex.EncodeToString(sum[:16])
}

// Source is one message observed in one managed source channel.
//
// The identifiers are not interchangeable: ManagedChannelID is the local row
// and the only value a permission check may use, while TelegramChannelID and
// Message.ID are Telegram's own numbers and are meaningful only inside one
// NetworkKey.
type Source struct {
	// ClientKey is the connection that observed the message. It is recorded as
	// a possible media reader and is not part of the event identity.
	ClientKey         string
	NetworkKey        string
	ManagedChannelID  int64
	TelegramChannelID int64
	Message           rpc.Message
	// GroupedID is the album a message belongs to, zero when it stands alone.
	// It is carried on the observation rather than in rpc.Message, which is the
	// channel messages API's shape and has never needed it.
	GroupedID int64
	// ReceivedAt is when this process saw it, which is not the same as the
	// message's own date: a recovered message is old and newly seen.
	ReceivedAt time.Time
}

// Event normalizes one observation.
//
// Media is metadata only. The bytes are never fetched here and never stored:
// the payload keeps what the channel messages API already reports plus the
// relative path an authorized caller reads them from. Fetching attachments
// during ingestion would put a network transfer on the update path and make
// every message pay for media most automations never use.
func (s Source) Event() automation.MessageEvent {
	occurred := s.Message.Date
	if occurred.IsZero() {
		occurred = s.ReceivedAt
	}
	payload := automation.MessagePayload{
		Message: s.Message,
	}
	if s.Message.Media != nil {
		payload.MediaURLPath = automation.MediaURLPath(s.ClientKey, s.TelegramChannelID, s.Message.ID)
	}

	var grouped *int64
	if s.GroupedID != 0 {
		g := s.GroupedID
		grouped = &g
	}

	return automation.MessageEvent{
		ID:                Identity(automation.EventMessageCreated, s.NetworkKey, s.TelegramChannelID, int64(s.Message.ID)),
		Type:              automation.EventMessageCreated,
		NetworkKey:        s.NetworkKey,
		SourceClientKey:   s.ClientKey,
		ManagedChannelID:  s.ManagedChannelID,
		TelegramChannelID: s.TelegramChannelID,
		MessageID:         int64(s.Message.ID),
		GroupedID:         grouped,
		OccurredAt:        occurred.UTC(),
		ReceivedAt:        s.ReceivedAt.UTC(),
		Payload:           payload,
	}
}

// Outcome says what ingestion did with one observation. It is worth naming
// because two of the three outcomes are normal and must not look like errors.
type Outcome string

const (
	// OutcomeInserted is a new event, stored and fanned out.
	OutcomeInserted Outcome = "inserted"
	// OutcomeDuplicate is a message already known. The event is not created
	// again and no jobs are scheduled; the observer is still recorded so an
	// authorized connection able to read the media stays known.
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeSuppressed is a message this installation published. It is not
	// stored as an event at all: a workflow must not feed itself.
	OutcomeSuppressed Outcome = "suppressed"
	// OutcomeUnmanaged is a message in a channel this installation does not
	// manage. Nothing is stored: automation reacts to managed source channels
	// only, and an unmanaged message is not a failure to record.
	OutcomeUnmanaged Outcome = "unmanaged"
)

// Ingestor accepts observations and turns them into durable work.
type Ingestor struct {
	events    automation.EventStore
	jobs      automation.JobEnqueuer
	webhooks  automation.WebhookSubscriptions
	workflows automation.WorkflowLookup
	outbound  automation.OutboundMarker
	logf      func(format string, args ...any)
}

// NewIngestor wires the fan-out. Any of the last four may be nil in a test
// that only cares about storage; a nil subscriber simply contributes no jobs.
func NewIngestor(
	events automation.EventStore,
	jobs automation.JobEnqueuer,
	webhooks automation.WebhookSubscriptions,
	workflows automation.WorkflowLookup,
	outbound automation.OutboundMarker,
) *Ingestor {
	return &Ingestor{
		events: events, jobs: jobs, webhooks: webhooks,
		workflows: workflows, outbound: outbound,
		logf: func(string, ...any) {},
	}
}

// WithLogf receives one line per decision worth recording.
func (i *Ingestor) WithLogf(logf func(format string, args ...any)) *Ingestor {
	if logf != nil {
		i.logf = logf
	}
	return i
}

// Ingest stores one event and schedules the work it implies.
//
// The order matters. The event row is written first and the jobs after, so a
// crash in between leaves a stored event with no jobs; the reverse would leave
// jobs pointing at an event that does not exist. A gap is visible and can be
// repaired by re-ingesting the message, because ingestion is idempotent and
// re-running it reuses the same event id and the same unique job.
func (i *Ingestor) Ingest(ctx context.Context, event automation.MessageEvent) (Outcome, error) {
	if i.outbound != nil {
		own, err := i.outbound.IsOwnPublication(ctx,
			event.NetworkKey, event.SourceClientKey, event.TelegramChannelID, event.MessageID)
		if err != nil {
			return "", fmt.Errorf("events: check own publication: %w", err)
		}
		if own {
			// Ours. Suppressing it here is what stops a workflow that
			// publishes into a monitored channel from feeding itself.
			i.logf("events: suppressed own publication event=%s channel=%d message=%d",
				event.ID, event.TelegramChannelID, event.MessageID)
			return OutcomeSuppressed, nil
		}
	}

	inserted, err := i.events.InsertMessageEvent(ctx, event)
	if err != nil {
		return "", fmt.Errorf("events: insert %s: %w", event.ID, err)
	}
	if !inserted {
		// Already known. The store recorded this observer against the existing
		// event, so nothing is lost by not fanning out again.
		return OutcomeDuplicate, nil
	}

	if err := i.fanOut(ctx, event); err != nil {
		return OutcomeInserted, fmt.Errorf("events: fan out %s: %w", event.ID, err)
	}
	return OutcomeInserted, nil
}

// fanOut schedules one job per subscribed automation. A failure to reach one
// subscriber is reported but does not undo the others: the event is already
// durable, and the error names what was missed so it can be repaired.
func (i *Ingestor) fanOut(ctx context.Context, event automation.MessageEvent) error {
	var problems []string

	if i.jobs != nil && i.webhooks != nil {
		ids, err := i.webhooks.WebhookIDsForChannel(ctx, event.ManagedChannelID)
		if err != nil {
			problems = append(problems, "webhooks: "+err.Error())
		}
		for _, id := range ids {
			if err := i.jobs.EnqueueWebhookDelivery(ctx, event.ID, id); err != nil {
				problems = append(problems, fmt.Sprintf("webhook %d: %v", id, err))
			}
		}
	}

	if i.jobs != nil && i.workflows != nil {
		id, ok, err := i.workflows.WorkflowIDForChannel(ctx, event.ManagedChannelID)
		switch {
		case err != nil:
			problems = append(problems, "workflow: "+err.Error())
		case ok:
			if err := i.jobs.EnqueueWorkflowRun(ctx, event.ID, id); err != nil {
				problems = append(problems, fmt.Sprintf("workflow %d: %v", id, err))
			}
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("event %s is stored but %d automation(s) could not be scheduled: %s",
			event.ID, len(problems), strings.Join(problems, "; "))
	}
	return nil
}

// JobsFor reports which automations would react to one channel, without
// storing anything. It exists for the status route and for tests that assert
// the subscription resolution itself.
func (i *Ingestor) JobsFor(ctx context.Context, managedChannelID int64) (webhookIDs []int64, workflowID int64, hasWorkflow bool, err error) {
	if i.webhooks != nil {
		webhookIDs, err = i.webhooks.WebhookIDsForChannel(ctx, managedChannelID)
		if err != nil {
			return nil, 0, false, err
		}
	}
	if i.workflows != nil {
		workflowID, hasWorkflow, err = i.workflows.WorkflowIDForChannel(ctx, managedChannelID)
		if err != nil {
			return nil, 0, false, err
		}
	}
	return webhookIDs, workflowID, hasWorkflow, nil
}
