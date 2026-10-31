package gateway_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

func serverEnv(t *testing.T) *accountEnv {
	t.Helper()
	worker := readyWorker()
	return newAccountEnv(t, registry{"official": worker}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{{Key: "official", Type: "official", Enabled: true, Registered: true}}
	})
}

func TestServerListIsBehindTheServersViewPermission(t *testing.T) {
	env := serverEnv(t)
	reader := env.account(t, "channel-reader", channelstore.PermissionChannelsView)

	resp := env.send(t, reader.token, http.MethodGet, "/api/v1/telegram", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 without servers.view", resp.StatusCode)
	}
	var body errorEnvelope
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.Error.Code != "forbidden" {
		t.Fatalf("code = %q", body.Error.Code)
	}

	holder := env.account(t, "server-watcher", channelstore.PermissionServersView)
	resp = env.send(t, holder.token, http.MethodGet, "/api/v1/telegram", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with servers.view", resp.StatusCode)
	}
	var list serverList
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Data.Servers) != 1 || list.Data.Servers[0].Key != "official" {
		t.Fatalf("servers = %+v", list.Data.Servers)
	}
}

func TestServerListByPermissionNeverNamesConfiguredSecrets(t *testing.T) {
	worker := readyWorker()
	env := newAccountEnv(t, registry{"official": worker}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{{Key: "official", Type: "official", Enabled: true, Registered: true}}
	})

	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/telegram", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	text := readAll(t, resp)
	for _, sentinel := range []string{"mtproto.example.invalid", "settings", "app_hash", "session", "phone"} {
		if strings.Contains(text, sentinel) {
			t.Fatalf("the listing names %q: %s", sentinel, text)
		}
	}
	// A healthy worker's last error is the worker's own sentence, not a
	// configuration leak, so it may appear; the row's field set is checked
	// against the route's own type, not this text.
}

func TestServerListReportsTheAccountsIdentity(t *testing.T) {
	worker := readyWorker()
	worker.profile.User = &rpc.Account{ID: 42, FirstName: "Ada", LastName: "Lovelace", Username: "ada"}
	worker.profile.ThisDC = 2
	pending := readyWorker()
	pending.health.Key = "pending"
	pending.profile = rpc.Profile{Key: "pending", Type: "custom", Authorized: false}
	pending.health.Authorized = false
	pending.health.State = rpc.StateNeedsAuth

	env := newAccountEnv(t, registry{"official": worker, "pending": pending}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{
			{Key: "official", Type: "official", Enabled: true, Registered: true},
			{Key: "pending", Type: "custom", Enabled: true, Registered: true},
		}
	})

	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/telegram", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw := bodyOf(resp)
	var rows struct {
		Data struct {
			Servers []map[string]any `json:"servers"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatal(err)
	}
	off := rows.Data.Servers[0]
	user, _ := off["user"].(map[string]any)
	if user == nil || user["id"] != float64(42) || user["username"] != "ada" || user["first_name"] != "Ada" {
		t.Fatalf("official user = %v", off["user"])
	}
	if off["state"] != "connected" || off["authorized"] != true {
		t.Fatalf("official row = %v", off)
	}
	// The selector row's facts are all still there, under their own names.
	for _, name := range []string{"key", "type", "enabled", "health_checkable", "state", "authorized", "transport", "last_success_at", "last_error"} {
		if _, ok := off[name]; !ok {
			t.Errorf("the status row is missing %q", name)
		}
	}
	if pending := rows.Data.Servers[1]; pending["user"] != nil || pending["authorized"] != false {
		t.Fatalf("an unauthorized account must report no identity: %v", pending)
	}

	// It never fails the page when the account is unreachable.
	worker.profileE = rpc.ErrUnavailable
	resp = env.send(t, defaultCaller, http.MethodGet, "/api/v1/telegram", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d after a profile failure", resp.StatusCode)
	}
	if !strings.Contains(bodyOf(resp), `"key":"official"`) {
		t.Fatal("the failed identity hid the server")
	}
}

func TestServerListRejectsOtherMethodsWithPermission(t *testing.T) {
	env := serverEnv(t)
	if resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/telegram", ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", resp.StatusCode)
	}
	// An unknown parameter is refused, as on every route in the API.
	if resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/telegram?q=x", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad parameter = %d, want 400", resp.StatusCode)
	}
}
