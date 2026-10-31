package wordpress

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Bounds on an operator-supplied site URL. They are far above any real
// WordPress installation's origin and keep a hostile value from being stored.
const (
	// MaxHostLen bounds the host, the DNS limit.
	MaxHostLen = 253
	// MaxPathLen bounds the path prefix, for an install under a subdirectory.
	MaxPathLen = 255
)

// Policy is what a site URL and its connections are allowed to be.
//
// There is one production policy (the zero value) and one test policy, which
// exists so the package's own tests can reach an httptest server on loopback.
// Nothing in the daemon can select the test policy.
type Policy struct {
	// allowHTTP and allowPrivate are only set by TestPolicy.
	allowHTTP    bool
	allowPrivate bool
}

// TestPolicy permits plain http and loopback/private addresses.
//
// It is exported for tests in other packages that call Probe against an
// httptest server. It is never read from configuration.
func TestPolicy() Policy { return Policy{allowHTTP: true, allowPrivate: true} }

// production is the policy NormalizeBaseURL and NewClient use when none is
// given.
var production Policy

// NormalizeBaseURL validates and canonicalizes an operator-supplied URL.
//
// It accepts an https origin with an optional path prefix — no credentials, no
// query, no fragment, no port other than the default, no IP address, no
// single-label or internal hostname — and returns it with the scheme and host
// lower-cased, the default port and trailing slashes removed.
func NormalizeBaseURL(raw string, opts ...Policy) (string, error) {
	policy := production
	if len(opts) > 0 {
		policy = opts[0]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", refuse("the site url is empty")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", refuse("the site url is malformed")
	}
	if u.Opaque != "" {
		return "", refuse("the site url must be an absolute https address")
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme == "https":
	case scheme == "http" && policy.allowHTTP:
	default:
		return "", refuse("the site url must use https")
	}
	if u.User != nil {
		return "", refuse("the site url must not carry a username or password; give them as the request's username and password")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", refuse("the site url must not have a query string")
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return "", refuse("the site url must not have a fragment")
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", refuse("the site url has no host")
	}
	port := u.Port()
	if port != "" && !policy.allowHTTP {
		// Ports are not a supported part of a site origin: a public site is
		// reached on 443, and anything else is more likely a mistake or a
		// probe of this network.
		return "", refuse("the site url must not name a port")
	}
	if err := validateHost(host, policy); err != nil {
		return "", err
	}

	path := strings.TrimRight(u.EscapedPath(), "/")
	if len(path) > MaxPathLen {
		return "", refuse("the site url's path is too long")
	}
	for _, seg := range strings.Split(path, "/") {
		switch strings.ToLower(seg) {
		case "", ".":
			continue
		case "..":
			return "", refuse("the site url's path must not contain \"..\"")
		}
	}
	if decoded, err := url.PathUnescape(path); err != nil || strings.ContainsAny(decoded, "\\\x00") {
		return "", refuse("the site url's path is not valid")
	}

	out := scheme + "://" + host
	if port != "" {
		out += ":" + port
	}
	return out + path, nil
}

func refuse(why string) error { return fmt.Errorf("%w: %s", ErrInvalidURL, why) }

// ipLiteral matches what a host that is really an address looks like.
var ipLiteral = regexp.MustCompile(`^[0-9.]+$`)

// hostnameLabel is one DNS label: letters, digits and hyphens, not at the ends.
var hostnameLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// scatterPrefixes are hosts that only ever mean an address inside a network.
var scatterPrefixes = []string{
	"localhost", "metadata", "instance-data", "my-ip",
}

// scatterSuffixes are internal DNS zones. A name inside one resolves inside
// the network that runs it, whatever its labels say.
var scatterSuffixes = []string{
	"localhost", "local", "localdomain", "internal", "intranet", "lan", "home",
	"home.arpa", "arpa", "local.arpa", "local.internal", "cluster.local",
	"svc", "corp", "private",
}

// validateHost refuses an address literal and any hostname that names an
// internal zone. A public name is resolved only at dial time, where every
// answer is checked again.
func validateHost(host string, policy Policy) error {
	if ipLiteral.MatchString(host) || strings.Contains(host, ":") || strings.ContainsAny(host, "[]") {
		if !policy.allowPrivate {
			return refuse("the site url must name a hostname, not an IP address")
		}
		return nil
	}
	if len(host) > MaxHostLen {
		return refuse("the site url's host is too long")
	}
	if strings.HasSuffix(host, ".") {
		return refuse("the site url's host must not end with a dot")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return refuse("the site url's host must be a public domain name")
	}
	for _, label := range labels {
		if !hostnameLabel.MatchString(label) {
			return refuse("the site url's host is not a valid domain name")
		}
	}
	if policy.allowPrivate {
		return nil
	}
	for _, prefix := range scatterPrefixes {
		if strings.HasPrefix(host, prefix+".") {
			return refuse("the site url's host refers to a local or internal address")
		}
	}
	for _, suffix := range scatterSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return refuse("the site url's host refers to a local or internal address")
		}
	}
	return nil
}
