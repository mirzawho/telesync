package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/cli"
)

// openStore creates the schema the daemon would create and returns its path.
func openStore(t *testing.T) (string, *channelstore.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "db", "channels.db")
	store, err := channelstore.Open(path, channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return path, store
}

func adminRequest(path string) cli.AdminRequest { return cli.AdminRequest{DatabasePath: path} }

func TestCreateAdministratorStoresAHashAndEveryPermission(t *testing.T) {
	ctx := context.Background()
	path, store := openStore(t)

	res, err := createAdministrator(ctx, adminRequest(path), "admin", "Administrator", "a-strong-password")
	if err != nil {
		t.Fatalf("createAdministrator: %v", err)
	}
	if !res.Created || res.UserID <= 0 || res.Username != "admin" {
		t.Fatalf("result = %+v", res)
	}

	user, ok, err := store.UserByUsername(ctx, "admin")
	if err != nil || !ok {
		t.Fatalf("UserByUsername: %v %v", ok, err)
	}
	if user.PasswordHash == "a-strong-password" || !strings.HasPrefix(user.PasswordHash, "$argon2id$") {
		t.Fatalf("stored hash = %q", user.PasswordHash)
	}
	if ok, err := auth.VerifyPassword(user.PasswordHash, "a-strong-password"); err != nil || !ok {
		t.Fatalf("the stored hash does not verify: %v %v", ok, err)
	}
	if !user.IsActive {
		t.Fatal("the account was created inactive")
	}

	set, err := store.PermissionsOf(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := channelstore.GlobalPermissions()
	if len(set.Global) != len(want) {
		t.Fatalf("global = %v, want every one of %v", set.Global, want)
	}
	authr, err := store.AuthorizerFor(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range want {
		if !authr.Global(p) {
			t.Errorf("the administrator lacks %s", p)
		}
	}
	if !authr.Allowed(channelstore.KindChannel, channelstore.ActionView, 1) ||
		!authr.Allowed(channelstore.KindCategory, channelstore.ActionView, 1) {
		t.Error("the administrator should see every channel and category")
	}
}

func TestCreateAdministratorIsIdempotentAndResetsThePassword(t *testing.T) {
	ctx := context.Background()
	path, store := openStore(t)
	if _, err := createAdministrator(ctx, adminRequest(path), "admin", "Administrator", "first-password"); err != nil {
		t.Fatal(err)
	}
	first, _, _ := store.UserByUsername(ctx, "admin")

	// A re-run replaces the password and keeps every permission it finds.

	res, err := createAdministrator(ctx, adminRequest(path), "admin", "Admin Renamed", "second-password")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.Created {
		t.Fatalf("second run = %+v, want an existing account promoted", res)
	}
	second, ok, _ := store.UserByUsername(ctx, "admin")
	if !ok || second.ID != first.ID || second.Fullname != "Admin Renamed" || !second.IsActive {
		t.Fatalf("account after the second run = %+v", second)
	}
	if ok, _ := auth.VerifyPassword(second.PasswordHash, "first-password"); ok {
		t.Fatal("the old password still verifies")
	}
	if ok, _ := auth.VerifyPassword(second.PasswordHash, "second-password"); !ok {
		t.Fatal("the new password does not verify")
	}
	set, _ := store.PermissionsOf(ctx, first.ID)
	if len(set.Global) != len(channelstore.GlobalPermissions()) {
		t.Fatalf("permissions after the second run = %v", set.Global)
	}

	// Only one account exists: a re-run never creates a duplicate.
	users, total, err := store.ListUsers(ctx, channelstore.UserFilter{})
	if err != nil || total != 1 || len(users) != 1 {
		t.Fatalf("users = %d (total %d), err %v", len(users), total, err)
	}
}

func TestCreateAdministratorNeverRemovesItsOwnAccess(t *testing.T) {
	// A second run cannot leave the account worse: the grants are only added.
	ctx := context.Background()
	path, store := openStore(t)
	if _, err := createAdministrator(ctx, adminRequest(path), "admin", "A", "first-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := createAdministrator(ctx, adminRequest(path), "admin", "A", "second-password"); err != nil {
		t.Fatal(err)
	}
	user, _, _ := store.UserByUsername(ctx, "admin")
	set, _ := store.PermissionsOf(ctx, user.ID)
	if len(set.Global) != len(channelstore.GlobalPermissions()) {
		t.Fatalf("global = %v", set.Global)
	}
}

func TestCreateAdministratorRejectsInvalidInputBeforeTouchingAnything(t *testing.T) {
	ctx := context.Background()
	path, store := openStore(t)

	for name, tc := range map[string]struct {
		username, fullname, password string
	}{
		"short password": {"admin", "A", "short"},
		"no username":    {"", "A", "long-enough"},
		"unsafe name":    {"a b", "A", "long-enough"},
	} {
		if _, err := createAdministrator(ctx, adminRequest(path), tc.username, tc.fullname, tc.password); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, total, _ := store.ListUsers(ctx, channelstore.UserFilter{}); total != 0 {
		t.Fatalf("%d accounts were created by rejected input", total)
	}
}

func TestCreateAdministratorCreatesTheSchemaOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh", "channels.db")
	if _, err := createAdministrator(context.Background(), adminRequest(path), "admin", "A", "long-enough"); err != nil {
		t.Fatalf("first run on a new database: %v", err)
	}
	store, err := channelstore.Open(path, channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok, err := store.UserByUsername(context.Background(), "admin"); err != nil || !ok {
		t.Fatalf("the account is not in the new database: %v %v", ok, err)
	}
}

func TestTheAdministratorCanLogInThroughTheRealAuth(t *testing.T) {
	// The end of the chain: a hash this command wrote verifies, and the
	// permissions it granted are the ones the gateway reads back.
	ctx := context.Background()
	path, store := openStore(t)
	if _, err := createAdministrator(ctx, adminRequest(path), "admin", "A", "a-strong-password"); err != nil {
		t.Fatal(err)
	}
	user, _, _ := store.UserByUsername(ctx, "admin")
	if ok, err := auth.VerifyPassword(user.PasswordHash, "a-strong-password"); err != nil || !ok {
		t.Fatalf("login would fail: %v %v", ok, err)
	}
	authr, err := store.AuthorizerFor(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range channelstore.GlobalPermissions() {
		if !authr.Global(p) {
			t.Errorf("the account logged in without %s", p)
		}
	}
}
