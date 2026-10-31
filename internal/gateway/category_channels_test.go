package gateway_test

import (
	"context"
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

	// A caller that may not see the channel may not file it.
	limited := env.account(t, "limited")
	if resp := env.send(t, limited.token, http.MethodPut, path, body); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("without the channel = %d, want 403", resp.StatusCode)
	}
	// Nor file it under a category it may not see.
	other := env.account(t, "other", channelstore.PermissionChannelsView)
	if resp := env.send(t, other.token, http.MethodPut, path, body); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("without the category = %d, want 403", resp.StatusCode)
	}

	if resp := env.send(t, defaultCaller, http.MethodPut, path, `{"category_ids":[999]}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown category = %d, want 404", resp.StatusCode)
	}
	if resp := env.send(t, defaultCaller, http.MethodPut, localChannelsPath+"/9999/categories", body); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown channel = %d, want 404", resp.StatusCode)
	}
	if resp := env.send(t, defaultCaller, http.MethodPut, path, `{"category_ids":[],"extra":1}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", resp.StatusCode)
	}
	if resp := env.send(t, defaultCaller, http.MethodPut, path,
		`{"category_ids":[`+strconv.FormatInt(cat.ID, 10)+`,`+strconv.FormatInt(cat.ID, 10)+`]}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("repeated id = %d, want 400", resp.StatusCode)
	}
	// And the category's own member route no longer sets anything.
	if resp := env.send(t, defaultCaller, http.MethodPost, categoryChannelsPath(cat), body); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("setting from the category = %d, want 405", resp.StatusCode)
	}
}
