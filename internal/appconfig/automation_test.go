package appconfig_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/appconfig"
)

// aiConfig writes a minimal configuration whose automation block is the given
// JSON, and returns its path.
//
// The AI key has two homes and exactly one may be used: a literal api_key in
// the file, or the name of a variable in api_key_env. That is the same rule
// api.jwt.secret, api.wordpress.encryption_key and a client's app_hash follow,
// and it is worth pinning — the alternative is a deployment that silently
// ignores the key it was given.
func aiConfig(t *testing.T, automation string, env map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	body := `{
  "api": {"listen_addr": "127.0.0.1:8080"},
  "runtime": {
    "run_dir": "` + dir + `/run",
    "session_dir": "` + dir + `/sessions",
    "data_dir": "` + dir + `/db",
    "cache_dir": "` + dir + `/cache",
    "peer_dir": "` + dir + `/peers"
  },
  "automation": ` + automation + `,
  "clients": [
    {
      "key": "official",
      "type": "official",
      "enabled": false,
      "app_id": 1,
      "app_hash_env": "HASH",
      "phone_env": "PHONE",
      "session_path": "` + dir + `/s.json",
      "rpc_socket_path": "` + dir + `/run/o.sock",
      "peer_cache_path": "` + dir + `/peers/p.json"
    }
  ]
}`
	path := filepath.Join(dir, "telesync.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	base := map[string]string{"HASH": "test-hash", "PHONE": "+10000000000"}
	for k, v := range env {
		base[k] = v
	}
	lookup = func(k string) string { return base[k] }
	return path
}

// lookup is the environment seam each test sets before loading.
var lookup func(string) string

// ai renders the "ai" object with whatever extra fields a case needs, so a key
// is always placed where the schema expects it.
func ai(extra ...string) string {
	body := `{"enabled": true, "base_url": "https://api.example/v1", "model": "m"`
	for _, e := range extra {
		if e != "" {
			body += ", " + e
		}
	}
	return body + `}`
}

func TestAnAIKeyInTheFileIsAcceptedAndMarkedAsSuch(t *testing.T) {
	path := aiConfig(t, `{"enabled": false, "ai": `+ai(`"api_key": "literal-key"`)+`}`, nil)
	cfg, err := appconfig.Load(path, lookup)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Automation.AI.APIKey != "literal-key" {
		t.Fatalf("APIKey = %q, want the literal one", cfg.Automation.AI.APIKey)
	}
	if !cfg.Automation.AI.APIKeyInFile {
		t.Error("APIKeyInFile = false, want true for a key that came from the file")
	}
}

func TestAnAIKeyFromAVariableIsAcceptedAndNotMarkedAsInFile(t *testing.T) {
	path := aiConfig(t, `{"enabled": false, "ai": `+ai(`"api_key_env": "AI_KEY"`)+`}`,
		map[string]string{"AI_KEY": "from-the-environment"})
	cfg, err := appconfig.Load(path, lookup)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Automation.AI.APIKey != "from-the-environment" {
		t.Fatalf("APIKey = %q, want the variable's value", cfg.Automation.AI.APIKey)
	}
	if cfg.Automation.AI.APIKeyInFile {
		t.Error("APIKeyInFile = true for a key that came from a variable")
	}
}

func TestAnAIKeyFromBothSourcesIsRefused(t *testing.T) {
	path := aiConfig(t, `{"enabled": false, "ai": `+ai(`"api_key": "literal-key"`, `"api_key_env": "AI_KEY"`)+`}`,
		map[string]string{"AI_KEY": "from-the-environment"})
	_, err := appconfig.Load(path, lookup)
	if err == nil {
		t.Fatal("both api_key and api_key_env were accepted; the choice is ambiguous")
	}
	if !strings.Contains(err.Error(), "api_key") || !strings.Contains(err.Error(), "api_key_env") {
		t.Fatalf("the refusal names neither source: %v", err)
	}
}

func TestAnEnabledAIFailsWithoutAnyKeyAtAll(t *testing.T) {
	path := aiConfig(t, `{"enabled": true, "ai": `+ai()+`}`, nil)
	_, err := appconfig.Load(path, lookup)
	if err == nil {
		t.Fatal("an enabled AI with no key was accepted; no request could ever be signed")
	}
	for _, want := range []string{"api_key", "api_key_env"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %s: %v", want, err)
		}
	}
}

func TestAnEnabledAIFailsWhenTheNamedVariableIsNotSet(t *testing.T) {
	path := aiConfig(t, `{"enabled": true, "ai": `+ai(`"api_key_env": "MISSING_KEY"`)+`}`, nil)
	_, err := appconfig.Load(path, lookup)
	if err == nil {
		t.Fatal("an unset variable was accepted")
	}
	if !strings.Contains(err.Error(), "MISSING_KEY") {
		t.Fatalf("the refusal does not name the variable: %v", err)
	}
}

func TestNoKeyIsNeededWhenAIIsSwitchedOff(t *testing.T) {
	path := aiConfig(t, `{"enabled": true, "ai": {"enabled": false}}`, nil)
	cfg, err := appconfig.Load(path, lookup)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Automation.AI.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", cfg.Automation.AI.APIKey)
	}
	if !cfg.Automation.Enabled {
		t.Error("the automation block's own switch was not honoured")
	}
}

// A refusal must never echo the secret back, whatever it says.
func TestAnAIRefusalNeverEchoesTheKeyItWasGiven(t *testing.T) {
	const secret = "sk-super-secret-value"
	path := aiConfig(t, `{"enabled": true, "ai": {"enabled": true, "base_url": "https://api.example/v1", "model": "m", "api_key": "`+secret+`", "api_key_env": "AI_KEY"}}`,
		map[string]string{"AI_KEY": "other"})
	_, err := appconfig.Load(path, lookup)
	if err == nil {
		t.Fatal("both sources were accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the refusal echoes the key: %v", err)
	}
}

// The resolved key must not travel with the configuration: it is usable in
// memory and invisible in every marshalled form.
func TestAnAIKeyIsNeverMarshalledWithTheConfiguration(t *testing.T) {
	path := aiConfig(t, `{"enabled": false, "ai": `+ai(`"api_key": "literal-key"`)+`}`, nil)
	cfg, err := appconfig.Load(path, lookup)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := json.Marshal(cfg.Automation.AI)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "literal-key") {
		t.Fatalf("the key was marshalled: %s", out)
	}
}
