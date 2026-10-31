package wordpress

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Defaults for one request to a site.
const (
	DefaultTimeout = 10 * time.Second
	DefaultMaxBody = 1 << 20
	dialTimeout    = 5 * time.Second
)

// Errors a caller matches with errors.Is.
var (
	ErrInvalidURL     = errors.New("wordpress: invalid site url")
	ErrBlockedAddress = errors.New("wordpress: refused to connect to a non-public address")
	ErrUnreachable    = errors.New("wordpress: the site is unreachable")
	ErrUnauthorized   = errors.New("wordpress: the username or password was refused")
	ErrForbidden      = errors.New("wordpress: the account lacks permission")
	ErrIncompatible   = errors.New("wordpress: the site did not answer as a WordPress REST API")
	ErrTimeout        = errors.New("wordpress: the site did not answer in time")
)

// SiteInfo is what a probe reads from a site.
type SiteInfo struct {
	Title string
	// SiteIconURL is nil when the site has no icon or it could not be
	// resolved. It is never the attachment id.
	SiteIconURL *string
}

// Client talks to WordPress sites.
type Client struct {
	policy   Policy
	timeout  time.Duration
	maxBody  int64
	tcp      dialFunc
	resolver *net.Resolver
	// transport, when set, replaces the one httpClient would build (tests).
	transport http.RoundTripper
}

// Option configures a Client.
type Option func(*Client)

// WithPolicy uses another URL and address policy. Only tests pass one.
func WithPolicy(p Policy) Option { return func(c *Client) { c.policy = p } }

// WithTimeout bounds one request to a site.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithMaxBody bounds a response body.
func WithMaxBody(n int64) Option { return func(c *Client) { c.maxBody = n } }

// WithTransport replaces the HTTP transport. The package's tests and the
// gateway's use it: there the site is a scripted handler rather than a server,
// because a test must not bind a port or reach the network.
func WithTransport(rt http.RoundTripper) Option { return func(c *Client) { c.transport = rt } }

// NewClient builds a client. Without options it is the production one: https
// only, public addresses only, a 10s timeout and 1 MiB response bodies.
func NewClient(opts ...Option) *Client {
	c := &Client{
		timeout:  DefaultTimeout,
		maxBody:  DefaultMaxBody,
		resolver: net.DefaultResolver,
	}
	c.tcp = (&net.Dialer{Timeout: dialTimeout}).DialContext
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Probe authenticates against baseURL and reads the site's title and icon.
//
// It calls GET {baseURL}/wp-json/wp/v2/settings with HTTP Basic authentication,
// then resolves the numeric site_icon attachment id through
// GET {baseURL}/wp-json/wp/v2/media/{id}. No redirect is followed and no
// request is ever retried or sent anonymously: a refusal is reported as it is.
func (c *Client) Probe(ctx context.Context, baseURL, username, password string) (SiteInfo, error) {
	base, err := NormalizeBaseURL(baseURL, c.policy)
	if err != nil {
		return SiteInfo{}, err
	}
	if username == "" {
		return SiteInfo{}, errors.New("wordpress: the username is empty")
	}

	httpClient := c.httpClient()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var settings settingsReply
	if err := c.getJSON(ctx, httpClient, base+"/wp-json/wp/v2/settings", username, password, &settings); err != nil {
		return SiteInfo{}, err
	}
	// httpClient builds a new transport per Probe; its idle connections would
	// otherwise outlive the call.
	httpClient.CloseIdleConnections()

	info := SiteInfo{Title: settings.Title}
	if id, ok := settings.IconAttachmentID(); ok {
		// The icon is optional: a site whose attachment cannot be read is
		// still a site. Only the settings document decides compatibility.
		var media mediaReply
		if err := c.getJSON(ctx, httpClient, base+"/wp-json/wp/v2/media/"+strconv.FormatInt(id, 10), username, password, &media); err == nil {
			if icon, ok := publicURL(media.SourceURL); ok {
				info.SiteIconURL = &icon
			}
		}
	}
	return info, nil
}

// httpClient is the one client every request uses.
//
// Redirects are refused rather than followed-and-checked: the WordPress routes
// never redirect, so a 3xx is either a misconfigured site or an attempt to make
// the daemon fetch something else.
func (c *Client) httpClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           safeDial(c.tcp, c.resolver, c.policy),
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: c.timeout,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}
	if c.transport == nil {
		c.transport = transport
	}
	return &http.Client{
		Transport: c.transport,
		Timeout:   c.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// CloseIdleConnections releases this package's pooled connections. The daemon
// calls nothing; a test uses it so its in-memory transport ends with it.
func (c *Client) CloseIdleConnections() {
	if t, ok := c.transport.(interface{ CloseIdleConnections() }); ok {
		t.CloseIdleConnections()
	}
}

// settingsReply is the part of wp/v2/settings this package reads.
type settingsReply struct {
	Title string          `json:"title"`
	Icon  json.RawMessage `json:"site_icon"`
}

// IconAttachmentID reads site_icon as an attachment id.
//
// WordPress sends 0 or false for "no icon", the id as a number, or (with a
// plugin altering the schema) something else entirely. Only an id is accepted:
// a value that looks like a URL is not one, and reporting it as an image URL
// would show a broken picture.
func (s settingsReply) IconAttachmentID() (int64, bool) {
	raw := strings.TrimSpace(string(s.Icon))
	if raw == "" || raw == "null" || raw == "false" || raw == "0" || raw == `""` {
		return 0, false
	}
	var decoded any
	if err := json.Unmarshal(s.Icon, &decoded); err != nil {
		return 0, false
	}
	switch v := decoded.(type) {
	case float64:
		if v > 0 && v == float64(int64(v)) {
			return int64(v), true
		}
	case string:
		if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && id > 0 {
			return id, true
		}
	}
	return 0, false
}

// mediaReply is the part of a media item this package reads.
type mediaReply struct {
	SourceURL string `json:"source_url"`
}

// publicURL accepts an icon URL only when it is a plain https address on a
// public hostname: the value comes from the remote site and ends up in the
// management UI, so it goes through the same host rules as a site URL.
func publicURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return "", false
	}
	if err := validateHost(strings.ToLower(u.Hostname()), Policy{allowHTTP: true}); err != nil {
		return "", false
	}
	if ipLiteral.MatchString(u.Hostname()) {
		return "", false
	}
	return raw, true
}

// getJSON performs one authenticated GET and decodes a JSON object.
func (c *Client) getJSON(ctx context.Context, client *http.Client, target, username, password string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "telesync")

	resp, err := client.Do(req)
	if err != nil {
		return describeRequestError(ctx, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		if isTimeout(err) {
			return ErrTimeout
		}
		return fmt.Errorf("%w: reading the answer failed", ErrUnreachable)
	}
	if int64(len(body)) > c.maxBody {
		return fmt.Errorf("%w: the answer is larger than %d bytes", ErrIncompatible, c.maxBody)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: reading the site settings needs a user with the manage_options "+
			"capability (an Administrator, or a role that was granted it)", ErrForbidden)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Errorf("%w: it answered with a redirect (HTTP %d) instead of the REST API",
			ErrIncompatible, resp.StatusCode)
	default:
		return fmt.Errorf("%w: it answered HTTP %d for %s", ErrIncompatible, resp.StatusCode, requestTarget(target))
	}

	if err := checkJSON(resp.Header.Get("Content-Type"), resp.StatusCode, requestTarget(target)); err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: the answer is not the expected JSON object", ErrIncompatible)
	}
	if dec.More() {
		return fmt.Errorf("%w: the answer has more than one JSON value", ErrIncompatible)
	}
	// An empty object would decode into every reply type; the required field
	// is what makes it a settings document.
	if settings, ok := out.(*settingsReply); ok && settings.Title == "" {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(body, &probe); err != nil {
			return fmt.Errorf("%w: the answer is not a JSON object", ErrIncompatible)
		}
		if _, has := probe["title"]; !has {
			return fmt.Errorf("%w: the answer has no title", ErrIncompatible)
		}
	}
	return nil
}

// requestTarget keeps the fixed API path in an error and drops the host.
func requestTarget(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return "its REST API"
	}
	return u.Path
}

// checkJSON decides whether a 2xx answer is a JSON document at all.
//
// The media type is checked when the site declares one; a site whose server
// omits it is decoded instead of refused, and the decode is what proves it.
func checkJSON(mediaType string, status int, target string) error {
	mediaType = contentTypeOf(mediaType)
	if mediaType == "" || strings.Contains(strings.ToLower(mediaType), "json") {
		return nil
	}
	if strings.HasPrefix(mediaType, "text/html") {
		return fmt.Errorf("%w: it answered HTTP %d with an HTML page for %s, so the REST API is "+
			"not reachable there (are permalinks enabled, or is the site behind a login page?)",
			ErrIncompatible, status, target)
	}
	return fmt.Errorf("%w: it answered %q, not JSON", ErrIncompatible, mediaType)
}

func contentTypeOf(mediaType string) string {
	if mediaType == "" {
		return ""
	}
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.TrimSpace(mediaType)
}

// describeRequestError maps a transport failure.
//
// The error names the site's host and what happened; it never carries the
// request, which holds the credential.
func describeRequestError(ctx context.Context, err error) error {
	if errors.Is(err, ErrBlockedAddress) {
		return err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || isTimeout(err) {
		return ErrTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("%w: %s could not be resolved", ErrUnreachable, dnsErr.Name)
	}
	var tlsErr *tls.CertificateVerificationError
	if errors.As(err, &tlsErr) {
		return fmt.Errorf("%w: its TLS certificate could not be verified", ErrUnreachable)
	}
	return fmt.Errorf("%w: the connection failed", ErrUnreachable)
}
