package webhooktest_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/webhooktest"
)

// The echo is what this receiver is for. It reports what arrived — method,
// address, headers, body — exactly as it arrived, so an operator can see what
// Telesync actually sent rather than what they hope it sent.

func echoOf(t *testing.T, method, target, contentType string, body []byte) webhooktest.RequestRecord {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req := httptest.NewRequest(method, target, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return webhooktest.Describe(req, body, false)
}

func TestEchoReportsAJSONBodyBothRawAndParsed(t *testing.T) {
	body := []byte(`{"event":"telegram.message.created","message":"Hello"}`)
	got := echoOf(t, http.MethodPost, "/webhook/bearer?source=telegram", "application/json", body)

	if got.Method != http.MethodPost {
		t.Fatalf("Method = %q", got.Method)
	}
	if got.Path != "/webhook/bearer" {
		t.Fatalf("Path = %q", got.Path)
	}
	if got.URI != "/webhook/bearer?source=telegram" {
		t.Fatalf("URI = %q", got.URI)
	}
	if got.ContentType != "application/json" {
		t.Fatalf("ContentType = %q", got.ContentType)
	}
	if got.BodySize != len(body) {
		t.Fatalf("BodySize = %d, want %d", got.BodySize, len(body))
	}
	if got.BodyEncoding != webhooktest.EncodingUTF8 {
		t.Fatalf("BodyEncoding = %q", got.BodyEncoding)
	}
	if string(got.BodyText) != string(body) {
		t.Fatalf("BodyText = %q", got.BodyText)
	}
	if !json.Valid(got.Body) {
		t.Fatalf("Body is not the parsed JSON value: %s", got.Body)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got.Body, &parsed); err != nil {
		t.Fatalf("Body: %v", err)
	}
	if parsed["event"] != "telegram.message.created" {
		t.Fatalf("parsed event = %v", parsed["event"])
	}
}

func TestEchoReportsQueryParameters(t *testing.T) {
	got := echoOf(t, http.MethodGet, "/webhook/none?a=1&a=2&b=x", "", nil)
	if len(got.Query["a"]) != 2 || got.Query["a"][0] != "1" || got.Query["a"][1] != "2" {
		t.Fatalf("Query[a] = %v", got.Query["a"])
	}
	if len(got.Query["b"]) != 1 || got.Query["b"][0] != "x" {
		t.Fatalf("Query[b] = %v", got.Query["b"])
	}
}

func TestEchoKeepsEveryValueOfARepeatedHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook/none", strings.NewReader("{}"))
	req.Header.Add("X-Trace", "one")
	req.Header.Add("X-Trace", "two")

	got := webhooktest.Describe(req, []byte("{}"), false)
	if len(got.Headers["X-Trace"]) != 2 {
		t.Fatalf("Headers[X-Trace] = %v", got.Headers["X-Trace"])
	}
	if got.Headers["X-Trace"][0] != "one" || got.Headers["X-Trace"][1] != "two" {
		t.Fatalf("Headers[X-Trace] = %v", got.Headers["X-Trace"])
	}
}

func TestEchoKeepsTheAuthenticationHeadersExactlyAsTheyArrived(t *testing.T) {
	// The point of echoing is to see what the sender put on the wire, so a
	// credential-bearing header is reported as sent, not redacted.
	req := httptest.NewRequest(http.MethodPost, "/webhook/bearer", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-bearer-token")
	req.Header.Set("Content-Type", "application/json")

	got := webhooktest.Describe(req, []byte("{}"), false)
	if got.Headers["Authorization"][0] != "Bearer test-bearer-token" {
		t.Fatalf("Headers[Authorization] = %v", got.Headers["Authorization"])
	}
}

func TestEchoReportsATextBodyAsText(t *testing.T) {
	body := []byte("hello, plain world\n")
	got := echoOf(t, http.MethodPost, "/webhook/none", "text/plain", body)
	if string(got.BodyText) != string(body) {
		t.Fatalf("BodyText = %q", got.BodyText)
	}
	if got.BodyEncoding != webhooktest.EncodingUTF8 {
		t.Fatalf("BodyEncoding = %q", got.BodyEncoding)
	}
	if len(got.Body) != 0 {
		t.Fatalf("Body = %s, want no parsed value for plain text", got.Body)
	}
}

func TestEchoTreatsInvalidJSONAsRawContent(t *testing.T) {
	body := []byte(`{"event": "unterminated`)
	got := echoOf(t, http.MethodPost, "/webhook/none", "application/json", body)
	if len(got.Body) != 0 {
		t.Fatalf("Body = %s, want no parsed value for invalid JSON", got.Body)
	}
	if string(got.BodyText) != string(body) {
		t.Fatalf("BodyText = %q", got.BodyText)
	}
}

func TestEchoReportsAnEmptyBody(t *testing.T) {
	got := echoOf(t, http.MethodGet, "/webhook/none", "", nil)
	if got.BodySize != 0 {
		t.Fatalf("BodySize = %d", got.BodySize)
	}
	if got.BodyEncoding != webhooktest.EncodingUTF8 {
		t.Fatalf("BodyEncoding = %q", got.BodyEncoding)
	}
	if string(got.BodyText) != "" {
		t.Fatalf("BodyText = %q", got.BodyText)
	}
	if got.ContentLength == nil || *got.ContentLength != 0 {
		t.Fatalf("ContentLength = %v", got.ContentLength)
	}
}

func TestEchoReportsABinaryBodyWithoutCorruptingIt(t *testing.T) {
	body := []byte{0x00, 0xff, 0xfe, 0x80, 0x01, 0x02}
	got := echoOf(t, http.MethodPost, "/webhook/none", "application/octet-stream", body)

	if got.BodyEncoding != webhooktest.EncodingBase64 {
		t.Fatalf("BodyEncoding = %q", got.BodyEncoding)
	}
	if len(got.BodyText) != 0 {
		t.Fatalf("BodyText = %q, want no text form for binary", got.BodyText)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.BodyBase64)
	if err != nil {
		t.Fatalf("BodyBase64: %v", err)
	}
	if string(decoded) != string(body) {
		t.Fatalf("decoded body = %v, want %v", decoded, body)
	}
	if got.BodySize != len(body) {
		t.Fatalf("BodySize = %d, want %d", got.BodySize, len(body))
	}
}

func TestEchoReportsTheProtocolHostAndRemoteAddress(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8091/webhook/none", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:54321"

	got := webhooktest.Describe(req, []byte("{}"), false)
	if got.Protocol != "HTTP/1.1" {
		t.Fatalf("Protocol = %q", got.Protocol)
	}
	if got.Host != "localhost:8091" {
		t.Fatalf("Host = %q", got.Host)
	}
	if got.RemoteAddr != "127.0.0.1:54321" {
		t.Fatalf("RemoteAddr = %q", got.RemoteAddr)
	}
}

func TestEchoOmitsContentLengthWhenTheRequestDidNotDeclareOne(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook/none", strings.NewReader("{}"))
	req.ContentLength = -1

	got := webhooktest.Describe(req, []byte("{}"), false)
	if got.ContentLength != nil {
		t.Fatalf("ContentLength = %v, want it omitted", *got.ContentLength)
	}
}
