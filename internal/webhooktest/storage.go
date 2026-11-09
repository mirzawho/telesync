package webhooktest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record is one saved diagnostic record: the request that arrived, what the
// receiver concluded about it, and the response it sent back.
//
// It is written for a human reading it later, so it keeps everything the
// exchange had — including the authentication headers, which is why the files
// are kept private to their owner and are never committed.
type Record struct {
	SavedAt        time.Time      `json:"saved_at"`
	Route          string         `json:"route"`
	AuthType       AuthType       `json:"auth_type"`
	Received       RequestRecord  `json:"received"`
	Authentication Result         `json:"authentication"`
	Response       ResponseRecord `json:"response"`
}

// ResponseRecord is the response the handler generated. BodyText is the exact
// bytes written; for a HEAD request the handler writes none, and BodyOmitted
// says so rather than leaving a reader to wonder.
type ResponseRecord struct {
	Status      int                 `json:"status"`
	StatusText  string              `json:"status_text"`
	Headers     map[string][]string `json:"headers"`
	Body        json.RawMessage     `json:"body,omitempty"`
	BodyText    string              `json:"body_text"`
	BodySize    int                 `json:"body_size"`
	BodyOmitted bool                `json:"body_omitted"`
}

// Store writes records into one directory.
type Store struct {
	dir string
	now func() time.Time
}

// NewStore prepares dir, creating it if it does not exist.
//
// The directory and every file in it are private to their owner: a record holds
// the request exactly as it arrived, credentials included, and this is a
// development tool rather than somewhere that should be readable by a group.
func NewStore(dir string, now func() time.Time) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("webhooktest: no output directory")
	}
	if now == nil {
		now = time.Now
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("webhooktest: resolve the output directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("webhooktest: create %s: %w", abs, err)
	}
	// MkdirAll leaves an existing directory as it found it, and a permissive
	// umask would leave a new one wider than asked. Either way this is the one
	// place the owner-only rule is enforced, so it is stated outright.
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("webhooktest: restrict %s: %w", abs, err)
	}
	return &Store{dir: abs, now: now}, nil
}

// Dir is the directory records are written to.
func (s *Store) Dir() string { return s.dir }

// Save writes one record and returns the absolute path it was written to.
//
// A name is never reused: the file is created exclusively, and a collision
// takes a new unique suffix rather than overwriting a record already kept.
func (s *Store) Save(rec Record) (string, error) {
	if rec.SavedAt.IsZero() {
		rec.SavedAt = s.now()
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("webhooktest: encode the record: %w", err)
	}
	raw = append(raw, '\n')

	const attempts = 16
	var lastErr error
	for i := 0; i < attempts; i++ {
		path := filepath.Join(s.dir, fileName(rec, uniqueSuffix()))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				lastErr = err
				continue
			}
			return "", fmt.Errorf("webhooktest: create %s: %w", path, err)
		}
		// Created with 0600 and then set to 0600: the mode is the rule here,
		// not whatever the process's umask happens to leave behind.
		if err := f.Chmod(0o600); err != nil {
			f.Close()
			return "", fmt.Errorf("webhooktest: restrict %s: %w", path, err)
		}
		if _, err := f.Write(raw); err != nil {
			f.Close()
			return "", fmt.Errorf("webhooktest: write %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("webhooktest: close %s: %w", path, err)
		}
		return path, nil
	}
	return "", fmt.Errorf("webhooktest: could not find a free file name in %s: %w", s.dir, lastErr)
}

// fileName is when the request arrived, which route and method it used, and a
// suffix that keeps two requests in the same second apart.
//
// Nothing in it comes from the request unexamined: an HTTP method is chosen by
// whoever sends the request, and a name built from one is a name built from
// attacker-controlled text.
func fileName(rec Record, suffix string) string {
	return strings.Join([]string{
		sanitize(rec.SavedAt.UTC().Format("2006-01-02T15-04-05.000Z")),
		sanitize(string(rec.AuthType)),
		sanitize(rec.Received.Method),
		sanitize(suffix),
	}, "_") + ".json"
}

// sanitize keeps the characters a file name may safely hold and replaces the
// rest, so no part of a request can choose a directory or hide an extension.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// uniqueSuffix is four hex characters chosen at random, so a name cannot be
// predicted and two requests cannot collide by construction.
func uniqueSuffix() string {
	var buf [2]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "0000"
	}
	return hex.EncodeToString(buf[:])
}
