package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Listen prepares the socket path and starts listening on it.
//
// The parent directory is created (or tightened) to 0700 and the socket itself
// is chmod'ed to 0600, so only the owning user can reach the private RPC
// interface. A regular file at the path is never replaced: that is somebody's
// data, not a stale socket.
func Listen(path string) (net.Listener, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("worker: socket path is empty")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("worker: socket path %q must be absolute", path)
	}
	if strings.Contains(filepath.ToSlash(path), "../") {
		return nil, fmt.Errorf("worker: socket path %q must not contain %q", path, "..")
	}
	cleaned := filepath.Clean(path)
	dir := filepath.Dir(cleaned)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("worker: create run directory %q: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("worker: restrict run directory %q: %w", dir, err)
	}

	info, err := os.Lstat(cleaned)
	switch {
	case err == nil && info.Mode()&os.ModeSocket != 0:
		// A live socket is somebody else's: a second worker must fail here,
		// not steal the path from the one already serving on it.
		if live, liveErr := isLive(cleaned); liveErr != nil {
			return nil, fmt.Errorf("worker: inspect socket %q: %w", cleaned, liveErr)
		} else if live {
			return nil, fmt.Errorf("worker: socket %q is already in use", cleaned)
		}
		// A leftover socket from a previous process. Binding over it would work
		// on Linux but leave a stale inode behind on some platforms.
		if err := os.Remove(cleaned); err != nil {
			return nil, fmt.Errorf("worker: remove stale socket %q: %w", cleaned, err)
		}
	case err == nil:
		return nil, fmt.Errorf("worker: %q exists and is not a socket; move it aside manually", cleaned)
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("worker: inspect %q: %w", cleaned, err)
	}

	ln, err := net.Listen("unix", cleaned)
	if err != nil {
		return nil, fmt.Errorf("worker: listen on %q: %w", cleaned, err)
	}
	if err := os.Chmod(cleaned, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("worker: restrict socket %q: %w", cleaned, err)
	}
	return ln, nil
}

// isLive reports whether something is accepting connections on the Unix socket
// at path. ECONNREFUSED is the only answer that means "nobody is there": a
// socket file exists but no process listens on it.
func isLive(path string) (bool, error) {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		conn.Close()
		return true, nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
		return false, nil
	}
	return false, err
}

// Serve answers RPC requests on ln until ctx is cancelled.
//
// The server is never exposed on TCP: ln is a Unix listener created by Listen.
func Serve(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("worker: shutdown: %w", err)
	}
	return nil
}
