package appconfig_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/appconfig"
)

// Direct sending is off until a deployment says otherwise, and its limits are
// the ones the rest of the system can actually honour. Both are decided here,
// at load time, so a mistake is a startup failure and not a surprise later.

// sendJSON is the standard test configuration with a "send" block spliced in.
// The directories below follow the fixture's own rule: sessions and sockets are
// kept out of the runtime directory, because a session is a credential.
func sendJSON(t *testing.T, dir, sendBlock string) string {
	t.Helper()
	base := officialJSON(filepath.Join(dir, "sessions", "official.json"), filepath.Join(dir, "run", "official.sock"))
	at := strings.LastIndex(base, "}")
	if at < 0 {
		t.Fatalf("the fixture is not a JSON object: %s", base)
	}
	return base[:at] + `,"send": ` + sendBlock + base[at:]
}

func loadSend(t *testing.T, sendBlock string, overrides map[string]string) (appconfig.Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := writeConfig(t, dir, sendJSON(t, dir, sendBlock))
	if overrides == nil {
		overrides = map[string]string{}
	}
	if _, ok := overrides["OFFICIAL_PHONE"]; !ok {
		overrides["OFFICIAL_PHONE"] = "+15551230000"
	}
	return appconfig.Load(path, env(overrides))
}

// Test A. With nothing said, sending is off and its settings are the safe
// defaults. A feature that writes to Telegram is never on because someone
// forgot to say no.
func TestASendingIsOffAndBoundedByDefault(t *testing.T) {
	cfg, err := loadSend(t, `{}`, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Send.Enabled {
		t.Fatal("direct sending is enabled by default")
	}
	if cfg.Send.MaxFileBytes != appconfig.DefaultSendMaxFileBytes {
		t.Fatalf("max_file_bytes = %d, want %d", cfg.Send.MaxFileBytes, appconfig.DefaultSendMaxFileBytes)
	}
	if cfg.Send.StagingDir != appconfig.DefaultSendStagingDir {
		t.Fatalf("staging_dir = %q, want %q", cfg.Send.StagingDir, appconfig.DefaultSendStagingDir)
	}
	if cfg.Send.HistoryRetentionDays != appconfig.DefaultSendHistoryRetention {
		t.Fatalf("history_retention_days = %d, want %d",
			cfg.Send.HistoryRetentionDays, appconfig.DefaultSendHistoryRetention)
	}
}

// Test B. The file's own values are used as they are written.
func TestBTheConfigurationFileIsRead(t *testing.T) {
	cfg, err := loadSend(t, `{
		"enabled": true,
		"staging_dir": "/var/lib/telesync/sends",
		"max_file_bytes": 1048576,
		"history_retention_days": 7
	}`, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Send.Enabled {
		t.Fatal("the file enabled sending and the loader did not")
	}
	if cfg.Send.StagingDir != "/var/lib/telesync/sends" {
		t.Fatalf("staging_dir = %q", cfg.Send.StagingDir)
	}
	if cfg.Send.MaxFileBytes != 1048576 {
		t.Fatalf("max_file_bytes = %d, want 1048576", cfg.Send.MaxFileBytes)
	}
	if cfg.Send.HistoryRetentionDays != 7 {
		t.Fatalf("history_retention_days = %d, want 7", cfg.Send.HistoryRetentionDays)
	}
}

// Test C. The environment wins whenever it says anything, so a deployment can
// switch the feature on without editing a file that is often templated.
func TestCTheEnvironmentOverridesTheFile(t *testing.T) {
	cfg, err := loadSend(t, `{"enabled": false, "staging_dir": "/from-file", "max_file_bytes": 1048576}`, map[string]string{
		appconfig.SendEnabledEnv:   "true",
		appconfig.SendStagingDir:   "/from-environment",
		appconfig.SendMaxFileBytes: "2097152",
		appconfig.SendHistoryDays:  "3",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Send.Enabled {
		t.Fatal("the environment enabled sending and the loader did not")
	}
	if cfg.Send.StagingDir != "/from-environment" {
		t.Fatalf("staging_dir = %q, want the environment's own", cfg.Send.StagingDir)
	}
	if cfg.Send.MaxFileBytes != 2097152 {
		t.Fatalf("max_file_bytes = %d, want 2097152", cfg.Send.MaxFileBytes)
	}
	if cfg.Send.HistoryRetentionDays != 3 {
		t.Fatalf("history_retention_days = %d, want 3", cfg.Send.HistoryRetentionDays)
	}
}

// Test D. A limit that could not work is refused at load time. Below it a
// photograph would not fit; above it the worker transfer could not carry the
// file, so neither is a setting anybody means.
func TestDAlimitThatCannotWorkIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
	}{
		{"below the smallest useful limit", `{"max_file_bytes": 12}`},
		{"above what one transfer can carry", `{"max_file_bytes": 999999999}`},
		{"not a size at all", `{"max_file_bytes": -1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadSend(t, tc.block, nil); err == nil {
				t.Fatal("the loader accepted a limit that cannot work")
			}
		})
	}
}

// Test E. A retention window cannot be negative. Nothing is kept for a
// negative number of days, and a configuration that says so is a mistake.
func TestEARetentionWindowCannotBeNegative(t *testing.T) {
	if _, err := loadSend(t, `{"history_retention_days": -1}`, nil); err == nil {
		t.Fatal("the loader accepted a negative retention window")
	}
}

// Test F. A variable that is set but is not a number is a mistake and is said
// so. Reading it as zero would silently disable a limit.
func TestFAVariableThatIsNotANumberIsAMistake(t *testing.T) {
	_, err := loadSend(t, `{}`, map[string]string{appconfig.SendMaxFileBytes: "thirty-two"})
	if err == nil {
		t.Fatal("the loader accepted a limit that is not a number")
	}
	if !strings.Contains(err.Error(), appconfig.SendMaxFileBytes) {
		t.Fatalf("the problem does not name the variable: %v", err)
	}
}

// Test G. An environment flag that says nothing recognisable is false, never
// true: a security-adjacent switch must fail closed.
func TestGAnUnrecognisedFlagIsOff(t *testing.T) {
	cfg, err := loadSend(t, `{"enabled": true}`, map[string]string{appconfig.SendEnabledEnv: "please"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Send.Enabled {
		t.Fatal("an unrecognised value for the enable flag turned sending on")
	}
}
