// Package profile turns one configured client into an isolated gotd/td
// configuration.
//
// Official clients leave telegram.Options.DC, DCList, Resolver and PublicKeys at
// their zero values so gotd applies Telegram's production defaults. Custom
// clients set all four explicitly from the configuration file, so a custom
// client can never reach official Telegram, its CDN, or its bundled RSA keys.
package profile

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/log/logslog"

	"github.com/gotd/td/exchange"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/keys"
	"github.com/mirza/telesync/internal/routing"
	"github.com/mirza/telesync/internal/sessionstore"
)

// Kind identifies how a client connects.
type Kind string

const (
	// Official targets Telegram production infrastructure.
	Official Kind = "official"
	// Custom targets a user-supplied MTProto backend.
	Custom Kind = "custom"
)

// Spec is a fully resolved, isolated client configuration.
type Spec struct {
	Kind        Kind
	Key         string
	AppID       int
	AppHash     string
	Phone       string
	SessionPath string
	Session     *sessionstore.Store

	// Custom-only fields; zero for the official profile.
	DC         int
	Endpoint   string
	PublicKeys []exchange.PublicKey
	DCList     dcs.List
	Resolver   dcs.Resolver
	Protocol   string
	Obfuscated bool

	// UpdateHandler receives incoming Telegram updates. Nil installs the
	// no-op handler: updates are still requested (NoUpdates stays false, which
	// the custom backend requires) but discarded. Automation installs gotd's
	// update manager here so new channel messages reach the pipeline. Either
	// way a handler is present — without one gotd wraps every request in
	// invokeWithoutUpdates, which the custom backend rejects (D10).
	UpdateHandler telegram.UpdateHandler
}

// IsOfficial reports whether this is the official profile.
func (s *Spec) IsOfficial() bool { return s.Kind == Official }

// FromClient resolves a spec for one configured instance.
//
// The instance key, session path and (for custom servers) endpoint all come from
// the configuration file. Isolation rules are identical to the legacy two-profile
// build: official leaves gotd's production defaults in place, custom sets
// everything explicitly.
func FromClient(cl appconfig.Client) (*Spec, error) {
	if cl.Key == "" {
		return nil, fmt.Errorf("client key is required")
	}
	if cl.AppID <= 0 {
		return nil, fmt.Errorf("client %q: app_id is required and must be positive", cl.Key)
	}
	if strings.TrimSpace(cl.APIHash) == "" {
		return nil, fmt.Errorf("client %q: app_hash is required (resolve app_hash_env first)", cl.Key)
	}
	if strings.TrimSpace(cl.SessionPath) == "" {
		return nil, fmt.Errorf("client %q: session_path is required", cl.Key)
	}
	store, err := sessionstore.NewAt(cl.SessionPath)
	if err != nil {
		return nil, fmt.Errorf("client %q: %w", cl.Key, err)
	}

	switch cl.Type {
	case appconfig.Official:
		return &Spec{
			Kind:        Official,
			Key:         cl.Key,
			AppID:       cl.AppID,
			AppHash:     cl.APIHash,
			Phone:       cl.Phone,
			SessionPath: store.Path(),
			Session:     store,
			// Zero DC/DCList/Resolver/PublicKeys: gotd's production defaults.
		}, nil

	case appconfig.Custom:
		if cl.Server == nil {
			return nil, fmt.Errorf("client %q: type custom requires a server block", cl.Key)
		}
		srv := cl.Server
		pubKeys, err := keys.Load(srv.RSAPublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("client %q: %w", cl.Key, err)
		}
		proto, err := routing.Protocol(srv.Transport)
		if err != nil {
			return nil, fmt.Errorf("client %q: %w", cl.Key, err)
		}
		policy := routing.Policy(srv.Routing)
		if policy == "" {
			policy = routing.SingleDC
		}
		resolver := routing.New(routing.Options{
			Host:       srv.Host,
			Port:       srv.Port,
			DCID:       srv.DCID,
			Policy:     policy,
			Protocol:   proto,
			Obfuscated: srv.Obfuscated,
		})

		if st := sessionstore.Inspect(store.Path()); st.Exists && st.DC != 0 && st.DC != srv.DCID {
			return nil, fmt.Errorf(
				"client %q: session file %q belongs to DC %d but the configuration says DC %d; "+
					"refusing to reuse it. Move the file aside (it is credentials, not cache)",
				cl.Key, store.Path(), st.DC, srv.DCID)
		}

		return &Spec{
			Kind:        Custom,
			Key:         cl.Key,
			AppID:       cl.AppID,
			AppHash:     cl.APIHash,
			Phone:       cl.Phone,
			SessionPath: store.Path(),
			Session:     store,
			DC:          srv.DCID,
			Endpoint:    resolver.Endpoint(),
			PublicKeys:  pubKeys,
			DCList:      customDCList(srv.Host, srv.Port, srv.DCID),
			Resolver:    resolver,
			Protocol:    srv.Transport,
			Obfuscated:  srv.Obfuscated,
		}, nil

	default:
		return nil, fmt.Errorf("client %q: unknown type %q", cl.Key, cl.Type)
	}
}

// customDCList is the initial address list. It only serves the very first dial:
// gotd later replaces its in-memory list with help.getConfig, and the resolver
// is what keeps subsequent dials on the configured endpoint.
func customDCList(host string, port, dcID int) dcs.List {
	return dcs.List{Options: []tg.DCOption{{
		ID:        dcID,
		IPAddress: host,
		Port:      port,
		Static:    true,
	}}}
}

// Options returns the telegram.Options for this spec.
//
// logTo, when non-nil, receives gotd's own diagnostics. They are the only way
// to see why a handshake keeps failing.
func (s *Spec) Options(state func(telegram.ConnectionState), logTo io.Writer) telegram.Options {
	opt := telegram.Options{
		SessionStorage: s.Session,
		// A handler keeps NoUpdates=false, so gotd does not wrap requests in
		// invokeWithoutUpdates. Verified against the custom backend: with no
		// handler installed, initConnection is wrapped and the backend answers
		// 400 CONNECTION_NOT_INITED. By default the handler discards updates —
		// telesync is not a messaging client. Automation installs gotd's update
		// manager here instead, which is still a handler and so still keeps the
		// backend happy.
		UpdateHandler: s.UpdateHandler,
		Device: telegram.DeviceConfig{
			DeviceModel:    "telesync",
			SystemVersion:  "linux",
			AppVersion:     "0.1.0",
			SystemLangCode: "en",
			LangCode:       "en",
		},
	}
	if opt.UpdateHandler == nil {
		opt.UpdateHandler = telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error {
			return nil
		})
	}
	if state != nil {
		opt.OnConnectionState = state
	}
	if logTo != nil {
		opt.Logger = logslog.New(slog.New(slog.NewTextHandler(logTo, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))
	}
	if s.IsOfficial() {
		// Intentionally left zero: official DC list, resolver and public keys.
		return opt
	}
	opt.DC = s.DC
	opt.DCList = s.DCList
	opt.Resolver = s.Resolver
	opt.PublicKeys = s.PublicKeys
	return opt
}

// NewClient constructs the gotd client for this spec.
func (s *Spec) NewClient(state func(telegram.ConnectionState), logTo io.Writer) *telegram.Client {
	return s.NewClientWithBackoff(state, logTo, nil)
}

// NewClientWithBackoff constructs the client with an explicit reconnection
// policy.
//
// gotd's own default backs off exponentially up to 5 s between attempts; a
// network that is simply unreachable (a filtered link, a VPN that is not up yet)
// is retried at a fixed, operator-chosen interval instead, so the connection
// comes back as soon as the network does. A nil factory keeps the default.
func (s *Spec) NewClientWithBackoff(state func(telegram.ConnectionState), logTo io.Writer,
	backoff func() backoff.BackOff) *telegram.Client {

	opt := s.Options(state, logTo)
	if backoff != nil {
		opt.ReconnectionBackoff = backoff
	}
	return telegram.NewClient(s.AppID, s.AppHash, opt)
}

// Describe returns redacted, human-readable configuration lines.
func (s *Spec) Describe() []string {
	out := []string{
		fmt.Sprintf("key: %s", s.Key),
		fmt.Sprintf("api_id: %d", s.AppID),
		fmt.Sprintf("api_hash: %s", appconfig.MaskSecret(s.AppHash)),
		fmt.Sprintf("phone: %s", describePhone(s.Phone)),
		fmt.Sprintf("session: %s", s.SessionPath),
	}
	if s.IsOfficial() {
		return append(out, "server: Telegram production defaults (gotd bundled DC list, resolver and keys)")
	}
	return append(out,
		fmt.Sprintf("server: %s", s.Endpoint),
		fmt.Sprintf("datacenter: %d", s.DC),
		fmt.Sprintf("transport: %s (obfuscated=%t)", s.Protocol, s.Obfuscated),
		fmt.Sprintf("public keys: %d [%s]", len(s.PublicKeys),
			strings.Join(keys.Fingerprints(s.PublicKeys), ", ")),
	)
}

func describePhone(phone string) string {
	if phone == "" {
		return "(not set, will prompt at auth)"
	}
	return appconfig.MaskPhone(phone)
}
