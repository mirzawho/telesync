package gateway_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/wordpress"
)

// wpTermsEnv is the publishing suite with a site that has a taxonomy.
func wpTermsEnv(t *testing.T) (*accountEnv, *wpFakeClient, channelstore.Channel) {
	env, client, row, _ := wpTermsEnvWorker(t)
	return env, client, row
}

func wpTermsEnvWorker(t *testing.T) (*accountEnv, *wpFakeClient, channelstore.Channel, *fakeWorker) {
	t.Helper()
	env, client, row, worker := wpPublishEnvWorker(t)
	// The source message carries an attachment, so the featured-image path has
	// something real to upload.
	worker.message.Messages = []rpc.Message{{
		ID: 9, Text: "hello",
		Media: &rpc.Media{ID: "9:2", Type: "photo", Label: "تصویر", MimeType: "image/jpeg", Size: 3},
	}}
	client.terms = map[string][]wordpress.TermRef{
		"categories": {
			{ID: 4, Name: "دستهٔ یک", Slug: "one", Count: 3},
			{ID: 5, Name: "دستهٔ دو", Slug: "two"},
		},
		"tags": {
			{ID: 8, Name: "برچسب یک", Slug: "tag-one"},
		},
	}
	return env, client, row, worker
}

// The picker needs the *destination site's* own terms, because a category or a
// tag is a row in one site's taxonomy and names nothing on another.
func TestWordPressCategoriesAreTheSitesOwnTerms(t *testing.T) {
	env, client, _ := wpTermsEnv(t)

	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/1/categories", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.gotTermsOf != "categories" {
		t.Fatalf("taxonomy = %q, want categories", client.gotTermsOf)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `"id":4`) || !strings.Contains(body, `"id":5`) {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "app-password-value") {
		t.Fatalf("the site credential reached the browser: %s", body)
	}
}

func TestWordPressTagsAreTheSitesOwnTerms(t *testing.T) {
	env, client, _ := wpTermsEnv(t)

	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/1/tags", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.gotTermsOf != "tags" {
		t.Fatalf("taxonomy = %q, want tags", client.gotTermsOf)
	}
	if body := readBody(t, resp); !strings.Contains(body, `"id":8`) {
		t.Fatalf("body = %s", body)
	}
}

// Searching and paging are the site's own, so a large taxonomy is never pulled
// down in one go and a search is not done in the browser against a full list.
func TestWordPressTermsAreSearchedAndPagedOnTheSite(t *testing.T) {
	env, client, _ := wpTermsEnv(t)

	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/1/categories?search=%D8%AF%D8%B3%D8%AA%D9%87&page=2&per_page=20", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.gotTermsSearch != "دسته" {
		t.Fatalf("search = %q", client.gotTermsSearch)
	}
	if client.gotTermsPage != 2 || client.gotTermsPerPage != 20 {
		t.Fatalf("page = %d per_page = %d", client.gotTermsPage, client.gotTermsPerPage)
	}
}

func TestWordPressTermsNeedACredential(t *testing.T) {
	env, _, _ := wpTermsEnv(t)
	resp := env.send(t, "", http.MethodGet, "/api/v1/local/wordpress/1/categories", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// A site the caller may not see is not told apart from one that does not exist:
// either way the answer is "no such site", never its terms.
func TestWordPressTermsRespectSiteVisibility(t *testing.T) {
	env, client, _ := wpTermsEnv(t)
	grantPublish(t, env, env.defaults)

	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/99/categories", "")
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 404 or 403", resp.StatusCode)
	}
	if client.gotTermsCalls != 0 {
		t.Fatal("a site that does not exist must not be asked for its terms")
	}
}

// A read the site refused is reported as a read that failed. It is never
// answered as an empty list, which would look like a site with no categories.
func TestWordPressTermsFailureIsNotAnEmptyList(t *testing.T) {
	env, client, _ := wpTermsEnv(t)
	client.termsErr = wordpress.ErrUnreachable

	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/1/categories", "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if body := readBody(t, resp); strings.Contains(body, `"terms":[]`) {
		t.Fatalf("a failed read must not look like an empty taxonomy: %s", body)
	}
}

func TestWordPressTermsOnlyAnswerGET(t *testing.T) {
	env, _, _ := wpTermsEnv(t)
	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/categories", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", allow)
	}
}

func TestWordPressUnknownNestedRouteIsNotFound(t *testing.T) {
	env, _, _ := wpTermsEnv(t)
	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/1/pages", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Publishing
// ---------------------------------------------------------------------------

// The form's category and tag ids are WordPress term ids of the destination
// site, and they are sent on unchanged. A Telegram hashtag is never submitted
// as one of these.
func TestWordPressPublishSendsTheChosenTermIDsAndSlug(t *testing.T) {
	env, client, row := wpTermsEnv(t)
	grantPublish(t, env, env.defaults)
	body := `{
		"source": {"local_channel_id": ` + strconv.FormatInt(row.ID, 10) + `, "message_id": 9},
		"post": {
			"title": "عنوان",
			"slug": "my-slug",
			"content": "<p>Body</p>",
			"status": "publish",
			"categories": [4, 5],
			"tag_ids": [8]
		},
		"media": {"mode": "omit"},
		"options": {"comment_status": "open"}
	}`

	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/posts", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.gotPost.Slug != "my-slug" {
		t.Fatalf("slug = %q", client.gotPost.Slug)
	}
	if len(client.gotPost.CategoryIDs) != 2 || client.gotPost.CategoryIDs[0] != 4 {
		t.Fatalf("category ids = %v", client.gotPost.CategoryIDs)
	}
	if len(client.gotPost.TagIDs) != 1 || client.gotPost.TagIDs[0] != 8 {
		t.Fatalf("tag ids = %v", client.gotPost.TagIDs)
	}
	// Ids were named, so no tag name is resolved or created on the site.
	if client.tagsCreated != 0 {
		t.Fatalf("tags created = %d, want 0 when tag ids are named", client.tagsCreated)
	}
}

// An empty slug is not sent at all: WordPress generates one from the title,
// which is the only sensible answer for a Persian title.
func TestWordPressPublishLeavesAnEmptySlugToTheSite(t *testing.T) {
	env, client, row := wpTermsEnv(t)
	grantPublish(t, env, env.defaults)
	body := `{
		"source": {"local_channel_id": ` + strconv.FormatInt(row.ID, 10) + `, "message_id": 9},
		"post": {"title": "سلام", "slug": "", "content": "x", "status": "publish"},
		"media": {"mode": "omit"},
		"options": {"comment_status": "open"}
	}`

	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/posts", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.gotPost.Slug != "" {
		t.Fatalf("slug = %q, want empty", client.gotPost.Slug)
	}
}

// The featured image is the source attachment the user picked, and its position
// is read back from its id rather than assumed to be the first.
func TestWordPressPublishUploadsTheNamedAttachment(t *testing.T) {
	env, client, row, worker := wpTermsEnvWorker(t)
	grantPublish(t, env, env.defaults)
	body := `{
		"source": {"local_channel_id": ` + strconv.FormatInt(row.ID, 10) + `, "message_id": 9},
		"post": {"title": "T", "content": "x", "status": "publish"},
		"media": {"mode": "preserve_source", "attachment_id": "9:2"},
		"options": {"comment_status": "open"}
	}`

	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/posts", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.uploadsCreated != 1 {
		t.Fatalf("uploads = %d, want 1", client.uploadsCreated)
	}
	if client.gotPost.FeaturedMedia != 5 {
		t.Fatalf("featured media = %d, want the uploaded attachment id", client.gotPost.FeaturedMedia)
	}
	// photoIndex is a photo *size*, not an attachment selector: the source
	// attachment is read at 0, the largest, exactly as the media route reads it.
	if worker.lastMediaIndex != 0 {
		t.Fatalf("photo size = %d, want 0 (the largest)", worker.lastMediaIndex)
	}
}

// The featured image must be an attachment of the source message. A media id
// from another message is refused rather than answered with this one.
func TestWordPressPublishRefusesAnAttachmentOfAnotherMessage(t *testing.T) {
	env, client, row, _ := wpTermsEnvWorker(t)
	grantPublish(t, env, env.defaults)
	body := `{
		"source": {"local_channel_id": ` + strconv.FormatInt(row.ID, 10) + `, "message_id": 9},
		"post": {"title": "T", "content": "x", "status": "publish"},
		"media": {"mode": "preserve_source", "attachment_id": "555:77"},
		"options": {"comment_status": "open"}
	}`

	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/posts", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if client.uploadsCreated != 0 {
		t.Fatal("no attachment may be uploaded when the named one is not this message's")
	}
}
