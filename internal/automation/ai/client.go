// Package ai polishes one message through an OpenAI-compatible Chat
// Completions endpoint.
//
// It is deliberately a small net/http client rather than an SDK: the required
// surface is one POST to {base_url}/chat/completions, and this repository keeps
// its dependency list short. There is no provider-abstraction layer, no
// streaming, no tool calling and no retry loop: the durable queue in
// internal/automation owns retry policy, and this package only reports whether
// a failure is worth another attempt through Classify.
//
// The one exception to "no retries" is the response_format fallback documented
// on Polish: that is request-shape negotiation with a provider whose dialect we
// do not know, bounded to exactly one extra call, and it never changes what the
// answer is held to.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
)

const (
	// chatCompletionsPath is appended to automation.ai.base_url.
	chatCompletionsPath = "/chat/completions"
	// temperature is the moderate setting: high enough that a rewrite is not a
	// verbatim echo, low enough that the polished text stays an edit of the
	// source rather than a new composition.
	temperature = 0.2
	// maxResponseBytes bounds how much of an answer is read into memory. A
	// provider that streams a novel back cannot become a memory problem.
	maxResponseBytes = 8 << 20
	// maxErrorExcerpt bounds how much of a provider error is quoted.
	maxErrorExcerpt = 300
	// codeResponseFormatUnsupported is an internal marker, never returned to a
	// caller: it triggers the one documented fallback to plain JSON prompting.
	codeResponseFormatUnsupported = "ai_response_format_unsupported"
)

// Processor is the OpenAI-compatible polishing client. It implements
// automation.AIProcessor.
type Processor struct {
	cfg    appconfig.AIConfig
	client *http.Client
	sem    chan struct{}
}

// New builds a processor from the server-side automation.ai configuration.
//
// Missing credentials are not an error here: a deployment may load the config
// with AI switched off and never call Polish. What New refuses is a base URL
// that cannot be an endpoint at all, because that is a typo the operator wants
// to hear about at startup rather than in a workflow run.
func New(cfg appconfig.AIConfig) (*Processor, error) {
	if base := strings.TrimSpace(cfg.BaseURL); base != "" {
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, newError(CodeNotConfigured,
				"automation.ai.base_url must be an absolute http or https URL", false)
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = appconfig.DefaultAITimeout
	}
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = appconfig.DefaultAIMaxConcurrentRequests
	}
	if cfg.MaxInputChars <= 0 {
		cfg.MaxInputChars = appconfig.DefaultAIMaxInputChars
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = appconfig.DefaultAIMaxOutputTokens
	}
	return &Processor{
		cfg:    cfg,
		client: &http.Client{},
		sem:    make(chan struct{}, cfg.MaxConcurrentRequests),
	}, nil
}

// Polish improves one message and returns the validated structured output.
//
// What it sends: one system message made of the fixed DefaultPrompt with the
// workflow's own prompt appended behind a precedence clause, and one user
// message holding the source text behind <source> delimiters that name it as
// content. Images go out only when the workflow passed them, and only as
// supported image types.
//
// What it asks for: model, messages, max_tokens from automation.ai.max_output_tokens,
// a moderate temperature, and response_format json_object.
//
// What it tolerates: a provider that does not implement response_format gets
// exactly one retry with the parameter omitted and the JSON instruction left in
// the prompt. A provider that does not implement vision is a classified
// failure, never a silent drop of the images.
//
// What it refuses: input past automation.ai.max_input_chars is rejected rather
// than truncated, because an edit of a prefix is not an edit of the message.
// Anything the model returns outside the structured output contract is
// refused, including any field that would name a destination, a permission, a
// tool or a URL.
//
// The API key is sent as a bearer token and appears in no error, no result and
// no log line this package produces.
func (p *Processor) Polish(ctx context.Context, input automation.PolishInput) (automation.PolishResult, error) {
	var zero automation.PolishResult

	if !p.cfg.Enabled {
		return zero, newError(CodeDisabled, "automation.ai.enabled is false", false)
	}
	if strings.TrimSpace(p.cfg.BaseURL) == "" {
		return zero, newError(CodeNotConfigured, "automation.ai.base_url is required", false)
	}
	if strings.TrimSpace(p.cfg.Model) == "" {
		return zero, newError(CodeNotConfigured, "automation.ai.model is required", false)
	}
	if p.cfg.APIKey == "" {
		return zero, newError(CodeNotConfigured,
			"automation.ai.api_key_env names a variable that did not resolve to a key", false)
	}

	// The bound is on the whole text of one request, custom prompt included:
	// both go into the same prompt and neither may be unbounded. Rejected, not
	// truncated; see the doc comment above.
	chars := utf8.RuneCountInString(input.Prompt) + utf8.RuneCountInString(input.Text)
	if chars > p.cfg.MaxInputChars {
		return zero, newError(CodeInputTooLarge, fmt.Sprintf(
			"the request text is %d characters and automation.ai.max_input_chars allows %d; it is rejected rather than truncated",
			chars, p.cfg.MaxInputChars), false)
	}

	// One slot per in-flight call, and never a goroutine per request. The wait
	// is bounded by the caller's context, not by the provider timeout, so a busy
	// pool costs queue time and not provider budget.
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return zero, contextFailure(ctx)
	}

	images := selectImages(input.Images, p.cfg.MaxImages, p.cfg.MaxImageBytes)

	start := time.Now()
	answer, jobErr := p.complete(ctx, input, images, true)
	if jobErr != nil && jobErr.Code == codeResponseFormatUnsupported {
		// Documented fallback: this provider has no response_format. Ask again
		// without it; the prompt still demands a JSON object, and the answer is
		// still held to the same contract.
		answer, jobErr = p.complete(ctx, input, images, false)
	}
	if jobErr != nil {
		if jobErr.Code == codeResponseFormatUnsupported {
			jobErr = newError(CodeRequestRejected, jobErr.Message, false)
		}
		return zero, jobErr
	}

	result, err := DecodeOutput(answer.Content)
	if err != nil {
		return zero, err
	}
	result.Model = answer.Model
	if result.Model == "" {
		result.Model = p.cfg.Model
	}
	result.InputTokens = answer.PromptTokens
	result.OutputTokens = answer.CompletionTokens
	result.Duration = time.Since(start)
	return result, nil
}

// chatRequest is the Chat Completions request body. It is a typed struct rather
// than a map so the shape cannot drift, and so no field can be added by
// accident.
type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

// chatAnswer is the part of a provider answer this package keeps.
type chatAnswer struct {
	Model            string
	Content          string
	PromptTokens     int
	CompletionTokens int
}

// chatResponse is the provider wire shape.
//
// Content is kept raw because providers disagree about whether it is a string
// or a list of blocks; see messageContent.
type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// complete performs one Chat Completions call and classifies everything that
// can go wrong with it. It never retries.
func (p *Processor) complete(ctx context.Context, input automation.PolishInput, images []automation.ImageInput, withResponseFormat bool) (chatAnswer, *automation.JobError) {
	body, jobErr := p.buildRequest(input, images, withResponseFormat)
	if jobErr != nil {
		return chatAnswer{}, jobErr
	}

	reqCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + chatCompletionsPath
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return chatAnswer{}, newError(CodeNotConfigured,
			"automation.ai.base_url could not be turned into a request: "+err.Error(), false)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// The credential is a header and nothing else. It is never part of a URL, a
	// body, a message or a result.
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return chatAnswer{}, p.requestFailure(ctx, reqCtx, err)
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if readErr != nil {
		return chatAnswer{}, newError(CodeServerError,
			"the AI provider's answer could not be read: "+readErr.Error(), true)
	}
	if int64(len(raw)) > maxResponseBytes {
		return chatAnswer{}, newError(CodeInvalidOutput,
			"the AI provider's answer is larger than this client will read", false)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return chatAnswer{}, p.statusFailure(resp, raw, len(images) > 0, withResponseFormat)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return chatAnswer{}, newError(CodeInvalidOutput,
			"the AI provider's answer is not the expected JSON document", false)
	}
	content, jobErr := messageContent(parsed)
	if jobErr != nil {
		return chatAnswer{}, jobErr
	}

	return chatAnswer{
		Model:            parsed.Model,
		Content:          content,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
	}, nil
}

// buildRequest assembles the request body.
func (p *Processor) buildRequest(input automation.PolishInput, images []automation.ImageInput, withResponseFormat bool) ([]byte, *automation.JobError) {
	var userContent any = buildSourceBlock(input.Text)
	if len(images) > 0 {
		parts := []any{map[string]any{"type": "text", "text": buildSourceBlock(input.Text)}}
		for _, img := range images {
			part := imageContentPart(img)
			if part == nil {
				// Cannot happen for selectImages output, but a nil part must
				// never be turned into an empty object.
				continue
			}
			parts = append(parts, part)
		}
		userContent = parts
	}

	req := chatRequest{
		Model: p.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: buildSystemPrompt(input.Prompt)},
			{Role: "user", Content: userContent},
		},
		MaxTokens:   p.cfg.MaxOutputTokens,
		Temperature: temperature,
	}
	if withResponseFormat {
		req.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, newError(CodeInvalidOutput, "the AI request could not be encoded: "+err.Error(), false)
	}
	return body, nil
}

// messageContent extracts the assistant's text from an answer.
//
// The common shape is a plain string. A list of content blocks is also read,
// taking the text parts in order, because some compatible providers answer that
// way even on this endpoint. Anything else is refused rather than guessed at.
func messageContent(parsed chatResponse) (string, *automation.JobError) {
	if len(parsed.Choices) == 0 {
		return "", newError(CodeInvalidOutput, "the AI provider returned no choices", false)
	}
	raw := parsed.Choices[0].Message.Content

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil && blocks != nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type == "text" || block.Type == "" {
				b.WriteString(block.Text)
			}
		}
		return b.String(), nil
	}
	return "", newError(CodeInvalidOutput,
		"the AI provider returned a message that is not text", false)
}

// statusFailure turns a non-2xx answer into a classified error.
//
// The ordering matters and is deliberate. A provider that refuses image input
// is refused back as an image failure even when a response_format fallback is
// still on the table, because dropping the images to make the call succeed
// would be the silent degradation this release is written against.
func (p *Processor) statusFailure(resp *http.Response, body []byte, imagesSent, withResponseFormat bool) *automation.JobError {
	excerpt := p.describe(body)
	message := fmt.Sprintf("the AI provider returned %d: %s", resp.StatusCode, excerpt)

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return newRetryableAfter(CodeRateLimited, message, parseRetryAfter(resp.Header.Get("Retry-After")))
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// Names the setting, never the value.
		return newError(CodeUnauthorized, fmt.Sprintf(
			"the AI provider rejected the credential resolved from automation.ai.api_key_env (status %d): %s",
			resp.StatusCode, excerpt), false)
	case resp.StatusCode == http.StatusBadRequest:
		if imagesSent && looksLikeImageRejection(body) {
			return newError(CodeImagesUnsupported, message, false)
		}
		if withResponseFormat {
			return newError(codeResponseFormatUnsupported, message, false)
		}
		if imagesSent {
			return newError(CodeImagesUnsupported, message, false)
		}
		return newError(CodeRequestRejected, message, false)
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooEarly,
		resp.StatusCode >= 500:
		return newError(CodeServerError, message, true)
	default:
		return newError(CodeRequestRejected, message, false)
	}
}

// describe renders a provider error body as a short, scrubbed excerpt.
func (p *Processor) describe(body []byte) string {
	s := strings.TrimSpace(scrub(p.cfg.APIKey, string(body)))
	if s == "" {
		return "no detail"
	}
	if utf8.RuneCountInString(s) > maxErrorExcerpt {
		s = string([]rune(s)[:maxErrorExcerpt]) + "…"
	}
	return s
}

// looksLikeImageRejection reports whether a provider refusal is about the image
// input. Only image vocabulary counts: a refusal about response_format must
// reach the fallback instead of being mistaken for a vision problem.
func looksLikeImageRejection(body []byte) bool {
	s := strings.ToLower(string(body))
	for _, keyword := range []string{"image", "vision", "multimodal", "image_url", "base64"} {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

// parseRetryAfter reads a Retry-After header in either of its two legal forms.
// A missing or unreadable value means no provider-supplied delay, and the
// queue's own backoff applies.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// requestFailure maps a transport failure onto the classified vocabulary.
//
// The three cases are distinct and a caller can act on each: the caller gave
// up, the provider did not answer in time, or the provider could not be reached
// at all. Only the last two are worth another attempt.
func (p *Processor) requestFailure(parent, reqCtx context.Context, err error) *automation.JobError {
	if errors.Is(parent.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return newError(CodeCancelled, "the AI request was cancelled", false)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
		return newError(CodeTimeout, "the AI request did not finish in time", true)
	}
	return newError(CodeServerError,
		"the AI provider could not be reached: "+scrub(p.cfg.APIKey, err.Error()), true)
}

// contextFailure maps a cancelled or expired context onto the classified
// vocabulary. A caller that gave up is not the provider's fault, so it is
// permanent; a deadline is worth another attempt.
func contextFailure(ctx context.Context) *automation.JobError {
	if errors.Is(ctx.Err(), context.Canceled) {
		return newError(CodeCancelled, "the AI request was cancelled", false)
	}
	return newError(CodeTimeout, "the AI request did not finish in time", true)
}
