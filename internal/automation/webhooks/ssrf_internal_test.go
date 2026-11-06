package webhooks

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

// fakeDial records the addresses a dialler was asked to reach and never opens a
// connection. Reaching the network would defeat the point of these tests.
type fakeDial struct {
	called []string
	err    error
}

func (f *fakeDial) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	f.called = append(f.called, addr)
	if f.err != nil {
		return nil, f.err
	}
	return nil, errors.New("fake dial: no connection is ever opened")
}

// fakeLookup answers exactly once per call and counts its calls, so a test can
// prove the name is resolved a single time and never again during connect.
type fakeLookup struct {
	addrs []netip.Addr
	err   error
	calls int
}

func (f *fakeLookup) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.addrs, nil
}

func TestBlockedIPNamesEveryRangeWeRefuse(t *testing.T) {
	cases := []string{
		// loopback
		"127.0.0.1", "::1",
		// this network and private ranges
		"10.0.0.1", "172.16.0.1", "192.168.1.1", "10.255.255.255",
		// link-local, where cloud metadata services live
		"169.254.169.254", "fe80::1",
		// IPv4-mapped IPv6 reaches the v4 address it names
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		// unique local and site local
		"fc00::1", "fd12:3456::1", "fec0::1",
		// unspecified, multicast, carrier NAT
		"0.0.0.0", "::", "224.0.0.1", "100.64.0.1",
		// documentation and reserved ranges
		"192.0.2.1", "198.51.100.1", "203.0.113.1", "240.0.0.1", "2001:db8::1",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				t.Fatal(err)
			}
			if why := blockedIP(addr); why == "" {
				t.Fatalf("blockedIP(%s) = \"\", want a reason", raw)
			}
		})
	}

	for _, raw := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"} {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatal(err)
		}
		if why := blockedIP(addr); why != "" {
			t.Fatalf("blockedIP(%s) = %q, want it allowed", raw, why)
		}
	}
}

func TestResolvingDialRefusesAHostThatResolvesSomewherePrivate(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.5", "169.254.169.254", "::1", "fd00::1"} {
		t.Run(raw, func(t *testing.T) {
			d := &fakeDial{}
			lookup := &fakeLookup{addrs: []netip.Addr{netip.MustParseAddr(raw)}}
			fn := resolvingDial(d.dial, lookup.lookup, Policy{})

			_, err := fn(context.Background(), "tcp", "receiver.example.com:443")
			if !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("err = %v, want ErrBlockedAddress", err)
			}
			if len(d.called) != 0 {
				t.Fatalf("a refused address was still dialled: %v", d.called)
			}
		})
	}
}

func TestResolvingDialRefusesAHostWithOnePrivateAnswerAmongPublicOnes(t *testing.T) {
	d := &fakeDial{}
	lookup := &fakeLookup{addrs: []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("127.0.0.1"),
	}}
	fn := resolvingDial(d.dial, lookup.lookup, Policy{})

	if _, err := fn(context.Background(), "tcp", "receiver.example.com:443"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress", err)
	}
	if len(d.called) != 0 {
		t.Fatalf("a host with a private answer was dialled: %v", d.called)
	}
}

func TestResolvingDialDialsTheAddressItCheckedAndNeverResolvesTwice(t *testing.T) {
	// This is the rebinding defence: the name is resolved once, every answer is
	// checked, and the connection goes to the exact address that was checked. A
	// second resolution — the one a rebinding attacker controls — never happens.
	d := &fakeDial{}
	public := netip.MustParseAddr("93.184.216.34")
	lookup := &fakeLookup{addrs: []netip.Addr{public}}
	fn := resolvingDial(d.dial, lookup.lookup, Policy{})

	_, _ = fn(context.Background(), "tcp", "receiver.example.com:443")

	if lookup.calls != 1 {
		t.Fatalf("resolutions = %d, want exactly 1", lookup.calls)
	}
	if len(d.called) != 1 || d.called[0] != "93.184.216.34:443" {
		t.Fatalf("dialled %v, want the checked address with the original port", d.called)
	}
}

func TestResolvingDialReportsAnUnresolvableHost(t *testing.T) {
	d := &fakeDial{}
	lookup := &fakeLookup{err: errors.New("no such host")}
	fn := resolvingDial(d.dial, lookup.lookup, Policy{})

	_, err := fn(context.Background(), "tcp", "receiver.example.com:443")
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}

	lookup = &fakeLookup{}
	fn = resolvingDial(d.dial, lookup.lookup, Policy{})
	if _, err := fn(context.Background(), "tcp", "receiver.example.com:443"); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("empty answer err = %v, want ErrUnreachable", err)
	}
}

func TestResolvingDialDialsEveryCheckedAddressUntilOneAnswers(t *testing.T) {
	d := &fakeDial{err: errors.New("refused")}
	first, second := netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("93.184.216.35")
	lookup := &fakeLookup{addrs: []netip.Addr{first, second}}
	fn := resolvingDial(d.dial, lookup.lookup, Policy{})

	if _, err := fn(context.Background(), "tcp", "receiver.example.com:443"); err == nil {
		t.Fatal("a dial that always fails reported success")
	}
	if len(d.called) != 2 {
		t.Fatalf("dialled %v, want both checked addresses", d.called)
	}
}
