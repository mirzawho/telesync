package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
)

// This file is the real-provider reproduction for the reported case: a
// workflow whose operator asked for a friendlier, longer, more detailed edit of
// one Persian message, and whose run recorded a successful polish with the
// source text unchanged.
//
// It spends real tokens, so it only runs when the operator asks for it:
//
//	TELESYNC_AI_LIVE=1 go test ./internal/automation/ai -run TestLiveProvider -v
//
// It never publishes anything: it stops at the Chat Completions response. The
// key is read from the deployment's own configuration and is never printed.

const (
	// reportedPrompt is the workflow's own editing instruction.
	reportedLivePrompt = "هر متنی که برای تو داره ارسال میشه برای من با لحنی بسیار دوستانه و صمیمانه بکنی و متن رو طولانی تر و جزئیات برام بکنی"
	// reportedLiveSource is the source message from the reported run.
	reportedLiveSource = "سلام به نازنین آدم های دنیا که در آفرینش این کانال کت همکاری می کنند."
)

func liveConfig(t *testing.T) appconfig.AIConfig {
	t.Helper()
	path := os.Getenv("TELESYNC_CONFIG")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("home: %v", err)
		}
		path = filepath.Join(home, ".telesync", "config.json")
	}
	cfg, err := appconfig.Load(path, func(k string) string { return os.Getenv(k) })
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if !cfg.Automation.AI.Enabled {
		t.Fatal("automation.ai.enabled is false in the deployment's own configuration")
	}
	if cfg.Automation.AI.APIKey == "" {
		t.Fatal("no API key resolved: set automation.ai.api_key or the variable it names")
	}
	return cfg.Automation.AI
}

// liveAnswer is the part of a provider answer this diagnostic reports. It is
// deliberately wider than chatAnswer: the finish reason is what decides between
// "the model echoed" and "the answer was cut off".
type liveAnswer struct {
	Status         int
	Model          string
	FinishReason   string
	Content        string
	Refusal        string
	ReasoningChars int
	PromptTokens   int
	OutputTokens   int
}

func TestLiveProviderReproduction(t *testing.T) {
	if os.Getenv("TELESYNC_AI_LIVE") == "" {
		t.Skip("set TELESYNC_AI_LIVE=1 to call the configured provider; this spends real tokens")
	}
	cfg := liveConfig(t)
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	input := automation.PolishInput{WorkflowID: 1, Prompt: reportedLivePrompt, Text: reportedLiveSource}

	// One request built by the production code path, sent by hand so the raw
	// answer is visible. Nothing about the request differs from what a run sends.
	body, jobErr := p.buildRequest(input, nil, true, false)
	if jobErr != nil {
		t.Fatalf("buildRequest: %v", jobErr)
	}

	endpoint := cfg.BaseURL + chatCompletionsPath
	t.Logf("provider     : %s", endpoint)
	t.Logf("model        : %s", cfg.Model)
	t.Logf("max_tokens   : %d", cfg.MaxOutputTokens)
	t.Logf("system chars : %d", utf8.RuneCountInString(buildSystemPrompt(input.Prompt)))
	t.Logf("operator     : %s", input.Prompt)
	t.Logf("source       : %s", input.Text)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("provider call failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	t.Logf("status       : %d", resp.StatusCode)

	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   json.RawMessage `json:"content"`
				Refusal   string          `json:"refusal"`
				Reasoning string          `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("answer is not JSON (%d bytes): %v\n%.400s", len(raw), err, raw)
	}

	out := liveAnswer{
		Status:       resp.StatusCode,
		Model:        parsed.Model,
		PromptTokens: parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}
	if len(parsed.Choices) > 0 {
		out.FinishReason = parsed.Choices[0].FinishReason
		out.Refusal = parsed.Choices[0].Message.Refusal
		out.ReasoningChars = utf8.RuneCountInString(parsed.Choices[0].Message.Reasoning)
		var text string
		if err := json.Unmarshal(parsed.Choices[0].Message.Content, &text); err == nil {
			out.Content = text
		} else {
			out.Content = string(parsed.Choices[0].Message.Content)
		}
	}

	t.Logf("model listed : %s", out.Model)
	t.Logf("finish       : %q", out.FinishReason)
	t.Logf("refusal      : %q", out.Refusal)
	t.Logf("reasoning    : %d chars", out.ReasoningChars)
	t.Logf("usage        : in=%d out=%d", out.PromptTokens, out.OutputTokens)
	t.Logf("raw content  : %s", out.Content)
	t.Logf("content == source: %v", out.Content == reportedLiveSource)

	decoded, decErr := DecodeOutput(out.Content)
	if decErr != nil {
		t.Logf("DecodeOutput : ERROR %v", decErr)
	} else {
		t.Logf("parsed text  : %s", decoded.Text)
		t.Logf("parsed == source: %v", decoded.Text == reportedLiveSource)
	}

	// Now the same input through Polish, to see whether the bounded second
	// attempt fires and what it is finally held to.
	pctx, pcancel := context.WithTimeout(context.Background(), 2*cfg.Timeout)
	defer pcancel()
	result, polishErr := p.Polish(pctx, input)
	if polishErr != nil {
		t.Logf("Polish       : ERROR %v", polishErr)
		return
	}
	t.Logf("Polish text  : %s", result.Text)
	t.Logf("Polish == source: %v", result.Text == reportedLiveSource)
	t.Logf("Polish usage : in=%d out=%d", result.InputTokens, result.OutputTokens)
}

// TestLiveProviderInstructionPlacement isolates one variable: where the
// operator's editing instructions are said. Production puts them at the end of
// a ~2800-character system message, behind a "supplementary" header; this asks
// the same thing with the instruction in the user turn, as the task itself.
//
// One call. Compare the printed result with TestLiveProviderReproduction's.
func TestLiveProviderInstructionPlacement(t *testing.T) {
	if os.Getenv("TELESYNC_AI_LIVE") == "" {
		t.Skip("set TELESYNC_AI_LIVE=1 to call the configured provider; this spends real tokens")
	}
	cfg := liveConfig(t)
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The security and output contract stays in the system turn. The operator's
	// instruction moves to the user turn, where the task is.
	body, jobErr := p.buildRequest(automation.PolishInput{
		WorkflowID: 1,
		Prompt:     "",
		Text:       reportedLiveSource,
	}, nil, true, false)
	if jobErr != nil {
		t.Fatalf("buildRequest: %v", jobErr)
	}
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	req.Messages[1].Content = "Editing instructions:\n" + reportedLivePrompt +
		"\n\n" + buildSourceBlock(reportedLiveSource)
	body, err = json.Marshal(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.BaseURL+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("provider call failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))

	var parsed struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("answer is not JSON: %v\n%.400s", err, raw)
	}
	var text string
	if len(parsed.Choices) > 0 {
		t.Logf("finish       : %q", parsed.Choices[0].FinishReason)
		_ = json.Unmarshal(parsed.Choices[0].Message.Content, &text)
	}
	t.Logf("usage        : in=%d out=%d", parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens)
	t.Logf("source chars : %d", utf8.RuneCountInString(reportedLiveSource))
	t.Logf("answer chars : %d", utf8.RuneCountInString(text))
	t.Logf("raw content  : %s", text)
	decoded, decErr := DecodeOutput(text)
	if decErr != nil {
		t.Logf("DecodeOutput : ERROR %v", decErr)
		return
	}
	t.Logf("parsed text  : %s", decoded.Text)
}

// TestLiveProviderMinimalInstruction isolates the other variable: whether the
// model will expand at all when nothing of ours constrains it. The system turn
// is a bare task and nothing else; the source is the only other content.
//
// One call. If this expands and the other two did not, the constraint is our
// system prompt. If this also only normalises orthography, the model is not
// performing the transformation and no wording of ours will make it.
func TestLiveProviderMinimalInstruction(t *testing.T) {
	if os.Getenv("TELESYNC_AI_LIVE") == "" {
		t.Skip("set TELESYNC_AI_LIVE=1 to call the configured provider; this spends real tokens")
	}
	cfg := liveConfig(t)
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body, jobErr := p.buildRequest(automation.PolishInput{
		WorkflowID: 1,
		Text:       reportedLiveSource,
	}, nil, true, false)
	if jobErr != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	req.Messages[0].Content = "You are a content editor. Rewrite the message below in a warm, " +
		"friendly, informal tone and make it noticeably longer, adding expressive detail " +
		"without inventing any new facts. Reply with one JSON object and nothing else: " +
		`{"text":"...","title":null,"hashtags":[],"mentions":[]}`
	body, err = json.Marshal(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.BaseURL+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("provider call failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))

	var parsed struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("answer is not JSON: %v\n%.400s", err, raw)
	}
	var text string
	if len(parsed.Choices) > 0 {
		t.Logf("finish       : %q", parsed.Choices[0].FinishReason)
		_ = json.Unmarshal(parsed.Choices[0].Message.Content, &text)
	}
	t.Logf("usage        : in=%d out=%d", parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens)
	t.Logf("source chars : %d", utf8.RuneCountInString(reportedLiveSource))
	t.Logf("answer chars : %d", utf8.RuneCountInString(text))
	t.Logf("raw content  : %s", text)
}
