package gateway_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/wordpress"
)

// envStoreFor reads the account store the suite wired as the WordPress store.
func envStoreFor(c *gateway.Config) gateway.WordPressStore {
	store, _ := c.Channels.(gateway.WordPressStore)
	return store
}

// grantWordPress replaces the site assignments of one account.
func (e *accountEnv) grantWordPress(t *testing.T, who identity, sets ...channelstore.ResourcePermissions) {
	t.Helper()
	ctx := context.Background()
	current, err := e.store.PermissionsOf(ctx, who.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Wordsites = sets
	if err := e.store.ReplacePermissions(ctx, who.user.ID, current); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
}

// WordPress publishing takes a prepared post to a registered site. The site is
// named by its local record, never by a URL from the browser, and the tags are
// resolved to real WordPress terms rather than silently dropped.

// wpSiteHandler is a scripted WordPress REST API for the gateway's tests.
type wpSiteHandler struct {
	postsCreated   int
	uploadsCreated int
	tagsCreated    int
	gotPost        wordpress.PostInput
	gotTagNames    []string
	gotUploadName  string
	postErr        error
	nextTagID      int
}

// wpFakeClient is the gateway's WordPress client over the scripted site.
type wpFakeClient struct {
	*wpSiteHandler
	existing map[string]wordpress.TagRef
}

func (c *wpFakeClient) Probe(context.Context, string, string, string) (wordpress.SiteInfo, error) {
	return wordpress.SiteInfo{Title: "Site"}, nil
}

func (c *wpFakeClient) CreatePost(_ context.Context, _, _, _ string, in wordpress.PostInput) (wordpress.PostResult, error) {
	c.postsCreated++
	c.gotPost = in
	if c.postErr != nil {
		return wordpress.PostResult{}, c.postErr
	}
	return wordpress.PostResult{
		ID: 42, Link: "https://site.example.com/hello/", Status: "publish",
		Title: in.Title, TagIDs: append([]int(nil), in.TagIDs...),
	}, nil
}

func (c *wpFakeClient) UploadMedia(_ context.Context, _, _, _, name, _ string, _ []byte) (wordpress.MediaRef, error) {
	c.uploadsCreated++
	c.gotUploadName = name
	return wordpress.MediaRef{ID: 5, SourceURL: "https://site.example.com/a.jpg"}, nil
}

func (c *wpFakeClient) FindTag(_ context.Context, _, _, _, name string) (wordpress.TagRef, bool, error) {
	if t, ok := c.existing[name]; ok {
		return t, true, nil
	}
	return wordpress.TagRef{}, false, nil
}

func (c *wpFakeClient) CreateTag(_ context.Context, _, _, _, name string) (wordpress.TagRef, error) {
	c.tagsCreated++
	c.gotTagNames = append(c.gotTagNames, name)
	c.nextTagID++
	return wordpress.TagRef{ID: 100 + c.nextTagID, Name: name}, nil
}

// wpBox seals and opens the suite's WordPress credential.
type wpBox struct{ key []byte }

func newWPBox() wpBox { return wpBox{key: []byte("0123456789abcdef0123456789abcdef")} }

func (b wpBox) Seal(plaintext string) (secrets.Box, error) {
	enc, err := secrets.NewEncrypter(b.key)
	if err != nil {
		return secrets.Box{}, err
	}
	return enc.Seal(plaintext)
}

func (b wpBox) Open(box secrets.Box) (string, error) {
	enc, err := secrets.NewEncrypter(b.key)
	if err != nil {
		return "", err
	}
	return enc.Open(box)
}

// wpRequest is one WordPress publishing request.
func wpRequest(localID int64, messageID int, title string) string {
	return fmt.Sprintf(`{
		"source": {"local_channel_id": %d, "message_id": %d},
		"post": {"title": %q, "content": "<p>Body</p>", "status": "publish", "tags": ["technology", "news"]},
		"media": {"mode": "preserve_source"},
		"options": {"comment_status": "open"}
	}`, localID, messageID, title)
}

// wpPublishEnv wires an account suite with a registered site, a source channel
// and a scripted WordPress client.
func wpPublishEnv(t *testing.T) (*accountEnv, *wpFakeClient, channelstore.Channel) {
	t.Helper()
	client := &wpFakeClient{
		wpSiteHandler: &wpSiteHandler{},
		existing:      map[string]wordpress.TagRef{"technology": {ID: 7, Name: "Technology"}},
	}

	worker := readyWorker()
	worker.message = rpc.MessagePage{
		Channel:  rpc.Channel{ID: 555, Title: "Source"},
		Messages: []rpc.Message{{ID: 9, Text: "hello"}},
		Page:     rpc.PageInfo{Page: 1, PerPage: 1, Total: 1},
	}
	worker.media = rpc.MediaFile{MediaID: "555:9", MimeType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}}

	env := newAccountEnv(t, registry{"official": worker}, func(c *gateway.Config) {
		c.WordPress = envStoreFor(c)
		c.WordPressClient = client
		c.Secrets = newWPBox()
	})
	env.signedIn = true
	row := env.channelRow(t, "official", 555)

	site := channelstore.WordPressSite{
		URL:        "https://site.example.com",
		Title:      "Site",
		Username:   "editor",
		Credential: mustBox(t),
	}
	if _, err := env.store.CreateWordPressSite(context.Background(), site); err != nil {
		t.Fatalf("register site: %v", err)
	}
	return env, client, row
}

func mustBox(t *testing.T) secrets.Box {
	t.Helper()
	box, err := newWPBox().Seal("app-password-value")
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestWordPressPublishNeedsACredential(t *testing.T) {
	env, _, row := wpPublishEnv(t)
	resp := env.sendWith(t, "", http.MethodPost, "/api/v1/local/wordpress/1/posts", nil, wpRequest(row.ID, 9, "Title"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestWordPressPublishNeedsThePublishingPermission(t *testing.T) {
	env, client, row := wpPublishEnv(t)
	plain := env.account(t, "plain", channelstore.PermissionWordPressView)
	resp := env.send(t, plain.token, http.MethodPost, "/api/v1/local/wordpress/1/posts", wpRequest(row.ID, 9, "Title"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if client.postsCreated != 0 {
		t.Fatal("a caller without content.publish must not reach the site")
	}
}

func TestWordPressPublishCreatesThePostWithRealTerms(t *testing.T) {
	env, client, row := wpPublishEnv(t)
	grantPublish(t, env, env.defaults)

	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/posts",
		wpRequest(row.ID, 9, "Hello"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.postsCreated != 1 {
		t.Fatalf("posts created = %d", client.postsCreated)
	}
	if client.gotPost.Title != "Hello" || client.gotPost.Status != "publish" {
		t.Fatalf("post = %+v", client.gotPost)
	}
	// "technology" already existed as a term; "news" had to be created.
	if len(client.gotPost.TagIDs) != 2 || client.gotPost.TagIDs[0] != 7 {
		t.Fatalf("tag ids = %v", client.gotPost.TagIDs)
	}
	if client.tagsCreated != 1 {
		t.Fatalf("tags created = %d, want only the missing one", client.tagsCreated)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `"id":42`) {
		t.Fatalf("body = %s", body)
	}
}

func TestWordPressPublishWithoutMediaSendsTextOnly(t *testing.T) {
	env, client, row := wpPublishEnv(t)
	grantPublish(t, env, env.defaults)
	req := strings.Replace(wpRequest(row.ID, 9, "T"), `"preserve_source"`, `"omit"`, 1)

	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/posts", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.uploadsCreated != 0 {
		t.Fatal("media.mode=omit must not upload anything")
	}
}

func TestWordPressPublishRequiresAValidSiteRecord(t *testing.T) {
	env, client, row := wpPublishEnv(t)
	grantPublish(t, env, env.defaults)
	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/99/posts",
		wpRequest(row.ID, 9, "T"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if client.postsCreated != 0 {
		t.Fatal("an unknown site must not be published to")
	}
}

func TestWordPressPublishRefusesASiteTheCallerCannotSee(t *testing.T) {
	env, client, row := wpPublishEnv(t)
	grantPublish(t, env, env.defaults)
	site, ok, err := env.store.GetWordPressSite(context.Background(), 1)
	if err != nil || !ok {
		t.Fatalf("site: ok=%v err=%v", ok, err)
	}
	env.grantWordPress(t, env.defaults, channelstore.ResourcePermissions{
		ResourceID:  site.ID,
		Permissions: []channelstore.ResourcePermission{{Action: channelstore.ActionView, Effect: channelstore.EffectDeny}},
	})
	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/posts",
		wpRequest(row.ID, 9, "T"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if client.postsCreated != 0 {
		t.Fatal("a site the caller cannot see must not be published to")
	}
}

func TestWordPressPublishDoesNotClaimSuccessWhenTheSiteRefuses(t *testing.T) {
	env, client, row := wpPublishEnv(t)
	grantPublish(t, env, env.defaults)
	client.postErr = wordpress.ErrForbidden

	resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress/1/posts",
		wpRequest(row.ID, 9, "T"))
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a refused post must not be reported as published")
	}
	if code := errorCode(t, resp); code != "publish_wordpress_forbidden" {
		t.Fatalf("code = %q", code)
	}
}
