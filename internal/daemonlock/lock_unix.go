//go:build darwin || linux

// Package daemonlock gives one daemon exclusive ownership of a local
// persistence directory across processes.
package daemonlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// ErrAlreadyHeld means another daemon currently owns this persistence
// directory.
var ErrAlreadyHeld = errors.New("daemonlock: another daemon owns this persistence directory")

// Lock is held until Close or process exit. The kernel releases it after a
// crash, unlike a PID marker file.
type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

// Acquire takes an exclusive non-blocking lock on path.
//
// The lock file must live beside the SQLite database, not under the worker
// socket directory: two daemons using the same local persistence cannot both
// start account Runners, even if their socket directories differ.
func Acquire(path string) (*Lock, error) {
	if path == "" {
		return nil, errors.New("daemonlock: lock path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("daemonlock: create lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemonlock: open lock file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("daemonlock: restrict lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAlreadyHeld
		}
		return nil, fmt.Errorf("daemonlock: acquire lock: %w", err)
	}
	return &Lock{file: f}, nil
}

// Close releases the lock. It is safe to call more than once.
func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.file == nil {
			return
		}
		unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		closeErr := l.file.Close()
		if unlockErr != nil {
			l.err = fmt.Errorf("daemonlock: release lock: %w", unlockErr)
		} else if closeErr != nil {
			l.err = fmt.Errorf("daemonlock: close lock file: %w", closeErr)
		}
	})
	return l.err
}
