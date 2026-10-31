package channelstore_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/secrets"
)

// box seals a credential with a key only the test holds.
func testBox(t *testing.T, plaintext string) secrets.Box {
	t.Helper()
	enc, err := secrets.NewEncrypter(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	box, err := enc.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

// site is a valid WordPress site record.
func site(url, username string) channelstore.WordPressSite {
	return channelstore.WordPressSite{
		URL:       url,
		Title:     "A Site",
		Username:  username,
		CreatedAt: timeAt(2026, 10, 1),
		UpdatedAt: timeAt(2026, 10, 1),
	}
}

func TestWordPressSitesRoundTripWithoutAPlaintextCredential(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	password := "abcd EFGH 1234 ijkl"

	enc, err := secrets.NewEncrypter(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	box, err := enc.Seal(password)
	if err != nil {
		t.Fatal(err)
	}
	rec := site("https://example.com", "editor")
	rec.Credential = box
	icon := "https://example.com/icon.png"
	rec.IconURL = &icon

	created, err := store.CreateWordPressSite(ctx, rec)
	if err != nil {
		t.Fatalf("CreateWordPressSite: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("no id was assigned")
	}

	got, found, err := store.GetWordPressSite(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("GetWordPressSite: found=%v err=%v", found, err)
	}
	if got.Credential.Version != secrets.Version1 || got.Credential.Plaintext() {
		t.Fatalf("credential = %+v", got.Credential.Version)
	}
	plain, err := enc.Open(got.Credential)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if plain != password {
		t.Fatal("the stored credential does not open to the password")
	}
	if got.IconURL == nil || *got.IconURL != icon {
		t.Fatalf("IconURL = %v", got.IconURL)
	}

	list, err := store.ListWordPressSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}
}

func TestTheDatabaseHoldsNoPlaintextCredential(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	store, err := channelstore.Open(path, channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	password := "correct horse battery staple wp"
	rec := site("https://secret.example.com", "admin")
	rec.Credential = testBox(t, password)
	if _, err := store.CreateWordPressSite(ctx, rec); err != nil {
		t.Fatal(err)
	}

	_ = store.Close()

	// And the file itself.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(password)) {
		t.Fatal("the password appears in the database file")
	}
	if bytes.Contains(raw, []byte("battery")) {
		t.Fatal("part of the password appears in the database file")
	}
	// The WAL holds pages that were not checkpointed yet. A WAL holding a
	// plaintext credential would be the same leak, so it is checked too; WAL
	// header pages are the only ones with a readable page number, and a file
	// without the password is what this asserts.
	if wal, err := os.ReadFile(path + "-wal"); err == nil && bytes.Contains(wal, []byte(password)) {
		t.Fatal("the password appears in the write-ahead log")
	}
}

func TestCreateWordPressSiteRefusesASecondURL(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	rec := site("https://example.com", "a")
	rec.Credential = testBox(t, "one")
	if _, err := store.CreateWordPressSite(ctx, rec); err != nil {
		t.Fatal(err)
	}
	again := site("https://example.com", "b")
	again.Credential = testBox(t, "two")
	if _, err := store.CreateWordPressSite(ctx, again); !errors.Is(err, channelstore.ErrDuplicateSite) {
		t.Fatalf("err = %v, want ErrDuplicateSite", err)
	}
}

func TestWordPressSiteURLsAreUniqueIgnoringCase(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	rec := site("https://example.com/blog", "a")
	rec.Credential = testBox(t, "one")
	if _, err := store.CreateWordPressSite(ctx, rec); err != nil {
		t.Fatal(err)
	}
	second := site("https://EXAMPLE.com/BLOG", "b")
	second.Credential = testBox(t, "two")
	if _, err := store.CreateWordPressSite(ctx, second); !errors.Is(err, channelstore.ErrDuplicateSite) {
		t.Fatalf("err = %v, want ErrDuplicateSite", err)
	}
}

func TestCreateWordPressSiteValidatesTheRecord(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	cases := map[string]func(*channelstore.WordPressSite){
		"no url":        func(r *channelstore.WordPressSite) { r.URL = "" },
		"no username":   func(r *channelstore.WordPressSite) { r.Username = "  " },
		"no credential": func(r *channelstore.WordPressSite) { r.Credential = secrets.Box{} },
		"plaintext":     func(r *channelstore.WordPressSite) { r.Credential = secrets.Box{Version: 0} },
		"credentials":   func(r *channelstore.WordPressSite) { r.URL = "https://u:p@example.com" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rec := site("https://example.com", "admin")
			rec.Credential = testBox(t, "pw")
			mutate(&rec)
			if _, err := store.CreateWordPressSite(ctx, rec); !errors.Is(err, channelstore.ErrInvalidSite) {
				t.Fatalf("err = %v, want ErrInvalidSite", err)
			}
		})
	}
}

func TestGetWordPressSiteReportsAMissingRow(t *testing.T) {
	store := open(t)
	_, found, err := store.GetWordPressSite(context.Background(), 404)
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestUpdateWordPressSiteKeepsWhatIsOmitted(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	rec := site("https://example.com", "admin")
	rec.Credential = testBox(t, "original-password")
	created, err := store.CreateWordPressSite(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}

	next := created
	next.Title = "Renamed"
	next.UpdatedAt = timeAt(2026, 10, 2)
	// Reopen the same file with a later clock, which is what a later
	// update (in the daemon, the same process) sees.
	store = openAt(t, store.Path(), func() time.Time { return timeAt(2026, 10, 2) })
	updated, err := store.UpdateWordPressSite(ctx, next)
	if err != nil {
		t.Fatalf("UpdateWordPressSite: %v", err)
	}
	if updated.Title != "Renamed" {
		t.Fatalf("Title = %q", updated.Title)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at changed: %v", updated.CreatedAt)
	}
	if !updated.UpdatedAt.Equal(timeAt(2026, 10, 2)) {
		t.Fatalf("updated_at = %v", updated.UpdatedAt)
	}
	enc, _ := secrets.NewEncrypter(bytes.Repeat([]byte("k"), 32))
	if plain, err := enc.Open(updated.Credential); err != nil || plain != "original-password" {
		t.Fatalf("credential after update: %q %v", plain, err)
	}
}

func TestUpdateWordPressSiteReportsAMissingRow(t *testing.T) {
	store := open(t)
	rec := site("https://example.com", "admin")
	rec.ID = 77
	rec.Credential = testBox(t, "pw")
	if _, err := store.UpdateWordPressSite(context.Background(), rec); !errors.Is(err, channelstore.ErrSiteNotFound) {
		t.Fatalf("err = %v, want ErrSiteNotFound", err)
	}
}

func TestUpdateWordPressSiteRefusesAnotherSitesURL(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	first := site("https://one.example.com", "admin")
	first.Credential = testBox(t, "pw")
	second := site("https://two.example.com", "admin")
	second.Credential = testBox(t, "pw")
	a, err := store.CreateWordPressSite(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateWordPressSite(ctx, second)
	if err != nil {
		t.Fatal(err)
	}

	moved := b
	moved.URL = a.URL
	if _, err := store.UpdateWordPressSite(ctx, moved); !errors.Is(err, channelstore.ErrDuplicateSite) {
		t.Fatalf("err = %v, want ErrDuplicateSite", err)
	}
	// Nothing was written.
	got, _, err := store.GetWordPressSite(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://two.example.com" {
		t.Fatalf("url after a refused update = %q", got.URL)
	}
}

func TestDeleteWordPressSiteRemovesOnlyThatRow(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	keep := site("https://keep.example.com", "admin")
	keep.Credential = testBox(t, "pw")
	drop := site("https://drop.example.com", "admin")
	drop.Credential = testBox(t, "pw")
	kept, err := store.CreateWordPressSite(ctx, keep)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := store.CreateWordPressSite(ctx, drop)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteWordPressSite(ctx, gone.ID); err != nil {
		t.Fatalf("DeleteWordPressSite: %v", err)
	}
	if _, found, _ := store.GetWordPressSite(ctx, gone.ID); found {
		t.Fatal("the deleted site is still there")
	}
	if _, found, _ := store.GetWordPressSite(ctx, kept.ID); !found {
		t.Fatal("deleting one site removed another")
	}
	if err := store.DeleteWordPressSite(ctx, gone.ID); !errors.Is(err, channelstore.ErrSiteNotFound) {
		t.Fatalf("second delete err = %v, want ErrSiteNotFound", err)
	}
}

func TestDeletingAUserThatHeldASiteRightLeavesTheSite(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	rec := site("https://example.com", "admin")
	rec.Credential = testBox(t, "pw")
	created, err := store.CreateWordPressSite(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	user := createUser(t, store, "holder")
	if err := store.ReplacePermissions(ctx, user.ID, channelstore.PermissionSet{
		Wordsites: []channelstore.ResourcePermissions{{ResourceID: created.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}}},
	}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
	if err := store.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, found, _ := store.GetWordPressSite(ctx, created.ID); !found {
		t.Fatal("deleting an account removed the site it could see")
	}
}

func TestWordPressSitePermissionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	rec := site("https://example.com", "admin")
	rec.Credential = testBox(t, "pw")
	created, err := store.CreateWordPressSite(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	user := createUser(t, store, "editor")
	if err := store.ReplacePermissions(ctx, user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionWordPressView},
		Wordsites: []channelstore.ResourcePermissions{{ResourceID: created.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
		}}},
	}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
	store2, err := channelstore.Open(store.Path(), channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store2.Close() })
	auth, err := store2.AuthorizerFor(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Allowed(channelstore.KindWordPress, channelstore.ActionUpdate, created.ID) {
		t.Fatal("the site update right did not survive a reopen")
	}
	if !auth.CanList(channelstore.KindWordPress) {
		t.Fatal("the global wordpress view did not survive a reopen")
	}
}

func TestWordPressSitePermissionsRequireAnExistingSite(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	user := createUser(t, store, "editor")
	err := store.ReplacePermissions(ctx, user.ID, channelstore.PermissionSet{
		Wordsites: []channelstore.ResourcePermissions{{ResourceID: 999, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}}},
	})
	if !errors.Is(err, channelstore.ErrResourceNotFound) {
		t.Fatalf("err = %v, want ErrResourceNotFound", err)
	}
}

func TestDeletingASiteRemovesItsAssignmentsNotTheAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	rec := site("https://example.com", "admin")
	rec.Credential = testBox(t, "pw")
	created, err := store.CreateWordPressSite(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	user := createUser(t, store, "holder")
	if err := store.ReplacePermissions(ctx, user.ID, channelstore.PermissionSet{
		Wordsites: []channelstore.ResourcePermissions{{ResourceID: created.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWordPressSite(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.GetUser(ctx, user.ID); err != nil || !found {
		t.Fatalf("the account is gone: found=%v err=%v", found, err)
	}
	set, err := store.PermissionsOf(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Wordsites) != 0 {
		t.Fatalf("assignments remained: %+v", set.Wordsites)
	}
}

// openAt reopens an existing database with a fixed clock.
func openAt(t *testing.T, path string, now func() time.Time) *channelstore.Store {
	t.Helper()
	store, err := channelstore.Open(path, channelstore.Options{Now: now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// createUser makes an active account with no permissions.
func createUser(t *testing.T, store *channelstore.Store, name string) channelstore.User {
	t.Helper()
	u, err := store.CreateUser(context.Background(), name, name, "hash-"+name, true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

// timeAt is a fixed timestamp for stored records.
func timeAt(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
}

func TestSiteCategoriesAreSetReadAndCascaded(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	rec := site("https://example.com", "admin")
	rec.Credential = testBox(t, "pw")
	saved, err := store.CreateWordPressSite(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	news, err := store.CreateCategory(ctx, "News", "")
	if err != nil {
		t.Fatal(err)
	}
	tech, err := store.CreateCategory(ctx, "Tech", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.ReplaceSiteCategories(ctx, saved.ID, []int64{news.ID, tech.ID, news.ID}); err != nil {
		t.Fatalf("ReplaceSiteCategories: %v", err)
	}
	ids, err := store.SiteCategories(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != news.ID || ids[1] != tech.ID {
		t.Fatalf("categories = %v", ids)
	}
	bySite, err := store.CategoriesOfSites(ctx, []int64{saved.ID})
	if err != nil || len(bySite[saved.ID]) != 2 {
		t.Fatalf("CategoriesOfSites = %v %v", bySite, err)
	}

	// An empty set unfiles the site from every category.
	if err := store.ReplaceSiteCategories(ctx, saved.ID, nil); err != nil {
		t.Fatal(err)
	}
	if ids, _ := store.SiteCategories(ctx, saved.ID); len(ids) != 0 {
		t.Fatalf("categories after an empty set = %v", ids)
	}

	// Deleting a category drops the link; deleting the site drops its links.
	if err := store.ReplaceSiteCategories(ctx, saved.ID, []int64{news.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCategory(ctx, news.ID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := store.SiteCategories(ctx, saved.ID); len(ids) != 0 {
		t.Fatalf("after deleting the category: %v", ids)
	}
	if err := store.ReplaceSiteCategories(ctx, saved.ID, []int64{tech.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWordPressSite(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SiteCategories(ctx, saved.ID); !errors.Is(err, channelstore.ErrSiteNotFound) {
		t.Fatalf("the deleted site's categories: err = %v, want ErrSiteNotFound", err)
	}
}

func TestSiteCategoriesRefuseAnUnknownSiteOrCategory(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	rec := site("https://example.com", "admin")
	rec.Credential = testBox(t, "pw")
	saved, err := store.CreateWordPressSite(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSiteCategories(ctx, 999, nil); !errors.Is(err, channelstore.ErrSiteNotFound) {
		t.Fatalf("unknown site: %v", err)
	}
	if err := store.ReplaceSiteCategories(ctx, saved.ID, []int64{999}); !errors.Is(err, channelstore.ErrCategoryNotFound) {
		t.Fatalf("unknown category: %v", err)
	}
	if _, err := store.SiteCategories(ctx, 999); !errors.Is(err, channelstore.ErrSiteNotFound) {
		t.Fatalf("read of an unknown site: %v", err)
	}
}
