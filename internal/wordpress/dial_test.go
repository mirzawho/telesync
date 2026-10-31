// This file is in the package itself because the dial check happens below the
// HTTP surface: a test in wordpress_test could only observe a refusal, not
// which address was connected to.
package wordpress

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

// lookupTable stands in for DNS, so a test can script what a name resolves to.
type lookupTable map[string][]string

func (l lookupTable) resolve(_ context.Context, host string) ([]netip.Addr, error) {
	raw, ok := l[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]netip.Addr, 0, len(raw))
	for _, r := range raw {
		out = append(out, netip.MustParseAddr(r))
	}
	return out, nil
}

// neverDial fails the test's intent if a blocked destination were dialed.
func neverDial(counter *int) dialFunc {
	return func(context.Context, string, string) (net.Conn, error) {
		*counter++
		return nil, errors.New("must not dial")
	}
}

func TestSafeDialRefusesAPrivateAnswer(t *testing.T) {
	dialed := 0
	d := resolvingDial(neverDial(&dialed), lookupTable{"rebind.example.com": {"10.0.0.7"}}.resolve, production)
	_, err := d(context.Background(), "tcp", "rebind.example.com:443")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress", err)
	}
	if dialed != 0 {
		t.Fatalf("dialed %d times", dialed)
	}
}

func TestSafeDialRefusesWhenAnyAnswerIsPrivate(t *testing.T) {
	// A name that answers both a public and a metadata address must not be used
	// at all: which answer a connection happens to pick never decides reach.
	dialed := 0
	d := resolvingDial(neverDial(&dialed), lookupTable{"mixed.example.com": {"93.184.215.14", "169.254.169.254"}}.resolve, production)
	_, err := d(context.Background(), "tcp", "mixed.example.com:443")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress", err)
	}
	if dialed != 0 {
		t.Fatalf("dialed %d times", dialed)
	}
}

func TestSafeDialConnectsToTheAddressesItChecked(t *testing.T) {
	var dialed []string
	dial := func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return nil, errors.New("no server here")
	}
	d := resolvingDial(dial, lookupTable{"public.example.com": {"93.184.215.14", "2606:4700:4700::1111"}}.resolve, production)
	_, _ = d(context.Background(), "tcp", "public.example.com:443")

	if len(dialed) != 2 {
		t.Fatalf("dialed %v", dialed)
	}
	for _, addr := range dialed {
		if addr != "93.184.215.14:443" && addr != "[2606:4700:4700::1111]:443" {
			t.Fatalf("dialed an address the resolver did not return: %s", addr)
		}
	}
}

func TestSafeDialReportsANameThatDoesNotResolve(t *testing.T) {
	dialed := 0
	d := resolvingDial(neverDial(&dialed), lookupTable{}.resolve, production)
	_, err := d(context.Background(), "tcp", "nothing.example.com:443")
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestSafeDialReturnsTheLastConnectionError(t *testing.T) {
	refused := errors.New("connection refused")
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, refused }
	d := resolvingDial(dial, lookupTable{"public.example.com": {"93.184.215.14"}}.resolve, production)
	_, err := d(context.Background(), "tcp", "public.example.com:443")
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the dialer's error", err)
	}
}

func TestSafeDialAllowsLoopbackOnlyUnderTheTestPolicy(t *testing.T) {
	var dialed string
	dial := func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = addr
		return nil, errors.New("no server")
	}
	d := safeDial(dial, net.DefaultResolver, TestPolicy())
	_, _ = d(context.Background(), "tcp", "127.0.0.1:8080")
	if dialed != "127.0.0.1:8080" {
		t.Fatalf("test policy dialed %q", dialed)
	}
}
