package channelstore_test

import (
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

// Publishing is a business operation of its own. Granting it must not fall out
// of an unrelated right: "channels.create" lets you add a channel to the
// inventory, not send one a message.

func TestContentPublishIsItsOwnGlobalPermission(t *testing.T) {
	perms := channelstore.GlobalPermissions()
	found := false
	for _, p := range perms {
		if p == channelstore.PermissionContentPublish {
			found = true
		}
	}
	if !found {
		t.Fatalf("GlobalPermissions() = %v, want it to include %q", perms, channelstore.PermissionContentPublish)
	}
	if string(channelstore.PermissionContentPublish) != "content.publish" {
		t.Fatalf("PermissionContentPublish = %q", channelstore.PermissionContentPublish)
	}
	if !channelstore.PermissionContentPublish.Valid() {
		t.Fatal("content.publish must be a valid global permission")
	}
	if channelstore.GlobalPermission("publishing.create").Valid() {
		t.Fatal("an unknown permission must not validate")
	}
}

func TestPublishingIsNotGrantedByAnyOtherPermission(t *testing.T) {
	// The authorizer answers Global(...) per name; a user holding every other
	// global right still has no publishing one.
	a := channelstore.NewAuthorizer([]channelstore.GlobalPermission{
		channelstore.PermissionUsersView,
		channelstore.PermissionUsersManage,
		channelstore.PermissionChannelsView,
		channelstore.PermissionChannelsCreate,
		channelstore.PermissionCategoriesView,
		channelstore.PermissionCategoriesCreate,
		channelstore.PermissionWordPressView,
		channelstore.PermissionWordPressManage,
		channelstore.PermissionServersView,
	}, nil)
	if a.Global(channelstore.PermissionContentPublish) {
		t.Fatal("an account without content.publish must not be allowed to publish")
	}
	b := channelstore.NewAuthorizer([]channelstore.GlobalPermission{
		channelstore.PermissionContentPublish,
	}, nil)
	if !b.Global(channelstore.PermissionContentPublish) {
		t.Fatal("content.publish must gate publishing on its own")
	}
}
