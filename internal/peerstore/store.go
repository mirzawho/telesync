// Package peerstore remembers the access hash of channels this worker has
// resolved.
//
// MTProto addresses a channel by the pair (id, access hash). An access hash is
// learned from Telegram (a dialog, a search hit, a username resolve) and cannot
// be recomputed from the id, so a worker that forgets it cannot address the
// channel again after a restart. This store is that memory.
//
// It is private worker state: never served, never logged, and written 0600.
package peerstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// maxPeers bounds the file so a long-lived worker cannot grow it without limit.
const maxPeers = 2000

// Store is a per-client channel access-hash cache.
type Store struct {
	path string

	mu      sync.Mutex
	entries map[int64]entry
	// order records insertion order so the oldest entry can be evicted.
	order []int64
}

type entry struct {
	AccessHash int64  `json:"access_hash"`
	Title      string `json:"title,omitempty"`
	Username   string `json:"username,omitempty"`
}

type file struct {
	Version int              `json:"version"`
	Peers   map[string]entry `json:"peers"`
}

// Open loads the store at path, creating the directory if needed.
//
// A missing file is not an error: it means nothing has been resolved yet.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("peerstore: path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("peerstore: create directory: %w", err)
	}
	s := &Store{path: path, entries: map[int64]entry{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("peerstore: read %q: %w", s.path, err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		// A corrupted cache is recoverable: the next resolve rewrites it. This
		// file is derived data, unlike a session, so refusing to start over it
		// would be worse than starting empty.
		return nil
	}
	for rawID, e := range f.Peers {
		var id int64
		if _, err := fmt.Sscan(rawID, &id); err != nil || id <= 0 || e.AccessHash == 0 {
			continue
		}
		s.entries[id] = e
		s.order = append(s.order, id)
	}
	sort.Slice(s.order, func(i, j int) bool { return s.order[i] < s.order[j] })
	return nil
}

// Save records the access hash of one channel.
func (s *Store) Save(id, accessHash int64, title, username string) error {
	if id <= 0 || accessHash == 0 {
		return errors.New("peerstore: channel id and access hash must be non-zero")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, seen := s.entries[id]; !seen {
		s.order = append(s.order, id)
	}
	s.entries[id] = entry{AccessHash: accessHash, Title: title, Username: username}
	for len(s.order) > maxPeers {
		evict := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, evict)
	}
	return s.flushLocked()
}

// Find returns the remembered access hash of one channel.
func (s *Store) Find(id int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || e.AccessHash == 0 {
		return 0, false
	}
	return e.AccessHash, true
}

// Len reports how many channels are remembered.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// flushLocked writes the whole file atomically.
//
// The file is derived data, and a worker restart is the only reader, so a
// whole-file rewrite is simpler and safer than an incremental update.
func (s *Store) flushLocked() error {
	f := file{Version: 1, Peers: make(map[string]entry, len(s.entries))}
	for id, e := range s.entries {
		f.Peers[fmt.Sprintf("%d", id)] = e
	}
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("peerstore: encode: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".peers-*.tmp")
	if err != nil {
		return fmt.Errorf("peerstore: write %q: %w", s.path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("peerstore: write %q: %w", s.path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("peerstore: write %q: %w", s.path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("peerstore: write %q: %w", s.path, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("peerstore: write %q: %w", s.path, err)
	}
	return nil
}
