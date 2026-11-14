package daemonlock_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/mirza/telesync/internal/daemonlock"
)

func TestAcquireExcludesAnotherDaemonUsingTheSamePersistenceDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	first, err := daemonlock.Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Close()

	if second, err := daemonlock.Acquire(path); !errors.Is(err, daemonlock.ErrAlreadyHeld) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second Acquire = %v, want ErrAlreadyHeld", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	third, err := daemonlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	_ = third.Close()
}
