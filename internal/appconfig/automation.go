package appconfig

import (
	"fmt"
	"strings"
	"time"
)

// Defaults for the background automation subsystem. They are deliberately
// conservative: a small worker pool, bounded retries and short leases, so a
// misconfiguration degrades into slowness rather than into a runaway.
const (
	DefaultAutomationWorkers          = 2
	DefaultAutomationPollInterval     = time.Second
	DefaultAutomationPublishInterval  = 2 * time.Second
	DefaultAutomationMaxAttempts      = 5
	DefaultAutomationRetryDelay       = 10 * time.Second
	DefaultAutomationJobLease         = 2 * time.Minute
	DefaultAutomationHistoryRetention = 30
	DefaultRecoveryBatchSize          = 20
	DefaultRecoveryInterval           = 5 * time.Second
	DefaultWebhookTimeout             = 15 * time.Second
	DefaultWebhookMaxAttempts         = 5
	DefaultWebhookMaxResponseBytes    = 64 << 10
	DefaultWebhookMaxConcurrent       = 4
	DefaultWebhookHistoryRetention    = 30
	DefaultAITimeout                  = 45 * time.Second
	DefaultAIMaxOutputTokens          = 1500
	DefaultAIMaxInputChars            = 16000
	DefaultAIMaxConcurrentRequests    = 2
	DefaultAIMaxImages                = 4
	DefaultAIMaxImageBytes            = 8 << 20

	MaxAutomationWorkers       = 32
	MaxWebhookMaxConcurrent    = 32
	MaxAIMaxConcurrentRequests = 16
)

// AutomationConfig is the background automation subsystem: event ingestion,
// the durable job queue, webhooks, workflows and AI polishing.
type AutomationConfig struct {
	// Enabled is the master switch. With it off nothing is ingested, no worker
	// runs, and the automation routes report the subsystem as disabled.
	Enabled bool
	// Workers is how many queue jobs run at once across all job kinds.
	Workers int
	// PollInterval is how often an idle queue worker looks for work.
	PollInterval time.Duration
	// PublishInterval is the minimum gap between two publications to the same
	// destination, so recovered backlogs cannot burst a channel.
	PublishInterval time.Duration
	// MaxAttempts bounds how many times one job is tried before it is recorded
	// as terminally failed.
	MaxAttempts int
	// RetryDelay is the base delay for exponential backoff.
	RetryDelay time.Duration
	// JobLease is how long a claimed job stays owned before another worker may
	// reclaim it after a crash.
	JobLease time.Duration
	// HistoryRetentionDays is how long execution history is kept. Zero keeps it
	// forever.
	HistoryRetentionDays int
	Recovery             RecoveryConfig
	Webhooks             WebhookConfig
	AI                   AIConfig
}

// RecoveryConfig controls catching up on messages that arrived while a
// connection was down. It recovers only what automation was already entitled
// to see; it never backfills a channel's historical archive.
type RecoveryConfig struct {
	Enabled   bool
	BatchSize int
	Interval  time.Duration
}

// WebhookConfig bounds outbound webhook delivery.
type WebhookConfig struct {
	Timeout                 time.Duration
	MaxAttempts             int
	MaxResponseBytes        int64
	MaxConcurrentDeliveries int
	HistoryRetentionDays    int
}

// AIConfig is the OpenAI-compatible polishing service. It is server-side
// configuration: no route accepts or returns any of these values.
type AIConfig struct {
	Enabled bool
	BaseURL string
	// APIKeyEnv names the variable holding the key. Exactly one of it and a
	// literal api_key is set, the same rule api.jwt.secret,
	// api.wordpress.encryption_key and a client's app_hash follow.
	APIKeyEnv string
	// APIKey is the resolved key. Never printed, never marshalled.
	APIKey string `json:"-"`
	// APIKeyInFile is true when the key came from automation.ai.api_key rather
	// than from a variable, so a diagnostic can say where it came from without
	// ever saying what it is.
	APIKeyInFile bool
	Model        string
	Timeout      time.Duration
	// MaxOutputTokens and MaxInputChars bound one request.
	MaxOutputTokens int
	MaxInputChars   int
	// MaxConcurrentRequests bounds how many AI calls run at once across all
	// workflows.
	MaxConcurrentRequests int
	// MaxImages and MaxImageBytes bound optional image input. Images are only
	// sent at all when a workflow opts in.
	MaxImages     int
	MaxImageBytes int64
}

// rawAutomation is the JSON shape of the "automation" block.
type rawAutomation struct {
	Enabled              bool        `json:"enabled"`
	Workers              *int        `json:"workers"`
	PollInterval         string      `json:"poll_interval"`
	PublishInterval      string      `json:"publish_interval"`
	MaxAttempts          *int        `json:"max_attempts"`
	RetryDelay           string      `json:"retry_delay"`
	JobLease             string      `json:"job_lease"`
	HistoryRetentionDays *int        `json:"history_retention_days"`
	Recovery             rawRecovery `json:"recovery"`
	Webhooks             rawWebhooks `json:"webhooks"`
	AI                   rawAI       `json:"ai"`
}

type rawRecovery struct {
	Enabled   bool   `json:"enabled"`
	BatchSize *int   `json:"batch_size"`
	Interval  string `json:"interval"`
}

type rawWebhooks struct {
	Timeout                 string `json:"timeout"`
	MaxAttempts             *int   `json:"max_attempts"`
	MaxResponseBytes        *int64 `json:"max_response_bytes"`
	MaxConcurrentDeliveries *int   `json:"max_concurrent_deliveries"`
	HistoryRetentionDays    *int   `json:"history_retention_days"`
}

type rawAI struct {
	Enabled               bool    `json:"enabled"`
	BaseURL               string  `json:"base_url"`
	APIKeyEnv             string  `json:"api_key_env"`
	APIKey                *string `json:"api_key"`
	Model                 string  `json:"model"`
	Timeout               string  `json:"timeout"`
	MaxOutputTokens       *int    `json:"max_output_tokens"`
	MaxInputChars         *int    `json:"max_input_chars"`
	MaxConcurrentRequests *int    `json:"max_concurrent_requests"`
	MaxImages             *int    `json:"max_images"`
	MaxImageBytes         *int64  `json:"max_image_bytes"`
}

// resolve fills in defaults and reports every problem it finds, so one run of
// the loader names all the mistakes rather than the first.
func (raw rawAutomation) resolve(base AutomationConfig, lookup func(string) string) (AutomationConfig, []string) {
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	positive := func(name string, got *int, def, max int) int {
		if got == nil {
			return def
		}
		if *got <= 0 {
			fail("automation.%s %d must be positive", name, *got)
			return def
		}
		if max > 0 && *got > max {
			fail("automation.%s %d must be at most %d", name, *got, max)
			return def
		}
		return *got
	}
	nonNegative := func(name string, got *int, def int) int {
		if got == nil {
			return def
		}
		if *got < 0 {
			fail("automation.%s %d must not be negative", name, *got)
			return def
		}
		return *got
	}
	duration := func(name, got string, def time.Duration) time.Duration {
		if strings.TrimSpace(got) == "" {
			return def
		}
		d, err := time.ParseDuration(got)
		if err != nil || d <= 0 {
			fail("automation.%s %q must be a positive duration such as 30s", name, got)
			return def
		}
		return d
	}

	out := base
	out.Enabled = raw.Enabled
	out.Workers = positive("workers", raw.Workers, DefaultAutomationWorkers, MaxAutomationWorkers)
	out.PollInterval = duration("poll_interval", raw.PollInterval, DefaultAutomationPollInterval)
	out.PublishInterval = duration("publish_interval", raw.PublishInterval, DefaultAutomationPublishInterval)
	out.MaxAttempts = positive("max_attempts", raw.MaxAttempts, DefaultAutomationMaxAttempts, 100)
	out.RetryDelay = duration("retry_delay", raw.RetryDelay, DefaultAutomationRetryDelay)
	out.JobLease = duration("job_lease", raw.JobLease, DefaultAutomationJobLease)
	out.HistoryRetentionDays = nonNegative("history_retention_days", raw.HistoryRetentionDays, DefaultAutomationHistoryRetention)

	out.Recovery.Enabled = raw.Recovery.Enabled
	out.Recovery.BatchSize = positive("recovery.batch_size", raw.Recovery.BatchSize, DefaultRecoveryBatchSize, 1000)
	out.Recovery.Interval = duration("recovery.interval", raw.Recovery.Interval, DefaultRecoveryInterval)

	out.Webhooks.Timeout = duration("webhooks.timeout", raw.Webhooks.Timeout, DefaultWebhookTimeout)
	out.Webhooks.MaxAttempts = positive("webhooks.max_attempts", raw.Webhooks.MaxAttempts, DefaultWebhookMaxAttempts, 100)
	if raw.Webhooks.MaxResponseBytes == nil {
		out.Webhooks.MaxResponseBytes = DefaultWebhookMaxResponseBytes
	} else if *raw.Webhooks.MaxResponseBytes <= 0 {
		fail("automation.webhooks.max_response_bytes %d must be positive", *raw.Webhooks.MaxResponseBytes)
		out.Webhooks.MaxResponseBytes = DefaultWebhookMaxResponseBytes
	} else {
		out.Webhooks.MaxResponseBytes = *raw.Webhooks.MaxResponseBytes
	}
	out.Webhooks.MaxConcurrentDeliveries = positive("webhooks.max_concurrent_deliveries", raw.Webhooks.MaxConcurrentDeliveries, DefaultWebhookMaxConcurrent, MaxWebhookMaxConcurrent)
	out.Webhooks.HistoryRetentionDays = nonNegative("webhooks.history_retention_days", raw.Webhooks.HistoryRetentionDays, DefaultWebhookHistoryRetention)

	out.AI.Enabled = raw.AI.Enabled
	out.AI.BaseURL = strings.TrimSpace(raw.AI.BaseURL)
	out.AI.Model = strings.TrimSpace(raw.AI.Model)
	out.AI.Timeout = duration("ai.timeout", raw.AI.Timeout, DefaultAITimeout)
	out.AI.MaxOutputTokens = positive("ai.max_output_tokens", raw.AI.MaxOutputTokens, DefaultAIMaxOutputTokens, 0)
	out.AI.MaxInputChars = positive("ai.max_input_chars", raw.AI.MaxInputChars, DefaultAIMaxInputChars, 0)
	out.AI.MaxConcurrentRequests = positive("ai.max_concurrent_requests", raw.AI.MaxConcurrentRequests, DefaultAIMaxConcurrentRequests, MaxAIMaxConcurrentRequests)
	out.AI.MaxImages = nonNegative("ai.max_images", raw.AI.MaxImages, DefaultAIMaxImages)
	if raw.AI.MaxImageBytes == nil {
		out.AI.MaxImageBytes = DefaultAIMaxImageBytes
	} else if *raw.AI.MaxImageBytes <= 0 {
		fail("automation.ai.max_image_bytes %d must be positive", *raw.AI.MaxImageBytes)
		out.AI.MaxImageBytes = DefaultAIMaxImageBytes
	} else {
		out.AI.MaxImageBytes = *raw.AI.MaxImageBytes
	}

	// Exactly one key source: a literal api_key, or the name of the variable
	// holding it. Both is ambiguous, neither is unusable — the same rule
	// api.jwt.secret, api.wordpress.encryption_key and a client's app_hash
	// follow. Whichever it came from, the resolved value is never printed and
	// never marshalled.
	out.AI.APIKeyEnv = strings.TrimSpace(raw.AI.APIKeyEnv)
	literalKey := ""
	if raw.AI.APIKey != nil {
		literalKey = strings.TrimSpace(*raw.AI.APIKey)
	}
	switch {
	case literalKey != "" && out.AI.APIKeyEnv != "":
		fail("automation.ai: set either api_key or api_key_env, not both")
	case literalKey != "":
		out.AI.APIKey = literalKey
		out.AI.APIKeyInFile = true
	case out.AI.APIKeyEnv != "":
		out.AI.APIKey = strings.TrimSpace(lookup(out.AI.APIKeyEnv))
	}

	if out.AI.Enabled {
		if out.AI.BaseURL == "" {
			fail("automation.ai.base_url is required when automation.ai.enabled is true")
		}
		if out.AI.Model == "" {
			fail("automation.ai.model is required when automation.ai.enabled is true")
		}
		if out.AI.APIKey == "" {
			if out.AI.APIKeyEnv != "" {
				fail("automation.ai.api_key_env names %s, which is not set", out.AI.APIKeyEnv)
			} else {
				fail("automation.ai: api_key (a literal key) or api_key_env (the name of the variable holding it) is required when automation.ai.enabled is true")
			}
		}
	}
	return out, problems
}

// NetworkKey identifies the Telegram-compatible network a client is connected
// to, so one logical message seen through two accounts on the same network is
// one event, while two independent backends whose channel and message ids
// happen to overlap never collide.
//
// It is derived from the connection role and the custom backend's endpoint,
// never from the client key: client keys are operator labels and carry no
// meaning about which network is on the other end.
func (c Client) NetworkKey() string {
	if c.Type == Official {
		return string(Official)
	}
	if c.Server == nil {
		return string(Custom)
	}
	return string(Custom) + "@" + c.Server.Endpoint()
}
