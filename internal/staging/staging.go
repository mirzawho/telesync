// Package staging holds an uploaded attachment between the moment a send is
// accepted and the moment a background worker has sent it.
//
// The two moments are in different requests and can be separated by a restart,
// so nothing belonging to the HTTP request may be relied on: not its body, not
// its reader, and above all not the file name the client chose. What this
// package writes is a file named by this server, in a directory this server
// owns, together with enough about the content to recognise it again.
//
// It is deliberately narrow. It stores bytes and hands them back; it knows
// nothing about Telegram, media kinds or send operations, and it never returns
// a path. A staged file is addressed by its storage name and is readable only
// through here.
package staging

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Errors a caller is expected to act on.
var (
	// ErrTooLarge means the attachment is above the deployment's upload limit.
	// Nothing is stored: the request is refused, not truncated.
	ErrTooLarge = errors.New("staging: attachment is larger than the configured limit")
	// ErrNotFound means no staged attachment has that name.
	ErrNotFound = errors.New("staging: no staged attachment has that name")
	// ErrInvalidName means the name offered is not one this server could have
	// produced, so it names nothing here and never will.
	ErrInvalidName = errors.New("staging: that is not a staged attachment name")
)

// nameLength is the length of a storage name in hexadecimal characters: 16
// bytes of randomness, which is enough that a name cannot be guessed and is
// never derived from anything a caller sends.
const nameLength = 32

// partSuffix marks a file that is still being written. It is never a staged
// attachment and is never accepted as one: a crash leaves such a file behind,
// and it is removed by the next sweep rather than mistaken for finished work.
const partSuffix = ".part"

// copyBuffer is the chunk a staged upload is read in. It is large enough to
// make the syscall count reasonable and small enough that staging many uploads
// at once does not hold them all in memory.
const copyBuffer = 32 << 10

// Meta is what a caller knows about the attachment that is not its bytes.
//
// OriginalName is the file name the client supplied. It is a label: it is
// recorded so the message can show the name the sender chose, and it is never
// used to build a path.
type Meta struct {
	OriginalName string
	MIMEType     string
}

// File is one staged attachment as this server records it.
type File struct {
	// Name is the storage name. It is not a path and it is never derived from
	// anything a caller sent.
	Name string
	// Size is the number of bytes staged.
	Size int64
	// SHA256 is the hex digest of those bytes. It is how two uploads of the
	// same file name are told apart: a name proves nothing about content.
	SHA256 string
	// Original is the sanitised client file name, kept as metadata only.
	Original string
	// MIMEType is the content type the caller declared for the bytes.
	MIMEType string
}

// Staging is a durable holding area for staged attachments.
type Staging struct {
	dir string
	max int64

	mu sync.Mutex
}

// Open prepares the staging directory, creating it if it is not there.
//
// The directory survives a restart on purpose: an attachment staged before one
// must still be sendable after it. The limit is the largest single attachment
// this deployment accepts; zero means unlimited, which nothing should ask for.
func Open(dir string, maxFileBytes int64) (*Staging, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("staging: the staging directory is not configured")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("staging: create %s: %w", dir, err)
	}
	// Tighten an existing directory rather than trusting how it was made: an
	// attachment other accounts on this machine can read is not staged, it is
	// published.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("staging: secure %s: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("staging: inspect %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("staging: %s is not a directory", dir)
	}
	return &Staging{dir: dir, max: maxFileBytes}, nil
}

// Dir is the staging directory. It is reported for diagnostics and is never
// part of an API answer: a staged file is addressed by name, not by path.
func (s *Staging) Dir() string { return s.dir }

// Limit is the largest attachment this area will hold. Zero means unlimited. It
// is read back out so a caller can name the number in the answer it gives for a
// refusal, rather than quoting a limit of its own that might not be the one
// applied.
func (s *Staging) Limit() int64 { return s.max }

// Write streams one attachment into the staging area.
//
// The bytes are never held whole: they are copied in chunks straight to disk,
// hashed as they pass. The file appears under its final name only once every
// byte has been written and flushed, so a name that exists is a complete
// attachment and a partial upload is never mistaken for one.
//
// A write that fails, is refused for size or is cancelled removes what it
// started, and reports why. It never leaves a partial behind for cleanup to
// find later if it can clean up now.
func (s *Staging) Write(ctx context.Context, r io.Reader, meta Meta) (File, error) {
	if r == nil {
		return File{}, errors.New("staging: nothing to stage")
	}
	if err := ctx.Err(); err != nil {
		return File{}, err
	}

	name, err := newName()
	if err != nil {
		return File{}, err
	}
	final := filepath.Join(s.dir, name)
	partial := final + partSuffix

	out, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return File{}, fmt.Errorf("staging: begin %s: %w", name, err)
	}
	// Everything below abandons this file on failure; only success renames it
	// into place.
	keep := false
	defer func() {
		if !keep {
			_ = out.Close()
			_ = os.Remove(partial)
		}
	}()

	hash := sha256.New()
	limited := io.LimitReader(r, s.limit()+1)
	buf := make([]byte, copyBuffer)

	written, err := copyContext(ctx, io.MultiWriter(out, hash), limited, buf)
	if err != nil {
		return File{}, err
	}
	if s.max > 0 && written > s.max {
		return File{}, ErrTooLarge
	}

	// Flush before the rename: a name that is visible must be a whole file, so
	// a crash mid-write cannot publish half an attachment under a finished name.
	if err := out.Sync(); err != nil {
		return File{}, fmt.Errorf("staging: flush %s: %w", name, err)
	}
	if err := out.Close(); err != nil {
		return File{}, fmt.Errorf("staging: close %s: %w", name, err)
	}
	if err := os.Rename(partial, final); err != nil {
		return File{}, fmt.Errorf("staging: finalise %s: %w", name, err)
	}
	syncDir(s.dir)
	keep = true

	return File{
		Name:     name,
		Size:     written,
		SHA256:   hex.EncodeToString(hash.Sum(nil)),
		Original: sanitiseFileName(meta.OriginalName),
		MIMEType: strings.TrimSpace(meta.MIMEType),
	}, nil
}

// limit is the number of bytes Write will read before refusing. One byte more
// than the maximum is read so that "exactly at the limit" and "one over" are
// told apart without trusting the caller's declared length.
func (s *Staging) limit() int64 {
	if s.max <= 0 {
		return 1<<62 - 1
	}
	return s.max
}

// Open reads one staged attachment back as a stream.
//
// The name is checked before it is joined to anything, and the entry is checked
// again before it is opened: a name is a token this server issued and nothing
// else resolves here, not a path, not a symlink and not a directory.
func (s *Staging) Open(name string) (io.ReadCloser, error) {
	path, err := s.pathFor(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		// A symlink or a directory is not a staged attachment, whatever it is
		// called. Reading through one would leave this directory entirely.
		return nil, ErrNotFound
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// ReadAll returns a staged attachment's bytes. It is for the send path, which
// hands the whole attachment to the worker's transfer at once; answering a
// download streams instead and never comes through here.
func (s *Staging) ReadAll(name string) ([]byte, error) {
	rc, err := s.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Remove discards one staged attachment. Removing one that is already gone is
// not a failure: a retention sweep and the completion of the job that used it
// can both run.
func (s *Staging) Remove(name string) error {
	path, err := s.pathFor(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Names lists the staged attachments currently held.
func (s *Staging) Names() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.names()
}

func (s *Staging) names() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, partSuffix) {
			continue
		}
		if !validName(name) {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// Reconcile removes staged attachments that no operation claims any more, and
// reports how many it removed.
//
// It is what answers a crash between "the bytes were written" and "the
// operation that owns them was recorded": such a file is on disk with nothing
// pointing at it, and it stays there forever without this. The decision about
// what to keep is the caller's, because only the send operations know which
// attachments a pending or retrying job still needs.
func (s *Staging) Reconcile(keep func(name string) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names, err := s.names()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, name := range names {
		if keep != nil && keep(name) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// SweepPartials removes abandoned half-written files and reports how many.
//
// A file still being written is a partial too, so this is only ever run when no
// upload is in flight — at start-up, when this process has just begun and
// cannot be staging anything yet. An age of zero therefore means "every
// partial", which is exactly what a restarting process wants.
func (s *Staging) SweepPartials(olderThan time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), partSuffix) {
			continue
		}
		if olderThan > 0 {
			info, err := e.Info()
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()) < olderThan {
				continue
			}
		}
		if err := os.Remove(filepath.Join(s.dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// pathFor resolves a storage name to the file behind it, refusing anything that
// is not a name this server could have issued.
func (s *Staging) pathFor(name string) (string, error) {
	if !validName(name) {
		return "", ErrInvalidName
	}
	return filepath.Join(s.dir, name), nil
}

// validName reports whether name is one this server issues: 32 hexadecimal
// characters and nothing else. Checking the shape rather than cleaning the
// input is deliberate — a caller's name is not sanitised into a path here, it
// is refused.
func validName(name string) bool {
	if len(name) != nameLength {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// newName issues a storage name: 16 bytes of randomness, hex encoded. It is
// unguessable and it carries nothing about the attachment, the caller or the
// request.
func newName() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("staging: name: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// sanitiseFileName keeps the client's file name as the label it is.
//
// It is what the message shows for a document, so it survives — but only as
// text. Every path separator is cut, the last segment is kept, and a name that
// is nothing but path syntax becomes the neutral "attachment" rather than being
// walked or dropped silently.
func sanitiseFileName(raw string) string {
	name := strings.ReplaceAll(raw, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	switch {
	case name == "", name == ".", name == "..":
		return "attachment"
	case strings.Contains(name, ".."):
		// What is left is a label, and a label with dot-dot in it is confusing
		// at best. It is kept as text, never as traversal.
		name = strings.ReplaceAll(name, ".", "_")
	}
	const maxNameRunes = 200
	runes := []rune(name)
	if len(runes) > maxNameRunes {
		runes = runes[:maxNameRunes]
		name = string(runes)
	}
	return strings.TrimSpace(name)
}

// copyContext copies src into dst in buf-sized chunks, stopping early when the
// context ends. A chunked copy is what makes an interrupted upload observable
// rather than something that only shows up as a stuck request.
func copyContext(ctx context.Context, dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			w, writeErr := dst.Write(buf[:n])
			written += int64(w)
			if writeErr != nil {
				return written, writeErr
			}
			if w != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

// syncDir flushes a directory entry so a rename survives the machine going
// down. Not every filesystem accepts this; failing to sync a directory is not a
// reason to lose the attachment that was just written.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}
