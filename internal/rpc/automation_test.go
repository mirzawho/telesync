package rpc_test

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/rpc"
)

// ingestRecorder is the daemon half: it records what crossed the bridge.
type ingestRecorder struct {
	got     []rpc.IngestMessage
	outcome rpc.IngestOutcome
	err     error
}

func (r *ingestRecorder) Ingest(_ context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
	if r.err != nil {
		return "", r.err
	}
	r.got = append(r.got, msg)
	return r.outcome, nil
}

func stateRecorder(calls *[]rpc.StateCall) rpc.StateBackend {
	return rpc.StateBackendFunc(func(_ context.Context, call rpc.StateCall) (rpc.StateResult, error) {
		*calls = append(*calls, call)
		return rpc.StateResult{Found: true, Pts: 7}, nil
	})
}

// bridge wires a worker client to a daemon handler over a live HTTP server, the
// same JSON-over-a-socket exchange production uses (here over a loopback dial).
func bridge(t *testing.T, ingest rpc.IngestBackend, state rpc.StateBackend) *rpc.AutomationClient {
	t.Helper()
	srv := httptest.NewServer(rpc.NewAutomationHandler(ingest, state))
	t.Cleanup(srv.Close)
	client := rpc.NewAutomationClientWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}, 5*time.Second)
	t.Cleanup(client.Close)
	return client
}

// One observed message crosses the worker→daemon bridge and lands as exactly
// one ingestion, metadata intact.
func TestAnObservationCrossesTheBridge(t *testing.T) {
	rec := &ingestRecorder{outcome: rpc.IngestInserted}
	client := bridge(t, rec, nil)

	msg := rpc.IngestMessage{
		ClientKey:         "official",
		NetworkKey:        "official",
		TelegramChannelID: 123456789,
		ReceivedAt:        time.Unix(1_700_000_000, 0).UTC(),
		Message: rpc.Message{
			ID:   36,
			Text: "hello",
			Media: &rpc.Media{
				ID: "m", Type: "photo", Label: "photo",
				MimeType: "image/jpeg", Size: 1234,
			},
			Views:     intPtr(1),
			Reactions: []rpc.Reaction{},
			Entities:  []rpc.Entity{},
		},
	}
	outcome, err := client.Ingest(context.Background(), msg)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome != rpc.IngestInserted {
		t.Fatalf("outcome = %q, want %q", outcome, rpc.IngestInserted)
	}
	if len(rec.got) != 1 {
		t.Fatalf("daemon received %d observations, want 1", len(rec.got))
	}
	got := rec.got[0]
	if got.TelegramChannelID != 123456789 || got.Message.ID != 36 {
		t.Fatalf("identity = channel %d message %d, want 123456789 36",
			got.TelegramChannelID, got.Message.ID)
	}
	if got.Message.Media == nil || got.Message.Media.Size != 1234 {
		t.Fatalf("media metadata = %+v, want it preserved", got.Message.Media)
	}
}

// The ingestion route is bounded: a hostile or malformed payload is refused
// before it is stored, so the update path cannot be used to write unbounded
// rows.
func TestTheIngestionRouteRefusesAnUnboundedPayload(t *testing.T) {
	rec := &ingestRecorder{outcome: rpc.IngestInserted}
	client := bridge(t, rec, nil)

	huge := rpc.IngestMessage{
		ClientKey:         "official",
		NetworkKey:        "official",
		TelegramChannelID: 1,
		Message:           rpc.Message{ID: 1, Text: strings.Repeat("x", rpc.MaxIngestTextBytes+1)},
	}
	if _, err := client.Ingest(context.Background(), huge); err == nil {
		t.Fatal("an oversized message was accepted")
	}
	if len(rec.got) != 0 {
		t.Fatal("an oversized message reached the store")
	}
}

// A state call crosses the bridge and returns the daemon's answer.
func TestAStateCallCrossesTheBridge(t *testing.T) {
	var calls []rpc.StateCall
	client := bridge(t, nil, stateRecorder(&calls))

	res, err := client.CallState(context.Background(), rpc.StateCall{
		Method: rpc.StateGetState, ClientKey: "client", UserID: 7,
	})
	if err != nil {
		t.Fatalf("call state: %v", err)
	}
	if !res.Found || res.Pts != 7 {
		t.Fatalf("result = found=%t pts=%d, want true 7", res.Found, res.Pts)
	}
	if len(calls) != 1 || calls[0].Method != rpc.StateGetState || calls[0].ClientKey != "client" {
		t.Fatalf("daemon got %+v, want one get_state for client", calls)
	}
}

// A message and a state call must not both be set: one route carries one thing.
func TestTheBridgeRejectsAMixedCall(t *testing.T) {
	handler := rpc.NewAutomationHandler(
		&ingestRecorder{outcome: rpc.IngestInserted}, stateRecorder(&[]rpc.StateCall{}))

	body := `{"message":{"client_key":"a","network_key":"n","telegram_channel_id":1,"received_at":"2026-01-01T00:00:00Z","message":{"id":1}},"state":{"method":"get_state"}}`
	req := httptest.NewRequest("POST", rpc.AutomationPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Fatal("a mixed message+state call was accepted")
	}
}

func intPtr(v int) *int { return &v }
