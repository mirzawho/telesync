package webhooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
)

// The delivery engine runs one attempt and records what happened. It does not
// decide when to retry — that is the queue's job — but it does decide whether
// another attempt could ever succeed, and it says so in the *automation.JobError
// it returns.

// attemptResult is what one HTTP exchange produced.
type attemptResult struct {
	attempt   automation.WebhookAttempt
	status    string
	retryable bool
	// retryAfter is a receiver-supplied delay, already bounded.
	retryAfter time.Duration
	code       string
	message    string
}

// Handle runs one claimed webhook delivery job.
func (s *Service) Handle(ctx context.Context, job automation.Job) error {
	if job.Kind != automation.JobWebhookDelivery {
		return &automation.JobError{
			Code:      "unknown_job",
			Message:   fmt.Sprintf("this handler does not run %q jobs", job.Kind),
			Retryable: false,
		}
	}

	wh, found, err := s.store.GetWebhook(ctx, job.TargetID)
	if err != nil {
		return s.retryable("store", err)
	}
	if !found {
		return &automation.JobError{
			Code:      "webhook_not_found",
			Message:   "the webhook no longer exists",
			Retryable: false,
		}
	}

	delivery, err := s.ensureDelivery(ctx, job)
	if err != nil {
		return err
	}

	// Bounded retries, twice over: the queue's own budget, and this package's
	// hard ceiling. Neither can be talked out of another attempt.
	if job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts {
		return s.finish(ctx, delivery, attemptResult{
			status:    StatusFailed,
			code:      "attempts_exhausted",
			message:   fmt.Sprintf("the delivery was attempted %d times without success", delivery.AttemptCount),
			retryable: false,
		})
	}
	if delivery.AttemptCount >= MaxDeliveryAttempts {
		return s.finish(ctx, delivery, attemptResult{
			status:    StatusFailed,
			code:      "attempts_exhausted",
			message:   fmt.Sprintf("the delivery reached the %d attempt ceiling", MaxDeliveryAttempts),
			retryable: false,
		})
	}

	if !wh.Enabled {
		return s.finish(ctx, delivery, attemptResult{
			status:    StatusCancelled,
			code:      "webhook_disabled",
			message:   "the webhook is disabled",
			retryable: false,
		})
	}

	body, err := s.bodyFor(ctx, delivery, job.EventID)
	if err != nil {
		var gone *eventGoneError
		if errors.As(err, &gone) {
			return s.finish(ctx, delivery, attemptResult{
				status:    StatusCancelled,
				code:      "event_missing",
				message:   "the event this delivery carries no longer exists",
				retryable: false,
			})
		}
		return s.retryable("payload", err)
	}

	// The attempt records its own outcome; there is nothing left for finish to
	// decide, so the job error comes straight from what the exchange produced.
	return s.attempt(ctx, wh, delivery, body).jobError()
}

// ensureDelivery finds the one delivery of this event to this webhook, creating
// it if this is the first attempt. There is never a second row: the schema's
// UNIQUE (webhook_id, event_id) refuses one, and a race here is resolved by
// reading the winner's row rather than by creating another.
func (s *Service) ensureDelivery(ctx context.Context, job automation.Job) (automation.WebhookDelivery, error) {
	delivery, found, err := s.store.WebhookDeliveryForEvent(ctx, job.TargetID, job.EventID)
	if err != nil {
		return automation.WebhookDelivery{}, s.retryable("store", err)
	}
	if found {
		return delivery, nil
	}
	created, err := s.store.CreateWebhookDelivery(ctx, automation.WebhookDelivery{
		WebhookID: job.TargetID,
		EventID:   job.EventID,
		Status:    StatusPending,
	})
	switch {
	case err == nil:
		return created, nil
	case errors.Is(err, automation.ErrConflict):
		// Another worker created it first. Its row is the delivery.
		existing, ok, readErr := s.store.WebhookDeliveryForEvent(ctx, job.TargetID, job.EventID)
		if readErr != nil {
			return automation.WebhookDelivery{}, s.retryable("store", readErr)
		}
		if !ok {
			return automation.WebhookDelivery{}, s.retryable("store", err)
		}
		return existing, nil
	default:
		return automation.WebhookDelivery{}, s.retryable("store", err)
	}
}

// eventGoneError means the event a delivery carries has disappeared.
type eventGoneError struct{ id string }

func (e *eventGoneError) Error() string {
	return "webhooks: event " + e.id + " is no longer stored"
}

// bodyFor returns the exact bytes this attempt transmits.
//
// A retry reuses the previous attempt's stored request body when it is whole.
// That is what makes "the signed payload snapshot is stored separately from
// secret material so retries rebuild reliably" true: the bytes that were signed
// are the bytes that are re-sent, and nothing re-serializes JSON after signing.
// Only when no whole snapshot exists is the payload rebuilt from the event.
func (s *Service) bodyFor(ctx context.Context, delivery automation.WebhookDelivery, eventID string) ([]byte, error) {
	if previous, ok, err := s.store.LatestWebhookAttempt(ctx, delivery.ID); err == nil && ok {
		if !previous.RequestBodyTruncated && previous.RequestBody != "" {
			return []byte(previous.RequestBody), nil
		}
	}

	if s.events == nil {
		return nil, errors.New("webhooks: no event source is configured")
	}
	event, found, err := s.events(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &eventGoneError{id: eventID}
	}
	channel, err := s.channelFor(ctx, event.ManagedChannelID)
	if err != nil {
		return nil, err
	}
	env, err := s.build.Build(event, channel)
	if err != nil {
		return nil, err
	}
	return jsonMarshal(env)
}

// channelFor reads the labels of the managed channel an event came from.
//
// A channel row that is no longer there is not an error: the event's own
// identifiers still say where the message came from, and a delivery must not
// fail over a name the store has dropped. A lookup that fails is a real failure
// and is reported as one, rather than quietly sending a payload missing the
// channel the operator asked receivers to see.
func (s *Service) channelFor(ctx context.Context, managedChannelID int64) (Channel, error) {
	if s.channels == nil {
		return Channel{}, nil
	}
	channel, found, err := s.channels(ctx, managedChannelID)
	if err != nil {
		return Channel{}, fmt.Errorf("webhooks: read source channel %d: %w", managedChannelID, err)
	}
	if !found {
		return Channel{}, nil
	}
	return channel, nil
}

// attempt performs one HTTP exchange and records it.
//
// The URL is validated here as well as at configuration time: a target that was
// safe when it was written is not thereby safe now. The request is built once,
// signed once and sent as those exact bytes, and the record keeps the same
// bytes with credential-bearing values replaced.
func (s *Service) attempt(ctx context.Context, wh automation.Webhook, delivery automation.WebhookDelivery, body []byte) attemptResult {
	attemptNumber := delivery.AttemptCount + 1
	logicalID := deliveryIDPrefix + strconv.FormatInt(delivery.ID, 10)
	started := s.now()
	// A retry never reuses a previous attempt's timestamp, even when it lands in
	// the same second: a receiver that rejects a replayed (id, timestamp,
	// signature) triple must always be handed a fresh one.
	signedAt := s.nextSignatureTime(started)

	target, err := ValidateTarget(wh.URL, s.policy.ForWebhook(wh.AllowLocalhost))
	if err != nil {
		return s.record(ctx, delivery, attemptNumber, attemptResult{
			status:    StatusFailed,
			code:      "invalid_target",
			message:   err.Error(),
			retryable: false,
		})
	}

	secret, err := s.openSecret(ctx, wh)
	if err != nil {
		return s.record(ctx, delivery, attemptNumber, attemptResult{
			status:    StatusFailed,
			code:      "secret_unavailable",
			message:   "the webhook's secret could not be read",
			retryable: false,
		})
	}

	req, err := http.NewRequestWithContext(ctx, wh.Method, target, bytes.NewReader(body))
	if err != nil {
		return s.record(ctx, delivery, attemptNumber, attemptResult{
			status:    StatusFailed,
			code:      "invalid_target",
			message:   err.Error(),
			retryable: false,
		})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "telesync-webhooks/1")

	// The delivery id and the timestamp are not secret and every mode carries
	// them, so a receiver can deduplicate a delivery whatever authenticates it.
	signature := Signature{ID: logicalID, Timestamp: signedAt}
	switch strings.ToLower(strings.TrimSpace(wh.AuthMode)) {
	case AuthHMAC:
		signature, err = Sign(secret, logicalID, signedAt, body)
		if err != nil {
			return s.record(ctx, delivery, attemptNumber, attemptResult{
				status:    StatusFailed,
				code:      "secret_unavailable",
				message:   "the webhook's secret could not be used to sign",
				retryable: false,
			})
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
			return s.record(ctx, delivery, attemptNumber, attemptResult{
				status:    StatusFailed,
				code:      "invalid_auth_header",
				message:   err.Error(),
				retryable: false,
			})
		}
		req.Header.Set(wh.AuthHeader, secret)
	default:
		req.Header.Set(IDHeader, logicalID)
		req.Header.Set(TimestampHeader, signatureHeaderTimestamp(signedAt))
	}

	timeout := wh.Timeout
	if timeout <= 0 {
		timeout = s.cfg.Webhooks.Timeout
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	snapshot := redactedRequest(req, wh.AuthHeader)
	// The body is the signed payload and holds no credential: it is stored as
	// transmitted, which is what lets a retry re-send the same bytes.
	snapshotBody, bodyTruncated := boundString(string(body), MaxRecordedBodyBytes)

	resp, err := s.send(ctx, req, timeout)
	finished := s.now()
	recorded := attemptResult{
		status:    StatusFailed,
		retryable: true,
		code:      "network",
		message:   "the request did not complete",
	}
	recorded.attempt = automation.WebhookAttempt{
		AttemptNumber:        attemptNumber,
		RequestMethod:        req.Method,
		RequestURL:           snapshot.URL,
		RequestHeaders:       snapshot.Headers,
		RequestBody:          snapshotBody,
		RequestBodyTruncated: bodyTruncated,
		StartedAt:            started,
		FinishedAt:           &finished,
		DurationMS:           finished.Sub(started).Milliseconds(),
	}

	if err != nil {
		recorded.code, recorded.retryable, recorded.message = classifyError(err)
		return s.record(ctx, delivery, attemptNumber, recorded)
	}
	defer resp.Body.Close()

	raw, truncated := readBounded(resp.Body, s.cfg.Webhooks.MaxResponseBytes)
	recorded.attempt.ResponseStatus = resp.StatusCode
	recorded.attempt.ResponseHeaders = redactedHeaderMap(resp.Header, wh.AuthHeader)
	recorded.attempt.ResponseBody = raw
	recorded.attempt.ResponseBodyTruncated = truncated
	recorded.attempt.ResponseBytes = responseBytes(resp, len(raw))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		recorded.status = StatusSucceeded
		recorded.retryable = false
		recorded.code = ""
		recorded.message = ""
		return s.record(ctx, delivery, attemptNumber, recorded)
	}

	recorded.retryable, recorded.code, recorded.message = classifyStatus(resp.StatusCode)
	recorded.retryAfter = boundedRetryAfter(resp.Header.Get("Retry-After"), s.now())
	return s.record(ctx, delivery, attemptNumber, recorded)
}

// nextSignatureTime returns the timestamp this attempt signs with. It is
// strictly greater than the previous attempt's, so a retry is never a byte-for-
// byte replay of the signature it is retrying, but it never invents a future
// either: once real time catches up, the counter follows it.
func (s *Service) nextSignatureTime(now time.Time) time.Time {
	s.sigMu.Lock()
	defer s.sigMu.Unlock()
	ts := now.UTC().Unix()
	if ts <= s.lastSigTS {
		ts = s.lastSigTS + 1
	}
	s.lastSigTS = ts
	return time.Unix(ts, 0).UTC()
}

// send performs the request under the deployment's concurrency bound and the
// webhook's own timeout.
func (s *Service) send(ctx context.Context, req *http.Request, timeout time.Duration) (*http.Response, error) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.client.Do(req.WithContext(attemptCtx))
}

// record writes the attempt history and the delivery's resulting state. It is
// the only place the delivery row changes after an exchange, so the row always
// describes the last thing that was actually tried.
func (s *Service) record(ctx context.Context, delivery automation.WebhookDelivery, attemptNumber int,
	res attemptResult) attemptResult {

	if res.attempt.ErrorCode == "" {
		res.attempt.ErrorCode = res.code
		res.attempt.ErrorMessage = res.message
	}
	if _, err := s.store.AppendWebhookAttempt(ctx, delivery.ID, res.attempt); err != nil {
		// History is evidence, not a precondition: the outcome still decides the
		// job, and the failure to record it is reported rather than hidden.
		res.message = strings.TrimSpace(res.message + " (the attempt could not be recorded)")
	}

	now := s.now()
	completed := now
	delivery.AttemptCount = attemptNumber
	delivery.LastHTTPStatus = res.attempt.ResponseStatus
	delivery.LastError = res.message
	delivery.NextAttemptAt = nil
	delivery.CompletedAt = nil

	switch {
	case res.status == StatusSucceeded:
		delivery.Status = StatusSucceeded
		delivery.CompletedAt = &completed
	case res.status == StatusCancelled:
		delivery.Status = StatusCancelled
		delivery.CompletedAt = &completed
	case res.retryable:
		delivery.Status = StatusPending
		next := now.Add(res.retryAfter)
		delivery.NextAttemptAt = &next
	default:
		delivery.Status = StatusFailed
		delivery.CompletedAt = &completed
	}
	if _, _, err := s.store.UpdateWebhookDelivery(ctx, delivery); err != nil {
		// The job outcome is still what the queue acts on. A row that could not
		// be updated is corrected by the next attempt or seen by an operator
		// reading the error.
		_ = err
	}
	return res
}

// finish applies an outcome that produced no HTTP exchange — an exhausted
// budget, a disabled webhook, a vanished event — and returns the job error the
// queue reads. No attempt is recorded, because none was made.
func (s *Service) finish(ctx context.Context, delivery automation.WebhookDelivery, res attemptResult) error {
	now := s.now()
	completed := now
	delivery.LastError = res.message
	delivery.NextAttemptAt = nil
	delivery.CompletedAt = nil

	switch {
	case res.status == StatusSucceeded:
		delivery.AttemptCount++
		delivery.Status = StatusSucceeded
		delivery.CompletedAt = &completed
	case res.status == StatusCancelled:
		delivery.Status = StatusCancelled
		delivery.CompletedAt = &completed
	case res.retryable:
		delivery.Status = StatusPending
		next := now.Add(res.retryAfter)
		delivery.NextAttemptAt = &next
	default:
		delivery.Status = StatusFailed
		delivery.CompletedAt = &completed
	}
	if _, _, err := s.store.UpdateWebhookDelivery(ctx, delivery); err != nil {
		_ = err
	}
	return res.jobError()
}

// jobError is the queue's view of one attempt's outcome.
func (r attemptResult) jobError() error {
	if r.status == StatusSucceeded {
		return nil
	}
	return &automation.JobError{
		Code:       r.code,
		Message:    r.message,
		Retryable:  r.retryable,
		RetryAfter: r.retryAfter,
	}
}

func (s *Service) retryable(code string, err error) error {
	return &automation.JobError{
		Code:      code,
		Message:   err.Error(),
		Retryable: true,
	}
}

// openSecret decrypts one webhook's secret. The plaintext lives for the length
// of one request and is never written anywhere.
func (s *Service) openSecret(ctx context.Context, wh automation.Webhook) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(wh.AuthMode))
	if mode == AuthNone {
		return "", nil
	}
	if s.secrets == nil {
		return "", errors.New("webhooks: no secret store is configured")
	}
	box, found, err := s.store.WebhookSecretBox(ctx, wh.ID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("webhooks: the webhook has no stored secret")
	}
	return s.secrets.Open(box)
}

// classifyStatus decides whether another attempt could ever succeed.
//
// A 2xx is success. A 3xx is terminal: the receiver asked us to talk to
// somewhere else and this transport refuses to follow, so retrying would send
// the same request to the same address and get the same answer. 408, 425 and
// 429 say "not now", and a 5xx says the receiver is broken, so both are worth
// another attempt. Everything else is the receiver refusing what we sent, which
// a resend will not fix.
func classifyStatus(code int) (retryable bool, errCode, message string) {
	switch {
	case code >= 200 && code < 300:
		return false, "", ""
	case code >= 300 && code < 400:
		return false, "redirect_refused",
			fmt.Sprintf("the receiver answered %d and asked for another address; redirects are not followed", code)
	case code == http.StatusRequestTimeout, code == http.StatusTooEarly, code == http.StatusTooManyRequests:
		return true, "http_status", fmt.Sprintf("the receiver answered %d", code)
	case code >= 500 && code < 600:
		if code == http.StatusNotImplemented || code == http.StatusHTTPVersionNotSupported {
			return false, "http_status", fmt.Sprintf("the receiver answered %d", code)
		}
		return true, "http_status", fmt.Sprintf("the receiver answered %d", code)
	default:
		return false, "http_status", fmt.Sprintf("the receiver answered %d", code)
	}
}

// classifyError decides what a transport failure means. A refusal to connect to
// a non-public address or a malformed target is permanent: it is this
// application saying no, and it will say no again. A timeout or a connection
// failure is the network, which is worth another attempt.
func classifyError(err error) (code string, retryable bool, message string) {
	switch {
	case errors.Is(err, ErrBlockedAddress):
		return "blocked_address", false, err.Error()
	case errors.Is(err, ErrInvalidTarget):
		return "invalid_target", false, err.Error()
	case errors.Is(err, context.Canceled):
		return "cancelled", false, "the request was cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", true, "the receiver did not answer in time"
	case errors.Is(err, ErrUnreachable):
		return "unreachable", true, err.Error()
	default:
		return "network", true, err.Error()
	}
}

// boundedRetryAfter reads a receiver's Retry-After and refuses to be parked by
// it. A receiver does not get to stop a delivery for a week with one header.
func boundedRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return 0
		}
		return minDuration(time.Duration(seconds)*time.Second, MaxRetryAfter)
	}
	if at, err := http.ParseTime(raw); err == nil {
		d := at.Sub(now)
		if d <= 0 {
			return 0
		}
		return minDuration(d, MaxRetryAfter)
	}
	return 0
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// sensitiveHeaders are the request and response headers that may hold a
// credential. A record that kept one would be a place a secret is stored, which
// is exactly what the history must never become.
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"api-key":             true,
	"x-auth-token":        true,
	"x-access-token":      true,
	"x-csrf-token":        true,
}

// sensitiveQueryKeys are query parameter names whose values are credentials.
var sensitiveQueryKeys = map[string]bool{
	"key": true, "token": true, "secret": true, "password": true, "passwd": true,
	"pwd": true, "api_key": true, "apikey": true, "access_token": true, "auth": true,
	"authorization": true, "signature": true, "sig": true, "credential": true,
	"credentials": true, "session": true, "session_id": true, "client_secret": true,
	"private_key": true, "hmac": true, "bearer": true,
}

// redacted is one request as it is stored.
type redacted struct {
	URL     string
	Headers map[string]string
}

// redactedRequest renders a request as a record that keeps no credential.
func redactedRequest(req *http.Request, extraSecretHeader string) redacted {
	return redacted{
		URL:     redactURL(req.URL.String()),
		Headers: redactedHeaderMap(req.Header, extraSecretHeader),
	}
}

func redactedHeaderMap(h http.Header, extraSecretHeader string) map[string]string {
	out := make(map[string]string, len(h))
	extra := strings.ToLower(strings.TrimSpace(extraSecretHeader))
	for name, values := range h {
		lower := strings.ToLower(name)
		value := strings.Join(values, ", ")
		if sensitiveHeaders[lower] || (extra != "" && lower == extra) {
			value = "[redacted]"
		}
		out[name] = value
	}
	return out
}

// redactURL replaces the values of credential-shaped query parameters and
// leaves the rest alone, so a record still shows which endpoint was called.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	q := u.Query()
	for key := range q {
		if sensitiveQueryKeys[strings.ToLower(key)] {
			q.Set(key, "[redacted]")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// readBounded reads at most cap bytes of a response body and reports whether
// more was there.
func readBounded(body io.Reader, cap int64) (string, bool) {
	if cap <= 0 {
		cap = DefaultMaxResponseBytes
	}
	raw, err := io.ReadAll(io.LimitReader(body, cap+1))
	if err != nil && len(raw) == 0 {
		return "", false
	}
	if int64(len(raw)) > cap {
		return string(raw[:cap]), true
	}
	return string(raw), false
}

// responseBytes reports the response's total size when the receiver declared
// one, and otherwise how much was captured — which is a lower bound when the
// capture was truncated, as the truncation flag says.
func responseBytes(resp *http.Response, captured int) int64 {
	if resp != nil && resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return int64(captured)
}

// boundString caps a stored body and reports whether it was cut.
func boundString(s string, limit int) (string, bool) {
	if limit <= 0 || len(s) <= limit {
		return s, false
	}
	return s[:limit], true
}

// signatureHeaderTimestamp renders a signature timestamp the way the header
// carries it: unix seconds.
func signatureHeaderTimestamp(at time.Time) string {
	return strconv.FormatInt(at.UTC().Unix(), 10)
}

// ValidAuthHeaderName reports whether name is a custom authentication header
// this application's sender would accept for auth_mode header.
//
// It is the same rule the delivery path enforces, exported so a receiver can
// reject at configuration time exactly the names the sender would refuse to
// send under. A test receiver that accepted "Authorization" or a "webhook-"
// name would be verifying a header the sender never sets, and would pass every
// request while proving nothing.
func ValidAuthHeaderName(name string) error {
	return validAuthHeaderName(name)
}

// validAuthHeaderName refuses a custom auth header that would rewrite the
// request's framing or one of the Standard Webhooks headers. It is checked
// again at delivery time, not only when the webhook was saved.
func validAuthHeaderName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("auth_mode header needs an auth_header name")
	}
	if strings.ContainsAny(name, " \t\r\n:/") {
		return fmt.Errorf("auth_header %q is not a valid header name", name)
	}
	for _, r := range name {
		if r <= 0x20 || r >= 0x7f {
			return fmt.Errorf("auth_header %q is not a valid header name", name)
		}
	}
	lower := strings.ToLower(name)
	switch lower {
	case "host", "content-length", "connection", "transfer-encoding", "upgrade",
		"authorization", "proxy-authorization":
		return fmt.Errorf("auth_header must not overwrite %s", name)
	}
	if strings.HasPrefix(lower, "webhook-") {
		return fmt.Errorf("auth_header must not use the webhook- prefix")
	}
	return nil
}
