package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// This file is the automation bridge: the reverse half of the private RPC.
//
// The worker's ordinary routes run daemon → worker over the worker's own socket.
// Automation runs the other way. An MTProto update is observed in the worker,
// which holds no database, and the durable record belongs to the daemon. So the
// worker calls a small, bounded route on a daemon-served socket to hand over one
// normalized message at a time, and to read or advance the gotd update counter
// the daemon keeps for it.
//
// Nothing here touches a network. The socket is the same private, file-permission
// boundary as the worker's own: never a TCP listener, never a public endpoint.

// AutomationPath is the single route carrying both directions of the bridge: a
// message observation or one update-state call, never both.
const AutomationPath = "/automation"

// MaxIngestTextBytes bounds the text of one ingested message. It matches what a
// single Telegram message can hold, so a real message is never truncated while a
// malformed or hostile payload is rejected before it is stored.
const MaxIngestTextBytes = 64 << 10

// MaxIngestEntities bounds the formatting spans one ingested message carries.
const MaxIngestEntities = 256

// MaxIngestReactions bounds the reaction tallies one ingested message carries.
const MaxIngestReactions = 64

// IngestMessage is one observed channel message crossing from a worker to the
// daemon.
//
// It carries rpc.Message-shaped metadata and nothing else. Media bytes are never
// copied here: an attachment is referenced by the media path the message already
// reports and read later through the authorized media pipeline. Keeping bytes off
// this route is what makes ingestion cheap and bounded on the update path.
type IngestMessage struct {
	// ClientKey is the connection that observed the message.
	ClientKey string `json:"client_key"`
	// NetworkKey is the network the connection belongs to. Two connections on
	// one network describe one logical message; a different network is a
	// different message even when its numbers coincide.
	NetworkKey string `json:"network_key"`
	// TelegramChannelID is the remote channel id. The daemon resolves it to the
	// local managed_channels.id, which is the only value a permission check may
	// use.
	TelegramChannelID int64 `json:"telegram_channel_id"`
	// Message is the normalized, media-metadata-only message.
	Message Message `json:"message"`
	// GroupedID is the album the message belongs to, zero when it stands alone.
	GroupedID int64 `json:"grouped_id,omitempty"`
	// ReceivedAt is when the worker observed it, which is not the message's own
	// date: a recovered message is old and newly seen.
	ReceivedAt time.Time `json:"received_at"`
}

// IngestOutcome is what the daemon did with one observation. Every value is a
// normal outcome, not an error: duplicate delivery is expected, a suppressed
// message is our own post, and an unmanaged channel is simply not automation's
// business.
type IngestOutcome string

const (
	// IngestInserted is a new event, stored and fanned out.
	IngestInserted IngestOutcome = "inserted"
	// IngestDuplicate is a message already known. The event is not created again
	// and no jobs are scheduled.
	IngestDuplicate IngestOutcome = "duplicate"
	// IngestSuppressed is a message this installation published. It is never
	// stored as an event, so a workflow cannot feed itself.
	IngestSuppressed IngestOutcome = "suppressed"
	// IngestUnmanaged is a message in a channel this installation does not
	// manage. Nothing is stored: automation reacts to managed source channels
	// only.
	IngestUnmanaged IngestOutcome = "unmanaged"
)

// StateSnapshot is gotd's user update counter set. The daemon holds it; the
// worker reads and advances it so missed updates can be asked for instead of
// lost across a restart.
type StateSnapshot struct {
	Pts  int `json:"pts"`
	Qts  int `json:"qts"`
	Date int `json:"date"`
	Seq  int `json:"seq"`
}

// State-method names understood by the bridge. They mirror gotd's
// updates.StateStorage and access-hash interfaces one for one, so a change in
// either is a compile error here rather than a silent loss of state.
const (
	StateGetState      = "get_state"
	StateSetState      = "set_state"
	StateSetPts        = "set_pts"
	StateSetQts        = "set_qts"
	StateSetDate       = "set_date"
	StateSetSeq        = "set_seq"
	StateSetDateSeq    = "set_date_seq"
	StateGetChannelPts = "get_channel_pts"
	StateSetChannelPts = "set_channel_pts"
	StateListChannels  = "list_channel_pts"
	StateGetPeerHash   = "get_peer_hash"
	StateSetPeerHash   = "set_peer_hash"
	StateGetCheckpoint = "get_checkpoint"
	StateSetCheckpoint = "set_checkpoint"
)

// StateCall is one update-state query or mutation.
//
// Exactly the fields the named method needs are set. The method name is the
// discriminator; a call with an unknown method is refused rather than guessed
// at.
type StateCall struct {
	Method string `json:"method"`
	// ClientKey scopes the state to one connection. Several independent
	// connections run at once and their counters must never be confused.
	ClientKey string `json:"client_key"`
	// UserID is the account whose update state this is.
	UserID int64 `json:"user_id"`
	// ChannelID is a channel counter (channel pts methods).
	ChannelID int64 `json:"channel_id,omitempty"`
	// TargetID is a peer id (access-hash methods).
	TargetID int64 `json:"target_id,omitempty"`
	// Kind is the peer kind for access-hash methods: "channel" or "user".
	Kind string `json:"kind,omitempty"`
	// State is the whole counter set (set_state).
	State *StateSnapshot `json:"state,omitempty"`
	// Pts, Qts, Date, Seq, AccessHash are the individual values.
	Pts        int   `json:"pts,omitempty"`
	Qts        int   `json:"qts,omitempty"`
	Date       int   `json:"date,omitempty"`
	Seq        int   `json:"seq,omitempty"`
	AccessHash int64 `json:"access_hash,omitempty"`
	// LastMessageID is the per-channel backfill checkpoint (checkpoint methods).
	LastMessageID int64 `json:"last_message_id,omitempty"`
}

// ChannelPts is one channel's high-water mark, for a list answer.
type ChannelPts struct {
	ChannelID int64 `json:"channel_id"`
	Pts       int   `json:"pts"`
}

// StateResult is the answer to one StateCall. Only the fields the method
// produces are set.
type StateResult struct {
	Found      bool           `json:"found"`
	State      *StateSnapshot `json:"state,omitempty"`
	Pts        int            `json:"pts,omitempty"`
	AccessHash int64          `json:"access_hash,omitempty"`
	Channels   []ChannelPts   `json:"channels,omitempty"`
	// LastMessageID is the per-channel backfill checkpoint (checkpoint methods).
	LastMessageID int64 `json:"last_message_id,omitempty"`
}

// AutomationCall is one bridge request: exactly one of Message or State is set.
type AutomationCall struct {
	Message *IngestMessage `json:"message,omitempty"`
	State   *StateCall     `json:"state,omitempty"`
}

// AutomationResult is one bridge answer: the matching half of the call.
type AutomationResult struct {
	Outcome IngestOutcome `json:"outcome,omitempty"`
	State   *StateResult  `json:"state,omitempty"`
}

// IngestBackend is the daemon side of message ingestion. It resolves the
// message's channel to a local managed row, turns the observation into a durable
// event and schedules the automations that subscribe to it. It is implemented by
// the daemon's event ingestor.
type IngestBackend interface {
	Ingest(ctx context.Context, msg IngestMessage) (IngestOutcome, error)
}

// StateBackend is the daemon side of update-state persistence. It is one method
// because the wire is one bounded route; the adapter behind it dispatches on the
// call's method name. It is backed by the daemon's channel store.
type StateBackend interface {
	CallState(ctx context.Context, call StateCall) (StateResult, error)
}

// IngestBackendFunc and StateBackendFunc adapt plain functions to the backends.
type IngestBackendFunc func(ctx context.Context, msg IngestMessage) (IngestOutcome, error)

// Ingest implements IngestBackend.
func (f IngestBackendFunc) Ingest(ctx context.Context, msg IngestMessage) (IngestOutcome, error) {
	return f(ctx, msg)
}

// StateBackendFunc adapts a function to StateBackend.
type StateBackendFunc func(ctx context.Context, call StateCall) (StateResult, error)

// CallState implements StateBackend.
func (f StateBackendFunc) CallState(ctx context.Context, call StateCall) (StateResult, error) {
	return f(ctx, call)
}

// NewAutomationHandler serves the automation bridge.
//
// ingest and state are the daemon's two halves. Either may be nil, in which case
// that half answers CodeNotFound: a deployment with automation switched off must
// not appear to accept observations it will drop on the floor.
func NewAutomationHandler(ingest IngestBackend, state StateBackend) http.Handler {
	return &automationHandler{ingest: ingest, state: state}
}

type automationHandler struct {
	ingest IngestBackend
	state  StateBackend
}

func (h *automationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := trimPath(r.URL.Path)
	if path != AutomationPath {
		http.NotFound(w, r)
		return
	}
	var call AutomationCall
	if !decodeJSON(w, r, &call) {
		return
	}
	switch {
	case call.Message != nil && call.State != nil:
		failJSON(w, NewError(CodeInvalidInput,
			"exactly one of message or state must be set", 0))
	case call.Message != nil:
		if h.ingest == nil {
			failJSON(w, NewError(CodeNotFound, "automation ingestion is not enabled", 0))
			return
		}
		if err := validateIngest(*call.Message); err != nil {
			failJSON(w, err)
			return
		}
		outcome, err := h.ingest.Ingest(r.Context(), *call.Message)
		if err != nil {
			failJSON(w, err)
			return
		}
		writeJSON(w, http.StatusOK, AutomationResult{Outcome: outcome})
	case call.State != nil:
		if h.state == nil {
			failJSON(w, NewError(CodeNotFound, "automation state is not enabled", 0))
			return
		}
		res, err := h.state.CallState(r.Context(), *call.State)
		if err != nil {
			failJSON(w, err)
			return
		}
		writeJSON(w, http.StatusOK, AutomationResult{State: &res})
	default:
		failJSON(w, NewError(CodeInvalidInput, "one of message or state must be set", 0))
	}
}

// validateIngest keeps one observation bounded. It is the boundary that stops a
// hostile or malformed payload from being stored at full size: the update path
// must never become a way to write unbounded rows.
func validateIngest(msg IngestMessage) error {
	if msg.ClientKey == "" {
		return NewError(CodeInvalidInput, "client_key is required", 0)
	}
	if msg.TelegramChannelID == 0 {
		return NewError(CodeInvalidInput, "telegram_channel_id is required", 0)
	}
	if msg.Message.ID == 0 {
		return NewError(CodeInvalidInput, "message.id is required", 0)
	}
	if len(msg.Message.Text) > MaxIngestTextBytes {
		return NewError(CodeInvalidInput,
			"message text exceeds "+strconv.Itoa(MaxIngestTextBytes)+" bytes", 0)
	}
	if len(msg.Message.Entities) > MaxIngestEntities {
		return NewError(CodeInvalidInput,
			"message carries more than "+strconv.Itoa(MaxIngestEntities)+" entities", 0)
	}
	if len(msg.Message.Reactions) > MaxIngestReactions {
		return NewError(CodeInvalidInput,
			"message carries more than "+strconv.Itoa(MaxIngestReactions)+" reactions", 0)
	}
	return nil
}

// AutomationClient is the worker side of the bridge: the handle a worker uses to
// hand over an observation and to keep its update counter current in the daemon.
//
// The client is safe for concurrent use. Each call is one bounded request over
// the private socket, so the update path never opens a stream or a media
// transfer.
type AutomationClient struct {
	http *http.Client
}

// NewAutomationClient connects to the daemon's automation socket.
//
// socketPath is the daemon-served ingest socket, a private Unix socket in a
// 0700 directory. timeout bounds one bridge call; ingestion must not hang the
// update callback indefinitely.
func NewAutomationClient(socketPath string, timeout time.Duration) *AutomationClient {
	return NewAutomationClientWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}, timeout)
}

// NewAutomationClientWithDialer builds a client over an injected dialer (tests).
func NewAutomationClientWithDialer(dial DialFunc, timeout time.Duration) *AutomationClient {
	transport := &http.Transport{
		DialContext:           dial,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &AutomationClient{http: &http.Client{Transport: transport, Timeout: timeout}}
}

// Ingest hands one normalized observation to the daemon. It returns the outcome
// the daemon recorded. It never blocks on webhook delivery, AI calls or
// publishing: those are scheduled as durable jobs and run later. What it does
// wait for is the durable store, so that by the time the caller returns the
// event is on disk and a crash cannot skip it.
func (c *AutomationClient) Ingest(ctx context.Context, msg IngestMessage) (IngestOutcome, error) {
	var res AutomationResult
	if err := c.post(ctx, AutomationCall{Message: &msg}, &res); err != nil {
		return "", err
	}
	return res.Outcome, nil
}

// CallState performs one update-state query or mutation in the daemon.
func (c *AutomationClient) CallState(ctx context.Context, call StateCall) (StateResult, error) {
	var res AutomationResult
	if err := c.post(ctx, AutomationCall{State: &call}, &res); err != nil {
		return StateResult{}, err
	}
	if res.State == nil {
		return StateResult{}, fmt.Errorf("rpc: the daemon returned no state for %q", call.Method)
	}
	return *res.State, nil
}

func (c *AutomationClient) post(ctx context.Context, call AutomationCall, dst *AutomationResult) error {
	payload, err := json.Marshal(call)
	if err != nil {
		return fmt.Errorf("encode automation request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://daemon"+AutomationPath,
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return requestError(ctx, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return decodeErrorBody(resp.StatusCode, body)
	}
	return json.Unmarshal(body, dst)
}

// Close releases idle connections.
func (c *AutomationClient) Close() {
	c.http.CloseIdleConnections()
}

// --- shared JSON helpers for the reverse handler ---

func trimPath(p string) string {
	if len(p) > 1 && p[len(p)-1] == '/' {
		return p[:len(p)-1]
	}
	return p
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Method != http.MethodPost {
		failJSON(w, NewError(CodeMethodNotAllowed, "method not allowed", 0))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		failJSON(w, NewError(CodeInvalidInput, "the request body is not valid", 0))
		return false
	}
	return true
}

func failJSON(w http.ResponseWriter, err error) {
	var coded *Error
	if errors.As(err, &coded) {
		writeJSON(w, statusForCode(coded.Code), ErrorBody{Error: coded})
		return
	}
	// A backend error is reported honestly without leaking its text to the
	// caller; the daemon logs the detail.
	writeJSON(w, http.StatusInternalServerError,
		ErrorBody{Error: NewError(CodeInternal, "the automation backend failed", 0)})
}
