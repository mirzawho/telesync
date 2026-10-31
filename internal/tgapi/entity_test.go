package tgapi_test

import (
	"testing"

	"github.com/mirza/telesync/internal/tgapi"
)

// Telegram reports and accepts entity offsets in UTF-16 code units. A Go
// string's byte length or rune count is not that number: an emoji outside the
// BMP is one rune but two UTF-16 units, and Persian text is one unit per
// character. These tests pin the conversion on both directions.

func TestUTF16LenCountsCodeUnitsNotBytesOrRunes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "abc", 3},
		{"persian", "سلام دنیا", 9},
		{"one astral rune", "🙂", 2},
		{"astral inside ascii", "a🙂b", 4},
		{"flag is two regional indicators", "🇺🇸", 4},
		{"zwnj is one unit", "می‌روم", 6},
		{"mixed", "خبر #فناوری 🙂", 14},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tgapi.UTF16Len(tc.in); got != tc.want {
				t.Fatalf("UTF16Len(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestUTF16SliceReturnsExactlyTheSpannedText(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		offset, length int
		want           string
		wantOK         bool
	}{
		{"ascii word", "hello world", 0, 5, "hello", true},
		{"ascii tail", "hello world", 6, 5, "world", true},
		// "#فناوری" is one ASCII unit plus six Persian units; "خبر" is three.
		{"persian word", "#فناوری خبر", 0, 7, "#فناوری", true},
		{"after persian", "#فناوری خبر", 8, 3, "خبر", true},
		{"the space between", "#فناوری خبر", 7, 1, " ", true},
		{"astral start", "a🙂b", 1, 2, "🙂", true},
		{"astral half is refused", "a🙂b", 1, 1, "", false},
		{"astral through ascii", "a🙂b", 1, 3, "🙂b", true},
		{"zero length", "abc", 1, 0, "", true},
		{"whole string", "abc", 0, 3, "abc", true},
		{"past the end", "abc", 1, 5, "", false},
		{"offset past the end", "abc", 4, 1, "", false},
		{"negative offset", "abc", -1, 1, "", false},
		{"negative length", "abc", 0, -1, "", false},
		{"empty text", "", 0, 0, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tgapi.UTF16Slice(tc.in, tc.offset, tc.length)
			if ok != tc.wantOK {
				t.Fatalf("UTF16Slice(%q, %d, %d) ok = %v, want %v", tc.in, tc.offset, tc.length, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("UTF16Slice(%q, %d, %d) = %q, want %q", tc.in, tc.offset, tc.length, got, tc.want)
			}
		})
	}
}

func TestUTF16SliceOfAMultiByteRunOnlyCoversWholeRunes(t *testing.T) {
	// A span must never cut a rune in half: Telegram's offsets are code units,
	// but a code unit pair is one visible character.
	_, ok := tgapi.UTF16Slice("🙂🙂", 2, 1)
	if ok {
		t.Fatal("half a surrogate pair must not be a valid span")
	}
	got, ok := tgapi.UTF16Slice("🙂🙂", 2, 2)
	if !ok || got != "🙂" {
		t.Fatalf("second emoji = %q ok=%v", got, ok)
	}
}

func TestEntityValidateRejectsSpansOutsideTheText(t *testing.T) {
	text := "سلام دنیا"
	cases := []struct {
		name string
		e    tgapi.Entity
		want bool
	}{
		{"inside", tgapi.Entity{Type: tgapi.EntityBold, Offset: 0, Length: 4}, true},
		{"exactly the end", tgapi.Entity{Type: tgapi.EntityBold, Offset: 5, Length: 4}, true},
		{"past the end", tgapi.Entity{Type: tgapi.EntityBold, Offset: 5, Length: 5}, false},
		{"negative offset", tgapi.Entity{Type: tgapi.EntityBold, Offset: -1, Length: 1}, false},
		{"negative length", tgapi.Entity{Type: tgapi.EntityBold, Offset: 0, Length: -1}, false},
		{"zero length", tgapi.Entity{Type: tgapi.EntityBold, Offset: 1, Length: 0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tgapi.ValidEntity(text, tc.e); got != tc.want {
				t.Fatalf("ValidEntity(%q, %+v) = %v, want %v", text, tc.e, got, tc.want)
			}
		})
	}
}

func TestEntityTypesAreTheOnesTelegramDefines(t *testing.T) {
	// The type strings are the wire contract: the worker writes them and the
	// public API reports them, so a typo here is a silently dropped format.
	want := map[string]string{
		"mention":      tgapi.EntityMention,
		"hashtag":      tgapi.EntityHashtag,
		"bot_command":  tgapi.EntityBotCommand,
		"url":          tgapi.EntityURL,
		"email":        tgapi.EntityEmail,
		"bold":         tgapi.EntityBold,
		"italic":       tgapi.EntityItalic,
		"code":         tgapi.EntityCode,
		"pre":          tgapi.EntityPre,
		"text_url":     tgapi.EntityTextURL,
		"mention_name": tgapi.EntityMentionName,
		"cashtag":      tgapi.EntityCashtag,
		"phone":        tgapi.EntityPhone,
		"spoiler":      tgapi.EntitySpoiler,
		"blockquote":   tgapi.EntityBlockquote,
		"custom_emoji": tgapi.EntityCustomEmoji,
	}
	for name, value := range want {
		if value != name {
			t.Fatalf("entity constant %q = %q", name, value)
		}
	}
	if tgapi.EntityUnknown != "unknown" {
		t.Fatalf("EntityUnknown = %q", tgapi.EntityUnknown)
	}
}
