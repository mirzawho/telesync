package wordpress

import (
	"net/netip"
	"testing"
)

func TestBlockedIPRefusesEveryNonPublicDestination(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.8.9.10", "::1",
		"10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.0.1",
		"169.254.169.254", "169.254.0.1", "fe80::1",
		"100.64.0.1", "0.0.0.0", "::",
		"224.0.0.1", "239.255.255.250", "ff02::1",
		"192.0.2.1", "198.51.100.7", "203.0.113.9", "240.0.0.1",
		"198.18.0.1", "255.255.255.255",
		"fd00:ec2::254",
		"64:ff9b::a00:1",
	}
	for _, raw := range blocked {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", raw, err)
		}
		if why := blockedIP(addr); why == "" {
			t.Errorf("blockedIP(%s) = allowed", raw)
		}
	}
}

func TestBlockedIPAllowsPublicDestinations(t *testing.T) {
	for _, raw := range []string{"93.184.215.14", "1.1.1.1", "2606:4700:4700::1111"} {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatal(err)
		}
		if why := blockedIP(addr); why != "" {
			t.Errorf("blockedIP(%s) = %q", raw, why)
		}
	}
	if why := blockedIP(netip.Addr{}); why != "not an address" {
		t.Errorf("blockedIP(zero) = %q", why)
	}
}

func TestBlockedIPUnmapsIPv4InIPv6(t *testing.T) {
	addr := netip.MustParseAddr("::ffff:127.0.0.1")
	if why := blockedIP(addr); why == "" {
		t.Fatal("an IPv4-mapped loopback address was allowed")
	}
}
