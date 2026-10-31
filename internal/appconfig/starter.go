package appconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirName is the directory under the user's home that holds the configuration.
const DirName = ".telesync"

// FileName is the configuration file name inside DirName.
const FileName = "config.json"

// DefaultPath returns $HOME/.telesync/config.json.
//
// It is only a location: the file is not created here, so callers can decide
// whether a missing file is an error or a reason to write the starter.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("resolve the home directory: it is empty")
	}
	return filepath.Join(home, DirName, FileName), nil
}

// CreateIfMissing writes a starter configuration at path when nothing exists
// there, and reports whether it wrote one.
//
// An existing file is never touched, whatever its contents: it is the user's
// configuration, and overwriting it would destroy real client definitions. The
// starter directory is created 0700 and the file 0600, because a configuration
// names real clients and paths.
func CreateIfMissing(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("configuration path is empty")
	}
	cleaned := filepath.Clean(path)

	if _, err := os.Lstat(cleaned); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect %q: %w", cleaned, err)
	}

	dir := filepath.Dir(cleaned)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create configuration directory %q: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return false, fmt.Errorf("restrict configuration directory %q: %w", dir, err)
	}

	// Write through a temp file and rename, so a crash never leaves a partial
	// configuration that looks valid.
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temporary configuration in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("restrict configuration file permissions: %w", err)
	}
	if _, err := tmp.WriteString(Starter); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("write starter configuration: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("sync starter configuration: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close starter configuration: %w", err)
	}
	if err := os.Rename(tmpName, cleaned); err != nil {
		return false, fmt.Errorf("write configuration %q: %w", cleaned, err)
	}
	return true, nil
}

// Starter is the configuration written on first run.
//
// It is deliberately minimal: one official client with the key the docs and API
// examples use. It holds no secret values — only the name of the environment
// variable that does — and its app_id is zero, because telesync must never
// invent a credential. Validation therefore fails on it and says what to fill
// in, which is the point.
const Starter = `{
  "api": {
    "listen_addr": "127.0.0.1:8080",
    "request_timeout": "30s",
    "jwt": {
      "secret_env": "TELESYNC_JWT_SECRET"
    }
  },
  "runtime": {
    "run_dir": "./run",
    "session_dir": "./sessions",
    "restart_initial_delay": "1s",
    "restart_max_delay": "30s",
    "max_restart_attempts": 10,
    "startup_timeout": "60s",
    "shutdown_timeout": "15s"
  },
  "clients": [
    {
      "key": "official",
      "type": "official",
      "enabled": true,
      "app_id": 0,
      "app_hash_env": "OFFICIAL_API_HASH",
      "phone_env": "OFFICIAL_PHONE"
    }
  ]
}
`
