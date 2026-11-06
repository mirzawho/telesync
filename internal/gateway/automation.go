package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// automationPrefix is the background automation namespace: webhook
// configuration and history, workflow configuration and run history. Like
// every other path constant it is stated without BasePath and matched against
// s.fullPath(...), because route matching strips the mount prefix.
const automationPrefix = "/api/v1/local/automation"

const (
	maxAutomationBody = 64 << 10
	maxAutomationQ    = 120
)

// Automation views. They are the gateway's public shapes and are rendered from
// the shared automation records. None of them can carry a credential: a webhook
// reports only whether it has one.

type webhookView struct {
	ID             int64                       `json:"id"`
	Name           string                      `json:"name"`
	Description    string                      `json:"description"`
	Enabled        bool                        `json:"enabled"`
	URL            string                      `json:"url"`
	Method         string                      `json:"method"`
	AuthMode       string                      `json:"auth_mode"`
	AuthHeader     string                      `json:"auth_header"`
	HasSecret      bool                        `json:"has_secret"`
	TimeoutMS      int64                       `json:"timeout_ms"`
	MaxAttempts    int                         `json:"max_attempts"`
	SourceChannels []int64                     `json:"source_channel_ids"`
	CreatedAt      string                      `json:"created_at"`
	UpdatedAt      string                      `json:"updated_at"`
	LastDelivery   *webhookDeliverySummaryView `json:"last_delivery,omitempty"`
	Summary        *webhookCountsView          `json:"summary,omitempty"`
}

type webhookCountsView struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}

type webhookDeliverySummaryView struct {
	Status       string `json:"status"`
	HTTPStatus   int    `json:"http_status"`
	At           string `json:"at"`
	AttemptCount int    `json:"attempt_count"`
}

type deliveryView struct {
	ID             int64         `json:"id"`
	WebhookID      int64         `json:"webhook_id"`
	EventID        string        `json:"event_id"`
	Status         string        `json:"status"`
	AttemptCount   int           `json:"attempt_count"`
	NextAttemptAt  *string       `json:"next_attempt_at"`
	LastHTTPStatus int           `json:"last_http_status"`
	LastError      string        `json:"last_error"`
	CreatedAt      string        `json:"created_at"`
	UpdatedAt      string        `json:"updated_at"`
	CompletedAt    *string       `json:"completed_at"`
	Request        *requestView  `json:"request,omitempty"`
	Attempts       []attemptView `json:"attempts,omitempty"`
}

type requestView struct {
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	Body          string            `json:"body"`
	BodyTruncated bool              `json:"body_truncated"`
}

type attemptView struct {
	ID                    int64             `json:"id"`
	AttemptNumber         int               `json:"attempt_number"`
	RequestMethod         string            `json:"request_method"`
	RequestURL            string            `json:"request_url"`
	RequestHeaders        map[string]string `json:"request_headers"`
	RequestBody           string            `json:"request_body"`
	RequestBodyTruncated  bool              `json:"request_body_truncated"`
	ResponseStatus        int               `json:"response_status"`
	ResponseHeaders       map[string]string `json:"response_headers"`
	ResponseBody          string            `json:"response_body"`
	ResponseBodyTruncated bool              `json:"response_body_truncated"`
	ResponseBytes         int64             `json:"response_bytes"`
	DurationMS            int64             `json:"duration_ms"`
	ErrorCode             string            `json:"error_code"`
	ErrorMessage          string            `json:"error_message"`
	StartedAt             string            `json:"started_at"`
	FinishedAt            *string           `json:"finished_at"`
}

type workflowView struct {
	ID                 int64                     `json:"id"`
	ManagedChannelID   int64                     `json:"managed_channel_id"`
	Enabled            bool                      `json:"enabled"`
	AIEnabled          bool                      `json:"ai_enabled"`
	AIPrompt           string                    `json:"ai_prompt"`
	AISendImages       bool                      `json:"ai_send_images"`
	AIPublishOnFailure bool                      `json:"ai_publish_on_failure"`
	ConfigRevision     int                       `json:"config_revision"`
	Destinations       []workflowDestinationView `json:"destinations"`
	CreatedAt          string                    `json:"created_at"`
	UpdatedAt          string                    `json:"updated_at"`
	Channel            *sourceChannelView        `json:"channel,omitempty"`
	LastRun            *workflowRunSummaryView   `json:"last_run,omitempty"`
}

// sourceChannelView names one managed source channel. Both numbers are given
// because they answer different questions: the local id is what permissions are
// decided on, the Telegram id is what a person recognises.
type sourceChannelView struct {
	ID                int64  `json:"id"`
	ClientKey         string `json:"client_key"`
	TelegramChannelID int64  `json:"telegram_channel_id"`
	Title             string `json:"title"`
	Username          string `json:"username"`
}

type workflowDestinationView struct {
	ID              int64          `json:"id"`
	DestinationType string         `json:"destination_type"`
	ClientKey       string         `json:"client_key"`
	ChannelID       int64          `json:"channel_id"`
	SiteID          int64          `json:"site_id"`
	Options         map[string]any `json:"options"`
	Position        int            `json:"position"`
	Enabled         bool           `json:"enabled"`
}

type workflowRunSummaryView struct {
	ID          int64   `json:"id"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt *string `json:"completed_at"`
}

type runView struct {
	ID               int64             `json:"id"`
	WorkflowID       int64             `json:"workflow_id"`
	EventID          string            `json:"event_id"`
	Status           string            `json:"status"`
	SourceMessageID  int64             `json:"source_message_id"`
	OriginalContent  string            `json:"original_content"`
	PolishedContent  string            `json:"polished_content"`
	AIStatus         string            `json:"ai_status"`
	AIFallbackUsed   bool              `json:"ai_fallback_used"`
	AIModel          string            `json:"ai_model"`
	AIErrorCode      string            `json:"ai_error_code"`
	AIErrorMessage   string            `json:"ai_error_message"`
	AIInputTokens    int               `json:"ai_input_tokens"`
	AIOutputTokens   int               `json:"ai_output_tokens"`
	AIDurationMS     int64             `json:"ai_duration_ms"`
	ConfigRevision   int               `json:"config_revision"`
	AttemptCount     int               `json:"attempt_count"`
	LastErrorCode    string            `json:"last_error_code"`
	LastErrorMessage string            `json:"last_error_message"`
	CreatedAt        string            `json:"created_at"`
	StartedAt        *string           `json:"started_at"`
	CompletedAt      *string           `json:"completed_at"`
	Publications     []publicationView `json:"publications"`
}

type publicationView struct {
	ID               int64   `json:"id"`
	DestinationType  string  `json:"destination_type"`
	ClientKey        string  `json:"client_key"`
	ChannelID        int64   `json:"channel_id"`
	SiteID           int64   `json:"site_id"`
	Status           string  `json:"status"`
	RemoteMessageID  int64   `json:"remote_message_id"`
	RemotePostID     int64   `json:"remote_post_id"`
	RemoteURL        string  `json:"remote_url"`
	AttemptCount     int     `json:"attempt_count"`
	LastErrorCode    string  `json:"last_error_code"`
	LastErrorMessage string  `json:"last_error_message"`
	CompletedAt      *string `json:"completed_at"`
}

type statusView struct {
	Enabled        bool                `json:"enabled"`
	Workers        int                 `json:"workers"`
	PendingJobs    int                 `json:"pending_jobs"`
	RetryBacklog   int                 `json:"retry_backlog"`
	Processing     int                 `json:"processing"`
	FailedRecent   int                 `json:"failed_recent"`
	RecentFailures []statusFailureView `json:"recent_failures"`
}

type statusFailureView struct {
	JobType      string `json:"job_type"`
	TargetID     int64  `json:"target_id"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	At           string `json:"at"`
}

// Request shapes. Secret is write-only: nil leaves the stored secret alone and
// a non-nil value replaces it. No response ever carries one back.

type webhookRequest struct {
	Name             *string `json:"name"`
	Description      *string `json:"description"`
	Enabled          *bool   `json:"enabled"`
	URL              *string `json:"url"`
	Method           *string `json:"method"`
	AuthMode         *string `json:"auth_mode"`
	AuthHeader       *string `json:"auth_header"`
	Secret           *string `json:"secret"`
	TimeoutMS        *int64  `json:"timeout_ms"`
	MaxAttempts      *int    `json:"max_attempts"`
	SourceChannelIDs []int64 `json:"source_channel_ids"`
}

type workflowRequest struct {
	ManagedChannelID   *int64                        `json:"managed_channel_id"`
	Enabled            *bool                         `json:"enabled"`
	AIEnabled          *bool                         `json:"ai_enabled"`
	AIPrompt           *string                       `json:"ai_prompt"`
	AISendImages       *bool                         `json:"ai_send_images"`
	AIPublishOnFailure *bool                         `json:"ai_publish_on_failure"`
	Destinations       *[]workflowDestinationRequest `json:"destinations"`
}

type workflowDestinationRequest struct {
	DestinationType string         `json:"destination_type"`
	ClientKey       string         `json:"client_key"`
	ChannelID       int64          `json:"channel_id"`
	SiteID          int64          `json:"site_id"`
	Options         map[string]any `json:"options"`
	Position        int            `json:"position"`
	Enabled         *bool          `json:"enabled"`
}

// automation dispatches the background automation namespace.
//
// Permission model: automation.view admits the area and every read; writes to
// webhooks need webhooks.manage and writes to workflows need workflows.manage.
// Beyond the global right, a row is only readable when the caller can see its
// source channel, and a write must name only channels and sites the caller can
// already see. Telegram peer ids are never used for a permission check: the
// local managed_channels.id is.
func (s *Server) automation(w http.ResponseWriter, r *http.Request, requestID string) {
	authr := actorAuth(r)
	if !authr.Global(channelstore.PermissionAutomationView) {
		s.forbidden(w, requestID)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, s.fullPath(automationPrefix))
	switch {
	case rest == "", rest == "/", rest == "/status":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, http.MethodGet, requestID)
			return
		}
		s.automationStatus(w, r, requestID)
	case underPath(rest, "/webhooks"):
		s.automationWebhooks(w, r, requestID, strings.TrimPrefix(rest, "/webhooks"))
	case underPath(rest, "/workflows"):
		s.automationWorkflows(w, r, requestID, strings.TrimPrefix(rest, "/workflows"))
	default:
		s.notFound(w, requestID)
	}
}

// methodNotAllowed answers 405 with the Allow header the convention requires.
// It is called before an id is parsed, so an unsupported method gets 405 even
// when the id is malformed.
func (s *Server) methodNotAllowed(w http.ResponseWriter, allow, requestID string) {
	w.Header().Set("Allow", allow)
	s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
		"Only "+allow+" is supported here.", requestID)
}

func (s *Server) automationUnavailable(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusServiceUnavailable, CodeAutomationUnavailable,
		"The automation subsystem is switched off. Set \"automation\": {\"enabled\": true} "+
			"in the configuration file and restart the daemon.", requestID)
}

// --- webhooks ---

func (s *Server) automationWebhooks(w http.ResponseWriter, r *http.Request, requestID string, rest string) {
	if s.cfg.AutomationWebhooks == nil {
		s.automationUnavailable(w, requestID)
		return
	}
	if rest == "" || rest == "/" {
		switch r.Method {
		case http.MethodGet:
			s.listWebhooks(w, r, requestID)
		case http.MethodPost:
			s.createWebhook(w, r, requestID)
		default:
			s.methodNotAllowed(w, "GET, POST", requestID)
		}
		return
	}

	raw := strings.TrimPrefix(rest, "/")
	parts := strings.SplitN(raw, "/", 2)
	rawID, sub := parts[0], ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	// The sub-resource and its method decide first, so an unknown one is a 404
	// and an unsupported one is a 405, whatever the id looks like. A mistyped
	// path must not read as a malformed id.
	item, delivery := true, ""
	switch sub {
	case "":
		switch r.Method {
		case http.MethodGet, http.MethodPatch, http.MethodDelete:
		default:
			s.methodNotAllowed(w, "GET, PATCH, DELETE", requestID)
			return
		}
	case "test":
		item = false
		if r.Method != http.MethodPost {
			s.methodNotAllowed(w, http.MethodPost, requestID)
			return
		}
	case "deliveries":
		item = false
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, http.MethodGet, requestID)
			return
		}
	default:
		if !strings.HasPrefix(sub, "deliveries/") {
			s.notFound(w, requestID)
			return
		}
		delivery = strings.TrimPrefix(sub, "deliveries/")
		rest := delivery
		isRetry := strings.HasSuffix(rest, "/retry")
		if isRetry {
			rest = strings.TrimSuffix(rest, "/retry")
		}
		if rest == "" || strings.Contains(rest, "/") {
			s.notFound(w, requestID)
			return
		}
		if isRetry {
			if r.Method != http.MethodPost {
				s.methodNotAllowed(w, http.MethodPost, requestID)
				return
			}
		} else if r.Method != http.MethodGet {
			s.methodNotAllowed(w, http.MethodGet, requestID)
			return
		}
		item = false
	}

	id, err := parseID(rawID)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "id must be a positive integer.", requestID)
		return
	}

	switch {
	case item:
		switch r.Method {
		case http.MethodGet:
			s.getWebhook(w, r, requestID, id)
		case http.MethodPatch:
			s.updateWebhook(w, r, requestID, id)
		case http.MethodDelete:
			s.deleteWebhook(w, r, requestID, id)
		default:
			s.methodNotAllowed(w, "GET, PATCH, DELETE", requestID)
		}
	case sub == "test":
		s.testWebhook(w, r, requestID, id)
	case sub == "deliveries":
		s.listDeliveries(w, r, requestID, id)
	default:
		s.automationDelivery(w, r, requestID, id, delivery)
	}
}

func (s *Server) automationDelivery(w http.ResponseWriter, r *http.Request, requestID string, webhookID int64, rest string) {
	parts := strings.SplitN(rest, "/", 2)
	rawID, sub := parts[0], ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	switch sub {
	case "":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, http.MethodGet, requestID)
			return
		}
	case "retry":
		if r.Method != http.MethodPost {
			s.methodNotAllowed(w, http.MethodPost, requestID)
			return
		}
		if !actorAuth(r).Global(channelstore.PermissionWebhooksManage) {
			s.forbidden(w, requestID)
			return
		}
	default:
		s.notFound(w, requestID)
		return
	}
	deliveryID, err := parseID(rawID)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"delivery_id must be a positive integer.", requestID)
		return
	}
	if sub == "retry" {
		s.retryDelivery(w, r, requestID, webhookID, deliveryID)
		return
	}
	s.getDelivery(w, r, requestID, webhookID, deliveryID)
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request, requestID string) {
	page, perPage, q, err := parsePageQuery(r.URL.Query(), maxAutomationQ)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	enabled, err := parseEnabled(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	authr := actorAuth(r)
	result, err := s.cfg.AutomationWebhooks.ListWebhooks(r.Context(), automation.WebhookListOptions{
		Page: page, PerPage: perPage, Query: q, Enabled: enabled,
		Visible: func(localChannelID int64) bool {
			return visible(authr, channelstore.KindChannel, channelstore.ActionView, localChannelID)
		},
	})
	if err != nil {
		s.storeError(w, "list_webhooks", err, requestID)
		return
	}

	views := make([]webhookView, 0, len(result.Webhooks))
	for _, hook := range result.Webhooks {
		views = append(views, webhookViewOf(hook))
	}
	s.writeList(w, requestID, "webhooks", views, page, perPage, result.Total)
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request, requestID string, id int64) {
	hook, found, err := s.cfg.AutomationWebhooks.GetWebhook(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_webhook", err, requestID)
		return
	}
	if !found || !s.webhookVisible(actorAuth(r), hook) {
		s.notFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"webhook": webhookViewOf(hook)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request, requestID string) {
	if !actorAuth(r).Global(channelstore.PermissionWebhooksManage) {
		s.forbidden(w, requestID)
		return
	}
	var req webhookRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxAutomationBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if reason := s.webhookPreflight(r, req, true); reason != "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, reason, requestID)
		return
	}
	in := webhookInputOf(req, automation.WebhookInput{CreatedBy: actorOf(r).user.ID})
	if !s.webhookSourcesAllowed(w, r, requestID, in.SourceChannelIDs) {
		return
	}
	hook, err := s.cfg.AutomationWebhooks.CreateWebhook(r.Context(), in)
	if err != nil {
		s.automationWriteError(w, "create_webhook", err, requestID)
		return
	}
	writeJSON(w, http.StatusCreated, envelope{
		Data: map[string]any{"webhook": webhookViewOf(hook)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request, requestID string, id int64) {
	if !actorAuth(r).Global(channelstore.PermissionWebhooksManage) {
		s.forbidden(w, requestID)
		return
	}
	existing, found, err := s.cfg.AutomationWebhooks.GetWebhook(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_webhook", err, requestID)
		return
	}
	if !found || !s.webhookVisible(actorAuth(r), existing) {
		s.notFound(w, requestID)
		return
	}
	var req webhookRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxAutomationBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if reason := s.webhookPreflight(r, req, false); reason != "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, reason, requestID)
		return
	}
	seed := automation.WebhookInput{
		Name: existing.Name, Description: existing.Description, Enabled: existing.Enabled,
		URL: existing.URL, Method: existing.Method, AuthMode: existing.AuthMode,
		AuthHeader: existing.AuthHeader, Timeout: existing.Timeout,
		MaxAttempts: existing.MaxAttempts, SourceChannelIDs: existing.SourceChannelIDs,
	}
	in := webhookInputOf(req, seed)
	if !s.webhookSourcesAllowed(w, r, requestID, in.SourceChannelIDs) {
		return
	}
	hook, found, err := s.cfg.AutomationWebhooks.UpdateWebhook(r.Context(), id, in)
	if err != nil {
		s.automationWriteError(w, "update_webhook", err, requestID)
		return
	}
	if !found {
		s.notFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"webhook": webhookViewOf(hook)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request, requestID string, id int64) {
	if !actorAuth(r).Global(channelstore.PermissionWebhooksManage) {
		s.forbidden(w, requestID)
		return
	}
	existing, found, err := s.cfg.AutomationWebhooks.GetWebhook(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_webhook", err, requestID)
		return
	}
	if !found || !s.webhookVisible(actorAuth(r), existing) {
		s.notFound(w, requestID)
		return
	}
	if _, err := s.cfg.AutomationWebhooks.DeleteWebhook(r.Context(), id); err != nil {
		s.automationWriteError(w, "delete_webhook", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"deleted": true},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request, requestID string, id int64) {
	// A test is a write, not a read: it sends a request to the configured
	// address using the stored secret. The documentation grants "send tests"
	// with webhooks.manage, and without that right automation.view alone would
	// let a reader make this installation post to a URL of their choosing.
	if !actorAuth(r).Global(channelstore.PermissionWebhooksManage) {
		s.forbidden(w, requestID)
		return
	}
	existing, found, err := s.cfg.AutomationWebhooks.GetWebhook(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_webhook", err, requestID)
		return
	}
	if !found || !s.webhookVisible(actorAuth(r), existing) {
		s.notFound(w, requestID)
		return
	}
	result, found, err := s.cfg.AutomationWebhooks.TestWebhook(r.Context(), id)
	if err != nil {
		s.automationWriteError(w, "test_webhook", err, requestID)
		return
	}
	if !found {
		s.notFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"delivery": deliveryViewOf(result.Delivery)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request, requestID string, webhookID int64) {
	existing, found, err := s.cfg.AutomationWebhooks.GetWebhook(r.Context(), webhookID)
	if err != nil {
		s.storeError(w, "get_webhook", err, requestID)
		return
	}
	if !found || !s.webhookVisible(actorAuth(r), existing) {
		s.notFound(w, requestID)
		return
	}
	page, perPage, err := parseDeliveryQuery(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	result, err := s.cfg.AutomationWebhooks.ListDeliveries(r.Context(), webhookID,
		automation.DeliveryListOptions{Page: page, PerPage: perPage, Status: deliveryStatusOf(r.URL.Query())})
	if err != nil {
		s.storeError(w, "list_deliveries", err, requestID)
		return
	}
	views := make([]deliveryView, 0, len(result.Deliveries))
	for _, d := range result.Deliveries {
		views = append(views, deliveryViewOf(d))
	}
	s.writeList(w, requestID, "deliveries", views, page, perPage, result.Total)
}

func (s *Server) getDelivery(w http.ResponseWriter, r *http.Request, requestID string, webhookID, deliveryID int64) {
	existing, found, err := s.cfg.AutomationWebhooks.GetWebhook(r.Context(), webhookID)
	if err != nil {
		s.storeError(w, "get_webhook", err, requestID)
		return
	}
	if !found || !s.webhookVisible(actorAuth(r), existing) {
		s.notFound(w, requestID)
		return
	}
	delivery, found, err := s.cfg.AutomationWebhooks.GetDelivery(r.Context(), webhookID, deliveryID)
	if err != nil {
		s.storeError(w, "get_delivery", err, requestID)
		return
	}
	if !found {
		s.notFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"delivery": deliveryViewOf(delivery)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) retryDelivery(w http.ResponseWriter, r *http.Request, requestID string, webhookID, deliveryID int64) {
	// The webhook is asked about first, exactly as getDelivery does it: a retry
	// sends the source message to a URL of the operator's choosing, so a caller
	// who may not see the webhook's channels must not be able to trigger one. A
	// webhook they cannot see is a 404, so the route cannot be used to probe for
	// another account's configuration.
	existing, found, err := s.cfg.AutomationWebhooks.GetWebhook(r.Context(), webhookID)
	if err != nil {
		s.storeError(w, "get_webhook", err, requestID)
		return
	}
	if !found || !s.webhookVisible(actorAuth(r), existing) {
		s.notFound(w, requestID)
		return
	}
	delivery, found, err := s.cfg.AutomationWebhooks.RetryDelivery(r.Context(), webhookID, deliveryID)
	if err != nil {
		s.automationWriteError(w, "retry_delivery", err, requestID)
		return
	}
	if !found {
		s.notFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"delivery": deliveryViewOf(delivery)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// --- workflows ---

func (s *Server) automationWorkflows(w http.ResponseWriter, r *http.Request, requestID string, rest string) {
	if s.cfg.AutomationWorkflows == nil {
		s.automationUnavailable(w, requestID)
		return
	}
	switch rest {
	case "", "/":
		switch r.Method {
		case http.MethodGet:
			s.listWorkflows(w, r, requestID)
		case http.MethodPost:
			s.createWorkflow(w, r, requestID)
		default:
			s.methodNotAllowed(w, "GET, POST", requestID)
		}
		return
	}

	raw := strings.TrimPrefix(rest, "/")
	parts := strings.SplitN(raw, "/", 2)
	rawID, sub := parts[0], ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	// The sub-resource and its method decide first, so an unknown one is a 404
	// and an unsupported one is a 405, whatever the id looks like. A mistyped
	// path must not read as a malformed id.
	switch {
	case sub == "":
		switch r.Method {
		case http.MethodGet, http.MethodPatch, http.MethodDelete:
		default:
			s.methodNotAllowed(w, "GET, PATCH, DELETE", requestID)
			return
		}
	case sub == "runs", strings.HasPrefix(sub, "runs/"):
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, http.MethodGet, requestID)
			return
		}
		if sub != "runs" {
			if rest := strings.TrimPrefix(sub, "runs/"); rest == "" || strings.Contains(rest, "/") {
				s.notFound(w, requestID)
				return
			}
		}
	default:
		s.notFound(w, requestID)
		return
	}

	id, err := parseID(rawID)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "id must be a positive integer.", requestID)
		return
	}
	switch {
	case sub == "":
		switch r.Method {
		case http.MethodGet:
			s.getWorkflow(w, r, requestID, id)
		case http.MethodPatch:
			s.updateWorkflow(w, r, requestID, id)
		case http.MethodDelete:
			s.deleteWorkflow(w, r, requestID, id)
		default:
			s.methodNotAllowed(w, "GET, PATCH, DELETE", requestID)
		}
	case sub == "runs":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, http.MethodGet, requestID)
			return
		}
		s.listRuns(w, r, requestID, id)
	default:
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, http.MethodGet, requestID)
			return
		}
		runID, err := parseID(strings.TrimPrefix(sub, "runs/"))
		if err != nil {
			s.fail(w, http.StatusBadRequest, CodeInvalidInput,
				"run_id must be a positive integer.", requestID)
			return
		}
		s.getRun(w, r, requestID, id, runID)
	}
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request, requestID string) {
	page, perPage, q, err := parsePageQuery(r.URL.Query(), maxAutomationQ)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	enabled, err := parseEnabled(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	authr := actorAuth(r)
	result, err := s.cfg.AutomationWorkflows.ListWorkflows(r.Context(), automation.WorkflowListOptions{
		Page: page, PerPage: perPage, Query: q, Enabled: enabled,
		Visible: func(localChannelID int64) bool {
			return visible(authr, channelstore.KindChannel, channelstore.ActionView, localChannelID)
		},
	})
	if err != nil {
		s.storeError(w, "list_workflows", err, requestID)
		return
	}
	views := make([]workflowView, 0, len(result.Workflows))
	for i := range result.Workflows {
		s.withSourceChannel(r.Context(), &result.Workflows[i])
		views = append(views, workflowViewOf(result.Workflows[i]))
	}
	s.writeList(w, requestID, "workflows", views, page, perPage, result.Total)
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request, requestID string, id int64) {
	wf, found, err := s.cfg.AutomationWorkflows.GetWorkflow(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_workflow", err, requestID)
		return
	}
	if !found || !visible(actorAuth(r), channelstore.KindChannel, channelstore.ActionView, wf.ManagedChannelID) {
		s.notFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"workflow": s.workflowViewResolved(r.Context(), wf)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request, requestID string) {
	if !actorAuth(r).Global(channelstore.PermissionWorkflowsManage) {
		s.forbidden(w, requestID)
		return
	}
	var req workflowRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxAutomationBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if req.ManagedChannelID == nil || *req.ManagedChannelID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"managed_channel_id is required and must be a positive integer.", requestID)
		return
	}
	if !visible(actorAuth(r), channelstore.KindChannel, channelstore.ActionView, *req.ManagedChannelID) {
		s.fail(w, http.StatusForbidden, CodeAutomationSourceDenied,
			"The source channel is not visible to this account.", requestID)
		return
	}
	in, ok := s.workflowInputOf(w, r, requestID, req,
		automation.WorkflowInput{
			ManagedChannelID: *req.ManagedChannelID,
			CreatedBy:        actorOf(r).user.ID,
		})
	if !ok {
		return
	}
	// A workflow publishes under the authority of the account that created it,
	// re-read at every execution. The operator token is a credential and not an
	// account, so a workflow it creates has no authority to publish under and
	// would silently never run. It may be created for inspection; switching one
	// on needs a real owner.
	if in.Enabled && in.CreatedBy == 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"enabling a workflow needs an authenticated account: a workflow publishes under the "+
				"authority of the account that created it.", requestID)
		return
	}
	wf, err := s.cfg.AutomationWorkflows.CreateWorkflow(r.Context(), in)
	if err != nil {
		s.automationWriteError(w, "create_workflow", err, requestID)
		return
	}
	writeJSON(w, http.StatusCreated, envelope{
		Data: map[string]any{"workflow": s.workflowViewResolved(r.Context(), wf)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) updateWorkflow(w http.ResponseWriter, r *http.Request, requestID string, id int64) {
	if !actorAuth(r).Global(channelstore.PermissionWorkflowsManage) {
		s.forbidden(w, requestID)
		return
	}
	existing, found, err := s.cfg.AutomationWorkflows.GetWorkflow(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_workflow", err, requestID)
		return
	}
	if !found || !visible(actorAuth(r), channelstore.KindChannel, channelstore.ActionView, existing.ManagedChannelID) {
		s.notFound(w, requestID)
		return
	}
	var req workflowRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxAutomationBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	// The source channel is fixed at creation: a workflow is the automation of
	// one channel, and moving it would orphan the queued work behind it.
	if req.ManagedChannelID != nil && *req.ManagedChannelID != existing.ManagedChannelID {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"managed_channel_id cannot be changed; delete this workflow and create one for the other channel.",
			requestID)
		return
	}
	seed := automation.WorkflowInput{
		ManagedChannelID: existing.ManagedChannelID, Enabled: existing.Enabled,
		AIEnabled: existing.AIEnabled, AIPrompt: existing.AIPrompt,
		AISendImages: existing.AISendImages, AIPublishOnFailure: existing.AIPublishOnFailure,
		Destinations: existing.Destinations,
	}
	in, ok := s.workflowInputOf(w, r, requestID, req, seed)
	if !ok {
		return
	}
	wf, found, err := s.cfg.AutomationWorkflows.UpdateWorkflow(r.Context(), id, in)
	if err != nil {
		s.automationWriteError(w, "update_workflow", err, requestID)
		return
	}
	if !found {
		s.notFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"workflow": s.workflowViewResolved(r.Context(), wf)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request, requestID string, id int64) {
	if !actorAuth(r).Global(channelstore.PermissionWorkflowsManage) {
		s.forbidden(w, requestID)
		return
	}
	existing, found, err := s.cfg.AutomationWorkflows.GetWorkflow(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_workflow", err, requestID)
		return
	}
	if !found || !visible(actorAuth(r), channelstore.KindChannel, channelstore.ActionView, existing.ManagedChannelID) {
		s.notFound(w, requestID)
		return
	}
	if _, err := s.cfg.AutomationWorkflows.DeleteWorkflow(r.Context(), id); err != nil {
		s.automationWriteError(w, "delete_workflow", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"deleted": true},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, requestID string, workflowID int64) {
	wf, found, err := s.cfg.AutomationWorkflows.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		s.storeError(w, "get_workflow", err, requestID)
		return
	}
	if !found || !visible(actorAuth(r), channelstore.KindChannel, channelstore.ActionView, wf.ManagedChannelID) {
		s.notFound(w, requestID)
		return
	}
	page, perPage, err := parseDeliveryQuery(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	result, err := s.cfg.AutomationWorkflows.ListRuns(r.Context(), workflowID,
		automation.RunListOptions{Page: page, PerPage: perPage, Status: deliveryStatusOf(r.URL.Query())})
	if err != nil {
		s.storeError(w, "list_runs", err, requestID)
		return
	}
	views := make([]runView, 0, len(result.Runs))
	for _, run := range result.Runs {
		views = append(views, runViewOf(run))
	}
	s.writeList(w, requestID, "runs", views, page, perPage, result.Total)
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request, requestID string, workflowID, runID int64) {
	wf, found, err := s.cfg.AutomationWorkflows.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		s.storeError(w, "get_workflow", err, requestID)
		return
	}
	if !found || !visible(actorAuth(r), channelstore.KindChannel, channelstore.ActionView, wf.ManagedChannelID) {
		s.notFound(w, requestID)
		return
	}
	run, found, err := s.cfg.AutomationWorkflows.GetRun(r.Context(), workflowID, runID)
	if err != nil {
		s.storeError(w, "get_run", err, requestID)
		return
	}
	if !found {
		s.notFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"run": runViewOf(run)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// --- status ---

func (s *Server) automationStatus(w http.ResponseWriter, r *http.Request, requestID string) {
	view := statusView{Enabled: s.cfg.AutomationEnabled, Workers: s.cfg.AutomationWorkers}
	if s.cfg.AutomationStatus != nil {
		st, err := s.cfg.AutomationStatus.AutomationStatus(r.Context())
		if err != nil {
			s.storeError(w, "automation_status", err, requestID)
			return
		}
		view.PendingJobs = st.PendingJobs
		view.RetryBacklog = st.RetryBacklog
		view.Processing = st.Processing
		view.FailedRecent = st.FailedRecent
		view.RecentFailures = make([]statusFailureView, 0, len(st.RecentFailures))
		for _, f := range st.RecentFailures {
			view.RecentFailures = append(view.RecentFailures, statusFailureView{
				JobType: f.JobType, TargetID: f.TargetID,
				ErrorCode: f.ErrorCode, ErrorMessage: f.ErrorMessage,
				At: RFC3339String(f.At),
			})
		}
	}
	if view.RecentFailures == nil {
		view.RecentFailures = []statusFailureView{}
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"status": view},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// --- shared helpers ---

// writeList renders one page in the local-list shape the panel already reads:
// a named collection plus page metadata. total is the count the service
// reports after the caller's visibility restriction has been applied, so the
// page cannot claim rows the caller may not see.
func (s *Server) writeList(w http.ResponseWriter, requestID, name string, views any, page, perPage, total int) {
	meta := metaFor(requestID, s.cfg.Now())
	meta["page"] = page
	meta["per_page"] = perPage
	meta["total"] = total
	meta["total_pages"] = (total + perPage - 1) / perPage
	meta["has_more"] = page*perPage < total
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{name: views}, Meta: meta})
}

// webhookVisible reports whether a caller may see one webhook. A webhook leaks
// the content of every channel it subscribes to, so it is only visible when the
// caller can see at least one of those channels.
func (s *Server) webhookVisible(authr *channelstore.Authorizer, hook automation.Webhook) bool {
	if len(hook.SourceChannelIDs) == 0 {
		return false
	}
	for _, id := range hook.SourceChannelIDs {
		if visible(authr, channelstore.KindChannel, channelstore.ActionView, id) {
			return true
		}
	}
	return false
}

// withSourceChannel resolves the source channel's metadata onto a workflow, so
// a screen can name the channel without a second request.
//
// It is best effort on purpose: the workflow is readable either way and its
// managed_channel_id is already on the record, so a lookup that fails leaves
// the metadata absent rather than failing the whole response.
func (s *Server) withSourceChannel(ctx context.Context, wf *automation.Workflow) {
	store, ok := s.cfg.Channels.(LocalChannelStore)
	if !ok {
		return
	}
	row, found, err := store.GetByID(ctx, wf.ManagedChannelID)
	if err != nil || !found {
		return
	}
	wf.Channel = &automation.WorkflowSourceChannel{
		ID: row.ID, ClientKey: row.ClientKey, TelegramChannelID: row.ChannelID,
		Title: row.Title, Username: row.Username,
	}
}

// workflowViewResolved renders one workflow after resolving its source
// channel's metadata.
func (s *Server) workflowViewResolved(ctx context.Context, wf automation.Workflow) workflowView {
	s.withSourceChannel(ctx, &wf)
	return workflowViewOf(wf)
}

// parseEnabled reads the optional enabled filter.
func parseEnabled(q map[string][]string) (*bool, error) {
	raw, ok := singleParam(q, "enabled")
	if !ok {
		return nil, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, errors.New("enabled must be true or false.")
	}
	return &v, nil
}

// parseDeliveryQuery validates page/per_page for a history list, where the only
// extra parameter is status.
func parseDeliveryQuery(q map[string][]string) (int, int, error) {
	page, perPage := rpc.DefaultPage, rpc.DefaultPerPage
	for key, values := range q {
		if len(values) != 1 {
			return 0, 0, errors.New(key + " must be given exactly once.")
		}
		switch key {
		case "page":
			v, err := strconv.Atoi(values[0])
			if err != nil || v < 1 {
				return 0, 0, errors.New("page must be an integer of at least 1.")
			}
			page = v
		case "per_page":
			v, err := strconv.Atoi(values[0])
			if err != nil || v < 1 || v > rpc.MaxPerPage {
				return 0, 0, errors.New("per_page must be an integer between 1 and " +
					strconv.Itoa(rpc.MaxPerPage) + ".")
			}
			perPage = v
		case "status":
			if values[0] == "" {
				return 0, 0, errors.New("status must not be empty.")
			}
		default:
			return 0, 0, errors.New("unknown query parameter " + strconv.Quote(key) + ".")
		}
	}
	return page, perPage, nil
}

// deliveryStatusOf reads the optional status filter.
func deliveryStatusOf(q map[string][]string) string {
	raw, ok := singleParam(q, "status")
	if !ok {
		return ""
	}
	return raw
}

func singleParam(q map[string][]string, key string) (string, bool) {
	values, ok := q[key]
	if !ok || len(values) != 1 {
		return "", false
	}
	return values[0], true
}

// webhookPreflight validates the parts of a request the store cannot: the
// method, the authentication mode and the header name. It returns a reason to
// refuse, or "" when the request is acceptable.
func (s *Server) webhookPreflight(r *http.Request, req webhookRequest, creating bool) string {
	if req.Method != nil {
		switch *req.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			return "method must be one of POST, PUT or PATCH."
		}
	} else if creating {
		return "method is required."
	}
	if req.AuthMode != nil {
		switch *req.AuthMode {
		case "hmac", "bearer", "header", "none":
		default:
			return "auth_mode must be one of hmac, bearer, header or none."
		}
	}
	if req.AuthHeader != nil && *req.AuthHeader != "" && !validAuthHeader(*req.AuthHeader) {
		return "auth_header is not a usable HTTP header name."
	}
	if req.MaxAttempts != nil && (*req.MaxAttempts < 1 || *req.MaxAttempts > 100) {
		return "max_attempts must be between 1 and 100."
	}
	if req.TimeoutMS != nil && (*req.TimeoutMS < 100 || *req.TimeoutMS > 300_000) {
		return "timeout_ms must be between 100 and 300000."
	}
	return ""
}

// reservedHeaders are never accepted as a custom authentication header: they
// are transport headers the client must control, and letting a configuration
// overwrite them is how a webhook turns into a smuggling primitive.
var reservedHeaders = map[string]bool{
	"host":                true,
	"content-length":      true,
	"content-type":        true,
	"connection":          true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"te":                  true,
	"trailer":             true,
	"authorization":       true,
	"proxy-authorization": true,
	"webhook-id":          true,
	"webhook-timestamp":   true,
	"webhook-signature":   true,
}

// validAuthHeader reports whether a name may be used as a custom
// authentication header. http.CanonicalHeaderKey rejects anything that is not
// a usable HTTP field name, and the reserved set refuses the headers a client
// must control: letting a configuration overwrite those is how a webhook turns
// into a request-smuggling primitive.
//
// The whole webhook-* namespace is refused as well, not only the three names
// this build signs with: a configuration that could pick one would collide with
// the Standard Webhooks headers the very same request carries, and a later
// version that adds one would silently inherit the hole.
func validAuthHeader(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	if http.CanonicalHeaderKey(name) == "" {
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "webhook-") {
		return false
	}
	return !reservedHeaders[lower]
}

// webhookInputOf merges a request onto a seed. Validation that needs the
// request context already ran in webhookPreflight, so this only copies fields.
func webhookInputOf(req webhookRequest, seed automation.WebhookInput) automation.WebhookInput {
	in := seed
	if req.Name != nil {
		in.Name = *req.Name
	}
	if req.Description != nil {
		in.Description = *req.Description
	}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	if req.URL != nil {
		in.URL = *req.URL
	}
	if req.Method != nil {
		in.Method = *req.Method
	}
	if req.AuthMode != nil {
		in.AuthMode = *req.AuthMode
	}
	if req.AuthHeader != nil {
		in.AuthHeader = *req.AuthHeader
	}
	if req.Secret != nil {
		in.Secret = req.Secret
	}
	if req.TimeoutMS != nil {
		in.Timeout = time.Duration(*req.TimeoutMS) * time.Millisecond
	}
	if req.MaxAttempts != nil {
		in.MaxAttempts = *req.MaxAttempts
	}
	if req.SourceChannelIDs != nil {
		in.SourceChannelIDs = req.SourceChannelIDs
	}
	return in
}

// webhookSourcesAllowed reports whether the caller may see every source channel
// a webhook is being subscribed to. Subscribing a webhook to a channel hands it
// that channel's content, so naming one is not enough: the caller must already
// be able to read it. It writes its own refusal and reports false in that case.
func (s *Server) webhookSourcesAllowed(w http.ResponseWriter, r *http.Request, requestID string, ids []int64) bool {
	authr := actorAuth(r)
	for i, id := range ids {
		if id <= 0 {
			s.fail(w, http.StatusBadRequest, CodeInvalidInput,
				"source_channel_ids["+strconv.Itoa(i)+"] must be a positive integer.", requestID)
			return false
		}
		if !visible(authr, channelstore.KindChannel, channelstore.ActionView, id) {
			s.fail(w, http.StatusForbidden, CodeAutomationSourceDenied,
				"source_channel_ids["+strconv.Itoa(i)+"] is not visible to this account.", requestID)
			return false
		}
	}
	return true
}

// workflowInputOf merges a workflow request onto a seed and refuses any
// destination the caller cannot see. A destination is a place content will be
// sent, so naming one is not enough: the caller must already be able to post
// there. It writes its own refusal and reports false in that case.
func (s *Server) workflowInputOf(w http.ResponseWriter, r *http.Request, requestID string, req workflowRequest, seed automation.WorkflowInput) (automation.WorkflowInput, bool) {
	in := seed
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	if req.AIEnabled != nil {
		in.AIEnabled = *req.AIEnabled
	}
	if req.AIPrompt != nil {
		in.AIPrompt = *req.AIPrompt
	}
	if req.AISendImages != nil {
		in.AISendImages = *req.AISendImages
	}
	if req.AIPublishOnFailure != nil {
		in.AIPublishOnFailure = *req.AIPublishOnFailure
	}
	if req.Destinations == nil {
		return in, true
	}

	authr := actorAuth(r)
	dests := make([]automation.WorkflowDestination, 0, len(*req.Destinations))
	for i, d := range *req.Destinations {
		where := "destinations[" + strconv.Itoa(i) + "]"
		dest := automation.WorkflowDestination{
			DestinationType: d.DestinationType, ClientKey: d.ClientKey,
			ChannelID: d.ChannelID, SiteID: d.SiteID, Options: d.Options,
			Position: d.Position, Enabled: d.Enabled == nil || *d.Enabled,
		}
		switch d.DestinationType {
		case string(automation.DestinationTelegram):
			if strings.TrimSpace(d.ClientKey) == "" || d.ChannelID == 0 {
				s.fail(w, http.StatusBadRequest, CodeInvalidInput,
					where+" must name a client_key and a channel_id.", requestID)
				return automation.WorkflowInput{}, false
			}
			// The destination is addressed the way the manual publishing route
			// addresses one: a connection key and a Telegram peer id. The
			// permission rule only understands the local managed_channels row,
			// so the pair is resolved first and the row is what is asked about.
			target, err := s.targetOf(r.Context(), d.ClientKey, d.ChannelID)
			if err != nil {
				s.logf("gateway: request_id=%s automation destination lookup failed: %v", requestID, err)
			}
			if !access(authr, target).allowed {
				s.fail(w, http.StatusForbidden, CodeAutomationDestDenied,
					where+" is not visible to this account.", requestID)
				return automation.WorkflowInput{}, false
			}
		case string(automation.DestinationWordPress):
			if d.SiteID <= 0 {
				s.fail(w, http.StatusBadRequest, CodeInvalidInput,
					where+" must name a site_id.", requestID)
				return automation.WorkflowInput{}, false
			}
			if !visible(authr, channelstore.KindWordPress, channelstore.ActionView, d.SiteID) {
				s.fail(w, http.StatusForbidden, CodeAutomationDestDenied,
					where+" is not visible to this account.", requestID)
				return automation.WorkflowInput{}, false
			}
		default:
			s.fail(w, http.StatusBadRequest, CodeInvalidInput,
				where+" must be telegram or wordpress.", requestID)
			return automation.WorkflowInput{}, false
		}
		dests = append(dests, dest)
	}
	in.Destinations = dests
	return in, true
}

// automationWriteError maps a service failure onto the response, using the
// narrow codes the panel explains to the user.
func (s *Server) automationWriteError(w http.ResponseWriter, op string, err error, requestID string) {
	switch {
	case errors.Is(err, automation.ErrConflict):
		s.fail(w, http.StatusConflict, CodeAutomationConflict, err.Error(), requestID)
	case errors.Is(err, automation.ErrInvalid):
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
	case errors.Is(err, automation.ErrBaseURL):
		s.fail(w, http.StatusBadRequest, CodeAutomationBaseURLRequired, err.Error(), requestID)
	case errors.Is(err, automation.ErrNotFound):
		s.notFound(w, requestID)
	default:
		s.storeError(w, op, err, requestID)
	}
}

// --- view rendering ---

func webhookViewOf(hook automation.Webhook) webhookView {
	v := webhookView{
		ID: hook.ID, Name: hook.Name, Description: hook.Description,
		Enabled: hook.Enabled, URL: hook.URL, Method: hook.Method,
		AuthMode: hook.AuthMode, AuthHeader: hook.AuthHeader, HasSecret: hook.HasSecret,
		TimeoutMS: hook.Timeout.Milliseconds(), MaxAttempts: hook.MaxAttempts,
		SourceChannels: hook.SourceChannelIDs,
		CreatedAt:      RFC3339String(hook.CreatedAt), UpdatedAt: RFC3339String(hook.UpdatedAt),
	}
	if v.SourceChannels == nil {
		v.SourceChannels = []int64{}
	}
	if hook.LastDelivery != nil {
		v.LastDelivery = &webhookDeliverySummaryView{
			Status: hook.LastDelivery.Status, HTTPStatus: hook.LastDelivery.HTTPStatus,
			At: RFC3339String(hook.LastDelivery.At), AttemptCount: hook.LastDelivery.AttemptCount,
		}
	}
	if hook.Summary != nil {
		v.Summary = &webhookCountsView{
			Succeeded: hook.Summary.Succeeded, Failed: hook.Summary.Failed,
			Pending: hook.Summary.Pending,
		}
	}
	return v
}

func deliveryViewOf(d automation.WebhookDelivery) deliveryView {
	v := deliveryView{
		ID: d.ID, WebhookID: d.WebhookID, EventID: d.EventID, Status: d.Status,
		AttemptCount: d.AttemptCount, LastHTTPStatus: d.LastHTTPStatus,
		LastError: d.LastError,
		CreatedAt: RFC3339String(d.CreatedAt), UpdatedAt: RFC3339String(d.UpdatedAt),
		NextAttemptAt: timePtr(d.NextAttemptAt),
		CompletedAt:   timePtr(d.CompletedAt),
		Attempts:      make([]attemptView, 0, len(d.Attempts)),
	}
	if d.Request != nil {
		v.Request = &requestView{
			Method: d.Request.Method, URL: d.Request.URL,
			Headers: orEmptyHeaders(d.Request.Headers), Body: d.Request.Body,
			BodyTruncated: d.Request.BodyTruncated,
		}
	}
	for _, a := range d.Attempts {
		v.Attempts = append(v.Attempts, attemptView{
			ID: a.ID, AttemptNumber: a.AttemptNumber,
			RequestMethod: a.RequestMethod, RequestURL: a.RequestURL,
			RequestHeaders: orEmptyHeaders(a.RequestHeaders), RequestBody: a.RequestBody,
			RequestBodyTruncated: a.RequestBodyTruncated,
			ResponseStatus:       a.ResponseStatus, ResponseHeaders: orEmptyHeaders(a.ResponseHeaders),
			ResponseBody: a.ResponseBody, ResponseBodyTruncated: a.ResponseBodyTruncated,
			ResponseBytes: a.ResponseBytes, DurationMS: a.DurationMS,
			ErrorCode: a.ErrorCode, ErrorMessage: a.ErrorMessage,
			StartedAt: RFC3339String(a.StartedAt), FinishedAt: timePtr(a.FinishedAt),
		})
	}
	return v
}

func workflowViewOf(wf automation.Workflow) workflowView {
	v := workflowView{
		ID: wf.ID, ManagedChannelID: wf.ManagedChannelID, Enabled: wf.Enabled,
		AIEnabled: wf.AIEnabled, AIPrompt: wf.AIPrompt, AISendImages: wf.AISendImages,
		AIPublishOnFailure: wf.AIPublishOnFailure, ConfigRevision: wf.ConfigRevision,
		Destinations: make([]workflowDestinationView, 0, len(wf.Destinations)),
		CreatedAt:    RFC3339String(wf.CreatedAt), UpdatedAt: RFC3339String(wf.UpdatedAt),
	}
	for _, d := range wf.Destinations {
		opts := d.Options
		if opts == nil {
			opts = map[string]any{}
		}
		v.Destinations = append(v.Destinations, workflowDestinationView{
			ID: d.ID, DestinationType: d.DestinationType, ClientKey: d.ClientKey,
			ChannelID: d.ChannelID, SiteID: d.SiteID, Options: opts,
			Position: d.Position, Enabled: d.Enabled,
		})
	}
	if wf.Channel != nil {
		v.Channel = &sourceChannelView{
			ID: wf.Channel.ID, ClientKey: wf.Channel.ClientKey,
			TelegramChannelID: wf.Channel.TelegramChannelID,
			Title:             wf.Channel.Title, Username: wf.Channel.Username,
		}
	}
	if wf.LastRun != nil {
		v.LastRun = &workflowRunSummaryView{
			ID: wf.LastRun.ID, Status: wf.LastRun.Status,
			CreatedAt: RFC3339String(wf.LastRun.CreatedAt), CompletedAt: timePtr(wf.LastRun.CompletedAt),
		}
	}
	return v
}

func runViewOf(run automation.WorkflowRun) runView {
	v := runView{
		ID: run.ID, WorkflowID: run.WorkflowID, EventID: run.EventID, Status: run.Status,
		SourceMessageID: run.SourceMessageID,
		OriginalContent: run.OriginalContent, PolishedContent: run.PolishedContent,
		AIStatus: run.AIStatus, AIFallbackUsed: run.AIFallbackUsed, AIModel: run.AIModel,
		AIErrorCode: run.AIErrorCode, AIErrorMessage: run.AIErrorMessage,
		AIInputTokens: run.AIInputTokens, AIOutputTokens: run.AIOutputTokens,
		AIDurationMS: run.AIDurationMS, ConfigRevision: run.ConfigRevision,
		AttemptCount:  run.AttemptCount,
		LastErrorCode: run.LastErrorCode, LastErrorMessage: run.LastErrorMessage,
		CreatedAt:    RFC3339String(run.CreatedAt),
		StartedAt:    timePtr(run.StartedAt),
		CompletedAt:  timePtr(run.CompletedAt),
		Publications: make([]publicationView, 0, len(run.Publications)),
	}
	for _, p := range run.Publications {
		v.Publications = append(v.Publications, publicationView{
			ID: p.ID, DestinationType: p.DestinationType, ClientKey: p.ClientKey,
			ChannelID: p.ChannelID, SiteID: p.SiteID, Status: p.Status,
			RemoteMessageID: p.RemoteMessageID, RemotePostID: p.RemotePostID,
			RemoteURL: p.RemoteURL, AttemptCount: p.AttemptCount,
			LastErrorCode: p.LastErrorCode, LastErrorMessage: p.LastErrorMessage,
			CompletedAt: timePtr(p.CompletedAt),
		})
	}
	return v
}

func timePtr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := RFC3339String(*t)
	return &s
}

func orEmptyHeaders(h map[string]string) map[string]string {
	if h == nil {
		return map[string]string{}
	}
	return h
}
