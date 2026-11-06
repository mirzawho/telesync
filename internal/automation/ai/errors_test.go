package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
)

func TestClassifySeparatesRetryableFromPermanent(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
		code      string
	}{
		{name: "nil", err: nil, retryable: false, code: ""},
		{
			name:      "rate limited is retryable",
			err:       newError(CodeRateLimited, "too many requests", true),
			retryable: true,
			code:      CodeRateLimited,
		},
		{
			name:      "server error is retryable",
			err:       newError(CodeServerError, "bad gateway", true),
			retryable: true,
			code:      CodeServerError,
		},
		{
			name:      "timeout is retryable",
			err:       newError(CodeTimeout, "deadline exceeded", true),
			retryable: true,
			code:      CodeTimeout,
		},
		{
			name:      "connection reset is retryable",
			err:       newError(CodeServerError, "connection reset", true),
			retryable: true,
			code:      CodeServerError,
		},
		{
			name:      "unauthorized is permanent",
			err:       newError(CodeUnauthorized, "bad key", false),
			retryable: false,
			code:      CodeUnauthorized,
		},
		{
			name:      "rejected request is permanent",
			err:       newError(CodeRequestRejected, "bad request", false),
			retryable: false,
			code:      CodeRequestRejected,
		},
		{
			name:      "malformed output is permanent",
			err:       newError(CodeInvalidOutput, "not json", false),
			retryable: false,
			code:      CodeInvalidOutput,
		},
		{
			name:      "unsupported images are permanent",
			err:       newError(CodeImagesUnsupported, "no vision", false),
			retryable: false,
			code:      CodeImagesUnsupported,
		},
		{
			name:      "not configured is permanent",
			err:       newError(CodeNotConfigured, "no key", false),
			retryable: false,
			code:      CodeNotConfigured,
		},
		{
			name:      "input too large is permanent",
			err:       newError(CodeInputTooLarge, "too long", false),
			retryable: false,
			code:      CodeInputTooLarge,
		},
		{
			name:      "cancellation is permanent",
			err:       context.Canceled,
			retryable: false,
			code:      CodeCancelled,
		},
		{
			name:      "deadline is retryable",
			err:       context.DeadlineExceeded,
			retryable: true,
			code:      CodeTimeout,
		},
		{
			name:      "unclassified errors are retryable",
			err:       errors.New("something nobody classified"),
			retryable: true,
			code:      CodeUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			retryable, code := Classify(tc.err)
			if retryable != tc.retryable {
				t.Fatalf("Classify(%v) retryable = %v, want %v", tc.err, retryable, tc.retryable)
			}
			if code != tc.code {
				t.Fatalf("Classify(%v) code = %q, want %q", tc.err, code, tc.code)
			}
		})
	}
}

func TestClassifyUnwrapsWrappedJobErrors(t *testing.T) {
	inner := newError(CodeRateLimited, "slow down", true)
	wrapped := fmt.Errorf("polish workflow 7: %w", inner)

	retryable, code := Classify(wrapped)
	if !retryable || code != CodeRateLimited {
		t.Fatalf("Classify(wrapped) = (%v, %q), want (true, %q)", retryable, code, CodeRateLimited)
	}
}

func TestRetryAfterIsBounded(t *testing.T) {
	if got := clampRetryAfter(-time.Second); got != 0 {
		t.Fatalf("clampRetryAfter(-1s) = %v, want 0", got)
	}
	if got := clampRetryAfter(30 * time.Second); got != 30*time.Second {
		t.Fatalf("clampRetryAfter(30s) = %v, want 30s", got)
	}
	if got := clampRetryAfter(24 * time.Hour); got != maxRetryAfter {
		t.Fatalf("clampRetryAfter(24h) = %v, want %v", got, maxRetryAfter)
	}

	err := newRetryableAfter(CodeRateLimited, "slow down", 24*time.Hour)
	if err.RetryAfter != maxRetryAfter {
		t.Fatalf("RetryAfter = %v, want %v", err.RetryAfter, maxRetryAfter)
	}
	if !err.Retryable {
		t.Fatal("RetryAfter error must be retryable")
	}
}

func TestScrubRemovesTheAPIKey(t *testing.T) {
	const key = "sk-super-secret-value-do-not-print"

	cases := []struct {
		name string
		in   string
	}{
		{name: "bare key", in: "provider said " + key + " is invalid"},
		{name: "bearer header", in: `upstream echoed Authorization: Bearer ` + key},
		{name: "json field", in: `{"api_key":"` + key + `"}`},
		{name: "repeated", in: key + " and again " + key},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scrub(key, tc.in)
			if got == "" {
				t.Fatal("scrub returned an empty message; it must keep the useful part")
			}
			if containsKey(got, key) {
				t.Fatalf("scrub(%q) leaked the key: %q", tc.in, got)
			}
		})
	}
}

func TestScrubKeepsMessagesWithoutTheKey(t *testing.T) {
	const msg = "automation.ai.api_key_env names TELESYNC_AI_KEY, which is not set"
	if got := scrub("sk-never-appears", msg); got != msg {
		t.Fatalf("scrub changed an unrelated message: %q", got)
	}
}

func TestErrorTextNeverNamesAKeyValue(t *testing.T) {
	const key = "sk-do-not-print-me"
	err := newError(CodeUnauthorized, scrub(key, "the provider rejected Authorization: Bearer "+key), false)

	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("error is %T, want *automation.JobError", err)
	}
	if containsKey(jobErr.Error(), key) {
		t.Fatalf("JobError.Error() leaked the key: %q", jobErr.Error())
	}
	if containsKey(jobErr.Message, key) {
		t.Fatalf("JobError.Message leaked the key: %q", jobErr.Message)
	}
}

// containsKey reports whether the API key's value is present in s. It is the
// leak check every secret-facing test uses: the full value must never appear,
// in any message, log line or stored error.
func containsKey(s, key string) bool {
	return strings.Contains(s, key)
}
