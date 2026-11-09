package webhooktest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/webhooktest"
)

// The receiver is four paths and one job: say back exactly what arrived, after
// checking it against the credential that route verifies. The HTTP method never
// chooses the authentication, and an unknown path is not a webhook.

var serverNow = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

type harness struct {
	srv *webhooktest.Server
	log bytes.Buffer
	dir string
}

func newHarness(t *testing.T, tweak func(*webhooktest.Options, *webhooktest.Auth)) *harness {
	t.Helper()
	h := &harness{dir: t.TempDir()}
	opts := webhooktest.Options{
		Host:      "127.0.0.1",
		OutputDir: filepath.Join(h.dir, "temp", "webhook-responses"),
		Log:       &h.log,
		Now:       func() time.Time { return serverNow },
	}
	auth := testAuth()
	if tweak != nil {
		tweak(&opts, &auth)
	}
	srv, err := webhooktest.New(opts, auth)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.srv = srv
	return h
}

func (h *harness) do(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *harness) send(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return h.do(t, httptest.NewRequest(method, target, strings.NewReader(body)))
}

func decoded(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("the response is not valid JSON: %v\n%s", err, rec.Body.String())
	}
	return out
}

func requestOf(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	req, ok := payload["request"].(map[string]any)
	if !ok {
		t.Fatalf("the response carries no request: %v", payload)
	}
	return req
}

func TestTheFourRoutesExistAndNothingElseDoes(t *testing.T) {
	h := newHarness(t, nil)
	for _, path := range []string{"/webhook/hmac", "/webhook/bearer", "/webhook/header", "/webhook/none"} {
		if rec := h.send(t, http.MethodPost, path, "{}"); rec.Code != http.StatusUnauthorized &&
			rec.Code != http.StatusOK {
			t.Fatalf("%s answered %d", path, rec.Code)
		}
	}
	for _, path := range []string{
		"/", "/webhook", "/webhook/", "/webhook/none/", "/webhook/hmac/post",
		"/webhook/none/extra", "/Webhook/None", "/admin",
	} {
		t.Run(path, func(t *testing.T) {
			rec := h.send(t, http.MethodPost, path, "{}")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s answered %d, want 404", path, rec.Code)
			}
			if payload := decoded(t, rec); payload["success"] != false {
				t.Fatalf("%s answered %v", path, payload["success"])
			}
		})
	}
}

func TestEveryRouteAcceptsEveryMethodWithoutChangingHowItAuthenticates(t *testing.T) {
	routes := map[string]func(*http.Request){
		"/webhook/hmac": func(r *http.Request) {
			body := `{"event":"test"}`
			sig, err := webhooks.Sign("test-hmac-secret", "wbd_1", serverNow, []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			for name, values := range sig.Headers() {
				r.Header[name] = values
			}
		},
		"/webhook/bearer": func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer test-bearer-token")
		},
		"/webhook/header": func(r *http.Request) {
			r.Header.Set("X-API-Key", "test-header-secret")
		},
		"/webhook/none": func(r *http.Request) {},
	}
	methods := []string{
		http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions,
	}

	h := newHarness(t, nil)
	for path, authenticate := range routes {
		for _, method := range methods {
			t.Run(method+" "+path, func(t *testing.T) {
				req := httptest.NewRequest(method, path, strings.NewReader(`{"event":"test"}`))
				authenticate(req)
				rec := h.do(t, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s %s answered %d: %s", method, path, rec.Code, rec.Body.String())
				}
				payload := decoded(t, rec)
				if payload["success"] != true {
					t.Fatalf("success = %v", payload["success"])
				}
				if got := requestOf(t, payload)["method"]; got != method {
					t.Fatalf("echoed method = %v, want %s", got, method)
				}
			})
		}
	}
}

func TestHeadReturnsHeadersAndNoBody(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.send(t, http.MethodHead, "/webhook/none", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("a HEAD response carried %d body bytes", rec.Body.Len())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Fatal("a HEAD response carries no Content-Length")
	}
}

func TestHeadRefusesBadCredentialsWithoutABody(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.send(t, http.MethodHead, "/webhook/bearer", "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("a HEAD response carried %d body bytes", rec.Body.Len())
	}
}

func TestAValidRequestIsEchoedInFull(t *testing.T) {
	h := newHarness(t, nil)
	req := httptest.NewRequest(http.MethodPost,
		"/webhook/bearer?source=telegram&source=panel", strings.NewReader(`{"message":"hello"}`))
	req.Header.Set("Authorization", "Bearer test-bearer-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("X-Trace", "one")
	req.Header.Add("X-Trace", "two")
	req.RemoteAddr = "127.0.0.1:54321"

	rec := h.do(t, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	payload := decoded(t, rec)
	if payload["success"] != true {
		t.Fatalf("success = %v", payload["success"])
	}
	if payload["received_at"] != "2026-11-08T10:00:00Z" {
		t.Fatalf("received_at = %v", payload["received_at"])
	}

	auth, _ := payload["authentication"].(map[string]any)
	if auth["type"] != "bearer" || auth["valid"] != true {
		t.Fatalf("authentication = %v", auth)
	}

	reqRec := requestOf(t, payload)
	if reqRec["method"] != "POST" || reqRec["path"] != "/webhook/bearer" {
		t.Fatalf("request = %v", reqRec)
	}
	if reqRec["uri"] != "/webhook/bearer?source=telegram&source=panel" {
		t.Fatalf("uri = %v", reqRec["uri"])
	}
	if reqRec["protocol"] != "HTTP/1.1" || reqRec["remote_addr"] != "127.0.0.1:54321" {
		t.Fatalf("request = %v", reqRec)
	}
	if reqRec["body_size"] != float64(len(`{"message":"hello"}`)) {
		t.Fatalf("body_size = %v", reqRec["body_size"])
	}
	if reqRec["body_text"] != `{"message":"hello"}` {
		t.Fatalf("body_text = %v", reqRec["body_text"])
	}
	headers, _ := reqRec["headers"].(map[string]any)
	if got := headers["X-Trace"]; len(got.([]any)) != 2 {
		t.Fatalf("X-Trace = %v", got)
	}
	if got := headers["Authorization"]; got.([]any)[0] != "Bearer test-bearer-token" {
		t.Fatalf("Authorization = %v", got)
	}
	query, _ := reqRec["query"].(map[string]any)
	if len(query["source"].([]any)) != 2 {
		t.Fatalf("query = %v", query)
	}
}

func TestAnInvalidCredentialIsRefusedWithTheReasonAndTheRequest(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.send(t, http.MethodPost, "/webhook/bearer", "{}")
	// No Authorization header at all.

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	payload := decoded(t, rec)
	if payload["success"] != false {
		t.Fatalf("success = %v", payload["success"])
	}
	auth, _ := payload["authentication"].(map[string]any)
	if auth["type"] != "bearer" || auth["valid"] != false {
		t.Fatalf("authentication = %v", auth)
	}
	errBody, _ := payload["error"].(map[string]any)
	if errBody["code"] != string(webhooktest.CodeMissingBearer) {
		t.Fatalf("error = %v", errBody)
	}
	if requestOf(t, payload)["path"] != "/webhook/bearer" {
		t.Fatal("a refused request is not echoed")
	}
}

func TestEveryAuthenticationModeRefusesWhatItMust(t *testing.T) {
	cases := []struct {
		path  string
		code  webhooktest.ErrCode
		setup func(*http.Request)
	}{
		{"/webhook/hmac", webhooktest.CodeMissingSignature, func(r *http.Request) {
			r.Header.Set(webhooks.IDHeader, "wbd_1")
			r.Header.Set(webhooks.TimestampHeader, strconv.FormatInt(serverNow.Unix(), 10))
		}},
		{"/webhook/hmac", webhooktest.CodeInvalidSignature, func(r *http.Request) {
			sig, err := webhooks.Sign("not-the-secret", "wbd_1", serverNow, []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			for name, values := range sig.Headers() {
				r.Header[name] = values
			}
		}},
		{"/webhook/hmac", webhooktest.CodeExpiredTimestamp, func(r *http.Request) {
			sig, err := webhooks.Sign("test-hmac-secret", "wbd_1", serverNow.Add(-time.Hour), []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			for name, values := range sig.Headers() {
				r.Header[name] = values
			}
		}},
		{"/webhook/bearer", webhooktest.CodeMissingBearer, func(r *http.Request) {}},
		{"/webhook/bearer", webhooktest.CodeInvalidBearer, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer wrong")
		}},
		{"/webhook/header", webhooktest.CodeMissingHeader, func(r *http.Request) {}},
		{"/webhook/header", webhooktest.CodeInvalidHeader, func(r *http.Request) {
			r.Header.Set("X-API-Key", "wrong")
		}},
	}
	h := newHarness(t, nil)
	for _, tc := range cases {
		t.Run(tc.path+" "+string(tc.code), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{}`))
			tc.setup(req)
			rec := h.do(t, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			payload := decoded(t, rec)
			errBody, _ := payload["error"].(map[string]any)
			if errBody["code"] != string(tc.code) {
				t.Fatalf("error = %v, want %q", errBody, tc.code)
			}
			if errBody["message"] == nil || errBody["message"] == "" {
				t.Fatalf("error = %v, want a message", errBody)
			}
		})
	}
}

func TestTheUnauthenticatedRouteAnswersEveryMethod(t *testing.T) {
	h := newHarness(t, nil)
	for _, method := range []string{
		http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions,
	} {
		t.Run(method, func(t *testing.T) {
			rec := h.send(t, method, "/webhook/none", "{}")
			if rec.Code != http.StatusOK {
				t.Fatalf("%s answered %d", method, rec.Code)
			}
		})
	}
}

func TestABodyIsEchoedWhateverItIsMadeOf(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
	}{
		{"json", "application/json", `{"event":"x"}`},
		{"plain text", "text/plain", "hello there"},
		{"form data", "application/x-www-form-urlencoded", "a=1&b=2"},
		{"invalid json", "application/json", `{"event": `},
		{"empty", "", ""},
		{"xml", "application/xml", "<a/>"},
	}
	h := newHarness(t, nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook/none", strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rec := h.do(t, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			got := requestOf(t, decoded(t, rec))
			if got["body_text"] != nil && got["body_text"] != tc.body {
				t.Fatalf("body_text = %v, want %q", got["body_text"], tc.body)
			}
			if got["body_size"] != float64(len(tc.body)) {
				t.Fatalf("body_size = %v, want %d", got["body_size"], len(tc.body))
			}
		})
	}
}

func TestABinaryBodyIsEchoedWithoutBeingTurnedIntoText(t *testing.T) {
	h := newHarness(t, nil)
	body := string([]byte{0x00, 0xff, 0xfe, 0x80})
	req := httptest.NewRequest(http.MethodPost, "/webhook/none", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")

	rec := h.do(t, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	got := requestOf(t, decoded(t, rec))
	if got["body_encoding"] != webhooktest.EncodingBase64 {
		t.Fatalf("body_encoding = %v", got["body_encoding"])
	}
	if got["body_text"] != nil {
		t.Fatalf("body_text = %v, want none for binary", got["body_text"])
	}
}

func TestAnOversizedBodyIsRefusedWithoutBeingRead(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.MaxBodyBytes = 16
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook/none", strings.NewReader(strings.Repeat("x", 64)))
	rec := h.do(t, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
	payload := decoded(t, rec)
	if payload["success"] != false {
		t.Fatalf("success = %v", payload["success"])
	}
	errBody, _ := payload["error"].(map[string]any)
	if errBody["code"] != string(webhooktest.CodeBodyTooLarge) {
		t.Fatalf("error = %v", errBody)
	}
	got := requestOf(t, payload)
	if got["body_truncated"] != true {
		t.Fatalf("body_truncated = %v", got["body_truncated"])
	}
	if got["body_text"] != nil {
		t.Fatalf("an oversized body was echoed: %v", got["body_text"])
	}
}

func TestABodyInsideTheLimitIsAccepted(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.MaxBodyBytes = 16
	})
	rec := h.send(t, http.MethodPost, "/webhook/none", strings.Repeat("x", 16))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSaveResponseWritesNothingWhenItIsNotEnabled(t *testing.T) {
	h := newHarness(t, nil)
	h.send(t, http.MethodPost, "/webhook/none", "{}")

	dir := filepath.Join(h.dir, "temp", "webhook-responses")
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) != 0 {
		t.Fatalf("%d files were written with --save-response off", len(entries))
	}
}

func TestSaveResponseWritesOneFilePerRequestAndPrintsItsPath(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.SaveResponses = true
	})
	h.send(t, http.MethodPost, "/webhook/none", `{"message":"one"}`)
	h.send(t, http.MethodPost, "/webhook/none", `{"message":"two"}`)

	dir := filepath.Join(h.dir, "temp", "webhook-responses")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d files were written for two requests", len(entries))
	}
	for _, entry := range entries {
		abs, err := filepath.Abs(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(h.log.String(), abs) {
			t.Fatalf("the console did not print %s\n%s", abs, h.log.String())
		}
	}
}

func TestSaveResponseKeepsRefusedRequests(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.SaveResponses = true
	})
	h.send(t, http.MethodPost, "/webhook/bearer", "{}")

	dir := filepath.Join(h.dir, "temp", "webhook-responses")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d files were written for one refused request", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var rec webhooktest.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("the saved record is not valid JSON: %v", err)
	}
	if rec.Authentication.Valid {
		t.Fatal("a refused request was saved as authenticated")
	}
	if rec.Response.Status != http.StatusUnauthorized {
		t.Fatalf("saved status = %d", rec.Response.Status)
	}
}

func TestSaveResponseRecordsTheBodyTheHandlerActuallySent(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.SaveResponses = true
	})
	rec := h.send(t, http.MethodPost, "/webhook/none", `{"message":"hello"}`)

	dir := filepath.Join(h.dir, "temp", "webhook-responses")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var saved webhooktest.Record
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("the saved record is not valid JSON: %v", err)
	}
	if saved.Response.BodyText != rec.Body.String() {
		t.Fatalf("saved body = %q, want %q", saved.Response.BodyText, rec.Body.String())
	}
	if saved.Response.Status != rec.Code {
		t.Fatalf("saved status = %d, want %d", saved.Response.Status, rec.Code)
	}
	if saved.Route != "/webhook/none" || saved.AuthType != webhooktest.AuthNone {
		t.Fatalf("saved route = %q %q", saved.Route, saved.AuthType)
	}
}

func TestSaveResponseRecordsThatAHeadResponseHasNoBody(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.SaveResponses = true
	})
	h.send(t, http.MethodHead, "/webhook/none", "")

	dir := filepath.Join(h.dir, "temp", "webhook-responses")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var saved webhooktest.Record
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("the saved record is not valid JSON: %v", err)
	}
	if !saved.Response.BodyOmitted {
		t.Fatal("a HEAD record does not say its body was omitted")
	}
	if saved.Response.BodyText != "" {
		t.Fatalf("a HEAD record carries a body: %q", saved.Response.BodyText)
	}
}

func TestTheConsoleNeverPrintsASecretOrABody(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.SaveResponses = true
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook/bearer", strings.NewReader(`{"private":"message body"}`))
	req.Header.Set("Authorization", "Bearer test-bearer-token")
	h.do(t, req)

	out := h.log.String()
	for _, secret := range []string{
		"test-bearer-token", "test-hmac-secret", "test-header-secret",
		"message body", "private",
	} {
		if strings.Contains(out, secret) {
			t.Fatalf("the console printed %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "[webhook-test]") {
		t.Fatalf("the console printed nothing recognisable:\n%s", out)
	}
}

func TestAFailedSaveIsReportedRatherThanClaimed(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.SaveResponses = true
	})
	// Remove the output directory underneath the server so the write fails.
	if err := os.RemoveAll(h.srv.Store().Dir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.srv.Store().Dir(), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := h.send(t, http.MethodPost, "/webhook/none", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("a failed save stopped the receiver: %d", rec.Code)
	}
	out := h.log.String()
	if strings.Contains(out, "Saved:") {
		t.Fatalf("the console claimed a save that failed:\n%s", out)
	}
	if !strings.Contains(out, "save failed") {
		t.Fatalf("the console did not report the failed save:\n%s", out)
	}
}

func TestTheBannerDescribesTheReceiverWithoutLeakingAnything(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.SaveResponses = true
		o.Port = 8091
	})
	var banner bytes.Buffer
	h.srv.WriteBanner(&banner)
	out := banner.String()

	for _, want := range []string{
		"Telesync Webhook Test Receiver",
		"ANY /webhook/hmac",
		"ANY /webhook/bearer",
		"ANY /webhook/header",
		"ANY /webhook/none",
		"8091",
		"Save responses: enabled",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the banner does not mention %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"test-hmac-secret", "test-bearer-token", "test-header-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the banner printed %q:\n%s", secret, out)
		}
	}
}

func TestTheBannerSaysSavingIsOff(t *testing.T) {
	h := newHarness(t, nil)
	var banner bytes.Buffer
	h.srv.WriteBanner(&banner)
	if !strings.Contains(banner.String(), "Save responses: disabled") {
		t.Fatalf("banner:\n%s", banner.String())
	}
}

func TestTheBannerWarnsWhenTheBindingIsNotLoopback(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "192.168.1.10", "example.com"} {
		t.Run(host, func(t *testing.T) {
			h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) { o.Host = host })
			var banner bytes.Buffer
			h.srv.WriteBanner(&banner)
			out := banner.String()
			if !strings.Contains(out, "not a loopback address") {
				t.Fatalf("binding %s produced no exposure warning:\n%s", host, out)
			}
			if !strings.Contains(out, host) {
				t.Fatalf("the warning does not name %s:\n%s", host, out)
			}
		})
	}
}

func TestAHostIsHonouredWhenTheServerBinds(t *testing.T) {
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.Host = "127.0.0.1"
		o.Port = freePort(t)
	})
	if got := h.srv.ListenAddr(); !strings.HasPrefix(got, "127.0.0.1:") {
		t.Fatalf("ListenAddr() = %q", got)
	}

	srv, err := webhooktest.New(webhooktest.Options{Host: "localhost", Log: &bytes.Buffer{}}, testAuth())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.HasPrefix(srv.ListenAddr(), "localhost:") {
		t.Fatalf("ListenAddr() = %q", srv.ListenAddr())
	}
}

func TestNewRefusesConfigurationItCannotServe(t *testing.T) {
	cases := []struct {
		name  string
		tweak func(*webhooktest.Options, *webhooktest.Auth)
	}{
		{"a port above the valid range", func(o *webhooktest.Options, _ *webhooktest.Auth) { o.Port = 70000 }},
		{"a negative port", func(o *webhooktest.Options, _ *webhooktest.Auth) { o.Port = -1 }},
		{"a header name the sender would never send", func(_ *webhooktest.Options, a *webhooktest.Auth) {
			a.HeaderName = "Authorization"
		}},
		{"no output directory when saving", func(o *webhooktest.Options, _ *webhooktest.Auth) {
			o.SaveResponses = true
			o.OutputDir = "  "
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := webhooktest.Options{Host: "127.0.0.1", Log: &bytes.Buffer{}}
			auth := testAuth()
			tc.tweak(&opts, &auth)
			if _, err := webhooktest.New(opts, auth); err == nil {
				t.Fatal("New accepted a configuration it cannot serve")
			}
		})
	}
}

func TestTheServerBindsThePortItWasGivenAndStopsOnItsContext(t *testing.T) {
	port := freePort(t)
	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.Port = port
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.srv.Run(ctx) }()

	waitFor(t, fmt.Sprintf("127.0.0.1:%d", port))
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/webhook/none", port),
		"application/json", strings.NewReader(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver did not stop when its context ended")
	}
}

func TestTheDefaultBindingIsLoopback(t *testing.T) {
	srv, err := webhooktest.New(webhooktest.Options{Log: &bytes.Buffer{}}, testAuth())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if srv.ListenAddr() != "127.0.0.1:8091" {
		t.Fatalf("ListenAddr() = %q", srv.ListenAddr())
	}
}

func TestAnUnwritablePortIsReportedClearly(t *testing.T) {
	// A port the process may not bind: the message has to say which address.
	blocked := freePort(t)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", blocked))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	h := newHarness(t, func(o *webhooktest.Options, _ *webhooktest.Auth) {
		o.Port = blocked
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = h.srv.Run(ctx)
	if err == nil {
		t.Fatal("Run bound a port already in use")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(blocked)) {
		t.Fatalf("Run error %q does not name the port", err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func waitFor(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing answered on %s", addr)
}
