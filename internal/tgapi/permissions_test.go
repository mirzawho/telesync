package tgapi_test

import (
	"testing"

	"github.com/mirza/telesync/internal/tgapi"
)

// member is the baseline: a joined, unrestricted account in a supergroup.
func member() tgapi.ChannelAccess {
	return tgapi.ChannelAccess{Known: true, Member: true}
}

func TestNormalizePermissionsForAnOrdinarySupergroupSubscriber(t *testing.T) {
	p := tgapi.NormalizePermissions(member())
	if !p.Read {
		t.Error("a member can read")
	}
	if !p.New {
		t.Error("an unrestricted supergroup member can post")
	}
	if p.Edit || p.Delete {
		t.Errorf("a subscriber holds no edit/delete right: %+v", p)
	}
}

func TestNormalizePermissionsForAnOrdinaryBroadcastSubscriber(t *testing.T) {
	a := member()
	a.Broadcast = true
	p := tgapi.NormalizePermissions(a)
	if !p.Read {
		t.Error("a channel subscriber can read")
	}
	if p.New || p.Edit || p.Delete {
		t.Errorf("a channel subscriber cannot publish or moderate: %+v", p)
	}
}

func TestNormalizePermissionsForTheCreator(t *testing.T) {
	a := member()
	a.Broadcast = true
	a.Creator = true
	p := tgapi.NormalizePermissions(a)
	if !p.Read || !p.New || !p.Edit || !p.Delete {
		t.Fatalf("the creator owns every right: %+v", p)
	}
}

func TestNormalizePermissionsForAnAdminWithPostingRights(t *testing.T) {
	a := member()
	a.Broadcast = true
	a.Admin = true
	a.PostMessages = true
	a.EditMessages = true
	a.DeleteMessages = true
	p := tgapi.NormalizePermissions(a)
	if !p.New || !p.Edit || !p.Delete {
		t.Fatalf("an admin with all rights must report them: %+v", p)
	}
}

func TestNormalizePermissionsForAnAdminWithoutPostingRights(t *testing.T) {
	a := member()
	a.Broadcast = true
	a.Admin = true
	// An admin who may manage members but not post.
	p := tgapi.NormalizePermissions(a)
	if p.New {
		t.Error("an admin without post_messages must not report the new right")
	}
	if p.Edit || p.Delete {
		t.Error("an admin without edit/delete rights must not report them")
	}
	if !p.Read {
		t.Error("an admin can still read")
	}
}

func TestNormalizePermissionsForARestrictedOrBannedMember(t *testing.T) {
	banned := member()
	banned.SendDenied = true
	p := tgapi.NormalizePermissions(banned)
	if p.New {
		t.Error("a restricted member cannot post")
	}
	if p.Edit || p.Delete {
		t.Errorf("a restricted member holds no moderation rights: %+v", p)
	}

	readOnly := member()
	readOnly.ViewDenied = true
	if got := tgapi.NormalizePermissions(readOnly); got != (tgapi.Permissions{}) {
		t.Fatalf("an account forbidden to view has no permissions: %+v", got)
	}
}

func TestNormalizePermissionsForAnInaccessibleOrNonMemberChannel(t *testing.T) {
	notMember := tgapi.ChannelAccess{Known: true, Member: false, Broadcast: true}
	if got := tgapi.NormalizePermissions(notMember); got != (tgapi.Permissions{}) {
		t.Fatalf("a non-member cannot read through this client: %+v", got)
	}

	left := member()
	left.Left = true
	if got := tgapi.NormalizePermissions(left); got != (tgapi.Permissions{}) {
		t.Fatalf("a left channel is not readable: %+v", got)
	}
}

func TestNormalizePermissionsWithInsufficientInformation(t *testing.T) {
	unknown := tgapi.ChannelAccess{}
	if got := tgapi.NormalizePermissions(unknown); got != (tgapi.Permissions{}) {
		t.Fatalf("unknown standing must not be guessed: %+v", got)
	}
}

func TestNormalizePermissionsForADefaultRestrictedSupergroup(t *testing.T) {
	a := member()
	a.DefaultSendDenied = true
	p := tgapi.NormalizePermissions(a)
	if p.New {
		t.Error("a supergroup whose default rights forbid sending must not report the new right")
	}
	if !p.Read {
		t.Error("reading is unaffected by the default send restriction")
	}
}
