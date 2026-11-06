package webhooks_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/automation/webhooks"
)

func TestValidateTargetAcceptsAPublicHTTPSAddress(t *testing.T) {
	for _, raw := range []string{
		"https://receiver.example.com/hook",
		"https://receiver.example.com:8443/hooks/telegram?team=7",
		"https://hooks.example.co.uk/a/b/c",
	} {
		got, err := webhooks.ValidateTarget(raw, webhooks.Policy{})
		if err != nil {
			t.Fatalf("ValidateTarget(%q): %v", raw, err)
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("ValidateTarget(%q) returned an empty address", raw)
		}
	}
}

func TestValidateTargetRefusesAnythingThatIsNotAPublicHTTPSAddress(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"no scheme", "receiver.example.com/hook"},
		{"ftp", "ftp://receiver.example.com/hook"},
		{"file", "file:///etc/passwd"},
		{"gopher", "gopher://receiver.example.com:70/1"},
		{"javascript", "javascript:alert(1)"},
		{"plain http", "http://receiver.example.com/hook"},
		{"credentials", "https://user:pass@receiver.example.com/hook"},
		{"fragment", "https://receiver.example.com/hook#part"},
		{"loopback name", "https://localhost/hook"},
		{"loopback subdomain", "https://api.localhost/hook"},
		{"internal zone", "https://receiver.internal/hook"},
		{"local zone", "https://receiver.local/hook"},
		{"metadata name", "https://metadata/hook"},
		{"ipv4 literal", "https://93.184.216.34/hook"},
		{"ipv4 loopback literal", "https://127.0.0.1/hook"},
		{"cloud metadata literal", "https://169.254.169.254/latest/meta-data/"},
		{"ipv6 loopback literal", "https://[::1]/hook"},
		{"ipv6 unique local literal", "https://[fd00::1]/hook"},
		{"ipv4-mapped literal", "https://[::ffff:127.0.0.1]/hook"},
		{"single label host", "https://intranet/hook"},
		{"trailing dot", "https://receiver.example.com./hook"},
		{"parent traversal", "https://receiver.example.com/a/../b"},
		{"no host", "https:///hook"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := webhooks.ValidateTarget(tc.url, webhooks.Policy{}); err == nil {
				t.Fatalf("ValidateTarget(%q) accepted an address it must refuse", tc.url)
			}
		})
	}
}

func TestValidateTargetAllowsPlainHTTPOnlyInTheTestPolicy(t *testing.T) {
	if _, err := webhooks.ValidateTarget("http://127.0.0.1:8080/hook", webhooks.Policy{}); err == nil {
		t.Fatal("the production policy accepted plain http")
	}
	if _, err := webhooks.ValidateTarget("http://127.0.0.1:8080/hook", webhooks.TestPolicy()); err != nil {
		t.Fatalf("the test policy refused a loopback http target: %v", err)
	}
	// The test policy loosens the address rules; it never loosens the scheme
	// rules beyond plain http.
	if _, err := webhooks.ValidateTarget("file:///etc/passwd", webhooks.TestPolicy()); err == nil {
		t.Fatal("the test policy accepted a non-http scheme")
	}
}

// redirectTransport answers one request with a redirect, which is exactly what a
// receiver used as a probe would send.
type redirectTransport struct {
	target  string
	called  int
	lastURL string
}

func (r *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.called++
	r.lastURL = req.URL.String()
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{r.target}},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func TestTheRestrictedClientDoesNotFollowRedirects(t *testing.T) {
	// A redirect is where a well-configured target turns into a probe of this
	// network. The client refuses to follow one at all rather than following and
	// re-checking: a 3xx is reported as the receiver's own answer.
	rt := &redirectTransport{target: "http://169.254.169.254/latest/meta-data/"}
	client := webhooks.NewClient(webhooks.WithTransport(rt))

	req, err := http.NewRequest(http.MethodPost, "https://receiver.example.com/hook", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want the redirect itself", resp.StatusCode)
	}
	if rt.called != 1 {
		t.Fatalf("requests = %d, want 1: the redirect was followed", rt.called)
	}
	if rt.lastURL != "https://receiver.example.com/hook" {
		t.Fatalf("the client moved to %q", rt.lastURL)
	}
}

func TestRefusedAddressesAreSentinelsCallersCanMatch(t *testing.T) {
	if !errors.Is(webhooks.ErrBlockedAddress, webhooks.ErrBlockedAddress) {
		t.Fatal("ErrBlockedAddress is not matchable")
	}
	if webhooks.ErrBlockedAddress.Error() == "" || webhooks.ErrInvalidTarget.Error() == "" {
		t.Fatal("an error has no message")
	}
}
