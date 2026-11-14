package staging_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/staging"
)

// Ownership of staged media is what stops one tenant's upload from being sent
// by another. The area is shared on purpose — one directory, one sweep — so the
// guarantee has to come from every read being asked "whose is this?" first.

// writeOne stages one attachment for one owner. The owner is passed rather than
// invented: an attachment staged with no binding is one nothing may read back,
// so Write refuses it, which is the behaviour this file is here to pin.
func writeOne(t *testing.T, s *staging.Staging, name, body string, owner int64, account string) staging.File {
	t.Helper()
	f, err := s.Write(context.Background(), strings.NewReader(body), staging.Meta{
		OriginalName: name,
		MIMEType:     "image/png",
		OwnerUserID:  owner,
		AccountID:    account,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return f
}

func TestAStagedFileIsBoundToItsOwner(t *testing.T) {
	s, err := staging.Open(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f := writeOne(t, s, "photo.png", "alice-bytes", 100, "acc_alice")

	if f.OwnerUserID == 0 || f.AccountID == "" {
		t.Fatalf("a staged file must record its owner and account, got (%d, %q)", f.OwnerUserID, f.AccountID)
	}
}

// A write with no owner is refused. Storing it would produce bytes no read is
// allowed to return — and the alternative, reading it back anyway, is exactly
// the cross-tenant leak this package exists to prevent.
func TestAStagedFileWithNoOwnerIsRefused(t *testing.T) {
	s, err := staging.Open(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, meta := range []staging.Meta{
		{OriginalName: "a.png", MIMEType: "image/png"},
		{OriginalName: "b.png", MIMEType: "image/png", OwnerUserID: 100},
		{OriginalName: "c.png", MIMEType: "image/png", AccountID: "acc_alice"},
	} {
		if _, err := s.Write(context.Background(), strings.NewReader("x"), meta); err == nil {
			t.Fatalf("Write with meta %+v was accepted, want a refusal", meta)
		}
	}
}

func TestAStagedFileCannotBeReadThroughAnotherOwnersName(t *testing.T) {
	// A reference is not a capability. Knowing or guessing a storage name must
	// not let one user read another's upload.
	ctx := context.Background()
	s, err := staging.Open(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f := writeOne(t, s, "photo.png", "alice-bytes", 100, "acc_alice")

	if _, err := s.ReadFor(ctx, 111, "acc_alice", f.Name); err == nil {
		t.Fatal("another owner must not read this staged file")
	}
	if rc, err := s.OpenFor(ctx, 111, "acc_alice", f.Name); err == nil {
		_ = rc.Close()
		t.Fatal("another owner must not open this staged file")
	}

	data, err := s.ReadFor(ctx, 100, "acc_alice", f.Name)
	if err != nil {
		t.Fatalf("the owner must read their own file: %v", err)
	}
	if string(data) != "alice-bytes" {
		t.Fatalf("read %q", string(data))
	}
	rc, err := s.OpenFor(ctx, 100, "acc_alice", f.Name)
	if err != nil {
		t.Fatalf("the owner must open their own file: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || string(got) != "alice-bytes" {
		t.Fatalf("open read %q (%v)", got, err)
	}
}

func TestCleanupNeverRemovesAnotherAccountsActiveMedia(t *testing.T) {
	// The sweep decides what is garbage from a set of names it is told to keep.
	// A keep set built from one account's operations must not be enough to
	// delete another account's still-queued upload.
	ctx := context.Background()
	s, err := staging.Open(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mine := writeOne(t, s, "mine.png", "mine", 100, "acc_alice")
	theirs := writeOne(t, s, "theirs.png", "theirs", 200, "acc_bob")

	// Only "mine" is named as held, by the account that owns it.
	removed, err := s.ReconcileKeeping(ctx, []string{mine.Name}, staging.OwnerFilter{
		OwnerUserID: 100,
		AccountID:   "acc_alice",
	})
	if err != nil {
		t.Fatalf("ReconcileKeeping: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed %d files, want 0: nothing belonging to another account is swept", removed)
	}

	// Both survive, so neither account's queued send lost its attachment.
	for _, name := range []string{mine.Name, theirs.Name} {
		if _, err := s.ReadAll(name); err != nil {
			t.Fatalf("%s was removed by a sweep that did not own it: %v", name, err)
		}
	}
}

func TestAnOwnerSweepOnlyTouchesThatOwnersAbandonedFiles(t *testing.T) {
	ctx := context.Background()
	s, err := staging.Open(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	abandoned := writeOne(t, s, "abandoned.png", "gone", 100, "acc_alice")
	other := writeOne(t, s, "other.png", "still here", 200, "acc_bob")

	removed, err := s.ReconcileKeeping(ctx, nil, staging.OwnerFilter{
		OwnerUserID: abandoned.OwnerUserID,
		AccountID:   abandoned.AccountID,
	})
	if err != nil {
		t.Fatalf("ReconcileKeeping: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want only the owner's own abandoned file", removed)
	}
	if _, err := s.ReadAll(abandoned.Name); err == nil {
		t.Fatal("the abandoned file should be gone")
	}
	if _, err := s.ReadAll(other.Name); err != nil {
		t.Fatalf("a sweep took another owner's file: %v", err)
	}
}

func TestOwnedSweepLeavesUnknownSidecarsUntouched(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := staging.Open(dir, 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	missing := writeOne(t, s, "missing sidecar", "keep-missing", 100, "acc_alice")
	if err := os.Remove(filepath.Join(dir, missing.Name+".own")); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}
	malformed := writeOne(t, s, "malformed sidecar", "keep-malformed", 100, "acc_alice")
	if err := os.WriteFile(filepath.Join(dir, malformed.Name+".own"), []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt sidecar: %v", err)
	}

	removed, err := s.ReconcileOwnedKeeping(ctx, nil, []staging.OwnerFilter{{OwnerUserID: 100, AccountID: "acc_alice"}})
	if err != nil {
		t.Fatalf("ReconcileOwnedKeeping: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed %d files with unknown ownership; want conservative retention", removed)
	}
	for _, name := range []string{missing.Name, malformed.Name} {
		if _, err := s.ReadAll(name); err != nil {
			t.Fatalf("unknown-owner file %q was removed: %v", name, err)
		}
	}
}
