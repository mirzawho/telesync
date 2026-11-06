package webhooks_test

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation/webhooks"
)

var signingTime = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func TestSigningKeyDecodesWhsecSecretsAndKeepsOthersLiteral(t *testing.T) {
	key, err := webhooks.SigningKey("whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")))
	if err != nil {
		t.Fatalf("SigningKey: %v", err)
	}
	if string(key) != "0123456789abcdef" {
		t.Fatalf("key = %q", key)
	}

	key, err = webhooks.SigningKey("a passphrase an operator chose")
	if err != nil {
		t.Fatalf("SigningKey: %v", err)
	}
	if string(key) != "a passphrase an operator chose" {
		t.Fatalf("key = %q", key)
	}

	if _, err := webhooks.SigningKey("   "); err == nil {
		t.Fatal("an empty secret was accepted")
	}
	if _, err := webhooks.SigningKey("whsec_!!not base64!!"); err == nil {
		t.Fatal("a malformed whsec_ secret was accepted")
	}
}

func TestSignEmitsTheStandardWebhooksHeaders(t *testing.T) {
	body := []byte(`{"specversion":"1.0"}`)
	sig, err := webhooks.Sign("whsec_"+base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")),
		"wbd_1_evt_abc", signingTime, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	h := sig.Headers()
	if got := h.Get(webhooks.IDHeader); got != "wbd_1_evt_abc" {
		t.Fatalf("webhook-id = %q", got)
	}
	if got := h.Get(webhooks.TimestampHeader); got != strconv.FormatInt(signingTime.Unix(), 10) {
		t.Fatalf("webhook-timestamp = %q", got)
	}
	if !strings.HasPrefix(h.Get(webhooks.SignatureHeader), "v1,") {
		t.Fatalf("webhook-signature = %q", h.Get(webhooks.SignatureHeader))
	}
	if len(h) != 3 {
		t.Fatalf("the signature carries headers it does not own: %v", h)
	}
}

func TestVerifyAcceptsTheMatchingSignatureAndRefusesEverythingElse(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	body := []byte(`{"specversion":"1.0","id":"evt_abc"}`)
	sig, err := webhooks.Sign(secret, "wbd_1", signingTime, body)
	if err != nil {
		t.Fatal(err)
	}

	if err := webhooks.Verify(secret, "wbd_1", signingTime, body, sig.Value); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// A receiver that already rotated keys may offer several candidates; one
	// match is enough.
	multi := "v1," + base64.StdEncoding.EncodeToString(make([]byte, 32)) + " " + sig.Value
	if err := webhooks.Verify(secret, "wbd_1", signingTime, body, multi); err != nil {
		t.Fatalf("Verify with a leading wrong candidate: %v", err)
	}

	cases := []struct {
		name string
		body []byte
		id   string
		at   time.Time
		sig  string
	}{
		{"a different body", []byte(`{"specversion":"1.0","id":"evt_abd"}`), "wbd_1", signingTime, sig.Value},
		{"a different delivery id", body, "wbd_2", signingTime, sig.Value},
		{"a different timestamp", body, "wbd_1", signingTime.Add(time.Second), sig.Value},
		{"a tampered signature", body, "wbd_1", signingTime, "v1," + base64.StdEncoding.EncodeToString(make([]byte, 32))},
		{"a malformed signature", body, "wbd_1", signingTime, "not-a-signature"},
		{"an empty signature", body, "wbd_1", signingTime, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := webhooks.Verify(secret, tc.id, tc.at, tc.body, tc.sig); err == nil {
				t.Fatal("Verify accepted a signature that does not match")
			}
		})
	}
}

func TestRetryKeepsTheDeliveryIDAndMovesTheTimestamp(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	body := []byte(`{"specversion":"1.0"}`)
	first, err := webhooks.Sign(secret, "wbd_1", signingTime, body)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := webhooks.Sign(secret, "wbd_1", signingTime.Add(30*time.Second), body)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != first.ID {
		t.Fatalf("webhook-id changed between attempts: %q then %q", first.ID, retry.ID)
	}
	if !retry.Timestamp.After(first.Timestamp) {
		t.Fatalf("the retry reused the original timestamp: %v", retry.Timestamp)
	}
	if retry.Value == first.Value {
		t.Fatal("the retry reused the original signature")
	}
	// Each attempt verifies against its own timestamp and no other.
	if err := webhooks.Verify(secret, "wbd_1", retry.Timestamp, body, retry.Value); err != nil {
		t.Fatalf("retry signature: %v", err)
	}
	if err := webhooks.Verify(secret, "wbd_1", first.Timestamp, body, retry.Value); err == nil {
		t.Fatal("the retry signature verified against the first timestamp")
	}
}

func TestSignRefusesToSignWithoutADeliveryID(t *testing.T) {
	if _, err := webhooks.Sign("secret", "  ", signingTime, []byte("{}")); err == nil {
		t.Fatal("Sign accepted an empty delivery id")
	}
}
