// Package gateway serves the public HTTP API and routes each request to the
// worker that owns the requested Telegram client.
//
// Routing is configuration-driven: the gateway holds a registry of client keys,
// and it never falls back to another client when the requested one is
// unavailable.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// Worker is the upstream operation set for one configured client.
type Worker interface {
	Health(ctx context.Context) (rpc.Health, error)
	Profile(ctx context.Context) (rpc.Profile, error)
	Channels(ctx context.Context, page, perPage int) (rpc.ChannelPage, error)
	Messages(ctx context.Context, channelID int64, page, perPage int) (rpc.MessagePage, error)
	Message(ctx context.Context, channelID int64, messageID int) (rpc.MessagePage, error)
	Media(ctx context.Context, channelID int64, messageID, photoIndex int, slice rpc.MediaSlice) (rpc.MediaStream, error)
	// CustomEmoji fetches the bytes of one reaction custom emoji.
	CustomEmoji(ctx context.Context, documentID int64) (rpc.MediaFile, error)

	// Find resolves a discovery query against this client's Telegram account.
	Find(ctx context.Context, req rpc.FindRequest) (rpc.FindResult, error)
	// Channel resolves one channel to its current Telegram state.
	Channel(ctx context.Context, ref rpc.ChannelRef) (rpc.ChannelResult, error)
	// Join performs the Telegram membership operation.
	Join(ctx context.Context, ref rpc.ChannelRef) (rpc.JoinResult, error)
	// Avatar downloads a profile photo.
	Avatar(ctx context.Context, ref rpc.ChannelRef) (rpc.AvatarFile, error)
	// SendMessage sends one prepared post to a channel this account can post
	// in. It is the only write to Telegram's content the gateway performs, and
	// it is reached only from the publishing route.
	SendMessage(ctx context.Context, req rpc.SendRequest) (rpc.SendResult, error)
}

// Registry maps configured client keys to their workers.
type Registry interface {
	Worker(key string) (Worker, bool)
}

// Config configures the gateway handler.
type Config struct {
	Workers Registry
	// Servers lists the Telegram clients GET /api/v1/telegram may report. Only
	// entries with Enabled are listed; Registered says whether a worker serves
	// the client, so its health can be asked for. Nil lists nothing.
	Servers []ServerInfo
	// RequestTimeout bounds one upstream call. Zero means 30s.
	RequestTimeout time.Duration
	// Channels is the local managed-channel store. Nil disables join, and the
	// avatar then streams without a persistent cache.
	Channels ManagedStore
	// Avatars is the persistent avatar cache. Nil serves avatars directly.
	Avatars AvatarCache
	// Categories is the local category store. Nil answers the local category
	// API with 503; the Telegram routes are unaffected.
	Categories CategoryStore
	// Users is the local account store. Nil disables login and answers the
	// local user API with 503.
	Users UserStore
	// Tokens signs and verifies access tokens. Nil disables login.
	Tokens Tokens
	// Operator, when set, is the operator's fixed access token, accepted only
	// on a loopback listener. It authenticates as a caller holding every
	// permission but is not a user account.
	Operator string
	// BasePath is the prefix the handler is mounted under, such as "/admin".
	// The panel's links are absolute paths, so without it they would leave the
	// mount; with it, every path the gateway redirects to or renders for the
	// browser (login URL, documentation links) stays under the prefix.
	BasePath string
	// Now overrides the clock (tests).
	Now func() time.Time
	// Panel, when set, serves the admin panel's static build under BasePath:
	// its assets as files, and its index.html for every other GET that asks
	// for a document. Nil leaves the panel answers to the API's 404.
	Panel http.Handler
	// Docs, when set, renders the Markdown documentation site at /docs. The
	// daemon builds it from the documentation directory; nil leaves /docs
	// answering 404.
	Docs http.Handler
	// OnCreated, when set, is told the kind and local id of every resource the
	// API creates. The daemon does not use it; tests do, to model the "the
	// creator gets access" rule the category contract tests rely on.
	OnCreated func(kind channelstore.ResourceKind, id int64)
	// WordPress is the local WordPress site store. Nil answers the WordPress
	// routes with 503.
	WordPress WordPressStore
	// WordPressClient makes the authenticated request that validates a site.
	// Nil answers the WordPress routes with 503.
	WordPressClient WordPressClient
	// Secrets seals and opens site credentials. Nil answers the WordPress
	// routes with 503: without a key no credential can be stored safely.
	Secrets SecretBox
	// Publishes records publishing attempts so a retry with the same
	// Idempotency-Key cannot publish twice. Nil lets a request without such a
	// key through and answers a request with one with 503.
	Publishes PublishStore
	// Logf receives one line per request failure. Nil discards.
	Logf func(format string, args ...any)
}

// Error codes returned to API clients.
const (
	CodeInvalidInput     = "invalid_input"
	CodeUnknownClient    = "unknown_client"
	CodeUnauthorized     = "unauthorized"
	CodeNeedsAuth        = "needs_auth"
	CodeNotConnected     = "not_connected"
	CodeChannelNotFound  = "channel_not_found"
	CodePeerUnresolved   = "peer_unresolved"
	CodeRateLimited      = "rate_limited"
	CodeWorkerUnavail    = "worker_unavailable"
	CodeWorkerTimeout    = "worker_timeout"
	CodeWorkerError      = "worker_error"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeStoreError       = "store_error"
	// WordPress site registration failures.
	CodeWordPressAuth         = "wordpress_unauthorized"
	CodeWordPressForbidden    = "wordpress_forbidden"
	CodeWordPressUnreachable  = "wordpress_unreachable"
	CodeWordPressIncompatible = "wordpress_incompatible"
	CodeWordPressTimeout      = "wordpress_timeout"
	CodeWordPressBlocked      = "wordpress_blocked_address"
	CodeDuplicateSite         = "duplicate_site"
	// Publishing failures. The prefix names the operation, so a client can
	// tell a refused publish from an ordinary not-found.
	CodePublishForbidden     = "publish_forbidden"
	CodePublishSourceMissing = "publish_source_unavailable"
	CodePublishSourceDenied  = "publish_source_forbidden"
	CodePublishDestMissing   = "publish_destination_unavailable"
	CodePublishDestDenied    = "publish_destination_forbidden"
	CodePublishDenied        = "publish_permission_denied"
	CodePublishUnsupported   = "publish_unsupported"
	CodePublishMediaTooLarge = "publish_media_too_large"
	CodePublishConflict      = "publish_idempotency_conflict"
	CodePublishUnknown       = "publish_outcome_unknown"
	CodePublishFailed        = "publish_failed"
	CodePublishPartial       = "publish_partial_failure"
	// CodePublishSourceMedia is the source attachment failing to be read. It is
	// its own stage: nothing was sent anywhere, so the draft is intact and the
	// caller may drop the attachment and publish the text alone.
	CodePublishSourceMedia = "publish_source_media_unavailable"
	// CodePublishMediaUpload is the destination refusing a post that carried an
	// attachment which had already been read successfully. The opposite stage
	// from the one above, and never interchangeable with it.
	CodePublishMediaUpload   = "publish_media_upload_failed"
	CodePublishWordPressAuth = "publish_wordpress_auth_failed"

	CodePublishWordPressForbidden    = "publish_wordpress_forbidden"
	CodePublishWordPressTimeout      = "publish_wordpress_timeout"
	CodePublishWordPressUnreachable  = "publish_wordpress_unreachable"
	CodePublishWordPressIncompatible = "publish_wordpress_incompatible"
)

// Error is the documented error body.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

type errorEnvelope struct {
	Error Error `json:"error"`
}

type envelope struct {
	Data any `json:"data"`
	Meta any `json:"meta"`
}

type Server struct {
	cfg Config
}

// New builds the public HTTP handler.
func New(cfg Config) http.Handler {
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Server{cfg: cfg}
}

// paths is the routing prefix; the client key follows it.
const paths = "/api/v1/telegram/"

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFrom(r)
	w.Header().Set("X-Request-Id", requestID)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// The API is the only part of this server that checks a credential. Every
	// page — the panel, the documentation, the health check — is served to
	// whoever asks: none of them is account data, and the login page has to be
	// reachable before there is a caller at all.
	p := strings.TrimPrefix(r.URL.Path, s.cfg.BasePath)

	switch {
	case p == "/healthz":
		s.gatewayHealth(w, r, requestID)
		return
	case p == docsPath || strings.HasPrefix(p, docsPath+"/"):
		// A document group without its trailing slash is the same page, and
		// with one every relative link beside it resolves from the group's
		// directory rather than from its parent.
		if r.Method == http.MethodGet && !strings.HasSuffix(p, "/") && s.docsGroup(p) {
			target := s.fullPath(p) + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		s.docs(w, r, requestID)
		return
	case p == loginPath:
		s.login(w, r, requestID)
		return
	case underPath(p, "/api"):
		s.serveAPI(w, r, requestID)
		return
	}

	// Everything else under the mount is the panel: one page for its routes,
	// its files for the rest. A request outside it finds nothing.
	if s.servePanel(w, r, requestID) {
		return
	}
	s.notFound(w, requestID)
}

// serveAPI is the JSON API under the mount, and the only place a credential is
// checked.
func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request, requestID string) {
	path := strings.TrimPrefix(r.URL.Path, s.cfg.BasePath)

	switch {
	case path == serversPath:
		// One listing for the selector and the dashboard's status card; see
		// servers.go.
		s.requireAuth(s.servers)(w, r)
		return
	case path == mePath:
		s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			s.me(w, r, requestID)
		})(w, r)
		return
	case path == changePasswordPath:
		s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			s.changePassword(w, r, requestID)
		})(w, r)
		return
	case underPath(path, categoriesPrefix) || underPath(path, usersPrefix) ||
		underPath(path, localChannelsPrefix) || underPath(path, wordpressPrefix) ||
		underPath(path, statsPrefix):
		// Local users, categories, channels and the dashboard's counts are this
		// application's data, not any one Telegram account's.
		if underPath(path, statsPrefix) && s.missingStatsStores() {
			// The dashboard's answer is a count over these very stores, so a
			// deployment that has none is told before its credential is read:
			// with no account store no credential can be resolved at all, and
			// there is no count behind the route to keep secret.
			s.statsUnconfigured(w, requestID)
			return
		}
		s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			s.serveLocal(w, r, requestID)
		})(w, r)
		return
	case strings.HasPrefix(path, paths):
		s.serveTelegram(w, r, requestID)
		return
	}

	// An unknown API path is 404 — for a caller, and for one with no
	// credential too: there is nothing behind it to keep secret.
	s.notFound(w, requestID)
}

// serveTelegram routes one /api/v1/telegram/{key}/... request to its worker.
//
// The caller is resolved before anything that varies by path: an
// unauthenticated request must not be able to tell a configured client key from
// an unknown one.
func (s *Server) serveTelegram(w http.ResponseWriter, r *http.Request, requestID string) {
	a, err := s.authenticate(r)
	if err != nil {
		s.authFailure(w, err, requestID)
		return
	}
	r = withActor(r, a)

	rest, _ := strings.CutPrefix(r.URL.Path, s.fullPath(paths))
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	if len(segments) < 2 || segments[0] == "" {
		s.notFound(w, requestID)
		return
	}
	key, resource := segments[0], segments[1]

	worker, ok := s.cfg.Workers.Worker(key)
	if !ok {
		s.fail(w, http.StatusNotFound, CodeUnknownClient,
			"No configured Telegram client has that key.", requestID)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	s.routeTelegram(ctx, w, r, key, worker, resource, segments, requestID)
}

// routeTelegram is the per-resource part of the Telegram routes.
func (s *Server) routeTelegram(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key string, worker Worker, resource string, segments []string, requestID string) {
	// join and publish are the only state-changing routes. Everything else is
	// read-only and answers 405 to anything but GET.
	if resource == "messages" && len(segments) == 2 {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Publishing changes state and must be sent as POST.", requestID)
			return
		}
		s.publishTelegram(ctx, w, r, key, worker, requestID)
		return
	}
	if resource == "channels" && len(segments) == 4 && segments[3] == "join" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Joining changes state and must be sent as POST.", requestID)
			return
		}
		s.join(ctx, w, r, key, worker, segments[2], requestID)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Only GET is supported.", requestID)
		return
	}

	switch resource {
	case "health":
		if len(segments) != 2 {
			s.notFound(w, requestID)
			return
		}
		s.health(ctx, w, key, worker, requestID)
	case "profile":
		if len(segments) != 2 {
			s.notFound(w, requestID)
			return
		}
		s.profile(ctx, w, r, key, worker, requestID)
	case "channels":
		switch len(segments) {
		case 2:
			s.channels(ctx, w, r, key, worker, requestID)
		case 3:
			// /channels/find is discovery, not a channel named "find".
			if segments[2] == "find" {
				s.find(ctx, w, r, key, worker, requestID)
				return
			}
			// /channels/{id} is one channel's current Telegram state.
			s.channel(ctx, w, r, key, worker, segments[2], requestID)
		case 4:
			switch segments[3] {
			case "messages":
				s.messages(ctx, w, r, key, worker, segments[2], requestID)
				return
			case "avatar":
				s.avatar(ctx, w, r, key, worker, segments[2], requestID)
				return
			}
			s.notFound(w, requestID)
		case 5:
			// /channels/{id}/messages/{message_id}
			if segments[3] != "messages" {
				s.notFound(w, requestID)
				return
			}
			s.message(ctx, w, r, key, worker, segments[2], segments[4], requestID)
		case 6:
			// /channels/{id}/messages/{message_id}/media
			if segments[3] != "messages" || segments[5] != "media" {
				s.notFound(w, requestID)
				return
			}
			s.media(ctx, w, r, key, worker, segments[2], segments[4], requestID)
		default:
			s.notFound(w, requestID)
		}
	case "custom_emoji":
		// /custom_emoji/{document_id}: the image behind a reaction. It is not
		// tied to a channel, so it needs no channel permission.
		if len(segments) != 3 {
			s.notFound(w, requestID)
			return
		}
		s.customEmoji(ctx, w, r, key, worker, segments[2], requestID)
	case "channel", "channel_message", "message", "messages":
		// A near miss on the resource name is almost always a typo in the
		// path. Say what the canonical route is instead of a bare 404.
		s.fail(w, http.StatusNotFound, CodeNotFound,
			"Unknown resource "+strconv.Quote(resource)+". "+
				"Use /api/v1/telegram/"+key+"/channels and "+
				"/api/v1/telegram/"+key+"/channels/{channel_id}/messages.", requestID)
	default:
		s.notFound(w, requestID)
	}
}

func (s *Server) gatewayHealth(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Only GET is supported.", requestID)
		return
	}
	// Anonymous: /healthz reveals nothing about the workers or the account, and
	// a page that asked for a credential could not be the first thing an
	// operator checks.
	// Deliberately independent of every worker: a live gateway stays healthy
	// while a Telegram client is down.
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   RFC3339String(s.cfg.Now()),
	})
}

func (s *Server) health(ctx context.Context, w http.ResponseWriter, key string, worker Worker, requestID string) {
	health, err := worker.Health(ctx)
	if err != nil {
		s.upstreamError(w, key, "health", err, requestID)
		return
	}
	data := map[string]any{
		"key":            health.Key,
		"type":           health.Type,
		"state":          string(health.State),
		"authorized":     health.Authorized,
		"transport":      health.Transport,
		"rpc_ready":      health.RPCReady,
		"pid":            health.PID,
		"started_at":     formatTime(health.StartedAt),
		"uptime_seconds": health.UptimeSeconds,
	}
	if health.ThisDC != 0 {
		data["this_dc"] = health.ThisDC
	}
	if health.LastSuccessAt != nil {
		data["last_success_at"] = formatTime(*health.LastSuccessAt)
	}
	if health.LastError != "" {
		data["last_error"] = health.LastError
	}
	writeJSON(w, http.StatusOK, envelope{Data: data, Meta: metaFor(requestID, s.cfg.Now())})
}

func (s *Server) profile(ctx context.Context, w http.ResponseWriter, r *http.Request, key string, worker Worker, requestID string) {
	if !s.allowedAnyChannel(r, channelstore.ActionView) {
		s.forbidden(w, requestID)
		return
	}
	profile, err := worker.Profile(ctx)
	if err != nil {
		s.upstreamError(w, key, "profile", err, requestID)
		return
	}
	data := map[string]any{
		"key":                 profile.Key,
		"type":                profile.Type,
		"authorized":          profile.Authorized,
		"authorization_state": profile.AuthorizationState,
		"user":                nil,
	}
	if profile.User != nil {
		data["user"] = map[string]any{
			"id":         profile.User.ID,
			"first_name": profile.User.FirstName,
			"last_name":  profile.User.LastName,
			"username":   profile.User.Username,
		}
	}
	if profile.ThisDC != 0 {
		data["this_dc"] = profile.ThisDC
	}
	// The custom backend's address is deliberately not returned: it is
	// infrastructure detail, not account identity, and the API may be reachable
	// from elsewhere.
	writeJSON(w, http.StatusOK, envelope{Data: data, Meta: metaFor(requestID, s.cfg.Now())})
}

// channels lists the dialogs the connected Telegram account actually has.
//
// This intentionally does not read the local managed-channel store: the list
// is Telegram's, not the application's selection. Search results and dialogs
// are never recorded as a side effect of being read.
func (s *Server) channels(ctx context.Context, w http.ResponseWriter, r *http.Request, key string, worker Worker, requestID string) {
	page, perPage, err := parsePaging(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	if !s.allowedAnyChannel(r, channelstore.ActionView) {
		s.forbidden(w, requestID)
		return
	}
	result, err := worker.Channels(ctx, page, perPage)
	if err != nil {
		s.upstreamError(w, key, "channels", err, requestID)
		return
	}
	filtered, err := s.applyChannelFilter(ctx, r, key, result)
	if err != nil {
		s.storeError(w, "channels_authorization", err, requestID)
		return
	}
	result = filtered
	result.Channels = applyCapabilityFilter(r.URL.Query(), result.Channels)
	channels := make([]listedChannel, 0, len(result.Channels))
	for _, ch := range result.Channels {
		channels = append(channels, listedChannelOf(key, ch))
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"channels": channels},
		Meta: pageMeta(requestID, s.cfg.Now(), result.Page, true),
	})
}

// listedChannel is a dialog with the permission object the contract requires.
type listedChannel struct {
	rpc.Channel
	Permissions perms `json:"permissions"`
}

// listedChannelOf attaches permissions to a dialog.
//
// The permissions are the ones the worker normalized from Telegram's own
// channel rights, so a dialog and a search hit for the same channel agree.
func listedChannelOf(_ string, ch rpc.Channel) listedChannel {
	return listedChannel{Channel: ch, Permissions: permsOf(ch.Permissions)}
}

func (s *Server) messages(ctx context.Context, w http.ResponseWriter, r *http.Request, key string, worker Worker, rawID, requestID string) {
	channelID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || channelID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "channel_id must be a positive integer.", requestID)
		return
	}
	page, perPage, err := parsePaging(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	if _, ok := s.requireChannelView(ctx, w, r, key, channelID, requestID); !ok {
		return
	}
	result, err := worker.Messages(ctx, channelID, page, perPage)
	if err != nil {
		s.upstreamError(w, key, "messages", err, requestID)
		return
	}
	messages := result.Messages
	if messages == nil {
		messages = []rpc.Message{}
	}
	views := make([]messageView, 0, len(messages))
	for _, msg := range messages {
		views = append(views, messageViewOf(key, channelID, msg))
	}
	// The channel is already in the path, so repeating it in the body would
	// only duplicate what the caller supplied.
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"messages": views},
		Meta: pageMeta(requestID, s.cfg.Now(), result.Page, false),
	})
}

// messageView is the public shape of one message: the worker's fields plus the
// absolute-path URL that fetches its attachment.
type messageView struct {
	rpc.Message
	Media *mediaView `json:"media,omitempty"`
	// Views is Telegram's view count, null when it did not report one.
	Views *int `json:"views"`
	// Reactions is always an array, empty when there are none.
	Reactions []rpc.Reaction `json:"reactions"`
	// Entities is always an array, empty when the message carries no
	// formatting. The spans are in UTF-16 code units, exactly as Telegram
	// reports them.
	Entities []rpc.Entity `json:"entities"`
}

type mediaView struct {
	ID    string `json:"id,omitempty"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
	// Filename is the original name the sender gave the file. It is omitted
	// when Telegram never reported one: an attachment with no name must not
	// grow a name this build invented. It never replaces Label, which is the
	// localised description of the media kind.
	Filename string `json:"filename,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	// URL streams the bytes. It is a path below the API root ("/telegram/…"),
	// not an absolute URL: a client joins it to the root it already talks to
	// and the answer is correct behind any host, proxy or mount.
	URL string `json:"url,omitempty"`
}

// mediaHasBytes reports whether the worker can return this media type's
// content through Media. Photos and documents carry file data; polls,
// locations, links and anything a backend could not classify do not.
func mediaHasBytes(mediaType string) bool {
	switch mediaType {
	case "photo", "video", "document", "audio", "voice", "sticker", "gif":
		return true
	default:
		return false
	}
}

func messageViewOf(key string, channelID int64, msg rpc.Message) messageView {
	out := messageView{
		Message:   msg,
		Views:     msg.Views,
		Reactions: make([]rpc.Reaction, 0, len(msg.Reactions)),
		Entities:  make([]rpc.Entity, 0, len(msg.Entities)),
	}
	out.Reactions = append(out.Reactions, msg.Reactions...)
	out.Entities = append(out.Entities, msg.Entities...)
	if msg.Media == nil {
		out.Media = nil
		return out
	}
	out.Media = &mediaView{
		ID:       msg.Media.ID,
		Type:     msg.Media.Type,
		Label:    msg.Media.Label,
		Filename: msg.Media.Filename,
		MimeType: msg.Media.MimeType,
		Size:     msg.Media.Size,
	}
	// Only an attachment whose bytes the media route can fetch has a URL. A
	// poll, a location, a link preview and a deleted document have nothing to
	// download — each is one of these types.
	if mediaHasBytes(msg.Media.Type) {
		out.Media.URL = fmt.Sprintf("%s%s/channels/%d/messages/%d/media",
			strings.TrimPrefix(paths, "/api/v1"), key, channelID, msg.ID)
	}
	return out
}

// message returns exactly one post.
func (s *Server) message(ctx context.Context, w http.ResponseWriter, r *http.Request, key string,
	worker Worker, rawChannelID, rawMessageID, requestID string) {

	channelID, err := strconv.ParseInt(rawChannelID, 10, 64)
	if err != nil || channelID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "channel_id must be a positive integer.", requestID)
		return
	}
	messageID, err := strconv.Atoi(rawMessageID)
	if err != nil || messageID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "message_id must be a positive integer.", requestID)
		return
	}

	if _, ok := s.requireChannelView(ctx, w, r, key, channelID, requestID); !ok {
		return
	}
	result, err := worker.Message(ctx, channelID, messageID)
	if err != nil {
		s.upstreamError(w, key, "message", err, requestID)
		return
	}
	if len(result.Messages) == 0 {
		s.fail(w, http.StatusNotFound, CodeNotFound, "No message with that id exists in this channel.", requestID)
		return
	}

	views := make([]messageView, 0, len(result.Messages))
	for _, msg := range result.Messages {
		views = append(views, messageViewOf(key, channelID, msg))
	}
	// A single message has no pagination; the envelope still carries meta so
	// callers can treat every response the same way. The channel is in the path
	// and is not repeated in the body.
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"messages": views},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// media streams one attachment's bytes.
//
// The worker returns the content; the gateway decides what the public HTTP
// answer looks like — the content type, the name a download is saved under, and
// for a request that asks for part of the file, exactly which bytes and which
// status. The bytes are forwarded as they arrive, so a 60 MB document never
// accumulates here either.
func (s *Server) media(ctx context.Context, w http.ResponseWriter, r *http.Request, key string,
	worker Worker, rawChannelID, rawMessageID, requestID string) {

	channelID, err := strconv.ParseInt(rawChannelID, 10, 64)
	if err != nil || channelID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "channel_id must be a positive integer.", requestID)
		return
	}
	messageID, err := strconv.Atoi(rawMessageID)
	if err != nil || messageID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "message_id must be a positive integer.", requestID)
		return
	}

	if _, ok := s.requireChannelView(ctx, w, r, key, channelID, requestID); !ok {
		return
	}

	wanted, kind := parseRange(r.Header.Get("Range"))
	// A download is a transfer, not a call: the request budget that answers a
	// listing must not end it mid-body. The fetch is still tied to this handler,
	// so a caller that walks away stops it.
	streamCtx, cancelStream := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelStream()
	stream, err := worker.Media(streamCtx, channelID, messageID, 0, mediaSliceOf(wanted))
	if err != nil {
		s.upstreamError(w, key, "media", err, requestID)
		return
	}
	if stream.Body == nil {
		stream.Body = http.NoBody
	}
	defer stream.Body.Close()

	// A range is only promised when the attachment's length is known: without
	// one there is nothing to slice against and nothing to report in
	// Content-Range. What cannot be honoured is not advertised.
	if stream.Size > 0 {
		w.Header().Set("Accept-Ranges", "bytes")
	} else {
		w.Header().Set("Accept-Ranges", "none")
	}

	// The error answer is JSON, so it must be decided before the attachment's
	// own headers are written.
	if kind == rangeSlice && stream.Size > 0 && unsatisfiableRange(stream.Size, stream.Length) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", stream.Size))
		s.fail(w, http.StatusRequestedRangeNotSatisfiable, CodeInvalidInput,
			"The requested range is outside this attachment.", requestID)
		return
	}

	contentType := stream.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", contentDisposition(stream.Filename))
	// The bytes are a file the sender named, never a page: a browser must not
	// second-guess the type and render something else.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Attachments are immutable: the same message id always yields the same
	// bytes, so a private cache can keep them.
	w.Header().Set("Cache-Control", "private, max-age=86400")

	status := http.StatusOK
	if kind == rangeSlice && stream.Size > 0 {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d",
			stream.Offset, stream.Offset+stream.Length-1, stream.Size))
	}
	if stream.Length > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(stream.Length, 10))
	}
	// A long transfer must outlive the server's per-request write deadline, for
	// the reason the worker clears its own in rpc.writeMediaStream. Flushing
	// below does not extend that deadline; only this does.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(status)
	// The headers go out before the bytes: a player reads the content type and
	// the range it was given before the attachment starts arriving, and a
	// download shows its name immediately.
	http.NewResponseController(w).Flush()
	written, copyErr := io.Copy(w, stream.Body)
	// The status and the length are already on the wire, so a transfer that
	// stopped early cannot be reported to the caller as an error. It is reported
	// here instead: fewer bytes than promised is a broken download, and nothing
	// else on the path says so.
	switch {
	case copyErr != nil && stream.Length > 0:
		s.logf("gateway: client=%s op=media request_id=%s channel=%d message=%d: truncated after %d of %d bytes: %v",
			key, requestID, channelID, messageID, written, stream.Length, copyErr)
	case copyErr != nil:
		// No size was ever reported, so nothing was promised and nothing is
		// missing from a count — but the transfer is still not whole.
		s.logf("gateway: client=%s op=media request_id=%s channel=%d message=%d: the transfer stopped after %d bytes: %v",
			key, requestID, channelID, messageID, written, copyErr)
	case stream.Length > 0 && written != stream.Length:
		s.logf("gateway: client=%s op=media request_id=%s channel=%d message=%d: truncated after %d of %d bytes, the body ended early",
			key, requestID, channelID, messageID, written, stream.Length)
	}
}

// customEmoji streams one reaction custom emoji as its bytes.
func (s *Server) customEmoji(ctx context.Context, w http.ResponseWriter, r *http.Request, key string,
	worker Worker, rawID, requestID string) {

	documentID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || documentID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"document_id must be a positive integer.", requestID)
		return
	}
	file, err := worker.CustomEmoji(ctx, documentID)
	if err != nil {
		s.upstreamError(w, key, "custom_emoji", err, requestID)
		return
	}
	if len(file.Data) == 0 {
		s.fail(w, http.StatusNotFound, CodeNotFound, "This custom emoji is not available.", requestID)
		return
	}
	contentType := file.MimeType
	if contentType == "" {
		contentType = http.DetectContentType(file.Data)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(file.Data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A custom emoji document's bytes never change, so a private cache keeps
	// them for a day like an attachment.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Data)
}

// upstreamError maps a worker failure onto the public contract.
func (s *Server) upstreamError(w http.ResponseWriter, key, op string, err error, requestID string) {
	status, code, message := classify(err)
	s.cfg.Logf("gateway: client=%s op=%s request_id=%s code=%s err=%v", key, op, requestID, code, err)
	w.Header().Set("X-Request-Id", requestID)
	var coded *rpc.Error
	if status == http.StatusTooManyRequests && errors.As(err, &coded) && coded.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(coded.RetryAfter))
	}
	s.fail(w, status, code, message, requestID)
}

func classify(err error) (int, string, string) {
	switch {
	case errors.Is(err, rpc.ErrNeedsAuth):
		return http.StatusConflict, CodeNeedsAuth,
			"The configured Telegram account is not authorized; run the auth command for this client."
	case errors.Is(err, rpc.ErrNotConnected):
		return http.StatusServiceUnavailable, CodeNotConnected,
			"The Telegram client is configured but has no live connection."
	case errors.Is(err, rpc.ErrChannelNotFound):
		return http.StatusNotFound, CodeChannelNotFound, "The channel was not found for this client."
	case errors.Is(err, rpc.ErrPeerUnresolved):
		return http.StatusBadRequest, CodePeerUnresolved,
			"This numeric channel id cannot be resolved yet; search for the channel by username or link first."
	case errors.Is(err, rpc.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, CodeWorkerTimeout, "The Telegram client did not respond in time."
	case errors.Is(err, rpc.ErrUnavailable):
		return http.StatusServiceUnavailable, CodeWorkerUnavail,
			"The configured Telegram client is currently unavailable."
	}
	var coded *rpc.Error
	if errors.As(err, &coded) {
		switch coded.Code {
		case rpc.CodeInvalidInput:
			return http.StatusBadRequest, CodeInvalidInput, "The request was rejected by the Telegram client."
		case rpc.CodeRateLimited:
			return http.StatusTooManyRequests, CodeRateLimited, "Telegram is rate limiting this client."
		case rpc.CodeChannelNotFound:
			return http.StatusNotFound, CodeChannelNotFound, "The channel was not found for this client."
		case rpc.CodeNotFound:
			return http.StatusNotFound, CodeNotFound, "The requested item does not exist for this client."
		case rpc.CodeNoAvatar:
			return http.StatusNotFound, CodeNotFound, "This channel has no avatar."
		}
	}
	return http.StatusBadGateway, CodeWorkerError, "The Telegram client failed to complete the request."
}

// parsePaging validates page/per_page and rejects unknown parameters so a typo
// cannot silently change the result.
func parsePaging(q map[string][]string) (int, int, error) {
	page, perPage := rpc.DefaultPage, rpc.DefaultPerPage
	for key, values := range q {
		if len(values) != 1 {
			return 0, 0, errors.New(key + " must be given exactly once.")
		}
		raw := values[0]
		switch key {
		case "page":
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 {
				return 0, 0, errors.New("page must be an integer of at least 1.")
			}
			page = v
		case "per_page":
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > rpc.MaxPerPage {
				return 0, 0, errors.New("per_page must be an integer between 1 and " +
					strconv.Itoa(rpc.MaxPerPage) + ".")
			}
			perPage = v
		case "capability":
			// The destination picker asks for channels this account may post
			// to. Any other value is refused rather than silently ignored.
			if raw != "send" {
				return 0, 0, errors.New(`capability must be "send".`)
			}
		default:
			return 0, 0, errors.New("unknown query parameter " + strconv.Quote(key) + ".")
		}
	}
	return page, perPage, nil
}

// pageMeta renders pagination metadata.
//
// total is optional: Telegram reports how many dialogs an account has, but not
// how many messages a channel contains. A messages page therefore omits total
// rather than render "total": 0, which would read as "no messages".
func pageMeta(requestID string, now time.Time, page rpc.PageInfo, withTotal bool) map[string]any {
	meta := metaFor(requestID, now)
	meta["page"] = page.Page
	meta["per_page"] = page.PerPage
	meta["has_more"] = page.HasMore
	if withTotal {
		meta["total"] = page.Total
	}
	if page.NextPage > 0 {
		meta["next_page"] = page.NextPage
	}
	return meta
}

func metaFor(requestID string, now time.Time) map[string]any {
	return map[string]any{
		"request_id":   requestID,
		"generated_at": RFC3339String(now),
	}
}

// formatTime renders a timestamp, or nil for the zero time (a field the API
// omits rather than invents). RFC3339String is the same as a string, for a
// field that is always present.
func formatTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return RFC3339String(t)
}

// RFC3339String renders a timestamp in UTC, the one format every response uses.
func RFC3339String(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// fullPath is one of the API's fixed paths as the caller addresses it: under
// the configured base path, with the base path "" meaning the root.
func (s *Server) fullPath(p string) string {
	return s.cfg.BasePath + p
}

// underPath reports whether path is prefix itself or below it.
func underPath(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func (s *Server) notFound(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusNotFound, CodeNotFound, "The requested resource does not exist.", requestID)
}

func (s *Server) fail(w http.ResponseWriter, status int, code, message, requestID string) {
	writeJSON(w, status, errorEnvelope{Error: Error{Code: code, Message: message, RequestID: requestID}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// requestIDFrom trusts a caller-supplied id only when it is a short, safe
// token, so it can be echoed and logged without injection risk.
func requestIDFrom(r *http.Request) string {
	if given := strings.TrimSpace(r.Header.Get("X-Request-Id")); given != "" && safeID(given) {
		return given
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

func safeID(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
