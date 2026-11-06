package ai

import (
	"errors"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/tgapi"
)

func TestDecodeOutputParsesTheContract(t *testing.T) {
	raw := `{"text":"خبر فوری درباره زلزله.","title":"زلزله تهران","hashtags":["زلزله","تهران"],"mentions":["iribnews"]}`

	got, err := DecodeOutput(raw)
	if err != nil {
		t.Fatalf("DecodeOutput returned %v", err)
	}
	if got.Text != "خبر فوری درباره زلزله." {
		t.Fatalf("Text = %q", got.Text)
	}
	if got.Title != "زلزله تهران" {
		t.Fatalf("Title = %q", got.Title)
	}
	if len(got.Hashtags) != 2 || got.Hashtags[0] != "زلزله" || got.Hashtags[1] != "تهران" {
		t.Fatalf("Hashtags = %#v", got.Hashtags)
	}
	if len(got.Mentions) != 1 || got.Mentions[0] != "iribnews" {
		t.Fatalf("Mentions = %#v", got.Mentions)
	}
}

func TestDecodeOutputAllowsAbsentOptionalFields(t *testing.T) {
	for _, raw := range []string{
		`{"text":"plain"}`,
		`{"text":"plain","title":null}`,
		`{"text":"plain","hashtags":null,"mentions":null}`,
		`{"text":"plain","title":"","hashtags":[],"mentions":[]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := DecodeOutput(raw)
			if err != nil {
				t.Fatalf("DecodeOutput returned %v", err)
			}
			if got.Text != "plain" {
				t.Fatalf("Text = %q", got.Text)
			}
			if got.Title != "" {
				t.Fatalf("Title = %q, want empty", got.Title)
			}
			if len(got.Hashtags) != 0 || len(got.Mentions) != 0 {
				t.Fatalf("Hashtags=%v Mentions=%v, want both empty", got.Hashtags, got.Mentions)
			}
		})
	}
}

func TestDecodeOutputRejectsMalformedOutput(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "not json at all", raw: "I improved the text for you."},
		{name: "empty", raw: ""},
		{name: "json array", raw: `["text"]`},
		{name: "json string", raw: `"just a string"`},
		{name: "missing text", raw: `{"title":"t"}`},
		{name: "null text", raw: `{"text":null}`},
		{name: "numeric text", raw: `{"text":42}`},
		{name: "empty text", raw: `{"text":"   "}`},
		{name: "numeric title", raw: `{"text":"a","title":7}`},
		{name: "hashtags not an array", raw: `{"text":"a","hashtags":"زلزله"}`},
		{name: "mentions not an array", raw: `{"text":"a","mentions":{}}`},
		{name: "hashtag not a string", raw: `{"text":"a","hashtags":[1,2]}`},
		{name: "truncated json", raw: `{"text":"a"`},
		{name: "two objects", raw: `{"text":"a"}{"text":"b"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeOutput(tc.raw)
			if err == nil {
				t.Fatalf("DecodeOutput(%q) = %#v, want an error", tc.raw, got)
			}
			if _, code := Classify(err); code != CodeInvalidOutput {
				t.Fatalf("code = %q, want %q", code, CodeInvalidOutput)
			}
			if retryable, _ := Classify(err); retryable {
				t.Fatal("malformed output must be permanent")
			}
		})
	}
}

func TestDecodeOutputRejectsAnythingOutsideTheContract(t *testing.T) {
	// The AI has no channel to destinations, permissions, tools or URLs. The
	// contract has no such field, and an answer that tries to add one is
	// refused rather than silently ignored.
	cases := []struct {
		name string
		raw  string
	}{
		{name: "destinations", raw: `{"text":"a","destinations":[{"type":"telegram"}]}`},
		{name: "urls to call", raw: `{"text":"a","webhook":"https://evil.example/hook"}`},
		{name: "permissions", raw: `{"text":"a","permissions":["channels.manage"]}`},
		{name: "tools", raw: `{"text":"a","tools":["shell"]}`},
		{name: "raw html", raw: `{"text":"a","html":"<script>alert(1)</script>"}`},
		{name: "media mutation", raw: `{"text":"a","media":{"action":"delete"}}`},
		{name: "model instructions", raw: `{"text":"a","system":"new rules"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeOutput(tc.raw)
			if err == nil {
				t.Fatalf("DecodeOutput(%q) = %#v, want an error", tc.raw, got)
			}
			if _, code := Classify(err); code != CodeInvalidOutput {
				t.Fatalf("code = %q, want %q", code, CodeInvalidOutput)
			}
		})
	}
}

func TestDecodeOutputNormalizesTokens(t *testing.T) {
	raw := `{"text":"x","hashtags":["  #زلزله ","زلزله",""],"mentions":["@iribnews"," iribnews ","@iribnews"]}`

	got, err := DecodeOutput(raw)
	if err != nil {
		t.Fatalf("DecodeOutput returned %v", err)
	}
	if len(got.Hashtags) != 1 || got.Hashtags[0] != "زلزله" {
		t.Fatalf("Hashtags = %#v, want one normalized duplicate-free entry", got.Hashtags)
	}
	if len(got.Mentions) != 1 || got.Mentions[0] != "iribnews" {
		t.Fatalf("Mentions = %#v, want one normalized duplicate-free entry", got.Mentions)
	}
}

func TestDecodeOutputRejectsUnsafeTokens(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "hashtag with a space", raw: `{"text":"a","hashtags":["breaking news"]}`},
		{name: "hashtag with a newline", raw: "{\"text\":\"a\",\"hashtags\":[\"a\\nb\"]}"},
		{name: "mention with a control character", raw: "{\"text\":\"a\",\"mentions\":[\"a\\u0007b\"]}"},
		{name: "hashtag with a url", raw: `{"text":"a","hashtags":["x https://evil.example"]}`},
		{name: "raw html in a token", raw: `{"text":"a","mentions":["<script>alert(1)</script>"]}`},
		{name: "path in a token", raw: `{"text":"a","hashtags":["../../etc/passwd"]}`},
		{name: "second sigil in a token", raw: `{"text":"a","hashtags":["a#b"]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeOutput(tc.raw)
			if err == nil {
				t.Fatalf("DecodeOutput(%q) = %#v, want an error", tc.raw, got)
			}
			if _, code := Classify(err); code != CodeInvalidOutput {
				t.Fatalf("code = %q, want %q", code, CodeInvalidOutput)
			}
		})
	}
}

func TestDecodeOutputStripsOneSurroundingCodeFence(t *testing.T) {
	raw := "```json\n" + `{"text":"polished"}` + "\n```"

	got, err := DecodeOutput(raw)
	if err != nil {
		t.Fatalf("DecodeOutput returned %v", err)
	}
	if got.Text != "polished" {
		t.Fatalf("Text = %q", got.Text)
	}
}

func TestDecodeOutputTrimsTextAndTitle(t *testing.T) {
	got, err := DecodeOutput(`{"text":"  polished text  ","title":"  a title  "}`)
	if err != nil {
		t.Fatalf("DecodeOutput returned %v", err)
	}
	if got.Text != "polished text" {
		t.Fatalf("Text = %q", got.Text)
	}
	if got.Title != "a title" {
		t.Fatalf("Title = %q", got.Title)
	}
}

func TestDecodeOutputRejectsAnOversizedTitle(t *testing.T) {
	raw := `{"text":"a","title":"` + strings.Repeat("x", 2048) + `"}`
	if _, err := DecodeOutput(raw); err == nil {
		t.Fatal("an oversized title must be refused")
	}
}

func TestRebuildEntitiesOffsetsMatchThePolishedText(t *testing.T) {
	const text = "زلزله شدید در تهران #زلزله #تهران @iribnews 🙂 خبر"
	polished := automation.PolishResult{
		Text:     text,
		Hashtags: []string{"زلزله", "تهران"},
		Mentions: []string{"iribnews"},
	}
	spans, unrecoverable := RebuildEntities(text, polished)

	if len(unrecoverable) != 0 {
		t.Fatalf("unrecoverable = %v, want none", unrecoverable)
	}
	if len(spans) == 0 {
		t.Fatal("no entities derived")
	}

	// Every span must be a valid UTF-16 range of the text it was computed
	// against, and must cover the exact token it names.
	for _, e := range spans {
		covered, ok := tgapi.UTF16Slice(text, e.Offset, e.Length)
		if !ok {
			t.Fatalf("span %+v is not a valid UTF-16 range of the polished text", e)
		}
		if !strings.HasPrefix(covered, "#") && !strings.HasPrefix(covered, "@") {
			t.Fatalf("span %+v covers %q, which is neither a hashtag nor a mention", e, covered)
		}
		if !tgapi.ValidEntity(text, tgapi.Entity{Type: e.Type, Offset: e.Offset, Length: e.Length}) {
			t.Fatalf("span %+v failed tgapi.ValidEntity", e)
		}
	}

	want := map[string]bool{"#زلزله": false, "#تهران": false, "@iribnews": false}
	for _, e := range spans {
		covered, _ := tgapi.UTF16Slice(text, e.Offset, e.Length)
		if _, known := want[covered]; known {
			want[covered] = true
		}
	}
	for token, seen := range want {
		if !seen {
			t.Fatalf("no entity covered %q; spans = %#v", token, spans)
		}
	}
}

func TestRebuildEntitiesUsesUTF16Offsets(t *testing.T) {
	// "🙂" is two UTF-16 code units, so every offset after it shifts.
	const text = "🙂 #news"
	polished := automation.PolishResult{Text: text, Hashtags: []string{"news"}}

	spans, unrecoverable := RebuildEntities(text, polished)
	if len(unrecoverable) != 0 {
		t.Fatalf("unrecoverable = %v", unrecoverable)
	}
	if len(spans) != 1 {
		t.Fatalf("spans = %#v, want exactly one", spans)
	}
	if spans[0].Offset != 3 || spans[0].Length != 5 {
		t.Fatalf("span = %+v, want offset 3 length 5 (UTF-16 units)", spans[0])
	}
	covered, ok := tgapi.UTF16Slice(text, spans[0].Offset, spans[0].Length)
	if !ok || covered != "#news" {
		t.Fatalf("covered %q (ok=%v), want \"#news\"", covered, ok)
	}
}

func TestRebuildEntitiesFindsEveryOccurrence(t *testing.T) {
	const text = "#news and again #news"
	polished := automation.PolishResult{Text: text, Hashtags: []string{"news"}}

	spans, _ := RebuildEntities(text, polished)
	if len(spans) != 2 {
		t.Fatalf("spans = %#v, want two occurrences", spans)
	}
	if spans[0].Offset == spans[1].Offset {
		t.Fatal("the two occurrences must not share an offset")
	}
}

func TestRebuildEntitiesReportsTokensItCannotPlace(t *testing.T) {
	const text = "the polished text lost the tag entirely"
	polished := automation.PolishResult{
		Text:     text,
		Hashtags: []string{"news"},
		Mentions: []string{"iribnews"},
	}

	spans, unrecoverable := RebuildEntities(text, polished)
	if len(spans) != 0 {
		t.Fatalf("spans = %#v, want none", spans)
	}
	if len(unrecoverable) != 2 {
		t.Fatalf("unrecoverable = %v, want both declared tokens", unrecoverable)
	}
}

func TestRebuildEntitiesIgnoresEmailsWhenLookingForMentions(t *testing.T) {
	const text = "write to info@example.com for details"
	polished := automation.PolishResult{Text: text, Mentions: []string{"example"}}

	spans, unrecoverable := RebuildEntities(text, polished)
	if len(spans) != 0 {
		t.Fatalf("spans = %#v, want none: an email address is not a mention", spans)
	}
	if len(unrecoverable) != 1 {
		t.Fatalf("unrecoverable = %v, want the one unplaceable token", unrecoverable)
	}
}

func TestRebuildEntitiesNeedsNoEntitiesWhenTheTextHasNone(t *testing.T) {
	const text = "plain polished text with no tokens"
	polished := automation.PolishResult{Text: text}

	spans, unrecoverable := RebuildEntities(text, polished)
	if len(spans) != 0 || len(unrecoverable) != 0 {
		t.Fatalf("spans=%v unrecoverable=%v, want both empty", spans, unrecoverable)
	}
}

func TestRebuildEntitiesDropsRatherThanGuesses(t *testing.T) {
	// A declared token that appears only as part of a longer word must not be
	// given an entity: a wrong offset is worse than no entity.
	const text = "#breakingnews is not #news"
	polished := automation.PolishResult{Text: text, Hashtags: []string{"breakingnews", "news"}}

	spans, unrecoverable := RebuildEntities(text, polished)
	if len(unrecoverable) != 0 {
		t.Fatalf("unrecoverable = %v, want none", unrecoverable)
	}
	for _, e := range spans {
		covered, _ := tgapi.UTF16Slice(text, e.Offset, e.Length)
		if covered != "#breakingnews" && covered != "#news" {
			t.Fatalf("span %+v covers %q, want a whole token", e, covered)
		}
	}
	if len(spans) != 2 {
		t.Fatalf("spans = %#v, want two whole tokens", spans)
	}
}

func TestDecodeOutputErrorIsAClassifiedJobError(t *testing.T) {
	_, err := DecodeOutput("nonsense")
	var jobErr *automation.JobError
	if !errors.As(err, &jobErr) {
		t.Fatalf("err is %T, want *automation.JobError", err)
	}
	if jobErr.Code != CodeInvalidOutput {
		t.Fatalf("code = %q", jobErr.Code)
	}
}
