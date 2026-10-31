// Package routing keeps a custom client on its configured MTProto endpoint.
//
// gotd replaces its datacenter list with the addresses the server reports in
// help.getConfig. A resolver that honoured that list could be redirected off the
// configured backend, so this resolver ignores it and dials only what the
// configuration says.
package routing

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/transport"
)

// Policy decides what happens when gotd asks for a datacenter other than the
// configured one.
type Policy string

const (
	// SingleDC refuses every DC id except the configured one. Correct for a
	// backend that serves exactly one datacenter, because auth keys are
	// per-datacenter.
	SingleDC Policy = "single-dc"
	// MapAll maps every requested DC id onto the configured endpoint. Only
	// correct when the backend serves all DC ids; opt-in only.
	MapAll Policy = "map-all"
)

// ErrForeignDC is returned for connections to an undeclared datacenter while
// the policy is SingleDC.
type ErrForeignDC struct {
	Requested  int
	Configured int
	Endpoint   string
}

// Error implements error.
func (e ErrForeignDC) Error() string {
	return fmt.Sprintf(
		"refusing to connect to DC %d: this custom client serves only DC %d at %s. "+
			"This usually means the server redirected the client or answered with a *_MIGRATE error. "+
			"Fix server.dc_id/server.host, or set server.routing=map-all "+
			"if the backend really serves every datacenter ID",
		e.Requested, e.Configured, e.Endpoint)
}

// Resolver is a dcs.Resolver that never leaves the configured endpoint.
type Resolver struct {
	base     dcs.Resolver
	host     string
	port     int
	dcID     int
	policy   Policy
	endpoint string
}

// Options configures a Resolver.
type Options struct {
	Host     string
	Port     int
	DCID     int
	Policy   Policy
	Protocol transport.Protocol
	// Obfuscated wraps every connection in Obfuscated2, like official clients.
	Obfuscated bool
	// Dial overrides the dialer; used by tests.
	Dial dcs.DialFunc
}

// New builds a resolver bound to one endpoint.
func New(opts Options) *Resolver {
	if opts.Policy == "" {
		opts.Policy = SingleDC
	}
	if opts.Port == 0 {
		opts.Port = 443
	}
	protocol := opts.Protocol
	if protocol.Codec() == nil {
		protocol = transport.Intermediate
	}
	return &Resolver{
		base: dcs.Plain(dcs.PlainOptions{
			Protocol:   protocol,
			Obfuscated: opts.Obfuscated,
			Dial:       opts.Dial,
		}),
		host:     opts.Host,
		port:     opts.Port,
		dcID:     opts.DCID,
		policy:   opts.Policy,
		endpoint: net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)),
	}
}

// Endpoint returns host:port of the configured backend.
func (r *Resolver) Endpoint() string { return r.endpoint }

// check enforces the routing policy for a requested datacenter.
func (r *Resolver) check(dc int) error {
	if r.host == "" {
		return fmt.Errorf("custom backend host is empty")
	}
	if r.dcID < 1 || r.dcID > 5 {
		return fmt.Errorf("custom backend datacenter ID %d is out of range 1-5", r.dcID)
	}
	if r.policy == SingleDC && dc != r.dcID {
		return ErrForeignDC{Requested: dc, Configured: r.dcID, Endpoint: r.endpoint}
	}
	return nil
}

// listFor returns a DC list holding only the configured endpoint. The DC id in
// the entry is the requested one because it is encoded in the Obfuscated2
// handshake and must match what the client asks for.
func (r *Resolver) listFor(dc int) dcs.List {
	return dcs.List{Options: []tg.DCOption{{
		ID:        dc,
		IPAddress: r.host,
		Port:      r.port,
		Static:    true,
	}}}
}

// Primary implements dcs.Resolver.
func (r *Resolver) Primary(ctx context.Context, dc int, _ dcs.List) (transport.Conn, error) {
	if err := r.check(dc); err != nil {
		return nil, err
	}
	return r.base.Primary(ctx, dc, r.listFor(dc))
}

// MediaOnly implements dcs.Resolver. A single-endpoint backend has no separate
// media host, so media connections reuse it; the DC policy still applies.
func (r *Resolver) MediaOnly(ctx context.Context, dc int, _ dcs.List) (transport.Conn, error) {
	if err := r.check(dc); err != nil {
		return nil, err
	}
	return r.base.Primary(ctx, dc, r.listFor(dc))
}

// CDN implements dcs.Resolver. The backend does not advertise CDN hosts, so
// CDN requests fail rather than reaching Telegram's CDN.
func (r *Resolver) CDN(_ context.Context, dc int, _ dcs.List) (transport.Conn, error) {
	return nil, fmt.Errorf(
		"CDN connections are not supported by this custom client (requested DC %d at %s); "+
			"the configured backend does not serve a CDN network", dc, r.endpoint)
}

// Protocol maps a transport name from configuration onto a transport.Protocol.
func Protocol(name string) (transport.Protocol, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "intermediate":
		return transport.Intermediate, nil
	case "abridged":
		return transport.Abridged, nil
	case "padded-intermediate", "padded":
		return transport.PaddedIntermediate, nil
	case "full":
		return transport.Full, nil
	default:
		return transport.Protocol{}, fmt.Errorf("unknown MTProto transport %q", name)
	}
}
