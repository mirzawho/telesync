package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

func TestDeleteTelegramMessageReturnsRemoteDeletionConfirmation(t *testing.T) {
	worker := readyWorker()
	worker.deleteResult = rpc.DeleteMessageResult{Deleted: true}
	worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Type: "channel", Title: "Destination", IsMember: true,
		Permissions: rpc.Permissions{Delete: true, Read: true},
	}}
	official := readyWorker()
	env := newAccountEnv(t, registry{"cat": worker, "official": official})
	env.channelRow(t, "cat", 98)
	env.signedIn = true
	resp := env.send(t, defaultCaller, http.MethodDelete, "/api/v1/telegram/cat/channels/98/messages/11", "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}
	defer resp.Body.Close()
	var answer struct {
		Data struct {
			Deleted    bool   `json:"deleted"`
			ChannelID  string `json:"channel_id"`
			MessageID  int    `json:"message_id"`
			Connection struct {
				ClientKey string `json:"client_key"`
			} `json:"connection"`
		} `json:"data"`
		Meta struct {
			RequestID   string `json:"request_id"`
			GeneratedAt string `json:"generated_at"`
		} `json:"meta"`
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var shape struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("decode response shape: %v", err)
	}
	if !answer.Data.Deleted || answer.Data.ChannelID != "98" || answer.Data.MessageID != 11 || answer.Data.Connection.ClientKey != "cat" {
		t.Fatalf("data = %+v, want confirmed deletion of cat/98/11", answer.Data)
	}
	if _, ok := shape.Data["account_id"]; ok {
		t.Fatalf("legacy keyed delete response gained account_id: %s", raw)
	}
	if answer.Meta.RequestID == "" || answer.Meta.GeneratedAt == "" {
		t.Fatalf("meta = %+v, want request id and generated_at", answer.Meta)
	}
	if worker.deleteCalls != 1 || worker.deleteReq != (rpc.DeleteMessageRequest{ChannelID: 98, MessageID: 11}) {
		t.Fatalf("selected worker delete = calls %d request %+v", worker.deleteCalls, worker.deleteReq)
	}
	if official.deleteCalls != 0 {
		t.Fatalf("unselected worker delete calls = %d, want 0", official.deleteCalls)
	}
}

func TestAccountScopedDeleteResponseNamesTheAccountAndKeepsTheServerKeyInformational(t *testing.T) {
	worker := readyWorker()
	worker.deleteResult = rpc.DeleteMessageResult{Deleted: true}
	worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Type: "channel", Title: "Destination", IsMember: true,
		Permissions: rpc.Permissions{Delete: true, Read: true},
	}}
	env := newAccountEnv(t, registry{"cat": worker})
	env.channelRow(t, "cat", 98)
	env.signedIn = true
	accountID := env.testEnv.account["cat"].ID

	resp := env.send(t, defaultCaller, http.MethodDelete,
		"/api/v1/telegram/accounts/"+accountID+"/channels/98/messages/11", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var answer struct {
		Data struct {
			Deleted    bool   `json:"deleted"`
			ChannelID  string `json:"channel_id"`
			MessageID  int    `json:"message_id"`
			AccountID  string `json:"account_id"`
			Connection struct {
				ClientKey string `json:"client_key"`
			} `json:"connection"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	if !answer.Data.Deleted || answer.Data.ChannelID != "98" || answer.Data.MessageID != 11 ||
		answer.Data.AccountID != accountID || answer.Data.Connection.ClientKey != "cat" {
		t.Fatalf("data = %+v, want account %s and informational server key cat",
			answer.Data, accountID)
	}
}

func TestDeleteTelegramMessageRouteAllowsSupportedMethods(t *testing.T) {
	env := newEnv(t, registry{"cat": readyWorker()})
	resp := env.do(t, http.MethodPost, "/api/v1/telegram/cat/channels/98/messages/11")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if got := resp.Header.Get("Allow"); got != "GET, PUT, DELETE" {
		t.Fatalf("Allow = %q, want GET, PUT and DELETE on the shared message route", got)
	}
}

func TestDeleteTelegramMessageRequiresAuthentication(t *testing.T) {
	env := newEnv(t, registry{"cat": readyWorker()})
	req := newRawRequest(http.MethodDelete, "/api/v1/telegram/cat/channels/98/messages/11", "")
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestDeleteTelegramMessageValidatesTheMessageID(t *testing.T) {
	env := newEnv(t, registry{"cat": readyWorker()})
	for _, id := range []string{"0", "-1", "nope", "9223372036854775808"} {
		t.Run(id, func(t *testing.T) {
			resp := env.do(t, http.MethodDelete, "/api/v1/telegram/cat/channels/98/messages/"+id)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
			body := decode[errorEnvelope](t, resp)
			if body.Error.Code != "invalid_input" {
				t.Fatalf("error code = %q, want invalid_input", body.Error.Code)
			}
		})
	}
}

func TestDeleteTelegramMessageValidatesTheChannelID(t *testing.T) {
	env := newEnv(t, registry{"cat": readyWorker()})
	for _, id := range []string{"0", "-1", "nope", "9223372036854775808"} {
		t.Run(id, func(t *testing.T) {
			resp := env.do(t, http.MethodDelete, "/api/v1/telegram/cat/channels/"+id+"/messages/11")
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestDeleteTelegramMessageRequiresApplicationChannelDeletePermission(t *testing.T) {
	worker := readyWorker()
	env := newAccountEnv(t, registry{"cat": worker})
	channel := env.channelRow(t, "cat", 98)
	// Exercise the permission gate through the account that owns the selected
	// Telegram session. A different user with no account on this server is
	// rejected earlier by account ownership and cannot isolate channel rights.
	reader := env.defaults
	if err := env.store.ReplacePermissions(context.Background(), reader.user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionChannelsView},
	}); err != nil {
		t.Fatal(err)
	}
	env.grantChannels(t, reader, channelstore.ResourcePermissions{ResourceID: channel.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
	}})
	resp := env.send(t, reader.token, http.MethodDelete, "/api/v1/telegram/cat/channels/98/messages/11", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	if worker.deleteCalls != 0 {
		t.Fatalf("worker delete calls = %d, want 0", worker.deleteCalls)
	}
}
