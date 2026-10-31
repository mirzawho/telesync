package avatarcache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/avatarcache"
)

// clock is a settable clock so expiry is testable without sleeping.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock { return &clock{at: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func openCache(t *testing.T, clk *clock, mutate ...func(*avatarcache.Options)) *avatarcache.Cache {
	t.Helper()
	opts := avatarcache.Options{Now: clk.Now}
	for _, fn := range mutate {
		fn(&opts)
	}
	c, err := avatarcache.Open(filepath.Join(t.TempDir(), "cache"), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return c
}

// counterFetch counts downloads and returns a stable image.
func counterFetch(calls *int32, data string) avatarcache.FetchFunc {
	return func(context.Context) ([]byte, string, int64, error) {
		atomic.AddInt32(calls, 1)
		return []byte(data), "image/jpeg", 9, nil
	}
}

func TestFirstRequestFetchesAndCaches(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	var calls int32

	img, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "jpeg-a"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(img.Data) != "jpeg-a" || img.ContentType != "image/jpeg" {
		t.Fatalf("image = %+v", img)
	}
	if calls != 1 {
		t.Fatalf("fetches = %d, want 1", calls)
	}
}

func TestARepeatedRequestWithinTheDayServesTheCache(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	var calls int32

	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "jpeg-a")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(23 * time.Hour)

	img, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "jpeg-b"))
	if err != nil {
		t.Fatal(err)
	}
	if string(img.Data) != "jpeg-a" {
		t.Fatalf("bytes = %q, want the cached image", img.Data)
	}
	if calls != 1 {
		t.Fatalf("fetches = %d, the cache should have answered", calls)
	}
}

func TestAnExpiredEntryIsRefreshed(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	var calls int32

	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "jpeg-a")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(25 * time.Hour)

	img, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "jpeg-b"))
	if err != nil {
		t.Fatal(err)
	}
	if string(img.Data) != "jpeg-b" {
		t.Fatalf("bytes = %q, want the refreshed image", img.Data)
	}
	if calls != 2 {
		t.Fatalf("fetches = %d, want 2", calls)
	}
}

func TestCacheEntriesAreIsolatedByClientKey(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	var officialCalls, catCalls int32

	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&officialCalls, "official-a")); err != nil {
		t.Fatal(err)
	}
	img, err := cache.Get(context.Background(), "the-cat", 500, counterFetch(&catCalls, "cat-a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(img.Data) != "cat-a" {
		t.Fatalf("bytes = %q; a different client must not see official's avatar", img.Data)
	}
	if officialCalls != 1 || catCalls != 1 {
		t.Fatalf("official=%d the-cat=%d", officialCalls, catCalls)
	}
}

func TestCacheEntriesAreIsolatedByChannel(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	var calls int32

	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "a")); err != nil {
		t.Fatal(err)
	}
	img, err := cache.Get(context.Background(), "official", 501, counterFetch(&calls, "b"))
	if err != nil {
		t.Fatal(err)
	}
	if string(img.Data) != "b" {
		t.Fatalf("bytes = %q", img.Data)
	}
	if calls != 2 {
		t.Fatalf("fetches = %d", calls)
	}
}

func TestTheCacheSurvivesAReopen(t *testing.T) {
	clk := newClock()
	dir := filepath.Join(t.TempDir(), "cache")
	var calls int32

	first, err := avatarcache.Open(dir, avatarcache.Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Get(context.Background(), "official", 500, counterFetch(&calls, "jpeg")); err != nil {
		t.Fatal(err)
	}

	second, err := avatarcache.Open(dir, avatarcache.Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	img, err := second.Get(context.Background(), "official", 500, counterFetch(&calls, "should-not-fetch"))
	if err != nil {
		t.Fatal(err)
	}
	if string(img.Data) != "jpeg" {
		t.Fatalf("bytes = %q after reopen", img.Data)
	}
	if calls != 1 {
		t.Fatalf("fetches = %d, the reopened cache should have read the file", calls)
	}
}

func TestAFailedFetchIsNotCached(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	fetches := 0

	failing := func(context.Context) ([]byte, string, int64, error) {
		fetches++
		return nil, "", 0, errors.New("telegram is down")
	}
	if _, err := cache.Get(context.Background(), "official", 500, failing); err == nil {
		t.Fatal("a failed fetch must return an error")
	}
	// A second call must try again rather than serve an empty entry.
	if _, err := cache.Get(context.Background(), "official", 500, failing); err == nil {
		t.Fatal("a failed fetch must not have been cached")
	}
	if fetches != 2 {
		t.Fatalf("fetches = %d, want 2", fetches)
	}
}

func TestAMissingAvatarIsReportedAsSuch(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)

	_, err := cache.Get(context.Background(), "official", 500,
		func(context.Context) ([]byte, string, int64, error) {
			return nil, "", 0, avatarcache.ErrNoAvatar
		})
	if !errors.Is(err, avatarcache.ErrNoAvatar) {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrentRequestsCauseOneDownload(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	var calls int32

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			img, err := cache.Get(context.Background(), "official", 500,
				func(ctx context.Context) ([]byte, string, int64, error) {
					atomic.AddInt32(&calls, 1)
					// Widen the window in which a duplicate download would show.
					time.Sleep(20 * time.Millisecond)
					return []byte("jpeg"), "image/jpeg", 9, nil
				})
			if err != nil {
				return
			}
			results[i] = string(img.Data)
		}(i)
	}
	close(start)
	wg.Wait()

	if calls != 1 {
		t.Fatalf("downloads = %d, want one shared download", calls)
	}
}

func TestAnExpiredEntryIsServedBrieflyWhenTheRefreshFails(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	var calls int32

	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "old")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(25 * time.Hour)

	img, err := cache.Get(context.Background(), "official", 500,
		func(context.Context) ([]byte, string, int64, error) {
			return nil, "", 0, errors.New("telegram is down")
		})
	if err != nil {
		t.Fatalf("a recent-enough entry should be served stale: %v", err)
	}
	if !img.Stale || string(img.Data) != "old" {
		t.Fatalf("image = %+v", img)
	}
}

func TestAVeryOldEntryIsNotServed(t *testing.T) {
	clk := newClock()
	cache := openCache(t, clk)
	var calls int32

	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "old")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(48 * time.Hour)

	_, err := cache.Get(context.Background(), "official", 500,
		func(context.Context) ([]byte, string, int64, error) {
			return nil, "", 0, errors.New("telegram is down")
		})
	if err == nil {
		t.Fatal("an entry past the stale window must not be served")
	}
}

func TestASidecarWithoutBytesIsAMiss(t *testing.T) {
	clk := newClock()
	dir := filepath.Join(t.TempDir(), "cache")
	cache, err := avatarcache.Open(dir, avatarcache.Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "jpeg")); err != nil {
		t.Fatal(err)
	}

	// Delete the image but leave the metadata, as a crash between the two
	// writes would.
	img := filepath.Join(dir, "official", "500", "avatar.img")
	if err := os.Remove(img); err != nil {
		t.Fatal(err)
	}

	got, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "again"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "again" {
		t.Fatalf("bytes = %q", got.Data)
	}
	if calls != 2 {
		t.Fatalf("fetches = %d, a half-written entry must be a miss", calls)
	}
}

func TestUnsafeClientKeysAreRejected(t *testing.T) {
	cache := openCache(t, newClock())
	var calls int32
	for _, key := range []string{"", "../etc", "a/b", "Upper", "-lead", "trail-", "a.b"} {
		if _, err := cache.Get(context.Background(), key, 500, counterFetch(&calls, "x")); err == nil {
			t.Fatalf("client key %q must be rejected", key)
		}
	}
	if calls != 0 {
		t.Fatalf("fetches = %d; nothing should have been downloaded", calls)
	}
}

func TestANonPositiveChannelIsRejected(t *testing.T) {
	cache := openCache(t, newClock())
	var calls int32
	for _, id := range []int64{0, -1} {
		if _, err := cache.Get(context.Background(), "official", id, counterFetch(&calls, "x")); err == nil {
			t.Fatalf("channel id %d must be rejected", id)
		}
	}
}

func TestOversizedImagesAreRejected(t *testing.T) {
	cache := openCache(t, newClock())
	huge := make([]byte, avatarcache.MaxAvatarBytes+1)
	_, err := cache.Get(context.Background(), "official", 500,
		func(context.Context) ([]byte, string, int64, error) {
			return huge, "image/jpeg", 1, nil
		})
	if err == nil {
		t.Fatal("an oversized avatar must be rejected")
	}
}

func TestTheContentTypeComesFromTheFetcher(t *testing.T) {
	cache := openCache(t, newClock())
	img, err := cache.Get(context.Background(), "official", 500,
		func(context.Context) ([]byte, string, int64, error) {
			return []byte("png"), "image/png", 3, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if img.ContentType != "image/png" {
		t.Fatalf("content type = %q", img.ContentType)
	}

	// An unknown type falls back to the safe default, not a guess.
	img, err = cache.Get(context.Background(), "official", 501,
		func(context.Context) ([]byte, string, int64, error) {
			return []byte("?"), "", 3, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if img.ContentType != "application/octet-stream" {
		t.Fatalf("content type = %q", img.ContentType)
	}
}

func TestCleanupRemovesOnlyExpiredEntries(t *testing.T) {
	clk := newClock()
	// A one-hour TTL keeps the stale window and the fresh entry far apart.
	cache := openCache(t, clk, func(o *avatarcache.Options) { o.TTL = time.Hour })
	var calls int32

	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "old")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(7*time.Hour + 30*time.Minute)
	if _, err := cache.Get(context.Background(), "official", 501, counterFetch(&calls, "new")); err != nil {
		t.Fatal(err)
	}

	// 8h in: the first entry is past TTL+grace, the second is still valid.
	clk.Advance(30 * time.Minute)
	removed, err := cache.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if removed == 0 {
		t.Fatal("the old entry should have been removed")
	}

	before := calls
	if _, err := cache.Get(context.Background(), "official", 501, counterFetch(&calls, "x")); err != nil {
		t.Fatal(err)
	}
	if calls != before {
		t.Fatal("cleanup removed a live entry")
	}
	if _, err := cache.Get(context.Background(), "official", 500, counterFetch(&calls, "refetched")); err != nil {
		t.Fatal(err)
	}
	if calls != before+1 {
		t.Fatal("the cleaned entry should have been fetched again")
	}
}

func TestCleanupLeavesTheCacheUsableOnAnEmptyDirectory(t *testing.T) {
	cache := openCache(t, newClock())
	removed, err := cache.Cleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d", removed)
	}
}

func TestOpenRejectsARelativeDirectory(t *testing.T) {
	if _, err := avatarcache.Open("cache", avatarcache.Options{}); err == nil {
		t.Fatal("a relative cache directory must be rejected")
	}
}

func TestTheCacheDirectoryIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if _, err := avatarcache.Open(dir, avatarcache.Options{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("cache directory mode = %o, want 700", perm)
	}
}
