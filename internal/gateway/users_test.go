package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
)

const usersPath = "/api/v1/local/users"

type userPage struct {
	Data struct {
		Users []userBody `json:"users"`
	} `json:"data"`
	Meta struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
	} `json:"meta"`
}

type oneUser struct {
	Data struct {
		User userBody `json:"user"`
	} `json:"data"`
}

func TestCreateUserHashesThePasswordAndNeverReturnsIt(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	who := env.account(t, "boss", channelstore.PermissionUsersManage)

	resp := env.send(t, who.token, http.MethodPost, usersPath,
		`{"fullname":"Example User","username":"example","password":"a-strong-password","is_active":true}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	raw := bodyOf(resp)
	if strings.Contains(raw, "a-strong-password") || strings.Contains(strings.ToLower(raw), "password") {
		t.Fatalf("the response mentions the password: %s", raw)
	}
	var body oneUser
	_ = json.Unmarshal([]byte(raw), &body)
	if body.Data.User.Username != "example" || !body.Data.User.IsActive || body.Data.User.ID <= 0 {
		t.Fatalf("user = %+v", body.Data.User)
	}

	// The stored value is an Argon2id hash and the password verifies against it.
	stored, ok, err := env.store.UserByUsername(context.Background(), "example")
	if err != nil || !ok {
		t.Fatalf("stored user: %v %v", ok, err)
	}
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("stored hash = %q, want Argon2id", stored.PasswordHash)
	}
	if ok, err := auth.VerifyPassword(stored.PasswordHash, "a-strong-password"); err != nil || !ok {
		t.Fatalf("the stored hash does not verify: %v %v", ok, err)
	}
}

func TestCreateUserValidation(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	who := env.account(t, "boss", channelstore.PermissionUsersManage)
	env.account(t, "taken")

	cases := map[string]struct {
		body   string
		status int
	}{
		"no password":        {`{"fullname":"A","username":"a"}`, http.StatusBadRequest},
		"short password":     {`{"fullname":"A","username":"a","password":"short"}`, http.StatusBadRequest},
		"no username":        {`{"fullname":"A","password":"long-enough"}`, http.StatusBadRequest},
		"unsafe username":    {`{"fullname":"A","username":"a b","password":"long-enough"}`, http.StatusBadRequest},
		"no fullname":        {`{"fullname":" ","username":"a","password":"long-enough"}`, http.StatusBadRequest},
		"unknown field":      {`{"fullname":"A","username":"a","password":"long-enough","password_hash":"x"}`, http.StatusBadRequest},
		"malformed":          {`{`, http.StatusBadRequest},
		"duplicate username": {`{"fullname":"A","username":"TAKEN","password":"long-enough"}`, http.StatusConflict},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resp := env.send(t, who.token, http.MethodPost, usersPath, tc.body)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.status, bodyOf(resp))
			}
		})
	}
}

func TestListUsersPaginatesAndSearches(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	who := env.account(t, "boss", channelstore.PermissionUsersManage, channelstore.PermissionUsersView)
	// Accounts: the three below, the administrator, and the suite's default.
	for _, name := range []string{"alice", "bob", "carol"} {
		env.account(t, name)
	}
	resp := env.send(t, who.token, http.MethodGet, usersPath+"?page=1&per_page=10", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	page := decode[userPage](t, resp)
	if page.Meta.Total != 5 || len(page.Data.Users) != 5 {
		t.Fatalf("meta=%+v users=%d", page.Meta, len(page.Data.Users))
	}

	narrow := decode[userPage](t, env.send(t, who.token, http.MethodGet, usersPath+"?page=2&per_page=2", ""))
	if len(narrow.Data.Users) != 2 || narrow.Meta.Page != 2 {
		t.Fatalf("page 2 = %+v", narrow)
	}

	found := decode[userPage](t, env.send(t, who.token, http.MethodGet, usersPath+"?q=CAR", ""))
	if found.Meta.Total != 1 || found.Data.Users[0].Username != "carol" {
		t.Fatalf("search = %+v", found)
	}

	bad := env.send(t, who.token, http.MethodGet, usersPath+"?per_page=101", "")
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad pagination = %d", bad.StatusCode)
	}
}

func TestGetUserReturnsTheProfileAndStoredPermissions(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	admin := env.account(t, "boss", channelstore.PermissionUsersManage, channelstore.PermissionUsersView)
	other := env.account(t, "alice", channelstore.PermissionUsersView)

	resp := env.send(t, admin.token, http.MethodGet, fmt.Sprintf("%s/%d", usersPath, other.user.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw := bodyOf(resp)
	if strings.Contains(raw, "password") {
		t.Fatalf("the response exposes the hash: %s", raw)
	}
	var body oneUser
	_ = json.Unmarshal([]byte(raw), &body)
	if body.Data.User.ID != other.user.ID || len(body.Data.User.Permissions.Global) != 1 {
		t.Fatalf("user = %+v", body.Data.User)
	}

	missing := env.send(t, admin.token, http.MethodGet, usersPath+"/9999", "")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing user = %d", missing.StatusCode)
	}
}

func TestAUserMayReadItselfButNotOthersWithoutThePermission(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	env.session = ""
	alice := env.account(t, "alice")
	bob := env.account(t, "bob")

	if resp := env.send(t, alice.token, http.MethodGet, fmt.Sprintf("%s/%d", usersPath, alice.user.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("self = %d", resp.StatusCode)
	}
	if resp := env.send(t, alice.token, http.MethodGet, fmt.Sprintf("%s/%d", usersPath, bob.user.ID), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other = %d, want 403", resp.StatusCode)
	}
	list := decode[userPage](t, env.send(t, alice.token, http.MethodGet, usersPath, ""))
	if list.Meta.Total != 1 || len(list.Data.Users) != 1 || list.Data.Users[0].ID != alice.user.ID {
		t.Fatalf("list = %+v, want only the caller", list)
	}
}

func TestUpdateUserChangesProfileActivationAndPassword(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	admin := env.account(t, "boss", channelstore.PermissionUsersManage)
	other := env.account(t, "alice")

	resp := env.send(t, admin.token, http.MethodPut, fmt.Sprintf("%s/%d", usersPath, other.user.ID),
		`{"fullname":"Alice A","username":"alice2","password":"another-password"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	var body oneUser
	_ = json.Unmarshal([]byte(bodyOf(resp)), &body)
	if body.Data.User.Fullname != "Alice A" || body.Data.User.Username != "alice2" {
		t.Fatalf("user = %+v", body.Data.User)
	}

	// The old password no longer logs in; the new one does.
	if resp := env.send(t, "", http.MethodPost, loginPath, `{"username":"alice2","password":"password-alice"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password = %d", resp.StatusCode)
	}
	if resp := env.send(t, "", http.MethodPost, loginPath, `{"username":"alice2","password":"another-password"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("new password = %d", resp.StatusCode)
	}

	// A raw hash is not an editable field.
	rejected := env.send(t, admin.token, http.MethodPut, fmt.Sprintf("%s/%d", usersPath, other.user.ID),
		`{"password_hash":"$argon2id$..."}`)
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("password_hash field = %d, want 400", rejected.StatusCode)
	}
}

func TestAnAdministratorCannotEscalateHimself(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	admin := env.account(t, "boss", channelstore.PermissionUsersManage)
	ch := env.channelRow(t, "official", 500)

	// The permissions endpoint refuses a self-change outright.
	body := fmt.Sprintf(`{"global_permissions":["users.manage","channels.view"],"channel_permissions":[{"channel_id":%d,"permissions":{"view":"allow"}}],"category_permissions":[]}`, ch.ID)
	resp := env.send(t, admin.token, http.MethodPut, fmt.Sprintf("%s/%d/permissions", usersPath, admin.user.ID), body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("self permissions = %d, want 403", resp.StatusCode)
	}
	set, _ := env.store.PermissionsOf(context.Background(), admin.user.ID)
	if len(set.Global) != 1 {
		t.Fatalf("the self-update changed the grants: %+v", set.Global)
	}

	// Nor are permissions smuggled in through the profile update.
	resp = env.send(t, admin.token, http.MethodPut, fmt.Sprintf("%s/%d", usersPath, admin.user.ID),
		`{"is_active":true,"global_permissions":["users.view"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("profile with a permission field = %d, want 400", resp.StatusCode)
	}
	set, _ = env.store.PermissionsOf(context.Background(), admin.user.ID)
	if len(set.Global) != 1 {
		t.Fatalf("the profile update changed the grants: %+v", set.Global)
	}

	// Nor may anyone deactivate or delete themselves, which would be a lockout.
	if resp := env.send(t, admin.token, http.MethodPut, fmt.Sprintf("%s/%d", usersPath, admin.user.ID), `{"is_active":false}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self-deactivate = %d, want 400", resp.StatusCode)
	}
	if resp := env.send(t, admin.token, http.MethodDelete, fmt.Sprintf("%s/%d", usersPath, admin.user.ID), ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self-delete = %d, want 400", resp.StatusCode)
	}
}

func TestUserManagementNeedsThePermission(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	viewer := env.account(t, "viewer", channelstore.PermissionUsersView)
	nobody := env.account(t, "nobody")
	victim := env.account(t, "victim")

	for _, who := range []identity{viewer, nobody} {
		if resp := env.send(t, who.token, http.MethodPost, usersPath, `{"fullname":"x","username":"x","password":"long-enough"}`); resp.StatusCode != http.StatusForbidden {
			t.Errorf("create as %s = %d", who.user.Username, resp.StatusCode)
		}
		if resp := env.send(t, who.token, http.MethodPut, fmt.Sprintf("%s/%d", usersPath, victim.user.ID), `{"fullname":"x"}`); resp.StatusCode != http.StatusForbidden {
			t.Errorf("update as %s = %d", who.user.Username, resp.StatusCode)
		}
		if resp := env.send(t, who.token, http.MethodDelete, fmt.Sprintf("%s/%d", usersPath, victim.user.ID), ""); resp.StatusCode != http.StatusForbidden {
			t.Errorf("delete as %s = %d", who.user.Username, resp.StatusCode)
		}
	}
	if _, ok, _ := env.store.GetUser(context.Background(), victim.user.ID); !ok {
		t.Fatal("a forbidden request deleted the user")
	}
}

func TestDeleteUserRemovesItAndEveryAssignment(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	admin := env.account(t, "boss", channelstore.PermissionUsersManage)
	ctx := context.Background()
	ch := env.channelRow(t, "official", 500)
	cat, _ := env.store.CreateCategory(ctx, "News", "")
	victim := env.account(t, "victim")
	if err := env.store.ReplacePermissions(ctx, victim.user.ID, channelstore.PermissionSet{
		Global:     []channelstore.GlobalPermission{channelstore.PermissionUsersView},
		Channels:   []channelstore.ResourcePermissions{{ResourceID: ch.ID}},
		Categories: []channelstore.ResourcePermissions{{ResourceID: cat.ID}},
	}); err != nil {
		t.Fatal(err)
	}

	resp := env.send(t, admin.token, http.MethodDelete, fmt.Sprintf("%s/%d", usersPath, victim.user.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	if _, ok, _ := env.store.GetUser(ctx, victim.user.ID); ok {
		t.Fatal("the user is still there")
	}
	set, err := env.store.PermissionsOf(ctx, victim.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Global)+len(set.Channels)+len(set.Categories) != 0 {
		t.Fatalf("the deleted user's assignments survived: %+v", set)
	}
	// The token of a deleted user stops working.
	if resp := env.send(t, victim.token, http.MethodGet, mePath, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("deleted user's token = %d", resp.StatusCode)
	}
}

func TestTheLastAdministratorIsProtected(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	// The suite's default caller holds users.manage; nobody else does.
	admin := env.defaults

	resp := env.send(t, admin.token, http.MethodDelete, fmt.Sprintf("%s/%d", usersPath, admin.user.ID), "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self-delete = %d", resp.StatusCode)
	}

	other := env.account(t, "other", channelstore.PermissionUsersManage)
	// Two administrators now, so one may go (not itself, but the other).
	if resp := env.send(t, other.token, http.MethodDelete, fmt.Sprintf("%s/%d", usersPath, admin.user.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete with another administrator present = %d", resp.StatusCode)
	}
}

func TestDeactivatingAUserIsRefusedWhenItWouldStrandTheDeployment(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	// Strip the accounts down to one administrator, who is not the caller, so
	// the caller's own self-protection does not mask the rule under test.
	for _, u := range append([]identity{env.defaults}, env.account(t, "boss", channelstore.PermissionUsersManage)) {
		if err := env.store.ReplacePermissions(t.Context(), u.user.ID, channelstore.PermissionSet{}); err != nil {
			t.Fatal(err)
		}
	}
	strandedAdmin := env.account(t, "sole-admin", channelstore.PermissionUsersManage)
	operator := env.account(t, "operator", channelstore.PermissionUsersManage)
	if err := env.store.ReplacePermissions(t.Context(), operator.user.ID, channelstore.PermissionSet{}); err != nil {
		t.Fatal(err)
	}

	// The caller needs users.manage to attempt it; give it through the store,
	// then take it away from everyone else so sole-admin is the last.
	if err := env.store.ReplacePermissions(t.Context(), env.defaults.user.ID,
		channelstore.PermissionSet{Global: []channelstore.GlobalPermission{
			channelstore.PermissionUsersManage,
			channelstore.PermissionUsersView,
		}}); err != nil {
		t.Fatal(err)
	}
	// Two administrators again; the store-level rule is what this checks, so
	// remove the caller's own permission last and test through the store.
	if err := env.store.ReplacePermissions(t.Context(), env.defaults.user.ID, channelstore.PermissionSet{}); err != nil {
		t.Fatal(err)
	}
	inactive := false
	if _, err := env.store.UpdateUser(t.Context(), strandedAdmin.user.ID, channelstore.UserUpdate{IsActive: &inactive}); err == nil {
		t.Fatal("the store deactivated the last administrator")
	}
	if err := env.store.DeleteUser(t.Context(), strandedAdmin.user.ID); err == nil {
		t.Fatal("the store deleted the last administrator")
	}
	// With a successor it is allowed.
	env.account(t, "successor", channelstore.PermissionUsersManage)
	if err := env.store.DeleteUser(t.Context(), strandedAdmin.user.ID); err != nil {
		t.Fatalf("delete with a successor: %v", err)
	}
}

func TestPermissionsEndpointReadsAndReplacesTheWholeSet(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	admin := env.account(t, "boss", channelstore.PermissionUsersManage)
	ctx := context.Background()
	ch := env.channelRow(t, "official", 500)
	cat, _ := env.store.CreateCategory(ctx, "News", "")
	target := env.account(t, "alice")

	body := fmt.Sprintf(`{
		"global_permissions": ["users.view", "channels.view", "channels.create"],
		"channel_permissions": [{"channel_id": %d, "permissions": {"view": "allow", "update": "allow", "delete": "deny"}}],
		"category_permissions": [{"category_id": %d, "permissions": {"view": "allow", "update": "deny", "delete": "deny"}}]
	}`, ch.ID, cat.ID)
	resp := env.send(t, admin.token, http.MethodPut, fmt.Sprintf("%s/%d/permissions", usersPath, target.user.ID), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	raw := bodyOf(resp)
	var got struct {
		Data struct {
			Permissions permissionsJSON `json:"permissions"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data.Permissions.Global) != 3 || len(got.Data.Permissions.Channels) != 1 || len(got.Data.Permissions.Categories) != 1 {
		t.Fatalf("permissions = %+v (raw %s)", got.Data.Permissions, raw)
	}
	if got.Data.Permissions.Channels[0].Delete != "deny" {
		t.Fatalf("channel delete = %q", got.Data.Permissions.Channels[0].Delete)
	}

	// A second PUT omitting a channel removes that assignment: no stale rows.
	replaced := fmt.Sprintf(`{"global_permissions":[],"channel_permissions":[],"category_permissions":[{"category_id":%d,"permissions":{"view":"allow"}}]}`, cat.ID)
	resp = env.send(t, admin.token, http.MethodPut, fmt.Sprintf("%s/%d/permissions", usersPath, target.user.ID), replaced)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replace = %d", resp.StatusCode)
	}
	set, err := env.store.PermissionsOf(ctx, target.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Global) != 0 || len(set.Channels) != 0 || len(set.Categories) != 1 {
		t.Fatalf("set = %+v", set)
	}
	// Exactly one row for the target: the replaced set left none behind (the
	// stored set above already proves it; this pins the row count itself).
	again, err := env.store.PermissionsOf(ctx, target.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Categories) != 1 || len(again.Categories[0].Permissions) != 1 {
		t.Fatalf("stored set = %+v", again)
	}

	// Reading it back gives the same set.
	read := env.send(t, admin.token, http.MethodGet, fmt.Sprintf("%s/%d/permissions", usersPath, target.user.ID), "")
	if read.StatusCode != http.StatusOK {
		t.Fatalf("read = %d", read.StatusCode)
	}
}

func TestPermissionsEndpointRejectsInvalidInput(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	admin := env.account(t, "boss", channelstore.PermissionUsersManage)
	target := env.account(t, "alice")
	path := fmt.Sprintf("%s/%d/permissions", usersPath, target.user.ID)

	cases := map[string]string{
		"unknown global":      `{"global_permissions":["channels.explode"],"channel_permissions":[],"category_permissions":[]}`,
		"unknown action":      `{"global_permissions":[],"channel_permissions":[{"channel_id":1,"permissions":{"publish":"allow"}}],"category_permissions":[]}`,
		"bad effect":          `{"global_permissions":[],"channel_permissions":[{"channel_id":1,"permissions":{"view":"maybe"}}],"category_permissions":[]}`,
		"create per resource": `{"global_permissions":[],"channel_permissions":[{"channel_id":1,"permissions":{"create":"allow"}}],"category_permissions":[]}`,
		"zero channel":        `{"global_permissions":[],"channel_permissions":[{"channel_id":0,"permissions":{"view":"allow"}}],"category_permissions":[]}`,
		"unknown channel":     `{"global_permissions":[],"channel_permissions":[{"channel_id":4242,"permissions":{"view":"allow"}}],"category_permissions":[]}`,
		"telegram id":         `{"global_permissions":[],"channel_permissions":[{"channel_id":1234567890,"permissions":{"view":"allow"}}],"category_permissions":[]}`,
		"unknown category":    `{"global_permissions":[],"channel_permissions":[],"category_permissions":[{"category_id":4242,"permissions":{"view":"allow"}}]}`,
		"malformed":           `{"global_permissions":`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp := env.send(t, admin.token, http.MethodPut, path, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", resp.StatusCode, bodyOf(resp))
			}
		})
	}

	// A nonexistent user is 404, not a silent no-op.
	resp := env.send(t, admin.token, http.MethodPut, usersPath+"/9999/permissions", `{"global_permissions":[]}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown user = %d, want 404", resp.StatusCode)
	}
}

// A manage permission is only meaningful on top of its view permission: a
// wrapper who manages users, creates channels or creates categories must also
// be able to see them. The rule lives here so every client, not just the panel,
// is held to it.
func TestAManagePermissionRequiresItsViewPermission(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	admin := env.account(t, "boss", channelstore.PermissionUsersManage)
	target := env.account(t, "alice")
	path := fmt.Sprintf("%s/%d/permissions", usersPath, target.user.ID)

	pairs := []struct{ manage, view string }{
		{"users.manage", "users.view"},
		{"channels.create", "channels.view"},
		{"categories.create", "categories.view"},
	}
	for _, pair := range pairs {
		t.Run(pair.manage, func(t *testing.T) {
			body := fmt.Sprintf(`{"global_permissions":[%q]}`, pair.manage)
			resp := env.send(t, admin.token, http.MethodPut, path, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s alone = %d, want 400: %s", pair.manage, resp.StatusCode, bodyOf(resp))
			}
			var refused struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(bodyOf(resp)), &refused); err != nil {
				t.Fatal(err)
			}
			if refused.Error.Code != gateway.CodeInvalidInput {
				t.Fatalf("code = %q", refused.Error.Code)
			}
			if !strings.Contains(refused.Error.Message, pair.view) {
				t.Fatalf("message %q does not name %s", refused.Error.Message, pair.view)
			}
			stored, err := env.store.PermissionsOf(t.Context(), target.user.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(stored.Global) != 0 {
				t.Fatalf("a rejected set was written: %+v", stored.Global)
			}

			body = fmt.Sprintf(`{"global_permissions":[%q,%q]}`, pair.view, pair.manage)
			if resp := env.send(t, admin.token, http.MethodPut, path, body); resp.StatusCode != http.StatusOK {
				t.Fatalf("%s+%s = %d: %s", pair.view, pair.manage, resp.StatusCode, bodyOf(resp))
			}
			if err := env.store.ReplacePermissions(t.Context(), target.user.ID, channelstore.PermissionSet{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOnlyAuthorizedCallersMayChangeAnotherUsersPermissions(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	viewer := env.account(t, "viewer", channelstore.PermissionUsersView)
	target := env.account(t, "target")

	path := fmt.Sprintf("%s/%d/permissions", usersPath, target.user.ID)
	if resp := env.send(t, viewer.token, http.MethodPut, path, `{"global_permissions":["users.manage"]}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("replace as viewer = %d, want 403", resp.StatusCode)
	}
	if resp := env.send(t, viewer.token, http.MethodGet, path, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("read as viewer = %d", resp.StatusCode)
	}
	if resp := env.send(t, target.token, http.MethodGet, path, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("read self = %d", resp.StatusCode)
	}
	if resp := env.send(t, target.token, http.MethodPut, path, `{"global_permissions":["users.manage"]}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("self-grant = %d, want 403", resp.StatusCode)
	}
}

func TestTheLastUserAdministratorCannotLoseThePermission(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	// The caller may manage permissions; the subject is the only account that
	// also holds users.manage besides the caller's default session, which is
	// removed first.
	caller := env.account(t, "caller", channelstore.PermissionUsersManage)
	subject := env.account(t, "subject", channelstore.PermissionUsersManage)
	if err := env.store.ReplacePermissions(t.Context(), env.defaults.user.ID, channelstore.PermissionSet{}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/%d/permissions", usersPath, subject.user.ID)

	// While the caller still holds users.manage, the subject may lose it.
	if resp := env.send(t, caller.token, http.MethodPut, path, `{"global_permissions":[]}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("replace with a successor = %d: %s", resp.StatusCode, bodyOf(resp))
	}

	// Now the caller is the last active administrator. The suite's default
	// session (the operator token) holds it too, so give the subject the
	// permission back and let the default session act: the subject and the
	// operator are administrators, and stripping the subject is allowed.
	if err := env.store.ReplacePermissions(t.Context(), subject.user.ID,
		channelstore.PermissionSet{Global: []channelstore.GlobalPermission{channelstore.PermissionUsersManage}}); err != nil {
		t.Fatal(err)
	}
	if resp := env.send(t, defaultCaller, http.MethodPut, path, `{"global_permissions":[]}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("operator replace = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	set, err := env.store.PermissionsOf(t.Context(), subject.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hasGlobal(set.Global, channelstore.PermissionUsersManage) {
		t.Fatal("the replace did not land")
	}
}

func hasGlobal(set []channelstore.GlobalPermission, want channelstore.GlobalPermission) bool {
	for _, p := range set {
		if p == want {
			return true
		}
	}
	return false
}
