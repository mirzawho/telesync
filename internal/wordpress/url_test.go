package wordpress_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/wordpress"
)

func TestNormalizeBaseURLAcceptsHTTPS(t *testing.T) {
	cases := map[string]string{
		"https://example.com":                     "https://example.com",
		"https://example.com/":                    "https://example.com",
		"https://example.com/blog":                "https://example.com/blog",
		"https://example.com/blog/":               "https://example.com/blog",
		"  https://Example.COM/Blog/  ":           "https://example.com/Blog",
		"HTTPS://example.com":                     "https://example.com",
		"https://www.example.com":                 "https://www.example.com",
		"https://xn--bcher-kva.example":           "https://xn--bcher-kva.example",
		"https://example.com/wordpress/index.php": "https://example.com/wordpress/index.php",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := wordpress.NormalizeBaseURL(in)
			if err != nil {
				t.Fatalf("NormalizeBaseURL(%q): %v", in, err)
			}
			if got != want {
				t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestNormalizeBaseURLRefusesWhatMustNotBeFetched(t *testing.T) {
	cases := map[string]string{
		"http scheme":       "http://example.com",
		"no scheme":         "example.com",
		"ftp scheme":        "ftp://example.com",
		"gopher scheme":     "gopher://example.com",
		"file scheme":       "file:///etc/passwd",
		"scheme-relative":   "//example.com",
		"user and password": "https://admin:secret@example.com",
		"user only":         "https://admin@example.com",
		"fragment":          "https://example.com/#about",
		"query":             "https://example.com/wp?x=1",
		"empty":             "",
		"spaces":            "   ",
		"host only":         "https://",
		"single label":      "https://localhost",
		"dotless":           "https://intranet",
		"ip address":        "https://203.0.113.10",
		"ipv6":              "https://[2001:db8::1]",
		"trailing dot host": "https://example.com./",
		"underscore host":   "https://ex_ample.com",
		"leading dot":       "https://.example.com",
		"label too long":    "https://" + strings.Repeat("a", 64) + ".com",
		"double dot":        "https://example..com",
		"nul in host":       "https://exam\x00ple.com",
		"port":              "https://example.com:8443/wp",
		"default port":      "https://example.com:443/",
		"dot-dot path":      "https://example.com/a/../b",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := wordpress.NormalizeBaseURL(in)
			if !errors.Is(err, wordpress.ErrInvalidURL) {
				t.Fatalf("NormalizeBaseURL(%q) = %q, %v; want ErrInvalidURL", in, got, err)
			}
		})
	}
}

func TestNormalizeBaseURLRefusesInternalHostnames(t *testing.T) {
	// A DNS name that only resolves inside a network is the same reach as the
	// address it resolves to, so the obvious ones are refused from the start.
	for _, host := range []string{
		"localhost", "localhost.localdomain", "thing.local", "x.internal",
		"metadata.google.internal", "svc.cluster.local", "x.home.arpa",
	} {
		if _, err := wordpress.NormalizeBaseURL("https://" + host); !errors.Is(err, wordpress.ErrInvalidURL) {
			t.Fatalf("%s: err = %v, want ErrInvalidURL", host, err)
		}
	}
}

func TestNormalizeBaseURLRejectsOversizedComponents(t *testing.T) {
	long := make([]byte, wordpress.MaxHostLen+1)
	for i := range long {
		long[i] = 'a'
	}
	long[20] = '.'
	long[40] = '.'
	if _, err := wordpress.NormalizeBaseURL("https://" + string(long)); !errors.Is(err, wordpress.ErrInvalidURL) {
		t.Fatalf("oversized host: err = %v", err)
	}
	path := make([]byte, wordpress.MaxPathLen+1)
	for i := range path {
		path[i] = 'a'
	}
	if _, err := wordpress.NormalizeBaseURL("https://example.com/" + string(path)); !errors.Is(err, wordpress.ErrInvalidURL) {
		t.Fatalf("oversized path: err = %v", err)
	}
}

func TestTestPolicyAllowsLoopbackForTheTestServer(t *testing.T) {
	// The only caller of AllowPrivate is a test, against httptest's server. It
	// changes nothing else: the scheme, host shape and path rules still apply.
	p := wordpress.TestPolicy()
	got, err := wordpress.NormalizeBaseURL("http://127.0.0.1:8080", p)
	if err != nil {
		t.Fatalf("loopback under the test policy: %v", err)
	}
	if got != "http://127.0.0.1:8080" {
		t.Fatalf("normalized = %q", got)
	}
	if _, err := wordpress.NormalizeBaseURL("https://example.com", p); err != nil {
		t.Fatalf("public host under the test policy: %v", err)
	}
	if _, err := wordpress.NormalizeBaseURL("https://evil.example.com/a/../b", p); !errors.Is(err, wordpress.ErrInvalidURL) {
		t.Fatalf("dot-dot path under the test policy: err = %v", err)
	}
}
