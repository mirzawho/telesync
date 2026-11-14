package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

// A channel is filed under categories through its own route, and a category
// lists the channels it holds: each side of the same assignment is read where
// the record it names is known to exist.

func filedEnv(t *testing.T) (*accountEnv, channelstore.Channel, channelstore.Category) {
	t.Helper()
	env := newAccountEnv(t, registry{"official": readyWorker()})
	env.signedIn = true
	ch := env.channelRow(t, "official", 700)
	cat, err := env.store.CreateCategory(context.Background(), "News", "")
	if err != nil {
		t.Fatal(err)
	}
	return env, ch, cat
}

func categoryChannelsPath(cat channelstore.Category) string {
	return categoriesPath + "/" + strconv.FormatInt(cat.ID, 10) + "/channels"
}

func channelCategoriesPath(ch channelstore.Channel) string {
	return localChannelsPath + "/" + strconv.FormatInt(ch.ID, 10) + "/categories"
}

func TestAChannelIsFiledAndListedFromBothSides(t *testing.T) {
	env, ch, cat := filedEnv(t)
	path := channelCategoriesPath(ch)
	body := `{"category_ids":[` + strconv.FormatInt(cat.ID, 10) + `]}`

	if resp := env.send(t, defaultCaller, http.MethodGet, path, ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(bodyOf(resp), `"categories":[]`) {
		t.Fatalf("empty channel listing = %d %s", resp.StatusCode, bodyOf(resp))
	}
	if resp := env.send(t, defaultCaller, http.MethodPut, path, body); resp.StatusCode != http.StatusOK ||
		!strings.Contains(bodyOf(resp), `"name":"News"`) {
		t.Fatalf("file = %d %s", resp.StatusCode, bodyOf(resp))
	}

	// The category now lists the channel, and the channel its category.
	if resp := env.send(t, defaultCaller, http.MethodGet, categoryChannelsPath(cat), ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(bodyOf(resp), `"title":"`+ch.Title+`"`) {
		t.Fatalf("category listing = %d %s", resp.StatusCode, bodyOf(resp))
	}

	// Filing the same set twice keeps one filing.
	if resp := env.send(t, defaultCaller, http.MethodPut, path, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("second file = %d", resp.StatusCode)
	}
	resp := env.send(t, defaultCaller, http.MethodGet, path, "")
	if got := strings.Count(bodyOf(resp), `"name":"News"`); got != 1 {
		t.Fatalf("category appears %d times: %s", got, bodyOf(resp))
	}

	// An empty set unfiles it from everywhere.
	if resp := env.send(t, defaultCaller, http.MethodPut, path, `{"category_ids":[]}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("clear = %d", resp.StatusCode)
	}
	if resp := env.send(t, defaultCaller, http.MethodGet, categoryChannelsPath(cat), ""); !strings.Contains(bodyOf(resp), `"channels":[]`) {
		t.Fatalf("category after clearing = %s", bodyOf(resp))
	}
}

func TestFilingNeedsTheChannelAndTheCategory(t *testing.T) {
	env, ch, cat := filedEnv(t)
	path := channelCategoriesPath(ch)
	body := `{"category_ids":[` + strconv.FormatInt(cat.ID, 10) + `]}`

	// The registered account owner reaches application authorization; an
	// unrelated owner would be rejected earlier by row ownership.
	owner := env.defaults
	if err := env.store.ReplacePermissions(t.Context(), owner.user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionCategoriesView},
	}); err != nil {
		t.Fatal(err)
	}
	if resp := env.send(t, owner.token, http.MethodPut, path, body); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("without the channel = %d, want 403", resp.StatusCode)
	}
	// Nor file it under a category it may not see.
	if err := env.store.ReplacePermissions(t.Context(), owner.user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionChannelsView},
	}); err != nil {
		t.Fatal(err)
	}
	if resp := env.send(t, owner.token, http.MethodPut, path, body); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("without the category = %d, want 403", resp.StatusCode)
	}

	if err := env.store.ReplacePermissions(t.Context(), owner.user.ID, channelstore.PermissionSet{
		Global: channelstore.GlobalPermissions(),
	}); err != nil {
		t.Fatal(err)
	}
	if resp := env.send(t, owner.token, http.MethodPut, path, `{"category_ids":[999]}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown category = %d, want 404", resp.StatusCode)
	}
	if resp := env.send(t, owner.token, http.MethodPut, localChannelsPath+"/9999/categories", body); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown channel = %d, want 404", resp.StatusCode)
	}
	if resp := env.send(t, owner.token, http.MethodPut, path, `{"category_ids":[],"extra":1}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", resp.StatusCode)
	}
	if resp := env.send(t, owner.token, http.MethodPut, path,
		`{"category_ids":[`+strconv.FormatInt(cat.ID, 10)+`,`+strconv.FormatInt(cat.ID, 10)+`]}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("repeated id = %d, want 400", resp.StatusCode)
	}
	// And the category's own member route no longer sets anything.
	if resp := env.send(t, owner.token, http.MethodPost, categoryChannelsPath(cat), body); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("setting from the category = %d, want 405", resp.StatusCode)
	}
}

func TestCategoryChannelsAreScopedToTheAuthenticatedOwner(t *testing.T) {
	env := newAccountEnv(t, registry{"official": readyWorker()})
	viewGlobals := []channelstore.GlobalPermission{
		channelstore.PermissionCategoriesView,
		channelstore.PermissionChannelsView,
	}
	ownerA := env.account(t, "category-owner-a", viewGlobals...)
	ownerB := env.account(t, "category-owner-b", viewGlobals...)
	accountA := env.sessionAccount(t, ownerA, "official", channelstore.NetworkTelegram, readyWorker())
	accountB := env.sessionAccount(t, ownerB, "official", channelstore.NetworkTelegram, readyWorker())

	const telegramChannelID = 700
	channelA := env.ownedChannelRow(t, accountA, "official", telegramChannelID)
	channelB := env.ownedChannelRow(t, accountB, "official", telegramChannelID)
	category, err := env.store.CreateCategory(t.Context(), "Shared label", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, accountID := range []string{accountA, accountB} {
		if err := env.store.ReplaceCategories(t.Context(), accountID, telegramChannelID, []int64{category.ID}); err != nil {
			t.Fatal(err)
		}
	}

	resp := env.send(t, ownerA.token, http.MethodGet, categoryChannelsPath(category), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner A status = %d, body=%s", resp.StatusCode, bodyOf(resp))
	}
	var ownerAChannels struct {
		Data struct {
			Channels []struct {
				ID        int64  `json:"id"`
				AccountID string `json:"account_id"`
			} `json:"channels"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ownerAChannels); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(ownerAChannels.Data.Channels) != 1 ||
		ownerAChannels.Data.Channels[0].ID != channelA.ID ||
		ownerAChannels.Data.Channels[0].AccountID != accountA {
		t.Fatalf("owner A category channels = %+v, want only local channel %d/account %s (not B's %d)",
			ownerAChannels.Data.Channels, channelA.ID, accountA, channelB.ID)
	}

	// The operator token has broad category/channel permissions but no
	// authenticated Telesync owner, so it cannot enumerate either user's
	// private Telegram registrations through the shared category.
	resp = env.send(t, operatorToken, http.MethodGet, categoryChannelsPath(category), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("operator status = %d, body=%s", resp.StatusCode, bodyOf(resp))
	}
	defer resp.Body.Close()
	var operatorChannels struct {
		Data struct {
			Channels []json.RawMessage `json:"channels"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&operatorChannels); err != nil {
		t.Fatal(err)
	}
	if len(operatorChannels.Data.Channels) != 0 {
		t.Fatalf("operator category channels = %v, want no owner-scoped registrations",
			operatorChannels.Data.Channels)
	}
}
