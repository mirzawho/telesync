package wordpress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// PostInput is a prepared WordPress post.
//
// It is built from a source message the user edited: the title and content are
// what will appear on the site, and the tags are the WordPress terms to attach,
// already resolved to term ids. Nothing here reads or writes the source.
type PostInput struct {
	// Title is the post title. WordPress requires one.
	Title string
	// Slug is the post's slug. Empty lets WordPress generate one from the
	// title, which is what a non-Latin title needs.
	Slug string
	// Content is the post body, already converted to safe HTML.
	Content string
	// Status is "publish" for immediate publication, or "draft".
	Status string
	// CategoryIDs are the WordPress category term ids to attach. They belong to
	// the site being posted to and are never shared between sites.
	CategoryIDs []int
	// TagIDs are the WordPress post_tag term ids to attach.
	TagIDs []int
	// FeaturedMedia is the attachment id to use as the featured image.
	FeaturedMedia int
	// CommentStatus is "open" or "closed".
	CommentStatus string
}

// PostResult is what the site confirmed about the created post.
type PostResult struct {
	ID          int
	Link        string
	Status      string
	Title       string
	CategoryIDs []int
	TagIDs      []int
}

// MediaRef is an uploaded attachment.
type MediaRef struct {
	ID        int
	SourceURL string
	MediaType string
	MimeType  string
}

// TagRef is one WordPress post_tag term.
type TagRef struct {
	ID   int
	Name string
	Slug string
}

// CreatePost publishes one prepared post and reports what the site confirmed.
//
// A post the site did not answer for is never reported as created.
func (c *Client) CreatePost(ctx context.Context, baseURL, username, password string, in PostInput) (PostResult, error) {
	target, err := c.apiTarget(baseURL, "posts")
	if err != nil {
		return PostResult{}, err
	}
	body := map[string]any{
		"title":   in.Title,
		"content": in.Content,
		"status":  in.Status,
	}
	if in.Status == "" {
		body["status"] = "publish"
	}
	// A slug is only named when the caller has one. An absent slug is not sent
	// as an empty string: WordPress generates one from the title, which is the
	// only sensible answer for a title with no Latin in it.
	if in.Slug != "" {
		body["slug"] = in.Slug
	}
	if len(in.CategoryIDs) > 0 {
		categories := make([]int, 0, len(in.CategoryIDs))
		categories = append(categories, in.CategoryIDs...)
		body["categories"] = categories
	}
	if len(in.TagIDs) > 0 {
		tags := make([]int, 0, len(in.TagIDs))
		tags = append(tags, in.TagIDs...)
		body["tags"] = tags
	}
	if in.FeaturedMedia > 0 {
		body["featured_media"] = in.FeaturedMedia
	}
	if in.CommentStatus != "" {
		body["comment_status"] = in.CommentStatus
	}

	var reply struct {
		ID     int    `json:"id"`
		Link   string `json:"link"`
		Status string `json:"status"`
		Title  struct {
			Rendered string `json:"rendered"`
		} `json:"title"`
		Categories []int `json:"categories"`
		Tags       []int `json:"tags"`
	}
	if err := c.postJSON(ctx, target, username, password, body, &reply); err != nil {
		return PostResult{}, err
	}
	if reply.ID <= 0 {
		return PostResult{}, fmt.Errorf("%w: the answer names no post id", ErrIncompatible)
	}
	result := PostResult{
		ID:     reply.ID,
		Link:   reply.Link,
		Status: reply.Status,
		Title:  reply.Title.Rendered,
	}
	result.CategoryIDs = append([]int(nil), reply.Categories...)
	result.TagIDs = append([]int(nil), reply.Tags...)
	if result.CategoryIDs == nil {
		result.CategoryIDs = []int{}
	}
	if result.TagIDs == nil {
		result.TagIDs = []int{}
	}
	return result, nil
}

// UploadMedia uploads one attachment and returns what the site created.
func (c *Client) UploadMedia(ctx context.Context, baseURL, username, password, filename, mimeType string, data []byte) (MediaRef, error) {
	target, err := c.apiTarget(baseURL, "media")
	if err != nil {
		return MediaRef{}, err
	}
	if filename == "" {
		filename = "attachment"
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return MediaRef{}, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("Content-Disposition", `attachment; filename="`+quoteDisposition(filename)+`"`)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "telesync")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return MediaRef{}, describeRequestError(ctx, err)
	}
	defer resp.Body.Close()

	var reply struct {
		ID        int    `json:"id"`
		SourceURL string `json:"source_url"`
		MediaType string `json:"media_type"`
		MimeType  string `json:"mime_type"`
	}
	if err := c.readJSONResponse(resp, "media", &reply); err != nil {
		return MediaRef{}, err
	}
	if reply.ID <= 0 {
		return MediaRef{}, fmt.Errorf("%w: the answer names no attachment id", ErrIncompatible)
	}
	return MediaRef{
		ID:        reply.ID,
		SourceURL: reply.SourceURL,
		MediaType: reply.MediaType,
		MimeType:  reply.MimeType,
	}, nil
}

// FindTag looks for a post_tag term by name.
func (c *Client) FindTag(ctx context.Context, baseURL, username, password, name string) (TagRef, bool, error) {
	target, err := c.apiTarget(baseURL, "tags")
	if err != nil {
		return TagRef{}, false, err
	}
	target += "?search=" + url.QueryEscape(name)

	var reply []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := c.getJSON(ctx, c.httpClient(), target, username, password, &reply); err != nil {
		return TagRef{}, false, err
	}
	// Only an exact name match is a match: a search that found a similar term
	// must not be treated as the requested one.
	for _, t := range reply {
		if strings.EqualFold(t.Name, name) {
			return TagRef{ID: t.ID, Name: t.Name, Slug: t.Slug}, true, nil
		}
	}
	return TagRef{}, false, nil
}

// CreateTag creates a post_tag term and returns it.
func (c *Client) CreateTag(ctx context.Context, baseURL, username, password, name string) (TagRef, error) {
	target, err := c.apiTarget(baseURL, "tags")
	if err != nil {
		return TagRef{}, err
	}
	var reply struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := c.postJSON(ctx, target, username, password, map[string]any{"name": name}, &reply); err != nil {
		return TagRef{}, err
	}
	if reply.ID <= 0 {
		return TagRef{}, fmt.Errorf("%w: the answer names no term id", ErrIncompatible)
	}
	return TagRef{ID: reply.ID, Name: reply.Name, Slug: reply.Slug}, nil
}

// TermRef is one WordPress term: a category or a tag.
type TermRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	// Count is how many posts carry the term. WordPress reports it; it is not
	// computed here.
	Count int `json:"count"`
}

// TermPage is one page of a site's taxonomy.
type TermPage struct {
	Terms []TermRef
	// Total and TotalPages are the site's own counts, which is why a caller can
	// page without guessing.
	Total      int
	TotalPages int
	Page       int
	PerPage    int
}

// ListTerms reads one page of a site's categories or tags.
//
// taxonomy is "categories" or "tags" — the REST resource WordPress itself
// exposes. The search narrows by name on the site's side, and pagination is the
// site's own, so a large taxonomy is never fetched in one go.
func (c *Client) ListTerms(ctx context.Context, baseURL, username, password, taxonomy, search string, page, perPage int) (TermPage, error) {
	if taxonomy != "categories" && taxonomy != "tags" {
		return TermPage{}, fmt.Errorf("wordpress: unknown taxonomy %q", taxonomy)
	}
	target, err := c.apiTarget(baseURL, taxonomy)
	if err != nil {
		return TermPage{}, err
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		// 100 is WordPress's own ceiling for per_page.
		perPage = 100
	}
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("per_page", strconv.Itoa(perPage))
	if search != "" {
		query.Set("search", search)
	}
	target += "?" + query.Encode()

	var reply []TermRef
	resp, err := c.getJSONPage(ctx, target, username, password, &reply)
	if err != nil {
		return TermPage{}, err
	}
	terms := append([]TermRef(nil), reply...)
	if terms == nil {
		terms = []TermRef{}
	}
	result := TermPage{Terms: terms, Page: page, PerPage: perPage}
	result.Total = headerInt(resp, "X-WP-Total")
	result.TotalPages = headerInt(resp, "X-WP-TotalPages")
	return result, nil
}

// getJSONPage performs one authenticated GET and decodes it, keeping the
// response so its pagination headers can still be read afterwards.
func (c *Client) getJSONPage(ctx context.Context, target, username, password string, out any) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "telesync")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, describeRequestError(ctx, err)
	}
	defer resp.Body.Close()
	if err := c.readJSONResponse(resp, requestTarget(target), out); err != nil {
		return nil, err
	}
	return resp, nil
}

// headerInt reads one of WordPress's pagination counters. A header the site did
// not send is zero, which callers read as "unknown" rather than as a count.
func headerInt(resp *http.Response, name string) int {
	if resp == nil {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get(name)))
	if err != nil {
		return 0
	}
	return value
}

// quoteDisposition keeps a file name inside a Content-Disposition value.
func quoteDisposition(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '\r' || r == '\n' {
			return '_'
		}
		return r
	}, name)
}

// apiTarget builds one REST API URL under the site's normalized base.
func (c *Client) apiTarget(baseURL, resource string) (string, error) {
	base, err := NormalizeBaseURL(baseURL, c.policy)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(base, "/") + "/wp-json/wp/v2/" + resource, nil
}

// postJSON performs one authenticated POST and decodes a JSON object.
func (c *Client) postJSON(ctx context.Context, target, username, password string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "telesync")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return describeRequestError(ctx, err)
	}
	defer resp.Body.Close()
	return c.readJSONResponse(resp, requestTarget(target), out)
}

// readJSONResponse maps one REST answer onto the package's error sentinels and
// decodes it. A refusal is reported as a refusal; it is never read as success.
func (c *Client) readJSONResponse(resp *http.Response, target string, out any) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		if isTimeout(err) {
			return ErrTimeout
		}
		return fmt.Errorf("%w: reading the answer failed", ErrUnreachable)
	}
	if int64(len(raw)) > c.maxBody {
		return fmt.Errorf("%w: the answer is larger than %d bytes", ErrIncompatible, c.maxBody)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: the WordPress user may not perform this operation "+
			"(it needs the publish_posts or upload_files capability)", ErrForbidden)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Errorf("%w: it answered with a redirect (HTTP %d) instead of the REST API",
			ErrIncompatible, resp.StatusCode)
	default:
		return fmt.Errorf("%w: it answered HTTP %d for %s", ErrIncompatible, resp.StatusCode, target)
	}
	if err := checkJSON(resp.Header.Get("Content-Type"), resp.StatusCode, target); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: the answer is not the expected JSON object", ErrIncompatible)
	}
	return nil
}
