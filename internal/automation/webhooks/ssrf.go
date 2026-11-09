package webhooks

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
)

// This file is the outbound request discipline for webhook delivery. It mirrors
// internal/wordpress's, which is the repository's existing answer to "an
// operator configures a URL and the daemon connects to it": resolve once, check
// every answer, connect to the address that was checked, never follow a
// redirect, never use a proxy, and never speak anything but TLS. It is written
// again here rather than imported because a webhook is not a WordPress site —
// it has an arbitrary path, an arbitrary method and arbitrary headers — and
// because the WordPress client's site-URL rules (no port, no path traversal
// beyond an install prefix) are not this problem's rules.

// Bounds on an operator-supplied webhook target.
const (
	// MaxTargetLen bounds a whole target URL.
	MaxTargetLen = 2048
	// MaxHostLen bounds the host, the DNS limit.
	MaxHostLen = 253
	// MaxPathLen bounds the path.
	MaxPathLen = 1024
)

// Defaults for one webhook request.
const (
	// DefaultTimeout bounds one request, response body included.
	DefaultTimeout = 15 * time.Second
	// DefaultMaxResponseBytes bounds how much of a response is read.
	DefaultMaxResponseBytes = 64 << 10
	dialTimeout             = 5 * time.Second
)

// Errors a caller matches with errors.Is.
var (
	// ErrInvalidTarget means the configured address is not one this daemon will
	// ever connect to.
	ErrInvalidTarget = errors.New("webhooks: invalid webhook target")
	// ErrBlockedAddress means the target resolved to an address inside this
	// network or otherwise not public.
	ErrBlockedAddress = errors.New("webhooks: refused to connect to a non-public address")
	// ErrUnreachable means the target could not be resolved at all.
	ErrUnreachable = errors.New("webhooks: the receiver is unreachable")
)

// Policy is what a webhook target and its connections are allowed to be.
//
// There is one production policy (the zero value) and one test policy, which
// exists so this package's own tests can reach a scripted handler or a loopback
// server. Nothing in the daemon can select the test policy.
type Policy struct {
	// allowHTTP and allowPrivate are only set by TestPolicy.
	allowHTTP    bool
	allowPrivate bool
	// allowLocalhost permits the loopback exception: the three loopback
	// targets isLoopbackHost names, and plain http to them. It is not a
	// private-network allowance and never becomes one.
	allowLocalhost bool
}

// TestPolicy permits plain http and loopback/private addresses.
//
// It is exported for tests in other packages. It is never read from
// configuration and no constructor in the daemon offers it.
func TestPolicy() Policy { return Policy{allowHTTP: true, allowPrivate: true} }

// AllowLocalhost permits the loopback exception and nothing wider.
//
// It is what TELESYNC_WEBHOOK_ALLOW_LOCALHOST turns on. A webhook reaches it
// only if it asked for it as well, which ForWebhook is the answer to: the
// deployment's permission and the webhook's own opt-in are both required.
func AllowLocalhost() Policy { return Policy{allowLocalhost: true} }

// ForWebhook narrows the policy to one webhook's own opt-in.
func (p Policy) ForWebhook(allowLocalhost bool) Policy {
	p.allowLocalhost = p.allowLocalhost && allowLocalhost
	return p
}

// isLoopbackHost reports whether host is exactly one of the three loopback
// targets the exception covers.
//
// It is a list and not a range on purpose. A name that merely contains
// "localhost", a dotted or decimal or v4-mapped spelling of a loopback
// address, and every other member of 127.0.0.0/8 are refused: widening this to
// "anything loopback" is how an exception for one local service turns into an
// exception for everything behind the same boundary.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// dialFunc is net.Dialer's signature, so a test can supply its own.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// lookupFunc resolves a hostname.
type lookupFunc func(ctx context.Context, host string) ([]netip.Addr, error)

// nonPublicPrefixes are the ranges no webhook may reach: carrier NAT,
// link-local (where cloud metadata services live), the special-purpose,
// documentation, benchmarking and reserved ranges, and their IPv6
// counterparts.
var nonPublicPrefixes = []struct {
	prefix netip.Prefix
	why    string
}{
	{netip.MustParsePrefix("0.0.0.0/8"), "an unspecified address"},
	{netip.MustParsePrefix("100.64.0.0/10"), "a carrier-grade NAT address"},
	{netip.MustParsePrefix("169.254.0.0/16"), "a link-local address (cloud metadata)"},
	{netip.MustParsePrefix("192.0.0.0/24"), "a special-purpose address"},
	{netip.MustParsePrefix("192.0.2.0/24"), "a documentation address"},
	{netip.MustParsePrefix("192.88.99.0/24"), "a reserved address"},
	{netip.MustParsePrefix("198.18.0.0/15"), "a benchmarking address"},
	{netip.MustParsePrefix("198.51.100.0/24"), "a documentation address"},
	{netip.MustParsePrefix("203.0.113.0/24"), "a documentation address"},
	{netip.MustParsePrefix("240.0.0.0/4"), "a reserved address"},
	{netip.MustParsePrefix("2001:db8::/32"), "a documentation address"},
	{netip.MustParsePrefix("2002::/16"), "a 6to4 address"},
	{netip.MustParsePrefix("64:ff9b::/96"), "a NAT64 address"},
	{netip.MustParsePrefix("fc00::/7"), "a unique local address"},
	{netip.MustParsePrefix("fec0::/10"), "a site-local address"},
}

// blockedIP returns why addr may not be reached, or "" when it is public.
//
// The standard library's own classifications cover most of it (loopback,
// private, link-local, multicast, unspecified); the table adds the ranges it
// treats as ordinary public addresses. A v4-mapped v6 address is unmapped
// first, because "::ffff:127.0.0.1" reaches 127.0.0.1 and nothing else.
func blockedIP(addr netip.Addr) string {
	if !addr.IsValid() {
		return "not an address"
	}
	addr = addr.Unmap()
	switch {
	case addr.IsLoopback():
		return "a loopback address"
	case addr.IsPrivate():
		return "a private address"
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		return "a link-local address"
	case addr.IsInterfaceLocalMulticast(), addr.IsMulticast():
		return "a multicast address"
	case addr.IsUnspecified():
		return "an unspecified address"
	}
	for _, p := range nonPublicPrefixes {
		if p.prefix.Contains(addr) {
			return p.why
		}
	}
	return ""
}

// systemLookup resolves through the system resolver.
func systemLookup(resolver *net.Resolver) lookupFunc {
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		return resolver.LookupNetIP(ctx, "ip", host)
	}
}

// safeDial resolves host once, refuses it unless every answer is a public
// address, and connects to exactly one of the addresses it checked.
func safeDial(tcp dialFunc, resolver *net.Resolver, policy Policy) dialFunc {
	return resolvingDial(tcp, systemLookup(resolver), policy)
}

// resolvingDial is the resolve-once, dial-what-you-checked dialler.
//
// Checking the answer and connecting to that same address is what closes the
// gap between "the name looked public" and "the connection went somewhere
// else": a second resolution could return a private address — that is exactly
// what a DNS rebinding attack does — so there is no second resolution. The
// connection is made to the literal address that was checked, and the TLS
// handshake still verifies the hostname the operator configured, because the
// transport keeps the request's own host as the server name.
func resolvingDial(tcp dialFunc, lookup lookupFunc, policy Policy) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if policy.allowPrivate {
			return tcp(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBlockedAddress, err)
		}
		addrs, err := lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot resolve %s", ErrUnreachable, host)
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("%w: %s has no address", ErrUnreachable, host)
		}
		// A loopback target resolves to loopback and to nothing else. A name
		// that comes back with any other answer is not the host it says it is —
		// that is what a DNS rebinding attack does — so it is refused rather
		// than followed. This is the whole width of the exception: nothing
		// outside loopback becomes reachable because of it.
		loopbackTarget := policy.allowLocalhost && isLoopbackHost(host)
		for _, a := range addrs {
			if loopbackTarget {
				if !a.Unmap().IsLoopback() {
					return nil, fmt.Errorf("%w: %s resolved to a non-loopback address", ErrBlockedAddress, host)
				}
				continue
			}
			if why := blockedIP(a); why != "" {
				return nil, fmt.Errorf("%w: %s is %s", ErrBlockedAddress, host, why)
			}
		}
		var lastErr error
		for _, a := range addrs {
			conn, err := tcp(ctx, network, net.JoinHostPort(a.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// scatterPrefixes are hosts that only ever mean an address inside a network.
var scatterPrefixes = []string{
	"localhost", "metadata", "instance-data", "my-ip",
}

// scatterSuffixes are internal DNS zones. A name inside one resolves inside the
// network that runs it, whatever its labels say.
var scatterSuffixes = []string{
	"localhost", "local", "localdomain", "internal", "intranet", "lan", "home",
	"home.arpa", "arpa", "local.arpa", "local.internal", "cluster.local",
	"svc", "corp", "private",
}

// hostnameLabel is one DNS label: letters, digits and hyphens, not at the ends.
var hostnameLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateTarget checks one configured webhook address and returns it trimmed.
//
// It is what a configuration change is validated against. It is not the only
// check: delivery validates again and the dialler refuses a non-public answer
// on every attempt, so a target that was safe when it was written and is unsafe
// now is still refused.
func ValidateTarget(raw string, policy Policy) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", refuseTarget("the webhook url is empty")
	}
	if len(raw) > MaxTargetLen {
		return "", refuseTarget("the webhook url is too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", refuseTarget("the webhook url is malformed")
	}
	if u.Opaque != "" {
		return "", refuseTarget("the webhook url must be an absolute http(s) address")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", refuseTarget("the webhook url has no host")
	}
	loopback := isLoopbackHost(host)

	// The loopback exception is a policy decision and is reported as one: the
	// URL is not malformed, it is not permitted. Saying "must use https" here
	// would send an operator looking in the wrong place.
	if loopback && !policy.allowLocalhost && !policy.allowPrivate {
		return "", refuseTarget("the webhook url points at localhost and the loopback exception is not enabled for it: it needs allow_localhost on this webhook and " + appconfig.AllowLocalhostEnv + " on the deployment")
	}

	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		// Plain http is only reachable where it cannot be a mistake: the test
		// policy, or one of the three loopback targets both the deployment and
		// this webhook opted into. Every other address stays https-only.
		if !policy.allowHTTP && !(policy.allowLocalhost && loopback) {
			return "", refuseTarget("the webhook url must use https")
		}
	default:
		return "", refuseTarget("the webhook url must use https")
	}
	if u.User != nil {
		return "", refuseTarget("the webhook url must not carry a username or password")
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return "", refuseTarget("the webhook url must not have a fragment")
	}

	if err := validHost(host, policy); err != nil {
		return "", err
	}
	if port := u.Port(); port != "" {
		n, err := net.LookupPort("tcp", port)
		if err != nil || n <= 0 || n > 65535 {
			return "", refuseTarget("the webhook url's port is not valid")
		}
	}

	path := u.EscapedPath()
	if len(path) > MaxPathLen {
		return "", refuseTarget("the webhook url's path is too long")
	}
	for _, seg := range strings.Split(path, "/") {
		if strings.ToLower(seg) == ".." {
			return "", refuseTarget("the webhook url's path must not contain \"..\"")
		}
	}
	if decoded, err := url.PathUnescape(path); err != nil || strings.ContainsAny(decoded, "\\\x00") {
		return "", refuseTarget("the webhook url's path is not valid")
	}
	return strings.TrimSpace(raw), nil
}

func refuseTarget(why string) error { return fmt.Errorf("%w: %s", ErrInvalidTarget, why) }

// ipLiteral matches what a host that is really an address looks like.
var ipLiteral = regexp.MustCompile(`^[0-9.]+$`)

// validHost refuses an address literal and any hostname that names an internal
// zone. A public name is resolved only at dial time, where every answer is
// checked again.
func validHost(host string, policy Policy) error {
	// The loopback exception is answered on its own terms, before any of the
	// public-name rules below: "localhost" is not a public domain name and
	// never will be, and 127.0.0.1 is not a hostname.
	if policy.allowLocalhost && isLoopbackHost(host) {
		return nil
	}
	if ipLiteral.MatchString(host) || strings.Contains(host, ":") || strings.ContainsAny(host, "[]") {
		if !policy.allowPrivate {
			return refuseTarget("the webhook url must name a hostname, not an IP address")
		}
		return nil
	}
	if len(host) > MaxHostLen {
		return refuseTarget("the webhook url's host is too long")
	}
	if strings.HasSuffix(host, ".") {
		return refuseTarget("the webhook url's host must not end with a dot")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return refuseTarget("the webhook url's host must be a public domain name")
	}
	for _, label := range labels {
		if !hostnameLabel.MatchString(label) {
			return refuseTarget("the webhook url's host is not a valid domain name")
		}
	}
	if policy.allowPrivate {
		return nil
	}
	for _, prefix := range scatterPrefixes {
		if strings.HasPrefix(host, prefix+".") {
			return refuseTarget("the webhook url's host refers to a local or internal address")
		}
	}
	for _, suffix := range scatterSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return refuseTarget("the webhook url's host refers to a local or internal address")
		}
	}
	return nil
}

// ClientOption configures the restricted client.
type ClientOption func(*clientConfig)

type clientConfig struct {
	policy    Policy
	timeout   time.Duration
	tcp       dialFunc
	lookup    lookupFunc
	resolver  *net.Resolver
	transport http.RoundTripper
}

// WithPolicy uses another URL and address policy. Only tests pass one.
func WithPolicy(p Policy) ClientOption { return func(c *clientConfig) { c.policy = p } }

// WithTimeout bounds one request, response body included.
func WithTimeout(d time.Duration) ClientOption { return func(c *clientConfig) { c.timeout = d } }

// WithTransport replaces the HTTP transport. This package's tests use it so the
// request is answered by a scripted handler rather than a server: a test must
// not bind a port or reach the network.
//
// A nil transport is ignored rather than stored: these options are passed
// through from a configuration whose optional fields are nil when unset, and
// taking "unset" for "replace with nothing" would leave the client with no
// transport at all.
func WithTransport(rt http.RoundTripper) ClientOption {
	return func(c *clientConfig) {
		if rt != nil {
			c.transport = rt
		}
	}
}

// WithDial replaces the TCP dialler (tests). A nil dialler is ignored.
func WithDial(d dialFunc) ClientOption {
	return func(c *clientConfig) {
		if d != nil {
			c.tcp = d
		}
	}
}

// WithLookup replaces DNS resolution (tests). A nil lookup is ignored: the
// resolve-once dialler falls back to the system resolver.
func WithLookup(l lookupFunc) ClientOption {
	return func(c *clientConfig) {
		if l != nil {
			c.lookup = l
		}
	}
}

// NewClient builds the one client every webhook request uses.
//
// Without options it is the production one: https only, public addresses only,
// resolve-once with TLS hostname verification, no proxy, no redirects, TLS 1.2
// or better, and a bounded response body.
func NewClient(opts ...ClientOption) *http.Client {
	c := &clientConfig{
		timeout:  DefaultTimeout,
		resolver: net.DefaultResolver,
	}
	c.tcp = (&net.Dialer{Timeout: dialTimeout}).DialContext
	for _, opt := range opts {
		opt(c)
	}

	var dial dialFunc
	if c.lookup != nil {
		dial = resolvingDial(c.tcp, c.lookup, c.policy)
	} else {
		dial = safeDial(c.tcp, c.resolver, c.policy)
	}

	transport := &http.Transport{
		// No proxy, ever: a proxy would be a second place the request could be
		// sent, and it is not one this application chose.
		Proxy:                 nil,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: c.timeout,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}
	rt := http.RoundTripper(transport)
	if c.transport != nil {
		rt = c.transport
	}
	return &http.Client{
		Transport: rt,
		Timeout:   c.timeout,
		// A redirect is not followed and not re-checked. A receiver that answers
		// 3xx is telling us to talk to somewhere else, and somewhere else is
		// exactly what this transport exists to refuse.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
