package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func newTestPrompter(input string) (*terminalPrompter, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &terminalPrompter{in: strings.NewReader(input), out: out}, out
}

func TestPromptPhoneReadsLine(t *testing.T) {
	p, out := newTestPrompter("  +989121234567 \n")
	got, err := p.Phone(context.Background(), "official")
	if err != nil {
		t.Fatalf("Phone: %v", err)
	}
	if got != "+989121234567" {
		t.Errorf("phone = %q", got)
	}
	if !strings.Contains(out.String(), "official") {
		t.Errorf("prompt should name the profile:\n%s", out.String())
	}
}

func TestPromptCodeHintsDelivery(t *testing.T) {
	p, out := newTestPrompter("12345\n")
	sent := &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeSMS{Length: 5}}
	got, err := p.Code(context.Background(), sent)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if got != "12345" {
		t.Errorf("code = %q", got)
	}
	if !strings.Contains(out.String(), "SMS") || !strings.Contains(out.String(), "5 digits") {
		t.Errorf("prompt should describe the delivery method:\n%s", out.String())
	}
}

func TestPromptCodeDoesNotEchoTheCodeItself(t *testing.T) {
	p, out := newTestPrompter("987654\n")
	_, _ = p.Code(context.Background(), &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{Length: 6}})
	if strings.Contains(out.String(), "987654") {
		t.Errorf("the prompt must not contain the code:\n%s", out.String())
	}
}

func TestPromptEOFIsAnErrorNotAHang(t *testing.T) {
	p, _ := newTestPrompter("")
	if _, err := p.Phone(context.Background(), "custom"); err == nil {
		t.Fatal("closed stdin must produce an error")
	}
}

func TestPromptPasswordFromPipeIsRead(t *testing.T) {
	p, out := newTestPrompter("hunter2\n")
	got, err := p.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("password = %q", got)
	}
	if strings.Contains(out.String(), "hunter2") {
		t.Errorf("the password must never be echoed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "hidden") {
		t.Errorf("the prompt should say input handling:\n%s", out.String())
	}
}

func TestSentCodeHintVariants(t *testing.T) {
	tests := []struct {
		typ  tg.AuthSentCodeTypeClass
		want string
	}{
		{&tg.AuthSentCodeTypeSMS{Length: 5}, "SMS"},
		{&tg.AuthSentCodeTypeApp{Length: 6}, "Telegram app"},
		{&tg.AuthSentCodeTypeCall{Length: 5}, "call"},
		{&tg.AuthSentCodeTypeFlashCall{}, "flash call"},
		{&tg.AuthSentCodeTypeEmailCode{EmailPattern: "a***@b.c"}, "a***@b.c"},
		{&tg.AuthSentCodeTypeSMSWord{Beginning: "AB"}, "AB"},
		{&tg.AuthSentCodeTypeSMSPhrase{Beginning: "CD"}, "CD"},
	}
	for _, tt := range tests {
		if got := sentCodeHint(&tg.AuthSentCode{Type: tt.typ}); !strings.Contains(got, tt.want) {
			t.Errorf("hint for %T = %q, want it to mention %q", tt.typ, got, tt.want)
		}
	}
}
