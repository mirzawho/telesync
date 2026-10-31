package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/wordpress"
)

// The WordPress routes are driven here over the real SQLite store and the real
// account model, with a scripted WordPress API instead of a server: the sandbox
// cannot bind a port, and a test may not reach the network.

// testKeyBytes seeds the test encryptor. It stands for the deployment's key and
// is not a credential for anything.
var testKeyBytes = bytes.Repeat([]byte("t"), 32)

// wpEnv is the gateway with WordPress storage and a scripted WordPress API.
type wpEnv struct {
	*accountEnv
	enc     *secrets.Encrypter
	wpStore *channelstore.Store
	// mu guards the script and the record of what reached it.
	mu       sync.Mutex
	settings string
	media    map[string]string
	paths    []string
	auth     []string
	// status overrides the settings answer's status when non-zero.
	status int
}

// newWPEnv builds the gateway over a fresh WordPress store with the real client
// reading from a scripted site.
//
// The store is opened inside the configuration hook rather than before it:
// newAccountEnv stores its default administrator in cfg.Categories, so it must
// be the same store the WordPress routes read.
func newWPEnv(t *testing.T, mutates ...func(*gateway.Config)) *wpEnv {
	t.Helper()
	enc, err := secrets.NewEncrypter(testKeyBytes)
	if err != nil {
		t.Fatal(err)
	}
	env := &wpEnv{enc: enc, media: map[string]string{}}
	env.serveSite(`{"title":"Scripted Site","site_icon":0}`, nil)
	env.accountEnv = newAccountEnv(t, registry{}, append([]func(*gateway.Config){
		func(c *gateway.Config) {
			// The account the suite signs in as is stored through these
			// fields, so the WordPress store must be the same one.
			store, ok := c.Categories.(*channelstore.Store)
			if !ok {
				t.Fatal("the account store is not a channelstore.Store")
			}
			env.wpStore = store
			c.Users = store
			c.Categories = store
			c.Channels = store
			c.WordPress = store
			// The envelope carries the double's methods, so the site routes
			// have the category link the real store has.
			c.WordPress = siteStore{store}
			c.WordPressClient = wordpress.NewClient(
				wordpress.WithTransport(scriptTransport{env: env}),
				wordpress.WithTimeout(2*time.Second),
			)
			c.Secrets = enc
		},
	}, mutates...)...)
	return env
}

// serveSite scripts what the WordPress API answers.
func (e *wpEnv) serveSite(settings string, media map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.settings = settings
	e.media = map[string]string{}
	for k, v := range media {
		e.media[k] = v
	}
}

// hitsFor counts the requests that reached a path.
func (e *wpEnv) hitsFor(suffix string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, p := range e.paths {
		if strings.HasSuffix(p, suffix) {
			n++
		}
	}
	return n
}

// scriptTransport answers every request from the test's script.
type scriptTransport struct{ env *wpEnv }

func (s scriptTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	env := s.env
	env.mu.Lock()
	defer env.mu.Unlock()
	env.paths = append(env.paths, req.URL.Path)
	if user, pass, ok := req.BasicAuth(); ok {
		env.auth = append(env.auth, user+":"+pass)
	}

	status, body := http.StatusOK, ""
	switch {
	case strings.HasSuffix(req.URL.Path, "/wp-json/wp/v2/settings"):
		body = env.settings
		if env.status != 0 {
			status = env.status
		}
	case strings.Contains(req.URL.Path, "/wp-json/wp/v2/media/"):
		id := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		item, ok := env.media[id]
		if !ok {
			status = http.StatusNotFound
			item = `{"code":"rest_post_invalid_id"}`
		}
		body = item
	default:
		status = http.StatusNotFound
		body = `{"code":"rest_no_route"}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json; charset=UTF-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (*scriptTransport) CloseIdleConnections() {}

// siteView is the documented public shape of one registered site.
type siteView struct {
	ID          int64   `json:"id"`
	CategoryIDs []int64 `json:"category_ids"`
	URL         string  `json:"url"`
	Domain      string  `json:"domain"`
	Title       string  `json:"title"`
	IconURL     *string `json:"site_icon_url"`
	Username    string  `json:"username"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// create posts one registration and returns the response and its body.
func (e *wpEnv) create(t *testing.T, who, body string) (*http.Response, string) {
	t.Helper()
	resp := e.send(t, who, http.MethodPost, "/api/v1/local/wordpress", body)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(raw)
}

// patch sends one update.
func (e *wpEnv) patch(t *testing.T, who string, id int64, body string) (*http.Response, string) {
	t.Helper()
	resp := e.send(t, who, http.MethodPatch, fmt.Sprintf("/api/v1/local/wordpress/%d", id), body)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(raw)
}

// firstSite decodes the site of a create response.
func firstSite(t *testing.T, body string) siteView {
	t.Helper()
	var out struct {
		Data struct {
			Site siteView `json:"site"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return out.Data.Site
}

// storedSite reads one record straight from the store.
func (e *wpEnv) storedSite(t *testing.T, id int64) channelstore.WordPressSite {
	t.Helper()
	rec, found, err := e.store.GetWordPressSite(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("GetWordPressSite(%d): found=%v err=%v", id, found, err)
	}
	return rec
}

const registerBody = `{"url":"https://site.example.com","username":"admin","password":"abcd EFGH 1234"}`

func TestWordPressListRequiresACredential(t *testing.T) {
	env := newWPEnv(t)
	resp := env.sendAnon(t, http.MethodGet, "/api/v1/local/wordpress")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestWordPressCollectionRejectsAnUnknownMethod(t *testing.T) {
	env := newWPEnv(t)
	resp := env.send(t, defaultCaller, http.MethodDelete, "/api/v1/local/wordpress", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "GET, POST" {
		t.Fatalf("Allow = %q", allow)
	}
}

func TestWordPressListIsEmptyAndNeverInvented(t *testing.T) {
	env := newWPEnv(t)
	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Sites []siteView `json:"sites"`
		} `json:"data"`
	}
	decodeResp(t, resp, &body)
	if body.Data.Sites == nil || len(body.Data.Sites) != 0 {
		t.Fatalf("sites = %#v, want an empty array", body.Data.Sites)
	}
}

func TestWordPressRoutesAnswer503WithoutStorageOrAKey(t *testing.T) {
	for name, mutate := range map[string]func(*gateway.Config){
		"no storage": func(c *gateway.Config) { c.WordPress = nil },
		"no key":     func(c *gateway.Config) { c.Secrets = nil },
		"no client":  func(c *gateway.Config) { c.WordPressClient = nil },
	} {
		t.Run(name, func(t *testing.T) {
			env := newWPEnv(t, mutate)
			resp, _ := env.create(t, defaultCaller, registerBody)
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", resp.StatusCode)
			}
		})
	}
}

func TestWordPressCreateRejectsABadBody(t *testing.T) {
	cases := map[string]string{
		"not json":        `url=https://site.example.com`,
		"unknown field":   `{"url":"https://site.example.com","username":"a","password":"p","extra":1}`,
		"missing url":     `{"username":"a","password":"p"}`,
		"missing user":    `{"url":"https://site.example.com","password":"p"}`,
		"missing pass":    `{"url":"https://site.example.com","username":"a"}`,
		"empty pass":      `{"url":"https://site.example.com","username":"a","password":""}`,
		"empty user":      `{"url":"https://site.example.com","username":"  ","password":"p"}`,
		"wrong type":      `{"url":5,"username":"a","password":"p"}`,
		"trailing object": `{"url":"https://site.example.com","username":"a","password":"p"}{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			env := newWPEnv(t)
			resp, raw := env.create(t, defaultCaller, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", resp.StatusCode, raw)
			}
			if n := env.countSites(t); n != 0 {
				t.Fatalf("%d sites were stored", n)
			}
		})
	}

	env := newWPEnv(t)
	resp := env.sendBody(t, defaultCaller, http.MethodPost, "/api/v1/local/wordpress", "text/plain", registerBody)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain status = %d, want 415", resp.StatusCode)
	}
}

func TestWordPressCreateRejectsUnsafeURLsBeforeAnyRequest(t *testing.T) {
	cases := map[string]string{
		"plain http":      "http://site.example.com",
		"no scheme":       "site.example.com",
		"credentials":     "https://admin:secret@site.example.com",
		"loopback":        "https://127.0.0.1",
		"private":         "https://10.0.0.5/blog",
		"metadata":        "https://169.254.169.254/latest/meta-data",
		"metadata name":   "https://metadata.google.internal",
		"localhost":       "https://localhost:8080",
		"query":           "https://site.example.com/?a=1",
		"port":            "https://site.example.com:8443",
		"single label":    "https://intranet",
		"file scheme":     "file:///etc/passwd",
		"fragment":        "https://site.example.com/#x",
		"dot-dot in path": "https://site.example.com/a/../b",
	}
	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			env := newWPEnv(t)
			resp, raw := env.create(t, defaultCaller, fmt.Sprintf(`{"url":%q,"username":"admin","password":"p w d"}`, url))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", resp.StatusCode, raw)
			}
			if n := env.countSites(t); n != 0 {
				t.Fatalf("%d sites were stored", n)
			}
			if len(env.paths) != 0 {
				t.Fatalf("an unsafe URL produced a request: %v", env.paths)
			}
		})
	}
}

func TestWordPressCreateRefusesARedirectedRequest(t *testing.T) {
	env := newWPEnv(t)
	env.status = http.StatusFound
	resp, _ := env.create(t, defaultCaller, registerBody)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestWordPressCreateReportsEachFailureAndStoresNothing(t *testing.T) {
	cases := map[int]([]any){
		http.StatusUnauthorized:        {http.StatusUnauthorized, gateway.CodeWordPressAuth},
		http.StatusForbidden:           {http.StatusForbidden, gateway.CodeWordPressForbidden},
		http.StatusNotFound:            {http.StatusBadGateway, gateway.CodeWordPressIncompatible},
		http.StatusInternalServerError: {http.StatusBadGateway, gateway.CodeWordPressIncompatible},
	}
	for siteStatus, want := range cases {
		t.Run(fmt.Sprint(siteStatus), func(t *testing.T) {
			env := newWPEnv(t)
			env.status = siteStatus
			// A REST error body, as WordPress sends it.
			env.serveSite(`{"code":"rest_error","message":"no"}`, nil)
			resp, raw := env.create(t, defaultCaller, registerBody)
			if resp.StatusCode != want[0].(int) {
				t.Fatalf("status = %d (%s), want %d", resp.StatusCode, raw, want[0])
			}
			var body errorEnvelope
			if err := json.Unmarshal([]byte(raw), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != want[1].(string) {
				t.Fatalf("code = %q, want %q", body.Error.Code, want[1])
			}
			if strings.Contains(raw, "abcd EFGH 1234") {
				t.Fatalf("the response leaks the password: %s", raw)
			}
			if n := env.countSites(t); n != 0 {
				t.Fatalf("%d sites were recorded after a failure", n)
			}
		})
	}
}

func TestWordPressCreateRecordsTheTitleTheIconAndASealedCredential(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"My Blog","site_icon":42}`,
		map[string]string{"42": `{"id":42,"source_url":"https://site.example.com/wp-content/uploads/icon.png"}`})

	resp, raw := env.create(t, defaultCaller, registerBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%s)", resp.StatusCode, raw)
	}
	got := firstSite(t, raw)
	if got.ID == 0 || got.URL != "https://site.example.com" || got.Domain != "site.example.com" {
		t.Fatalf("site = %+v", got)
	}
	if got.Title != "My Blog" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.IconURL == nil || *got.IconURL != "https://site.example.com/wp-content/uploads/icon.png" {
		t.Fatalf("site_icon_url = %v", got.IconURL)
	}
	if got.Username != "admin" {
		t.Fatalf("username = %q", got.Username)
	}
	if strings.Contains(raw, "abcd EFGH") || strings.Contains(raw, "password") {
		t.Fatalf("the response carries credential material: %s", raw)
	}

	// The Basic credential reached the site exactly as given, once per call.
	if len(env.auth) != 2 || env.auth[0] != "admin:abcd EFGH 1234" {
		t.Fatalf("credentials seen by the site: %v", env.auth)
	}

	// At rest it is sealed, and only the key opens it.
	rec := env.storedSite(t, got.ID)
	if rec.Credential.Version != secrets.Version1 {
		t.Fatalf("credential version = %d", rec.Credential.Version)
	}
	plain, err := env.enc.Open(rec.Credential)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if plain != "abcd EFGH 1234" {
		t.Fatalf("the stored credential does not open to the password")
	}
}

func TestWordPressCreateNormalizesTheURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://Site.Example.COM/":         "https://site.example.com",
		"  HTTPS://Site.Example.com/blog/ ": "https://site.example.com/blog",
	} {
		t.Run(in, func(t *testing.T) {
			env := newWPEnv(t)
			env.serveSite(`{"title":"T","site_icon":0}`, nil)
			resp, raw := env.create(t, defaultCaller,
				fmt.Sprintf(`{"url":%q,"username":"admin","password":"p"}`, in))
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status = %d (%s)", resp.StatusCode, raw)
			}
			if got := firstSite(t, raw); got.URL != want {
				t.Fatalf("url = %q, want %q", got.URL, want)
			}
		})
	}
}

func TestWordPressCreateRefusesASiteThatIsAlreadyRegistered(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	if resp, raw := env.create(t, defaultCaller, registerBody); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: %d (%s)", resp.StatusCode, raw)
	}
	// The same site under another spelling of the same URL.
	resp, raw := env.create(t, defaultCaller,
		`{"url":"https://SITE.example.com/","username":"other","password":"p"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second create: %d (%s), want 409", resp.StatusCode, raw)
	}
	if n := env.countSites(t); n != 1 {
		t.Fatalf("%d sites stored", n)
	}
}

func TestWordPressListShowsSitesWithoutCredentials(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"One","site_icon":0}`, nil)
	if resp, raw := env.create(t, defaultCaller, registerBody); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d (%s)", resp.StatusCode, raw)
	}

	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw := readBody(t, resp)
	if strings.Contains(raw, "abcd") || strings.Contains(raw, "credential") || strings.Contains(raw, "nonce") {
		t.Fatalf("the list carries credential material: %s", raw)
	}
	var body struct {
		Data struct {
			Sites []siteView `json:"sites"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Sites) != 1 || body.Data.Sites[0].Title != "One" {
		t.Fatalf("sites = %+v", body.Data.Sites)
	}
}

func TestWordPressPatchKeepsOmittedFieldsAndTheCredential(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"Old Title","site_icon":0}`, nil)
	_, raw := env.create(t, defaultCaller, registerBody)
	created := firstSite(t, raw)

	// Only the title changes; the remote site is asked again because the
	// metadata is refreshed, and answers the same title, which the store takes.
	env.serveSite(`{"title":"New Title","site_icon":0}`, nil)
	resp, raw := env.patch(t, defaultCaller, created.ID, `{"username":"editor"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, raw)
	}
	got := firstSite(t, raw)
	if got.Username != "editor" {
		t.Fatalf("username = %q", got.Username)
	}
	if got.URL != created.URL || got.Title != "New Title" {
		t.Fatalf("site = %+v", got)
	}
	rec := env.storedSite(t, created.ID)
	plain, err := env.enc.Open(rec.Credential)
	if err != nil || plain != "abcd EFGH 1234" {
		t.Fatalf("the credential was not preserved: %q %v", plain, err)
	}
	if rec.CreatedAt.Format(time.RFC3339) != created.CreatedAt {
		t.Fatalf("created_at = %v, want %s", rec.CreatedAt, created.CreatedAt)
	}
	if strings.Contains(raw, "abcd EFGH") {
		t.Fatalf("the response leaks the password: %s", raw)
	}
}

func TestWordPressPatchRotationIsValidatedBeforeItIsStored(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	_, raw := env.create(t, defaultCaller, registerBody)
	created := firstSite(t, raw)

	// The new credential is refused by the site: the old one stays.
	env.status = http.StatusUnauthorized
	resp, body := env.patch(t, defaultCaller, created.ID, `{"password":"new password"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d (%s)", resp.StatusCode, body)
	}
	rec := env.storedSite(t, created.ID)
	if plain, _ := env.enc.Open(rec.Credential); plain != "abcd EFGH 1234" {
		t.Fatalf("the credential was replaced by a failed rotation: %q", plain)
	}
	if strings.Contains(body, "new password") || strings.Contains(body, "abcd EFGH") {
		t.Fatalf("the response leaks a password: %s", body)
	}

	// The new domain is refused too: nothing about the record moves.
	env.status = http.StatusForbidden
	resp, _ = env.patch(t, defaultCaller, created.ID, `{"url":"https://other.example.com"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if rec := env.storedSite(t, created.ID); rec.URL != "https://site.example.com" {
		t.Fatalf("url after a failed domain change = %q", rec.URL)
	}

	// And the old credential still works, so the record was never damaged.
	env.status = 0
	env.serveSite(`{"title":"Still Mine","site_icon":0}`, nil)
	resp, _ = env.patch(t, defaultCaller, created.ID, `{"username":"admin"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status after recovery = %d", resp.StatusCode)
	}
	if rec := env.storedSite(t, created.ID); rec.Title != "Still Mine" {
		t.Fatalf("title = %q", rec.Title)
	}
}

func TestWordPressPatchRejectsUnsafeOrUnknownInput(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	_, raw := env.create(t, defaultCaller, registerBody)
	created := firstSite(t, raw)

	for name, body := range map[string]string{
		"unknown field": `{"title":"ignored"}`,
		"empty body":    `{}`,
		"http url":      `{"url":"http://site.example.com"}`,
		"credentials":   `{"url":"https://u:p@other.example.com"}`,
		"empty user":    `{"username":""}`,
		"empty pass":    `{"password":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp, raw := env.patch(t, defaultCaller, created.ID, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", resp.StatusCode, raw)
			}
		})
	}
	for _, rawID := range []string{"0", "-1", "x", "1.5"} {
		resp := env.send(t, defaultCaller, http.MethodPatch, "/api/v1/local/wordpress/"+rawID, `{"username":"a"}`)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("id %q: status = %d, want 400", rawID, resp.StatusCode)
		}
	}
	resp := env.send(t, defaultCaller, http.MethodPatch, "/api/v1/local/wordpress/9999", `{"username":"a"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing site: status = %d, want 404", resp.StatusCode)
	}
}

func TestWordPressDeleteRemovesOnlyTheRegistration(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"A","site_icon":0}`, nil)
	_, raw := env.create(t, defaultCaller, registerBody)
	first := firstSite(t, raw)
	env.serveSite(`{"title":"B","site_icon":0}`, nil)
	secondRaw := `{"url":"https://second.example.com","username":"admin","password":"p"}`
	_, raw = env.create(t, defaultCaller, secondRaw)
	second := firstSite(t, raw)

	requestsBefore := len(env.paths)
	resp := env.send(t, defaultCaller, http.MethodDelete, fmt.Sprintf("/api/v1/local/wordpress/%d", first.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Deleted bool  `json:"deleted"`
			ID      int64 `json:"id"`
		} `json:"data"`
	}
	decodeResp(t, resp, &body)
	if !body.Data.Deleted || body.Data.ID != first.ID {
		t.Fatalf("delete body = %+v", body.Data)
	}
	if _, found, _ := env.store.GetWordPressSite(context.Background(), first.ID); found {
		t.Fatal("the deleted site is still stored")
	}
	if _, found, _ := env.store.GetWordPressSite(context.Background(), second.ID); !found {
		t.Fatal("deleting one site removed the other")
	}
	if len(env.paths) != requestsBefore {
		t.Fatal("delete contacted the remote site")
	}

	resp = env.send(t, defaultCaller, http.MethodDelete, fmt.Sprintf("/api/v1/local/wordpress/%d", first.ID), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete: status = %d, want 404", resp.StatusCode)
	}
	resp = env.send(t, defaultCaller, http.MethodDelete, "/api/v1/local/wordpress/nope", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad id: status = %d, want 400", resp.StatusCode)
	}
}

func TestWordPressSiteItemMethods(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	_, raw := env.create(t, defaultCaller, registerBody)
	created := firstSite(t, raw)

	for _, method := range []string{http.MethodPut, http.MethodPost} {
		resp := env.send(t, defaultCaller, method, fmt.Sprintf("/api/v1/local/wordpress/%d", created.ID), "")
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s: status = %d, want 405", method, resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow != "GET, PATCH, DELETE" {
			t.Fatalf("%s: Allow = %q", method, allow)
		}
	}
}

func TestWordPressUnknownPathBelowTheCollectionIs404(t *testing.T) {
	env := newWPEnv(t)
	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/1/credentials", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("credentials: status = %d, want 404", resp.StatusCode)
	}
}

func TestWordPressPostsRefusesAnyMethodButPost(t *testing.T) {
	// The route exists; GET on it is a method error, not a missing page.
	env := newWPEnv(t)
	resp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/1/posts", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestWordPressCreateNeedsTheManagePermission(t *testing.T) {
	env := newWPEnv(t)
	env.signedIn = true
	viewer := env.account(t, "viewer", channelstore.PermissionWordPressView)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)

	resp, _ := env.create(t, viewer.token, registerBody)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if n := env.countSites(t); n != 0 {
		t.Fatalf("%d sites stored by a caller without the permission", n)
	}

	// A global view reads the list; it does not write.
	listResp := env.send(t, viewer.token, http.MethodGet, "/api/v1/local/wordpress", "")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", listResp.StatusCode)
	}

	// The manage permission admits the write.
	manager := env.account(t, "manager", channelstore.PermissionWordPressView, channelstore.PermissionWordPressManage)
	resp, raw := env.create(t, manager.token, registerBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("manager create: %d (%s)", resp.StatusCode, raw)
	}
	id := firstSite(t, raw).ID

	// PATCH and DELETE need manage too, even for a site the caller can view.
	for _, req := range []struct{ method, body string }{
		{http.MethodPatch, `{"username":"x"}`},
		{http.MethodDelete, ""},
	} {
		resp := env.send(t, viewer.token, req.method, fmt.Sprintf("/api/v1/local/wordpress/%d", id), req.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", req.method, resp.StatusCode)
		}
	}
	if _, found, _ := env.store.GetWordPressSite(context.Background(), id); !found {
		t.Fatal("a forbidden DELETE removed the site")
	}
}

func TestWordPressCallerWithNoPermissionSeesNothing(t *testing.T) {
	env := newWPEnv(t)
	env.signedIn = true
	nobody := env.account(t, "nobody")
	resp := env.send(t, nobody.token, http.MethodGet, "/api/v1/local/wordpress", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// countSites is the number of registrations the store holds.
func (e *wpEnv) countSites(t *testing.T) int {
	t.Helper()
	sites, err := e.store.ListWordPressSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(sites)
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func decodeResp(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	raw := readBody(t, resp)
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
}

func TestWordPressSiteMetadataFollowsTheRemoteSite(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"Old","site_icon":5}`,
		map[string]string{"5": `{"id":5,"source_url":"https://site.example.com/a.png"}`})
	_, raw := env.create(t, defaultCaller, registerBody)
	created := firstSite(t, raw)
	if created.IconURL == nil || *created.IconURL != "https://site.example.com/a.png" {
		t.Fatalf("icon = %v", created.IconURL)
	}

	// The site is renamed and drops its icon: the update refreshes both, so
	// the old icon URL does not linger.
	env.serveSite(`{"title":"Renamed","site_icon":0}`, nil)
	resp, raw := env.patch(t, defaultCaller, created.ID, `{"username":"admin"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, raw)
	}
	got := firstSite(t, raw)
	if got.Title != "Renamed" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.IconURL != nil {
		t.Fatalf("icon = %v, want none", *got.IconURL)
	}
}

func TestWordPressNonNumericIconIDIsNotAnError(t *testing.T) {
	// A site answer whose site_icon cannot be read leaves the icon empty and
	// the registration intact: the icon is optional, the title is not.
	env := newWPEnv(t)
	env.serveSite(`{"title":"Odd Plugin","site_icon":{"rendered":7}}`, nil)
	resp, raw := env.create(t, defaultCaller, registerBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%s)", resp.StatusCode, raw)
	}
	got := firstSite(t, raw)
	if got.Title != "Odd Plugin" || got.IconURL != nil {
		t.Fatalf("site = %+v", got)
	}
}

func TestWordPressSiteListShowsOnlySitesTheCallerMaySee(t *testing.T) {
	env := newWPEnv(t)
	env.signedIn = true
	env.serveSite(`{"title":"Mine","site_icon":0}`, nil)
	_, raw := env.create(t, env.defaults.token, registerBody)
	mine := firstSite(t, raw).ID
	env.serveSite(`{"title":"Theirs","site_icon":0}`, nil)
	_, raw = env.create(t, env.defaults.token,
		`{"url":"https://other.example.com","username":"admin","password":"p"}`)
	theirs := firstSite(t, raw).ID

	// A per-site allow shows exactly that site: the global list would show
	// both, and a site with no assignment is hidden.
	holder := env.account(t, "holder")
	env.grantWordPress(t, holder, channelstore.ResourcePermissions{ResourceID: mine,
		Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}})

	resp := env.send(t, holder.token, http.MethodGet, "/api/v1/local/wordpress", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Sites []siteView `json:"sites"`
		} `json:"data"`
	}
	decodeResp(t, resp, &body)
	if len(body.Data.Sites) != 1 || body.Data.Sites[0].ID != mine {
		t.Fatalf("sites = %+v, want only %d", body.Data.Sites, mine)
	}
	_ = theirs
}

// grantWordPress replaces an account's per-site assignments.
func (e *wpEnv) grantWordPress(t *testing.T, who identity, sets ...channelstore.ResourcePermissions) {
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

func TestWordPressSiteCredentialIsReadableOnlyInsideTheBackend(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	_, raw := env.create(t, defaultCaller, registerBody)
	id := firstSite(t, raw).ID

	// The service the future publishing task uses: it opens the credential for
	// a request and never exposes it otherwise.
	var svc gateway.WordPressSiteService = env.service(t)
	rec, password, err := svc.Credential(context.Background(), id)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if password != "abcd EFGH 1234" {
		t.Fatalf("password = %q", password)
	}
	if rec.URL != "https://site.example.com" {
		t.Fatalf("url = %q", rec.URL)
	}
	if _, _, err := svc.Credential(context.Background(), 9999); !errors.Is(err, channelstore.ErrSiteNotFound) {
		t.Fatalf("missing site error = %v", err)
	}
}

// service is the gateway as the publishing service would hold it.
func (e *wpEnv) service(t *testing.T) *gateway.Server {
	t.Helper()
	server, ok := e.handler.(*gateway.Server)
	if !ok {
		t.Fatalf("the handler is %T", e.handler)
	}
	return server
}

func TestWordPressWritesAnswerADocumentNotARedirect(t *testing.T) {
	// The panel's client reads any non-2xx (a 3xx included) as a failure, so a
	// write must answer with a body that says what to do next.
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	resp, raw := env.create(t, defaultCaller, registerBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d (%s)", resp.StatusCode, raw)
	}
	var created struct {
		Data struct {
			Saved struct {
				ID         int64  `json:"id"`
				Refresh    bool   `json:"refresh"`
				RedirectTo string `json:"redirect_to"`
			} `json:"saved"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Data.Saved.Refresh || created.Data.Saved.ID == 0 ||
		created.Data.Saved.RedirectTo != fmt.Sprintf("/api/v1/local/wordpress/%d", created.Data.Saved.ID) {
		t.Fatalf("saved = %+v", created.Data.Saved)
	}

	resp, raw = env.patch(t, defaultCaller, created.Data.Saved.ID, `{"username":"editor"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d (%s)", resp.StatusCode, raw)
	}
	if !strings.Contains(raw, `"refresh":true`) {
		t.Fatalf("update body = %s", raw)
	}

	resp = env.send(t, defaultCaller, http.MethodDelete,
		fmt.Sprintf("/api/v1/local/wordpress/%d", created.Data.Saved.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `"redirect_to":"/api/v1/local/wordpress"`) {
		t.Fatalf("delete body = %s", body)
	}
}

func TestWordPressCreateAcceptsCategoriesAndTheViewsListThem(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	ctx := context.Background()
	news, err := env.store.CreateCategory(ctx, "News", "")
	if err != nil {
		t.Fatal(err)
	}
	tech, err := env.store.CreateCategory(ctx, "Tech", "")
	if err != nil {
		t.Fatal(err)
	}

	resp, raw := env.create(t, defaultCaller, fmt.Sprintf(
		`{"url":"https://site.example.com","username":"admin","password":"abcd EFGH 1234","category_ids":[%d,%d,%d]}`,
		news.ID, tech.ID, news.ID))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%s)", resp.StatusCode, raw)
	}
	created := firstSite(t, raw)
	if len(created.CategoryIDs) != 2 || created.CategoryIDs[0] != news.ID || created.CategoryIDs[1] != tech.ID {
		t.Fatalf("category_ids = %v", created.CategoryIDs)
	}

	listResp := env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress", "")
	listRaw := readBody(t, listResp)
	if !strings.Contains(listRaw, fmt.Sprintf(`"category_ids":[%d,%d]`, news.ID, tech.ID)) {
		t.Fatalf("list = %s", listRaw)
	}
}

func TestWordPressCreateRejectsACategoryThatDoesNotExist(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	resp, raw := env.create(t, defaultCaller, `{"url":"https://site.example.com","username":"admin","password":"abcd EFGH 1234","category_ids":[999]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want 400", resp.StatusCode, raw)
	}
	if strings.Contains(raw, "999") && !strings.Contains(raw, "دسته") {
		t.Fatalf("body = %s", raw)
	}
	if n := env.countSites(t); n != 0 {
		t.Fatalf("%d sites were stored", n)
	}
}

func TestWordPressCreateRejectsANegativeOrRepeatedCategory(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	for name, body := range map[string]string{
		"negative": `{"url":"https://site.example.com","username":"admin","password":"p","category_ids":[-1]}`,
		"zero":     `{"url":"https://site.example.com","username":"admin","password":"p","category_ids":[0]}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp, raw := env.create(t, defaultCaller, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d (%s)", resp.StatusCode, raw)
			}
		})
	}
}

func TestWordPressPatchItselfKeepsCategories(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	news, _ := env.store.CreateCategory(context.Background(), "News", "")
	_, raw := env.create(t, defaultCaller, fmt.Sprintf(
		`{"url":"https://site.example.com","username":"admin","password":"p","category_ids":[%d]}`, news.ID))
	created := firstSite(t, raw)
	resp, raw := env.patch(t, defaultCaller, created.ID, `{"username":"editor"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, raw)
	}
	if got := firstSite(t, raw); len(got.CategoryIDs) != 1 || got.CategoryIDs[0] != news.ID {
		t.Fatalf("category_ids after a plain update = %v", got.CategoryIDs)
	}
}

func TestWordPressPatchChangesCategories(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	ctx := context.Background()
	news, _ := env.store.CreateCategory(ctx, "News", "")
	tech, _ := env.store.CreateCategory(ctx, "Tech", "")
	_, raw := env.create(t, defaultCaller, fmt.Sprintf(
		`{"url":"https://site.example.com","username":"admin","password":"p","category_ids":[%d]}`, news.ID))
	created := firstSite(t, raw)

	// add_category_ids files it in another category, keeping the first.
	// `username` is repeated because a normalising PATCH calls the remote site
	// and rewrites the record, which starts from the stored categories.
	resp, raw := env.patch(t, defaultCaller, created.ID,
		fmt.Sprintf(`{"username":"admin","add_category_ids":[%d]}`, tech.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add: %d (%s)", resp.StatusCode, raw)
	}
	got := firstSite(t, raw)
	if len(got.CategoryIDs) != 2 {
		t.Fatalf("after add: %v", got.CategoryIDs)
	}
	// remove_category_ids drops one and keeps the rest.
	resp, raw = env.patch(t, defaultCaller, created.ID, fmt.Sprintf(`{"remove_category_ids":[%d]}`, news.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove: %d (%s)", resp.StatusCode, raw)
	}
	got = firstSite(t, raw)
	if len(got.CategoryIDs) != 1 || got.CategoryIDs[0] != tech.ID {
		t.Fatalf("after remove: %v", got.CategoryIDs)
	}
	// category_ids replaces the whole set.
	resp, raw = env.patch(t, defaultCaller, created.ID, fmt.Sprintf(`{"category_ids":[%d]}`, news.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replace: %d (%s)", resp.StatusCode, raw)
	}
	got = firstSite(t, raw)
	if len(got.CategoryIDs) != 1 || got.CategoryIDs[0] != news.ID {
		t.Fatalf("after replace: %v", got.CategoryIDs)
	}
}

func TestWordPressGetOneSiteReturnsItWithCategories(t *testing.T) {
	env := newWPEnv(t)
	env.serveSite(`{"title":"T","site_icon":0}`, nil)
	news, _ := env.store.CreateCategory(context.Background(), "News", "")
	_, raw := env.create(t, defaultCaller, fmt.Sprintf(
		`{"url":"https://site.example.com","username":"admin","password":"p","category_ids":[%d]}`, news.ID))
	created := firstSite(t, raw)

	resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("/api/v1/local/wordpress/%d", created.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := firstSite(t, readBody(t, resp))
	if got.ID != created.ID || len(got.CategoryIDs) != 1 {
		t.Fatalf("site = %+v", got)
	}
	resp = env.send(t, defaultCaller, http.MethodGet, "/api/v1/local/wordpress/999", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing: status = %d", resp.StatusCode)
	}
}

func TestWordPressPatchRejectsBothCategoryForms(t *testing.T) {
	env := newWPEnv(t)
	created := createSite(t, env, "https://site.example.com")
	resp := env.send(t, defaultCaller, http.MethodPatch, fmt.Sprintf("/api/v1/local/wordpress/%d", created),
		`{"category_ids":[],"add_category_ids":[]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// siteStore is the SQLite store as the interface the gateway holds. The
// envelope carries the link methods too: a *channelstore.Store in an interface
// hides them.
type siteStore struct{ *channelstore.Store }

var _ gateway.WordPressStore = siteStore{}
var _ gateway.WordPressCategories = siteStore{}
