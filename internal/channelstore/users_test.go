package channelstore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

func mustUser(t *testing.T, store *channelstore.Store, username string, active bool) channelstore.User {
	t.Helper()
	u, err := store.CreateUser(context.Background(), "User "+username, username, "encoded-hash", active)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", username, err)
	}
	return u
}

func grant(t *testing.T, store *channelstore.Store, id int64, globals ...channelstore.GlobalPermission) {
	t.Helper()
	if err := store.ReplacePermissions(context.Background(), id, channelstore.PermissionSet{Global: globals}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
}

func TestCreateUserStoresTheHashAndTrimsTheProfile(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	u, err := store.CreateUser(ctx, "  Example User ", " Example ", "hash-value", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID <= 0 || u.Fullname != "Example User" || u.Username != "Example" {
		t.Fatalf("user = %+v", u)
	}
	if u.PasswordHash != "hash-value" {
		t.Fatalf("hash = %q, want it stored as given", u.PasswordHash)
	}
	if !u.IsActive {
		t.Fatal("is_active was not stored")
	}
	if u.CreatedAt.IsZero() || !u.CreatedAt.Equal(u.UpdatedAt) {
		t.Fatalf("timestamps = %v / %v", u.CreatedAt, u.UpdatedAt)
	}
}

func TestCreateUserRejectsABlankPasswordHash(t *testing.T) {
	if _, err := open(t).CreateUser(context.Background(), "A", "a", "  ", true); !errors.Is(err, channelstore.ErrInvalidUser) {
		t.Fatalf("err = %v, want ErrInvalidUser", err)
	}
}

func TestCreateUserRejectsDuplicateUsernamesIgnoringCase(t *testing.T) {
	store := open(t)
	mustUser(t, store, "example", true)
	_, err := store.CreateUser(context.Background(), "Other", "EXAMPLE", "h", true)
	if !errors.Is(err, channelstore.ErrDuplicateUsername) {
		t.Fatalf("err = %v, want ErrDuplicateUsername", err)
	}
}

func TestNormalizeUserRejectsUnsafeValues(t *testing.T) {
	tests := []struct{ fullname, username string }{
		{"", "a"},
		{"   ", "a"},
		{"A", ""},
		{"A", "has space"},
		{"A", "semi;colon"},
		{"A", "ünïcode"},
		{"A", strings.Repeat("a", channelstore.MaxUsernameLen+1)},
		{"A", "tab\there"},
	}
	for _, tt := range tests {
		if _, _, err := channelstore.NormalizeUser(tt.fullname, tt.username); !errors.Is(err, channelstore.ErrInvalidUser) {
			t.Errorf("NormalizeUser(%q, %q) = %v, want ErrInvalidUser", tt.fullname, tt.username, err)
		}
	}
	for _, ok := range []string{"admin", "a.b_c-d", "User1"} {
		if _, _, err := channelstore.NormalizeUser("A", ok); err != nil {
			t.Errorf("NormalizeUser username %q: %v", ok, err)
		}
	}
}

func TestValidatePasswordEnforcesALength(t *testing.T) {
	if err := channelstore.ValidatePassword("short"); !errors.Is(err, channelstore.ErrInvalidUser) {
		t.Fatalf("err = %v, want ErrInvalidUser", err)
	}
	if err := channelstore.ValidatePassword("long-enough"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestGetUserAndUserByUsernameMissCleanly(t *testing.T) {
	store := open(t)
	if _, ok, err := store.GetUser(context.Background(), 404); err != nil || ok {
		t.Fatalf("GetUser = %v, %v", ok, err)
	}
	if _, ok, err := store.UserByUsername(context.Background(), "nobody"); err != nil || ok {
		t.Fatalf("UserByUsername = %v, %v", ok, err)
	}
	u := mustUser(t, store, "Admin", true)
	got, ok, err := store.UserByUsername(context.Background(), "admin")
	if err != nil || !ok || got.ID != u.ID {
		t.Fatalf("UserByUsername = %+v, %v, %v", got, ok, err)
	}
}

func TestUpdateUserAppliesPartialChanges(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "example", true)

	name := "Renamed"
	hash := "new-hash"
	updated, err := store.UpdateUser(ctx, u.ID, channelstore.UserUpdate{Fullname: &name, PasswordHash: &hash})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.Fullname != "Renamed" || updated.Username != "example" || updated.PasswordHash != "new-hash" {
		t.Fatalf("updated = %+v", updated)
	}
	if !updated.CreatedAt.Equal(u.CreatedAt) {
		t.Fatalf("created_at changed: %v -> %v", u.CreatedAt, updated.CreatedAt)
	}
}

func TestUpdateUserRejectsADuplicateUsername(t *testing.T) {
	store := open(t)
	mustUser(t, store, "first", true)
	second := mustUser(t, store, "second", true)
	other := "FIRST"
	_, err := store.UpdateUser(context.Background(), second.ID, channelstore.UserUpdate{Username: &other})
	if !errors.Is(err, channelstore.ErrDuplicateUsername) {
		t.Fatalf("err = %v, want ErrDuplicateUsername", err)
	}
}

func TestUpdateUserMissesCleanly(t *testing.T) {
	name := "x"
	_, err := open(t).UpdateUser(context.Background(), 99, channelstore.UserUpdate{Fullname: &name})
	if !errors.Is(err, channelstore.ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestListUsersPaginatesSearchesAndFiltersByID(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	// The shared fixture owns an account of its own, so it carries a user too.
	// The arithmetic under test is paging over whatever exists, not a hardcoded
	// count that a fixture change would silently invalidate.
	_, before, err := store.ListUsers(ctx, channelstore.UserFilter{})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"alice", "bob", "carol"} {
		u, err := store.CreateUser(ctx, "Full "+name, name, "h", true)
		if err != nil {
			t.Fatal(err)
		}
		_ = u
	}
	want := before + 3

	page, total, err := store.ListUsers(ctx, channelstore.UserFilter{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != want || len(page) != 2 {
		t.Fatalf("page=%d total=%d, want 2 on a page and %d in total", len(page), total, want)
	}

	found, total, err := store.ListUsers(ctx, channelstore.UserFilter{Query: "CAR"})
	if err != nil || total != 1 || len(found) != 1 || found[0].Username != "carol" {
		t.Fatalf("search = %v %d %v", found, total, err)
	}

	only, total, err := store.ListUsers(ctx, channelstore.UserFilter{IDs: []int64{found[0].ID}})
	if err != nil || total != 1 || len(only) != 1 {
		t.Fatalf("ids = %v %d %v", only, total, err)
	}
	none, total, err := store.ListUsers(ctx, channelstore.UserFilter{IDs: []int64{}})
	if err != nil || total != 0 || len(none) != 0 {
		t.Fatalf("no ids = %v %d %v", none, total, err)
	}
}

func TestDeleteUserRequiresAnotherActiveAdministrator(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	admin := mustUser(t, store, "admin", true)
	grant(t, store, admin.ID, channelstore.PermissionUsersManage)

	if err := store.DeleteUser(ctx, admin.ID); !errors.Is(err, channelstore.ErrLastAdministrator) {
		t.Fatalf("err = %v, want ErrLastAdministrator", err)
	}

	// A second administrator leaves the first free to go.
	second := mustUser(t, store, "second", true)
	grant(t, store, second.ID, channelstore.PermissionUsersManage)
	if err := store.DeleteUser(ctx, admin.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, ok, _ := store.GetUser(ctx, admin.ID); ok {
		t.Fatal("the user is still there")
	}
}

func TestDeactivatingTheLastAdministratorIsRefused(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	admin := mustUser(t, store, "admin", true)
	grant(t, store, admin.ID, channelstore.PermissionUsersManage)

	inactive := false
	if _, err := store.UpdateUser(ctx, admin.ID, channelstore.UserUpdate{IsActive: &inactive}); !errors.Is(err, channelstore.ErrLastAdministrator) {
		t.Fatalf("err = %v, want ErrLastAdministrator", err)
	}

	// An inactive administrator does not count as one.
	other := mustUser(t, store, "other", false)
	grant(t, store, other.ID, channelstore.PermissionUsersManage)
	if _, err := store.UpdateUser(ctx, admin.ID, channelstore.UserUpdate{IsActive: &inactive}); !errors.Is(err, channelstore.ErrLastAdministrator) {
		t.Fatalf("err = %v, want ErrLastAdministrator", err)
	}
}

func TestUsersWithoutUsersManageDoNotCountAsAdministrators(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	admin := mustUser(t, store, "admin", true)
	grant(t, store, admin.ID, channelstore.PermissionUsersManage)
	viewerOnly := mustUser(t, store, "viewer", true)
	grant(t, store, viewerOnly.ID, channelstore.PermissionUsersView)

	inactive := false
	if _, err := store.UpdateUser(ctx, admin.ID, channelstore.UserUpdate{IsActive: &inactive}); !errors.Is(err, channelstore.ErrLastAdministrator) {
		t.Fatalf("err = %v, want ErrLastAdministrator", err)
	}
}

func TestDeletingAUserRemovesEveryAssignment(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "admin", true)
	cat, err := store.CreateCategory(ctx, "News", "")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Upsert(ctx, channel("official", 1001))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Global:     []channelstore.GlobalPermission{channelstore.PermissionCategoriesView},
		Channels:   []channelstore.ResourcePermissions{{ResourceID: stored.ID, Permissions: []channelstore.ResourcePermission{{channelstore.ActionView, channelstore.EffectAllow}}}},
		Categories: []channelstore.ResourcePermissions{{ResourceID: cat.ID, Permissions: []channelstore.ResourcePermission{{channelstore.ActionUpdate, channelstore.EffectDeny}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	set, err := store.PermissionsOf(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Global)+len(set.Channels)+len(set.Categories) != 0 {
		t.Fatalf("assignments survived the delete: %+v", set)
	}
}

func TestSetPasswordHashRecoversAnAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "admin", true)
	if err := store.SetPasswordHash(ctx, u.ID, "replacement"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := store.GetUser(ctx, u.ID)
	if got.PasswordHash != "replacement" {
		t.Fatalf("hash = %q", got.PasswordHash)
	}
	if err := store.SetPasswordHash(ctx, 999, "x"); !errors.Is(err, channelstore.ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestUserIDsWithAnyPermission(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	a := mustUser(t, store, "a", true)
	mustUser(t, store, "b", true)
	grant(t, store, a.ID, channelstore.PermissionUsersManage)
	ids, err := store.UserIDsWithAnyPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids) != fmt.Sprint([]int64{a.ID}) {
		t.Fatalf("ids = %v", ids)
	}
}

func TestUsersReadBackTheirActiveFlag(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	on := mustUser(t, store, "on", true)
	mustUser(t, store, "off", false)
	got, ok, err := store.GetUser(ctx, on.ID)
	if err != nil || !ok || !got.IsActive {
		t.Fatalf("active user = %+v %v %v", got, ok, err)
	}
	users, _, err := store.ListUsers(ctx, channelstore.UserFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.Username == "off" && u.IsActive {
			t.Fatalf("an inactive user reads as active: %+v", u)
		}
	}
}
