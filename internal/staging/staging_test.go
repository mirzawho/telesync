package staging_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/staging"
)

// An attachment is handed to this process over one HTTP request and is sent to
// Telegram by another, minutes or a restart later. Nothing about the request
// survives that: not its body, not its reader, and not the name the client
// chose. What survives is what is written here — a server-owned name, the
// bytes, and the client's name kept as metadata and never used as a path.

func newStaging(t *testing.T, max int64) *staging.Staging {
	t.Helper()
	s, err := staging.Open(t.TempDir(), max)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// Test A. A streamed attachment is durable: it lands under a name this server
// chose, with its bytes, size and digest intact.
func TestAStreamedAttachmentIsDurableUnderAServerOwnedName(t *testing.T) {
	s := newStaging(t, 1<<20)
	body := bytes.Repeat([]byte("attachment bytes "), 100)

	got, err := s.Write(context.Background(), bytes.NewReader(body), staging.Meta{
		OriginalName: "report.pdf", MIMEType: "application/pdf",
		OwnerUserID: 1, AccountID: "acc_test",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got.Name == "report.pdf" {
		t.Fatal("the storage name is the client's own file name; the server must choose it")
	}
	if strings.ContainsAny(got.Name, `/\`) || strings.Contains(got.Name, "..") {
		t.Fatalf("storage name %q is a path, not a name", got.Name)
	}
	if got.Size != int64(len(body)) {
		t.Fatalf("size = %d, want %d", got.Size, len(body))
	}
	sum := sha256.Sum256(body)
	if got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %s, want %s", got.SHA256, hex.EncodeToString(sum[:]))
	}
	if got.Original != "report.pdf" {
		t.Fatalf("original name = %q, want the client's name kept as metadata", got.Original)
	}

	read, err := s.ReadAll(got.Name)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(read, body) {
		t.Fatal("the staged bytes are not what was written")
	}
}

// Test B. Two uploads never share a storage name, whatever the client calls
// them: the name is the identity of one staged file and nothing else.
func TestBTwoUploadsOfTheSameFileNameAreTwoFiles(t *testing.T) {
	s := newStaging(t, 1<<20)
	meta := staging.Meta{OriginalName: "photo.jpg", MIMEType: "image/jpeg", OwnerUserID: 1, AccountID: "acc_test"}

	first, err := s.Write(context.Background(), strings.NewReader("one"), meta)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	second, err := s.Write(context.Background(), strings.NewReader("two"), meta)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if first.Name == second.Name {
		t.Fatalf("both uploads were stored as %q", first.Name)
	}
	if first.Original != "photo.jpg" || second.Original != "photo.jpg" {
		t.Fatal("the client's name must survive on both as metadata")
	}
	one, err := s.ReadAll(first.Name)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(one) != "one" {
		t.Fatalf("the first file now reads %q; the second upload overwrote it", one)
	}
}

// Test C. The limit is enforced while the bytes arrive, so a body larger than
// the deployment accepts is refused without being kept, and without being held
// in memory first.
func TestCAnOversizedUploadIsRefusedAndKeptNowhere(t *testing.T) {
	s := newStaging(t, 64)

	_, err := s.Write(context.Background(), bytes.NewReader(make([]byte, 65)), staging.Meta{
		OriginalName: "big.bin", MIMEType: "application/octet-stream",
		OwnerUserID: 1, AccountID: "acc_test",
	})
	if !errors.Is(err, staging.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	names, err := s.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("a refused upload left %d file(s) behind: %v", len(names), names)
	}
}

// Test D. Exactly at the limit is not too large. A boundary that is off by one
// refuses content the deployment said it accepts.
func TestDASizeExactlyAtTheLimitIsAccepted(t *testing.T) {
	s := newStaging(t, 64)
	body := bytes.Repeat([]byte("x"), 64)

	got, err := s.Write(context.Background(), bytes.NewReader(body), staging.Meta{OriginalName: "at.bin", OwnerUserID: 1, AccountID: "acc_test"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got.Size != 64 {
		t.Fatalf("size = %d, want 64", got.Size)
	}
}

// Test E. Nothing a client supplies can name a file here. A storage name is a
// server-generated token, and every read refuses anything that is not one.
func TestENoClientSuppliedNameReachesTheFilesystem(t *testing.T) {
	s := newStaging(t, 1<<20)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hostile := []string{
		"../secret.txt",
		"../../etc/passwd",
		"/etc/passwd",
		filepath.Join("..", "secret.txt"),
		"..",
		".",
		"",
		"a/b",
		"a\\b",
	}
	for _, name := range hostile {
		if _, err := s.ReadAll(name); err == nil {
			t.Fatalf("ReadAll(%q) succeeded; a client name must never resolve a path", name)
		}
		if err := s.Remove(name); err == nil {
			t.Fatalf("Remove(%q) succeeded; a client name must never resolve a path", name)
		}
	}

	// The real staged file is still where it belongs.
	got, err := s.Write(context.Background(), strings.NewReader("mine"), staging.Meta{OriginalName: "x", OwnerUserID: 1, AccountID: "acc_test"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := s.ReadAll(got.Name); err != nil {
		t.Fatalf("ReadAll of a real staged file: %v", err)
	}
}

// Test F. A staged attachment is private to this process: the directory is not
// readable by others and neither is the file.
func TestFStagedFilesArePrivateToThisProcess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "send-staging")
	s, err := staging.Open(dir, 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := s.Write(context.Background(), strings.NewReader("body"), staging.Meta{OriginalName: "x", OwnerUserID: 1, AccountID: "acc_test"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("staging directory mode = %04o; it must not be reachable by group or other", perm)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, got.Name))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("staged file mode = %04o; it must not be reachable by group or other", perm)
	}
}

// Test G. A symlink planted in the staging directory is not a staged file. The
// reader must never follow one out of the directory.
func TestGAPlantedSymlinkIsNotRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "send-staging")
	s, err := staging.Open(dir, 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	name := "0123456789abcdef0123456789abcdef"
	if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	got, err := s.ReadAll(name)
	if err == nil {
		t.Fatalf("ReadAll followed a symlink out of the staging directory and read %q", got)
	}
}

// Test H. Reconciling the directory is what answers a crash between "the bytes
// were written" and "the operation that owns them was recorded": a file nothing
// claims is removed, and one an operation still needs is kept.
func TestHReconcileRemovesWhatNoOperationClaims(t *testing.T) {
	s := newStaging(t, 1<<20)
	orphan, err := s.Write(context.Background(), strings.NewReader("orphan"), staging.Meta{OriginalName: "o", OwnerUserID: 1, AccountID: "acc_test"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	kept, err := s.Write(context.Background(), strings.NewReader("kept"), staging.Meta{OriginalName: "k", OwnerUserID: 1, AccountID: "acc_test"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	removed, err := s.Reconcile(func(name string) bool { return name == kept.Name })
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := s.ReadAll(orphan.Name); !errors.Is(err, staging.ErrNotFound) {
		t.Fatalf("the orphaned file is still there: %v", err)
	}
	if _, err := s.ReadAll(kept.Name); err != nil {
		t.Fatalf("the claimed file was removed: %v", err)
	}
}

// Test I. A write that stops early — the client hung up, the context was
// cancelled — leaves nothing behind, so a retry starts clean and a partial
// upload is never mistaken for a staged attachment.
func TestIAnInterruptedWriteLeavesNothingBehind(t *testing.T) {
	s := newStaging(t, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.Write(ctx, strings.NewReader("half an attachment"), staging.Meta{OriginalName: "x", OwnerUserID: 1, AccountID: "acc_test"})
	if err == nil {
		t.Fatal("Write succeeded after its context was cancelled")
	}
	names, err := s.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("an interrupted write left %d file(s) behind: %v", len(names), names)
	}
}

// Test J. The client's file name is kept as what it is — a label — and is
// never trusted as a path. A name carrying separators is recorded without them
// rather than being walked.
func TestJTheClientsFileNameIsMetadataOnly(t *testing.T) {
	s := newStaging(t, 1<<20)

	got, err := s.Write(context.Background(), strings.NewReader("body"), staging.Meta{
		OriginalName: "../../etc/passwd", MIMEType: "text/plain",
		OwnerUserID: 1, AccountID: "acc_test",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if strings.ContainsAny(got.Original, `/\`) || strings.Contains(got.Original, "..") {
		t.Fatalf("original name %q kept its path syntax", got.Original)
	}
	if got.Original == "" {
		t.Fatal("the original name was dropped entirely; it is metadata and should survive")
	}
	if got.Name != strings.Trim(got.Name, "./") || strings.ContainsAny(got.Name, `/\`) {
		t.Fatalf("storage name %q is not a bare name", got.Name)
	}
}

// Test K. Removing is what cleanup does, and doing it twice is not a failure:
// a retention sweep and a completion cleanup can both run.
func TestKRemoveIsIdempotent(t *testing.T) {
	s := newStaging(t, 1<<20)
	got, err := s.Write(context.Background(), strings.NewReader("body"), staging.Meta{OriginalName: "x", OwnerUserID: 1, AccountID: "acc_test"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Remove(got.Name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s.Remove(got.Name); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if _, err := s.ReadAll(got.Name); !errors.Is(err, staging.ErrNotFound) {
		t.Fatalf("ReadAll after remove = %v, want ErrNotFound", err)
	}
}

// Test L. Opening the staging area is what makes the directory: a deployment
// whose configured directory does not exist yet must not fail its first send.
func TestLOpenCreatesTheDirectoryItWasGiven(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not", "there", "yet")
	s, err := staging.Open(dir, 1<<20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Write(context.Background(), strings.NewReader("body"), staging.Meta{OriginalName: "x", OwnerUserID: 1, AccountID: "acc_test"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
}

// Test M. Reading is streaming, so a send never has to hold the whole
// attachment to know it is there — and a large staged file is not an excuse for
// a large allocation in the reader either.
func TestMOpenStreamsRatherThanLoading(t *testing.T) {
	s := newStaging(t, 1<<20)
	body := bytes.Repeat([]byte("z"), 4096)
	got, err := s.Write(context.Background(), bytes.NewReader(body), staging.Meta{OriginalName: "x", OwnerUserID: 1, AccountID: "acc_test"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, err := s.Open(got.Name)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	head, err := io.ReadAll(io.LimitReader(rc, 4))
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(head) != "zzzz" {
		t.Fatalf("first bytes = %q", head)
	}
}
