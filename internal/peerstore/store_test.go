package peerstore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mirza/telesync/internal/peerstore"
)

func TestSaveAndFindRoundTrip(t *testing.T) {
	store, err := peerstore.Open(filepath.Join(t.TempDir(), "peers", "official.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(500, 900, "Example", "example"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	hash, ok := store.Find(500)
	if !ok || hash != 900 {
		t.Fatalf("Find = %d, %v", hash, ok)
	}
	if _, ok := store.Find(501); ok {
		t.Fatal("an unknown channel must not resolve")
	}
}

func TestTheStoreSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers", "official.json")
	first, err := peerstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Save(500, 900, "", ""); err != nil {
		t.Fatal(err)
	}

	second, err := peerstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	hash, ok := second.Find(500)
	if !ok || hash != 900 {
		t.Fatalf("Find after reopen = %d, %v", hash, ok)
	}
}

func TestTheFileIsRestricted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers", "official.json")
	store, err := peerstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(500, 900, "", ""); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
}

func TestAnEmptyPathIsRejected(t *testing.T) {
	if _, err := peerstore.Open(""); err == nil {
		t.Fatal("an empty path must be rejected")
	}
}

func TestSaveRejectsZeroValues(t *testing.T) {
	store, err := peerstore.Open(filepath.Join(t.TempDir(), "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(0, 900, "", ""); err == nil {
		t.Fatal("channel id 0 must be rejected")
	}
	if err := store.Save(500, 0, "", ""); err == nil {
		t.Fatal("access hash 0 must be rejected")
	}
}

func TestACorruptedFileIsRecoveredAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := peerstore.Open(path)
	if err != nil {
		t.Fatalf("a corrupted derived cache must not stop the worker: %v", err)
	}
	if store.Len() != 0 {
		t.Fatalf("entries = %d", store.Len())
	}
	if err := store.Save(500, 900, "", ""); err != nil {
		t.Fatalf("the store should still be writable: %v", err)
	}
}
