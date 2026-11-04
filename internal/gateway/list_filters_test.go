package gateway_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

// List filtering: the channels and WordPress collection routes accept a search
// text and a category filter, applied before pagination and inside the
// caller's own visibility.
//
// The filter uses OR semantics when more than one category is selected: a
// channel or site filed under any of them is listed. An empty selection is no
// category filter at all.

func TestLocalChannelsListFiltersBySelectedCategories(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	ctx := context.Background()

	newsCat, err := env.store.CreateCategory(ctx, "News", "")
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	sportsCat, err := env.store.CreateCategory(ctx, "Sports", "")
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	tech := env.localRow(t, "official", 1, "Tech Daily", "tech")
	sports := env.localRow(t, "official", 2, "Sports Hour", "sports")
	untagged := env.localRow(t, "official", 3, "Untagged", "plain")

	for _, filed := range []struct {
		ch  channelstore.Channel
		cat channelstore.Category
	}{
		{tech, newsCat},
		{sports, sportsCat},
	} {
		if err := env.store.ReplaceCategories(ctx, filed.ch.ClientKey, filed.ch.ChannelID,
			[]int64{filed.cat.ID}); err != nil {
			t.Fatalf("ReplaceCategories: %v", err)
		}
	}

	ids := func(path string) []int64 {
		t.Helper()
		page := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet, path, ""))
		out := make([]int64, 0, len(page.Data.Channels))
		for _, ch := range page.Data.Channels {
			out = append(out, ch.ID)
		}
		return out
	}

	if one := ids(fmt.Sprintf("%s?category_ids=%d", localChannelsPath, newsCat.ID)); len(one) != 1 || one[0] != tech.ID {
		t.Fatalf("news filter = %v, want only the tech row", one)
	}

	// OR semantics across the selected categories; the untagged row is never
	// listed under a category filter.
	two := ids(fmt.Sprintf("%s?category_ids=%d,%d", localChannelsPath, newsCat.ID, sportsCat.ID))
	if len(two) != 2 {
		t.Fatalf("both categories = %v, want the 2 tagged rows", two)
	}
	for _, id := range two {
		if id == untagged.ID {
			t.Fatal("an untagged row appeared under a category filter")
		}
	}

	// The search text and a category filter combine: both must hold.
	if combined := ids(fmt.Sprintf("%s?q=tech&category_ids=%d", localChannelsPath, newsCat.ID)); len(combined) != 1 || combined[0] != tech.ID {
		t.Fatalf("q=tech plus news = %v, want only the tech row", combined)
	}
	if miss := ids(fmt.Sprintf("%s?q=tech&category_ids=%d", localChannelsPath, sportsCat.ID)); len(miss) != 0 {
		t.Fatalf("q=tech plus sports = %v, want nothing", miss)
	}

	// No selection is no filter.
	if all := ids(localChannelsPath); len(all) != 3 {
		t.Fatalf("unfiltered = %v, want all 3 rows", all)
	}
}

func TestLocalChannelsListRefusesAMalformedCategoryFilter(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	for _, q := range []string{
		"?category_ids=abc",
		"?category_ids=0",
		"?category_ids=-1",
		"?category_ids=1,,2",
	} {
		resp := env.send(t, defaultCaller, http.MethodGet, localChannelsPath+q, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", q, resp.StatusCode)
		}
	}
}

type siteListPage struct {
	Data struct {
		Sites []siteView `json:"sites"`
	} `json:"data"`
}

// siteTitles registers one site at each url and answers its listing titles.
func siteTitles(t *testing.T, env *wpEnv, entries map[string]string) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	out := map[string]int64{}
	for url, title := range entries {
		id := createSite(t, env, url)
		rec := env.storedSite(t, id)
		rec.Title = title
		if _, err := env.store.UpdateWordPressSite(ctx, rec); err != nil {
			t.Fatalf("UpdateWordPressSite(%s): %v", url, err)
		}
		out[url] = id
	}
	return out
}

func TestWordPressListSearchesAndFiltersByCategories(t *testing.T) {
	env := newWPEnv(t)
	ctx := context.Background()

	newsCat, err := env.store.CreateCategory(ctx, "News", "")
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	techCat, err := env.store.CreateCategory(ctx, "Tech", "")
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	ids := siteTitles(t, env, map[string]string{
		"https://news.example.com":   "Newsroom",
		"https://portal.example.com": "Portal",
	})
	for siteID, cat := range map[int64]int64{
		ids["https://news.example.com"]:   newsCat.ID,
		ids["https://portal.example.com"]: techCat.ID,
	} {
		if err := env.store.ReplaceSiteCategories(ctx, siteID, []int64{cat}); err != nil {
			t.Fatalf("ReplaceSiteCategories: %v", err)
		}
	}

	listed := func(path string) []string {
		t.Helper()
		page := decode[siteListPage](t, env.send(t, defaultCaller, http.MethodGet, path, ""))
		out := make([]string, 0, len(page.Data.Sites))
		for _, site := range page.Data.Sites {
			out = append(out, site.Title)
		}
		return out
	}

	if got := listed("/api/v1/local/wordpress?q=Newsroom"); len(got) != 1 || got[0] != "Newsroom" {
		t.Fatalf("search by title = %v", got)
	}
	// The domain/URL is matched too, not only the title.
	if got := listed("/api/v1/local/wordpress?q=portal.example"); len(got) != 1 || got[0] != "Portal" {
		t.Fatalf("search by domain = %v", got)
	}
	if got := listed(fmt.Sprintf("/api/v1/local/wordpress?category_ids=%d", newsCat.ID)); len(got) != 1 || got[0] != "Newsroom" {
		t.Fatalf("category filter = %v", got)
	}
	// OR semantics across the categories.
	if got := listed(fmt.Sprintf("/api/v1/local/wordpress?category_ids=%d,%d", newsCat.ID, techCat.ID)); len(got) != 2 {
		t.Fatalf("both categories = %v, want 2 sites", got)
	}
	// Search and category filter work together.
	if got := listed(fmt.Sprintf("/api/v1/local/wordpress?q=portal&category_ids=%d", techCat.ID)); len(got) != 1 || got[0] != "Portal" {
		t.Fatalf("combined = %v", got)
	}
	if got := listed(fmt.Sprintf("/api/v1/local/wordpress?q=portal&category_ids=%d", newsCat.ID)); len(got) != 0 {
		t.Fatalf("combined on the wrong category = %v, want none", got)
	}
	// Unfiltered listing is unchanged.
	if got := listed("/api/v1/local/wordpress"); len(got) != 2 {
		t.Fatalf("unfiltered = %v, want 2 sites", got)
	}
}

func TestWordPressListRefusesAMalformedFilter(t *testing.T) {
	env := newWPEnv(t)
	for _, q := range []string{"?category_ids=abc", "?category_ids=0", "?category_ids=-2"} {
		resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress"+q, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", q, resp.StatusCode)
		}
	}
}
