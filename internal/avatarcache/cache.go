// Package avatarcache keeps channel profile photos on local disk for a day.
//
// The cache is a plain directory of `<client>/<channel>/<file>` entries plus a
// small JSON sidecar with the content type, expiry and Telegram's photo
// version. It is private server state, outside any served directory.
package avatarcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is how long a cached avatar is served without asking Telegram.
const DefaultTTL = 24 * time.Hour

// MaxAvatarBytes bounds one cached image.
const MaxAvatarBytes = 8 << 20

// staleGrace is how long an expired entry may still be served when refreshing
// fails. It is short on purpose: an avatar that is a few hours stale is better
// than a broken page, an avatar from last month is not.
const staleGrace = 6 * time.Hour

// maxScanEntries bounds a cleanup pass so a huge cache cannot stall a request.
const maxScanEntries = 10000

// Image is one cached avatar.
type Image struct {
	Data []byte
	// ContentType is what was determined when the image was stored.
	ContentType string
	// PhotoID is Telegram's photo version, so a changed avatar is a new entry.
	PhotoID int64
	// StoredAt is when this entry was written.
	StoredAt time.Time
	// ExpiresAt is when it must be refreshed.
	ExpiresAt time.Time
	// Stale is true when the entry is past its expiry and is only being served
	// because the refresh failed.
	Stale bool
}

// FetchFunc downloads one avatar. It is only called on a miss or an expiry.
type FetchFunc func(ctx context.Context) (data []byte, contentType string, photoID int64, err error)

// Cache is a persistent avatar cache rooted at one directory.
type Cache struct {
	dir string
	ttl time.Duration
	now func() time.Time
	log func(format string, args ...any)

	// mu serializes fetches per key so concurrent requests for the same uncached
	// avatar produce one download, not one per request.
	mu       sync.Mutex
	inflight map[string]*flight
}

// Options configures Open.
type Options struct {
	// TTL is how long an entry stays valid. Zero means DefaultTTL.
	TTL time.Duration
	// Now overrides the clock (tests).
	Now func() time.Time
	// Logf receives one line per failure. Nil discards.
	Logf func(format string, args ...any)
}

// flight is one in-progress fetch shared by concurrent callers.
type flight struct {
	done  chan struct{}
	image Image
	err   error
}

// Open prepares the cache directory.
func Open(dir string, opts Options) (*Cache, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("avatarcache: directory is empty")
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("avatarcache: directory %q must be absolute", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("avatarcache: create %q: %w", dir, err)
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Cache{
		dir:      filepath.Clean(dir),
		ttl:      ttl,
		now:      now,
		log:      logf,
		inflight: map[string]*flight{},
	}, nil
}

// Dir is the cache root.
func (c *Cache) Dir() string { return c.dir }

// Get returns the avatar for (clientKey, channelID), fetching it when needed.
//
// A valid entry is served without calling fetch. An expired entry is refreshed;
// if the refresh fails and the entry is still within the short stale window it
// is served with Stale set, so the caller can say so in a header. Nothing is
// ever written from a failed fetch.
func (c *Cache) Get(ctx context.Context, clientKey string, channelID int64, fetch FetchFunc) (Image, error) {
	if err := validateKey(clientKey); err != nil {
		return Image{}, err
	}
	if channelID <= 0 {
		return Image{}, errors.New("avatarcache: channel id must be positive")
	}

	base := c.entryBase(clientKey, channelID)

	if img, ok := c.read(base); ok && c.now().Before(img.ExpiresAt) {
		return img, nil
	}

	// Serialize the download for this key.
	c.mu.Lock()
	if f, running := c.inflight[base]; running {
		c.mu.Unlock()
		select {
		case <-f.done:
			if f.err == nil {
				return f.image, nil
			}
			return c.staleOrFail(base, f.err)
		case <-ctx.Done():
			return Image{}, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[base] = f
	c.mu.Unlock()

	img, err := c.refresh(ctx, base, fetch)
	f.image, f.err = img, err
	c.mu.Lock()
	delete(c.inflight, base)
	c.mu.Unlock()
	close(f.done)

	if err != nil {
		return c.staleOrFail(base, err)
	}
	return img, nil
}

// refresh fetches an avatar and stores it atomically.
func (c *Cache) refresh(ctx context.Context, base string, fetch FetchFunc) (Image, error) {
	data, contentType, photoID, err := fetch(ctx)
	if err != nil {
		return Image{}, err
	}
	if len(data) == 0 {
		return Image{}, ErrNoAvatar
	}
	if len(data) > MaxAvatarBytes {
		return Image{}, fmt.Errorf("avatarcache: avatar is %d bytes, above the %d limit",
			len(data), MaxAvatarBytes)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	now := c.now().UTC()
	img := Image{
		Data:        data,
		ContentType: contentType,
		PhotoID:     photoID,
		StoredAt:    now,
		ExpiresAt:   now.Add(c.ttl),
	}
	if err := c.write(base, img); err != nil {
		// Serving the bytes is still correct; only the caching failed.
		c.log("avatarcache: %v", err)
	}
	return img, nil
}

// staleOrFail returns an expired-but-recent entry, or the fetch error.
func (c *Cache) staleOrFail(base string, fetchErr error) (Image, error) {
	img, ok := c.read(base)
	if !ok {
		return Image{}, fetchErr
	}
	if c.now().After(img.ExpiresAt.Add(staleGrace)) {
		return Image{}, fetchErr
	}
	img.Stale = true
	return img, nil
}

// read loads one entry from disk.
func (c *Cache) read(base string) (Image, bool) {
	meta, err := readMeta(base + metaSuffix)
	if err != nil {
		return Image{}, false
	}
	data, err := os.ReadFile(base + dataSuffix)
	if err != nil || len(data) == 0 {
		return Image{}, false
	}
	return Image{
		Data:        data,
		ContentType: meta.ContentType,
		PhotoID:     meta.PhotoID,
		StoredAt:    meta.StoredAt,
		ExpiresAt:   meta.ExpiresAt,
	}, true
}

// entryBase is the path prefix for one channel, without a suffix.
//
// Every component is derived from validated data (a configured client key and a
// positive integer), never from a caller-supplied path, so a traversal attempt
// has nowhere to enter.
func (c *Cache) entryBase(clientKey string, channelID int64) string {
	return filepath.Join(c.dir, clientKey, fmt.Sprintf("%d", channelID), "avatar")
}

// validateKey accepts only the same shape the configuration allows for a client
// key, so a key can never contain a separator or a dot segment.
func validateKey(key string) error {
	if key == "" || len(key) > 64 {
		return errors.New("avatarcache: client key is empty or too long")
	}
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(key)-1 {
				return fmt.Errorf("avatarcache: unsafe client key %q", key)
			}
		default:
			return fmt.Errorf("avatarcache: unsafe client key %q", key)
		}
	}
	return nil
}

// Cleanup removes entries older than the stale window.
//
// It walks at most maxScanEntries so it stays cheap on a huge cache, and it
// only ever deletes files it wrote.
func (c *Cache) Cleanup(ctx context.Context) (int, error) {
	cutoff := c.now().Add(-c.ttl - staleGrace)
	removed := 0
	scanned := 0

	err := filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		if scanned++; scanned > maxScanEntries {
			return filepath.SkipAll
		}
		if !strings.HasSuffix(path, metaSuffix) {
			return nil
		}
		meta, err := readMeta(path)
		if err != nil {
			return nil
		}
		if meta.ExpiresAt.After(cutoff) {
			return nil
		}
		base := strings.TrimSuffix(path, metaSuffix)
		for _, suffix := range []string{dataSuffix, metaSuffix} {
			if err := os.Remove(base + suffix); err == nil {
				removed++
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return removed, fmt.Errorf("avatarcache: cleanup: %w", err)
	}
	return removed, nil
}

// ErrNoAvatar means the channel has no profile photo.
var ErrNoAvatar = errors.New("avatarcache: the channel has no avatar")

const (
	dataSuffix = ".img"
	metaSuffix = ".json"
)

// meta is the sidecar describing one cached image.
type meta struct {
	ContentType string    `json:"content_type"`
	PhotoID     int64     `json:"photo_id,omitempty"`
	StoredAt    time.Time `json:"stored_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Bytes       int       `json:"bytes"`
}

// write stores an entry atomically: both files are written to temporary names
// in the same directory and then renamed, so a concurrent reader never sees a
// half-written image.
func (c *Cache) write(base string, img Image) error {
	dir := filepath.Dir(base)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("avatarcache: create %q: %w", dir, err)
	}

	if err := writeFileAtomic(base+dataSuffix, img.Data); err != nil {
		return err
	}
	body, err := json.Marshal(meta{
		ContentType: img.ContentType,
		PhotoID:     img.PhotoID,
		StoredAt:    img.StoredAt,
		ExpiresAt:   img.ExpiresAt,
		Bytes:       len(img.Data),
	})
	if err != nil {
		return fmt.Errorf("avatarcache: encode metadata: %w", err)
	}
	// The sidecar is written last: its presence is what makes the entry
	// readable, so a crash between the two writes leaves no valid entry.
	return writeFileAtomic(base+metaSuffix, body)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("avatarcache: write %q: %w", path, err)
	}
	name := tmp.Name()
	defer os.Remove(name)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("avatarcache: write %q: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("avatarcache: write %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("avatarcache: write %q: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("avatarcache: write %q: %w", path, err)
	}
	return nil
}

func readMeta(path string) (meta, error) {
	var m meta
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.ContentType == "" || m.ExpiresAt.IsZero() {
		return m, errors.New("avatarcache: incomplete metadata")
	}
	return m, nil
}
