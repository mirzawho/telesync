package wordpress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// nonPublicPrefixes are the ranges no site request may reach: this network, the
// loopback, carrier NAT, link-local (where cloud metadata services live),
// multicast, the documentation and benchmarking ranges, and their IPv6
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
// treats as ordinary public addresses.
func blockedIP(addr netip.Addr) string {
	if !addr.IsValid() {
		return "not an address"
	}
	// A v4-mapped v6 address reaches the v4 destination it names.
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

// dialFunc is net.Dialer's signature, so a test can supply its own.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// lookupFunc resolves a hostname.
type lookupFunc func(ctx context.Context, host string) ([]netip.Addr, error)

// systemLookup resolves through the system resolver.
func systemLookup(resolver *net.Resolver) lookupFunc {
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		return resolver.LookupNetIP(ctx, "ip", host)
	}
}

// safeDial resolves host once, refuses it unless every answer is a public
// address, and connects to exactly one of the addresses it checked.
//
// Checking the answer and connecting to that same address is what closes the
// gap between "the name looked public" and "the connection went somewhere
// else": a second resolution could return a private address, so there is no
// second resolution.
func safeDial(tcp dialFunc, resolver *net.Resolver, policy Policy) dialFunc {
	return resolvingDial(tcp, systemLookup(resolver), policy)
}

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
		for _, a := range addrs {
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

// isTimeout reports whether err is a deadline, ours or the network's.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Timeout() {
		return true
	}
	return false
}
