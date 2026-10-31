package wordpress_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/wordpress"
)

// siteURL is the address every fake site is reached at. It is a public
// hostname so the production policy is never relaxed for a test: only the
// socket is in memory, because this sandbox cannot bind a port.
const siteURL = "https://site.example.com"

// site is a WordPress REST API served in memory, and what reached it.
//
// Only the socket is in memory: the request still goes through the real
// http.Client, Transport and redirect policy, because this sandbox cannot bind
// a port and the tests must not reach the network.
type site struct {
	handlerFn http.HandlerFunc
	// hits counts every request that reached the server.
	hits atomic.Int32
	// paths records the request paths in order.
	paths chan string
}

// newSite serves handler at siteURL and returns a client for it.
func newSite(t *testing.T, handler http.HandlerFunc, opts ...wordpress.Option) (*wordpress.Client, *site) {
	t.Helper()
	s := &site{paths: make(chan string, 16)}
	s.handlerFn = handler
	rt := &pipeTransport{t: t, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		select {
		case s.paths <- r.URL.Path:
		default:
		}
		handler(w, r)
	})}
	opts = append([]wordpress.Option{wordpress.WithTransport(rt), wordpress.WithTimeout(2 * time.Second)}, opts...)
	return wordpress.NewClient(opts...), s
}

// pipeTransport delivers each request to handler over net.Pipe.
type pipeTransport struct {
	t       *testing.T
	handler http.Handler
	// refuse makes every request fail at the transport, like a closed port.
	refuse bool
}

// handler returns the handler the site answers with.
func (s *site) handler() http.Handler { return s.handlerFn }

func (p *pipeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if p.refuse {
		return nil, errors.New("dial tcp: connect: connection refused")
	}
	url := *req.URL
	url.Scheme = "http"
	url.Host = "pipe"
	clientSide, serverSide := net.Pipe()
	srv := &http.Server{Handler: p.handler}
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(&oneConnListener{conn: serverSide})
		close(done)
	}()
	out := req.Clone(req.Context())
	out.URL = &url
	out.RequestURI = ""
	tr := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return clientSide, nil },
	}
	resp, err := tr.RoundTrip(out)
	if err != nil {
		_ = clientSide.Close()
		<-done
		return nil, err
	}
	resp.Body = &closingBody{ReadCloser: resp.Body, close: func() {
		_ = clientSide.Close()
		<-done
	}}
	return resp, nil
}

func (*pipeTransport) CloseIdleConnections() {}

type closingBody struct {
	io.ReadCloser
	close func()
}

func (b *closingBody) Close() error {
	err := b.ReadCloser.Close()
	b.close()
	return err
}

// oneConnListener serves exactly one in-memory connection.
type oneConnListener struct {
	conn net.Conn
	done bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.done {
		return nil, net.ErrClosed
	}
	l.done = true
	return l.conn, nil
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

func wordpressHandler(t *testing.T, user, pass string, iconID int, iconURL string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"rest_not_logged_in","message":"You are not currently logged in.","data":{"status":401}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		switch r.URL.Path {
		case "/wp-json/wp/v2/settings":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"title":     "My Test Site",
				"site_icon": iconID,
				"blogname":  "My Test Site",
			})
		case "/wp-json/wp/v2/media/" + fmt.Sprint(iconID):
			if iconURL == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"code":"rest_post_invalid_id"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         iconID,
				"source_url": iconURL,
				"media_type": "image",
				"mime_type":  "image/png",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"rest_no_route"}`)
		}
	}
}

func TestProbeReadsTitleAndResolvesTheIconAttachment(t *testing.T) {
	client, srv := newSite(t, wordpressHandler(t, "admin", "abcd EFGH 1234", 42, "https://example.com/wp-content/uploads/icon.png"))

	info, err := client.Probe(context.Background(), siteURL, "admin", "abcd EFGH 1234")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.Title != "My Test Site" {
		t.Fatalf("Title = %q", info.Title)
	}
	if info.SiteIconURL == nil || *info.SiteIconURL != "https://example.com/wp-content/uploads/icon.png" {
		t.Fatalf("SiteIconURL = %v", info.SiteIconURL)
	}

	// The settings document first, then the attachment: the id is resolved to
	// its URL, never reported as one.
	close(srv.paths)
	got := ""
	for p := range srv.paths {
		got += p + ","
	}
	want := "/wp-json/wp/v2/settings,/wp-json/wp/v2/media/42,"
	if got != want {
		t.Fatalf("requested %s, want %s", got, want)
	}
}

func TestProbeSendsBasicAuthOverTheURL(t *testing.T) {
	var seenUser, seenPass string
	var ok bool
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		seenUser, seenPass, ok = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"title":"T","site_icon":0}`)
	})
	if _, err := client.Probe(context.Background(), siteURL, "editor", "pass word 1234"); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !ok || seenUser != "editor" || seenPass != "pass word 1234" {
		t.Fatalf("BasicAuth = %q/%q/%v", seenUser, seenPass, ok)
	}
}

func TestProbeLeavesNoIconWhenTheSiteHasNone(t *testing.T) {
	client, _ := newSite(t, wordpressHandler(t, "admin", "pw", 0, ""))
	info, err := client.Probe(context.Background(), siteURL, "admin", "pw")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.SiteIconURL != nil {
		t.Fatalf("SiteIconURL = %v, want nil", *info.SiteIconURL)
	}
}

func TestProbeKeepsTheTitleWhenTheIconCannotBeResolved(t *testing.T) {
	// A site whose icon attachment is refused (private media, a broken
	// plugin answer) is still a WordPress site; the icon is optional.
	client, _ := newSite(t, wordpressHandler(t, "admin", "pw", 9, ""))
	info, err := client.Probe(context.Background(), siteURL, "admin", "pw")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.Title != "My Test Site" {
		t.Fatalf("Title = %q", info.Title)
	}
	if info.SiteIconURL != nil {
		t.Fatalf("SiteIconURL = %v, want nil", *info.SiteIconURL)
	}
}

func TestProbeIgnoresAnIconURLThatIsNotAPublicHTTPSURL(t *testing.T) {
	// source_url is sent by the remote site and ends up in the management UI;
	// it must be a plain https URL on a public hostname.
	for name, bad := range map[string]string{
		"http":       "http://example.com/icon.png",
		"file":       "file:///etc/passwd",
		"relative":   "/wp-content/icon.png",
		"private ip": "https://10.0.0.5/icon.png",
		"loopback":   "https://127.0.0.1/icon.png",
		"internal":   "https://metadata.google.internal/icon.png",
		"userinfo":   "https://u:p@example.com/icon.png",
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := newSite(t, wordpressHandler(t, "admin", "pw", 9, bad))
			info, err := client.Probe(context.Background(), siteURL, "admin", "pw")
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if info.SiteIconURL != nil {
				t.Fatalf("SiteIconURL = %v, want nil", *info.SiteIconURL)
			}
		})
	}
}

func TestProbeReportsBadCredentials(t *testing.T) {
	client, _ := newSite(t, wordpressHandler(t, "admin", "right", 0, ""))
	_, err := client.Probe(context.Background(), siteURL, "admin", "wrong")
	if !errors.Is(err, wordpress.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), "right") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error leaks a credential: %v", err)
	}
}

func TestProbeReportsInsufficientPermissions(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"rest_forbidden","message":"Sorry, you are not allowed to do that.","data":{"status":403}}`)
	})
	_, err := client.Probe(context.Background(), siteURL, "subscriber", "pw")
	if !errors.Is(err, wordpress.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if !strings.Contains(err.Error(), "manage_options") {
		t.Fatalf("the error does not say what is missing: %v", err)
	}
}

func TestProbeReportsASiteThatIsNotWordPress(t *testing.T) {
	for name, content := range map[string]string{
		"404 html":     "<html><body>Not here</body></html>",
		"rest is off":  `{"code":"rest_no_route"}`,
		"missing perm": `{"code":"rest_cannot_access"}`,
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, content)
			})
			_, err := client.Probe(context.Background(), siteURL, "admin", "pw")
			if !errors.Is(err, wordpress.ErrIncompatible) {
				t.Fatalf("err = %v, want ErrIncompatible", err)
			}
		})
	}
}

func TestProbeReportsAnHTMLLoginPageAsIncompatible(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = io.WriteString(w, "<html><body>Please log in</body></html>")
	})
	_, err := client.Probe(context.Background(), siteURL, "admin", "pw")
	if !errors.Is(err, wordpress.ErrIncompatible) {
		t.Fatalf("err = %v, want ErrIncompatible", err)
	}
	if !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("the error does not explain the mismatch: %v", err)
	}
}

func TestProbeReportsACompatibleSiteThatAnswersWrong(t *testing.T) {
	cases := map[string]string{
		"JSON but no title": `{"site_icon": 0}`,
		"title is a number": `{"title": 5}`,
		"not an object":     `[]`,
		"truncated":         `{"title": "T"`,
		"two objects":       `{"title":"T"}{"title":"U"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			})
			_, err := client.Probe(context.Background(), siteURL, "admin", "pw")
			if !errors.Is(err, wordpress.ErrIncompatible) {
				t.Fatalf("err = %v, want ErrIncompatible", err)
			}
		})
	}
}

func TestProbeReportsAnEmptyTitle(t *testing.T) {
	// A WordPress site with no title is valid; the record then has an empty
	// title rather than an invented one.
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"title":"","site_icon":"17"}`)
	})
	info, err := client.Probe(context.Background(), siteURL, "admin", "pw")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.Title != "" {
		t.Fatalf("Title = %q", info.Title)
	}
}

func TestProbeReadsTheIconOnlyFromAnAttachmentID(t *testing.T) {
	cases := map[string]struct {
		body  string
		media string
		want  *string
	}{
		"numeric id resolved": {`{"title":"T","site_icon":7}`,
			`{"id":7,"source_url":"https://example.com/i.png"}`, strPtr("https://example.com/i.png")},
		"numeric string id resolved": {`{"title":"T","site_icon":"7"}`,
			`{"id":7,"source_url":"https://example.com/i.png"}`, strPtr("https://example.com/i.png")},
		"zero means none":                {`{"title":"T","site_icon":0}`, ``, nil},
		"false means none":               {`{"title":"T","site_icon":false}`, ``, nil},
		"empty string means none":        {`{"title":"T","site_icon":""}`, ``, nil},
		"a url is not an attachment id":  {`{"title":"T","site_icon":"https://example.com/i.png"}`, ``, nil},
		"an object is not an attachment": {`{"title":"T","site_icon":{"id":7}}`, ``, nil},
		"media without source_url":       {`{"title":"T","site_icon":7}`, `{"id":7}`, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			media := c.media
			client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasPrefix(r.URL.Path, "/wp-json/wp/v2/media/") && media != "" {
					_, _ = io.WriteString(w, media)
					return
				}
				_, _ = io.WriteString(w, c.body)
			})
			info, err := client.Probe(context.Background(), siteURL, "admin", "pw")
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			switch {
			case c.want == nil && info.SiteIconURL != nil:
				t.Fatalf("SiteIconURL = %v, want nil", *info.SiteIconURL)
			case c.want != nil && (info.SiteIconURL == nil || *info.SiteIconURL != *c.want):
				t.Fatalf("SiteIconURL = %v, want %q", info.SiteIconURL, *c.want)
			}
		})
	}
}

func TestProbeReportsAtLeastFourHundredAsNotWordPress(t *testing.T) {
	// A site that answers GET wp/v2/settings with 404 (REST API off), or 500
	// (a broken site), is far more often "not a compatible site" - so a 4xx is
	// never taken as a credential failure it was not.
	for _, status := range []int{
		http.StatusBadRequest, http.StatusConflict, http.StatusTeapot, http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{}`)
			})
			_, err := client.Probe(context.Background(), siteURL, "admin", "pw")
			if !errors.Is(err, wordpress.ErrIncompatible) {
				t.Fatalf("status %d: err = %v, want ErrIncompatible", status, err)
			}
		})
	}
}

func TestProbeDoesNotFetchAnIconURLAsOne(t *testing.T) {
	// A settings document whose site_icon is a URL must not make the daemon
	// fetch that URL: only the media route of the same site is ever asked.
	var fetched []string
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		fetched = append(fetched, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"title":"T","site_icon":"https://elsewhere.example.com/tracker.png"}`)
	})
	if _, err := client.Probe(context.Background(), siteURL, "admin", "pw"); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(fetched) != 1 || fetched[0] != "/wp-json/wp/v2/settings" {
		t.Fatalf("fetched %v, want only the settings document", fetched)
	}
}

func TestProbeTimesOut(t *testing.T) {
	release := make(chan struct{})
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}, wordpress.WithTimeout(150*time.Millisecond))
	t.Cleanup(func() { close(release) })

	start := time.Now()
	_, err := client.Probe(context.Background(), siteURL, "admin", "pw")
	if !errors.Is(err, wordpress.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Probe took %s", elapsed)
	}
}

func TestProbeRefusesAnOversizedBody(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"title":"`+strings.Repeat("a", 4096)+`"}`)
	}, wordpress.WithMaxBody(1024))
	_, err := client.Probe(context.Background(), siteURL, "admin", "pw")
	if !errors.Is(err, wordpress.ErrIncompatible) {
		t.Fatalf("err = %v, want ErrIncompatible", err)
	}
}

func TestProbeReportsAnUnreachableSite(t *testing.T) {
	rt := &pipeTransport{t: t, refuse: true}
	client := wordpress.NewClient(wordpress.WithTransport(rt), wordpress.WithTimeout(2*time.Second))
	_, err := client.Probe(context.Background(), siteURL, "admin", "pw")
	if !errors.Is(err, wordpress.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestProbeFollowsNoRedirect(t *testing.T) {
	targetHits := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/elsewhere") {
			targetHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"title":"Redirected"}`)
			return
		}
		http.Redirect(w, r, "/elsewhere/wp-json/wp/v2/settings", http.StatusFound)
	}
	client, _ := newSite(t, handler)
	_, err := client.Probe(context.Background(), siteURL, "admin", "pw")
	if !errors.Is(err, wordpress.ErrIncompatible) {
		t.Fatalf("err = %v, want ErrIncompatible", err)
	}
	if targetHits != 0 {
		t.Fatalf("the redirect target was fetched %d times", targetHits)
	}
}

func TestProbeRequiresHTTPSInProduction(t *testing.T) {
	_, srv := newSite(t, wordpressHandler(t, "admin", "pw", 0, ""))
	production := wordpress.NewClient(wordpress.WithTransport(&pipeTransport{t: t, handler: srv.handler()}))
	_, err := production.Probe(context.Background(), "http://site.example.com", "admin", "pw")
	if !errors.Is(err, wordpress.ErrInvalidURL) {
		t.Fatalf("err = %v, want ErrInvalidURL", err)
	}
	if srv.hits.Load() != 0 {
		t.Fatal("a plain-http URL reached the server")
	}
}

func TestProbeUsesTheSitePathPrefix(t *testing.T) {
	client, _ := newSite(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/blog/wp-json/wp/v2/settings" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"title":"Subdir","site_icon":0}`)
	})
	info, err := client.Probe(context.Background(), siteURL+"/blog", "admin", "pw")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.Title != "Subdir" {
		t.Fatalf("Title = %q", info.Title)
	}
}

func strPtr(s string) *string { return &s }
