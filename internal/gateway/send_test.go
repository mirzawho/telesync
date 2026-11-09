package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/sending"
	"github.com/mirza/telesync/internal/staging"
)

// A direct send is accepted by an HTTP request and performed long after it has
// been answered. These tests are about the first half of that: what a caller
// must send, what they are refused for, and what the answer does and does not
// claim. Nothing here is allowed to send to Telegram during the request.

// recordingJobs is a queue that remembers what it was asked to schedule.
type recordingJobs struct {
	enqueued []string
	fail     error
}

func (j *recordingJobs) EnqueueMessageSend(_ context.Context, operationID string, _ int64) error {
	if j.fail != nil {
		return j.fail
	}
	j.enqueued = append(j.enqueued, operationID)
	return nil
}

// sendSuite is a gateway with the real sending service behind it: a real store,
// a real staging area and the real operation table. The only thing replaced is
// the queue's reach into the database, so a test can see what was scheduled.
type sendSuite struct {
	*accountEnv
	worker    *fakeWorker
	sender    *sending.Service
	jobs      *recordingJobs
	staging   *staging.Staging
	sendCalls int
}

// sendEnv builds the suite around the connection "cat", which is the route the
// API examples use.
func sendEnv(t *testing.T, mutate ...func(*gateway.Config)) *sendSuite {
	t.Helper()
	worker := readyWorker()
	worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Type: "channel", Title: "Destination",
		IsMember: true, Permissions: rpc.Permissions{New: true, Read: true},
	}}
	worker.send = rpc.SendResult{
		Message:   &rpc.Message{ID: 12345, Text: "outgoing"},
		ChannelID: "98",
	}

	env := newAccountEnv(t, registry{"cat": worker}, mutate...)
	env.signedIn = true

	area, err := staging.Open(filepath.Join(t.TempDir(), "send-staging"), 1<<20)
	if err != nil {
		t.Fatalf("staging.Open: %v", err)
	}
	jobs := &recordingJobs{}
	sender := &sending.Service{
		Sender: &publishing.Service{
			Workers: publishing.RegistryFunc(func(key string) (publishing.Worker, bool) {
				if key != "cat" {
					return nil, false
				}
				return worker, true
			}),
			Channels:  env.store,
			Publishes: env.store,
			Actors:    env.store,
		},
		Operations:       env.store,
		Staging:          area,
		Jobs:             jobs,
		HistoryRetention: 30 * 24 * time.Hour,
		Logf:             t.Logf,
	}

	suite := &sendSuite{accountEnv: env, worker: worker, sender: sender, jobs: jobs, staging: area}
	// The handler is rebuilt with the send service in place, the same way the
	// daemon wires it.
	env.started.Send = sender
	env.handler = gateway.New(env.started)
	return suite
}

// grantSendPublish gives the caller the right to publish.
func (s *sendSuite) grantSendPublish(t *testing.T) {
	t.Helper()
	grantPublish(t, s.accountEnv, s.defaults)
}

const sendPath = "/api/v1/telegram/cat/channels/98/messages/send"

func jsonSend(text string) string {
	return fmt.Sprintf(`{"text": %q, "entities": [], "options": {"silent": false, "disable_web_page_preview": false}}`, text)
}

// multipartPart is one section of a multipart send.
type multipartPart struct {
	name        string
	filename    string
	contentType string
	value       string
}

// multipartSend builds a multipart body and its content type. The parts go in
// the order they are given, so a test can put the file first as well as last.
func multipartSend(t *testing.T, parts []multipartPart) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name=%q; filename=%q`, p.name, p.filename))
		if p.contentType != "" {
			header.Set("Content-Type", p.contentType)
		}
		w, err := mw.CreatePart(header)
		if err != nil {
			t.Fatalf("CreatePart: %v", err)
		}
		if _, err := w.Write([]byte(p.value)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.String(), mw.FormDataContentType()
}

// postSend runs one send request through the handler.
func (s *sendSuite) postSend(t *testing.T, contentType, body string, headers ...string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, sendPath, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+s.session)
	}
	if s.signedIn {
		req.Header.Set("Authorization", "Bearer "+s.defaults.token)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Result()
}

// postJSON sends the text-only shape.
func (s *sendSuite) postJSON(t *testing.T, body string, headers ...string) *http.Response {
	t.Helper()
	return s.postSend(t, "application/json", body, headers...)
}

// --- accepting ---------------------------------------------------------

// Test A. A text-only message is accepted and scheduled. The answer is
// 202 with an operation to poll and a place to poll it from.
func TestATextOnlySendIsAcceptedForBackgroundDelivery(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	resp := s.postJSON(t, jsonSend("hello"), "Idempotency-Key", "hello-001")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get("Location"); got == "" {
		t.Fatal("the answer carries no Location for the operation it created")
	}

	body := decode[envelope](t, resp)
	var accepted struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
		AcceptedAt  string `json:"accepted_at"`
		StatusURL   string `json:"status_url"`
		Destination struct {
			ClientKey string `json:"client_key"`
			ChannelID string `json:"channel_id"`
		} `json:"destination"`
	}
	if err := json.Unmarshal(body.Data, &accepted); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if accepted.OperationID == "" {
		t.Fatal("no operation id was returned; the caller has nothing to poll")
	}
	if accepted.Status != "queued" {
		t.Fatalf("status = %q, want %q — nothing has been sent yet", accepted.Status, "queued")
	}
	if accepted.Destination.ClientKey != "cat" || accepted.Destination.ChannelID != "98" {
		t.Fatalf("destination = %+v, want cat/98", accepted.Destination)
	}
	if accepted.AcceptedAt == "" {
		t.Fatal("the answer carries no acceptance time")
	}
	if !strings.HasSuffix(accepted.StatusURL, "/messages/send/"+accepted.OperationID) {
		t.Fatalf("status_url = %q, it does not name this operation", accepted.StatusURL)
	}
	if s.worker.sendCalls != 0 {
		t.Fatalf("%d messages were sent during the request", s.worker.sendCalls)
	}
	if len(s.jobs.enqueued) != 1 {
		t.Fatalf("%d jobs were scheduled, want 1", len(s.jobs.enqueued))
	}
}

// Test B. A request without a credential is refused, and so is one for an
// account with no publishing permission.
func TestBSendNeedsACredentialAndThePublishingPermission(t *testing.T) {
	s := sendEnv(t)

	req := httptest.NewRequest(http.MethodPost, sendPath, strings.NewReader(jsonSend("hello")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	if rec.Result().StatusCode != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated send got %d, want 401", rec.Result().StatusCode)
	}

	plain := s.account(t, "plain")
	resp := s.send(t, plain.token, http.MethodPost, sendPath, jsonSend("hello"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if code := errorCode(t, resp); code != gateway.CodePublishForbidden {
		t.Fatalf("code = %q, want %q", code, gateway.CodePublishForbidden)
	}
}

// Test C. A connection that does not exist is a 404, never another client's.
func TestCAnUnknownConnectionIsNotFound(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	resp := s.send(t, defaultCaller, http.MethodPost, "/api/v1/telegram/no-such/channels/98/messages/send", jsonSend("hello"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if code := errorCode(t, resp); code != gateway.CodeUnknownClient {
		t.Fatalf("code = %q, want %q", code, gateway.CodeUnknownClient)
	}
}

// Test D. The route needs no source message. There is no field for one, and
// none is required for the send to be accepted.
func TestDNoSourceMessageIsNeeded(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	// A body that is only text, options and media. If any source field were
	// required it would be refused here, and it is not.
	resp := s.postJSON(t, `{"text":"hello","entities":[],"options":{"silent":false,"disable_web_page_preview":false}}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}
}

// Test E. A media-only message is a message. An empty caption beside a valid
// attachment is exactly what a recording is.
func TestEAMediaOnlyMessageIsAccepted(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	body, contentType := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json",
			value: `{"text":"","media":{"kind":"voice","duration_seconds":5},"options":{"silent":false}}`},
		{name: "file", filename: "note.ogg", contentType: "audio/ogg", value: "OggS\x00\x02opus-recording"},
	})
	resp := s.postSend(t, contentType, body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}
}

// Test F. Neither text nor an attachment is not a message.
func TestFNothingToSendIsRefused(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	resp := s.postJSON(t, jsonSend("   "))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if len(s.jobs.enqueued) != 0 {
		t.Fatal("a refused request was still scheduled")
	}
}

// --- multipart ---------------------------------------------------------

// Test G. A photograph goes up as a photograph, with its caption.
func TestGAPhotographIsStagedAndScheduled(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	jpeg := "\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00"
	body, contentType := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json",
			value: `{"text":"a picture","media":{"kind":"photo"}}`},
		{name: "file", filename: "picture.jpg", contentType: "image/jpeg", value: jpeg},
	})
	resp := s.postSend(t, contentType, body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}
	names, err := s.staging.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("%d staged attachments, want 1", len(names))
	}
}

// Test H. A recording is accepted as a recording and is never relabelled into
// a document on the way in.
func TestHARecordingIsAcceptedAsVoice(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	body, contentType := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json",
			value: `{"text":"","media":{"kind":"voice","duration_seconds":5}}`},
		{name: "file", filename: "note.ogg", contentType: "audio/ogg", value: "OggS\x00\x02opus"},
	})
	resp := s.postSend(t, contentType, body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}

	// Performing it is what proves the kind survived.
	var accepted struct {
		OperationID string `json:"operation_id"`
	}
	decodeData(t, resp, &accepted)
	if err := s.sender.Execute(context.Background(), automation.Job{EventID: accepted.OperationID, Attempt: 1}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if s.worker.sendCalls != 1 {
		t.Fatalf("%d sends, want 1", s.worker.sendCalls)
	}
	media := s.worker.sentReq.Media
	if media == nil || media.Kind != "voice" {
		t.Fatalf("the recording was sent as %+v, not as a voice message", media)
	}
	if media.Duration != 5 {
		t.Fatalf("duration = %d, want the length the caller gave", media.Duration)
	}
}

// Test I. The file may arrive before the payload. A multipart body is a
// sequence of parts and a client is free to choose the order.
func TestITheFileMayArriveBeforeThePayload(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	body, contentType := multipartSend(t, []multipartPart{
		{name: "file", filename: "report.pdf", contentType: "application/pdf", value: "%PDF-1.7\n"},
		{name: "payload", contentType: "application/json",
			value: `{"text":"a report","media":{"kind":"document"}}`},
	})
	resp := s.postSend(t, contentType, body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}
	names, err := s.staging.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("%d staged attachments, want 1", len(names))
	}
}

// Test J. A photograph is a photograph. A PDF offered as one is refused, and
// it is not quietly sent as a document instead.
func TestJAnIncompatibleFileIsRefusedNotRelabelled(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	body, contentType := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json", value: `{"text":"x","media":{"kind":"photo"}}`},
		{name: "file", filename: "report.pdf", contentType: "application/pdf", value: "%PDF-1.7\n"},
	})
	resp := s.postSend(t, contentType, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if code := errorCode(t, resp); code != "invalid_media_kind" {
		t.Fatalf("code = %q, want %q", code, "invalid_media_kind")
	}
	if names, _ := s.staging.Names(); len(names) != 0 {
		t.Fatalf("a refused upload left %d file(s) behind", len(names))
	}
}

// Test K. The payload is required and so is the file when it is named. Both
// mistakes get their own answer.
func TestKAPayloadAndItsFileAreEachRequired(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	noPayload, ct := multipartSend(t, []multipartPart{
		{name: "file", filename: "a.txt", contentType: "text/plain", value: "hello"},
	})
	if resp := s.postSend(t, ct, noPayload); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a body with no payload got %d, want 400", resp.StatusCode)
	}

	noFile, ct := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json", value: `{"text":"x","media":{"kind":"document"}}`},
	})
	if resp := s.postSend(t, ct, noFile); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a media request with no file got %d, want 400", resp.StatusCode)
	}
}

// Test L. One attachment per send. A second file part is refused, never
// silently dropped and never quietly preferred over the first.
func TestLMoreThanOneAttachmentIsRefused(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	body, contentType := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json", value: `{"text":"x","media":{"kind":"document"}}`},
		{name: "file", filename: "a.txt", contentType: "text/plain", value: "one"},
		{name: "file", filename: "b.txt", contentType: "text/plain", value: "two"},
	})
	resp := s.postSend(t, contentType, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if names, _ := s.staging.Names(); len(names) != 0 {
		t.Fatalf("a refused upload left %d file(s) behind", len(names))
	}
}

// Test M. Malformed payload JSON is refused as itself, with nothing staged and
// nothing scheduled.
func TestMMalformedPayloadIsRefused(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	body, contentType := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json", value: `{"text":`},
		{name: "file", filename: "a.txt", contentType: "text/plain", value: "one"},
	})
	resp := s.postSend(t, contentType, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if names, _ := s.staging.Names(); len(names) != 0 {
		t.Fatalf("a refused upload left %d file(s) behind", len(names))
	}
	if len(s.jobs.enqueued) != 0 {
		t.Fatal("a refused request was still scheduled")
	}
}

// Test N. A media kind that does not exist is refused. An unsupported kind is
// never sent as something else.
func TestNAnUnsupportedMediaKindIsRefused(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	body, contentType := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json", value: `{"text":"x","media":{"kind":"sticker"}}`},
		{name: "file", filename: "a.webp", contentType: "image/webp", value: "RIFF\x00\x00\x00\x00WEBP"},
	})
	resp := s.postSend(t, contentType, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, readBody(t, resp))
	}
}

// Test O. The body is application/json or multipart/form-data. Anything else
// is 415 and is not read.
func TestOAnUnsupportedContentTypeIsRefused(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	resp := s.postSend(t, "text/plain", jsonSend("hello"))
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (%s)", resp.StatusCode, readBody(t, resp))
	}
}

// --- idempotency -------------------------------------------------------

// Test P. The same request under the same key is the same send. It is answered
// with the operation that already exists and nothing is scheduled twice.
func TestPTheSameKeyAndRequestIsTheSameSend(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	first := s.postJSON(t, jsonSend("hello"), "Idempotency-Key", "hello-001")
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", first.StatusCode)
	}
	second := s.postJSON(t, jsonSend("hello"), "Idempotency-Key", "hello-001")
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", second.StatusCode, readBody(t, second))
	}
	if second.Header.Get("Idempotent-Replay") != "true" {
		t.Fatal("the repeated request was not marked as a replay")
	}

	var a, b struct {
		OperationID string `json:"operation_id"`
	}
	decodeData(t, first, &a)
	decodeData(t, second, &b)
	if a.OperationID != b.OperationID {
		t.Fatalf("the repeat is operation %q, not %q", b.OperationID, a.OperationID)
	}
	if len(s.jobs.enqueued) != 1 {
		t.Fatalf("%d jobs were scheduled, want 1 — a repeated request scheduled another send", len(s.jobs.enqueued))
	}
}

// Test Q. The same key naming something else is a conflict. Two different
// messages under one key is a mistake on the caller's side and is said so.
func TestQTheSameKeyForADifferentRequestConflicts(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	if resp := s.postJSON(t, jsonSend("one"), "Idempotency-Key", "dup-001"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	resp := s.postJSON(t, jsonSend("two"), "Idempotency-Key", "dup-001")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if code := errorCode(t, resp); code != "publish_idempotency_conflict" {
		t.Fatalf("code = %q, want %q", code, "publish_idempotency_conflict")
	}
}

// Test R. Two files with the same name are two requests. The name is not what
// identifies an attachment; the bytes are.
func TestRTheSameFileNameWithDifferentBytesIsADifferentRequest(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	one, ct := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json", value: `{"text":"x","media":{"kind":"document"}}`},
		{name: "file", filename: "report.pdf", contentType: "application/pdf", value: "%PDF-1.7 one"},
	})
	if resp := s.postSend(t, ct, one, "Idempotency-Key", "file-001"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	two, ct := multipartSend(t, []multipartPart{
		{name: "payload", contentType: "application/json", value: `{"text":"x","media":{"kind":"document"}}`},
		{name: "file", filename: "report.pdf", contentType: "application/pdf", value: "%PDF-1.7 two"},
	})
	resp := s.postSend(t, ct, two, "Idempotency-Key", "file-001")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, readBody(t, resp))
	}
}

// --- status ------------------------------------------------------------

// Test S. Polling reads the operation and sends nothing.
func TestSPollingReportsTheOperationAndSendsNothing(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	posted := s.postJSON(t, jsonSend("hello"))
	var accepted struct {
		OperationID string `json:"operation_id"`
	}
	decodeData(t, posted, &accepted)

	resp := s.send(t, defaultCaller, http.MethodGet, sendPath+"/"+accepted.OperationID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, readBody(t, resp))
	}
	var view struct {
		OperationID  string `json:"operation_id"`
		Status       string `json:"status"`
		AttemptCount int    `json:"attempt_count"`
		Result       *struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	decodeData(t, resp, &view)
	if view.OperationID != accepted.OperationID {
		t.Fatalf("polled %q, want %q", view.OperationID, accepted.OperationID)
	}
	if view.Status != "queued" {
		t.Fatalf("status = %q, want %q", view.Status, "queued")
	}
	if view.Result != nil {
		t.Fatal("a queued send already reports a destination message")
	}
	if s.worker.sendCalls != 0 {
		t.Fatal("polling sent a message")
	}
}

// Test T. A completed send reports the message the destination produced.
func TestTACompletedSendReportsItsDestinationMessage(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	var accepted struct {
		OperationID string `json:"operation_id"`
	}
	decodeData(t, s.postJSON(t, jsonSend("hello")), &accepted)
	if err := s.sender.Execute(context.Background(), automation.Job{EventID: accepted.OperationID, Attempt: 1}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	resp := s.send(t, defaultCaller, http.MethodGet, sendPath+"/"+accepted.OperationID, "")
	var view struct {
		Status string `json:"status"`
		Result *struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	decodeData(t, resp, &view)
	if view.Status != "sent" {
		t.Fatalf("status = %q, want %q", view.Status, "sent")
	}
	if view.Result == nil || view.Result.MessageID != 12345 {
		t.Fatalf("result = %+v, want the message the destination confirmed", view.Result)
	}
}

// Test U. An operation belongs to its own account and its own channel. Neither
// is reachable by naming an id.
func TestUAnOperationBelongsToItsAccountAndChannel(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	var accepted struct {
		OperationID string `json:"operation_id"`
	}
	decodeData(t, s.postJSON(t, jsonSend("hello")), &accepted)

	other := s.account(t, "other-admin", channelstore.GlobalPermissions()...)
	resp := s.send(t, other.token, http.MethodGet, sendPath+"/"+accepted.OperationID, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("another account read an operation with %d, want 404", resp.StatusCode)
	}

	resp = s.send(t, defaultCaller, http.MethodGet, "/api/v1/telegram/cat/channels/99/messages/send/"+accepted.OperationID, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an operation was read back under another channel with %d, want 404", resp.StatusCode)
	}
}

// Test V. An unknown operation is a 404 and nothing else.
func TestVAnUnknownOperationIsNotFound(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	resp := s.send(t, defaultCaller, http.MethodGet, sendPath+"/send_00000000000000000000000000000000", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// --- routing -----------------------------------------------------------

// Test W. The route is under the channel's messages and is not a message whose
// id happens to be "send".
func TestWTheSendRouteIsNotAMessageID(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)

	resp := s.send(t, defaultCaller, http.MethodGet, sendPath, "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", allow, http.MethodPost)
	}

	// The read-only companion is not a message either: it answers as itself.
	var accepted struct {
		OperationID string `json:"operation_id"`
	}
	decodeData(t, s.postJSON(t, jsonSend("hello")), &accepted)
	if resp := s.send(t, defaultCaller, http.MethodGet, sendPath+"/"+accepted.OperationID, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("the status route answered %d, want 200", resp.StatusCode)
	}
}

// Test X. When nothing can be recorded or scheduled, the request is refused
// rather than answered with an operation nobody will run.
func TestXAnUnschedulableSendIsRefused(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)
	s.jobs.fail = fmt.Errorf("the queue is not available")

	resp := s.postJSON(t, jsonSend("hello"))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if code := errorCode(t, resp); code != sending.CodeSendQueueUnavailable {
		t.Fatalf("code = %q, want %q", code, sending.CodeSendQueueUnavailable)
	}
}

// Test Y. A send that is refused before it is accepted leaves no operation
// behind to confuse a later request with the same key.
func TestYAFailedAcceptanceLeavesNothingBehind(t *testing.T) {
	s := sendEnv(t)
	s.grantSendPublish(t)
	s.jobs.fail = fmt.Errorf("the queue is not available")

	if resp := s.postJSON(t, jsonSend("hello"), "Idempotency-Key", "once-001"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	s.jobs.fail = nil
	resp := s.postJSON(t, jsonSend("hello"), "Idempotency-Key", "once-001")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("a retry after an unschedulable request got %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}
}

// --- helpers -----------------------------------------------------------

func decodeData(t *testing.T, resp *http.Response, into any) {
	t.Helper()
	defer resp.Body.Close()
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := json.Unmarshal(out.Data, into); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}
