package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/queue"
	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/automation/workflows"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/secrets"
)

// These helpers rehearse the automation routes the way the daemon wires them:
// one real SQLite store, the real webhook and workflow services over it, and
// the real queue they schedule work into. Only the network a webhook posts to
// is scripted, so a response the tests read is one the application produced and
// not a fixture's guess.

const (
	// autoPublicBaseURL stands for api.public_base_url: the origin a webhook
	// payload's media link is resolved against. Nothing connects to it.
	autoPublicBaseURL = "https://telesync.example"
	// autoBasePath is the panel mount the deployment uses.
	autoBasePath = "/admin"
	// autoWebhookURL is a plausible receiver. It is never dialled: the
	// environment's transport answers instead.
	autoWebhookURL = "https://receiver.example.com/hooks/telesync"
	// autoSecret is a webhook secret written by a caller. It must never appear
	// in any response or in any stored history record.
	autoSecret = "super-secret-value-never-echoed"
	// autoKey is the fixed 32-byte key the test seal uses. It stands for the
	// deployment's and is not a credential for anything.
	autoKey = "telesync-automation-key-32bytes!"
)

// autoTransport is the receiver a webhook posts to. Only the network is
// scripted: the request the application builds, the signature it computes and
// the attempt record it stores are all real. It keeps every request it saw so a
// test can compare what left the process with what the history kept.
type autoTransport struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	status   int
	response string
	err      error
}

func (t *autoTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		body = string(raw)
	}
	t.mu.Lock()
	t.requests = append(t.requests, req)
	t.bodies = append(t.bodies, body)
	status, answer, err := t.status, t.response, t.err
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(answer)),
		Request:    req,
	}, nil
}

// seen returns the requests that actually left the process.
func (t *autoTransport) seen() []*http.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*http.Request(nil), t.requests...)
}

// answer changes what the receiver replies with, so a test can watch a failure
// and a success on the same webhook.
func (t *autoTransport) answer(status int, body string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status, t.response, t.err = status, body, nil
}

// sentBodies returns the exact bytes each request carried.
func (t *autoTransport) sentBodies() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.bodies...)
}

// autoServices is one set of automation service instances over a store.
type autoServices struct {
	webhooks  *webhooks.Service
	workflows *workflows.Service
	status    *workflows.Status
	jobs      *queue.Enqueuer
}

// newAutoServices builds the real services over one store, exactly as
// cmd/telesync wires them: one seal for secrets, one queue for both job kinds,
// and the store's own event reader.
func newAutoServices(t *testing.T, store *channelstore.Store, box webhooks.Secrets,
	transport http.RoundTripper, basePath, publicBaseURL string) autoServices {

	t.Helper()
	enqueuer := queue.NewEnqueuer(store, 5)
	hooks, err := webhooks.New(webhooks.Config{
		PublicBaseURL: publicBaseURL,
		BasePath:      basePath,
		Webhooks: appconfig.WebhookConfig{
			Timeout:                 15 * time.Second,
			MaxAttempts:             5,
			MaxResponseBytes:        64 << 10,
			MaxConcurrentDeliveries: 4,
		},
		Store:     store,
		Secrets:   box,
		Events:    store.AutomationEvent,
		Jobs:      enqueuer,
		Reopen:    enqueuer.Reopen,
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("build the webhook service: %v", err)
	}
	return autoServices{
		webhooks:  hooks,
		workflows: &workflows.Service{Store: store},
		status:    &workflows.Status{Store: store},
		jobs:      enqueuer,
	}
}

// autoEnv is an accountEnv with the automation subsystem wired in.
type autoEnv struct {
	*accountEnv
	transport *autoTransport
	encrypter *secrets.Encrypter
	services  autoServices
	// basePath and publicBaseURL are the two deployment settings the media link
	// in a webhook payload is built from, so a test can move either one and see
	// what the payload says.
	basePath      string
	publicBaseURL string
}

// newAutoEnv builds the gateway over one real store with the real automation
// services. mutate runs on the finished configuration, so a test can change one
// field — the public origin, the mount, the services — and keep the rest.
func newAutoEnv(t *testing.T, mutate ...func(*gateway.Config)) *autoEnv {
	t.Helper()
	env := newAccountEnv(t, registry{"official": readyWorker()})
	encrypter, err := secrets.NewEncrypter([]byte(autoKey))
	if err != nil {
		t.Fatal(err)
	}
	e := &autoEnv{
		accountEnv:    env,
		transport:     &autoTransport{status: http.StatusOK, response: `{"received":true}`},
		encrypter:     encrypter,
		publicBaseURL: autoPublicBaseURL,
	}
	e.rewire(t, mutate...)
	return e
}

// rewire builds fresh service instances over the suite's store and installs a
// handler built from them, so a test can drop everything the previous requests
// may have held in memory and prove the database is what answered.
func (e *autoEnv) rewire(t *testing.T, mutate ...func(*gateway.Config)) {
	t.Helper()
	cfg := e.handlerConfig()
	cfg.Logf = t.Logf
	e.services = newAutoServices(t, e.store, e.encrypter, e.transport, e.basePath, e.publicBaseURL)
	cfg.AutomationWebhooks = e.services.webhooks
	cfg.AutomationWorkflows = e.services.workflows
	cfg.AutomationStatus = e.services.status
	cfg.AutomationEnabled = true
	cfg.AutomationWorkers = 2
	cfg.BasePath = e.basePath
	cfg.PublicBaseURL = e.publicBaseURL
	for _, fn := range mutate {
		fn(&cfg)
	}
	e.useConfig(cfg)
}

// restart issues a request through a handler that shares no memory with the one
// that wrote the row: new services, new router, same SQLite file. It is what
// makes "the value is still there" a statement about persistence.
func (e *autoEnv) restart(t *testing.T, method, who, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	e.rewire(t)
	return e.call(t, who, method, path, body)
}

// call sends one request and hands back the raw recorder, so a test can read the
// exact bytes, the status and the headers the gateway wrote. The path is the one
// the API documents, without the mount; the suite's own BasePath is applied
// here, so a test written against /api/v1 runs the same under /admin.
func (e *autoEnv) call(t *testing.T, who, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, e.basePath+path, strings.NewReader(body))
	req.Header.Set("X-Request-Id", "auto-test")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if who == defaultCaller {
		who = e.session
		if e.signedIn {
			who = e.defaults.token
		}
	}
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+who)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

// grantChannelView gives one account an explicit view of one managed channel.
func (e *autoEnv) grantChannelView(t *testing.T, who identity, ids ...int64) {
	t.Helper()
	sets := make([]channelstore.ResourcePermissions, 0, len(ids))
	for _, id := range ids {
		sets = append(sets, channelstore.ResourcePermissions{
			ResourceID:  id,
			Permissions: []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: channelstore.EffectAllow}},
		})
	}
	e.grantChannels(t, who, sets...)
}

// denyChannelView gives one account an explicit view:deny on one managed
// channel, which overrides a global "channels.view".
func (e *autoEnv) denyChannelView(t *testing.T, who identity, ids ...int64) {
	t.Helper()
	sets := make([]channelstore.ResourcePermissions, 0, len(ids))
	for _, id := range ids {
		sets = append(sets, channelstore.ResourcePermissions{
			ResourceID:  id,
			Permissions: []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: channelstore.EffectDeny}},
		})
	}
	e.grantChannels(t, who, sets...)
}

// channelWithID records one managed channel and returns its local row id, which
// is the only id a permission check may use.
func (e *autoEnv) channelWithID(t *testing.T, telegramID int64) int64 {
	t.Helper()
	return e.channelRow(t, "official", telegramID).ID
}

// storeWebhook reads one webhook straight out of SQLite, through the store's own
// SELECT. It is the row the request was supposed to leave behind.
func (e *autoEnv) storeWebhook(t *testing.T, id int64) automation.Webhook {
	t.Helper()
	rec, found, err := e.store.GetWebhook(context.Background(), id)
	if err != nil {
		t.Fatalf("read webhook %d from the store: %v", id, err)
	}
	if !found {
		t.Fatalf("webhook %d is not in the store", id)
	}
	return rec
}

// storeWorkflow reads one workflow straight out of SQLite.
func (e *autoEnv) storeWorkflow(t *testing.T, id int64) automation.Workflow {
	t.Helper()
	rec, found, err := e.store.GetWorkflow(context.Background(), id)
	if err != nil {
		t.Fatalf("read workflow %d from the store: %v", id, err)
	}
	if !found {
		t.Fatalf("workflow %d is not in the store", id)
	}
	return rec
}

// storeDelivery reads one delivery, with its attempts, straight out of SQLite.
func (e *autoEnv) storeDelivery(t *testing.T, id int64) automation.WebhookDelivery {
	t.Helper()
	rec, found, err := e.store.GetWebhookDelivery(context.Background(), id)
	if err != nil {
		t.Fatalf("read delivery %d from the store: %v", id, err)
	}
	if !found {
		t.Fatalf("delivery %d is not in the store", id)
	}
	return rec
}

// seedEvent stores one source message event, so a delivery has something to
// carry. The fields are the ones validateAutomationEvent requires.
func (e *autoEnv) seedEvent(t *testing.T, managedChannelID, telegramChannelID, messageID int64) automation.MessageEvent {
	t.Helper()
	event := automation.MessageEvent{
		ID: "evt_test_" + strconv.FormatInt(managedChannelID, 10) +
			"_" + strconv.FormatInt(messageID, 10),
		Type:              automation.EventMessageCreated,
		NetworkKey:        "official",
		SourceClientKey:   "official",
		ManagedChannelID:  managedChannelID,
		TelegramChannelID: telegramChannelID,
		MessageID:         messageID,
		OccurredAt:        time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC),
		ReceivedAt:        time.Date(2026, 11, 5, 12, 30, 1, 0, time.UTC),
		Payload: automation.MessagePayload{
			Message: rpc.Message{
				ID:   int(messageID),
				Date: time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC),
				Text: "Example message",
				Media: &rpc.Media{
					ID: "1:1", Type: "document", Label: "document",
					Filename: "example.apk", MimeType: "application/vnd.android.package-archive",
					Size: 59797754,
				},
			},
			MediaURLPath: automation.MediaURLPath("official", telegramChannelID, int(messageID)),
		},
	}
	if _, err := e.store.InsertMessageEvent(context.Background(), event); err != nil {
		t.Fatalf("store the source event: %v", err)
	}
	return event
}

// deliver runs one real webhook delivery through the real engine, so the
// history routes have a genuine row to report. The engine is called, never
// rewritten.
func (e *autoEnv) deliver(t *testing.T, webhookID int64, event automation.MessageEvent) automation.WebhookDelivery {
	t.Helper()
	err := e.services.webhooks.Handle(context.Background(), automation.Job{
		EventID:     event.ID,
		Kind:        automation.JobWebhookDelivery,
		TargetID:    webhookID,
		Attempt:     1,
		MaxAttempts: 5,
	})
	if err != nil {
		t.Logf("the delivery job reported: %v", err)
	}
	page, err := e.store.ListWebhookDeliveries(context.Background(), webhookID,
		automation.DeliveryListOptions{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(page.Deliveries) != 1 {
		t.Fatalf("deliveries stored = %d, want 1", len(page.Deliveries))
	}
	return page.Deliveries[0]
}

// autoDo sends one request through an explicit handler, for a test that holds
// two handlers at once (the mounted and unmounted forms).
func autoDo(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("X-Request-Id", "auto-test")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// --- response shapes ---

type autoWebhookView struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Enabled        bool    `json:"enabled"`
	URL            string  `json:"url"`
	Method         string  `json:"method"`
	AuthMode       string  `json:"auth_mode"`
	AuthHeader     string  `json:"auth_header"`
	HasSecret      bool    `json:"has_secret"`
	TimeoutMS      int64   `json:"timeout_ms"`
	MaxAttempts    int     `json:"max_attempts"`
	SourceChannels []int64 `json:"source_channel_ids"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type autoWorkflowView struct {
	ID               int64  `json:"id"`
	ManagedChannelID int64  `json:"managed_channel_id"`
	Enabled          bool   `json:"enabled"`
	AIEnabled        bool   `json:"ai_enabled"`
	AIPrompt         string `json:"ai_prompt"`
	AISendImages     bool   `json:"ai_send_images"`
	AIPublishOnFail  bool   `json:"ai_publish_on_failure"`
	ConfigRevision   int    `json:"config_revision"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	Destinations     []struct {
		DestinationType string         `json:"destination_type"`
		ClientKey       string         `json:"client_key"`
		ChannelID       int64          `json:"channel_id"`
		SiteID          int64          `json:"site_id"`
		Options         map[string]any `json:"options"`
		Position        int            `json:"position"`
		Enabled         bool           `json:"enabled"`
	} `json:"destinations"`
	Channel *struct {
		ID                int64  `json:"id"`
		ClientKey         string `json:"client_key"`
		TelegramChannelID int64  `json:"telegram_channel_id"`
		Title             string `json:"title"`
		Username          string `json:"username"`
	} `json:"channel"`
}

type autoDeliveryView struct {
	ID             int64   `json:"id"`
	WebhookID      int64   `json:"webhook_id"`
	EventID        string  `json:"event_id"`
	Status         string  `json:"status"`
	AttemptCount   int     `json:"attempt_count"`
	NextAttemptAt  *string `json:"next_attempt_at"`
	LastHTTPStatus int     `json:"last_http_status"`
	LastError      string  `json:"last_error"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	CompletedAt    *string `json:"completed_at"`
	Request        *struct {
		Method        string            `json:"method"`
		URL           string            `json:"url"`
		Headers       map[string]string `json:"headers"`
		Body          string            `json:"body"`
		BodyTruncated bool              `json:"body_truncated"`
	} `json:"request"`
	Attempts []struct {
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
	} `json:"attempts"`
}

type autoRunView struct {
	ID              int64  `json:"id"`
	WorkflowID      int64  `json:"workflow_id"`
	EventID         string `json:"event_id"`
	Status          string `json:"status"`
	SourceMessageID int64  `json:"source_message_id"`
	OriginalContent string `json:"original_content"`
	PolishedContent string `json:"polished_content"`
	AIStatus        string `json:"ai_status"`
	ConfigRevision  int    `json:"config_revision"`
	AttemptCount    int    `json:"attempt_count"`
	CreatedAt       string `json:"created_at"`
	Publications    []any  `json:"publications"`
}

type autoStatusView struct {
	Enabled        bool  `json:"enabled"`
	Workers        int   `json:"workers"`
	PendingJobs    int   `json:"pending_jobs"`
	RetryBacklog   int   `json:"retry_backlog"`
	Processing     int   `json:"processing"`
	FailedRecent   int   `json:"failed_recent"`
	RecentFailures []any `json:"recent_failures"`
}

// autoMeta is the metadata every answer carries. A list adds its own fields.
type autoMeta struct {
	RequestID   string `json:"request_id"`
	GeneratedAt string `json:"generated_at"`
	Page        *int   `json:"page"`
	PerPage     *int   `json:"per_page"`
	Total       *int   `json:"total"`
	TotalPages  *int   `json:"total_pages"`
	HasMore     *bool  `json:"has_more"`
}

// autoWebhooksBody is the collection answer.
type autoWebhooksBody struct {
	Webhooks []autoWebhookView `json:"webhooks"`
}
type autoDeliveriesBody struct {
	Deliveries []autoDeliveryView `json:"deliveries"`
}
type autoWorkflowsBody struct {
	Workflows []autoWorkflowView `json:"workflows"`
}
type autoRunsBody struct {
	Runs []autoRunView `json:"runs"`
}
type autoWebhookBody struct {
	Webhook autoWebhookView `json:"webhook"`
}
type autoWorkflowBody struct {
	Workflow autoWorkflowView `json:"workflow"`
}
type autoDeliveryBody struct {
	Delivery autoDeliveryView `json:"delivery"`
}
type autoRunBody struct {
	Run autoRunView `json:"run"`
}
type autoStatusBody struct {
	Status autoStatusView `json:"status"`
}
type autoDeletedBody struct {
	Deleted bool `json:"deleted"`
}

// autoDecode reads one successful answer's envelope and its data.
func autoDecode(t *testing.T, rec *httptest.ResponseRecorder, data any) autoMeta {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
		Meta json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode the envelope: %v (body %s)", err, rec.Body.String())
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		t.Fatalf("the envelope carries no data: %s", rec.Body.String())
	}
	if len(env.Meta) == 0 || string(env.Meta) == "null" {
		t.Fatalf("the envelope carries no meta: %s", rec.Body.String())
	}
	if data != nil {
		if err := json.Unmarshal(env.Data, data); err != nil {
			t.Fatalf("decode data: %v (data %s)", err, env.Data)
		}
	}
	var meta autoMeta
	if err := json.Unmarshal(env.Meta, &meta); err != nil {
		t.Fatalf("decode meta: %v (meta %s)", err, env.Meta)
	}
	if meta.RequestID == "" {
		t.Fatalf("meta carries no request_id: %s", env.Meta)
	}
	if meta.GeneratedAt == "" {
		t.Fatalf("meta carries no generated_at: %s", env.Meta)
	}
	return meta
}

// autoFail decodes one refusal and checks the documented error envelope.
func autoFail(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, wantStatus, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the error envelope: %v (body %s)", err, rec.Body.String())
	}
	if body.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q (body %s)", body.Error.Code, wantCode, rec.Body.String())
	}
	if body.Error.Message == "" {
		t.Fatalf("error.message is empty: %s", rec.Body.String())
	}
	if body.Error.RequestID == "" {
		t.Fatalf("error.request_id is empty: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("the answer is not the error envelope: %s", rec.Body.String())
	}
}

// autoOK refuses anything but a 200.
func autoOK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

func autoCreated(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
}

// autoWebhookPOST is the create body every test starts from. It is enabled
// because the standard environment does name api.public_base_url, and a
// disabled webhook is one no delivery will run.
func autoWebhookPOST(name string, channelIDs ...int64) string {
	body := map[string]any{
		"name":               name,
		"description":        "one outbound webhook",
		"enabled":            true,
		"url":                autoWebhookURL,
		"method":             "POST",
		"auth_mode":          "hmac",
		"secret":             autoSecret,
		"timeout_ms":         5000,
		"max_attempts":       5,
		"source_channel_ids": channelIDs,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// autoJSON renders a request body.
func autoJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
