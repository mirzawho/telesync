package tgapi_test

import (
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/tgapi"
)

func TestParseChannelQueryClassifiesEachSupportedForm(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want tgapi.ChannelQuery
	}{
		{name: "at username", in: "@telegram", want: tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "telegram"}},
		{name: "public link", in: "https://t.me/telegram", want: tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "telegram"}},
		{name: "public link without scheme", in: "t.me/telegram", want: tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "telegram"}},
		{name: "public link with trailing slash", in: "https://t.me/telegram/", want: tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "telegram"}},
		{name: "web preview link", in: "https://t.me/s/telegram", want: tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "telegram"}},
		{name: "invite plus", in: "https://t.me/+AbCdEf123456", want: tgapi.ChannelQuery{Kind: tgapi.QueryInvite, Invite: "AbCdEf123456"}},
		{name: "invite joinchat", in: "https://t.me/joinchat/AbCdEf123456", want: tgapi.ChannelQuery{Kind: tgapi.QueryInvite, Invite: "AbCdEf123456"}},
		{name: "numeric id", in: "1671633563", want: tgapi.ChannelQuery{Kind: tgapi.QueryID, ChannelID: 1671633563}},
		{name: "bot api id", in: "-1001671633563", want: tgapi.ChannelQuery{Kind: tgapi.QueryID, ChannelID: 1671633563}},
		{name: "text", in: "technology news", want: tgapi.ChannelQuery{Kind: tgapi.QueryText, Text: "technology news"}},
		{name: "bare word stays text", in: "telegram", want: tgapi.ChannelQuery{Kind: tgapi.QueryText, Text: "telegram"}},
		{name: "surrounding space", in: "  technology  ", want: tgapi.ChannelQuery{Kind: tgapi.QueryText, Text: "technology"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tgapi.ParseChannelQuery(tc.in)
			if err != nil {
				t.Fatalf("ParseChannelQuery(%q): %v", tc.in, err)
			}
			if got.Kind != tc.want.Kind {
				t.Fatalf("kind = %q, want %q", got.Kind, tc.want.Kind)
			}
			switch tc.want.Kind {
			case tgapi.QueryUsername:
				if got.Username != tc.want.Username {
					t.Fatalf("username = %q, want %q", got.Username, tc.want.Username)
				}
			case tgapi.QueryInvite:
				if got.Invite != tc.want.Invite {
					t.Fatalf("invite = %q, want %q", got.Invite, tc.want.Invite)
				}
			case tgapi.QueryID:
				if got.ChannelID != tc.want.ChannelID {
					t.Fatalf("channel id = %d, want %d", got.ChannelID, tc.want.ChannelID)
				}
			case tgapi.QueryText:
				if got.Text != tc.want.Text {
					t.Fatalf("text = %q, want %q", got.Text, tc.want.Text)
				}
			}
		})
	}
}

func TestParseChannelQueryRejectsUnusableInput(t *testing.T) {
	tests := []struct{ name, in string }{
		{name: "empty", in: ""},
		{name: "spaces only", in: "   "},
		{name: "too long", in: strings.Repeat("a", tgapi.MaxQueryLength+1)},
		{name: "newline", in: "a\nb"},
		{name: "bad username", in: "@ab"},
		{name: "bad invite hash", in: "https://t.me/+abc"},
		{name: "link to nothing", in: "https://t.me/"},
		{name: "zero id", in: "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tgapi.ParseChannelQuery(tc.in); err == nil {
				t.Fatalf("ParseChannelQuery(%q) must fail", tc.in)
			}
		})
	}
}

func TestParseChannelIDAcceptsBothRenderings(t *testing.T) {
	for _, raw := range []string{"1671633563", "-1001671633563"} {
		got, err := tgapi.ParseChannelID(raw)
		if err != nil {
			t.Fatalf("ParseChannelID(%q): %v", raw, err)
		}
		if got != 1671633563 {
			t.Fatalf("ParseChannelID(%q) = %d", raw, got)
		}
	}
	if _, err := tgapi.ParseChannelID("-5"); err != nil {
		t.Fatalf("a small negative id should normalize, got %v", err)
	}
	if _, err := tgapi.ParseChannelID("abc"); err == nil {
		t.Fatal("a non-numeric id must fail")
	}
}

func TestParseChannelRefAcceptsIDsAndInvites(t *testing.T) {
	tests := []struct {
		in     string
		id     int64
		invite string
	}{
		{in: "1671633563", id: 1671633563},
		{in: "-1001671633563", id: 1671633563},
		{in: "+AbCdEf123456", invite: "AbCdEf123456"},
		{in: "joinchat/AbCdEf123456", invite: "AbCdEf123456"},
	}
	for _, tc := range tests {
		got, err := tgapi.ParseChannelRef(tc.in)
		if err != nil {
			t.Fatalf("ParseChannelRef(%q): %v", tc.in, err)
		}
		if got.ID != tc.id || got.Invite != tc.invite {
			t.Fatalf("ParseChannelRef(%q) = %+v", tc.in, got)
		}
		if got.IsInvite() != (tc.invite != "") {
			t.Fatalf("IsInvite(%q) = %v", tc.in, got.IsInvite())
		}
	}
}

func TestParseChannelRefRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "  ", "abc", "+short", "0"} {
		if _, err := tgapi.ParseChannelRef(in); err == nil {
			t.Fatalf("ParseChannelRef(%q) must fail", in)
		}
	}
}
