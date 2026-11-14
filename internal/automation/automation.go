// Package automation holds the contracts shared by the background automation
// subsystem: event ingestion, the durable job queue, webhooks, workflows and
// AI polishing.
//
// This file is deliberately small and acyclic. It carries only what crosses a
// package boundary: everything else lives with the code that owns it. Types
// here describe domain facts, not wire formats; the gateway renders them into
// HTTP shapes and the webhook engine into the CloudEvents envelope.
package automation

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/rpc"
)

// EventType identifies what happened. The first and only type produced today
// is EventMessageCreated; the value is a dotted, versionable name so later
// types can be added without reworking stored rows.
type EventType string

// EventMessageCreated is a new message appearing in a managed source channel.
const EventMessageCreated EventType = "telegram.message.created"

// EventIDPrefix marks an event id in logs and API responses. The id itself is
// derived from the event identity, never generated at random, so a replayed
// update produces the same id and is refused by the database.
const EventIDPrefix = "evt_"

// MediaURLPath is the media endpoint path for one source message, as the
// channel messages API emits it: relative to the API namespace, without the
// mount prefix.
//
// It is stated once, here, because two things must agree byte for byte: the
// path in a channel message and the path a webhook payload resolves into an
// absolute URL. Both callers use this, so they cannot drift.
//
// The result is relative on purpose. A stored row must not claim a public
// address the deployment may not be able to serve, and the same relative path
// must remain correct if the public origin changes.
func MediaURLPath(clientKey string, telegramChannelID int64, messageID int) string {
	return "/telegram/" + clientKey + "/channels/" +
		strconv.FormatInt(telegramChannelID, 10) +
		"/messages/" + strconv.FormatInt(int64(messageID), 10) + "/media"
}

// APIMount is the prefix the API is served under, which is the mount plus the
// /api/v1 namespace.
//
// A media path is stated below this, so resolving one needs it: joining a media
// path to the bare mount would silently produce a URL that 404s. Keeping the
// join here means a caller cannot forget the namespace.
func APIMount(basePath string) string {
	return strings.TrimSuffix(basePath, "/") + "/api/v1"
}

// AbsoluteMediaURL resolves a stored media path against the externally
// reachable origin.
//
// mount is what APIMount returns, not the bare BasePath. publicBaseURL is the
// operator's declared origin with or without a trailing slash.
//
// ok is false when the deployment has not named an origin. In that case no URL
// is produced at all: a receiver must never be handed a link that cannot be
// opened, and an absolute-looking guess would be worse than an honest absence.
func AbsoluteMediaURL(publicBaseURL, mount, mediaPath string) (string, bool) {
	if publicBaseURL == "" || mediaPath == "" {
		return "", false
	}
	return strings.TrimSuffix(publicBaseURL, "/") + strings.TrimSuffix(mount, "/") + mediaPath, true
}

// MessagePayload is the durable snapshot of one source message. It holds what
// the channel messages API already reports, and nothing else: media bytes are
// never copied into it, only the metadata and the path an authorized caller
// fetches them from.
type MessagePayload struct {
	Message rpc.Message `json:"message"`
	// MediaURLPath is the media endpoint path, relative to the API mount, in
	// the same shape the channel messages API emits. It is stored relative so a
	// change to the public base URL does not invalidate history, and so no row
	// ever claims a public address the deployment cannot actually serve.
	MediaURLPath string `json:"media_url_path,omitempty"`
}

// MessageEvent is one normalized incoming Telegram channel message, already
// resolved to the local managed channel it belongs to.
//
// The identifier fields are not interchangeable: ManagedChannelID is the local
// row id and the only value that may be used for permission checks, while
// TelegramChannelID and MessageID are Telegram's own numbers and are only safe
// to compare within one NetworkKey.
//
// AccountID and SourceAccountID are the ownership dimension. AccountID names
// the user-owned Telegram account this event belongs to and scopes the event
// identity; SourceAccountID names the account whose connection observed it.
// Ingestion produces them as the same account, and they are kept apart because
// a cross-account relay would make them differ without changing who owns the
// event.
type MessageEvent struct {
	ID              string
	Type            EventType
	NetworkKey      string
	SourceClientKey string
	// AccountID is the owning account. It is empty only on a legacy row that
	// predates account ownership, and such a row is quarantined: it is never
	// fanned out and never runs under whichever account happens to be connected.
	AccountID string
	// SourceAccountID is the account that observed the message. It is the
	// connection a payload's media links are authorised against.
	SourceAccountID   string
	ManagedChannelID  int64
	TelegramChannelID int64
	MessageID         int64
	GroupedID         *int64
	OccurredAt        time.Time
	ReceivedAt        time.Time
	Payload           MessagePayload
	// ownerUserID is the Telesync user the account belongs to. It is not part
	// of the event identity and is filled in by the store on read; an event
	// built from an observation carries it directly.
	ownerUserID int64
}

// WithOwner records the user the owning account belongs to, so work scheduled
// from this event can be bound to an owner without a second lookup. It is set
// by the store when an event is read back and by ingestion when the observation
// already knows it.
func (e MessageEvent) WithOwner(ownerUserID int64) MessageEvent {
	e.ownerUserID = ownerUserID
	return e
}

// AccountOwnerUserID is the user behind AccountID, or zero for a legacy row
// that names no account.
func (e MessageEvent) AccountOwnerUserID() int64 { return e.ownerUserID }

// JobKind is the kind of background work one queue row represents.
type JobKind string

// The job kinds of this release. Webhook delivery and workflow execution share
// one queue and one worker pool on purpose: they are independent in outcome but
// not in infrastructure. A direct message send joins them for the same reason —
// it is a durable unit of work with an attempt budget and a retry policy, and
// giving it a queue of its own would give the deployment two of each to run.
const (
	JobWebhookDelivery JobKind = "webhook_delivery"
	JobWorkflowRun     JobKind = "workflow_run"
	// JobTelegramMessageSend is one accepted send waiting to be performed. Its
	// event id is the send operation's public id and its target is the
	// destination channel, so one channel's sends claim in order and cannot
	// burst past the queue's own per-destination pacing.
	JobTelegramMessageSend JobKind = "telegram_message_send"
	// JobTelegramMessageEdit is one accepted replacement of an existing
	// Telegram message. Its target key scopes ordering to one client/channel/
	// message tuple, so independent edits remain runnable concurrently.
	JobTelegramMessageEdit JobKind = "telegram_message_edit"
)

// JobStatus is the lifecycle state of one queued unit of work.
type JobStatus string

const (
	JobPending    JobStatus = "pending"
	JobProcessing JobStatus = "processing"
	JobRetryWait  JobStatus = "retry_wait"
	JobSucceeded  JobStatus = "succeeded"
	JobFailed     JobStatus = "failed"
	JobCancelled  JobStatus = "cancelled"
)

// Owner is the ownership binding every background record carries.
//
// It is stated once, here, so "who does this work belong to" has one shape in
// the queue, in the webhook and workflow records and in the operations the
// publishing pipeline keeps. OwnerUserID is the Telesync user; AccountID is the
// user-owned Telegram account the work runs through.
//
// A zero OwnerUserID and an empty AccountID describe a legacy row from before
// accounts existed. Such a row is paused and never claimed: no account is ever
// assigned to it by inference.
type Owner struct {
	OwnerUserID int64
	AccountID   string
}

// Bound reports whether this binding names a real owner. It is the one test for
// "this work may run", and it is a positive one: nothing is claimable unless it
// is bound to an account somebody owns.
func (o Owner) Bound() bool {
	return o.OwnerUserID > 0 && o.AccountID != ""
}

// Job is one claimed unit of background work.
type Job struct {
	ID          int64
	EventID     string
	Kind        JobKind
	TargetID    int64
	Attempt     int
	MaxAttempts int
	AvailableAt time.Time
	CreatedAt   time.Time
	// Owner is who this job runs for. A handler acts through Owner.AccountID and
	// through no other account, whatever else it happens to be able to reach.
	Owner Owner
}

// JobError is what a handler returns when a job did not complete. It carries
// enough for the queue to decide between another attempt and a terminal
// failure, and enough for the UI to explain what happened, without ever
// holding a secret.
type JobError struct {
	Code      string
	Message   string
	Retryable bool
	// RetryAfter is a provider-supplied delay, such as a Retry-After header or
	// a Telegram flood wait. Zero means the queue's own backoff applies.
	RetryAfter time.Duration
}

func (e *JobError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// JobHandler executes one claimed job. A nil return means the work is done; a
// *JobError decides whether the queue schedules another attempt. Any other
// error is treated as retryable, because an unclassified failure is not proof
// that the remote side rejected the request.
type JobHandler func(ctx context.Context, job Job) error

// JobHandlers maps each job kind to the code that executes it. The queue owns
// no domain knowledge; webhooks and workflows register their own handler.
type JobHandlers map[JobKind]JobHandler

// EventStore persists normalized incoming events.
//
// InsertMessageEvent reports inserted=false when the event was already known.
// That is a normal outcome, not an error: duplicate update delivery is
// expected, and a second observation of the same logical message is still
// recorded as a source so an authorized connection able to read the media
// stays known.
type EventStore interface {
	InsertMessageEvent(ctx context.Context, event MessageEvent) (inserted bool, err error)
}

// JobEnqueuer turns a stored event into durable background work.
//
// Every enqueue names the owner the work belongs to. That binding is what a
// worker is claimed against, so work scheduled for one user's account is never
// executed by another's, and there is no path that schedules work with no
// owner at all.
type JobEnqueuer interface {
	EnqueueWebhookDelivery(ctx context.Context, eventID string, webhookID int64, owner Owner) error
	EnqueueWorkflowRun(ctx context.Context, eventID string, workflowID int64, owner Owner) error
}

// WebhookSubscriptions resolves which webhooks react to one source channel.
type WebhookSubscriptions interface {
	WebhookIDsForChannel(ctx context.Context, managedChannelID int64) ([]int64, error)
}

// WorkflowLookup resolves the single workflow that reacts to one source
// channel. A channel has at most one workflow, so this returns an id and not a
// slice.
type WorkflowLookup interface {
	WorkflowIDForChannel(ctx context.Context, managedChannelID int64) (int64, bool, error)
}

// OutboundMarker records messages Telesync itself put into a Telegram channel.
// Ingestion consults it before creating an event, which is what stops a
// workflow from feeding itself. It is not a general message store: it holds
// only remote ids of our own publications.
type OutboundMarker interface {
	// IsOwnPublication reports whether the message was published by this
	// installation. networkKey must be the destination's network, since a
	// workflow may publish across networks.
	IsOwnPublication(ctx context.Context, networkKey, clientKey string, channelID, messageID int64) (bool, error)
	MarkPublished(ctx context.Context, m OutboundMessage) error
}

// OutboundMessage is one message this installation sent to a Telegram channel.
type OutboundMessage struct {
	NetworkKey      string
	ClientKey       string
	ChannelID       int64
	RemoteMessageID int64
	Origin          string
	OriginID        int64
}

// ImageInput is one image offered to the AI model. The bytes are held in
// memory only for the duration of one request and are never persisted.
type ImageInput struct {
	MimeType string
	Filename string
	Data     []byte
}

// PolishInput is one AI polishing request.
//
// Prompt is the workflow's custom prompt and may be empty, in which case the
// processor applies its own default polishing prompt. Text is untrusted source
// content: it is data to edit, never instructions to obey.
type PolishInput struct {
	WorkflowID int64
	Prompt     string
	Text       string
	Images     []ImageInput
}

// PolishResult is the validated structured output of one AI call.
type PolishResult struct {
	Text         string
	Title        string
	Hashtags     []string
	Mentions     []string
	Model        string
	InputTokens  int
	OutputTokens int
	Duration     time.Duration
}

// AIProcessor polishes one message. Implementations validate the provider's
// structured output and return an error rather than a partial or malformed
// result, so a caller can apply its own failure policy.
type AIProcessor interface {
	Polish(ctx context.Context, input PolishInput) (PolishResult, error)
}

// DestinationType is where one publication goes.
type DestinationType string

const (
	DestinationTelegram  DestinationType = "telegram"
	DestinationWordPress DestinationType = "wordpress"
)

// SourceRef identifies the message a publication originates from. Media is
// fetched through the source connection, never through the destination's.
type SourceRef struct {
	ManagedChannelID  int64
	ClientKey         string
	TelegramChannelID int64
	MessageID         int64
}

// Destination is one configured publication target. Which address fields are
// meaningful depends on Type: a Telegram destination uses ClientKey and
// ChannelID, a WordPress destination uses SiteID.
type Destination struct {
	Type      DestinationType
	ClientKey string
	ChannelID int64
	SiteID    int64
	// AccountID is the exact user-owned Telegram identity selected for this
	// destination. It is separate from the workflow/source account.
	AccountID string
	// Options carries the destination's existing publishing options as a JSON
	// object: silent, disable web page preview, WordPress category and tag ids,
	// thumbnail policy. The publisher interprets them; this package does not.
	Options map[string]any
}

// PublishOptions are the per-request publishing switches.
type PublishOptions struct {
	Silent                bool
	DisableWebPagePreview bool
	// MediaMode is "preserve_source" or "omit", matching the manual publishing
	// contract.
	MediaMode string
}

// PublishRequest asks the existing publisher to send one message to one
// destination. It is the same request the manual publishing route builds, so
// background and interactive publishing cannot drift apart.
type PublishRequest struct {
	Source         SourceRef
	Destination    Destination
	Text           string
	Entities       []rpc.Entity
	Options        PublishOptions
	IdempotencyKey string
	// UserID is the authorization context the request runs under. It is
	// re-resolved at execution time; a stale grant must not keep publishing.
	UserID int64
}

// PublishOutcome is what the destination actually did.
type PublishOutcome string

const (
	PublishSucceeded PublishOutcome = "succeeded"
	PublishFailed    PublishOutcome = "failed"
	// PublishUnknown means the request left this process but the outcome is not
	// known, such as a timeout after the destination may have accepted it. It
	// is never silently retried as if it had failed.
	PublishUnknown PublishOutcome = "unknown"
)

// PublishResult is one destination's answer.
type PublishResult struct {
	Outcome         PublishOutcome
	RemoteMessageID int64
	RemotePostID    int64
	RemoteURL       string
	ErrorCode       string
	ErrorMessage    string
}

// PublishingService sends one message to one destination. It is the v0.0.2
// publishing implementation exposed for background use: manual publishing and
// workflow publishing both go through it.
type PublishingService interface {
	Publish(ctx context.Context, request PublishRequest) (PublishResult, error)
}
