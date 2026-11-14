package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

// These tests check the authorization rules one by one: what is allowed, what
// is denied, and that the answer comes from the database on this request.

// loginPath and mePath are the session routes the tests exercise.
const (
	loginPath = "/api/v1/auth/login"
	mePath    = "/api/v1/auth/me"
)

// permissionsJSON is the permission object as the API renders it.
type permissionsJSON struct {
	Global   []string `json:"global"`
	Channels []struct {
		ChannelID int64  `json:"channel_id"`
		View      string `json:"view"`
		Update    string `json:"update"`
		Delete    string `json:"delete"`
	} `json:"channels"`
	Categories []struct {
		CategoryID int64  `json:"category_id"`
		View       string `json:"view"`
		Update     string `json:"update"`
		Delete     string `json:"delete"`
	} `json:"categories"`
}

// userBody is the safe user shape the API returns.
type userBody struct {
	ID          int64  `json:"id"`
	Fullname    string `json:"fullname"`
	Username    string `json:"username"`
	IsActive    bool   `json:"is_active"`
	Permissions struct {
		Global   []string `json:"global"`
		Channels []struct {
			ChannelID int64  `json:"channel_id"`
			View      string `json:"view"`
			Update    string `json:"update"`
			Delete    string `json:"delete"`
		} `json:"channels"`
		Categories []struct {
			CategoryID int64  `json:"category_id"`
			View       string `json:"view"`
			Update     string `json:"update"`
			Delete     string `json:"delete"`
		} `json:"categories"`
	} `json:"permissions"`
}

// --- authentication ---

type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func TestLoginReturnsABearerTokenForAValidPassword(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice", channelstore.PermissionUsersView)

	resp := env.send(t, "", http.MethodPost, loginPath, `{"username":"alice","password":"password-alice"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	body := decode[loginResponse](t, resp)
	if body.TokenType != "Bearer" || body.AccessToken == "" {
		t.Fatalf("login = %+v", body)
	}
	if !who.user.CreatedAt.IsZero() && body.ExpiresIn <= 0 {
		t.Fatalf("expires_in = %d", body.ExpiresIn)
	}

	// The token names the account and works on a protected route.
	if resp := env.send(t, body.AccessToken, http.MethodGet, mePath, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("me = %d", resp.StatusCode)
	}
}

func TestLoginRejectsAWrongPasswordAndAnUnknownUserTheSameWay(t *testing.T) {
	env := newAccountEnv(t, registry{})
	env.account(t, "alice")

	wrong := env.send(t, "", http.MethodPost, loginPath, `{"username":"alice","password":"not-the-password"}`)
	unknown := env.send(t, "", http.MethodPost, loginPath, `{"username":"nobody","password":"not-the-password"}`)

	if wrong.StatusCode != http.StatusUnauthorized || unknown.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong = %d, unknown = %d", wrong.StatusCode, unknown.StatusCode)
	}
	wrongBody, unknownBody := bodyOf(wrong), bodyOf(unknown)
	if stripRequestID(wrongBody) != stripRequestID(unknownBody) {
		t.Fatalf("the two failures differ:\n%s\n%s", wrongBody, unknownBody)
	}
}

func TestLoginRefusesAnInactiveAccount(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice")
	inactive := false
	if _, err := env.store.UpdateUser(context.Background(), who.user.ID, channelstore.UserUpdate{IsActive: &inactive}); err != nil {
		t.Fatal(err)
	}
	resp := env.send(t, "", http.MethodPost, loginPath, `{"username":"alice","password":"password-alice"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := decode[errorEnvelope](t, resp).Error.Code; code != "user_inactive" {
		t.Fatalf("code = %q", code)
	}
}

func TestProtectedRoutesRejectMissingInvalidAndExpiredTokens(t *testing.T) {
	// A signer whose clock is two hours ahead: the token it issues is already
	// expired for the gateway, which uses the real clock.
	ahead, err := auth.NewTokens(auth.TokenOptions{
		Secret: []byte(strings.Repeat("k", 48)),
		TTL:    time.Hour,
		Now:    func() time.Time { return time.Now().Add(2 * time.Hour) },
	})
	if err != nil {
		t.Fatal(err)
	}
	env := newAccountEnv(t, registry{}, func(c *gateway.Config) {
		c.Tokens = ahead
	})
	who := env.account(t, "alice", channelstore.GlobalPermissions()...)
	expired := who.token
	env.session = ""

	// A caller with no credential, then one whose credential the gateway
	// cannot accept.
	for _, path := range protectedPaths {
		if resp := env.sendAnon(t, http.MethodGet, path); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("missing %s = %d, want 401", path, resp.StatusCode)
		}
	}
	for name, token := range map[string]string{
		"garbage":  "not-a-jwt",
		"tampered": who.token[:len(who.token)-2] + "xx",
		"expired":  expired,
	} {
		for _, path := range protectedPaths {
			hreq := httptest.NewRequest(http.MethodGet, path, nil)
			hreq.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			env.handler.ServeHTTP(rec, hreq)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s = %d, want 401", name, path, rec.Code)
			}
		}
	}
	if resp := env.send(t, expired, http.MethodGet, mePath, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expired me = %d", resp.StatusCode)
	} else if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("an expired token should be described in WWW-Authenticate")
	}
}

var protectedPaths = []string{
	"/api/v1/telegram/official/health",
	"/api/v1/telegram/official/profile",
	"/api/v1/telegram/official/channels",
	"/api/v1/telegram/official/channels/find?q=500",
	"/api/v1/telegram/official/channels/1",
	"/api/v1/telegram/official/channels/1/messages",
	"/api/v1/telegram/official/channels/1/messages/9",
	"/api/v1/telegram/official/channels/1/messages/9/media",
	"/api/v1/telegram/official/channels/1/avatar",
	categoriesPath,
	categoriesPath + "/1",
	"/api/v1/local/users",
	"/api/v1/local/users/1",
	"/api/v1/local/users/1/permissions",
	mePath,
}

func TestAMissingOrDuplicateTokenDoesNotRevealWhetherAClientExists(t *testing.T) {
	env := newAccountEnv(t, registry{})
	env.session = ""
	known := env.send(t, "", http.MethodGet, "/api/v1/telegram/official/health", "")
	unknown := env.send(t, "", http.MethodGet, "/api/v1/telegram/not-configured/health", "")
	if known.StatusCode != http.StatusUnauthorized || unknown.StatusCode != http.StatusUnauthorized {
		t.Fatalf("known = %d, unknown = %d", known.StatusCode, unknown.StatusCode)
	}
}

func TestMeReportsTheStoredPermissionsWithoutTheHash(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice", channelstore.PermissionUsersView, channelstore.PermissionCategoriesCreate)
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	ch := env.ownedChannelRow(t, acc, "official", 500)
	if err := env.store.ReplacePermissions(context.Background(), who.user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionUsersView, channelstore.PermissionCategoriesCreate},
		Channels: []channelstore.ResourcePermissions{{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionUpdate, Effect: channelstore.EffectDeny},
		}}},
	}); err != nil {
		t.Fatal(err)
	}

	resp := env.send(t, who.token, http.MethodGet, mePath, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw := bodyOf(resp)
	if strings.Contains(raw, "password") || strings.Contains(raw, "argon2") {
		t.Fatalf("me exposes the password hash: %s", raw)
	}
	var body struct {
		Data userBody `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.ID != who.user.ID || body.Data.Username != "alice" {
		t.Fatalf("me = %+v", body.Data)
	}
	if len(body.Data.Permissions.Global) != 2 {
		t.Fatalf("global = %v", body.Data.Permissions.Global)
	}
	if len(body.Data.Permissions.Channels) != 1 {
		t.Fatalf("channels = %+v", body.Data.Permissions.Channels)
	}
	entry := body.Data.Permissions.Channels[0]
	if entry.ChannelID != ch.ID || entry.View != "allow" || entry.Update != "deny" || entry.Delete != "" {
		t.Fatalf("channel assignment = %+v, want the local id %d and the effects as stored", entry, ch.ID)
	}
}

func TestPermissionChangesTakeEffectWithoutANewToken(t *testing.T) {
	env := newAccountEnv(t, registry{})
	admin := env.account(t, "admin", channelstore.PermissionUsersManage)
	viewer := env.account(t, "viewer", channelstore.PermissionUsersView)

	// The viewer may list users now.
	if resp := env.send(t, viewer.token, http.MethodGet, "/api/v1/local/users", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("list as viewer = %d", resp.StatusCode)
	}
	// Remove the permission; the same token must stop working at once.
	set := channelstore.PermissionSet{}
	if err := env.store.ReplacePermissions(context.Background(), viewer.user.ID, set); err != nil {
		t.Fatal(err)
	}
	resp := env.send(t, viewer.token, http.MethodGet, mePath, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me after revocation = %d", resp.StatusCode)
	}
	var body struct {
		Data userBody `json:"data"`
	}
	_ = json.Unmarshal([]byte(bodyOf(resp)), &body)
	if len(body.Data.Permissions.Global) != 0 {
		t.Fatalf("me still reports %v", body.Data.Permissions.Global)
	}
	narrowed := decode[userPage](t, env.send(t, viewer.token, http.MethodGet, "/api/v1/local/users", ""))
	if narrowed.Meta.Total != 1 || len(narrowed.Data.Users) != 1 || narrowed.Data.Users[0].ID != viewer.user.ID {
		t.Fatalf("list after revocation = %+v, want only the caller", narrowed)
	}
	_ = admin
}

func TestDeactivatingAnAccountInvalidatesItsTokenImmediately(t *testing.T) {
	env := newAccountEnv(t, registry{})
	env.sessionAccount(t, env.defaults, "official", channelstore.NetworkTelegram, readyWorker())
	admin := env.account(t, "admin", channelstore.PermissionUsersManage)
	other := env.account(t, "other")

	if resp := env.send(t, other.token, http.MethodGet, "/api/v1/telegram/official/health", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("health as other = %d", resp.StatusCode)
	}
	resp := env.send(t, admin.token, http.MethodPut, fmt.Sprintf("/api/v1/local/users/%d", other.user.ID), `{"is_active":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deactivate = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	if resp := env.send(t, other.token, http.MethodGet, "/api/v1/telegram/official/health", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("health after deactivation = %d, want 401", resp.StatusCode)
	}
}

// --- channel permissions ---

func TestOneChannelFullAndAnotherViewOnly(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	full := env.ownedChannelRow(t, acc, "official", 500)
	view := env.ownedChannelRow(t, acc, "official", 600)
	// The caller needs their own account on the server before any keyed
	// route reaches the permission check this is testing.
	env.grantChannels(t, who,
		channelstore.ResourcePermissions{ResourceID: full.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
		}},
		channelstore.ResourcePermissions{ResourceID: view.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}},
	)

	for _, id := range []int64{500, 600} {
		if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("/api/v1/telegram/official/channels/%d/messages", id), ""); resp.StatusCode != http.StatusOK {
			t.Errorf("messages for %d = %d", id, resp.StatusCode)
		}
	}
}

func TestAChannelWithoutAGrantIsForbidden(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	wanted := env.ownedChannelRow(t, acc, "official", 500)
	// The caller needs their own account on the server before any keyed
	// route reaches the permission check this is testing.
	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: wanted.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
	}})

	for _, path := range []string{
		"/api/v1/telegram/official/channels/600/messages",
		"/api/v1/telegram/official/channels/600/messages/9",
		"/api/v1/telegram/official/channels/600/avatar",
		"/api/v1/telegram/official/channels/600",
	} {
		if resp := env.send(t, who.token, http.MethodGet, path, ""); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", path, resp.StatusCode)
		}
	}
	// And an unmanaged channel is not reachable per-channel either.
	if resp := env.send(t, who.token, http.MethodGet, "/api/v1/telegram/official/channels/700/messages", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unmanaged channel = %d, want 403", resp.StatusCode)
	}
}

func TestAnExplicitChannelDenyOverridesTheGlobalView(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice", channelstore.PermissionChannelsView)
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	hidden := env.ownedChannelRow(t, acc, "official", 500)
	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: hidden.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
	}})

	if resp := env.send(t, who.token, http.MethodGet, "/api/v1/telegram/official/channels/500/messages", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("denied channel = %d, want 403", resp.StatusCode)
	}
	// Another, unassigned channel is still visible through the global view.
	if resp := env.send(t, who.token, http.MethodGet, "/api/v1/telegram/official/channels/600/messages", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("unassigned channel = %d, want 200", resp.StatusCode)
	}
}

func TestChannelPermissionsUseTheLocalIDNotTheTelegramID(t *testing.T) {
	env := newAccountEnv(t, registry{})
	const telegramID = 1234567890
	who := env.account(t, "alice")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	ch := env.ownedChannelRow(t, acc, "official", telegramID)
	if ch.ID == telegramID {
		t.Skip("the local id happens to equal the Telegram id; nothing to distinguish")
	}

	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
	}})
	if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("/api/v1/telegram/official/channels/%d/messages", telegramID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("local id as a grant = %d, want 200", resp.StatusCode)
	}
}

func TestAnExplicitChannelAllowGrantsWithoutAGlobalPermission(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	ch := env.ownedChannelRow(t, acc, "official", 500)
	// The caller needs their own account on the server before any keyed
	// route reaches the permission check this is testing.
	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
	}})
	if resp := env.send(t, who.token, http.MethodGet, "/api/v1/telegram/official/channels/500/messages", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("explicit allow = %d, want 200", resp.StatusCode)
	}
}

func TestTheChannelListIsFilteredAndItsTotalRestated(t *testing.T) {
	listed := dialogsOpensWith(500, 600, 700, 800)
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, listed)
	only := env.ownedChannelRow(t, acc, "official", 600)
	env.ownedChannelRow(t, acc, "official", 700) // managed, but no grant and a deny below
	denied := env.ownedChannelRow(t, acc, "official", 800)
	// The caller needs their own account on the server before any keyed
	// route reaches the permission check this is testing.
	env.grantChannels(t, who,
		channelstore.ResourcePermissions{ResourceID: only.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}},
		channelstore.ResourcePermissions{ResourceID: denied.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
		}},
	)

	resp := env.send(t, who.token, http.MethodGet, "/api/v1/telegram/official/channels", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Channels []struct {
				ID int64 `json:"id"`
			} `json:"channels"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(bodyOf(resp)), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Channels) != 1 || body.Data.Channels[0].ID != 600 {
		t.Fatalf("channels = %+v, want only 600", body.Data.Channels)
	}
	if body.Meta.Total != 1 {
		t.Fatalf("total = %d, want 1 (the count the caller may see)", body.Meta.Total)
	}
}

func TestTheGlobalChannelViewListsEverythingWithoutDenies(t *testing.T) {
	listed := dialogsOpensWith(500, 600)
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice", channelstore.PermissionChannelsView)
	env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, listed)

	resp := env.send(t, who.token, http.MethodGet, "/api/v1/telegram/official/channels", "")
	var body struct {
		Data struct {
			Channels []json.RawMessage `json:"channels"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	_ = json.Unmarshal([]byte(bodyOf(resp)), &body)
	if resp.StatusCode != http.StatusOK || len(body.Data.Channels) != 2 || body.Meta.Total != 2 {
		t.Fatalf("status=%d channels=%d total=%d", resp.StatusCode, len(body.Data.Channels), body.Meta.Total)
	}
}

func TestFindHidesChannelsTheCallerMayNotSee(t *testing.T) {
	worker := readyWorker()
	worker.find = rpc.FindResult{Channels: []rpc.ChannelInfo{{ID: "500"}, {ID: "600"}, {ID: "700"}}}
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, worker)
	visible := env.ownedChannelRow(t, acc, "official", 500)
	hidden := env.ownedChannelRow(t, acc, "official", 600)
	// The caller needs their own account on the server before any keyed
	// route reaches the permission check this is testing.
	env.grantChannels(t, who,
		channelstore.ResourcePermissions{ResourceID: visible.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}},
		channelstore.ResourcePermissions{ResourceID: hidden.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
		}},
	)

	resp := env.send(t, who.token, http.MethodGet, "/api/v1/telegram/official/channels/find?q=500", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw := bodyOf(resp)
	if !strings.Contains(raw, `"id":"500"`) {
		t.Fatalf("the visible channel is missing: %s", raw)
	}
	if strings.Contains(raw, `"id":"600"`) || strings.Contains(raw, `"id":"700"`) {
		t.Fatalf("a prohibited or unmanaged channel leaked: %s", raw)
	}
}

func TestJoiningNeedsTheGlobalCreatePermission(t *testing.T) {
	worker := readyWorker()
	worker.join = rpc.JoinResult{
		Channel: rpc.ChannelInfo{ID: "500", Type: "channel", Title: "Example", IsMember: true, MembershipStatus: "member"},
		Outcome: rpc.OutcomeJoined,
	}
	env := newAccountEnv(t, registry{})
	noCreate := env.account(t, "alice", channelstore.PermissionChannelsView)
	env.sessionAccount(t, noCreate, "official", channelstore.NetworkTelegram, worker)

	resp := env.send(t, noCreate.token, http.MethodPost, "/api/v1/telegram/official/channels/500/join", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("join without channels.create = %d, want 403", resp.StatusCode)
	}
	if worker.callCount() != 0 {
		t.Fatal("a forbidden join reached the worker")
	}

	creator := env.account(t, "bob", channelstore.PermissionChannelsCreate)
	env.sessionAccount(t, creator, "official", channelstore.NetworkTelegram, worker)
	resp = env.send(t, creator.token, http.MethodPost, "/api/v1/telegram/official/channels/500/join", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join with channels.create = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	// The creator gets no per-channel grant from joining: only the global
	// permission they already held.
	set, err := env.store.PermissionsOf(context.Background(), creator.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Channels) != 0 {
		t.Fatalf("join granted per-channel rights: %+v", set.Channels)
	}
}

func TestProfileNeedsSomeChannelVisibility(t *testing.T) {
	env := newAccountEnv(t, registry{})
	// The channel is somebody's registration, so somebody has to own it.
	owner := env.account(t, "owner")
	acc := env.sessionAccount(t, owner, "official", channelstore.NetworkTelegram, readyWorker())
	ch := env.ownedChannelRow(t, acc, "official", 500)
	// Each caller acts through their own account. "nothing" owns one but can see
	// no channel; "granted" owns one and can see one of their own.
	nothing := env.account(t, "nobody")
	env.sessionAccount(t, nothing, "official", channelstore.NetworkTelegram, readyWorker())
	// "nothing" may not see the owner's channel, so it holds no visible channel
	// at all — which is exactly the state this case is about.
	env.grantChannels(t, nothing, channelstore.ResourcePermissions{ResourceID: ch.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
	}})
	if resp := env.send(t, nothing.token, http.MethodGet, "/api/v1/telegram/official/profile", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("profile without a channel view = %d, want 403", resp.StatusCode)
	}
	granted := env.account(t, "granted")
	acc2 := env.sessionAccount(t, granted, "official", channelstore.NetworkTelegram, readyWorker())
	seen := env.ownedChannelRow(t, acc2, "official", 510)
	env.grantChannels(t, granted, channelstore.ResourcePermissions{ResourceID: seen.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
	}})
	if resp := env.send(t, granted.token, http.MethodGet, "/api/v1/telegram/official/profile", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("profile with one channel grant = %d, want 200", resp.StatusCode)
	}
}

// --- category permissions ---

func TestTwoCategoriesWithDifferentPermissions(t *testing.T) {
	env := newAccountEnv(t, registry{})
	first, _ := env.store.CreateCategory(context.Background(), "Editable", "")
	second, _ := env.store.CreateCategory(context.Background(), "Viewable", "")
	third, _ := env.store.CreateCategory(context.Background(), "Hidden", "")
	who := env.account(t, "alice")
	env.grantCategories(t, who,
		channelstore.ResourcePermissions{ResourceID: first.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
		}},
		channelstore.ResourcePermissions{ResourceID: second.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}},
		channelstore.ResourcePermissions{ResourceID: third.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
			{Action: channelstore.ActionUpdate, Effect: channelstore.EffectDeny},
			{Action: channelstore.ActionDelete, Effect: channelstore.EffectDeny},
		}},
	)

	url := func(id int64) string { return fmt.Sprintf("%s/%d", categoriesPath, id) }

	if resp := env.send(t, who.token, http.MethodGet, url(first.ID), ""); resp.StatusCode != http.StatusOK {
		t.Errorf("view first = %d", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodPut, url(first.ID), `{"name":"Renamed","description":""}`); resp.StatusCode != http.StatusOK {
		t.Errorf("update first = %d", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodGet, url(second.ID), ""); resp.StatusCode != http.StatusOK {
		t.Errorf("view second = %d", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodPut, url(second.ID), `{"name":"Nope","description":""}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("update second = %d, want 403", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodGet, url(third.ID), ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("view third = %d, want 403", resp.StatusCode)
	}
}

func TestCategoryListingOmitsHiddenCategoriesAndCountsWhatRemains(t *testing.T) {
	env := newAccountEnv(t, registry{})
	env.signedIn = true
	ctx := context.Background()
	env.onlyCategoryPermissions(t)
	keep := env.grantedCategory(t, env.store, "Keep")
	if _, err := env.store.CreateCategory(ctx, "Hidden", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.CreateCategory(ctx, "NoGrant", ""); err != nil {
		t.Fatal(err)
	}
	_ = keep

	resp := env.send(t, defaultCaller, http.MethodGet, categoriesPath, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Categories []struct {
				ID int64 `json:"id"`
			} `json:"categories"`
		} `json:"data"`
		Meta struct {
			Total      int  `json:"total"`
			TotalPages int  `json:"total_pages"`
			HasMore    bool `json:"has_more"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(bodyOf(resp)), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Categories) != 1 || body.Data.Categories[0].ID != keep.ID {
		t.Fatalf("categories = %+v, want only the granted one", body.Data.Categories)
	}
	if body.Meta.Total != 1 {
		t.Fatalf("total = %d, want 1", body.Meta.Total)
	}
}

func TestCategoryPaginationIsAppliedAfterFiltering(t *testing.T) {
	env := newAccountEnv(t, registry{})
	env.signedIn = true
	env.onlyCategoryPermissions(t)
	for i := 0; i < 5; i++ {
		if _, err := env.store.CreateCategory(context.Background(), fmt.Sprintf("Hidden %d", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	var granted []channelstore.Category
	for i := 0; i < 3; i++ {
		granted = append(granted, env.grantedCategory(t, env.store, fmt.Sprintf("Visible %d", i)))
	}

	resp := env.send(t, defaultCaller, http.MethodGet, categoriesPath+"?page=2&per_page=2", "")
	var body struct {
		Data struct {
			Categories []struct {
				ID int64 `json:"id"`
			} `json:"categories"`
		} `json:"data"`
		Meta struct {
			Total      int  `json:"total"`
			TotalPages int  `json:"total_pages"`
			HasMore    bool `json:"has_more"`
		} `json:"meta"`
	}
	_ = json.Unmarshal([]byte(bodyOf(resp)), &body)
	if body.Meta.Total != 3 || body.Meta.TotalPages != 2 || body.Meta.HasMore {
		t.Fatalf("meta = %+v, want total 3, 2 pages, no more", body.Meta)
	}
	// Newest first: ids 8, 7, 6. Page 2 of 2 is the oldest of them.
	if len(body.Data.Categories) != 1 || body.Data.Categories[0].ID != granted[0].ID {
		t.Fatalf("page 2 = %+v, want category %d only", body.Data.Categories, granted[0].ID)
	}
}

func TestCreatingACategoryNeedsTheGlobalPermission(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "alice", channelstore.PermissionCategoriesView)
	if resp := env.send(t, who.token, http.MethodPost, categoriesPath, `{"name":"New","description":""}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create without categories.create = %d, want 403", resp.StatusCode)
	}
}

// --- helpers ---

func bodyOf(resp *http.Response) string {
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(data)
}

// stripRequestID removes the request id so two error bodies can be compared.
func stripRequestID(body string) string {
	var e errorEnvelope
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		return body
	}
	e.Error.RequestID = ""
	out, _ := json.Marshal(e)
	return string(out)
}

// The panel's navigation follows what the caller may read, so /me says so: an
// account with one section's view permission sees that section as readable
// and the others as not.
func TestMeReportsWhatTheCallerMayRead(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "reader", channelstore.PermissionUsersView)

	resp := env.send(t, who.token, http.MethodGet, mePath, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Access *struct {
				Channels   bool `json:"channels"`
				Categories bool `json:"categories"`
				Users      bool `json:"users"`
				Servers    bool `json:"servers"`
			} `json:"access"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(bodyOf(resp)), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Access == nil {
		t.Fatal("me reports no access summary")
	}
	got := *body.Data.Access
	if !got.Users || got.Channels || got.Categories {
		t.Fatalf("access = %+v, want only users", got)
	}
	if got.Servers {
		t.Fatal("users.view must not report the server status page as readable")
	}

	// servers.view is its own right: the panel's status page, and nothing that
	// was readable without it.
	watcher := env.account(t, "watcher", channelstore.PermissionServersView)
	body.Data.Access = nil
	if err := json.Unmarshal([]byte(bodyOf(env.send(t, watcher.token, http.MethodGet, mePath, ""))), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Access == nil || !body.Data.Access.Servers || body.Data.Access.Users || body.Data.Access.Channels || body.Data.Access.Categories {
		t.Fatalf("watcher access = %+v, want only servers", body.Data.Access)
	}

	// The operator holds every permission and has no account.
	op := env.send(t, operatorToken, http.MethodGet, mePath, "")
	if op.StatusCode != http.StatusOK {
		t.Fatalf("operator me = %d", op.StatusCode)
	}
	body.Data.Access = nil
	if err := json.Unmarshal([]byte(bodyOf(op)), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Access == nil || !body.Data.Access.Users || !body.Data.Access.Channels || !body.Data.Access.Categories || !body.Data.Access.Servers {
		t.Fatalf("operator access = %+v", body.Data.Access)
	}
}
