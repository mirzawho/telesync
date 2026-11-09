package webhooktest_test

import (
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/webhooktest"
)

// The receiver verifies what it is told to verify against, and it learns that
// from its own variables. Nothing falls back to the main application's
// credentials, and nothing is accepted on the grounds that no secret was
// configured.

func envOf(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func completeEnv() map[string]string {
	return map[string]string{
		webhooktest.EnvHMACSecret:   "test-hmac-secret",
		webhooktest.EnvBearerToken:  "test-bearer-token",
		webhooktest.EnvHeaderName:   "X-API-Key",
		webhooktest.EnvHeaderSecret: "test-header-secret",
	}
}

func TestLoadAuthReadsEveryVariableItVerifiesAgainst(t *testing.T) {
	got, err := webhooktest.LoadAuth(envOf(completeEnv()))
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if got.HMACSecret != "test-hmac-secret" {
		t.Fatalf("HMACSecret = %q", got.HMACSecret)
	}
	if got.BearerToken != "test-bearer-token" {
		t.Fatalf("BearerToken = %q", got.BearerToken)
	}
	if got.HeaderName != "X-API-Key" {
		t.Fatalf("HeaderName = %q", got.HeaderName)
	}
	if got.HeaderSecret != "test-header-secret" {
		t.Fatalf("HeaderSecret = %q", got.HeaderSecret)
	}
}

func TestLoadAuthRefusesToStartWithoutEverySecret(t *testing.T) {
	for _, missing := range []string{
		webhooktest.EnvHMACSecret,
		webhooktest.EnvBearerToken,
		webhooktest.EnvHeaderName,
		webhooktest.EnvHeaderSecret,
	} {
		t.Run(missing, func(t *testing.T) {
			env := completeEnv()
			delete(env, missing)
			_, err := webhooktest.LoadAuth(envOf(env))
			if err == nil {
				t.Fatalf("LoadAuth accepted a configuration with %s unset", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Fatalf("LoadAuth error %q does not name %s", err, missing)
			}
		})
	}
}

func TestLoadAuthReportsEveryMissingVariableAtOnce(t *testing.T) {
	_, err := webhooktest.LoadAuth(envOf(map[string]string{}))
	if err == nil {
		t.Fatal("LoadAuth accepted an empty environment")
	}
	for _, name := range []string{
		webhooktest.EnvHMACSecret,
		webhooktest.EnvBearerToken,
		webhooktest.EnvHeaderName,
		webhooktest.EnvHeaderSecret,
	} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("LoadAuth error %q does not name %s", err, name)
		}
	}
}

func TestLoadAuthTreatsAWhitespaceOnlySecretAsMissing(t *testing.T) {
	env := completeEnv()
	env[webhooktest.EnvBearerToken] = "   \n"
	_, err := webhooktest.LoadAuth(envOf(env))
	if err == nil {
		t.Fatal("LoadAuth accepted a whitespace-only secret")
	}
	if !strings.Contains(err.Error(), webhooktest.EnvBearerToken) {
		t.Fatalf("LoadAuth error %q does not name %s", err, webhooktest.EnvBearerToken)
	}
}

func TestLoadAuthAcceptsAStandardWebhooksSecret(t *testing.T) {
	env := completeEnv()
	env[webhooktest.EnvHMACSecret] = "whsec_dGVzdC1obWFjLXNlY3JldA=="
	got, err := webhooktest.LoadAuth(envOf(env))
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if got.HMACSecret != env[webhooktest.EnvHMACSecret] {
		t.Fatalf("the secret was rewritten to %q", got.HMACSecret)
	}
}

func TestLoadAuthRefusesAnUnusableHMACSecret(t *testing.T) {
	env := completeEnv()
	// A whsec_ secret that is not valid base64 would be stored happily and only
	// fail at the first delivery. It is refused here instead.
	env[webhooktest.EnvHMACSecret] = "whsec_!!not base64!!"
	_, err := webhooktest.LoadAuth(envOf(env))
	if err == nil {
		t.Fatal("LoadAuth accepted an undecodable whsec_ secret")
	}
	if !strings.Contains(err.Error(), webhooktest.EnvHMACSecret) {
		t.Fatalf("LoadAuth error %q does not name %s", err, webhooktest.EnvHMACSecret)
	}
}

func TestLoadAuthRefusesAHeaderNameTheSenderWouldNeverSend(t *testing.T) {
	for _, name := range []string{"", "X API Key", "Authorization", "webhook-signature", "X-Api\n-Key", "X-API-Key:"} {
		t.Run(name, func(t *testing.T) {
			env := completeEnv()
			env[webhooktest.EnvHeaderName] = name
			if _, err := webhooktest.LoadAuth(envOf(env)); err == nil {
				t.Fatalf("LoadAuth accepted the header name %q", name)
			}
		})
	}
}

func TestLoadAuthAcceptsAHeaderNameWithSurroundingWhitespace(t *testing.T) {
	// The sender trims the header name it is given, so a receiver that refused
	// " X-API-Key " would be stricter than the request it is meant to check.
	env := completeEnv()
	env[webhooktest.EnvHeaderName] = "  X-API-Key  "
	got, err := webhooktest.LoadAuth(envOf(env))
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if got.HeaderName != "X-API-Key" {
		t.Fatalf("HeaderName = %q", got.HeaderName)
	}
}
