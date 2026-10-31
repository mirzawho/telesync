package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/mirza/telesync/internal/rpc"
)

// serversPath is the listing of selectable Telegram servers. It is the one
// Telegram route without a client key in its path: it is how a client learns
// the keys.
const serversPath = "/api/v1/telegram"

// ServerInfo is one configured Telegram client as the listing reports it.
//
// Only what a selector needs: a stable key, the client type, whether it is
// enabled, and whether a worker is registered for it. Credentials, endpoints,
// session paths and every other configuration field are deliberately absent —
// this type is the whole of what the route may say about a client.
type ServerInfo struct {
	Key     string
	Type    string
	Enabled bool
	// Registered is true when a worker serves the client, so its health can be
	// checked at all.
	Registered bool
}

// serverStateUnknown is not a claim about a server: it says this request did
// not learn the worker's state, because no worker serves it.
const serverStateUnknown = "unknown"

// serverStateUnavailable is a registered worker that did not answer its
// health check (unreachable socket, or the request's deadline).
const serverStateUnavailable = "unavailable"

// serverView is one row of GET /api/v1/telegram.
//
// `enabled` is configuration; `state` is the worker's live report. A server is
// listed because it is enabled, never because it is healthy, and the two are
// never conflated: a configured client whose worker is down is still listed,
// with a state that says so.
type serverView struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	// HealthCheckable says whether a worker answered for this server, so a
	// caller can tell "no worker" from "worker says unknown".
	HealthCheckable bool   `json:"health_checkable"`
	State           string `json:"state"`
	Authorized      *bool  `json:"authorized"`
	Transport       string `json:"transport"`
	LastSuccessAt   any    `json:"last_success_at"`
	LastError       string `json:"last_error"`
	// User is the server's own Telegram account, null when it is not known.
	User *serverUser `json:"user"`
}

// servers serves GET /api/v1/telegram: the enabled clients behind the API,
// their live connection state, and who each one is signed in as. The
// dashboard's server status card and the panel's server selectors read it.
func (s *Server) servers(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFrom(r)
	if !s.requireServersView(w, r, requestID) {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Only GET is supported.", requestID)
		return
	}
	for name := range r.URL.Query() {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"unknown query parameter "+strconv.Quote(name)+".", requestID)
		return
	}

	ctx := r.Context()
	rows := make([]serverView, 0, len(s.cfg.Servers))
	for _, info := range s.cfg.Servers {
		if !info.Enabled {
			continue
		}
		row := s.serverView(ctx, info, requestID)
		row.User = s.serverUser(ctx, info, requestID)
		rows = append(rows, row)
	}

	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"servers": rows},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// serverView asks one worker for its health, and turns every failure into a
// state that names it rather than failing the listing.
func (s *Server) serverView(ctx context.Context, info ServerInfo, requestID string) serverView {
	out := serverView{Key: info.Key, Type: info.Type, Enabled: true, State: serverStateUnknown}
	if !info.Registered {
		out.LastError = "No worker serves this client."
		return out
	}
	worker, ok := s.cfg.Workers.Worker(info.Key)
	if !ok {
		out.LastError = "No worker serves this client."
		return out
	}

	health, err := worker.Health(ctx)
	if err != nil {
		out.State = serverStateUnavailable
		if errors.Is(err, rpc.ErrNeedsAuth) {
			out.State = string(rpc.StateNeedsAuth)
			notConfigured := false
			out.Authorized = &notConfigured
		}
		message := "The worker did not answer its health check."
		if errors.Is(err, rpc.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			message = "The worker did not answer in time."
		}
		out.LastError = message
		s.logf("gateway: client=%s op=servers request_id=%s health failed: %v", info.Key, requestID, err)
		return out
	}

	out.HealthCheckable = true
	out.State = string(health.State)
	if out.State == "" {
		out.State = serverStateUnknown
	}
	authorized := health.Authorized
	out.Authorized = &authorized
	out.Transport = health.Transport
	if health.LastSuccessAt != nil {
		out.LastSuccessAt = formatTime(*health.LastSuccessAt)
	}
	if health.LastError != "" {
		out.LastError = health.LastError
	}
	return out
}
