package gateway_test

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

// TestAPickedScopeNarrowsWhatMayBeSeenUnderAGlobalPermission pins the shape the
// administrators dialog saves for a resource scope: the global viewing
// permission of the kind, an "allow" on each picked row, and an explicit deny
// on each row the picker left out.
//
// A scope is only a scope if that stored deny outranks the global grant —
// without it an unpicked row falls back to "channels.view" and a limited
// selection quietly becomes access to every channel. This is the rule the
// dialog's report tab claims, so it is asserted against the real API.
func TestAPickedScopeNarrowsWhatMayBeSeenUnderAGlobalPermission(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	a := env.channelRow(t, "official", 1001)
	b := env.channelRow(t, "official", 1002)
	who := env.account(t, "test")
	admin := env.account(t, "admin",
		channelstore.PermissionUsersView, channelstore.PermissionUsersManage)

	body := fmt.Sprintf(`{"global_permissions":["channels.view"],`+
		`"channel_permissions":[{"channel_id":%d,"permissions":{"view":"allow"}},`+
		`{"channel_id":%d,"permissions":{"view":"deny","update":"deny","delete":"deny"}}],`+
		`"category_permissions":[]}`, a.ID, b.ID)
	saved := env.send(t, admin.token, http.MethodPut,
		"/api/v1/local/users/"+strconv.FormatInt(who.user.ID, 10)+"/permissions", body)
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("save = %d: %s", saved.StatusCode, bodyOf(saved))
	}

	ids, total := channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath, "")), "channels")
	wantIDs(t, "list", ids, a.ID)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if resp := env.send(t, who.token, http.MethodGet,
		localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("the picked channel = %d, want 200", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodGet,
		localChannelsPath+"/"+strconv.FormatInt(b.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the unpicked channel = %d, want 403", resp.StatusCode)
	}
}
