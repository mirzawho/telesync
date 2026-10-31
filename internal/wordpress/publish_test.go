package wordpress_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/wordpress"
)

// A WordPress post is not a Telegram message: it has a title, a status and
// native tags that are terms with ids. These tests pin the client against the
// documented REST shapes so a publish cannot claim a post the site never made.

func TestCreatePostSendsThePreparedPostAndReturnsWhatTheSiteConfirmed(t *testing.T) {
	var gotBody map[string]any
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/wp-json/wp/v2/posts" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id": 42, "link": "https://site.example.com/hello/", "status": "publish",
			"title": {"rendered": "Hello"}, "tags": [7, 8]}`)
	})
	defer client.CloseIdleConnections()

	out, err := client.CreatePost(context.Background(), siteURL, "editor", "app-pass", wordpress.PostInput{
		Title:   "Hello",
		Content: "<p>Body</p>",
		Status:  "publish",
		TagIDs:  []int{7, 8},
	})
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if gotBody["title"] != "Hello" || gotBody["content"] != "<p>Body</p>" || gotBody["status"] != "publish" {
		t.Fatalf("body = %v", gotBody)
	}
	tags, _ := gotBody["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("tags = %v", gotBody["tags"])
	}
	if out.ID != 42 || out.Link != "https://site.example.com/hello/" || out.Status != "publish" {
		t.Fatalf("result = %+v", out)
	}
	if len(out.TagIDs) != 2 || out.TagIDs[0] != 7 {
		t.Fatalf("tag ids = %v", out.TagIDs)
	}
}

func TestCreatePostReportsBadCredentials(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="WordPress"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	defer client.CloseIdleConnections()
	_, err := client.CreatePost(context.Background(), siteURL, "editor", "wrong", wordpress.PostInput{Title: "x", Content: "y", Status: "publish"})
	if !errors.Is(err, wordpress.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestCreatePostReportsAMissingCapability(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	defer client.CloseIdleConnections()
	_, err := client.CreatePost(context.Background(), siteURL, "editor", "app-pass", wordpress.PostInput{Title: "x", Content: "y", Status: "publish"})
	if !errors.Is(err, wordpress.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestUploadMediaPostsTheBytesAndReturnsTheAttachment(t *testing.T) {
	var gotContentType, gotDisposition string
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/wp-json/wp/v2/media" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotContentType = r.Header.Get("Content-Type")
		gotDisposition = r.Header.Get("Content-Disposition")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id": 5, "source_url": "https://site.example.com/wp-content/uploads/a.jpg",
			"media_type": "image", "mime_type": "image/jpeg"}`)
	})
	defer client.CloseIdleConnections()

	out, err := client.UploadMedia(context.Background(), siteURL, "editor", "app-pass",
		"a.jpg", "image/jpeg", []byte{0xff, 0xd8, 0xff})
	if err != nil {
		t.Fatalf("UploadMedia: %v", err)
	}
	if out.ID != 5 || out.SourceURL != "https://site.example.com/wp-content/uploads/a.jpg" {
		t.Fatalf("result = %+v", out)
	}
	if gotContentType != "image/jpeg" {
		t.Fatalf("content type = %q", gotContentType)
	}
	if !strings.Contains(gotDisposition, "a.jpg") {
		t.Fatalf("content disposition = %q", gotDisposition)
	}
}

func TestUploadMediaReportsAnUnsupportedType(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"rest_cannot_upload"}`, http.StatusBadRequest)
	})
	defer client.CloseIdleConnections()
	_, err := client.UploadMedia(context.Background(), siteURL, "editor", "app-pass",
		"a.exe", "application/octet-stream", []byte{1})
	if err == nil {
		t.Fatal("an upload the site refuses must fail")
	}
}

func TestFindTagReturnsTheExistingTerm(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/wp/v2/tags" || r.URL.Query().Get("search") != "technology" {
			t.Errorf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id": 7, "name": "Technology", "slug": "technology"}]`)
	})
	defer client.CloseIdleConnections()

	tag, found, err := client.FindTag(context.Background(), siteURL, "editor", "app-pass", "technology")
	if err != nil || !found {
		t.Fatalf("FindTag: found=%v err=%v", found, err)
	}
	if tag.ID != 7 || tag.Name != "Technology" {
		t.Fatalf("tag = %+v", tag)
	}
}

func TestFindTagReportsAMissingTerm(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	})
	defer client.CloseIdleConnections()
	_, found, err := client.FindTag(context.Background(), siteURL, "editor", "app-pass", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("an empty search must report no term")
	}
}

func TestCreateTagMakesTheMissingTerm(t *testing.T) {
	var gotName string
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotName, _ = body["name"].(string)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id": 9, "name": "News", "slug": "news"}`)
	})
	defer client.CloseIdleConnections()
	tag, err := client.CreateTag(context.Background(), siteURL, "editor", "app-pass", "News")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if gotName != "News" {
		t.Fatalf("name = %q", gotName)
	}
	if tag.ID != 9 {
		t.Fatalf("tag = %+v", tag)
	}
}

func TestCreateTagReportsAPermissionRefusalWithoutPretendingItWorked(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	defer client.CloseIdleConnections()
	_, err := client.CreateTag(context.Background(), siteURL, "editor", "app-pass", "News")
	if !errors.Is(err, wordpress.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}
