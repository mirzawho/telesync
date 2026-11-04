package apidocs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
)

// Routes is the API's route table: what the gateway serves, with the methods
// it serves at each path.
//
// The gateway routes by hand on the path segments, so its routes cannot be
// enumerated from Go's request patterns. This table is the one place that names
// them, and three checks keep it honest:
//
//   - TestRouteTableMatchesGateway replays every entry against the real
//     handler: each listed method must be accepted, no unlisted method at a
//     probed path may be, and a misspelled resource must be refused.
//   - TestDocumentationCoversEveryRoute requires every entry to be documented
//     in a generated page (or to match such a page), so a route added to the
//     table without a page fails the build.
//   - TestEveryDocumentedRouteIsServed goes the other way: every route the
//     generated pages mention must be in this table, so a removed route cannot
//     linger in the documentation.
var Routes = gatewayRoutes

// gatewayRoutes is the API surface the gateway routes by hand.
var gatewayRoutes = []Route{
	{Pattern: "/healthz", Methods: []string{http.MethodGet}},

	{Pattern: "/docs", Methods: []string{http.MethodGet}},
	{Pattern: "/docs/{path}", Methods: []string{http.MethodGet},
		Skip: "every document path is the same route; a concrete probe file would have to exist in the rendered tree"},

	{Pattern: "/api/v1/auth/login", Methods: []string{http.MethodPost}},
	{Pattern: "/api/v1/auth/me", Methods: []string{http.MethodGet}},
	{Pattern: "/api/v1/auth/password", Methods: []string{http.MethodPost}},

	{Pattern: "/api/v1/telegram", Methods: []string{http.MethodGet}},

	{Pattern: "/api/v1/telegram/{key}/health", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/profile", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/channels", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/channels/find", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/channels/{channel_id}", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/channels/{channel_id}/messages", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}/media", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/channels/{channel_id}/avatar", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/custom_emoji/{document_id}", Methods: []string{http.MethodGet}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/channels/{channel_id}/join", Methods: []string{http.MethodPost}, Probe: telegramKey},
	{Pattern: "/api/v1/telegram/{key}/messages", Methods: []string{http.MethodPost}, Probe: telegramKey},

	{Pattern: "/api/v1/local/channels", Methods: []string{http.MethodGet}},
	{Pattern: "/api/v1/local/channels/{id}", Methods: []string{http.MethodGet}},
	{Pattern: "/api/v1/local/channels/{id}/messages", Methods: []string{http.MethodGet}},
	{Pattern: "/api/v1/local/channels/{id}/avatar", Methods: []string{http.MethodGet}},
	{Pattern: "/api/v1/local/channels/{id}/leave", Methods: []string{http.MethodPost}},
	{Pattern: "/api/v1/local/channels/{id}/categories", Methods: []string{http.MethodGet, http.MethodPut}},

	{Pattern: "/api/v1/local/categories", Methods: []string{http.MethodGet, http.MethodPost}},
	{Pattern: "/api/v1/local/categories/{id}", Methods: []string{http.MethodGet, http.MethodPut, http.MethodDelete}},
	{Pattern: "/api/v1/local/categories/{id}/channels", Methods: []string{http.MethodGet}},

	{Pattern: "/api/v1/local/wordpress", Methods: []string{http.MethodGet, http.MethodPost}},
	{Pattern: "/api/v1/local/wordpress/{id}", Methods: []string{http.MethodGet, http.MethodPatch, http.MethodDelete}},
	{Pattern: "/api/v1/local/wordpress/{id}/posts", Methods: []string{http.MethodPost}},
	// The two taxonomies a publishing form needs: the destination site's own
	// categories and tags, read through this backend rather than by the browser
	// calling the site with a credential of its own.
	{Pattern: "/api/v1/local/wordpress/{id}/categories", Methods: []string{http.MethodGet}},
	{Pattern: "/api/v1/local/wordpress/{id}/tags", Methods: []string{http.MethodGet}},

	{Pattern: "/api/v1/local/users", Methods: []string{http.MethodGet, http.MethodPost}},
	{Pattern: "/api/v1/local/users/{id}", Methods: []string{http.MethodGet, http.MethodPut, http.MethodDelete}},
	{Pattern: "/api/v1/local/users/{id}/permissions", Methods: []string{http.MethodGet, http.MethodPut}},

	{Pattern: "/api/v1/local/stats/dashboard", Methods: []string{http.MethodGet}},
}

// Prober sends requests to the gateway the way the checks do.
//
// Every credential is a snapshot taken before the request is sent, so a request
// that changes the account it runs as (deactivating it, replacing its
// permissions, deleting it) cannot alter what a later request in the same pass
// sees.
type Prober interface {
	// Do sends one request. token is the bearer credential, or empty for none.
	// body is sent as application/json when non-empty.
	Do(ctx context.Context, method, path, token, body string) *httptest.ResponseRecorder
	// Anonymous is a caller with no credential.
	Anonymous() Credential
	// Operator is the fixed token holding every permission, or nil when the
	// deployment has none configured. It has no account, so account routes
	// refuse it.
	Operator() (Credential, bool)
	// Administrator is an active account holding every global permission.
	Administrator() Credential
	// Limited is an active account whose permission set is set by put: the
	// body of a PUT /api/v1/local/users/{id}/permissions request.
	Limited(put func(admin Credential) string) Credential
}

// Credential is one caller, frozen at capture.
type Credential interface {
	// Authorization is the Authorization header value, or "" for anonymous.
	Authorization() string
	// Snapshot reports whether the credential is still the caller it was
	// captured as: the session under the same id, or the operator token.
	Snapshot(ctx context.Context) bool
	// Request is the request that received this credential, when the probe
	// knows it. It is the evidence an unresolved note quotes.
	Request() string
	// ID names the caller in the report.
	ID() string
}

// NewRequestID returns a random id for a request that must not collide with one
// in the examples.
func NewRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "probe"
	}
	return "probe-" + hex.EncodeToString(buf)
}
