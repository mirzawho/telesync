// Package sessionstore stores one client's MTProto session in its own file.
//
// Session files are credentials: they are written atomically with mode 0600
// inside a directory tightened to 0700, and never shared between clients.
package sessionstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gotd/td/session"
)

// Store is a session.Storage backed by a single file.
type Store struct {
	path string
	mux  sync.Mutex
}

// NewAt returns a store for one explicit session file path.
//
// The parent directory is created (or tightened) to 0700 so every configured
// client keeps its session private.
func NewAt(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("session path is empty")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("session path %q must be absolute", path)
	}
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return nil, fmt.Errorf("session path %q must name a file", path)
	}
	if strings.Contains(filepath.ToSlash(path), "../") {
		return nil, fmt.Errorf("session path %q must not contain %q", path, "..")
	}
	cleaned := filepath.Clean(path)
	dir := filepath.Dir(cleaned)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory %q: %w", dir, err)
	}
	// MkdirAll does not tighten an existing directory.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict session directory %q: %w", dir, err)
	}
	return &Store{path: cleaned}, nil
}

// Path returns the session file path.
func (s *Store) Path() string { return s.path }

// LoadSession implements session.Storage.
func (s *Store) LoadSession(_ context.Context) ([]byte, error) {
	s.mux.Lock()
	defer s.mux.Unlock()

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read session %q: %w", s.path, err)
	}
	return data, nil
}

// StoreSession implements session.Storage, writing atomically with mode 0600.
func (s *Store) StoreSession(_ context.Context, data []byte) error {
	s.mux.Lock()
	defer s.mux.Unlock()

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary session file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("restrict session file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write session: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace session file %q: %w", s.path, err)
	}
	return nil
}

// State describes a locally stored session without exposing key material.
type State struct {
	Path        string
	Exists      bool
	Readable    bool
	HasAuthKey  bool
	DC          int
	SavedThisDC int
	Permission  string
	Size        int64
	Error       string
}

// String describes the state without ever carrying session bytes.
//
// The path is reported as its base name only: a full path can name a user or a
// phone number, and session bytes never appear here at all.
func (s State) String() string {
	return fmt.Sprintf("State{file:%s exists:%t readable:%t has_auth_key:%t dc:%d saved_this_dc:%d permission:%s size:%d error:%q}",
		filepath.Base(s.Path), s.Exists, s.Readable, s.HasAuthKey, s.DC, s.SavedThisDC,
		s.Permission, s.Size, s.Error)
}

// Inspect reports the local state of the session file at path.
func Inspect(path string) State {
	st := State{Path: path}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return st
	}
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Exists = true
	st.Size = info.Size()
	st.Permission = info.Mode().Perm().String()

	data, err := os.ReadFile(path)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Readable = true
	if len(data) == 0 {
		return st
	}

	var v struct {
		Version int
		Data    session.Data
	}
	if err := json.Unmarshal(data, &v); err != nil {
		st.Error = fmt.Sprintf("session file is not valid JSON: %v", err)
		return st
	}
	st.HasAuthKey = len(v.Data.AuthKey) > 0 && len(v.Data.AuthKeyID) > 0
	st.DC = v.Data.DC
	st.SavedThisDC = v.Data.Config.ThisDC
	return st
}
