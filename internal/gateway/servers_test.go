package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

// server is one row of GET /api/v1/telegram.
type server struct {
	Key             string  `json:"key"`
	Type            string  `json:"type"`
	Enabled         bool    `json:"enabled"`
	HealthCheckable bool    `json:"health_checkable"`
	State           string  `json:"state"`
	Authorized      *bool   `json:"authorized"`
	Transport       string  `json:"transport"`
	LastSuccessAt   *string `json:"last_success_at"`
	LastError       string  `json:"last_error"`
}

type serverList struct {
	Data struct {
		Servers []server `json:"servers"`
	} `json:"data"`
	Meta json.RawMessage `json:"meta"`
}

func TestServerListReturnsTheEligibleServers(t *testing.T) {
	official := readyWorker()
	cat := readyWorker()
	cat.health.Key = "the-cat"
	cat.health.Type = "custom"
	cat.health.State = "needs_auth"
	cat.health.Authorized = false

	env := newEnv(t, registry{"official": official, "the-cat": cat}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{
			{Key: "official", Type: "official", Enabled: true, Registered: true},
			// A candidate that was never registered has no worker: it stays
			// visible, but says plainly that its health is unknown.
			{Key: "not-running", Type: "custom", Enabled: true},
			// A disabled client is not eligible for selection.
			{Key: "old", Type: "official", Enabled: false},
		}
	})

	resp := env.get(t, "/api/v1/telegram")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body serverList
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	got := make([]string, 0, len(body.Data.Servers))
	for _, s := range body.Data.Servers {
		got = append(got, s.Key)
	}
	if strings.Join(got, ",") != "official,not-running" {
		t.Fatalf("servers = %v, want official and not-running only", got)
	}
	if len(body.Meta) == 0 {
		t.Error("the listing must carry meta, like every other route")
	}

	off := body.Data.Servers[0]
	if !off.Enabled || off.Type != "official" || !off.HealthCheckable || off.State != "connected" {
		t.Fatalf("official = %+v", off)
	}
	if off.Authorized == nil || !*off.Authorized {
		t.Fatalf("official authorized = %v", off.Authorized)
	}

	unknown := body.Data.Servers[1]
	if unknown.HealthCheckable || unknown.State != "unknown" || unknown.Authorized != nil {
		t.Fatalf("a server with no worker must not claim health: %+v", unknown)
	}

	// The listing asks each eligible worker and no other: it never touches a
	// disabled client's process.
	// Health and identity per server, and nothing for a disabled client.
	if official.callCount() != 2 || cat.callCount() != 0 {
		t.Fatalf("calls: official=%d the-cat=%d", official.callCount(), cat.callCount())
	}
}

func TestServerListNeverExposesConfiguration(t *testing.T) {
	worker := readyWorker()

	// The sentinels a listing must never carry: credentials, session material,
	// endpoint detail, and the names of the configuration's own fields.
	sentinels := []string{
		"0123456789abcdef0123456789abcdef", // an app hash
		"+15550001111",                     // a phone number
		"mtproto.example.invalid",          // a custom endpoint host
		"app_hash", "api_hash", "phone", "session", "token", "secret", "server_host",
		"api.listen", "rpc_socket",
	}
	env := newEnv(t, registry{"the-cat": worker}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{{Key: "the-cat", Type: "custom", Enabled: true, Registered: true}}
	})

	resp := env.get(t, "/api/v1/telegram")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	defer resp.Body.Close()

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, sentinel := range sentinels {
		// The key is sentinel-free, so a hit is never the key itself.
		if strings.Contains(strings.ToLower(text), strings.ToLower(sentinel)) && !strings.Contains(text, `"key":"the-cat"`) {
			t.Errorf("the listing contains %q: %s", sentinel, text)
		}
	}

	// The rows are exactly the documented fields.
	servers := raw["data"].(map[string]any)["servers"].([]any)
	row := servers[0].(map[string]any)
	want := map[string]bool{
		"key": true, "type": true, "enabled": true, "health_checkable": true,
		"state": true, "authorized": true,
		"transport": true, "last_success_at": true, "last_error": true, "user": true,
	}
	for name := range row {
		if !want[name] {
			t.Errorf("undocumented field %q in a server row", name)
		}
	}
	for name := range want {
		if _, ok := row[name]; !ok {
			t.Errorf("field %q is missing from a server row", name)
		}
	}
}

func TestServerListRequiresAuthentication(t *testing.T) {
	worker := readyWorker()
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{{Key: "official", Type: "official", Enabled: true, Registered: true}}
	})

	env.session = ""
	resp := env.get(t, "/api/v1/telegram", "Authorization", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var body errorEnvelope
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.Error.Code != "unauthorized" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if worker.callCount() != 0 {
		t.Fatal("an unauthenticated request must not reach a worker")
	}
}

func TestServerListRequiresTheViewPermission(t *testing.T) {
	worker := readyWorker()
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{{Key: "official", Type: "official", Enabled: true, Registered: true}}
		c.Operator = ""
	})

	// No way to authenticate without a store: the answer is 401, never a list.
	resp := env.get(t, "/api/v1/telegram", "Authorization", "Bearer nope")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
	if worker.callCount() != 0 {
		t.Fatal("a rejected credential must not reach a worker")
	}
}

func TestServerListWithoutServersIsAnEmptyArray(t *testing.T) {
	env := newEnv(t, registry{}, func(c *gateway.Config) { c.Servers = nil })

	resp := env.get(t, "/api/v1/telegram")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	text := readAll(t, resp)
	if !strings.Contains(text, `"servers":[]`) {
		t.Fatalf("an empty listing must be [], not null: %s", text)
	}
}

func TestServerListRejectsOtherMethods(t *testing.T) {
	env := newEnv(t, registry{"official": readyWorker()}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{{Key: "official", Type: "official", Enabled: true, Registered: true}}
	})

	resp := env.do(t, http.MethodPost, "/api/v1/telegram")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestServerListReportsAnUnreachableWorker(t *testing.T) {
	worker := readyWorker()
	worker.healthE = rpc.ErrUnavailable
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) {
		c.Servers = []gateway.ServerInfo{{Key: "official", Type: "official", Enabled: true, Registered: true}}
	})

	resp := env.get(t, "/api/v1/telegram")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("one unreachable worker must not fail the listing; status = %d", resp.StatusCode)
	}
	var body serverList
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	row := body.Data.Servers[0]
	if row.State != "unavailable" || row.Authorized != nil {
		t.Fatalf("row = %+v", row)
	}
	if row.LastError == "" {
		t.Error("the row should say why the health check failed")
	}
}

// readAll is one response's body as text, for assertions about its exact bytes.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var buf strings.Builder
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
