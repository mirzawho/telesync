package appconfig

import (
	"fmt"
	"strconv"
	"strings"
)

// Defaults for direct message sending.
//
// The limit is the number the worker's own transfer already enforces, so an
// attachment this deployment accepts is one the destination connection can
// carry. The window is long enough that an operator can look at a finished send
// and short enough that staged attachments do not accumulate.
const (
	DefaultSendEnabled                = false
	DefaultSendStagingDir             = "./data/send-staging"
	DefaultSendMaxFileBytes     int64 = 33554432
	DefaultSendHistoryRetention       = 30

	// MinSendMaxFileBytes is the smallest limit worth setting: below it a
	// photograph would not fit, which is not a configuration anyone means.
	MinSendMaxFileBytes int64 = 1024
	// MaxSendMaxFileBytes keeps a single upload within what one worker transfer
	// can carry, which is the real ceiling whatever this is set to.
	MaxSendMaxFileBytes int64 = 33554432
)

// SendConfig is direct message sending: messages a caller composed and files
// they uploaded, accepted over HTTP and delivered in the background.
//
// Its worker count, retry policy and lease are not here. They are the durable
// queue's, shared with webhooks and workflows, because there is one queue and
// one pool of workers running it — a second set of knobs for the same pool
// would be two answers to one question.
type SendConfig struct {
	// Enabled is the master switch. With it off the send routes answer 503 and
	// nothing is staged, recorded or queued.
	Enabled bool
	// StagingDir is where an uploaded attachment waits between acceptance and
	// delivery. It outlives the process on purpose: an attachment staged before
	// a restart must still be sendable after one. It is never served, and never
	// appears in an API answer.
	StagingDir string
	// MaxFileBytes is the largest attachment one send may carry. The upload is
	// bounded as it streams, so a body above this is refused without being
	// stored anywhere first.
	MaxFileBytes int64
	// HistoryRetentionDays is how long a finished operation and its staged
	// attachment are kept. Zero keeps them forever.
	HistoryRetentionDays int
}

// rawSend is the JSON shape of the "send" block.
type rawSend struct {
	Enabled              *bool  `json:"enabled"`
	StagingDir           string `json:"staging_dir"`
	MaxFileBytes         *int64 `json:"max_file_bytes"`
	HistoryRetentionDays *int   `json:"history_retention_days"`
}

func (raw rawSend) resolve(base SendConfig, lookup func(string) string) (SendConfig, []string) {
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	out := base
	out.Enabled = DefaultSendEnabled
	if raw.Enabled != nil {
		out.Enabled = *raw.Enabled
	}
	if fromEnv, said := envFlag(lookup, SendEnabledEnv); said {
		out.Enabled = fromEnv
	}

	out.StagingDir = strings.TrimSpace(raw.StagingDir)
	if out.StagingDir == "" {
		out.StagingDir = DefaultSendStagingDir
	}
	if fromEnv, said := envText(lookup, SendStagingDir); said {
		out.StagingDir = fromEnv
	}

	out.MaxFileBytes = DefaultSendMaxFileBytes
	if raw.MaxFileBytes != nil {
		switch {
		case *raw.MaxFileBytes <= 0:
			fail("send.max_file_bytes %d must be positive", *raw.MaxFileBytes)
		case *raw.MaxFileBytes < MinSendMaxFileBytes:
			fail("send.max_file_bytes %d must be at least %d", *raw.MaxFileBytes, MinSendMaxFileBytes)
		case *raw.MaxFileBytes > MaxSendMaxFileBytes:
			fail("send.max_file_bytes %d must be at most %d", *raw.MaxFileBytes, MaxSendMaxFileBytes)
		default:
			out.MaxFileBytes = *raw.MaxFileBytes
		}
	}
	if fromEnv, said := envInt64(lookup, SendMaxFileBytes, fail); said {
		switch {
		case fromEnv < MinSendMaxFileBytes:
			fail("%s %d must be at least %d", SendMaxFileBytes, fromEnv, MinSendMaxFileBytes)
		case fromEnv > MaxSendMaxFileBytes:
			fail("%s %d must be at most %d", SendMaxFileBytes, fromEnv, MaxSendMaxFileBytes)
		default:
			out.MaxFileBytes = fromEnv
		}
	}

	out.HistoryRetentionDays = DefaultSendHistoryRetention
	if raw.HistoryRetentionDays != nil {
		if *raw.HistoryRetentionDays < 0 {
			fail("send.history_retention_days %d must not be negative", *raw.HistoryRetentionDays)
		} else {
			out.HistoryRetentionDays = *raw.HistoryRetentionDays
		}
	}
	if fromEnv, said := envInt64(lookup, SendHistoryDays, fail); said {
		if fromEnv < 0 {
			fail("%s %d must not be negative", SendHistoryDays, fromEnv)
		} else {
			out.HistoryRetentionDays = int(fromEnv)
		}
	}
	return out, problems
}

// envText reads one environment value and reports whether the environment said
// anything at all. An unset or empty variable is "not said", so a file's own
// value survives one that was never exported.
func envText(lookup func(string) string, name string) (string, bool) {
	raw := strings.TrimSpace(lookup(name))
	if raw == "" {
		return "", false
	}
	return raw, true
}

// envInt64 reads one environment number and reports whether the environment
// said anything. A variable that is set but is not a number is a mistake and is
// reported as one rather than being read as zero and quietly accepted.
func envInt64(lookup func(string) string, name string, fail func(string, ...any)) (int64, bool) {
	raw := strings.TrimSpace(lookup(name))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		fail("%s %q must be a whole number", name, raw)
		return 0, false
	}
	return value, true
}

// Environment variables that override the file, beside the block they belong
// to. An unset or empty variable is "not said" and the file stands.
const (
	SendEnabledEnv   = "TELESYNC_SEND_ENABLED"
	SendStagingDir   = "TELESYNC_SEND_STAGING_DIR"
	SendMaxFileBytes = "TELESYNC_SEND_MAX_FILE_BYTES"
	SendHistoryDays  = "TELESYNC_SEND_HISTORY_RETENTION_DAYS"
)
