package gotdclient_test

import (
	"testing"

	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/tgapi"
)

// Telegram carries formatting as typed spans whose offsets are UTF-16 code
// units. The worker is the only package that sees gotd types, so the
// conversion to and from tgapi.Entity is the one place the mapping lives.

func TestEntitiesReadsEverySpanOfAMessage(t *testing.T) {
	msg := &tg.Message{Message: "hello #news @alice **"}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityHashtag{Offset: 6, Length: 4},
		&tg.MessageEntityMention{Offset: 11, Length: 6},
		&tg.MessageEntityBold{Offset: 0, Length: 5},
	})
	got := gotdclient.Entities(msg)
	want := []tgapi.Entity{
		{Type: tgapi.EntityHashtag, Offset: 6, Length: 4},
		{Type: tgapi.EntityMention, Offset: 11, Length: 6},
		{Type: tgapi.EntityBold, Offset: 0, Length: 5},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entities, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entity %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEntitiesIsNeverNil(t *testing.T) {
	// A message with no formatting must serialize as [] and not null, so every
	// reader of the public message can range over it without a branch.
	got := gotdclient.Entities(&tg.Message{Message: "plain"})
	if got == nil {
		t.Fatal("Entities returned nil")
	}
	if len(got) != 0 {
		t.Fatalf("Entities = %+v, want empty", got)
	}
}

func TestEntitiesCarriesTheTypeSpecificFields(t *testing.T) {
	msg := &tg.Message{}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityTextURL{Offset: 0, Length: 4, URL: "https://example.com"},
		&tg.MessageEntityPre{Offset: 5, Length: 3, Language: "go"},
		&tg.MessageEntityMentionName{Offset: 9, Length: 6, UserID: 4242},
		&tg.MessageEntityCustomEmoji{Offset: 16, Length: 2, DocumentID: 777},
	})
	got := gotdclient.Entities(msg)
	want := []tgapi.Entity{
		{Type: tgapi.EntityTextURL, Offset: 0, Length: 4, URL: "https://example.com"},
		{Type: tgapi.EntityPre, Offset: 5, Length: 3, Language: "go"},
		{Type: tgapi.EntityMentionName, Offset: 9, Length: 6, UserID: 4242},
		{Type: tgapi.EntityCustomEmoji, Offset: 16, Length: 2, DocumentID: 777},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entity %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEntitiesMapsTheRemainingTelegramTypesByName(t *testing.T) {
	cases := []struct {
		name string
		in   tg.MessageEntityClass
		want string
	}{
		{"bot command", &tg.MessageEntityBotCommand{Offset: 0, Length: 4}, tgapi.EntityBotCommand},
		{"url", &tg.MessageEntityURL{Offset: 0, Length: 4}, tgapi.EntityURL},
		{"email", &tg.MessageEntityEmail{Offset: 0, Length: 4}, tgapi.EntityEmail},
		{"italic", &tg.MessageEntityItalic{Offset: 0, Length: 4}, tgapi.EntityItalic},
		{"code", &tg.MessageEntityCode{Offset: 0, Length: 4}, tgapi.EntityCode},
		{"cashtag", &tg.MessageEntityCashtag{Offset: 0, Length: 4}, tgapi.EntityCashtag},
		{"phone", &tg.MessageEntityPhone{Offset: 0, Length: 4}, tgapi.EntityPhone},
		{"underline", &tg.MessageEntityUnderline{Offset: 0, Length: 4}, tgapi.EntityUnderline},
		{"strike", &tg.MessageEntityStrike{Offset: 0, Length: 4}, tgapi.EntityStrike},
		{"bank card", &tg.MessageEntityBankCard{Offset: 0, Length: 4}, tgapi.EntityBankCard},
		{"spoiler", &tg.MessageEntitySpoiler{Offset: 0, Length: 4}, tgapi.EntitySpoiler},
		{"blockquote", &tg.MessageEntityBlockquote{Offset: 0, Length: 4}, tgapi.EntityBlockquote},
		{"unknown", &tg.MessageEntityUnknown{Offset: 0, Length: 4}, tgapi.EntityUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &tg.Message{}
			msg.SetEntities([]tg.MessageEntityClass{tc.in})
			got := gotdclient.Entities(msg)
			if len(got) != 1 || got[0].Type != tc.want {
				t.Fatalf("Entities = %+v, want type %q", got, tc.want)
			}
		})
	}
}

func TestInputEntitiesRebuildsTheSpansForSending(t *testing.T) {
	in := []tgapi.Entity{
		{Type: tgapi.EntityBold, Offset: 0, Length: 5},
		{Type: tgapi.EntityHashtag, Offset: 6, Length: 4},
		{Type: tgapi.EntityTextURL, Offset: 11, Length: 4, URL: "https://example.com"},
		{Type: tgapi.EntityPre, Offset: 16, Length: 3, Language: "go"},
		{Type: tgapi.EntityMentionName, Offset: 20, Length: 6, UserID: 4242},
		{Type: tgapi.EntityCustomEmoji, Offset: 27, Length: 2, DocumentID: 777},
	}
	got, dropped := gotdclient.InputEntities(in)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(got) != len(in) {
		t.Fatalf("got %d entities, want %d", len(got), len(in))
	}
	checks := []func(t *testing.T, e tg.MessageEntityClass){
		func(t *testing.T, e tg.MessageEntityClass) {
			v, ok := e.(*tg.MessageEntityBold)
			if !ok || v.Offset != 0 || v.Length != 5 {
				t.Fatalf("bold = %#v", e)
			}
		},
		func(t *testing.T, e tg.MessageEntityClass) {
			v, ok := e.(*tg.MessageEntityHashtag)
			if !ok || v.Offset != 6 || v.Length != 4 {
				t.Fatalf("hashtag = %#v", e)
			}
		},
		func(t *testing.T, e tg.MessageEntityClass) {
			v, ok := e.(*tg.MessageEntityTextURL)
			if !ok || v.Offset != 11 || v.Length != 4 || v.URL != "https://example.com" {
				t.Fatalf("text_url = %#v", e)
			}
		},
		func(t *testing.T, e tg.MessageEntityClass) {
			v, ok := e.(*tg.MessageEntityPre)
			if !ok || v.Offset != 16 || v.Length != 3 || v.Language != "go" {
				t.Fatalf("pre = %#v", e)
			}
		},
		func(t *testing.T, e tg.MessageEntityClass) {
			v, ok := e.(*tg.MessageEntityMentionName)
			if !ok || v.Offset != 20 || v.Length != 6 || v.UserID != 4242 {
				t.Fatalf("mention_name = %#v", e)
			}
		},
		func(t *testing.T, e tg.MessageEntityClass) {
			v, ok := e.(*tg.MessageEntityCustomEmoji)
			if !ok || v.Offset != 27 || v.Length != 2 || v.DocumentID != 777 {
				t.Fatalf("custom_emoji = %#v", e)
			}
		},
	}
	for i, check := range checks {
		check(t, got[i])
	}
}

func TestInputEntitiesDropsAKindItCannotRebuild(t *testing.T) {
	// A kind with no constructor is reported as unsendable and left out rather
	// than written as a wrong span. The caller learns which ones were dropped.
	in := []tgapi.Entity{
		{Type: tgapi.EntityBold, Offset: 0, Length: 2},
		{Type: tgapi.EntityUnknown, Offset: 3, Length: 2},
	}
	got, dropped := gotdclient.InputEntities(in)
	if len(got) != 1 {
		t.Fatalf("rebuilt %d entities, want 1: %#v", len(got), got)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
}
