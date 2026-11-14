//go:build !darwin && !linux

package daemonlock

import (
	"errors"
	"fmt"
)

// ErrAlreadyHeld means another daemon currently owns this persistence
// directory.
var ErrAlreadyHeld = errors.New("daemonlock: another daemon owns this persistence directory")

// Lock is a build-compatible placeholder on platforms where this package has
// no supported cross-process lock implementation.
type Lock struct{}

// Acquire fails closed where a cross-process lock cannot be guaranteed.
func Acquire(path string) (*Lock, error) {
	return nil, fmt.Errorf("daemonlock: cross-process locks are unsupported on this platform (path %q)", path)
}

// Close is a no-op for an unavailable lock.
func (*Lock) Close() error { return nil }
