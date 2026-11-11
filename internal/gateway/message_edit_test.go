package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/editing"
	"github.com/mirza/telesync/internal/gateway"
)

const editPath = "/api/v1/telegram/cat/channels/98/messages/11"

type recordingEditJobs struct {
	enqueued []string
	fail     error
}

func (j *recordingEditJobs) EnqueueMessageEdit(_ context.Context, operationID string, _ int64) error {
	if j.fail != nil {
		return j.fail
	}
	j.enqueued = append(j.enqueued, operationID)
	return nil
}

func editEnv(t *testing.T) (*sendSuite, *recordingEditJobs) {
	t.Helper()
	s := sendEnv(t)
	jobs := &recordingEditJobs{}
	publisher, ok := s.sender.Sender.(*publishing.Service)
	if !ok {
		t.Fatalf("direct sender = %T, want *publishing.Service", s.sender.Sender)
	}
	s.accountEnv.started.Edit = &editing.Service{
		Sender: publisher, Channels: s.accountEnv.store, Operations: s.accountEnv.store,
		Staging: s.staging, Jobs: jobs, HistoryRetention: 30 * 24 * time.Hour,
	}
	s.accountEnv.handler = gateway.New(s.accountEnv.started)
	return s, jobs
}

func grantMessageEdit(t *testing.T, s *sendSuite, includeUpdate bool) {
	t.Helper()
	channel := s.channelRow(t, "cat", 98)
	permissions := []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: channelstore.EffectAllow}}
	if includeUpdate {
		permissions = append(permissions, channelstore.ResourcePermission{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow})
	}
	s.grantChannels(t, s.defaults, channelstore.ResourcePermissions{ResourceID: channel.ID, Permissions: permissions})
}

func TestMessageEditPUTIsAcceptedAsynchronously(t *testing.T) {
	s, jobs := editEnv(t)
	grantMessageEdit(t, s, true)

	resp := s.send(t, defaultCaller, http.MethodPut, editPath,
		`{"text":"replacement","entities":[],"media":null,"options":{"disable_web_page_preview":false}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if resp.Header.Get("Location") != "/api/v1/telegram/cat/channels/98/messages/11/edit/"+mustOperationID(t, resp) {
		t.Fatalf("Location = %q, want the target's edit-operation status URL", resp.Header.Get("Location"))
	}
	if s.worker.sendCalls != 0 {
		t.Fatalf("the HTTP request made %d send calls; editing must stay asynchronous and keep the message ID", s.worker.sendCalls)
	}
	if s.worker.calls != 0 || s.worker.editCalls != 0 {
		t.Fatalf("the HTTP request made %d upstream worker calls (%d edits); remote target checks belong in the background", s.worker.calls, s.worker.editCalls)
	}
	if len(jobs.enqueued) != 1 {
		t.Fatalf("edit jobs enqueued = %d, want 1", len(jobs.enqueued))
	}
}

func TestMessageEditRequiresChannelUpdatePermission(t *testing.T) {
	s, jobs := editEnv(t)
	channel := s.channelRow(t, "cat", 98)
	reader := s.account(t, "reader", channelstore.PermissionChannelsView)
	s.grantChannels(t, reader, channelstore.ResourcePermissions{ResourceID: channel.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
	}})

	resp := s.send(t, reader.token, http.MethodPut, editPath,
		`{"text":"replacement","entities":[],"media":null}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if code := errorCode(t, resp); code != gateway.CodeMessageEditForbidden {
		t.Fatalf("error code = %q, want %q", code, gateway.CodeMessageEditForbidden)
	}
	if len(jobs.enqueued) != 0 || s.worker.sendCalls != 0 {
		t.Fatalf("denied edit enqueued %d jobs and made %d sends", len(jobs.enqueued), s.worker.sendCalls)
	}
}

func TestMessageEditValidatesTheTargetID(t *testing.T) {
	s, _ := editEnv(t)
	grantMessageEdit(t, s, true)
	for _, id := range []string{"0", "-1", "bad", "2147483648", "9223372036854775808"} {
		t.Run(id, func(t *testing.T) {
			resp := s.send(t, defaultCaller, http.MethodPut,
				"/api/v1/telegram/cat/channels/98/messages/"+id,
				`{"text":"replacement","entities":[],"media":null}`)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, readBody(t, resp))
			}
			if code := errorCode(t, resp); code != gateway.CodeInvalidInput {
				t.Fatalf("error code = %q, want %q", code, gateway.CodeInvalidInput)
			}
		})
	}
}

func TestMessageEditRequiresAuthentication(t *testing.T) {
	s, jobs := editEnv(t)
	resp := s.sendAnon(t, http.MethodPut, editPath)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if len(jobs.enqueued) != 0 {
		t.Fatalf("unauthenticated request enqueued %d edits", len(jobs.enqueued))
	}
}

func TestMessageEditRejectsUnsupportedMethods(t *testing.T) {
	s, _ := editEnv(t)
	grantMessageEdit(t, s, true)
	resp := s.send(t, defaultCaller, http.MethodPatch, editPath, `{"text":"replacement"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get("Allow"); got != "GET, PUT, DELETE" {
		t.Fatalf("Allow = %q, want all methods on the message resource", got)
	}
}

func TestMessageEditRequiresTheTextField(t *testing.T) {
	s, jobs := editEnv(t)
	grantMessageEdit(t, s, true)
	resp := s.send(t, defaultCaller, http.MethodPut, editPath, `{"entities":[],"media":null}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, readBody(t, resp))
	}
	if len(jobs.enqueued) != 0 {
		t.Fatalf("missing text enqueued %d edit jobs", len(jobs.enqueued))
	}
}

func TestMessageEditMultipartStagesOneReplacementFile(t *testing.T) {
	s, jobs := editEnv(t)
	grantMessageEdit(t, s, true)
	jpeg := "\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00"
	body, contentType := multipartSend(t, []multipartPart{
		{name: "payload", value: `{"text":"new caption","entities":[],"media":{"kind":"photo"}}`},
		{name: "file", filename: "new.jpg", contentType: "image/jpeg", value: jpeg},
	})
	resp := s.sendBody(t, defaultCaller, http.MethodPut, editPath, contentType, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", resp.StatusCode, readBody(t, resp))
	}
	var accepted struct {
		Data struct {
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Data.OperationID == "" || len(jobs.enqueued) != 1 {
		t.Fatalf("operation = %q, enqueued jobs = %d; want accepted and scheduled", accepted.Data.OperationID, len(jobs.enqueued))
	}
	if s.worker.sendCalls != 0 {
		t.Fatalf("multipart request made %d send calls", s.worker.sendCalls)
	}
	if s.worker.calls != 0 || s.worker.editCalls != 0 {
		t.Fatalf("multipart request made %d upstream worker calls (%d edits)", s.worker.calls, s.worker.editCalls)
	}
}

func TestMessageEditUnknownConnectionDoesNotFallBack(t *testing.T) {
	s, _ := editEnv(t)
	grantMessageEdit(t, s, true)
	resp := s.send(t, defaultCaller, http.MethodPut,
		"/api/v1/telegram/missing/channels/98/messages/11",
		`{"text":"replacement","entities":[],"media":null}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", resp.StatusCode, readBody(t, resp))
	}
}

func TestMessageEditStatusIsReadOnlyAndScopedToItsTarget(t *testing.T) {
	s, _ := editEnv(t)
	grantMessageEdit(t, s, true)
	resp := s.send(t, defaultCaller, http.MethodPut, editPath,
		`{"text":"replacement","entities":[],"media":null}`)
	var accepted struct {
		Data struct {
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusAccepted {
		resp.Body.Close()
		t.Fatalf("accept status = %d, want 202", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	statusPath := fmt.Sprintf("%s/edit/%s", editPath, accepted.Data.OperationID)
	resp = s.send(t, defaultCaller, http.MethodGet, statusPath, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status poll = %d, want 200 (%s)", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	wrongTarget := fmt.Sprintf("/api/v1/telegram/cat/channels/99/messages/11/edit/%s", accepted.Data.OperationID)
	resp = s.send(t, defaultCaller, http.MethodGet, wrongTarget, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status under another channel = %d, want 404 (%s)", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	resp = s.send(t, defaultCaller, http.MethodPut, statusPath, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodGet {
		t.Fatalf("write to status resource = %d Allow %q, want 405 GET", resp.StatusCode, resp.Header.Get("Allow"))
	}
	if s.worker.sendCalls != 0 {
		t.Fatalf("polling caused %d Telegram sends", s.worker.sendCalls)
	}
}

func mustOperationID(t *testing.T, resp *http.Response) string {
	t.Helper()
	var answer struct {
		Data struct {
			OperationID string `json:"operation_id"`
			Status      string `json:"status"`
			Target      struct {
				ClientKey string `json:"client_key"`
				ChannelID string `json:"channel_id"`
				MessageID int    `json:"message_id"`
			} `json:"target"`
			StatusURL string `json:"status_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatalf("decode accepted edit: %v", err)
	}
	if answer.Data.OperationID == "" || answer.Data.Status != "queued" {
		t.Fatalf("accepted edit = %+v, want an operation id in queued state", answer.Data)
	}
	if answer.Data.Target.ClientKey != "cat" || answer.Data.Target.ChannelID != "98" || answer.Data.Target.MessageID != 11 {
		t.Fatalf("target = %+v, want cat/98/11", answer.Data.Target)
	}
	want := "/api/v1/telegram/cat/channels/98/messages/11/edit/" + answer.Data.OperationID
	if answer.Data.StatusURL != want {
		t.Fatalf("status_url = %q, want %q", answer.Data.StatusURL, want)
	}
	return answer.Data.OperationID
}
