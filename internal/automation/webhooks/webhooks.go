// Package webhooks delivers automation events to operator-configured HTTP
// endpoints, and manages the configuration that describes them.
//
// Three things shape everything here.
//
// The first is that an operator supplies a URL and this daemon connects to it,
// which is the most dangerous thing an operator can ask a daemon to do. The
// transport in ssrf.go therefore resolves the name once, refuses every answer
// that is not a public address, connects to the exact address it checked, never
// follows a redirect, never uses a proxy and speaks nothing but TLS. The check
// is repeated on every delivery, not only when the webhook was configured,
// because a target that was safe yesterday is not thereby safe today.
//
// The second is that a receiver has to be able to prove a request came from
// this installation and has not been replayed. signing.go implements the
// Standard Webhooks scheme: HMAC-SHA256 over the delivery id, the timestamp and
// the exact bytes transmitted, in the three headers that scheme defines. A
// retry keeps the delivery id, moves the timestamp and signs again.
//
// The third is that delivery history is evidence. Every attempt records what
// was sent and what came back, already redacted and already bounded, so an
// operator can see what happened without the record becoming a place a
// credential is kept.
//
// Media never travels in a payload. A receiver is told what an attachment is
// and where an authorized caller can fetch it, and nothing else.
package webhooks

import (
	"context"
	"net/http"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/secrets"
)

// Config is everything the webhook subsystem needs from its surroundings.
type Config struct {
	// PublicBaseURL is api.public_base_url, the externally reachable origin of
	// this installation. It is what turns the relative media path into an
	// address a receiver can open. When it is empty no webhook may be created
	// or enabled: there would be no honest address to put in the payload.
	PublicBaseURL string
	// BasePath is the path the HTTP gateway is mounted under, as
	// gateway.Config.BasePath reports it. See MediaURL.
	BasePath string
	// Webhooks bounds delivery: timeouts, attempts, response size and how many
	// deliveries run at once.
	Webhooks appconfig.WebhookConfig
	// Store is the webhook persistence.
	Store Store
	// Secrets seals a webhook secret for storage and opens one for use. The
	// plaintext exists for the length of one request and is never stored.
	Secrets Secrets
	// Events reads one stored event's message snapshot. The engine never
	// queries the event table itself and never talks to Telegram.
	Events EventSource
	// Jobs schedules delivery work, so a retry is durable rather than an
	// in-process loop.
	Jobs automation.JobEnqueuer
	// Reopen gives already-queued work a fresh attempt budget and puts it back
	// in the queue. It is what an operator pressing "retry" means, and it is
	// deliberately not EnqueueWebhookDelivery: that one is idempotent so a
	// replayed fan-out cannot deliver twice, which also means it never revives
	// work that ran out of attempts. Reopen refuses work that already succeeded
	// and work that is in flight.
	Reopen ReopenFunc
	// Now overrides the clock (tests).
	Now func() time.Time
	// Policy overrides the URL and address rules. Only tests set it.
	Policy Policy
	// Transport, Dial and Lookup replace the network for tests. Without them
	// the restricted transport in ssrf.go is used.
	Transport http.RoundTripper
	Dial      DialFunc
	Lookup    LookupFunc
}

// DialFunc is net.Dialer's signature, so a test can supply its own.
type DialFunc = dialFunc

// LookupFunc resolves a hostname.
type LookupFunc = lookupFunc

// ReopenFunc reschedules one unit of queued work with a fresh attempt budget.
// It is queue.Enqueuer.Reopen's signature.
type ReopenFunc func(ctx context.Context, eventID string, kind automation.JobKind, targetID int64) error

// EventSource reads one stored event's message snapshot.
//
// It is a function rather than an interface so the orchestrator can wire
// whichever event store this release ends up with without that store having to
// know about webhooks. Returning found=false means the event is gone: the
// delivery is then cancelled, because there is nothing left to send.
type EventSource func(ctx context.Context, eventID string) (automation.MessageEvent, bool, error)

// Secrets seals a webhook secret for storage and opens a stored one. It is the
// same pair internal/secrets offers, narrowed so this package cannot read any
// other kind of sealed value.
type Secrets interface {
	Seal(plaintext string) (secrets.Box, error)
	Open(box secrets.Box) (string, error)
}

// Store is the persistence the webhook subsystem needs. *channelstore.Store
// implements it.
type Store interface {
	CreateWebhook(ctx context.Context, createdBy int64, rec automation.Webhook, secret *secrets.Box) (automation.Webhook, error)
	GetWebhook(ctx context.Context, id int64) (automation.Webhook, bool, error)
	ListWebhooks(ctx context.Context, opts automation.WebhookListOptions) (automation.WebhookPage, error)
	UpdateWebhook(ctx context.Context, rec automation.Webhook, secret *secrets.Box) (automation.Webhook, bool, error)
	DeleteWebhook(ctx context.Context, id int64) (bool, error)
	WebhookSecretBox(ctx context.Context, id int64) (secrets.Box, bool, error)
	WebhookIDsForChannel(ctx context.Context, managedChannelID int64) ([]int64, error)

	CreateWebhookDelivery(ctx context.Context, rec automation.WebhookDelivery) (automation.WebhookDelivery, error)
	WebhookDeliveryForEvent(ctx context.Context, webhookID int64, eventID string) (automation.WebhookDelivery, bool, error)
	GetWebhookDelivery(ctx context.Context, id int64) (automation.WebhookDelivery, bool, error)
	ListWebhookDeliveries(ctx context.Context, webhookID int64, opts automation.DeliveryListOptions) (automation.WebhookDeliveryPage, error)
	UpdateWebhookDelivery(ctx context.Context, rec automation.WebhookDelivery) (automation.WebhookDelivery, bool, error)
	AppendWebhookAttempt(ctx context.Context, deliveryID int64, att automation.WebhookAttempt) (automation.WebhookAttempt, error)
	WebhookAttempts(ctx context.Context, deliveryID int64) ([]automation.WebhookAttempt, error)
	LatestWebhookAttempt(ctx context.Context, deliveryID int64) (automation.WebhookAttempt, bool, error)
}

// Delivery lifecycle states, matching the schema's CHECK constraint.
const (
	StatusPending    = "pending"
	StatusDelivering = "delivering"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
)

// Auth modes, matching the schema's CHECK constraint.
const (
	AuthHMAC   = "hmac"
	AuthBearer = "bearer"
	AuthHeader = "header"
	AuthNone   = "none"
)

// Bounds this package enforces whatever a record or a response asks for.
const (
	// MaxDeliveryAttempts is the hard ceiling on one delivery's attempts. A
	// webhook's own max_attempts is the budget handed to the queue; this is the
	// backstop that makes "never retry indefinitely" true even if it is not.
	MaxDeliveryAttempts = 100
	// MaxRetryAfter caps a receiver's Retry-After. A receiver does not get to
	// park a delivery for a week by sending one header.
	MaxRetryAfter = time.Hour
	// MaxRecordedBodyBytes caps what one attempt keeps of a request body. The
	// body is transmitted in full; only the record is bounded.
	MaxRecordedBodyBytes = 64 << 10
	// deliveryIDPrefix marks the stable logical delivery id carried in the
	// webhook-id header.
	deliveryIDPrefix = "wbd_"
)
