package automation

import (
	"context"
	"errors"
	"time"
)

// Errors the services return and the gateway maps onto narrow HTTP codes. A
// service wraps one of these with the detail that explains the refusal; it
// never invents its own conflict vocabulary, or the gateway cannot tell a
// duplicate from a missing parent.
var (
	// ErrConflict is a uniqueness refusal: most often a second workflow for one
	// managed channel, which the schema also refuses.
	ErrConflict = errors.New("automation: conflicting configuration")
	// ErrInvalid is a request the domain will not accept, such as a webhook
	// header name that would overwrite a transport header.
	ErrInvalid = errors.New("automation: invalid configuration")
	// ErrNotFound is a resource that does not exist.
	ErrNotFound = errors.New("automation: not found")
	// ErrBaseURL means a webhook cannot be enabled because the deployment has
	// not named an externally reachable origin, so receivers could not open the
	// media links the payload carries.
	ErrBaseURL = errors.New("automation: api.public_base_url is not configured")
)

// This file holds the records the HTTP gateway reads and the service contracts
// it calls. They are here, in the shared package, so the gateway depends on one
// stable set of shapes while the webhook and workflow implementations are
// written independently: a store returns these, the gateway renders them into
// JSON views, and nothing has to import backwards into the gateway.
//
// Secrets never appear in any type below. A webhook records only whether it has
// one.

// Webhook is one outbound webhook configuration.
type Webhook struct {
	ID          int64
	Name        string
	Description string
	Enabled     bool
	URL         string
	Method      string // POST, PUT or PATCH
	AuthMode    string // hmac, bearer, header or none
	AuthHeader  string
	// HasSecret reports whether a secret is stored. The value itself is never
	// read out of the database for delivery to a client.
	HasSecret   bool
	Timeout     time.Duration
	MaxAttempts int
	// AllowLocalhost is this webhook's half of the loopback exception. It only
	// ever narrows the deployment's own permission: without
	// TELESYNC_WEBHOOK_ALLOW_LOCALHOST it does nothing at all.
	AllowLocalhost bool
	// SourceChannelIDs are the local managed_channels.id values it subscribes
	// to. These are local row ids, never Telegram ids.
	SourceChannelIDs []int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// LastDelivery and Summary are filled in for list responses and may be nil.
	LastDelivery *WebhookDeliverySummary
	Summary      *WebhookDeliveryCounts
}

// WebhookDeliveryCounts is the success and failure tally shown in a list.
type WebhookDeliveryCounts struct {
	Succeeded int
	Failed    int
	Pending   int
}

// WebhookDeliverySummary is the one-line "last delivery" cell of a list row.
type WebhookDeliverySummary struct {
	Status       string
	HTTPStatus   int
	At           time.Time
	AttemptCount int
}

// WebhookInput is a create or update request. Secret is write-only: nil leaves
// the stored secret untouched, a non-nil value replaces it. CreatedBy is the
// account the configuration is recorded against and is read on create only; an
// update never reassigns it.
type WebhookInput struct {
	Name             string
	Description      string
	Enabled          bool
	URL              string
	Method           string
	AuthMode         string
	AuthHeader       string
	Secret           *string
	Timeout          time.Duration
	MaxAttempts      int
	SourceChannelIDs []int64
	AllowLocalhost   bool
	CreatedBy        int64
}

// WebhookDelivery is one logical delivery of one event to one webhook. A retry
// extends the same row; it never becomes a second delivery.
type WebhookDelivery struct {
	ID             int64
	WebhookID      int64
	EventID        string
	Status         string // pending, delivering, succeeded, failed, cancelled
	AttemptCount   int
	NextAttemptAt  *time.Time
	LastHTTPStatus int
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
	// Request and Attempts are filled in for a detail response.
	Request  *WebhookRequestRecord
	Attempts []WebhookAttempt
}

// WebhookRequestRecord is the request actually transmitted, as stored.
type WebhookRequestRecord struct {
	Method        string
	URL           string
	Headers       map[string]string // already redacted
	Body          string            // already redacted
	BodyTruncated bool
}

// WebhookAttempt is one HTTP exchange. Every field is already redacted and
// already bounded to the configured cap before it is stored.
type WebhookAttempt struct {
	ID                    int64
	AttemptNumber         int
	RequestMethod         string
	RequestURL            string
	RequestHeaders        map[string]string
	RequestBody           string
	RequestBodyTruncated  bool
	ResponseStatus        int
	ResponseHeaders       map[string]string
	ResponseBody          string
	ResponseBodyTruncated bool
	ResponseBytes         int64
	DurationMS            int64
	ErrorCode             string
	ErrorMessage          string
	StartedAt             time.Time
	FinishedAt            *time.Time
}

// WebhookDeliveryPage is one page of deliveries plus the total.
type WebhookDeliveryPage struct {
	Deliveries []WebhookDelivery
	Total      int
}

// WebhookTestResult is what a test send reports back. It carries the same
// bounded, redacted request and response the history keeps, so a test cannot be
// used to read more than a real delivery would.
type WebhookTestResult struct {
	Delivery WebhookDelivery
}

// WorkflowSourceChannel is the source channel's identifying metadata, carried
// so a list or a detail view can name the channel without a second request.
type WorkflowSourceChannel struct {
	// ID is the local managed_channels.id.
	ID int64
	// ClientKey and TelegramChannelID identify it on the network.
	ClientKey         string
	TelegramChannelID int64
	Title             string
	Username          string
}

// Workflow is the one automation attached to one managed source channel.
type Workflow struct {
	ID                 int64
	ManagedChannelID   int64
	Enabled            bool
	AIEnabled          bool
	AIPrompt           string
	AISendImages       bool
	AIPublishOnFailure bool
	ConfigRevision     int
	Destinations       []WorkflowDestination
	CreatedAt          time.Time
	UpdatedAt          time.Time
	// Channel is the source channel's metadata and may be nil when the caller
	// did not ask for it to be resolved.
	Channel *WorkflowSourceChannel
	// LastRun is filled in for list responses and may be nil.
	LastRun *WorkflowRunSummary
}

// WorkflowRunSummary is the "last run" cell of a list row.
type WorkflowRunSummary struct {
	ID          int64
	Status      string
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// WorkflowDestination is one publishing target. Exactly one address is
// meaningful for each type: ClientKey and ChannelID for telegram, SiteID for
// wordpress. The schema refuses a row that mixes them.
//
// Identifier rule, uniform across the whole subsystem:
//
//	managed_channel_id  always a local managed_channels.id
//	channel_id          always a Telegram peer id
//	client_key          always the connection key
//
// So ChannelID here is the Telegram peer id the publisher sends to, never a
// local row id. A caller's right to name it is checked by resolving
// (client_key, channel_id) to its managed_channels row and asking about that
// row — the same step the manual publishing route already performs.
type WorkflowDestination struct {
	ID              int64
	WorkflowID      int64
	DestinationType string // telegram or wordpress
	ClientKey       string
	ChannelID       int64
	SiteID          int64
	Options         map[string]any
	Position        int
	Enabled         bool
}

// WorkflowInput is a create or update request. ManagedChannelID is a local
// managed_channels.id and is fixed at creation. CreatedBy is read on create
// only; an update never reassigns it.
type WorkflowInput struct {
	ManagedChannelID   int64
	Enabled            bool
	AIEnabled          bool
	AIPrompt           string
	AISendImages       bool
	AIPublishOnFailure bool
	Destinations       []WorkflowDestination
	CreatedBy          int64
}

// WorkflowRun is one execution triggered by one source message. Original and
// polished content are kept apart; the source message itself is never changed.
type WorkflowRun struct {
	ID              int64
	WorkflowID      int64
	EventID         string
	Status          string // queued, processing, published, published_with_fallback, partially_published, failed, cancelled
	SourceMessageID int64
	OriginalContent string
	PolishedContent string
	AIStatus        string // not_used, succeeded, failed, fallback_used
	AIFallbackUsed  bool
	AIModel         string
	AIErrorCode     string
	AIErrorMessage  string
	AIInputTokens   int
	AIOutputTokens  int
	AIDurationMS    int64
	// ConfigRevision is the workflow configuration revision this run was
	// prepared from. It is what makes a queued run deterministic: editing the
	// workflow afterwards changes future runs and not this one.
	ConfigRevision   int
	AttemptCount     int
	LastErrorCode    string
	LastErrorMessage string
	CreatedAt        time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
	Publications     []WorkflowPublication
}

// WorkflowPublication is one destination's result within one run.
type WorkflowPublication struct {
	ID               int64
	WorkflowRunID    int64
	DestinationType  string
	ClientKey        string
	ChannelID        int64
	SiteID           int64
	Status           string // pending, published, failed, unknown
	RemoteMessageID  int64
	RemotePostID     int64
	RemoteURL        string
	AttemptCount     int
	LastErrorCode    string
	LastErrorMessage string
	CompletedAt      *time.Time
}

// WorkflowRunPage is one page of runs plus the total.
type WorkflowRunPage struct {
	Runs  []WorkflowRun
	Total int
}

// WorkflowPage is one page of workflows plus the total.
type WorkflowPage struct {
	Workflows []Workflow
	Total     int
}

// WebhookPage is one page of webhooks plus the total.
type WebhookPage struct {
	Webhooks []Webhook
	Total    int
}

// AutomationStatus is the non-sensitive operational state the status route
// reports. It never names a channel's content and never carries a credential.
type AutomationStatus struct {
	Enabled        bool
	Workers        int
	PendingJobs    int
	RetryBacklog   int
	Processing     int
	FailedRecent   int
	RecentFailures []AutomationFailure
}

// AutomationFailure is one recent terminal failure, for the status route.
type AutomationFailure struct {
	JobType      string
	TargetID     int64
	ErrorCode    string
	ErrorMessage string
	At           time.Time
}

// AutomationStatusSource reports queue health.
type AutomationStatusSource interface {
	AutomationStatus(ctx context.Context) (AutomationStatus, error)
}

// WebhookService is what the gateway calls for webhook configuration and
// delivery history. Implementations must resolve authorization before these
// methods are reached: the gateway checks permissions, the service enforces
// referential integrity.
type WebhookService interface {
	ListWebhooks(ctx context.Context, opts WebhookListOptions) (WebhookPage, error)
	GetWebhook(ctx context.Context, id int64) (Webhook, bool, error)
	CreateWebhook(ctx context.Context, in WebhookInput) (Webhook, error)
	UpdateWebhook(ctx context.Context, id int64, in WebhookInput) (Webhook, bool, error)
	DeleteWebhook(ctx context.Context, id int64) (bool, error)
	ListDeliveries(ctx context.Context, webhookID int64, opts DeliveryListOptions) (WebhookDeliveryPage, error)
	GetDelivery(ctx context.Context, webhookID, deliveryID int64) (WebhookDelivery, bool, error)
	// RetryDelivery reschedules an eligible failed delivery. It reuses the
	// existing delivery row and its event; it never creates a second delivery.
	RetryDelivery(ctx context.Context, webhookID, deliveryID int64) (WebhookDelivery, bool, error)
	// TestWebhook sends a fixed, safe test payload through the same restricted
	// transport a real delivery uses.
	TestWebhook(ctx context.Context, id int64) (WebhookTestResult, bool, error)
}

// WebhookListOptions selects a page of webhooks.
type WebhookListOptions struct {
	Page    int
	PerPage int
	Query   string
	Enabled *bool
	// Visible reports whether the caller may see one local managed-channel id.
	// The service applies it so the page and the total it returns are already
	// restricted to what this caller may see; the gateway does not filter a
	// page after the fact, which would make the total lie.
	Visible func(localChannelID int64) bool
}

// DeliveryListOptions selects a page of deliveries.
type DeliveryListOptions struct {
	Page    int
	PerPage int
	Status  string
}

// WorkflowService is what the gateway calls for workflow configuration and run
// history.
type WorkflowService interface {
	ListWorkflows(ctx context.Context, opts WorkflowListOptions) (WorkflowPage, error)
	GetWorkflow(ctx context.Context, id int64) (Workflow, bool, error)
	CreateWorkflow(ctx context.Context, in WorkflowInput) (Workflow, error)
	UpdateWorkflow(ctx context.Context, id int64, in WorkflowInput) (Workflow, bool, error)
	DeleteWorkflow(ctx context.Context, id int64) (bool, error)
	ListRuns(ctx context.Context, workflowID int64, opts RunListOptions) (WorkflowRunPage, error)
	GetRun(ctx context.Context, workflowID, runID int64) (WorkflowRun, bool, error)
}

// WorkflowListOptions selects a page of workflows.
type WorkflowListOptions struct {
	Page    int
	PerPage int
	Query   string
	Enabled *bool
	// Visible is the same restriction WebhookListOptions carries: supplied by
	// the gateway, applied by the service, so totals stay honest.
	Visible func(localChannelID int64) bool
}

// RunListOptions selects a page of runs.
type RunListOptions struct {
	Page    int
	PerPage int
	Status  string
}
