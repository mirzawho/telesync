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

// An ambiguous key is the only thing worth reporting about the key. Adding
// "and that variable is not set" on top of it sends someone looking for the
// wrong problem when the fix is to delete one of the two lines.
func TestAnAmbiguousAIKeyDoesNotAlsoComplainThatTheVariableIsUnset(t *testing.T) {
	path := aiConfig(t, `{"enabled": true, "ai": `+ai(`"api_key": "literal-key"`, `"api_key_env": "NEVER_SET"`)+`}`, nil)
	_, err := appconfig.Load(path, lookup)
	if err == nil {
		t.Fatal("both sources were accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not both") {
		t.Fatalf("the ambiguity is not named: %v", err)
	}
	if strings.Contains(msg, "is not set") {
		t.Fatalf("the refusal also blames the variable, which is not the problem: %v", err)
	}
	// And it is one complaint about the key, not two.
	if strings.Count(msg, "api_key") < 2 {
		t.Fatalf("the refusal does not name both sources: %v", err)
	}
}

// --- the loopback exception's two homes --------------------------------------
//
// It is one knob with two places to say it: allow_localhost beside the rest of
// the webhook settings, and AllowLocalhostEnv in the environment. The file is
// where an operator keeps the rest of this subsystem's settings and the loader
// refuses any key it does not know, so an exception that existed only as a
// variable would be a startup failure waiting for anyone who reached for the
// obvious place.

func TestTheLoopbackExceptionIsOffUntilSomethingTurnsItOn(t *testing.T) {
	path := aiConfig(t, `{"enabled": true}`, nil)
	cfg, err := appconfig.Load(path, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Automation.Webhooks.AllowLocalhost {
		t.Fatal("the loopback exception came on by itself")
	}
}

func TestTheLoopbackExceptionMayBeSetInTheConfigurationFile(t *testing.T) {
	path := aiConfig(t, `{"enabled": true, "webhooks": {"allow_localhost": true}}`, nil)
	cfg, err := appconfig.Load(path, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Automation.Webhooks.AllowLocalhost {
		t.Fatal("automation.webhooks.allow_localhost in the file was ignored")
	}
}

func TestTheEnvironmentOverridesTheFileForTheLoopbackException(t *testing.T) {
	// A deployment that templates or shares its configuration file still gets
	// the last word, in whichever direction it wants it.
	for _, tc := range []struct {
		name string
		file string
		env  string
		want bool
	}{
		{"the variable turns it off over a file that turned it on", "true", "false", false},
		{"the variable turns it on over a file that left it off", "false", "true", true},
		{"an unset variable leaves the file's word alone", "true", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			if tc.env != "" {
				env[appconfig.AllowLocalhostEnv] = tc.env
			}
			path := aiConfig(t, `{"enabled": true, "webhooks": {"allow_localhost": `+tc.file+`}}`, env)
			cfg, err := appconfig.Load(path, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Automation.Webhooks.AllowLocalhost != tc.want {
				t.Fatalf("allow_localhost = %v, want %v", cfg.Automation.Webhooks.AllowLocalhost, tc.want)
			}
		})
	}
}

func TestAnUnrecognisedEnvironmentValueNeverTurnsTheExceptionOn(t *testing.T) {
	// "maybe" is not permission. It fails closed against the file as well: a
	// variable that says nothing comprehensible is not a way to leave a security
	// exception wherever a shared file happened to put it.
	for _, file := range []string{"true", "false"} {
		path := aiConfig(t, `{"enabled": true, "webhooks": {"allow_localhost": `+file+`}}`,
			map[string]string{appconfig.AllowLocalhostEnv: "maybe"})
		cfg, err := appconfig.Load(path, lookup)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Automation.Webhooks.AllowLocalhost {
			t.Fatalf("an unrecognised value turned the exception on (the file said %s)", file)
		}
	}
}
