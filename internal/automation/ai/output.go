package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// Bounds on the structured output. They exist so a runaway answer cannot turn
// into an unbounded row in the database or an unbounded message to a channel.
const (
	// maxTitleRunes is the longest title the contract accepts.
	maxTitleRunes = 1024
	// maxTokenRunes is the longest single hashtag or mention.
	maxTokenRunes = 256
	// maxTokensPerKind is how many hashtags or mentions one answer may declare.
	maxTokensPerKind = 128
)

// rawOutput is the wire shape of the structured output contract.
//
// Every optional field is a pointer so that "absent", "null" and "present" stay
// distinguishable, which is what lets the contract say title may be absent
// rather than forcing the model to invent an empty one.
//
// There is no destination, permission, tool, URL or markup field here, and
// DecodeOutput refuses any JSON object that carries one. That is the structural
// half of the guarantee that an AI answer cannot move a publication anywhere:
// the other half is that this type is the only thing a caller ever sees.
type rawOutput struct {
	Text     *string   `json:"text"`
	Title    *string   `json:"title"`
	Hashtags *[]string `json:"hashtags"`
	Mentions *[]string `json:"mentions"`
}

// DecodeOutput validates and normalizes one model answer into the structured
// output contract.
//
// It accepts exactly this shape:
//
//	{"text":"...","title":"... or absent","hashtags":["..."],"mentions":["..."]}
//
// and refuses everything else. Refusal is the point: the answer is either the
// contract or it is an error, never a partial result a caller has to guess at.
//
// Two normalizations are applied and they are the only ones:
//
//   - one surrounding Markdown code fence around the whole JSON object is
//     removed, because a model that was told to answer in JSON often answers in
//     ```json ... ``` anyway and the enclosed document is unchanged;
//   - surrounding whitespace is trimmed from text and title, and hashtags and
//     mentions are trimmed, have a single leading # or @ removed, and are
//     de-duplicated in first-seen order.
//
// Nothing is invented, extended or repaired beyond that.
func DecodeOutput(raw string) (automation.PolishResult, error) {
	invalid := func(format string, args ...any) (automation.PolishResult, error) {
		return automation.PolishResult{}, newError(CodeInvalidOutput, "the AI response is not in the structured output contract: "+fmt.Sprintf(format, args...), false)
	}

	body := stripCodeFence(raw)
	if strings.TrimSpace(body) == "" {
		return invalid("the model returned no content")
	}

	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	var out rawOutput
	if err := dec.Decode(&out); err != nil {
		return invalid("%s", jsonFailure(err))
	}
	// A second JSON value behind the first is not part of the contract.
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return invalid("the model returned more than one JSON value")
	}

	var result automation.PolishResult

	if out.Text == nil {
		return invalid(`"text" is required`)
	}
	text := strings.TrimSpace(*out.Text)
	if text == "" {
		return invalid(`"text" must not be empty`)
	}
	if err := rejectControlRunes(text); err != nil {
		return invalid("%s", err.Error())
	}
	result.Text = text

	if out.Title != nil {
		title := strings.TrimSpace(*out.Title)
		if utf8.RuneCountInString(title) > maxTitleRunes {
			return invalid(`"title" is longer than %d characters`, maxTitleRunes)
		}
		if err := rejectControlRunes(title); err != nil {
			return invalid("%s", err.Error())
		}
		result.Title = title
	}

	tags, err := normalizeTokens('#', out.Hashtags)
	if err != nil {
		return invalid("%s", err.Error())
	}
	result.Hashtags = tags

	mentions, err := normalizeTokens('@', out.Mentions)
	if err != nil {
		return invalid("%s", err.Error())
	}
	result.Mentions = mentions

	return result, nil
}

// normalizeTokens turns a declared list into bare, safe tokens.
//
// The sigil is stripped so the value is the token itself; RebuildEntities puts
// the sigil back when it finds the token in the text. A token that carries
// whitespace, a control character, or any character that could close a markup
// context or build a path is refused whole rather than cleaned up, because a
// silently repaired token is a token the operator did not ask for.
func normalizeTokens(sigil byte, declared *[]string) ([]string, error) {
	if declared == nil {
		return nil, nil
	}
	if len(*declared) > maxTokensPerKind {
		return nil, errors.New("the AI declared more tokens than the contract allows")
	}
	var out []string
	seen := make(map[string]bool, len(*declared))
	for _, raw := range *declared {
		tok := strings.TrimSpace(raw)
		tok = strings.TrimSpace(strings.TrimPrefix(tok, string(sigil)))
		if tok == "" {
			// An empty entry is noise, not an attack: drop it rather than fail
			// a whole publication over a trailing comma in the model's answer.
			continue
		}
		if utf8.RuneCountInString(tok) > maxTokenRunes {
			return nil, errors.New("the AI declared a token longer than the contract allows")
		}
		if err := rejectUnsafeToken(tok); err != nil {
			return nil, err
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out, nil
}

// unsafeTokenRunes are the characters a declared token may never carry: the
// ones that close a markup or attribute context, build a path, or introduce a
// second sigil. A hashtag or mention is a bare word and nothing else.
const unsafeTokenRunes = "<>\"'&/\\#@`"

// rejectUnsafeToken refuses a token carrying anything that is not plain text.
func rejectUnsafeToken(tok string) error {
	for _, r := range tok {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("the AI declared a token containing whitespace or a control character")
		}
		if strings.ContainsRune(unsafeTokenRunes, r) {
			return errors.New("the AI declared a token containing a markup, path or sigil character")
		}
	}
	return nil
}

// rejectControlRunes refuses control characters in text a caller will publish.
// Newlines and tabs are ordinary message content and stay.
func rejectControlRunes(s string) error {
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return errors.New("the AI returned text containing a control character")
		}
	}
	return nil
}

// stripCodeFence removes one surrounding Markdown fence from a JSON envelope.
//
// It removes nothing else: a fence in the middle of the document, an unclosed
// fence, or prose before the fence is left alone and fails to parse, which is
// the honest outcome.
func stripCodeFence(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	open := strings.IndexByte(s, '\n')
	if open < 0 {
		return s
	}
	body := s[open+1:]
	end := strings.LastIndex(body, "```")
	if end < 0 {
		return s
	}
	return strings.TrimSpace(body[:end])
}

// jsonFailure turns a decoder error into a short explanation that cannot carry
// response content back to the caller.
func jsonFailure(err error) string {
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return "the model returned invalid JSON"
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return "the model returned " + typeErr.Value + " where " + typeErr.Type.String() + " is required"
	}
	if strings.Contains(err.Error(), "unknown field") {
		return "the model returned a field outside the structured output contract"
	}
	return "the model returned invalid JSON"
}

// RebuildEntities derives the formatting spans of polished text.
//
// The source message's spans are never reused. They describe a different
// string: the model rewrote the text, so the source's UTF-16 offsets point at
// whatever happens to sit at those positions now. Instead every span here is
// found by scanning text itself and carries offsets computed against text and
// against nothing else.
//
// It returns the spans it could prove, and the declared tokens it could not
// place. A declared token that does not appear in the text is reported in
// unrecoverable and given no entity: an absent entity is a formatting loss the
// operator can see, while a wrong offset silently bolds or links the wrong
// characters. When nothing can be proven, spans is empty and the caller must
// publish plain text.
func RebuildEntities(text string, polished automation.PolishResult) (spans []rpc.Entity, unrecoverable []string) {
	covered := make(map[string]bool)

	for _, e := range scanTokens(text) {
		// Prove the span before it is allowed to leave this function. A span
		// that fails here is dropped, never returned as a guess.
		coveredText, ok := tgapi.UTF16Slice(text, e.Offset, e.Length)
		if !ok || !tgapi.ValidEntity(text, tgapi.Entity{Type: e.Type, Offset: e.Offset, Length: e.Length}) {
			continue
		}
		spans = append(spans, e)
		covered[strings.ToLower(strings.TrimLeft(coveredText, "#@"))] = true
	}

	for _, tok := range polished.Hashtags {
		if !covered[strings.ToLower(tok)] {
			unrecoverable = append(unrecoverable, "#"+tok)
		}
	}
	for _, tok := range polished.Mentions {
		if !covered[strings.ToLower(tok)] {
			unrecoverable = append(unrecoverable, "@"+tok)
		}
	}
	return spans, unrecoverable
}

// scanTokens finds every hashtag and mention in text and returns entities whose
// offsets are UTF-16 code units, as Telegram counts them.
//
// A sigil only starts a token at a word boundary, so an email address is not a
// mention and abc#def is not a hashtag.
func scanTokens(text string) []rpc.Entity {
	var out []rpc.Entity

	unit := 0
	prev := rune(0) // start of string counts as a boundary
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if (r == '#' || r == '@') && isTokenBoundary(prev) {
			token, tokenBytes := readToken(text[i+size:], r)
			if token != "" {
				length := 1 + tgapi.UTF16Len(token)
				out = append(out, rpc.Entity{
					Type:   tokenKind(r),
					Offset: unit,
					Length: length,
				})
				unit += length
				i += size + tokenBytes
				prev = ' ' // a sigil directly after a token still starts one
				continue
			}
		}
		if r > 0xFFFF {
			unit += 2
		} else {
			unit++
		}
		prev = r
		i += size
	}
	return out
}

// readToken returns the token body following a sigil and its length in bytes.
func readToken(s string, sigil rune) (string, int) {
	n := 0
	for _, r := range s {
		if !tokenRune(r, sigil) {
			break
		}
		n += utf8.RuneLen(r)
	}
	if n == 0 {
		return "", 0
	}
	return s[:n], n
}

// tokenRune reports whether r may appear in a token introduced by sigil.
//
// Hashtags allow any letter or digit and the zero-width non-joiner that Persian
// compound words are spelled with. Mentions allow only the ASCII username
// alphabet Telegram itself defines.
func tokenRune(r, sigil rune) bool {
	if r == sigil {
		return false
	}
	if sigil == '#' {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '‌'
	}
	return r == '_' || isASCIIAlphaNumeric(r)
}

func isASCIIAlphaNumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// isTokenBoundary reports whether a sigil after prev starts a new token.
func isTokenBoundary(prev rune) bool {
	if prev == 0 {
		return true
	}
	if unicode.IsLetter(prev) || unicode.IsDigit(prev) {
		return false
	}
	return !strings.ContainsRune("_.-+/", prev)
}

func tokenKind(sigil rune) string {
	if sigil == '@' {
		return tgapi.EntityMention
	}
	return tgapi.EntityHashtag
}
