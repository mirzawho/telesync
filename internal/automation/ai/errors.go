package ai

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
)

// Classified error codes reported by this package. They are the vocabulary
// Agent C records in a workflow run's AI error column and shows to an operator,
// so they are stable strings and never carry a value that is a secret.
const (
	// CodeDisabled means automation.ai.enabled is false.
	CodeDisabled = "ai_disabled"
	// CodeNotConfigured means the client cannot be used at all: no base URL, no
	// model, or no resolvable API key.
	CodeNotConfigured = "ai_not_configured"
	// CodeInputTooLarge means the request text exceeded automation.ai.max_input_chars.
	CodeInputTooLarge = "ai_input_too_large"
	// CodeInvalidOutput means the model answered, but not in the structured
	// output contract. It is permanent: retrying the same prompt with the same
	// model is not evidence the answer gets better.
	CodeInvalidOutput = "ai_invalid_output"
	// CodeRateLimited means the provider answered 429 and may accept the same
	// request later. RetryAfter carries the provider's own delay when it sent one.
	CodeRateLimited = "ai_rate_limited"
	// CodeServerError means the provider failed transiently: 5xx, or a broken
	// connection before a complete answer.
	CodeServerError = "ai_server_error"
	// CodeTimeout means the call ran past automation.ai.timeout.
	CodeTimeout = "ai_timeout"
	// CodeCancelled means the caller cancelled the request.
	CodeCancelled = "ai_cancelled"
	// CodeUnauthorized means the provider refused the credential or the model
	// name. It is a misconfiguration, not a transient fault.
	CodeUnauthorized = "ai_unauthorized"
	// CodeRequestRejected means the provider refused the request for a reason
	// this package cannot repair.
	CodeRequestRejected = "ai_request_rejected"
	// CodeImagesUnsupported means the provider refused image input. Images are
	// never dropped to make such a call succeed.
	CodeImagesUnsupported = "ai_images_unsupported"
	// CodeUnknown is what Classify reports for an error it cannot recognise.
	CodeUnknown = "ai_unknown"
)

// maxRetryAfter bounds any provider-supplied delay. A hostile or broken
// Retry-After header must not park a workflow for hours, so the queue's own
// backoff stays authoritative above this ceiling.
const maxRetryAfter = 10 * time.Minute

// newError builds the error type every failure in this package returns.
func newError(code, message string, retryable bool) *automation.JobError {
	return &automation.JobError{Code: code, Message: message, Retryable: retryable}
}

// newRetryableAfter builds a retryable error that also carries the delay the
// provider asked for, clamped to maxRetryAfter.
func newRetryableAfter(code, message string, after time.Duration) *automation.JobError {
	return &automation.JobError{Code: code, Message: message, Retryable: true, RetryAfter: clampRetryAfter(after)}
}

// clampRetryAfter keeps a provider-supplied delay inside a sane bound.
func clampRetryAfter(after time.Duration) time.Duration {
	if after <= 0 {
		return 0
	}
	if after > maxRetryAfter {
		return maxRetryAfter
	}
	return after
}

// redactedMarker replaces a removed secret. It is a fixed string and never
// carries any part of the value it stands in for, so an operator learns that
// something was removed without learning what it was.
const redactedMarker = "[redacted]"

// scrub removes every occurrence of secret from msg.
//
// Every string this package hands to a caller, a log or a stored error column
// passes through here first: a provider that echoes a request header back in an
// error body must not become a way to read the API key out of the database. The
// replacement is a fixed marker and never the secret's own shape, so an
// operator can see that something was removed without learning what it was.
//
// An empty secret scrubs nothing, because there is nothing to remove. A
// non-empty secret is always removed even if it is short: mangling a message is
// a smaller failure than printing a credential.
func scrub(secret, msg string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, redactedMarker)
}

// Classify reports whether an error is worth another attempt and which code
// describes it.
//
// It understands everything this package returns and the two context errors a
// caller can cause on its own. Anything else is reported as retryable with
// CodeUnknown, matching internal/automation's rule that an unclassified failure
// is not proof that the remote side rejected the request.
func Classify(err error) (retryable bool, code string) {
	if err == nil {
		return false, ""
	}
	var jobErr *automation.JobError
	if errors.As(err, &jobErr) {
		return jobErr.Retryable, jobErr.Code
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return true, CodeTimeout
	case errors.Is(err, context.Canceled):
		return false, CodeCancelled
	}
	return true, CodeUnknown
}
