package webhooktest

import (
	"fmt"
	"strings"

	"github.com/mirza/telesync/internal/automation/webhooks"
)

// The variables this command reads. They are its own: nothing here reaches into
// the main application's configuration, so a credential used to test a delivery
// is never one this installation uses for anything real.
const (
	EnvHMACSecret   = "TELESYNC_TEST_WEBHOOK_HMAC_SECRET"
	EnvBearerToken  = "TELESYNC_TEST_WEBHOOK_BEARER_TOKEN"
	EnvHeaderName   = "TELESYNC_TEST_WEBHOOK_HEADER_NAME"
	EnvHeaderSecret = "TELESYNC_TEST_WEBHOOK_HEADER_SECRET"
)

// LoadAuth reads the credentials the receiver verifies against.
//
// Every variable is required. A missing one is a configuration error at startup
// rather than a route that quietly stops checking: an operator testing an
// authentication mode wants to be told they have not configured it, not to
// watch their request fail with a signature error they will spend an hour
// disproving.
//
// The header name is validated with the sender's own rule, so the receiver only
// ever looks for a header the sender would actually send.
func LoadAuth(lookup func(string) string) (Auth, error) {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}

	var out Auth
	var missing []string
	required := func(name string) string {
		value := strings.TrimSpace(lookup(name))
		if value == "" {
			missing = append(missing, name)
		}
		return value
	}

	out.HMACSecret = required(EnvHMACSecret)
	out.BearerToken = required(EnvBearerToken)
	out.HeaderName = required(EnvHeaderName)
	out.HeaderSecret = required(EnvHeaderSecret)

	if len(missing) > 0 {
		return Auth{}, fmt.Errorf(
			"every test credential must be set; missing %s\n"+
				"these are this command's own test values, not the application's credentials",
			strings.Join(missing, ", "))
	}
	// Both checks fail here rather than at the first request: a secret that can
	// never verify, or a header name the sender would refuse to use, is a typo
	// in this command's own configuration.
	if _, err := webhooks.SigningKey(out.HMACSecret); err != nil {
		return Auth{}, fmt.Errorf("%s: %v", EnvHMACSecret, err)
	}
	if err := webhooks.ValidAuthHeaderName(out.HeaderName); err != nil {
		return Auth{}, fmt.Errorf("%s: %v", EnvHeaderName, err)
	}
	return out, nil
}
