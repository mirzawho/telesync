package appconfig_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
)

const testHash = "00112233445566778899aabbccddeeff"

// env returns a lookup backed by a map, with sensible defaults for the hash
// variables used by the fixtures.
func env(overrides map[string]string) func(string) string {
	base := map[string]string{
		"OFFICIAL_HASH": testHash,
		"CAT_HASH":      testHash,
	}
	for k, v := range overrides {
		base[k] = v
	}
	return func(k string) string { return base[k] }
}

// writeConfig writes raw JSON to dir/telesync.json and returns its path.
func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "telesync.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeKey writes a placeholder PEM. appconfig only checks that the file
// exists and is regular; parsing is profile's job.
func writeKey(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "cat-rsa-public.pem")
	if err := os.WriteFile(path, []byte("-----BEGIN PUBLIC KEY-----\nZm9v\n-----END PUBLIC KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func officialJSON(session, socket string) string {
	return fmt.Sprintf(`{
	  "api": {"listen_addr": "127.0.0.1:8080", "request_timeout": "10s"},
	  "runtime": {
	    "run_dir": "%s",
	    "session_dir": "%s",
	    "restart_initial_delay": "500ms",
	    "restart_max_delay": "5s"
	  },
	  "clients": [{
	    "key": "official",
	    "type": "official",
	    "app_id": 12345,
	    "app_hash_env": "OFFICIAL_HASH",
	    "phone_env": "OFFICIAL_PHONE",
	    "session_path": "%s",
	    "rpc_socket_path": "%s"
	  }]
	}`, filepath.Dir(socket), filepath.Dir(session), session, socket)
}

func validOfficial(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	session := filepath.Join(dir, "sessions", "official.json")
	socket := filepath.Join(dir, "run", "official.sock")
	path := writeConfig(t, dir, officialJSON(session, socket))
	return path, map[string]string{"OFFICIAL_PHONE": "+15551230000"}
}

func customJSON(t *testing.T, dir string) string {
	key := writeKey(t, dir)
	return fmt.Sprintf(`{
	  "api": {"listen_addr": "127.0.0.1:8080"},
	  "runtime": {"run_dir": "%s", "session_dir": "%s"},
	  "clients": [{
	    "key": "the-cat",
	    "type": "custom",
	    "app_id": 65432,
	    "app_hash_env": "CAT_HASH",
	    "session_path": "%s",
	    "rpc_socket_path": "%s",
	    "server": {
	      "host": "127.0.0.1",
	      "port": 30001,
	      "dc_id": 2,
	      "rsa_public_key_path": "%s",
	      "transport": "intermediate",
	      "routing": "single-dc",
	      "obfuscated": true
	    }
	  }]
	}`, filepath.Join(dir, "run"), filepath.Join(dir, "sessions"),
		filepath.Join(dir, "sessions", "the-cat.json"),
		filepath.Join(dir, "run", "the-cat.sock"), key)
}

func TestLoadValidOfficial(t *testing.T) {
	path, extra := validOfficial(t)
	cfg, err := appconfig.Load(path, env(extra))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.API.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("listen = %q", cfg.API.ListenAddr)
	}
	if cfg.API.RequestTimeout != 10*time.Second {
		t.Errorf("request timeout = %s", cfg.API.RequestTimeout)
	}
	if cfg.Runtime.RestartInitialDelay != 500*time.Millisecond || cfg.Runtime.RestartMaxDelay != 5*time.Second {
		t.Errorf("restart delays = %s/%s", cfg.Runtime.RestartInitialDelay, cfg.Runtime.RestartMaxDelay)
	}
	if len(cfg.Clients) != 1 {
		t.Fatalf("clients = %d", len(cfg.Clients))
	}
	c := cfg.Clients[0]
	if c.Key != "official" || c.Type != appconfig.Official {
		t.Errorf("client = %+v", c)
	}
	if !c.Enabled {
		t.Error("enabled must default to true")
	}
	if c.AppID != 12345 || c.APIHash != testHash {
		t.Errorf("credentials = %d/%q", c.AppID, c.APIHash)
	}
	if c.Phone != "+15551230000" {
		t.Errorf("phone = %q", c.Phone)
	}
	if !filepath.IsAbs(c.SessionPath) || !filepath.IsAbs(c.RPCSocketPath) {
		t.Errorf("paths must be absolute: %q %q", c.SessionPath, c.RPCSocketPath)
	}
	if c.Server != nil {
		t.Errorf("official client must not carry a server block: %+v", c.Server)
	}
	if got := cfg.EnabledClients(); len(got) != 1 || got[0].Key != "official" {
		t.Errorf("enabled clients = %+v", got)
	}
	if found, ok := cfg.Find("official"); !ok || found.Key != "official" {
		t.Errorf("Find(official) = %+v %v", found, ok)
	}
	if _, ok := cfg.Find("nope"); ok {
		t.Error("Find should reject unknown keys")
	}
}

func TestLoadValidCustom(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, customJSON(t, dir))
	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := cfg.Clients[0]
	if c.Type != appconfig.Custom {
		t.Fatalf("type = %q", c.Type)
	}
	if c.Server == nil {
		t.Fatal("custom client must carry a server block")
	}
	if c.Server.Host != "127.0.0.1" || c.Server.Port != 30001 || c.Server.DCID != 2 {
		t.Errorf("server = %+v", c.Server)
	}
	if c.Server.Transport != "intermediate" || c.Server.Routing != "single-dc" || !c.Server.Obfuscated {
		t.Errorf("server options = %+v", c.Server)
	}
	if !filepath.IsAbs(c.Server.RSAPublicKeyPath) {
		t.Errorf("rsa key path must be absolute: %q", c.Server.RSAPublicKeyPath)
	}
	if c.SessionPath == "" || c.RPCSocketPath == "" {
		t.Error("paths must be resolved")
	}
}

func TestDefaultPathsAndTimeouts(t *testing.T) {
	dir := t.TempDir()
	body := `{
	  "clients": [{"key": "official", "type": "official", "app_id": 1, "app_hash_env": "OFFICIAL_HASH"}]
	}`
	path := writeConfig(t, dir, body)
	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.API.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("default listen = %q", cfg.API.ListenAddr)
	}
	if cfg.API.RequestTimeout != 30*time.Second {
		t.Errorf("default request timeout = %s", cfg.API.RequestTimeout)
	}
	c := cfg.Clients[0]
	wantSession := filepath.Join(dir, "data", "sessions", "official.json")
	wantSocket := filepath.Join(dir, "run", "official.sock")
	if c.SessionPath != wantSession {
		t.Errorf("session = %q, want %q", c.SessionPath, wantSession)
	}
	if c.RPCSocketPath != wantSocket {
		t.Errorf("socket = %q, want %q", c.RPCSocketPath, wantSocket)
	}
	if cfg.Runtime.StartupTimeout <= 0 || cfg.Runtime.ShutdownTimeout <= 0 {
		t.Errorf("startup/shutdown timeouts = %s/%s", cfg.Runtime.StartupTimeout, cfg.Runtime.ShutdownTimeout)
	}
}

func TestRelativePathsResolveAgainstTheConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	body := `{
	  "runtime": {"run_dir": "./run", "session_dir": "./data/sessions"},
	  "clients": [{"key": "official", "type": "official", "app_id": 1, "app_hash_env": "OFFICIAL_HASH"}]
	}`
	path := writeConfig(t, dir, body)
	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Runtime.RunDir != filepath.Join(dir, "run") {
		t.Errorf("run dir = %q", cfg.Runtime.RunDir)
	}
	if cfg.Clients[0].SessionPath != filepath.Join(dir, "data", "sessions", "official.json") {
		t.Errorf("session = %q", cfg.Clients[0].SessionPath)
	}
}

func TestRejectMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "no app_id",
			body: `{"clients":[{"key":"a","type":"official","app_hash_env":"OFFICIAL_HASH"}]}`,
			want: "app_id",
		},
		{
			name: "no hash env",
			body: `{"clients":[{"key":"a","type":"official","app_id":1}]}`,
			want: "app_hash_env",
		},
		{
			name: "custom without server",
			body: `{"clients":[{"key":"a","type":"custom","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`,
			want: "server",
		},
		{
			name: "custom without host",
			body: `{"clients":[{"key":"a","type":"custom","app_id":1,"app_hash_env":"OFFICIAL_HASH","server":{"port":1,"dc_id":2,"rsa_public_key_path":"k.pem"}}]}`,
			want: "host",
		},
		{
			name: "custom without key",
			body: `{"clients":[{"key":"a","type":"custom","app_id":1,"app_hash_env":"OFFICIAL_HASH","server":{"host":"h","port":1,"dc_id":2}}]}`,
			want: "rsa_public_key_path",
		},
		{
			name: "empty clients",
			body: `{"clients":[]}`,
			want: "at least one client",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeConfig(t, dir, tc.body)
			_, err := appconfig.Load(path, env(nil))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestRejectUnsupportedType(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{"clients":[{"key":"a","type":"proxy","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`)
	_, err := appconfig.Load(path, env(nil))
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectDuplicateKeys(t *testing.T) {
	dir := t.TempDir()
	body := `{"clients":[
	  {"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"},
	  {"key":"a","type":"official","app_id":2,"app_hash_env":"OFFICIAL_HASH"}]}`
	path := writeConfig(t, dir, body)
	_, err := appconfig.Load(path, env(nil))
	if err == nil || !strings.Contains(err.Error(), "duplicate client key") {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectUnsafeClientKeys(t *testing.T) {
	for _, key := range []string{"", "Official", "the cat", "../x", "a/b", "-x", "_x", "x.sock", strings.Repeat("a", 65)} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			body := fmt.Sprintf(`{"clients":[{"key":%q,"type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`, key)
			path := writeConfig(t, dir, body)
			_, err := appconfig.Load(path, env(nil))
			if err == nil {
				t.Fatalf("key %q should be rejected", key)
			}
		})
	}
}

func TestRejectInvalidPorts(t *testing.T) {
	for _, body := range []string{
		`{"api":{"listen_addr":"127.0.0.1:70000"},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`,
		`{"api":{"listen_addr":"nonsense"},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`,
		`{"clients":[{"key":"a","type":"custom","app_id":1,"app_hash_env":"OFFICIAL_HASH","server":{"host":"h","port":0,"dc_id":2,"rsa_public_key_path":"k.pem"}}]}`,
		`{"clients":[{"key":"a","type":"custom","app_id":1,"app_hash_env":"OFFICIAL_HASH","server":{"host":"h","port":65536,"dc_id":2,"rsa_public_key_path":"k.pem"}}]}`,
		`{"clients":[{"key":"a","type":"custom","app_id":1,"app_hash_env":"OFFICIAL_HASH","server":{"host":"h","port":1,"dc_id":9,"rsa_public_key_path":"k.pem"}}]}`,
	} {
		dir := t.TempDir()
		path := writeConfig(t, dir, body)
		if _, err := appconfig.Load(path, env(nil)); err == nil {
			t.Fatalf("expected an error for %s", body)
		}
	}
}

func TestRejectUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH","typo":true}]}`)
	_, err := appconfig.Load(path, env(nil))
	if err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectInvalidDurations(t *testing.T) {
	for _, body := range []string{
		`{"api":{"request_timeout":"0s"},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`,
		`{"api":{"request_timeout":"soon"},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`,
		`{"runtime":{"restart_initial_delay":"10s","restart_max_delay":"1s"},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`,
		`{"runtime":{"shutdown_timeout":"-1s"},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`,
	} {
		dir := t.TempDir()
		path := writeConfig(t, dir, body)
		if _, err := appconfig.Load(path, env(nil)); err == nil {
			t.Fatalf("expected an error for %s", body)
		}
	}
}

func TestRejectConflictingPaths(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		body string
	}{
		{
			name: "socket outside run dir",
			body: `{"runtime":{"run_dir":"` + filepath.Join(dir, "run") + `"},
			  "clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH","rpc_socket_path":"` + filepath.Join(dir, "elsewhere", "a.sock") + `"}]}`,
		},
		{
			name: "session inside run dir",
			body: `{"runtime":{"run_dir":"` + filepath.Join(dir, "run") + `","session_dir":"` + filepath.Join(dir, "sessions") + `"},
			  "clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH","session_path":"` + filepath.Join(dir, "run", "a.json") + `"}]}`,
		},
		{
			name: "same session and socket path",
			body: `{"runtime":{"run_dir":"` + filepath.Join(dir, "run") + `"},
			  "clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH",
			    "session_path":"` + filepath.Join(dir, "run", "a.sock") + `","rpc_socket_path":"` + filepath.Join(dir, "run", "a.sock") + `"}]}`,
		},
		{
			name: "duplicate session paths",
			body: `{"clients":[
			  {"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH","session_path":"` + filepath.Join(dir, "one.json") + `"},
			  {"key":"b","type":"official","app_id":2,"app_hash_env":"OFFICIAL_HASH","session_path":"` + filepath.Join(dir, "one.json") + `"}]}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, t.TempDir(), tc.body)
			_, err := appconfig.Load(path, env(nil))
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDisabledClientIsStillValidatedAndExcluded(t *testing.T) {
	dir := t.TempDir()
	body := `{"clients":[
	  {"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH","enabled":false},
	  {"key":"b","type":"official","app_id":2,"app_hash_env":"OFFICIAL_HASH","enabled":true}]}`
	path := writeConfig(t, dir, body)
	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Clients[0].Enabled {
		t.Error("a client must be disabled explicitly")
	}
	if got := cfg.EnabledClients(); len(got) != 1 || got[0].Key != "b" {
		t.Errorf("enabled = %+v", got)
	}
}

func TestDisabledClientStillRequiresItsSecret(t *testing.T) {
	dir := t.TempDir()
	body := `{"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"MISSING_HASH","enabled":false}]}`
	path := writeConfig(t, dir, body)
	_, err := appconfig.Load(path, env(nil))
	if err == nil || !strings.Contains(err.Error(), "MISSING_HASH") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretResolutionNeverLeaksValues(t *testing.T) {
	const secret = "super-secret-hash-value"
	dir := t.TempDir()
	body := `{"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH","phone_env":"OFFICIAL_PHONE"}]}`
	path := writeConfig(t, dir, body)
	cfg, err := appconfig.Load(path, env(map[string]string{
		"OFFICIAL_HASH":  secret,
		"OFFICIAL_PHONE": "+15551230000",
	}))
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(rendered, secret) {
		t.Fatalf("configuration description leaks the API hash: %s", rendered)
	}
	if strings.Contains(rendered, "+15551230000") {
		t.Fatalf("configuration description leaks the phone number: %s", rendered)
	}
	if !strings.Contains(rendered, "OFFICIAL_HASH") || !strings.Contains(rendered, "OFFICIAL_PHONE") {
		t.Fatalf("description should name the environment variables: %s", rendered)
	}

	// The resolved values must survive the redaction boundary, and must never
	// be marshalled back out.
	if cfg.Clients[0].APIHash != secret {
		t.Error("the secret was not resolved")
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("JSON encoding leaks the secret: %s", encoded)
	}
}

func TestMissingSecretNamesTheVariableAndNotAValue(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"MISSING_HASH"}]}`)
	_, err := appconfig.Load(path, env(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "MISSING_HASH") {
		t.Errorf("error should name the variable: %v", err)
	}
}

func TestRemoteBindRequiresAuthentication(t *testing.T) {
	base := `{"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`
	for _, addr := range []string{"0.0.0.0:9000", "192.168.1.10:9000", "example.com:9000", ":9000"} {
		t.Run(addr, func(t *testing.T) {
			body := fmt.Sprintf(`{"api":{"listen_addr":%q},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`, addr)
			path := writeConfig(t, t.TempDir(), body)
			if _, err := appconfig.Load(path, env(nil)); err == nil {
				t.Fatalf("remote bind %q must require authentication", addr)
			}
		})
	}

	// A non-loopback listener is served by per-user logins, configured with the
	// name of the variable holding the JWT key.
	body := `{"api":{"listen_addr":"0.0.0.0:9000","jwt":{"secret_env":"JWT_SECRET"}},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`
	path := writeConfig(t, t.TempDir(), body)
	cfg, err := appconfig.Load(path, env(map[string]string{"JWT_SECRET": strings.Repeat("k", 40)}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.API.JWT.Secret != strings.Repeat("k", 40) {
		t.Error("the JWT secret was not resolved")
	}
	rendered := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(rendered, strings.Repeat("k", 40)) {
		t.Fatalf("description leaks the JWT secret: %s", rendered)
	}

	// The fixed shared token is loopback-only, even next to a JWT secret.
	tokened := `{"api":{"listen_addr":"0.0.0.0:9000","auth_token_env":"API_TOKEN","jwt":{"secret_env":"JWT_SECRET"}},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`
	if _, err := appconfig.Load(writeConfig(t, t.TempDir(), tokened),
		env(map[string]string{"API_TOKEN": "tok", "JWT_SECRET": strings.Repeat("k", 40)})); err == nil ||
		!strings.Contains(err.Error(), "api.auth_token_env") {
		t.Fatalf("err = %v, want the shared token refused on a non-loopback listener", err)
	}

	if _, err := appconfig.Load(path, env(nil)); err == nil {
		t.Fatal("a configured jwt.secret_env with no value must be an error")
	}
	_ = base
}

func TestJWTSecretMustBeLongAndComeFromOnePlace(t *testing.T) {
	clients := `"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]`
	cases := map[string]struct {
		body string
		env  map[string]string
		want string
	}{
		"short variable": {`{"api":{"jwt":{"secret_env":"JWT_SECRET"}},` + clients + `}`, map[string]string{"JWT_SECRET": "short"}, "shorter than 32"},
		"short literal":  {`{"api":{"jwt":{"secret":"short"}},` + clients + `}`, nil, "shorter than 32"},
		"empty literal":  {`{"api":{"jwt":{"secret":""}},` + clients + `}`, nil, "api.jwt.secret is empty"},
		"both sources":   {`{"api":{"jwt":{"secret":"` + strings.Repeat("k", 40) + `","secret_env":"JWT_SECRET"}},` + clients + `}`, map[string]string{"JWT_SECRET": strings.Repeat("k", 40)}, "not both"},
		"bad ttl":        {`{"api":{"jwt":{"token_ttl":"soon"}},` + clients + `}`, nil, "token_ttl"},
		"ttl too long":   {`{"api":{"jwt":{"token_ttl":"48h"}},` + clients + `}`, nil, "at most 24h0m0s"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := appconfig.Load(writeConfig(t, t.TempDir(), tc.body), env(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}

	// A complete, valid configuration resolves the secret and the TTL.
	body := `{"api":{"jwt":{"secret_env":"JWT_SECRET","token_ttl":"15m"}},` + clients + `}`
	cfg, err := appconfig.Load(writeConfig(t, t.TempDir(), body), env(map[string]string{"JWT_SECRET": strings.Repeat("k", 32)}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.API.JWT.TTL != 15*time.Minute {
		t.Fatalf("ttl = %s", cfg.API.JWT.TTL)
	}
	encoded, _ := json.Marshal(cfg)
	if strings.Contains(string(encoded), strings.Repeat("k", 32)) {
		t.Fatal("JSON encoding leaks the JWT secret")
	}
}

func TestJWTSecretCanBeWrittenInTheFile(t *testing.T) {
	clients := `"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]`
	literal := strings.Repeat("L", 40)
	body := `{"api":{"jwt":{"secret":"` + literal + `","token_ttl":"2h"}},` + clients + `}`
	cfg, err := appconfig.Load(writeConfig(t, t.TempDir(), body), env(nil))
	if err != nil {
		t.Fatalf("Load with a literal secret: %v", err)
	}
	if cfg.API.JWT.Secret != literal || !cfg.API.JWT.SecretInFile || cfg.API.JWT.SecretEnv != "" {
		t.Fatalf("jwt = %+v (env %q, in file %v)", cfg.API.JWT.TTL, cfg.API.JWT.SecretEnv, cfg.API.JWT.SecretInFile)
	}
	if cfg.API.JWT.TTL != 2*time.Hour {
		t.Fatalf("ttl = %s", cfg.API.JWT.TTL)
	}
	rendered := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(rendered, literal) {
		t.Fatalf("describe leaks the literal key:\n%s", rendered)
	}
	if !strings.Contains(rendered, "api.jwt.secret in the configuration file") {
		t.Fatalf("describe should say the key is in the file:\n%s", rendered)
	}
	encoded, _ := json.Marshal(cfg)
	if strings.Contains(string(encoded), literal) {
		t.Fatal("JSON encoding leaks the literal key")
	}

	// A literal key also satisfies the non-loopback requirement.
	remote := `{"api":{"listen_addr":"0.0.0.0:9000","jwt":{"secret":"` + literal + `"}},` + clients + `}`
	if _, err := appconfig.Load(writeConfig(t, t.TempDir(), remote), env(nil)); err != nil {
		t.Fatalf("a non-loopback listener with a literal key: %v", err)
	}
}

func TestJWTTTLDefaultsToOneHour(t *testing.T) {
	clients := `"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]`
	cfg, err := appconfig.Load(writeConfig(t, t.TempDir(), `{"api":{"jwt":{"secret_env":"JWT_SECRET"}},`+clients+`}`),
		env(map[string]string{"JWT_SECRET": strings.Repeat("k", 32)}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.JWT.TTL != time.Hour {
		t.Fatalf("ttl = %s, want 1h", cfg.API.JWT.TTL)
	}
}

func TestLoopbackAllowsNoAuth(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:9000", "localhost:9000", "[::1]:9000"} {
		body := fmt.Sprintf(`{"api":{"listen_addr":%q},"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]}`, addr)
		path := writeConfig(t, t.TempDir(), body)
		if _, err := appconfig.Load(path, env(nil)); err != nil {
			t.Errorf("loopback bind %q should be allowed: %v", addr, err)
		}
	}
}

func TestOfficialRejectsServerBlock(t *testing.T) {
	dir := t.TempDir()
	body := `{"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH",
	  "server":{"host":"h","port":1,"dc_id":2,"rsa_public_key_path":"k.pem"}}]}`
	path := writeConfig(t, dir, body)
	if _, err := appconfig.Load(path, env(nil)); err == nil {
		t.Fatal("official must reject a server block")
	}
}

func TestCustomRejectsMissingKeyFile(t *testing.T) {
	dir := t.TempDir()
	body := fmt.Sprintf(`{"clients":[{"key":"a","type":"custom","app_id":1,"app_hash_env":"OFFICIAL_HASH",
	  "server":{"host":"h","port":1,"dc_id":2,"rsa_public_key_path":%q}}]}`, filepath.Join(dir, "missing.pem"))
	path := writeConfig(t, dir, body)
	_, err := appconfig.Load(path, env(nil))
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestCustomRejectsInvalidEnums(t *testing.T) {
	dir := t.TempDir()
	key := writeKey(t, dir)
	for _, extra := range []string{`"transport":"carrier-pigeon"`, `"routing":"anywhere"`} {
		body := fmt.Sprintf(`{"clients":[{"key":"a","type":"custom","app_id":1,"app_hash_env":"OFFICIAL_HASH",
		  "server":{"host":"h","port":1,"dc_id":2,"rsa_public_key_path":%q,%s}}]}`, key, extra)
		path := writeConfig(t, dir, body)
		if _, err := appconfig.Load(path, env(nil)); err == nil {
			t.Fatalf("expected an error for %s", extra)
		}
	}
}

func TestMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{"clients": [`)
	if _, err := appconfig.Load(path, env(nil)); err == nil {
		t.Fatal("expected a parse error")
	}
	path = writeConfig(t, dir, `{} {}`)
	if _, err := appconfig.Load(path, env(nil)); err == nil {
		t.Fatal("expected a trailing-data error")
	}
}

func TestMissingConfigFile(t *testing.T) {
	_, err := appconfig.Load(filepath.Join(t.TempDir(), "absent.json"), env(nil))
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
}

// --- literal secrets (no environment variable) ---

func TestLiteralAppHashIsAccepted(t *testing.T) {
	const hash = "9599c5e78fb5f75557b69ec78525a059"
	dir := t.TempDir()
	body := `{"clients":[{"key":"official","type":"official","app_id":1379537,"app_hash":"` + hash + `"}]}`
	path := writeConfig(t, dir, body)

	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Clients[0].APIHash != hash {
		t.Fatalf("hash = %q", cfg.Clients[0].APIHash)
	}
	if cfg.Clients[0].AppHashEnv != "" {
		t.Fatalf("no env name should be recorded: %q", cfg.Clients[0].AppHashEnv)
	}
}

func TestLiteralPhoneIsAccepted(t *testing.T) {
	dir := t.TempDir()
	body := `{"clients":[{"key":"official","type":"official","app_id":1,"app_hash":"abc","phone":"+15551230000"}]}`
	path := writeConfig(t, dir, body)
	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Clients[0].Phone != "+15551230000" {
		t.Fatalf("phone = %q", cfg.Clients[0].Phone)
	}
}

func TestAppHashAndEnvTogetherAreRejected(t *testing.T) {
	dir := t.TempDir()
	body := `{"clients":[{"key":"a","type":"official","app_id":1,"app_hash":"abc","app_hash_env":"OFFICIAL_HASH"}]}`
	path := writeConfig(t, dir, body)
	_, err := appconfig.Load(path, env(nil))
	if err == nil {
		t.Fatal("setting both app_hash and app_hash_env is ambiguous and must be rejected")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Fatalf("err = %v", err)
	}
}

func TestNeitherAppHashNorEnvIsRejected(t *testing.T) {
	dir := t.TempDir()
	body := `{"clients":[{"key":"a","type":"official","app_id":1}]}`
	path := writeConfig(t, dir, body)
	_, err := appconfig.Load(path, env(nil))
	if err == nil {
		t.Fatal("a client with no hash source must be rejected")
	}
	if !strings.Contains(err.Error(), "app_hash") {
		t.Fatalf("err = %v", err)
	}
}

func TestLiteralHashNeverAppearsInOutput(t *testing.T) {
	const hash = "9599c5e78fb5f75557b69ec78525a059"
	dir := t.TempDir()
	body := `{"clients":[{"key":"official","type":"official","app_id":1379537,"app_hash":"` + hash + `"}]}`
	path := writeConfig(t, dir, body)
	cfg, err := appconfig.Load(path, env(nil))
	if err != nil {
		t.Fatal(err)
	}

	rendered := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(rendered, hash) {
		t.Fatalf("Describe leaks the literal hash:\n%s", rendered)
	}
	if !strings.Contains(rendered, "app_hash") {
		t.Fatalf("Describe should still report that a hash is set:\n%s", rendered)
	}

	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), hash) {
		t.Fatalf("marshalling leaks the literal hash: %s", encoded)
	}
}

// withRuntimeField rewrites the runtime block of a valid fixture.
func withRuntimeField(t *testing.T, key, value string) string {
	t.Helper()
	dir := t.TempDir()
	session := filepath.Join(dir, "sessions", "official.json")
	socket := filepath.Join(dir, "run", "official.sock")
	base := officialJSON(session, socket)
	patched := strings.Replace(base, `"restart_initial_delay": "500ms",`,
		`"restart_initial_delay": "500ms",
	    "`+key+`": `+value+`,`, 1)
	path := writeConfig(t, dir, patched)
	return path
}

func TestReconnectDefaultsToTenSeconds(t *testing.T) {
	path, vars := validOfficial(t)
	cfg, err := appconfig.Load(path, env(vars))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Runtime.Reconnect != 10*time.Second {
		t.Fatalf("reconnect = %s, want 10s", cfg.Runtime.Reconnect)
	}
}

func TestReconnectIsAWholeNumberOfSeconds(t *testing.T) {
	for _, tc := range []struct {
		value int
		want  time.Duration
	}{
		{value: 1, want: time.Second},
		{value: 60, want: time.Minute},
	} {
		path := withRuntimeField(t, "reconnect", fmt.Sprintf("%d", tc.value))
		cfg, err := appconfig.Load(path, env(map[string]string{"OFFICIAL_PHONE": "+15551230000"}))
		if err != nil {
			t.Fatalf("reconnect %d: %v", tc.value, err)
		}
		if cfg.Runtime.Reconnect != tc.want {
			t.Fatalf("reconnect %d = %s, want %s", tc.value, cfg.Runtime.Reconnect, tc.want)
		}
	}
}

func TestReconnectRejectsUnusableValues(t *testing.T) {
	for _, value := range []string{"0", "-5", "1.5", `"10s"`} {
		path := withRuntimeField(t, "reconnect", value)
		if _, err := appconfig.Load(path, env(map[string]string{"OFFICIAL_PHONE": "+15551230000"})); err == nil {
			t.Fatalf("reconnect %s must be rejected", value)
		} else if !strings.Contains(err.Error(), "reconnect") {
			t.Fatalf("reconnect %s: error does not name the field: %v", value, err)
		}
	}
}

func TestLoopbackRecognisesEveryFormAndNothingElse(t *testing.T) {
	clients := `"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]`
	for addr, want := range map[string]bool{
		"127.0.0.1:8080":    true,
		"localhost:8080":    true,
		"[::1]:8080":        true,
		"0.0.0.0:8080":      false,
		"192.168.1.10:8080": false,
		"example.com:8080":  false,
	} {
		cfg, err := appconfig.Load(writeConfig(t, t.TempDir(),
			fmt.Sprintf(`{"api":{"listen_addr":%q},%s}`, addr, clients)), env(nil))
		if err != nil {
			// Only a non-loopback bind without authentication is an error.
			if want {
				t.Errorf("%s: %v", addr, err)
			}
			continue
		}
		if cfg.API.Loopback() != want {
			t.Errorf("%s: Loopback() = %v, want %v", addr, cfg.API.Loopback(), want)
		}
	}
}

func TestTheOperatorTokenIsOnlyHonouredOnALoopbackListener(t *testing.T) {
	clients := `"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]`
	local := `{"api":{"listen_addr":"127.0.0.1:8080","auth_token_env":"API_TOKEN"},` + clients + `}`
	cfg, err := appconfig.Load(writeConfig(t, t.TempDir(), local), env(map[string]string{"API_TOKEN": "tok"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.API.OperatorToken() != "tok" {
		t.Fatalf("operator token = %q", cfg.API.OperatorToken())
	}

	// A loopback deployment without the variable has no operator token.
	none := `{"api":{"listen_addr":"127.0.0.1:8080"},` + clients + `}`
	cfg, err = appconfig.Load(writeConfig(t, t.TempDir(), none), env(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.API.OperatorToken() != "" {
		t.Fatalf("operator token = %q, want none", cfg.API.OperatorToken())
	}
}

func TestDescribeNeverPrintsTheOperatorToken(t *testing.T) {
	clients := `"clients":[{"key":"a","type":"official","app_id":1,"app_hash_env":"OFFICIAL_HASH"}]`
	body := `{"api":{"listen_addr":"127.0.0.1:8080","auth_token_env":"API_TOKEN","jwt":{"secret_env":"JWT_SECRET"}},` + clients + `}`
	cfg, err := appconfig.Load(writeConfig(t, t.TempDir(), body), env(map[string]string{
		"API_TOKEN": "operator-secret", "JWT_SECRET": strings.Repeat("j", 40),
	}))
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.Join(cfg.Describe(), "\n")
	if strings.Contains(rendered, "operator-secret") || strings.Contains(rendered, strings.Repeat("j", 40)) {
		t.Fatalf("describe leaks a secret:\n%s", rendered)
	}
	if !strings.Contains(rendered, "api.operator_token") || !strings.Contains(rendered, "api.jwt") {
		t.Fatalf("describe should say how authentication is configured:\n%s", rendered)
	}
}
