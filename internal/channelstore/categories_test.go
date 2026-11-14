package channelstore_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
)

func TestCreateTrimsTheNameAndRecordsTimestamps(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	store := open(t, func(o *channelstore.Options) { o.Now = func() time.Time { return clock } })

	cat, err := store.CreateCategory(ctx, "  Technology \n", "  Technology channels  ")
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if cat.ID <= 0 {
		t.Fatalf("id = %d, want a generated id", cat.ID)
	}
	if cat.Name != "Technology" {
		t.Fatalf("name = %q, want trimmed", cat.Name)
	}
	if cat.Description != "Technology channels" {
		t.Fatalf("description = %q, want trimmed", cat.Description)
	}
	if !cat.CreatedAt.Equal(clock) || !cat.UpdatedAt.Equal(clock) {
		t.Fatalf("timestamps = %v / %v, want %v", cat.CreatedAt, cat.UpdatedAt, clock)
	}
}

func TestCreateAcceptsAnEmptyDescription(t *testing.T) {
	store := open(t)
	cat, err := store.CreateCategory(context.Background(), "News", "")
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if cat.Description != "" {
		t.Fatalf("description = %q, want empty", cat.Description)
	}
}

func TestCreateRejectsBlankNames(t *testing.T) {
	store := open(t)
	for _, name := range []string{"", "   ", "\t\n"} {
		if _, err := store.CreateCategory(context.Background(), name, ""); err == nil {
			t.Fatalf("name %q must be rejected", name)
		}
	}
}

func TestCreateRejectsDuplicateNamesIgnoringCase(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	if _, err := store.CreateCategory(ctx, "Technology", ""); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateCategory(ctx, "  technology ", "")
	if !errors.Is(err, channelstore.ErrDuplicateName) {
		t.Fatalf("err = %v, want ErrDuplicateName", err)
	}
}

func TestListCategoriesPaginatesAndCounts(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	for i := 1; i <= 5; i++ {
		if _, err := store.CreateCategory(ctx, fmt.Sprintf("Cat %d", i), ""); err != nil {
			t.Fatal(err)
		}
	}

	first, total, err := store.ListCategories(ctx, channelstore.ListQuery{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(first) != 2 {
		t.Fatalf("len = %d, want 2", len(first))
	}
	// Newest first, id descending as the tiebreaker.
	if first[0].Name != "Cat 5" || first[1].Name != "Cat 4" {
		t.Fatalf("order = %q, %q; want Cat 5, Cat 4", first[0].Name, first[1].Name)
	}

	third, total, err := store.ListCategories(ctx, channelstore.ListQuery{Page: 3, PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(third) != 1 || third[0].Name != "Cat 1" {
		t.Fatalf("page 3 = %v (total %d), want [Cat 1] total 5", third, total)
	}

	beyond, total, err := store.ListCategories(ctx, channelstore.ListQuery{Page: 9, PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if beyond != nil && len(beyond) != 0 {
		t.Fatalf("page beyond the end = %v, want empty", beyond)
	}
	if total != 5 {
		t.Fatalf("total beyond the end = %d, want 5", total)
	}
}

func TestListCategoriesIsEmptyBeforeAnythingExists(t *testing.T) {
	store := open(t)
	cats, total, err := store.ListCategories(context.Background(), channelstore.ListQuery{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 0 || total != 0 {
		t.Fatalf("list = %v, total %d; want empty", cats, total)
	}
}

func TestListCategoriesFiltersNameAndDescription(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	mustCreate(t, store, "Technology", "gadgets")
	mustCreate(t, store, "News", "technology desk")
	mustCreate(t, store, "Music", "")

	cats, total, err := store.ListCategories(ctx, channelstore.ListQuery{Page: 1, PerPage: 20, Query: "TECH"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(cats) != 2 {
		t.Fatalf("total = %d, list = %v; want the two technology matches", total, cats)
	}

	// LIKE metacharacters are data, not wildcards.
	cats, total, err = store.ListCategories(ctx, channelstore.ListQuery{Page: 1, PerPage: 20, Query: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(cats) != 0 {
		t.Fatalf("a literal %% matched %v", cats)
	}
}

func TestGetCategoryMissesCleanly(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	created := mustCreate(t, store, "Technology", "about")

	got, ok, err := store.GetCategory(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetCategory = %v, %v", ok, err)
	}
	if got.Name != "Technology" || got.Description != "about" {
		t.Fatalf("got = %+v", got)
	}

	if _, ok, err := store.GetCategory(ctx, created.ID+1); err != nil || ok {
		t.Fatalf("missing category: ok=%v err=%v", ok, err)
	}
}

func TestUpdateCategoryChangesFieldsAndKeepsCreatedAt(t *testing.T) {
	ctx := context.Background()
	created := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	now := created
	store := open(t, func(o *channelstore.Options) { o.Now = func() time.Time { return now } })
	cat := mustCreate(t, store, "Technology", "about")

	now = updated
	saved, err := store.UpdateCategory(ctx, cat.ID, "Gadgets", "")
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if saved.Name != "Gadgets" || saved.Description != "" {
		t.Fatalf("saved = %+v", saved)
	}
	if !saved.CreatedAt.Equal(created) {
		t.Fatalf("created_at = %v, want preserved %v", saved.CreatedAt, created)
	}
	if !saved.UpdatedAt.Equal(updated) {
		t.Fatalf("updated_at = %v, want %v", saved.UpdatedAt, updated)
	}
}

func TestUpdateCategoryRejectsMissingAndDuplicate(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	mustCreate(t, store, "Technology", "")
	other := mustCreate(t, store, "News", "")

	if _, err := store.UpdateCategory(ctx, other.ID+100, "Anything", ""); !errors.Is(err, channelstore.ErrCategoryNotFound) {
		t.Fatalf("err = %v, want ErrCategoryNotFound", err)
	}
	if _, err := store.UpdateCategory(ctx, other.ID, "", ""); err == nil {
		t.Fatal("a blank name must be rejected")
	}
	if _, err := store.UpdateCategory(ctx, other.ID, "technology", ""); !errors.Is(err, channelstore.ErrDuplicateName) {
		t.Fatalf("err = %v, want ErrDuplicateName", err)
	}
	// Renaming a category to its own name is not a conflict.
	if _, err := store.UpdateCategory(ctx, other.ID, " News ", "updated"); err != nil {
		t.Fatalf("renaming to itself: %v", err)
	}
}

func TestDeleteCategoryRemovesOnlyTheCategoryAndItsAssignments(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	account := categoryTestAccount(t, store, "official", "acc_category_delete")
	channelRow := channel("official", 1234567890)
	channelRow.AccountID = account.ID
	managed, err := store.UpsertFor(ctx, account.ID, channelRow)
	if err != nil {
		t.Fatal(err)
	}
	cat := mustCreate(t, store, "Technology", "")
	other := mustCreate(t, store, "News", "")
	for _, id := range []int64{cat.ID, other.ID} {
		if _, err := store.AssignChannel(ctx, account.ID, strconv.FormatInt(managed.ChannelID, 10), id); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.DeleteCategory(ctx, cat.ID); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}

	if _, ok, err := store.GetCategory(ctx, cat.ID); err != nil || ok {
		t.Fatalf("category still readable: ok=%v err=%v", ok, err)
	}
	raw := strconv.FormatInt(managed.ChannelID, 10)
	counts, err := store.AssignmentCounts(ctx, account.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	if counts[cat.ID] != 0 {
		t.Fatalf("assignment to the deleted category survived: %v", counts)
	}
	if counts[other.ID] != 1 {
		t.Fatalf("assignment to the other category was lost: %v", counts)
	}

	// The channel's own record and Telegram identifiers are untouched.
	ch, ok, err := store.Get(ctx, "official", managed.ChannelID)
	if err != nil || !ok {
		t.Fatalf("managed channel disappeared: ok=%v err=%v", ok, err)
	}
	if ch.ChannelID != managed.ChannelID || ch.Title != managed.Title ||
		ch.MembershipStatus != managed.MembershipStatus {
		t.Fatalf("managed channel changed: %+v", ch)
	}
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version < 2 {
		t.Fatalf("schema version = %d, want at least 2", version)
	}
}

func TestDeleteCategoryMissesCleanly(t *testing.T) {
	store := open(t)
	if err := store.DeleteCategory(context.Background(), 42); !errors.Is(err, channelstore.ErrCategoryNotFound) {
		t.Fatalf("err = %v, want ErrCategoryNotFound", err)
	}
}

func TestAssignChannelRepresentsEachChannelOnce(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	cat := mustCreate(t, store, "Technology", "")
	owner := mustUser(t, store, "category-owner", true)
	firstAccount := mustAccount(t, store, newAccount(owner.ID, "acc_category_a", "official", 801))
	secondAccount := mustAccount(t, store, newAccount(owner.ID, "acc_category_b", "official", 802))

	first, err := store.AssignChannel(ctx, firstAccount.ID, "100", cat.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AssignChannel(ctx, firstAccount.ID, "100", cat.ID)
	if err != nil {
		t.Fatalf("re-assigning must be a no-op, got %v", err)
	}
	if !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("created_at changed on re-assign: %v -> %v", first.CreatedAt, second.CreatedAt)
	}

	// The same remote channel under another account is a separate assignment.
	if _, err := store.AssignChannel(ctx, secondAccount.ID, "100", cat.ID); err != nil {
		t.Fatal(err)
	}
	firstCounts, err := store.AssignmentCounts(ctx, firstAccount.ID, "100")
	if err != nil || firstCounts[cat.ID] != 1 {
		t.Fatalf("first account assignments = %v, err=%v", firstCounts, err)
	}
	secondCounts, err := store.AssignmentCounts(ctx, secondAccount.ID, "100")
	if err != nil || secondCounts[cat.ID] != 1 {
		t.Fatalf("second account assignments = %v, err=%v", secondCounts, err)
	}
	if _, err := store.AssignChannel(ctx, firstAccount.ID, "100", cat.ID+1); !errors.Is(err, channelstore.ErrCategoryNotFound) {
		t.Fatalf("err = %v, want ErrCategoryNotFound", err)
	}
	if _, err := store.AssignChannel(ctx, "", "100", cat.ID); err == nil {
		t.Fatal("an empty account id must be rejected")
	}
}

func TestCategoryChannelsForOwnerKeepsDuplicateRegistrationsIsolated(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	category := mustCreate(t, store, "Shared label", "")
	ownerA := mustUser(t, store, "category-owner-a", true)
	ownerB := mustUser(t, store, "category-owner-b", true)
	accountA := mustAccount(t, store, newAccount(ownerA.ID, "acc_cat_owner_a", "official", 901))
	accountBInput := newAccount(ownerB.ID, "acc_cat_owner_b", "official", 902)
	accountBInput.Status = channelstore.AccountStatusDisconnected
	accountB := mustAccount(t, store, accountBInput)
	for _, account := range []channelstore.Account{accountA, accountB} {
		managed, err := store.UpsertFor(ctx, account.ID, channelstore.Channel{
			ClientKey: account.ServerKey, AccountID: account.ID, ChannelID: 77,
			PeerType: channelstore.PeerChannel, Title: account.ID,
			MembershipStatus: channelstore.StatusMember,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.ReplaceCategories(ctx, account.ID, managed.ChannelID, []int64{category.ID}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.CategoryChannelsForOwner(ctx, ownerA.ID, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AccountID != accountA.ID {
		t.Fatalf("owner A category channels = %+v, want only account %q", got, accountA.ID)
	}
	got, err = store.CategoryChannelsForOwner(ctx, ownerB.ID, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AccountID != accountB.ID {
		t.Fatalf("disconnected active owner B category channels = %+v, want account %q", got, accountB.ID)
	}
}

func TestCategoriesSurviveAReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "categories.db")
	first, err := channelstore.Open(path, channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cat := mustCreate(t, first, "Technology", "about")
	account := categoryTestAccount(t, first, "official", "acc_category_reopen")
	if _, err := first.AssignChannel(ctx, account.ID, "555", cat.ID); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := channelstore.Open(path, channelstore.Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	got, ok, err := second.GetCategory(ctx, cat.ID)
	if err != nil || !ok || got.Name != "Technology" {
		t.Fatalf("after reopen: %+v ok=%v err=%v", got, ok, err)
	}
	counts, err := second.AssignmentCounts(ctx, account.ID, "555")
	if err != nil || counts[cat.ID] != 1 {
		t.Fatalf("assignments after reopen: %v err=%v", counts, err)
	}
}

func categoryTestAccount(t *testing.T, store *channelstore.Store, serverKey, id string) channelstore.Account {
	t.Helper()
	user := mustUser(t, store, id+"_owner", true)
	return mustAccount(t, store, newAccount(user.ID, id, serverKey, int64(len(id)+900)))
}

func mustCreate(t *testing.T, store *channelstore.Store, name, description string) channelstore.Category {
	t.Helper()
	cat, err := store.CreateCategory(context.Background(), name, description)
	if err != nil {
		t.Fatalf("CreateCategory(%q): %v", name, err)
	}
	return cat
}
