package channelstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

func allow(action channelstore.Action) channelstore.ResourcePermission {
	return channelstore.ResourcePermission{Action: action, Effect: channelstore.EffectAllow}
}

func deny(action channelstore.Action) channelstore.ResourcePermission {
	return channelstore.ResourcePermission{Action: action, Effect: channelstore.EffectDeny}
}

func TestReplacePermissionsStoresExactlyWhatWasAsked(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "example", true)
	one, _ := store.Upsert(ctx, channel("official", 1001))
	two, _ := store.Upsert(ctx, channel("official", 1002))
	cat, _ := store.CreateCategory(ctx, "News", "")

	err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionChannelsCreate},
		Channels: []channelstore.ResourcePermissions{
			{ResourceID: one.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView), allow(channelstore.ActionUpdate), deny(channelstore.ActionDelete)}},
			{ResourceID: two.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}},
		},
		Categories: []channelstore.ResourcePermissions{
			{ResourceID: cat.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView), deny(channelstore.ActionUpdate)}},
		},
	})
	if err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}

	set, err := store.PermissionsOf(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Global) != 1 || set.Global[0] != channelstore.PermissionChannelsCreate {
		t.Fatalf("global = %v", set.Global)
	}
	if len(set.Channels) != 2 || set.Channels[0].ResourceID != one.ID || len(set.Channels[0].Permissions) != 3 {
		t.Fatalf("channels = %+v", set.Channels)
	}
	if len(set.Categories) != 1 || len(set.Categories[0].Permissions) != 2 {
		t.Fatalf("categories = %+v", set.Categories)
	}
}

func TestReplacePermissionsRemovesWhatTheNewSetOmits(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "example", true)
	ch, _ := store.Upsert(ctx, channel("official", 1001))
	seed := channelstore.PermissionSet{
		Global:   []channelstore.GlobalPermission{channelstore.PermissionUsersManage},
		Channels: []channelstore.ResourcePermissions{{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}}},
	}
	if err := store.ReplacePermissions(ctx, u.ID, seed); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{}); err != nil {
		t.Fatal(err)
	}
	set, err := store.PermissionsOf(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Global)+len(set.Channels)+len(set.Categories) != 0 {
		t.Fatalf("stale assignments remain: %+v", set)
	}
}

func TestReplacePermissionsRejectsAnUnknownChannelWithoutWritingAnything(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "example", true)
	grant(t, store, u.ID, channelstore.PermissionUsersView)

	err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Channels: []channelstore.ResourcePermissions{
			{ResourceID: 4242, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}},
		},
	})
	if !errors.Is(err, channelstore.ErrResourceNotFound) {
		t.Fatalf("err = %v, want ErrResourceNotFound", err)
	}
	set, _ := store.PermissionsOf(ctx, u.ID)
	if len(set.Global) != 1 {
		t.Fatalf("the failed replacement changed the stored set: %+v", set)
	}
}

func TestAChannelIDIsTheLocalIDNotTheTelegramID(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	const telegramID = 1234567890
	ch, err := store.Upsert(ctx, channel("official", telegramID))
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID == telegramID {
		t.Skip("the local id happens to equal the Telegram id; nothing to prove")
	}
	u := mustUser(t, store, "example", true)

	// The Telegram id is not a local id, so an assignment naming it names no
	// channel.
	err = store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Channels: []channelstore.ResourcePermissions{{ResourceID: telegramID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}}},
	})
	if !errors.Is(err, channelstore.ErrResourceNotFound) {
		t.Fatalf("assignment by Telegram id = %v, want ErrResourceNotFound", err)
	}
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Channels: []channelstore.ResourcePermissions{{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}}},
	}); err != nil {
		t.Fatalf("assignment by local id: %v", err)
	}
}

func TestReplacePermissionsRejectsInvalidActionsEffectsAndDuplicates(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "example", true)
	ch, _ := store.Upsert(ctx, channel("official", 1001))
	cat, _ := store.CreateCategory(ctx, "News", "")

	tests := map[string]channelstore.PermissionSet{
		"unknown global":     {Global: []channelstore.GlobalPermission{"channels.explode"}},
		"duplicate global":   {Global: []channelstore.GlobalPermission{channelstore.PermissionUsersView, channelstore.PermissionUsersView}},
		"unknown action":     {Channels: []channelstore.ResourcePermissions{{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{{Action: "create", Effect: "allow"}}}}},
		"unknown effect":     {Channels: []channelstore.ResourcePermissions{{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: "maybe"}}}}},
		"duplicate action":   {Channels: []channelstore.ResourcePermissions{{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView), deny(channelstore.ActionView)}}}},
		"duplicate resource": {Categories: []channelstore.ResourcePermissions{{ResourceID: cat.ID}, {ResourceID: cat.ID}}},
		"zero channel":       {Channels: []channelstore.ResourcePermissions{{ResourceID: 0}}},
		"unknown category":   {Categories: []channelstore.ResourcePermissions{{ResourceID: 777, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}}}},
	}
	for name, set := range tests {
		t.Run(name, func(t *testing.T) {
			if err := store.ReplacePermissions(ctx, u.ID, set); !errors.Is(err, channelstore.ErrInvalidUser) && !errors.Is(err, channelstore.ErrResourceNotFound) {
				t.Fatalf("err = %v, want a validation error", err)
			}
		})
	}
}

func TestReplacePermissionsMissesAnUnknownUser(t *testing.T) {
	err := open(t).ReplacePermissions(context.Background(), 42, channelstore.PermissionSet{})
	if !errors.Is(err, channelstore.ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestDeletingAChannelRemovesItsAssignments(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "example", true)
	ch, _ := store.Upsert(ctx, channel("official", 1001))
	other, _ := store.Upsert(ctx, channel("official", 1002))
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Channels: []channelstore.ResourcePermissions{
			{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}},
			{ResourceID: other.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteChannelRow(ctx, ch.ID); err != nil {
		t.Fatalf("DeleteChannelRow: %v", err)
	}
	set, err := store.PermissionsOf(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Channels) != 1 || set.Channels[0].ResourceID != other.ID {
		t.Fatalf("channels = %+v, want only the surviving one", set.Channels)
	}
}

func TestDeletingACategoryRemovesItsAssignmentsAndKeepsChannels(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "example", true)
	ch, _ := store.Upsert(ctx, channel("official", 1001))
	cat, _ := store.CreateCategory(ctx, "News", "")
	if _, err := store.AssignChannel(ctx, "official", "1001", cat.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Categories: []channelstore.ResourcePermissions{{ResourceID: cat.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCategory(ctx, cat.ID); err != nil {
		t.Fatal(err)
	}
	set, err := store.PermissionsOf(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Categories) != 0 {
		t.Fatalf("category assignments survived: %+v", set.Categories)
	}
	if _, ok, _ := store.GetByID(ctx, ch.ID); !ok {
		t.Fatal("deleting the category deleted its channel")
	}
}

func TestAuthorizerFollowsTheDocumentedRules(t *testing.T) {
	newA := func(globals []channelstore.GlobalPermission, grants ...channelstore.Grant) *channelstore.Authorizer {
		return channelstore.NewAuthorizer(globals, grants)
	}
	ch := func(id int64, action channelstore.Action, effect channelstore.Effect) channelstore.Grant {
		return channelstore.Grant{Kind: channelstore.KindChannel, ResourceID: id, Action: action, Effect: effect}
	}

	tests := []struct {
		name     string
		auth     *channelstore.Authorizer
		kind     channelstore.ResourceKind
		action   channelstore.Action
		resource int64
		want     bool
	}{
		{"global view grants an unassigned channel", newA([]channelstore.GlobalPermission{channelstore.PermissionChannelsView}), channelstore.KindChannel, channelstore.ActionView, 5, true},
		{"global view does not grant update", newA([]channelstore.GlobalPermission{channelstore.PermissionChannelsView}), channelstore.KindChannel, channelstore.ActionUpdate, 5, false},
		{"a resource allow grants without a global", newA(nil, ch(5, channelstore.ActionView, channelstore.EffectAllow), ch(5, channelstore.ActionUpdate, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionUpdate, 5, true},
		{"update does not stand in for a missing view", newA(nil, ch(5, channelstore.ActionUpdate, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionUpdate, 5, false},
		{"a view deny takes the update away", newA(nil, ch(5, channelstore.ActionView, channelstore.EffectDeny), ch(5, channelstore.ActionUpdate, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionUpdate, 5, false},
		{"a resource allow on one channel does not reach another", newA(nil, ch(5, channelstore.ActionView, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionView, 6, false},
		{"an explicit deny overrides a global view and its own update grant", newA([]channelstore.GlobalPermission{channelstore.PermissionChannelsView}, ch(5, channelstore.ActionView, channelstore.EffectDeny)), channelstore.KindChannel, channelstore.ActionView, 5, false},
		{"nothing at all is a deny", newA(nil), channelstore.KindChannel, channelstore.ActionView, 5, false},
		{"create is the global permission", newA([]channelstore.GlobalPermission{channelstore.PermissionChannelsCreate}), channelstore.KindChannel, channelstore.ActionCreate, 0, true},
		{"create is never resource-specific", newA(nil, ch(5, channelstore.ActionCreate, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionCreate, 5, false},
		{"update is never implied by category view", newA([]channelstore.GlobalPermission{channelstore.PermissionCategoriesView}), channelstore.KindCategory, channelstore.ActionUpdate, 3, false},
		{"a nil authorizer denies", nil, channelstore.KindChannel, channelstore.ActionView, 5, false},
		{"server view is its own global permission", newA([]channelstore.GlobalPermission{channelstore.PermissionServersView}), channelstore.KindServer, channelstore.ActionView, 0, true},
		{"channel view does not show servers", newA([]channelstore.GlobalPermission{channelstore.PermissionChannelsView}), channelstore.KindServer, channelstore.ActionView, 0, false},
		{"server view does not reach channels", newA([]channelstore.GlobalPermission{channelstore.PermissionServersView}), channelstore.KindChannel, channelstore.ActionView, 5, false},
		{"no assignment can deny a server", newA([]channelstore.GlobalPermission{channelstore.PermissionServersView}, channelstore.Grant{Kind: channelstore.KindServer, ResourceID: 1, Action: channelstore.ActionView, Effect: channelstore.EffectDeny}), channelstore.KindServer, channelstore.ActionView, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.auth.Allowed(tt.kind, tt.action, tt.resource); got != tt.want {
				t.Fatalf("Allowed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManageEditsAndDeletesWithinTheSelection(t *testing.T) {
	newA := func(globals []channelstore.GlobalPermission, grants ...channelstore.Grant) *channelstore.Authorizer {
		return channelstore.NewAuthorizer(globals, grants)
	}
	grant := func(kind channelstore.ResourceKind, id int64, action channelstore.Action, effect channelstore.Effect) channelstore.Grant {
		return channelstore.Grant{Kind: kind, ResourceID: id, Action: action, Effect: effect}
	}
	ch := func(id int64, action channelstore.Action, effect channelstore.Effect) channelstore.Grant {
		return grant(channelstore.KindChannel, id, action, effect)
	}
	manage := []channelstore.GlobalPermission{
		channelstore.PermissionChannelsCreate, channelstore.PermissionChannelsView,
	}

	tests := []struct {
		name     string
		auth     *channelstore.Authorizer
		kind     channelstore.ResourceKind
		action   channelstore.Action
		resource int64
		want     bool
	}{
		{"manage deletes an unassigned channel", newA(manage), channelstore.KindChannel, channelstore.ActionDelete, 5, true},
		{"manage updates an unassigned channel", newA(manage), channelstore.KindChannel, channelstore.ActionUpdate, 5, true},
		{"manage without the view grants no write", newA([]channelstore.GlobalPermission{channelstore.PermissionChannelsCreate}), channelstore.KindChannel, channelstore.ActionDelete, 5, false},
		{"the selection limits manage to the picked channel", newA(manage, ch(5, channelstore.ActionView, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionDelete, 5, true},
		{"the selection keeps manage off an unpicked channel", newA(manage, ch(5, channelstore.ActionView, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionDelete, 6, false},
		{"the selection limits update too", newA(manage, ch(5, channelstore.ActionView, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionUpdate, 6, false},
		{"a deny-only set is not a selection", newA(manage, ch(9, channelstore.ActionView, channelstore.EffectDeny)), channelstore.KindChannel, channelstore.ActionDelete, 5, true},
		{"one write deny is an exclusion, not a selection", newA(manage, ch(9, channelstore.ActionDelete, channelstore.EffectDeny)), channelstore.KindChannel, channelstore.ActionDelete, 5, true},
		{"a deny of both writes marks a selection", newA(manage, ch(9, channelstore.ActionDelete, channelstore.EffectDeny), ch(9, channelstore.ActionUpdate, channelstore.EffectDeny)), channelstore.KindChannel, channelstore.ActionDelete, 5, false},
		{"a view deny takes the write away", newA(manage, ch(5, channelstore.ActionView, channelstore.EffectDeny)), channelstore.KindChannel, channelstore.ActionDelete, 5, false},
		{"an explicit deny of the action beats manage", newA(manage, ch(5, channelstore.ActionDelete, channelstore.EffectDeny)), channelstore.KindChannel, channelstore.ActionDelete, 5, false},
		{"an explicit deny of update does not take delete", newA(manage, ch(5, channelstore.ActionUpdate, channelstore.EffectDeny)), channelstore.KindChannel, channelstore.ActionDelete, 5, true},
		{"an explicit allow still grants without manage", newA([]channelstore.GlobalPermission{channelstore.PermissionChannelsView},
			ch(5, channelstore.ActionView, channelstore.EffectAllow), ch(5, channelstore.ActionDelete, channelstore.EffectAllow)), channelstore.KindChannel, channelstore.ActionDelete, 5, true},
		{"channel manage does not reach categories", newA(manage), channelstore.KindCategory, channelstore.ActionDelete, 5, false},
		{"category manage works the same way", newA([]channelstore.GlobalPermission{channelstore.PermissionCategoriesCreate, channelstore.PermissionCategoriesView}), channelstore.KindCategory, channelstore.ActionDelete, 3, true},
		{"category manage is scoped by its own selection", newA([]channelstore.GlobalPermission{channelstore.PermissionCategoriesCreate, channelstore.PermissionCategoriesView},
			grant(channelstore.KindCategory, 3, channelstore.ActionView, channelstore.EffectAllow)), channelstore.KindCategory, channelstore.ActionDelete, 4, false},
		{"site manage works the same way", newA([]channelstore.GlobalPermission{channelstore.PermissionWordPressManage, channelstore.PermissionWordPressView}), channelstore.KindWordPress, channelstore.ActionDelete, 2, true},
		{"site manage is scoped by its own selection", newA([]channelstore.GlobalPermission{channelstore.PermissionWordPressManage, channelstore.PermissionWordPressView},
			grant(channelstore.KindWordPress, 1, channelstore.ActionView, channelstore.EffectAllow)), channelstore.KindWordPress, channelstore.ActionDelete, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.auth.Allowed(tt.kind, tt.action, tt.resource); got != tt.want {
				t.Fatalf("Allowed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthorizerForReadsTheDatabase(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	u := mustUser(t, store, "example", true)
	ch, _ := store.Upsert(ctx, channel("official", 1001))
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Global:   []channelstore.GlobalPermission{channelstore.PermissionChannelsView},
		Channels: []channelstore.ResourcePermissions{{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView), allow(channelstore.ActionUpdate)}}},
	}); err != nil {
		t.Fatal(err)
	}
	auth, err := store.AuthorizerFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Allowed(channelstore.KindChannel, channelstore.ActionView, ch.ID) {
		t.Fatal("the explicit allow should grant the view")
	}
	if !auth.Allowed(channelstore.KindChannel, channelstore.ActionUpdate, ch.ID) {
		t.Fatal("the explicit allow should grant update")
	}
	if !auth.Allowed(channelstore.KindChannel, channelstore.ActionView, ch.ID+1) {
		t.Fatal("the global view should cover an unassigned channel")
	}
	if !auth.CanList(channelstore.KindChannel) || auth.CanList(channelstore.KindCategory) {
		t.Fatal("CanList should follow the global view exactly")
	}

	// A view deny is the caller's instruction for that record: it takes away
	// the global view and every write the caller's other grants would allow.
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Global:   []channelstore.GlobalPermission{channelstore.PermissionChannelsView},
		Channels: []channelstore.ResourcePermissions{{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{deny(channelstore.ActionView), allow(channelstore.ActionUpdate), allow(channelstore.ActionDelete)}}},
	}); err != nil {
		t.Fatal(err)
	}
	auth, err = store.AuthorizerFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []channelstore.Action{channelstore.ActionView, channelstore.ActionUpdate, channelstore.ActionDelete} {
		if auth.Allowed(channelstore.KindChannel, action, ch.ID) {
			t.Fatalf("%s under a view deny = allowed", action)
		}
	}
}
