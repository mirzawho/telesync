package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/secrets"
)

// Service is the webhook subsystem: configuration, delivery and history. It
// implements automation.WebhookService, which is what the HTTP gateway calls,
// and it is also the automation.JobHandler the queue runs.
//
// Authorization is the gateway's: by the time a method here is reached, the
// caller has already been allowed to see or change this webhook. What this
// layer enforces is referential integrity and the delivery rules that are not
// expressible as a permission check.
type Service struct {
	cfg     Config
	store   Store
	events  EventSource
	reopen  ReopenFunc
	build   *Builder
	secrets Secrets
	client  *http.Client
	sem     chan struct{}
	now     func() time.Time
	policy  Policy

	// sigMu guards lastSigTS, the timestamp the last signature was made with.
	sigMu     sync.Mutex
	lastSigTS int64
}

// New builds the webhook subsystem.
//
// It does not require a public base URL: a deployment may configure webhooks
// before it names an origin. Creating or enabling one without it is refused
// instead, because there would be no honest media address to put in a payload.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("webhooks: no store is configured")
	}
	if cfg.Secrets == nil {
		return nil, errors.New("webhooks: no secret store is configured")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	concurrency := cfg.Webhooks.MaxConcurrentDeliveries
	if concurrency <= 0 {
		concurrency = 1
	}

	client := NewClient(
		WithPolicy(cfg.Policy),
		WithTimeout(0), // each attempt bounds itself with its own deadline
		WithTransport(cfg.Transport),
		WithDial(cfg.Dial),
		WithLookup(cfg.Lookup),
	)

	return &Service{
		cfg:     cfg,
		store:   cfg.Store,
		events:  cfg.Events,
		reopen:  cfg.Reopen,
		build:   NewBuilder(cfg.PublicBaseURL, cfg.BasePath),
		secrets: cfg.Secrets,
		client:  client,
		sem:     make(chan struct{}, concurrency),
		now:     now,
		policy:  cfg.Policy,
	}, nil
}

// Handler is the queue's entry point for automation.JobWebhookDelivery.
func (s *Service) Handler() automation.JobHandler { return s.Handle }

// ListWebhooks returns one page of configured webhooks.
func (s *Service) ListWebhooks(ctx context.Context, opts automation.WebhookListOptions) (automation.WebhookPage, error) {
	return s.store.ListWebhooks(ctx, opts)
}

// GetWebhook returns one configured webhook. It never carries secret material:
// HasSecret is the only secret-related fact a reader gets.
func (s *Service) GetWebhook(ctx context.Context, id int64) (automation.Webhook, bool, error) {
	return s.store.GetWebhook(ctx, id)
}

// CreateWebhook stores one webhook and its source channels.
//
// in.Secret is write-only: the plaintext is sealed here and never kept. A
// deployment that has not named api.public_base_url is not refused: the payload
// then carries the media path rather than an absolute address, and the receiver
// prefixes the origin it already knows. Refusing the webhook instead would stop
// someone using it for everything else it does over one link they were happy to
// build themselves.
func (s *Service) CreateWebhook(ctx context.Context, in automation.WebhookInput) (automation.Webhook, error) {
	box, err := s.seal(in)
	if err != nil {
		return automation.Webhook{}, err
	}
	rec, err := recordOf(in)
	if err != nil {
		return automation.Webhook{}, err
	}
	// The rule is enforced here as well as in the store: a configuration that
	// would have nothing to authenticate with is refused before it is written.
	if !hasSecret(box) && rec.AuthMode != AuthNone {
		return automation.Webhook{}, fmt.Errorf("%w: auth_mode %s needs a secret", automation.ErrInvalid, rec.AuthMode)
	}
	return s.store.CreateWebhook(ctx, in.CreatedBy, rec, box)
}

// UpdateWebhook replaces one webhook's configuration.
//
// in.CreatedBy is ignored: ownership is fixed at creation and a request cannot
// reassign it. A nil secret keeps the stored one, so an edit never has to
// round-trip a credential through a client.
func (s *Service) UpdateWebhook(ctx context.Context, id int64, in automation.WebhookInput) (automation.Webhook, bool, error) {
	rec, err := recordOf(in)
	if err != nil {
		return automation.Webhook{}, false, err
	}
	rec.ID = id
	existing, found, err := s.store.GetWebhook(ctx, id)
	if err != nil {
		return automation.Webhook{}, false, err
	}
	if !found {
		return automation.Webhook{}, false, fmt.Errorf("%w: no webhook has id %d", automation.ErrNotFound, id)
	}
	box, err := s.seal(in)
	if err != nil {
		return automation.Webhook{}, false, err
	}
	// What the webhook will hold once this lands: unchanged when the request
	// says nothing about the secret, absent when it asks for that, replaced
	// otherwise.
	willHave := existing.HasSecret
	if box != nil {
		willHave = hasSecret(box)
	}
	if !willHave && rec.AuthMode != AuthNone {
		return automation.Webhook{}, false, fmt.Errorf("%w: auth_mode %s needs a secret", automation.ErrInvalid, rec.AuthMode)
	}
	return s.store.UpdateWebhook(ctx, rec, box)
}

// DeleteWebhook removes one webhook and everything recorded about it.
func (s *Service) DeleteWebhook(ctx context.Context, id int64) (bool, error) {
	return s.store.DeleteWebhook(ctx, id)
}

// ListDeliveries returns one page of one webhook's delivery history.
func (s *Service) ListDeliveries(ctx context.Context, webhookID int64, opts automation.DeliveryListOptions) (automation.WebhookDeliveryPage, error) {
	return s.store.ListWebhookDeliveries(ctx, webhookID, opts)
}

// GetDelivery returns one delivery with its attempts and the request actually
// transmitted, as recorded and already redacted.
func (s *Service) GetDelivery(ctx context.Context, webhookID, deliveryID int64) (automation.WebhookDelivery, bool, error) {
	rec, found, err := s.store.GetWebhookDelivery(ctx, deliveryID)
	if err != nil || !found {
		return automation.WebhookDelivery{}, false, err
	}
	// A delivery is only visible under the webhook it belongs to, so an
	// identifier cannot be used to read another webhook's history.
	if rec.WebhookID != webhookID {
		return automation.WebhookDelivery{}, false, nil
	}
	return rec, true, nil
}

// RetryDelivery reschedules one eligible delivery.
//
// It reuses the existing delivery row and the event that row already names: a
// retry is another attempt at the same delivery, never a second delivery. The
// attempt history keeps growing, so the record shows every attempt in order
// rather than overwriting the first one.
func (s *Service) RetryDelivery(ctx context.Context, webhookID, deliveryID int64) (automation.WebhookDelivery, bool, error) {
	rec, found, err := s.store.GetWebhookDelivery(ctx, deliveryID)
	if err != nil {
		return automation.WebhookDelivery{}, false, err
	}
	if !found || rec.WebhookID != webhookID {
		return automation.WebhookDelivery{}, false, nil
	}
	switch rec.Status {
	case StatusFailed, StatusCancelled, StatusPending:
	default:
		// A delivery that succeeded, or is being delivered right now, is not a
		// delivery to reschedule.
		return rec, false, fmt.Errorf("%w: this delivery is %s and cannot be retried", automation.ErrConflict, rec.Status)
	}

	now := s.now()
	rec.Status = StatusPending
	rec.NextAttemptAt = &now
	rec.CompletedAt = nil
	rec.LastError = ""
	saved, ok, err := s.store.UpdateWebhookDelivery(ctx, rec)
	if err != nil || !ok {
		return automation.WebhookDelivery{}, false, err
	}
	if err := s.reschedule(ctx, saved); err != nil {
		return automation.WebhookDelivery{}, false, err
	}
	return saved, true, nil
}

// reschedule puts one delivery back in the queue with a fresh attempt budget.
//
// The job's own budget is what a manual retry resets: without it a delivery
// that exhausted its attempts would be refused the moment it was reopened, and
// pressing "retry" would do nothing. A delivery whose job row does not exist
// yet is simply enqueued. A job that already succeeded or is running is refused
// as a conflict, because retrying either is how the same message is published
// twice.
func (s *Service) reschedule(ctx context.Context, delivery automation.WebhookDelivery) error {
	if s.reopen != nil {
		err := s.reopen(ctx, delivery.EventID, automation.JobWebhookDelivery, delivery.WebhookID)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, automation.ErrConflict):
			return err
		case errors.Is(err, automation.ErrNotFound):
			// No job row yet: fall through and create one.
		default:
			return err
		}
	}
	if s.cfg.Jobs != nil {
		return s.cfg.Jobs.EnqueueWebhookDelivery(ctx, delivery.EventID, delivery.WebhookID)
	}
	return nil
}

// TestWebhook sends one fixed payload to a webhook's configured address.
//
// It uses the same restricted transport a real delivery uses, so a test cannot
// reach anything a delivery could not, and it carries a fixed payload with no
// client key, no channel and no message content, so it cannot be used to read
// this installation's data through somebody else's endpoint. Nothing is written
// to delivery history: there is no event behind a test.
func (s *Service) TestWebhook(ctx context.Context, id int64) (automation.WebhookTestResult, bool, error) {
	wh, found, err := s.store.GetWebhook(ctx, id)
	if err != nil {
		return automation.WebhookTestResult{}, false, err
	}
	if !found {
		return automation.WebhookTestResult{}, false, nil
	}

	body, err := TestPayload()
	if err != nil {
		return automation.WebhookTestResult{}, false, err
	}
	// A test is not a delivery, so it gets its own stable id in the same shape:
	// a receiver that deduplicates on webhook-id never confuses one with the
	// other.
	logicalID := deliveryIDPrefix + "test_" + strconv.FormatInt(id, 10)

	target, err := ValidateTarget(wh.URL, s.policy)
	if err != nil {
		return automation.WebhookTestResult{}, true, testFailure(err)
	}
	secret, err := s.openSecret(ctx, wh)
	if err != nil {
		return automation.WebhookTestResult{}, true, testFailure(err)
	}

	started := s.now()
	signedAt := s.nextSignatureTime(started)
	req, err := http.NewRequestWithContext(ctx, wh.Method, target, strings.NewReader(string(body)))
	if err != nil {
		return automation.WebhookTestResult{}, true, testFailure(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "telesync-webhooks/1")
	signature := Signature{ID: logicalID, Timestamp: signedAt}
	switch strings.ToLower(strings.TrimSpace(wh.AuthMode)) {
	case AuthHMAC:
		signature, err = Sign(secret, logicalID, signedAt, body)
		if err != nil {
			return automation.WebhookTestResult{}, true, testFailure(err)
		}
		for name, value := range signature.Headers() {
			req.Header[name] = value
		}
	case AuthBearer:
		req.Header.Set(IDHeader, logicalID)
		req.Header.Set(TimestampHeader, signatureHeaderTimestamp(signedAt))
		req.Header.Set("Authorization", "Bearer "+secret)
	case AuthHeader:
		req.Header.Set(IDHeader, logicalID)
		req.Header.Set(TimestampHeader, signatureHeaderTimestamp(signedAt))
		if err := validAuthHeaderName(wh.AuthHeader); err != nil {
			return automation.WebhookTestResult{}, true, testFailure(err)
		}
		req.Header.Set(wh.AuthHeader, secret)
	default:
		req.Header.Set(IDHeader, logicalID)
		req.Header.Set(TimestampHeader, signatureHeaderTimestamp(signedAt))
	}

	timeout := wh.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	snapshot := redactedRequest(req, wh.AuthHeader)
	storedBody, truncated := boundString(string(body), MaxRecordedBodyBytes)

	resp, err := s.send(ctx, req, timeout)
	finished := s.now()
	att := automation.WebhookAttempt{
		AttemptNumber:        1,
		RequestMethod:        req.Method,
		RequestURL:           snapshot.URL,
		RequestHeaders:       snapshot.Headers,
		RequestBody:          storedBody,
		RequestBodyTruncated: truncated,
		StartedAt:            started,
		FinishedAt:           &finished,
		DurationMS:           finished.Sub(started).Milliseconds(),
	}
	status := StatusSucceeded
	if err != nil {
		code, retryable, message := classifyError(err)
		att.ErrorCode, att.ErrorMessage = code, message
		if retryable {
			status = StatusPending
		} else {
			status = StatusFailed
		}
		return automation.WebhookTestResult{Delivery: testDelivery(wh, att, status)}, true, nil
	}
	defer resp.Body.Close()

	raw, cut := readBounded(resp.Body, s.cfg.Webhooks.MaxResponseBytes)
	att.ResponseStatus = resp.StatusCode
	att.ResponseHeaders = redactedHeaderMap(resp.Header, wh.AuthHeader)
	att.ResponseBody = raw
	att.ResponseBodyTruncated = cut
	att.ResponseBytes = responseBytes(resp, len(raw))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _, message := classifyStatus(resp.StatusCode)
		att.ErrorCode, att.ErrorMessage = "http_status", message
		status = StatusFailed
	}
	return automation.WebhookTestResult{Delivery: testDelivery(wh, att, status)}, true, nil
}

// testFailure reports a test that could not be attempted at all. The refusal is
// the same one a real delivery would give, so a test cannot be used to learn
// something a delivery would not reveal.
func testFailure(err error) error {
	return fmt.Errorf("%w: %v", automation.ErrInvalid, err)
}

// testDelivery shapes one test exchange as a delivery, so a test is rendered by
// the same code a real delivery is.
func testDelivery(wh automation.Webhook, att automation.WebhookAttempt, status string) automation.WebhookDelivery {
	return automation.WebhookDelivery{
		WebhookID:    wh.ID,
		EventID:      "",
		Status:       status,
		AttemptCount: 1,
		Attempts:     []automation.WebhookAttempt{att},
		Request: &automation.WebhookRequestRecord{
			Method:        att.RequestMethod,
			URL:           att.RequestURL,
			Headers:       att.RequestHeaders,
			Body:          att.RequestBody,
			BodyTruncated: att.RequestBodyTruncated,
		},
	}
}

// seal turns a write-only plaintext secret into the sealed box the store keeps.
//
// Three states, and the distinction matters because a client can never read a
// stored secret back to decide what to send: nil leaves the stored one alone, an
// empty string removes it, and anything else rotates it. That is what the
// panel's "remove the stored secret" action sends, and it is why the empty
// string is not the same request as an absent field.
// hasSecret reports whether a sealed value is a real one rather than an explicit
// "none".
func hasSecret(box *secrets.Box) bool {
	return box != nil && (box.Version != 0 || len(box.Ciphertext) > 0)
}

func (s *Service) seal(in automation.WebhookInput) (*secrets.Box, error) {
	if in.Secret == nil {
		return nil, nil
	}
	if strings.TrimSpace(*in.Secret) == "" {
		empty := secrets.Box{}
		return &empty, nil
	}
	// The secret is checked where it is typed, not where it is first used. A
	// `whsec_` secret that cannot be decoded would otherwise be sealed happily
	// and only fail at the first delivery — a long way from the field the
	// operator entered it into, and with a stored credential nobody can use.
	if _, err := SigningKey(*in.Secret); err != nil {
		return nil, fmt.Errorf("%w: %s", automation.ErrInvalid, err)
	}
	box, err := s.secrets.Seal(*in.Secret)
	if err != nil {
		return nil, fmt.Errorf("%w: the secret could not be sealed", automation.ErrInvalid)
	}
	return &box, nil
}

// recordOf validates one create or update request and renders it as a record.
func recordOf(in automation.WebhookInput) (automation.Webhook, error) {
	rec := automation.Webhook{
		Name:             strings.TrimSpace(in.Name),
		Description:      strings.TrimSpace(in.Description),
		Enabled:          in.Enabled,
		URL:              strings.TrimSpace(in.URL),
		Method:           strings.ToUpper(strings.TrimSpace(in.Method)),
		AuthMode:         strings.ToLower(strings.TrimSpace(in.AuthMode)),
		AuthHeader:       strings.TrimSpace(in.AuthHeader),
		Timeout:          in.Timeout,
		MaxAttempts:      in.MaxAttempts,
		SourceChannelIDs: append([]int64{}, in.SourceChannelIDs...),
	}
	if rec.Method == "" {
		rec.Method = http.MethodPost
	}
	if rec.AuthMode == "" {
		rec.AuthMode = AuthHMAC
	}

	if rec.Name == "" {
		return automation.Webhook{}, fmt.Errorf("%w: name is required", automation.ErrInvalid)
	}
	// The structural half of the URL check happens here; whether the host is
	// somewhere this installation may connect is the transport's answer,
	// re-checked on every attempt.
	if err := validTargetShape(rec.URL); err != nil {
		return automation.Webhook{}, fmt.Errorf("%w: %v", automation.ErrInvalid, err)
	}
	switch rec.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return automation.Webhook{}, fmt.Errorf("%w: method must be POST, PUT or PATCH", automation.ErrInvalid)
	}
	switch rec.AuthMode {
	case AuthHMAC, AuthBearer, AuthHeader, AuthNone:
	default:
		return automation.Webhook{}, fmt.Errorf("%w: auth_mode must be hmac, bearer, header or none", automation.ErrInvalid)
	}
	if rec.AuthMode == AuthHeader {
		if err := validAuthHeaderName(rec.AuthHeader); err != nil {
			return automation.Webhook{}, fmt.Errorf("%w: %v", automation.ErrInvalid, err)
		}
	} else if rec.AuthHeader != "" {
		return automation.Webhook{}, fmt.Errorf("%w: auth_header is only used with auth_mode header", automation.ErrInvalid)
	}
	if rec.Timeout < 0 {
		return automation.Webhook{}, fmt.Errorf("%w: timeout must not be negative", automation.ErrInvalid)
	}
	if rec.MaxAttempts < 0 || rec.MaxAttempts > MaxDeliveryAttempts {
		return automation.Webhook{}, fmt.Errorf("%w: max_attempts must be between 1 and %d", automation.ErrInvalid, MaxDeliveryAttempts)
	}
	for _, id := range rec.SourceChannelIDs {
		if id <= 0 {
			return automation.Webhook{}, fmt.Errorf("%w: source channel ids must be positive integers", automation.ErrInvalid)
		}
	}
	return rec, nil
}

// validTargetShape is the structural half of the target check: an absolute
// http(s) address with a host, no credentials and no fragment. Whether the host
// is public is decided at delivery time, on every attempt.
func validTargetShape(raw string) error {
	if raw == "" {
		return errors.New("url is required")
	}
	if len(raw) > MaxTargetLen {
		return errors.New("url is too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("url is malformed")
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return errors.New("url must be an absolute http(s) address")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return errors.New("url must use http or https")
	}
	if u.User != nil {
		return errors.New("url must not carry credentials")
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return errors.New("url must not have a fragment")
	}
	return nil
}

// jsonMarshal is the one place a payload is serialized. Nothing re-serializes
// it afterwards: what is marshalled here is what is signed and what is sent.
func jsonMarshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("webhooks: the payload could not be serialized: %w", err)
	}
	return raw, nil
}
