package webhooktest_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/webhooktest"
)

// Compatibility with the sender this receiver exists to test.
//
// A request is built exactly the way internal/automation/webhooks builds one —
// same headers, same order of operations, same signature over the same bytes —
// and sent over a real socket. If these pass, a delivery from this build is a
// delivery this receiver accepts; if one fails, the disagreement is in the
// protocol and not in the test.

const (
	senderSecret   = "test-hmac-secret"
	senderDelivery = "wbd_42"
)

// senderRequest builds one request the way the delivery engine builds one. The
// body is signed as these exact bytes and nothing reserializes it afterwards.
func senderRequest(t *testing.T, target, mode, body string, at time.Time) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "telesync-webhooks/1")

	// The delivery id and the timestamp are not secret and every mode carries
	// them, so a receiver can deduplicate a delivery whatever authenticates it.
	sentAt := strconv.FormatInt(at.UTC().Unix(), 10)
	switch mode {
	case string(webhooktest.AuthHMAC):
		sig, err := webhooks.Sign(senderSecret, senderDelivery, at, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		for name, values := range sig.Headers() {
			req.Header[name] = values
		}
	default:
		req.Header.Set(webhooks.IDHeader, senderDelivery)
		req.Header.Set(webhooks.TimestampHeader, sentAt)
		switch mode {
		case string(webhooktest.AuthBearer):
			req.Header.Set("Authorization", "Bearer test-bearer-token")
		case string(webhooktest.AuthHeader):
			req.Header.Set("X-API-Key", "test-header-secret")
		}
	}
	return req
}

func TestEveryModeTheSenderEmitsIsAcceptedOverTheWire(t *testing.T) {
	h := newHarness(t, nil)
	server := httptest.NewServer(h.srv.Handler())
	defer server.Close()

	body := `{"specversion":"1.0","id":"evt_abc"}`
	for _, mode := range []string{"hmac", "bearer", "header", "none"} {
		t.Run(mode, func(t *testing.T) {
			req := senderRequest(t, server.URL+"/webhook/"+mode, mode, body, serverNow)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d: %s", resp.StatusCode, raw)
			}

			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("the response is not valid JSON: %v\n%s", err, raw)
			}
			if payload["success"] != true {
				t.Fatalf("success = %v", payload["success"])
			}
			auth, _ := payload["authentication"].(map[string]any)
			if auth["type"] != mode || auth["valid"] != true {
				t.Fatalf("authentication = %v", auth)
			}

			// The signature covered the bytes that were transmitted, so what is
			// echoed is the same string it was computed over.
			request, _ := payload["request"].(map[string]any)
			if request["body_text"] != body {
				t.Fatalf("echoed body = %v, want %q", request["body_text"], body)
			}
		})
	}
}

func TestASignatureDoesNotSurviveTheBodyBeingChangedOnTheWire(t *testing.T) {
	h := newHarness(t, nil)
	server := httptest.NewServer(h.srv.Handler())
	defer server.Close()

	signed := `{"specversion":"1.0","id":"evt_abc"}`
	// The same request, signed over one body and sent with another: exactly
	// what a proxy rewriting a payload would produce.
	req := senderRequest(t, server.URL+"/webhook/hmac", "hmac", signed, serverNow)
	req.Body = io.NopCloser(strings.NewReader(`{"specversion":"1.0","id":"evt_abd"}`))
	req.ContentLength = int64(len(`{"specversion":"1.0","id":"evt_abd"}`))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), string(webhooktest.CodeInvalidSignature)) {
		t.Fatalf("the refusal does not name the signature: %s", raw)
	}
}

func TestARetryThatReSignsIsAcceptedAndOneThatReplaysIsNot(t *testing.T) {
	h := newHarness(t, nil)
	server := httptest.NewServer(h.srv.Handler())
	defer server.Close()

	body := `{"specversion":"1.0"}`
	// A retry keeps the delivery id and signs again at a later second, which is
	// what makes it a fresh request rather than a replay.
	retry := senderRequest(t, server.URL+"/webhook/hmac", "hmac", body, serverNow.Add(time.Second))
	resp, err := http.DefaultClient.Do(retry)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a re-signed retry answered %d", resp.StatusCode)
	}

	// A signature made yesterday is not a request being made now, whatever its
	// delivery id says.
	old := senderRequest(t, server.URL+"/webhook/hmac", "hmac", body,
		serverNow.Add(-webhooktest.TimestampTolerance-time.Minute))
	resp, err = http.DefaultClient.Do(old)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a stale signature answered %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), string(webhooktest.CodeExpiredTimestamp)) {
		t.Fatalf("the refusal does not name the timestamp: %s", raw)
	}
}
