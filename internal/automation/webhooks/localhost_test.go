package webhooks_test

import (
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/automation/webhooks"
)

// The loopback exception. It is a list of three targets — localhost, 127.0.0.1
// and ::1 — reachable only when the deployment permits it and the webhook
// asked for it. Everything else keeps the rules it had: a private address, a
// metadata endpoint, an http receiver in the public internet and a name that
// merely looks local are all still refused.

func refused(t *testing.T, raw string, policy webhooks.Policy) {
	t.Helper()
	if _, err := webhooks.ValidateTarget(raw, policy); err == nil {
		t.Fatalf("ValidateTarget(%q) accepted an address it must refuse", raw)
	}
}

func accepted(t *testing.T, raw string, policy webhooks.Policy) {
	t.Helper()
	got, err := webhooks.ValidateTarget(raw, policy)
	if err != nil {
		t.Fatalf("ValidateTarget(%q) refused: %v", raw, err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatalf("ValidateTarget(%q) returned no target", raw)
	}
}

func TestLocalhostIsRefusedByDefault(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:3000/webhook",
		"http://127.0.0.1:8080/hook",
		"http://[::1]:8080/webhook",
		"https://localhost:3000/webhook",
	} {
		refused(t, raw, webhooks.Policy{})
	}
}

func TestTheGlobalSwitchAloneIsNotEnough(t *testing.T) {
	// The deployment permits loopback; this webhook did not ask for it.
	for _, raw := range []string{
		"http://localhost:3000/webhook",
		"http://127.0.0.1:8080/hook",
		"http://[::1]:8080/webhook",
	} {
		refused(t, raw, webhooks.AllowLocalhost().ForWebhook(false))
	}
}

func TestTheWebhookOptInAloneIsNotEnough(t *testing.T) {
	// The webhook asked for it; the deployment does not permit it. A
	// per-webhook setting can never widen the global one.
	for _, raw := range []string{
		"http://localhost:3000/webhook",
		"http://127.0.0.1:8080/hook",
		"http://[::1]:8080/webhook",
	} {
		refused(t, raw, webhooks.Policy{}.ForWebhook(true))
	}
}

func TestTheThreeLoopbackTargetsAreAcceptedWhenBothSettingsAllowIt(t *testing.T) {
	policy := webhooks.AllowLocalhost().ForWebhook(true)
	for _, raw := range []string{
		"http://localhost:3000/webhook",
		"http://localhost:8080/api/events",
		"http://127.0.0.1:3000/hook",
		"http://[::1]:8080/webhook",
		"https://localhost:8443/hook",
	} {
		accepted(t, raw, policy)
	}
}

func TestPublicWebhooksKeepTheHTTPSRule(t *testing.T) {
	// The exception is for loopback and says nothing about the internet.
	policy := webhooks.AllowLocalhost().ForWebhook(true)
	accepted(t, "https://example.com/hook", policy)
	refused(t, "http://example.com/hook", policy)
}

func TestTheExceptionIsNotAPrivateNetworkAllowance(t *testing.T) {
	policy := webhooks.AllowLocalhost().ForWebhook(true)
	for _, raw := range []string{
		"http://192.168.1.10/hook",
		"http://10.0.0.5/hook",
		"http://172.16.0.9/hook",
		"http://169.254.169.254/hook",
		"http://[fd00::1]:8080/hook",
	} {
		refused(t, raw, policy)
	}
}

func TestANameThatMerelyContainsLocalhostIsNotLocalhost(t *testing.T) {
	policy := webhooks.AllowLocalhost().ForWebhook(true)
	for _, raw := range []string{
		"http://localhost.evil.example/hook",
		"http://evil-localhost.example/hook",
		"http://notlocalhost.example/hook",
		"http://localhost.example.com/hook",
		"http://127.0.0.1.example.com/hook",
	} {
		refused(t, raw, policy)
	}
}

func TestDisguisedLoopbackSpellingsAreNotAccepted(t *testing.T) {
	// Only the three named targets count. A dotted, decimal, shortened or
	// v4-mapped spelling of a loopback address is a different way of writing
	// one, and accepting it would turn a list into a range.
	policy := webhooks.AllowLocalhost().ForWebhook(true)
	for _, raw := range []string{
		"http://127.1:8080/hook",
		"http://127.0.1/hook",
		"http://0x7f000001:8080/hook",
		"http://2130706433:8080/hook",
		"http://[::ffff:127.0.0.1]:8080/hook",
		"http://[::ffff:7f00:1]:8080/hook",
		"http://127.0.0.1./hook",
		"http://localhost./hook",
		"http://127.0.0.2:8080/hook",
	} {
		refused(t, raw, policy)
	}
}

func TestCredentialsAndSchemesAreStillRefused(t *testing.T) {
	policy := webhooks.AllowLocalhost().ForWebhook(true)
	refused(t, "http://user:pass@localhost:3000/hook", policy)
	refused(t, "ftp://localhost:3000/hook", policy)
	refused(t, "file:///etc/passwd", policy)
	// A fragment is not part of a webhook address.
	refused(t, "http://localhost:3000/hook#frag", policy)
}
