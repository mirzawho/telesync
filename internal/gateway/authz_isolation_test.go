package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// These tests are the reported bug and its neighbourhood: two resources of one
// kind, one account allowed to reach exactly one of them. Every answer is read
// over the real HTTP routes, the real authorization and a real SQLite store.

// allow is one resource-level grant of action view.
func allow(id int64) channelstore.ResourcePermissions {
	return channelstore.ResourcePermissions{ResourceID: id, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
	}}
}

// deny is one resource-level view:deny.
func deny(id int64) channelstore.ResourcePermissions {
	return channelstore.ResourcePermissions{ResourceID: id, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
	}}
}

// allowAll grants every action on one resource.
func allowAll(id int64) channelstore.ResourcePermissions {
	return channelstore.ResourcePermissions{ResourceID: id, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
		{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
	}}
}

// denyAll is one resource-level deny for every action a resource carries.
func denyAll(id int64) channelstore.ResourcePermissions {
	return channelstore.ResourcePermissions{ResourceID: id, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
		{Action: channelstore.ActionUpdate, Effect: channelstore.EffectDeny},
		{Action: channelstore.ActionDelete, Effect: channelstore.EffectDeny},
	}}
}

// channelIDsOf decodes the channel list of a local or Telegram listing.
func channelIDsOf(t *testing.T, raw string, field string) ([]int64, int) {
	t.Helper()
	var body struct {
		Data map[string][]struct {
			ID        int64 `json:"id"`
			ChannelID int64 `json:"channel_id"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	var ids []int64
	for _, ch := range body.Data[field] {
		ids = append(ids, ch.ID)
	}
	return ids, body.Meta.Total
}

func wantIDs(t *testing.T, what string, got []int64, want ...int64) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// --- channels: local namespace ---

func TestALimitedAccountSeesAndReachesOnlyItsOwnLocalChannel(t *testing.T) {
	// The registry is keyed by account id: a request resolves the caller's own
	// account first and then the worker that serves it. Registering under the
	// server key would be the old shared-connection world.
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	w := readyWorker()
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, w)
	env.workers[acc] = w
	a := env.ownedChannelRow(t, acc, "official", 1001)
	b := env.ownedChannelRow(t, acc, "official", 1002)
	env.grantChannels(t, who, allow(a.ID))

	raw := bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath, ""))
	ids, total := channelIDsOf(t, raw, "channels")
	wantIDs(t, "GET /local/channels", ids, a.ID)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}

	pathOf := func(id int64, sub string) string {
		return localChannelsPath + "/" + strconv.FormatInt(id, 10) + sub
	}
	allowed := []struct{ method, path, body string }{
		{http.MethodGet, pathOf(a.ID, ""), ""},
		{http.MethodGet, pathOf(a.ID, "/messages"), ""},
		{http.MethodGet, pathOf(a.ID, "/categories"), ""},
	}
	denied := []struct{ method, path, body string }{
		{http.MethodGet, pathOf(b.ID, ""), ""},
		{http.MethodGet, pathOf(b.ID, "/messages"), ""},
		{http.MethodGet, pathOf(b.ID, "/categories"), ""},
		{http.MethodPut, pathOf(b.ID, "/categories"), `{"category_ids":[]}`},
		// A refused leave answers before the body is read, so an editing
		// caller cannot probe which bodies the route would accept.
		{http.MethodPost, pathOf(b.ID, "/leave"), `{"telegram":false}`},
		{http.MethodPost, pathOf(b.ID, "/leave"), `{"category_ids":[]}`},
	}
	for _, c := range allowed {
		if resp := env.send(t, who.token, c.method, c.path, c.body); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s = %d: %s", c.method, c.path, resp.StatusCode, bodyOf(resp))
		}
	}
	for _, c := range denied {
		if resp := env.send(t, who.token, c.method, c.path, c.body); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403: %s", c.method, c.path, resp.StatusCode, bodyOf(resp))
		}
	}
	// The denied leave left the record alone.
	if _, found, err := env.store.GetByID(context.Background(), b.ID); err != nil || !found {
		t.Fatalf("channel B after a refused leave: found=%v err=%v", found, err)
	}
}

func TestSearchOfLocalChannelsDoesNotLeakNames(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	a := env.ownedChannelRow(t, acc, "official", 1001)
	env.ownedChannelRow(t, acc, "official", 1002) // title "Channel 1002"
	env.grantChannels(t, who, allow(a.ID))

	ids, total := channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath+"?q=1002", "")), "channels")
	if len(ids) != 0 || total != 0 {
		t.Fatalf("q=1002 = %v (total %d), want nothing", ids, total)
	}
	ids, _ = channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath+"?q=1001", "")), "channels")
	wantIDs(t, "q=1001", ids, a.ID)
}

// --- channels: Telegram namespace ---

func TestALimitedAccountCannotReachAnotherTelegramChannelByID(t *testing.T) {
	listed := dialogsOpensWith(1001, 1002)
	listed.messages = rpc.MessagePage{Messages: []rpc.Message{{ID: 5, Text: "hello"}}}
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	a := env.ownedChannelRow(t, acc, "official", 1001)
	env.ownedChannelRow(t, acc, "official", 1002)
	env.grantChannels(t, who, allow(a.ID))

	tele := "/api/v1/telegram/official/channels"
	ids, total := channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, tele, "")), "channels")
	wantIDs(t, "GET .../channels", ids, 1001)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	for _, path := range []string{tele + "/1001", tele + "/1001/messages"} {
		if resp := env.send(t, who.token, http.MethodGet, path, ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, bodyOf(resp))
		}
	}
	for _, path := range []string{tele + "/1002", tele + "/1002/messages"} {
		if resp := env.send(t, who.token, http.MethodGet, path, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("GET %s = %d, want 403", path, resp.StatusCode)
		}
	}
}

// --- categories ---

func TestALimitedAccountReachesOnlyItsOwnCategoryAndItsChannels(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	ctx := context.Background()
	catA, err := env.store.CreateCategory(ctx, "Alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	catB, err := env.store.CreateCategory(ctx, "Beta", "")
	if err != nil {
		t.Fatal(err)
	}
	chA := env.ownedChannelRow(t, acc, "official", 2001)
	chB := env.ownedChannelRow(t, acc, "official", 2002)
	if err := env.store.ReplaceCategories(ctx, chA.ClientKey, chA.ChannelID, []int64{catA.ID, catB.ID}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.ReplaceCategories(ctx, chB.ClientKey, chB.ChannelID, []int64{catB.ID}); err != nil {
		t.Fatal(err)
	}
	env.grantCategories(t, who, allowAll(catA.ID))
	env.grantChannels(t, who, allow(chA.ID), allow(chB.ID))

	catIDsOf := func(raw string) ([]int64, int) {
		var body struct {
			Data struct {
				Categories []struct {
					ID int64 `json:"id"`
				} `json:"categories"`
			} `json:"data"`
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		var out []int64
		for _, c := range body.Data.Categories {
			out = append(out, c.ID)
		}
		return out, body.Meta.Total
	}

	ids, total := catIDsOf(bodyOf(env.send(t, who.token, http.MethodGet, categoriesPath, "")))
	wantIDs(t, "GET /local/categories", ids, catA.ID)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	ids, _ = catIDsOf(bodyOf(env.send(t, who.token, http.MethodGet, categoriesPath+"?q=beta", "")))
	if len(ids) != 0 {
		t.Fatalf("q=beta = %v, want nothing", ids)
	}

	pathOf := func(id int64, sub string) string { return categoriesPath + "/" + strconv.FormatInt(id, 10) + sub }
	if resp := env.send(t, who.token, http.MethodGet, pathOf(catA.ID, ""), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET own category = %d", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodGet, pathOf(catB.ID, ""), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET other category = %d, want 403", resp.StatusCode)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, pathOf(catB.ID, "/channels"), ""},
		{http.MethodPut, pathOf(catB.ID, ""), `{"name":"x","description":""}`},
		{http.MethodDelete, pathOf(catB.ID, ""), ""},
	} {
		if resp := env.send(t, who.token, c.method, c.path, c.body); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
	// Beta still exists; Alpha still holds both channels (Beta is just hidden).
	if _, found, err := env.store.GetCategory(ctx, catB.ID); err != nil || !found {
		t.Fatalf("beta after a refused delete: found=%v err=%v", found, err)
	}
}

func TestACategoriesChannelListHidesChannelsTheCallerMayNotSee(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	ctx := context.Background()
	cat, err := env.store.CreateCategory(ctx, "Shared", "")
	if err != nil {
		t.Fatal(err)
	}
	a := env.ownedChannelRow(t, acc, "official", 3001)
	b := env.ownedChannelRow(t, acc, "official", 3002)
	for _, ch := range []channelstore.Channel{a, b} {
		if err := env.store.ReplaceCategories(ctx, acc, ch.ChannelID, []int64{cat.ID}); err != nil {
			t.Fatal(err)
		}
	}
	env.grantCategories(t, who, allow(cat.ID))
	env.grantChannels(t, who, allow(a.ID))

	ids, _ := channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet,
		categoriesPath+"/"+strconv.FormatInt(cat.ID, 10)+"/channels", "")), "channels")
	if fmt.Sprint(ids) != fmt.Sprint([]int64{a.ID}) {
		t.Fatalf("category channels = %v, want [%d]", ids, a.ID)
	}
	// The channel's own category list hides the category it may not see.
	var body struct {
		Data struct {
			Categories []struct {
				ID int64 `json:"id"`
			} `json:"categories"`
		} `json:"data"`
	}
	other, err := env.store.CreateCategory(ctx, "Elsewhere", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = env.store.ReplaceCategories(ctx, a.ClientKey, a.ChannelID, []int64{cat.ID, other.ID})
	env.grantChannels(t, who, allow(a.ID))
	raw := bodyOf(env.send(t, who.token, http.MethodGet,
		localChannelsPath+"/"+strconv.FormatInt(a.ID, 10)+"/categories", ""))
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Categories) != 1 || body.Data.Categories[0].ID != cat.ID {
		t.Fatalf("channel categories = %s, want only %d", raw, cat.ID)
	}
}

func TestAccessToAParentCategoryDoesNotExposeItsOtherChannels(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	ctx := context.Background()
	cat, err := env.store.CreateCategory(ctx, "Shared", "")
	if err != nil {
		t.Fatal(err)
	}
	a := env.ownedChannelRow(t, acc, "official", 3001)
	b := env.ownedChannelRow(t, acc, "official", 3002)
	for _, ch := range []channelstore.Channel{a, b} {
		if err := env.store.ReplaceCategories(ctx, ch.ClientKey, ch.ChannelID, []int64{cat.ID}); err != nil {
			t.Fatal(err)
		}
	}
	// A category grant alone must not admit one of its channels.
	env.grantCategories(t, who, allow(cat.ID))
	if resp := env.send(t, who.token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("channel through a category grant = %d, want 403", resp.StatusCode)
	}
	// A channel grant alone must not admit one of its categories.
	env.grantCategories(t, who)
	env.grantChannels(t, who, allow(a.ID))
	if resp := env.send(t, who.token, http.MethodGet, categoriesPath+"/"+strconv.FormatInt(cat.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("category through a channel grant = %d, want 403", resp.StatusCode)
	}
	// Filing a channel under a category the caller may not see is refused.
	if resp := env.send(t, who.token, http.MethodPut,
		localChannelsPath+"/"+strconv.FormatInt(a.ID, 10)+"/categories",
		`{"category_ids":[`+strconv.FormatInt(cat.ID, 10)+`]}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("filing under an unseen category = %d, want 403", resp.StatusCode)
	}
}

// --- WordPress ---

func createSite(t *testing.T, env *wpEnv, url string) int64 {
	t.Helper()
	body := `{"url":"` + url + `","username":"admin","password":"abcd EFGH 1234"}`
	resp, raw := env.create(t, defaultCaller, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create %s = %d: %s", url, resp.StatusCode, raw)
	}
	id := firstSite(t, raw).ID
	// The account the suite signs in as is the one the save hook names.
	ctx := context.Background()
	set, err := env.store.PermissionsOf(ctx, env.defaults.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	set.Wordsites = append(set.Wordsites, allowAll(id))
	if err := env.store.ReplacePermissions(ctx, env.defaults.user.ID, set); err != nil {
		t.Fatal(err)
	}
	return id
}

// setWordPress replaces one account's per-site assignments.
func (e *wpEnv) setWordPress(t *testing.T, who identity, sets ...channelstore.ResourcePermissions) {
	t.Helper()
	ctx := context.Background()
	current, err := e.store.PermissionsOf(ctx, who.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Wordsites = sets
	if err := e.store.ReplacePermissions(ctx, who.user.ID, current); err != nil {
		t.Fatal(err)
	}
}

func siteIDsOf(t *testing.T, raw string) []int64 {
	t.Helper()
	var body struct {
		Data struct {
			Sites []struct {
				ID int64 `json:"id"`
			} `json:"sites"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	var out []int64
	for _, s := range body.Data.Sites {
		out = append(out, s.ID)
	}
	return out
}

func TestALimitedAccountSeesAndReachesOnlyItsOwnWordPressSite(t *testing.T) {
	env := newWPEnv(t)
	a := createSite(t, env, "https://one.example.com")
	b := createSite(t, env, "https://two.example.com")
	who := env.account(t, "test", channelstore.PermissionWordPressManage)
	env.setWordPress(t, who, allow(a))

	raw := bodyOf(env.send(t, who.token, http.MethodGet, "/api/v1/local/wordpress", ""))
	wantIDs(t, "GET /local/wordpress", siteIDsOf(t, raw), a)
	if strings.Contains(raw, "two.example.com") {
		t.Fatalf("the listing names a site the caller may not see: %s", raw)
	}
	if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("/api/v1/local/wordpress/%d", a), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("get own site = %d", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("/api/v1/local/wordpress/%d", b), ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get other site = %d, want 404", resp.StatusCode)
	}

	// The manage right alone must not let it rewrite or delete another
	// registration, nor rotate another site's credential.
	before := env.storedSite(t, b)
	resp, raw := env.patch(t, who.token, b, `{"username":"editor"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch other site = %d, want 404: %s", resp.StatusCode, raw)
	}
	resp, raw = env.patch(t, who.token, b, `{"url":"https://evil.example.com","username":"admin","password":"rotated pass 1"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("rotate other site's credential = %d, want 404: %s", resp.StatusCode, raw)
	}
	if resp := env.send(t, who.token, http.MethodDelete, fmt.Sprintf("/api/v1/local/wordpress/%d", b), ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete other site = %d, want 404", resp.StatusCode)
	}
	// An explicit deny on this caller's own site stops it too, once more widely
	// permitted callers exist to manage that site.
	fellow := env.account(t, "fellow", channelstore.PermissionWordPressView, channelstore.PermissionWordPressManage)
	env.setWordPress(t, fellow, allow(a), allow(b))
	env.setWordPress(t, who, allow(b), denyAll(a))
	// `a` is `who`'s only site assignment, now an explicit deny; `b` was granted
	// later and replaces it (granting replaces the whole set), so the listing
	// names `b` alone.
	if ids := siteIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, "/api/v1/local/wordpress", ""))); fmt.Sprint(ids) != fmt.Sprint([]int64{b}) {
		t.Fatalf("listing under a deny = %v, want [%d]", ids, b)
	}
	beforeA := env.storedSite(t, a)
	for _, c := range []struct {
		method, path, body string
	}{
		{http.MethodGet, fmt.Sprintf("/api/v1/local/wordpress/%d", a), ""},
		{http.MethodPatch, fmt.Sprintf("/api/v1/local/wordpress/%d", a), `{"username":"editor"}`},
		{http.MethodDelete, fmt.Sprintf("/api/v1/local/wordpress/%d", a), ""},
	} {
		if resp := env.send(t, who.token, c.method, c.path, c.body); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s own site under a deny = %d, want 404", c.method, resp.StatusCode)
		}
	}
	afterA := env.storedSite(t, a)
	if afterA.Username != beforeA.Username {
		t.Fatalf("a denied site was modified: %q -> %q", beforeA.Username, afterA.Username)
	}
	after := env.storedSite(t, b)
	if after.Username != before.Username || after.URL != before.URL ||
		!bytes.Equal(after.Credential.Ciphertext, before.Credential.Ciphertext) {
		t.Fatalf("site B changed: %+v -> %+v", before, after)
	}
	if strings.Contains(raw, "abcd EFGH") || strings.Contains(raw, "rotated") {
		t.Fatalf("a credential reached the answer: %s", raw)
	}
	// Its own site is still writable once the deny is withdrawn.
	env.setWordPress(t, who, allow(b), allow(a))
	resp, raw = env.patch(t, who.token, a, `{"username":"editor"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch own site = %d: %s", resp.StatusCode, raw)
	}
}

// --- the account API: an empty assignment means "no access beyond the global" ---

func TestAnEmptyAssignmentNeverMeansEveryResource(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	a := env.ownedChannelRow(t, acc, "official", 1001)
	env.ownedChannelRow(t, acc, "official", 1002)

	resp := env.send(t, defaultCaller, http.MethodPut, "/api/v1/local/users/"+strconv.FormatInt(who.user.ID, 10)+"/permissions",
		`{"global_permissions":["channels.view"],"channel_permissions":[]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put user = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	// The global view still admits everything; that is the documented model.
	ids, total := channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath, "")), "channels")
	if len(ids) != 2 || total != 2 {
		t.Fatalf("global view = %v, want both", ids)
	}

	// The same empty list with no global view is no access, not everything.
	resp = env.send(t, defaultCaller, http.MethodPut, "/api/v1/local/users/"+strconv.FormatInt(who.user.ID, 10)+"/permissions",
		`{"global_permissions":[],"channel_permissions":[]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put user = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	ids, total = channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath, "")), "channels")
	if len(ids) != 0 || total != 0 {
		t.Fatalf("no assignment = %v (total %d), want nothing", ids, total)
	}
	if resp := env.send(t, who.token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no assignment detail = %d, want 403", resp.StatusCode)
	}
}

func TestRevokingAChannelGrantTakesEffectOnTheNextRequest(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	a := env.ownedChannelRow(t, acc, "official", 1001)
	env.grantChannels(t, who, allow(a.ID))
	if resp := env.send(t, who.token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("granted = %d", resp.StatusCode)
	}
	env.grantChannels(t, who)
	if resp := env.send(t, who.token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked = %d, want 403", resp.StatusCode)
	}
}

func TestAChannelDenyOverridesTheGlobalViewOnEveryRoute(t *testing.T) {
	listed := dialogsOpensWith(1001, 1002)
	listed.messages = rpc.MessagePage{Messages: []rpc.Message{{ID: 5, Text: "hello"}}}
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test", channelstore.PermissionChannelsView)
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	env.ownedChannelRow(t, acc, "official", 1001)
	b := env.ownedChannelRow(t, acc, "official", 1002)
	env.grantChannels(t, who, deny(b.ID))

	ids, total := channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath, "")), "channels")
	wantIDs(t, "local list with a deny", ids, 1)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	ids, _ = channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, "/api/v1/telegram/official/channels", "")), "channels")
	wantIDs(t, "telegram list with a deny", ids, 1001)
	for _, path := range []string{
		localChannelsPath + "/" + strconv.FormatInt(b.ID, 10),
		localChannelsPath + "/" + strconv.FormatInt(b.ID, 10) + "/messages",
		"/api/v1/telegram/official/channels/1002",
	} {
		if resp := env.send(t, who.token, http.MethodGet, path, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("GET %s under a deny = %d, want 403", path, resp.StatusCode)
		}
	}
	// The operator token is not an account and holds no per-channel grants; the
	// deny is a user's instruction and does not widen the operator's access.
	if resp := env.send(t, operatorToken, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(b.ID, 10), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("operator under an account's deny = %d, want 200", resp.StatusCode)
	}
}

// --- write rights never outrun the view ---

func TestAnUpdateOrDeleteGrantWithoutViewDoesNotRevealTheResource(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	ctx := context.Background()
	a := env.ownedChannelRow(t, acc, "official", 1001)
	cat, err := env.store.CreateCategory(ctx, "Alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: a.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
		{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
	}})
	env.grantCategories(t, who, channelstore.ResourcePermissions{ResourceID: cat.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
		{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
	}})

	if resp := env.send(t, who.token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("channel detail with update only = %d, want 403", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodPost, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10)+"/leave", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("leave with delete but no view = %d, want 403", resp.StatusCode)
	}
	if resp := env.send(t, who.token, http.MethodPut, categoriesPath+"/"+strconv.FormatInt(cat.ID, 10),
		`{"name":"renamed","description":""}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("category update without view = %d, want 403", resp.StatusCode)
	}
	if ids, _ := channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath, "")), "channels"); len(ids) != 0 {
		t.Fatalf("channel list with update only = %v, want nothing", ids)
	}
}

func TestAnExplicitViewDenyTakesAwayTheUpdateGrantToo(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test", channelstore.PermissionChannelsView)
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	a := env.ownedChannelRow(t, acc, "official", 1001)
	env.grantChannels(t, who, deny(a.ID))
	if resp := env.send(t, who.token, http.MethodPost, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10)+"/leave", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("leave under a view deny = %d, want 403", resp.StatusCode)
	}
}

func TestTheOperatorTokenIsTheOnlyExplicitBypass(t *testing.T) {
	env := newAccountEnv(t, registry{})
	plain := env.account(t, "test")
	acc := env.sessionAccount(t, plain, "official", channelstore.NetworkTelegram, readyWorker())
	a := env.ownedChannelRow(t, acc, "official", 1001)
	if resp := env.send(t, plain.token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("account with no permission = %d, want 403", resp.StatusCode)
	}
	if resp := env.send(t, operatorToken, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("operator token = %d, want 200", resp.StatusCode)
	}
}

// --- nothing reaches a restricted caller through a worker it may not see ---

func TestRefusedResourcesAreNeverFetchedUpstream(t *testing.T) {
	listed := dialogsOpensWith(1001, 1002)
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	a := env.ownedChannelRow(t, acc, "official", 1001)
	b := env.ownedChannelRow(t, acc, "official", 1002)
	env.grantChannels(t, who, allow(a.ID))

	// The channel the caller may see is served normally, so a refused one is
	// not simply refused by a broken upstream path.
	if resp := env.send(t, who.token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10)+"/messages", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("own channel messages = %d", resp.StatusCode)
	}

	for _, path := range []string{
		localChannelsPath + "/" + strconv.FormatInt(b.ID, 10) + "/messages",
		localChannelsPath + "/" + strconv.FormatInt(b.ID, 10) + "/avatar",
		"/api/v1/telegram/official/channels/1002",
		"/api/v1/telegram/official/channels/1002/messages",
		"/api/v1/telegram/official/channels/1002/avatar",
	} {
		if resp := env.send(t, who.token, http.MethodGet, path, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("GET %s = %d, want 403", path, resp.StatusCode)
		}
	}
	// The status codes alone do not prove it: the worker must not have been
	// asked for channel B's data at all.
	if listed.lastChan == 1002 || listed.lastMessageChannel == 1002 || listed.lastMediaChannel == 1002 {
		t.Fatalf("a refused channel reached the worker: last=%d message=%d media=%d",
			listed.lastChan, listed.lastMessageChannel, listed.lastMediaChannel)
	}
}

// --- the reported scenario, end to end through the API the panel calls ---

// The administrator's permission dialog reads the user's permissions from
// GET /local/users/{id}, saves them with PUT /local/users/{id}/permissions, and
// the account then logs in through POST /auth/login. This test performs exactly
// that, then asks the channel API as `test`.

func TestTheReportedScenarioThroughThePermissionAPI(t *testing.T) {
	env := newAccountEnv(t, registry{})
	admin := env.account(t, "admin", channelstore.PermissionUsersView, channelstore.PermissionUsersManage,
		channelstore.PermissionChannelsView, channelstore.PermissionChannelsCreate,
		channelstore.PermissionCategoriesView, channelstore.PermissionCategoriesCreate)
	acc := env.sessionAccount(t, admin, "official", channelstore.NetworkTelegram, readyWorker())
	a := env.ownedChannelRow(t, acc, "official", 1001) // Channel A
	b := env.ownedChannelRow(t, acc, "official", 1002) // Channel B

	hash, err := auth.HashPassword("password-test")
	if err != nil {
		t.Fatal(err)
	}
	testUser, err := env.store.CreateUser(context.Background(), "Test", "test", hash, true)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The dialog loads the user, with the permissions it currently has.
	loaded := bodyOf(env.send(t, admin.token, http.MethodGet, "/api/v1/local/users/"+strconv.FormatInt(testUser.ID, 10), ""))
	var loadedBody struct {
		Data struct {
			User struct {
				Permissions struct {
					Global []string `json:"global"`
				} `json:"permissions"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(loaded), &loadedBody); err != nil {
		t.Fatalf("load: %v (%s)", err, loaded)
	}
	if n := len(loadedBody.Data.User.Permissions.Global); n != 0 {
		t.Fatalf("a fresh account already holds %d global permissions", n)
	}

	// 2. The dialog saves: Channel A picked, Channel B not.
	put := fmt.Sprintf(`{"global_permissions":[],"channel_permissions":[{"channel_id":%d,"permissions":{"view":"allow"}}],"category_permissions":[]}`, a.ID)
	saved := env.send(t, admin.token, http.MethodPut, "/api/v1/local/users/"+strconv.FormatInt(testUser.ID, 10)+"/permissions", put)
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("save = %d: %s", saved.StatusCode, bodyOf(saved))
	}

	// 3. The database holds the one intended grant and no global permission.
	set, err := env.store.PermissionsOf(context.Background(), testUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Global) != 0 {
		t.Fatalf("global permissions stored = %v, want none", set.Global)
	}
	if len(set.Channels) != 1 || set.Channels[0].ResourceID != a.ID {
		t.Fatalf("channel assignments stored = %+v, want only channel A (%d)", set.Channels, a.ID)
	}

	// 4. test logs in through the real login route.
	login := env.send(t, "", http.MethodPost, loginPath, `{"username":"test","password":"password-test"}`)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", login.StatusCode)
	}
	token := decode[loginResponse](t, login).AccessToken

	// 5. The channel API, as test.
	ids, total := channelIDsOf(t, bodyOf(env.send(t, token, http.MethodGet, localChannelsPath, "")), "channels")
	wantIDs(t, "list", ids, a.ID)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if resp := env.send(t, token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("Channel A detail = %d, want 200", resp.StatusCode)
	}
	if resp := env.send(t, token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10)+"/messages", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("Channel A messages = %d, want 200", resp.StatusCode)
	}
	if resp := env.send(t, token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(b.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Channel B detail = %d, want 403", resp.StatusCode)
	}
	if resp := env.send(t, token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(b.ID, 10)+"/messages", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Channel B messages = %d, want 403", resp.StatusCode)
	}
	// Neither the A nor the B row is reachable through a search for its name.
	if ids, total := channelIDsOf(t, bodyOf(env.send(t, token, http.MethodGet, localChannelsPath+"?q=1002", "")), "channels"); len(ids) != 0 || total != 0 {
		t.Fatalf("search for B = %v (total %d), want nothing", ids, total)
	}
	// One row per page: the second page is empty, since B is not there to fill it.
	ids, total = channelIDsOf(t, bodyOf(env.send(t, token, http.MethodGet, localChannelsPath+"?page=2&per_page=1", "")), "channels")
	if len(ids) != 0 || total != 1 {
		t.Fatalf("page 2 (per_page 1) = %v (total %d), want nothing and total 1", ids, total)
	}

	// 6. Revoking Channel A, same session: nothing is visible any more.
	revoked := env.send(t, admin.token, http.MethodPut, "/api/v1/local/users/"+strconv.FormatInt(testUser.ID, 10)+"/permissions",
		`{"global_permissions":[],"channel_permissions":[],"category_permissions":[]}`)
	if revoked.StatusCode != http.StatusOK {
		t.Fatalf("revoke = %d: %s", revoked.StatusCode, bodyOf(revoked))
	}
	if ids, total := channelIDsOf(t, bodyOf(env.send(t, token, http.MethodGet, localChannelsPath, "")), "channels"); len(ids) != 0 || total != 0 {
		t.Fatalf("after revoke = %v (total %d), want nothing", ids, total)
	}
	if resp := env.send(t, token, http.MethodGet, localChannelsPath+"/"+strconv.FormatInt(a.ID, 10), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Channel A after revoke = %d, want 403", resp.StatusCode)
	}
	// And granting it again through the API works on the next request.
	if resp := env.send(t, admin.token, http.MethodPut, "/api/v1/local/users/"+strconv.FormatInt(testUser.ID, 10)+"/permissions", put); resp.StatusCode != http.StatusOK {
		t.Fatalf("re-grant = %d", resp.StatusCode)
	}
	if ids, _ := channelIDsOf(t, bodyOf(env.send(t, token, http.MethodGet, localChannelsPath, "")), "channels"); fmt.Sprint(ids) != fmt.Sprint([]int64{a.ID}) {
		t.Fatalf("after re-grant = %v, want [%d]", ids, a.ID)
	}
}

func TestACallerWithNoAssignmentSelectsNothing(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "test")
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	env.ownedChannelRow(t, acc, "official", 1001)
	env.ownedChannelRow(t, acc, "official", 1002)
	ids, total := channelIDsOf(t, bodyOf(env.send(t, who.token, http.MethodGet, localChannelsPath, "")), "channels")
	if len(ids) != 0 || total != 0 {
		t.Fatalf("no assignment = %v (total %d), want nothing", ids, total)
	}
	if resp := env.send(t, who.token, http.MethodGet, localChannelsPath+"?page=1&per_page=10", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestSavingChannelPermissionsKeepsTheStoredSiteGrants(t *testing.T) {
	env := newWPEnv(t)
	site := createSite(t, env, "https://one.example.com")
	who := env.account(t, "test", channelstore.PermissionWordPressView)
	acc := env.sessionAccount(t, who, "official", channelstore.NetworkTelegram, readyWorker())
	env.setWordPress(t, who, allow(site))
	ch := env.ownedChannelRow(t, acc, "official", 1001)

	// The administrators dialog's body: channels only, no wordpress key.
	body := fmt.Sprintf(`{"global_permissions":[],"channel_permissions":[{"channel_id":%d,"permissions":{"view":"allow"}}],"category_permissions":[]}`, ch.ID)
	if resp := env.send(t, defaultCaller, http.MethodPut, "/api/v1/local/users/"+strconv.FormatInt(who.user.ID, 10)+"/permissions", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("save = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	set, err := env.store.PermissionsOf(context.Background(), who.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Wordsites) != 1 || set.Wordsites[0].ResourceID != site {
		t.Fatalf("site grants after a channel save = %+v, want still site %d", set.Wordsites, site)
	}
	if len(set.Channels) != 1 || set.Channels[0].ResourceID != ch.ID {
		t.Fatalf("channel grants = %+v", set.Channels)
	}

	// A body that does speak about sites replaces them.
	cleared := `{"global_permissions":[],"channel_permissions":[],"category_permissions":[],"wordpress_permissions":[]}`
	if resp := env.send(t, defaultCaller, http.MethodPut, "/api/v1/local/users/"+strconv.FormatInt(who.user.ID, 10)+"/permissions", cleared); resp.StatusCode != http.StatusOK {
		t.Fatalf("clear = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	set, err = env.store.PermissionsOf(context.Background(), who.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Wordsites) != 0 {
		t.Fatalf("site grants after an explicit clear = %+v, want none", set.Wordsites)
	}
}
