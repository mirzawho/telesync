package sessionstore_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/session"

	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/sessionstore"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, secrets.KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func testEncrypter(t *testing.T) *secrets.Encrypter {
	t.Helper()
	e, err := secrets.NewEncrypter(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// sessionForAccount builds an account-scoped sealed store in a temp directory.
func sessionForAccount(t *testing.T, dir, accountID string) (*sessionstore.AccountBox, error) {
	t.Helper()
	return sessionstore.NewAccountBox(sessionstore.AccountBoxConfig{
		Dir:         dir,
		AccountID:   accountID,
		Encrypter:   testEncrypter(t),
		KeyVersion:  1,
		NetworkKey:  "cat",
		NetworkType: "custom",
	})
}

func TestAccountBoxRoundTripsItsSealedSession(t *testing.T) {
	dir := t.TempDir()
	store, err := sessionForAccount(t, dir, "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}

	session := []byte(`{"Version":1,"Data":{"AuthKey":"session-bytes-for-one-account"}}`)
	if err := store.StoreSession(nil, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	got, err := store.LoadSession(nil)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !bytes.Equal(got, session) {
		t.Fatalf("LoadSession = %q, want %q", got, session)
	}
}

func TestAccountBoxWritesTheSealedBoxAndNeverTheSessionBytes(t *testing.T) {
	dir := t.TempDir()
	const accountID = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	store, err := sessionForAccount(t, dir, accountID)
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}

	marker := "SESSION-MARKER-THAT-MUST-NOT-APPEAR-ON-DISK"
	session := []byte(`{"Version":1,"Data":{"AuthKey":"` + marker + `"}}`)
	if err := store.StoreSession(nil, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	path := filepath.Join(dir, accountID+".box")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the sealed file: %v", err)
	}
	if bytes.Contains(raw, session) {
		t.Fatal("the session file holds the raw session bytes")
	}
	if bytes.Contains(raw, []byte(marker)) {
		t.Fatal("the session file leaks session key material")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("session file permission = %v, want 0600", perm)
	}
	if dirInfo, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("session directory permission = %v, want 0700", perm)
	}
}

func TestAccountBoxPathsAreDerivedFromTheOpaqueAccountID(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{
		"acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		"acc_bbbbbbbbbbbbbbbbbbbbbbbbbb",
	} {
		store, err := sessionForAccount(t, dir, id)
		if err != nil {
			t.Fatalf("NewAccountBox(%s): %v", id, err)
		}
		if want := filepath.Join(dir, id+".box"); store.Path() != want {
			t.Fatalf("Path() = %q, want %q", store.Path(), want)
		}
	}
	// A username or a phone number never appears in a path.
	for _, leaked := range []string{"@", "+", "user", "phone"} {
		if strings.Contains(filepath.Base(filepath.Join(dir, "acc_x.box")), leaked) {
			t.Fatalf("a path contains %q", leaked)
		}
	}
}

func TestAccountBoxRefusesAnIDThatEscapesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{
		"",
		"   ",
		"..",
		"../escape",
		"acc_../../etc/passwd",
		"acc_with_separator" + string(filepath.Separator),
		"/abs/path",
		"user@example.com",
		"+491234567",
	} {
		if _, err := sessionForAccount(t, dir, id); err == nil {
			t.Fatalf("NewAccountBox(%q) accepted an unsafe account id", id)
		}
	}
}

func TestAccountBoxRefusesAProfileThatMixesClientsOrNetworks(t *testing.T) {
	dir := t.TempDir()
	const accountID = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"

	for _, cfg := range []sessionstore.AccountBoxConfig{
		{Dir: dir, AccountID: accountID, Encrypter: testEncrypter(t), KeyVersion: 1, NetworkType: "custom"},
		{Dir: dir, AccountID: accountID, Encrypter: testEncrypter(t), KeyVersion: 1, NetworkKey: "cat", NetworkType: ""},
	} {
		if _, err := sessionstore.NewAccountBox(cfg); err == nil {
			t.Fatalf("NewAccountBox(%+v) accepted an incomplete profile", cfg)
		}
	}

	store, err := sessionForAccount(t, dir, accountID)
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if err := store.StoreSession(nil, []byte("session")); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// The same account may not be opened under another server definition or
	// another network kind: that would be one session used as two clients.
	other := sessionstore.AccountBoxConfig{
		Dir:         dir,
		AccountID:   accountID,
		Encrypter:   testEncrypter(t),
		KeyVersion:  1,
		NetworkKey:  "official",
		NetworkType: "custom",
	}
	if _, err := sessionstore.NewAccountBox(other); err == nil {
		t.Fatal("NewAccountBox accepted another server definition for the same session")
	}
	mismatched := sessionstore.AccountBoxConfig{
		Dir:         dir,
		AccountID:   accountID,
		Encrypter:   testEncrypter(t),
		KeyVersion:  1,
		NetworkKey:  "cat",
		NetworkType: "official",
	}
	if _, err := sessionstore.NewAccountBox(mismatched); err == nil {
		t.Fatal("NewAccountBox accepted another network kind for the same session")
	}
}

func TestAccountBoxesForTwoAccountsDoNotCollide(t *testing.T) {
	dir := t.TempDir()
	const (
		one = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
		two = "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	first, err := sessionForAccount(t, dir, one)
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	second, err := sessionForAccount(t, dir, two)
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if first.Path() == second.Path() {
		t.Fatalf("two accounts share one path %q", first.Path())
	}

	if err := first.StoreSession(nil, []byte("the first account's session")); err != nil {
		t.Fatalf("StoreSession(first): %v", err)
	}
	if err := second.StoreSession(nil, []byte("the second account's session")); err != nil {
		t.Fatalf("StoreSession(second): %v", err)
	}

	got, err := first.LoadSession(nil)
	if err != nil {
		t.Fatalf("LoadSession(first): %v", err)
	}
	if string(got) != "the first account's session" {
		t.Fatalf("first account read %q", got)
	}
	got, err = second.LoadSession(nil)
	if err != nil {
		t.Fatalf("LoadSession(second): %v", err)
	}
	if string(got) != "the second account's session" {
		t.Fatalf("second account read %q", got)
	}

	// Each account's own box is sealed under its own context, so one worker
	// cannot open the other's file even if it were pointed at the path.
	other, err := sessionstore.NewAccountBox(sessionstore.AccountBoxConfig{
		Dir:         dir,
		AccountID:   two,
		Encrypter:   testEncrypter(t),
		KeyVersion:  1,
		NetworkKey:  "cat",
		NetworkType: "custom",
	})
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if _, err := other.OpenFile(first.Path()); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("opening one account's file as another = %v, want ErrDecrypt", err)
	}
}

func TestOpenFileRefusesAnotherAccountsContext(t *testing.T) {
	dir := t.TempDir()
	const (
		one = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
		two = "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	mine, err := sessionForAccount(t, dir, one)
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if err := mine.StoreSession(nil, []byte("one account's session")); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	theirs, err := sessionForAccount(t, dir, two)
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if _, err := theirs.OpenFile(mine.Path()); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenFile(other account) = %v, want ErrDecrypt", err)
	}

	// The same account under another key version is a different context too.
	rotated, err := sessionstore.NewAccountBox(sessionstore.AccountBoxConfig{
		Dir:         dir,
		AccountID:   one,
		Encrypter:   testEncrypter(t),
		KeyVersion:  2,
		NetworkKey:  "cat",
		NetworkType: "custom",
	})
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if _, err := rotated.OpenFile(mine.Path()); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenFile(other key version) = %v, want ErrDecrypt", err)
	}
}

func TestOpenFileRefusesATamperedSealedBox(t *testing.T) {
	dir := t.TempDir()
	const accountID = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	store, err := sessionForAccount(t, dir, accountID)
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if err := store.StoreSession(nil, []byte("session to tamper with")); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	path := filepath.Join(dir, accountID+".box")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)/2] ^= 0x01
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.LoadSession(nil); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("LoadSession(tampered) = %v, want ErrDecrypt", err)
	}
	if _, err := store.OpenFile(path); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("OpenFile(tampered) = %v, want ErrDecrypt", err)
	}
}

func TestLoadSessionReportsAMissingFileAsNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := sessionForAccount(t, dir, "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if _, err := store.LoadSession(nil); err == nil {
		t.Fatal("LoadSession on a missing file succeeded")
	} else if !errors.Is(err, sessionstore.ErrSessionNotFound) {
		t.Fatalf("LoadSession error = %v, want ErrSessionNotFound", err)
	} else if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("LoadSession error = %v, want gotd's session.ErrNotFound", err)
	}
}

func TestABoxSealedWithoutAnAccountContextIsRefused(t *testing.T) {
	dir := t.TempDir()
	store, err := sessionForAccount(t, dir, "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	// A box written outside the account layer (a raw Seal) carries no binding
	// to the account, so it must never be accepted as that account's session.
	enc := testEncrypter(t)
	box, err := enc.Seal("session sealed with no context")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteBox(box); err == nil {
		t.Fatal("WriteBox accepted a box with no account context")
	}
	if _, err := store.OpenFile(store.Path()); err == nil {
		t.Fatal("OpenFile opened a file with no account context")
	}
}

func TestSealedSessionBytesAreNeverReturnedInStateOrStringForms(t *testing.T) {
	dir := t.TempDir()
	store, err := sessionForAccount(t, dir, "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	marker := "SESSION-MARKER-NEVER-SURFACED"
	if err := store.StoreSession(nil, []byte(`{"Data":{"AuthKey":"`+marker+`"}}`)); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	state := sessionstore.Inspect(store.Path())
	if strings.Contains(state.String(), marker) {
		t.Fatalf("Inspect state leaks session bytes: %s", state.String())
	}
	if strings.Contains(store.String(), marker) {
		t.Fatalf("AccountBox string form leaks session bytes: %s", store.String())
	}
	if strings.Contains(store.Path(), marker) {
		t.Fatal("the path leaks session bytes")
	}
}

func TestAccountBoxPersistsThePrivateCodeHashEncryptedUnderItsContext(t *testing.T) {
	dir := t.TempDir()
	const (
		accountID = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
		hash      = "private-code-hash-marker"
	)
	box, err := sessionForAccount(t, dir, accountID)
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if err := box.StorePhoneCodeHash(hash); err != nil {
		t.Fatalf("StorePhoneCodeHash: %v", err)
	}
	got, err := box.LoadPhoneCodeHash()
	if err != nil {
		t.Fatalf("LoadPhoneCodeHash: %v", err)
	}
	if got != hash {
		t.Fatalf("LoadPhoneCodeHash = %q, want the server's handle", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(hash)) {
			t.Fatalf("private phone-code hash is stored in cleartext in %q", entry.Name())
		}
	}

	other, err := sessionForAccount(t, dir, "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatalf("NewAccountBox(other): %v", err)
	}
	hashFile := strings.TrimSuffix(box.Path(), ".box") + ".login"
	otherHashFile := strings.TrimSuffix(other.Path(), ".box") + ".login"
	raw, err := os.ReadFile(hashFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherHashFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := other.LoadPhoneCodeHash(); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("LoadPhoneCodeHash under another account = %v, want ErrDecrypt", err)
	}
}

func TestAccountBoxClearsPrivateCodeHashAndDiscardsTemporarySession(t *testing.T) {
	dir := t.TempDir()
	box, err := sessionForAccount(t, dir, "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("NewAccountBox: %v", err)
	}
	if err := box.StoreSession(nil, []byte("temporary auth session")); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	if err := box.StorePhoneCodeHash("private-code-hash"); err != nil {
		t.Fatalf("StorePhoneCodeHash: %v", err)
	}
	if err := box.ClearPhoneCodeHash(); err != nil {
		t.Fatalf("ClearPhoneCodeHash: %v", err)
	}
	if _, err := box.LoadPhoneCodeHash(); !errors.Is(err, sessionstore.ErrPhoneCodeHashNotFound) {
		t.Fatalf("LoadPhoneCodeHash after clear = %v, want ErrPhoneCodeHashNotFound", err)
	}
	if err := box.Discard(); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, err := os.Stat(box.Path()); !os.IsNotExist(err) {
		t.Fatalf("session file after discard stat error = %v, want not-exist", err)
	}
}
