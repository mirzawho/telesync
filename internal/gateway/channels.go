package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/mirza/telesync/internal/avatarcache"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// ManagedStore is the local record of channels selected through join.
//
// It is deliberately the only database dependency in the gateway, and only the
// join path touches it. Listing, search, profile and health never do.
type ManagedStore interface {
	Get(ctx context.Context, clientKey string, channelID int64) (channelstore.Channel, bool, error)
	ManagedIDs(ctx context.Context, clientKey string) (map[int64]bool, error)
	Upsert(ctx context.Context, ch channelstore.Channel) (channelstore.Channel, error)
}

// AvatarCache is the persistent profile-photo cache.
type AvatarCache interface {
	Get(ctx context.Context, clientKey string, channelID int64, fetch avatarcache.FetchFunc) (avatarcache.Image, error)
}

// channelView is the public shape of one channel.
//
// It is the one channel object the API answers with, whichever source it came
// from: the live Telegram endpoint builds it from a fresh Telegram read, and
// the local channel detail builds it from the persisted snapshot. The fields
// are identical; only their values and their avatar URL's route differ.
type channelView struct {
	// ID is Telegram's channel id, a decimal string so JavaScript cannot
	// truncate it. It is never the local managed-channel row id.
	ID               string `json:"id"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	Username         string `json:"username,omitempty"`
	Description      string `json:"description,omitempty"`
	AvatarAvailable  bool   `json:"avatar_available"`
	AvatarURL        string `json:"avatar_url,omitempty"`
	IsMember         bool   `json:"is_member"`
	IsManaged        bool   `json:"is_managed"`
	MembershipStatus string `json:"membership_status"`
	Permissions      perms  `json:"permissions"`
	Verified         bool   `json:"verified"`
	Scam             bool   `json:"scam"`
	Fake             bool   `json:"fake"`
	// ParticipantsCount is null when Telegram did not report one.
	ParticipantsCount *int `json:"participants_count"`
}

// perms is the four-field permission object. Booleans only, never null.
type perms struct {
	New    bool `json:"new"`
	Edit   bool `json:"edit"`
	Delete bool `json:"delete"`
	Read   bool `json:"read"`
}

func permsOf(p rpc.Permissions) perms {
	return perms{New: p.New, Edit: p.Edit, Delete: p.Delete, Read: p.Read}
}

// channelViewOf builds the public view of one channel from a live Telegram read.
func (s *Server) channelViewOf(key string, info rpc.ChannelInfo, managed bool) channelView {
	url := ""
	if info.AvatarAvailable && info.ID != "" {
		url = avatarURLFor(key, info.ID)
	}
	return channelViewOf(url, info.ID, info, managed)
}

// channelViewOf turns one channel's fields into the public object.
//
// avatarURL is the URL that serves this channel's bytes — the Telegram route's,
// or the local one for a snapshot — or "" when the channel has no avatar.
func channelViewOf(avatarURL string, id string, info rpc.ChannelInfo, managed bool) channelView {
	out := channelView{
		ID:                id,
		Type:              info.Type,
		Title:             info.Title,
		Username:          info.Username,
		Description:       info.Description,
		AvatarAvailable:   info.AvatarAvailable,
		IsMember:          info.IsMember,
		IsManaged:         managed,
		MembershipStatus:  info.MembershipStatus,
		Permissions:       permsOf(info.Permissions),
		Verified:          info.Verified,
		Scam:              info.Scam,
		Fake:              info.Fake,
		ParticipantsCount: info.ParticipantsCount,
	}
	if out.MembershipStatus == "" {
		out.MembershipStatus = string(tgapi.MembershipUnknown)
	}
	if out.Type == "" {
		out.Type = "channel"
	}
	if info.AvatarAvailable && id != "" {
		out.AvatarURL = avatarURL
	}
	return out
}

func avatarURLFor(key, channelID string) string {
	return paths + key + "/channels/" + channelID + "/avatar"
}

// find serves GET /channels/find?q=... — a real Telegram discovery.
func (s *Server) find(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key string, worker Worker, requestID string) {

	raw := r.URL.Query().Get("q")
	query, err := tgapi.ParseChannelQuery(raw)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	// Only `q` is meaningful here; a stray parameter is a typo, not something
	// to silently ignore.
	for name := range r.URL.Query() {
		if name != "q" {
			s.fail(w, http.StatusBadRequest, CodeInvalidInput,
				"unknown query parameter "+strconv.Quote(name)+".", requestID)
			return
		}
	}

	a := actorAuth(r)
	if !s.allowedAnyChannel(r, channelstore.ActionView) {
		s.forbidden(w, requestID)
		return
	}
	result, err := worker.Find(ctx, findRequestOf(query))
	if err != nil {
		s.upstreamError(w, key, "find", err, requestID)
		return
	}

	managed, err := s.managedIDs(ctx, key)
	if err != nil {
		// Search results are still useful without the local flag; saying so
		// beats failing a read-only discovery request.
		s.logf("gateway: client=%s op=find request_id=%s managed lookup failed: %v", key, requestID, err)
	}

	channels := make([]channelView, 0, len(result.Channels))
	for _, info := range result.Channels {
		isManaged := false
		if id, convErr := strconv.ParseInt(info.ID, 10, 64); convErr == nil {
			isManaged = managed[id]
		}
		if !s.liveAllowed(ctx, a, key, info.ID) {
			continue
		}
		channels = append(channels, s.channelViewOf(key, info, isManaged))
	}

	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"channels": channels},
		Meta: map[string]any{"count": len(channels)},
	})
}

// findRequestOf turns a parsed query into the wire request.
func findRequestOf(q tgapi.ChannelQuery) rpc.FindRequest {
	switch q.Kind {
	case tgapi.QueryUsername:
		return rpc.FindRequest{Kind: rpc.FindByUsername, Username: q.Username}
	case tgapi.QueryInvite:
		return rpc.FindRequest{Kind: rpc.FindByInvite, Invite: q.Invite}
	case tgapi.QueryID:
		return rpc.FindRequest{Kind: rpc.FindByID, ChannelID: q.ChannelID}
	default:
		return rpc.FindRequest{Kind: rpc.FindByText, Text: q.Text}
	}
}

// channel serves GET /channels/{ref} — one channel's current Telegram state.
//
// The data is Telegram's, read fresh; `is_managed` additionally says whether the
// operator has selected it locally. It is the same channel object `find` and
// `join` return, so a client needs one parser.
func (s *Server) channel(ctx context.Context, w http.ResponseWriter, r *http.Request, key string,
	worker Worker, rawRef, requestID string) {

	ref, err := channelRefOf(key, rawRef)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	if ref.ChannelID > 0 {
		if _, ok := s.requireChannelView(ctx, w, r, key, ref.ChannelID, requestID); !ok {
			return
		}
	} else if !s.allowedAnyChannel(r, channelstore.ActionView) {
		// An invite names no channel yet, so there is no local row to check
		// against; only a caller who may view channels at all may resolve one.
		s.forbidden(w, requestID)
		return
	}

	result, err := worker.Channel(ctx, ref)
	if err != nil {
		s.upstreamError(w, key, "channel", err, requestID)
		return
	}

	managed := false
	if ref.ChannelID > 0 {
		isManaged, lookupErr := s.managedBefore(ctx, key, ref.ChannelID)
		if lookupErr != nil {
			s.logf("gateway: client=%s op=channel request_id=%s managed lookup failed: %v",
				key, requestID, lookupErr)
		} else {
			managed = isManaged
		}
	}

	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"channel": s.channelViewOf(key, result.Channel, managed)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// join serves POST /channels/{ref}/join.
//
// The order is fixed and deliberate: resolve, join when needed, confirm what
// Telegram said, then persist locally. A Telegram success followed by a local
// write failure is reported as exactly that, because the two systems are not
// one transaction.
func (s *Server) join(ctx context.Context, w http.ResponseWriter, r *http.Request, key string,
	worker Worker, rawRef, requestID string) {

	ref, err := channelRefOf(key, rawRef)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	// Joining creates a local channel record, which cannot carry an assignment
	// before it exists, so it is the global creation permission that decides.
	if !actorAuth(r).Global(channelstore.PermissionChannelsCreate) {
		s.forbidden(w, requestID)
		return
	}
	if s.cfg.Channels == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"Local channel storage is not configured.", requestID)
		return
	}

	// Whether the channel was already managed decides the reported result. It
	// is read before the join so the answer is not guessed from timestamps.
	alreadyManaged := false
	if ref.ChannelID > 0 {
		managed, lookupErr := s.managedBefore(ctx, key, ref.ChannelID)
		if lookupErr != nil {
			s.logf("gateway: client=%s op=join request_id=%s managed lookup failed: %v",
				key, requestID, lookupErr)
		} else {
			alreadyManaged = managed
		}
	}

	result, err := worker.Join(ctx, ref)
	if err != nil {
		s.upstreamError(w, key, "join", err, requestID)
		return
	}

	// A pending request is not membership. It is recorded so the operation is
	// visible, but the record never claims the account is in the channel.
	pending := result.Outcome == rpc.OutcomePending

	channelID, convErr := strconv.ParseInt(result.Channel.ID, 10, 64)
	if convErr != nil || channelID <= 0 {
		// An invite that resolved to a channel Telegram declined to identify.
		// Nothing can be persisted without an id, and inventing one would be
		// worse than reporting the gap.
		s.fail(w, http.StatusBadGateway, CodeWorkerError,
			"The Telegram client did not identify the channel, so it could not be recorded.", requestID)
		return
	}

	saved, err := s.cfg.Channels.Upsert(ctx, channelstore.Channel{
		ClientKey:        key,
		ChannelID:        channelID,
		PeerType:         peerTypeOf(result.Channel.Type),
		Title:            result.Channel.Title,
		Username:         result.Channel.Username,
		Description:      result.Channel.Description,
		AvatarPhotoID:    result.Channel.AvatarVersion,
		MembershipStatus: statusOf(result.Channel, pending),
		Verified:         result.Channel.Verified,
		Scam:             result.Channel.Scam,
		Fake:             result.Channel.Fake,
		// The worker always fills a channel's type, and a Telegram answer is a
		// report that carries the notices either way, so this join recorded them.
		NoticesKnown: result.Channel.Type != "",
		Permissions: tgapi.Permissions{
			New:    result.Channel.Permissions.New,
			Edit:   result.Channel.Permissions.Edit,
			Delete: result.Channel.Permissions.Delete,
			Read:   result.Channel.Permissions.Read,
		},
		PermissionsKnown:  statusOf(result.Channel, pending) == channelstore.StatusMember,
		ParticipantsCount: result.Channel.ParticipantsCount,
	})
	if err != nil {
		// Partial success, stated plainly: Telegram's side happened, the local
		// side did not. Retrying is safe because the join path is idempotent.
		if pending {
			s.fail(w, http.StatusInternalServerError, CodeWorkerError,
				"Telegram accepted the join request but the local record could not be saved; retry to record it.", requestID)
			return
		}
		s.fail(w, http.StatusInternalServerError, CodeWorkerError,
			"Telegram membership is confirmed but the local record could not be saved; retry to record it.", requestID)
		return
	}

	view := s.channelViewOf(key, result.Channel, true)
	if saved.ChannelID != channelID {
		// Defensive: never answer with a view of a different channel.
		s.logf("gateway: client=%s op=join request_id=%s stored %d for %d", key, requestID, saved.ChannelID, channelID)
	}

	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{
			"channel": view,
			"result":  joinResultOf(result.Outcome, alreadyManaged, pending),
		},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// joinResultOf names what actually happened, in the documented vocabulary.
//
// The distinction that matters is Telegram's side (joined vs already a member)
// and the local side (newly recorded vs already managed). It is derived from a
// read of the store made before the join, not from timestamps.
func joinResultOf(outcome string, alreadyManaged, pending bool) string {
	switch {
	case pending:
		return "join_pending"
	case alreadyManaged:
		return "already_managed"
	case outcome == rpc.OutcomeAlreadyMember:
		return "already_member_added_to_management"
	default:
		return "joined_and_added_to_management"
	}
}

// managedBefore reports whether a record exists, tolerating a lookup failure.
func (s *Server) managedBefore(ctx context.Context, key string, channelID int64) (bool, error) {
	if s.cfg.Channels == nil {
		return false, nil
	}
	_, ok, err := s.cfg.Channels.Get(ctx, key, channelID)
	return ok, err
}

// statusOf converts the worker's membership report into a stored status.
func statusOf(ch rpc.ChannelInfo, pending bool) string {
	if pending {
		return "join_pending"
	}
	switch ch.MembershipStatus {
	case string(tgapi.MembershipMember):
		return "member"
	case string(tgapi.MembershipUnknown), "":
		if ch.IsMember {
			return "member"
		}
		return "unknown"
	default:
		if ch.IsMember {
			return "member"
		}
		return "unknown"
	}
}

func peerTypeOf(kind string) string {
	if kind == "group" {
		return "group"
	}
	return "channel"
}

// channelRefOf parses the path segment of a channel-scoped route.
//
// A path segment is either a decimal channel id or "+<invite hash>"; anything
// else is rejected before it reaches a worker.
func channelRefOf(key, raw string) (rpc.ChannelRef, error) {
	parsed, err := tgapi.ParseChannelRef(raw)
	if err != nil {
		return rpc.ChannelRef{}, errors.New(
			"channel_id must be a decimal channel id or an invite hash such as +AbCdEf123456.")
	}
	if parsed.IsInvite() {
		return rpc.ChannelRef{Invite: parsed.Invite}, nil
	}
	return rpc.ChannelRef{ChannelID: parsed.ID}, nil
}

// managedIDs reads the local selection set for one client.
func (s *Server) managedIDs(ctx context.Context, key string) (map[int64]bool, error) {
	if s.cfg.Channels == nil {
		return map[int64]bool{}, nil
	}
	return s.cfg.Channels.ManagedIDs(ctx, key)
}

// avatar serves GET /channels/{id}/avatar — the image bytes, never a URL.
func (s *Server) avatar(ctx context.Context, w http.ResponseWriter, r *http.Request, key string,
	worker Worker, rawID, requestID string) {

	ref, err := channelRefOf(key, rawID)
	if err != nil || ref.Invite != "" {
		// An avatar needs a stable id to key its cache on.
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"avatar needs a numeric channel_id; resolve the invite first.", requestID)
		return
	}
	if _, ok := s.requireChannelView(ctx, w, r, key, ref.ChannelID, requestID); !ok {
		return
	}

	if s.cfg.Avatars == nil {
		s.serveAvatarDirect(ctx, w, key, worker, ref, requestID)
		return
	}

	img, err := s.cfg.Avatars.Get(ctx, key, ref.ChannelID, func(fctx context.Context) ([]byte, string, int64, error) {
		file, fetchErr := worker.Avatar(fctx, ref)
		if fetchErr != nil {
			if isNoAvatar(fetchErr) {
				return nil, "", 0, avatarcache.ErrNoAvatar
			}
			return nil, "", 0, fetchErr
		}
		contentType := file.MimeType
		if contentType == "" {
			contentType = http.DetectContentType(file.Data)
		}
		return file.Data, contentType, file.PhotoID, nil
	})
	if err != nil {
		if errors.Is(err, avatarcache.ErrNoAvatar) {
			s.fail(w, http.StatusNotFound, CodeNotFound, "This channel has no avatar.", requestID)
			return
		}
		s.upstreamError(w, key, "avatar", err, requestID)
		return
	}
	s.writeImage(w, img)
}

// isNoAvatar reports whether an upstream error means "there is no photo".
//
// The worker answers with a coded error, not a sentinel, so both forms are
// checked; neither is a failure worth a 502.
func isNoAvatar(err error) bool {
	if errors.Is(err, rpc.ErrNoAvatar) || errors.Is(err, avatarcache.ErrNoAvatar) {
		return true
	}
	var coded *rpc.Error
	return errors.As(err, &coded) && coded.Code == rpc.CodeNoAvatar
}

// serveAvatarDirect streams an avatar when no cache is configured.
func (s *Server) serveAvatarDirect(ctx context.Context, w http.ResponseWriter, key string,
	worker Worker, ref rpc.ChannelRef, requestID string) {

	file, err := worker.Avatar(ctx, ref)
	if err != nil {
		if errors.Is(err, rpc.ErrNoAvatar) {
			s.fail(w, http.StatusNotFound, CodeNotFound, "This channel has no avatar.", requestID)
			return
		}
		s.upstreamError(w, key, "avatar", err, requestID)
		return
	}
	if len(file.Data) == 0 {
		s.fail(w, http.StatusNotFound, CodeNotFound, "This channel has no avatar.", requestID)
		return
	}
	contentType := file.MimeType
	if contentType == "" {
		contentType = http.DetectContentType(file.Data)
	}
	s.writeImage(w, avatarcache.Image{Data: file.Data, ContentType: contentType, PhotoID: file.PhotoID})
}

// writeImage renders an image response with its cache headers.
func (s *Server) writeImage(w http.ResponseWriter, img avatarcache.Image) {
	contentType := img.ContentType
	if contentType == "" {
		contentType = http.DetectContentType(img.Data)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(img.Data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if img.PhotoID != 0 {
		// Telegram's photo id changes when the avatar does, so it is a stable
		// validators value as well as a cache key.
		w.Header().Set("ETag", `"`+strconv.FormatInt(img.PhotoID, 10)+`"`)
	}
	if img.Stale {
		// Served from cache because the refresh failed: say so, and let the
		// browser come back for a fresh copy rather than pinning this one.
		w.Header().Set("Warning", `110 - "Response is stale"`)
		w.Header().Set("Cache-Control", "private, max-age=0")
	} else {
		maxAge := int(int64(time.Until(img.ExpiresAt).Seconds()))
		if maxAge < 0 {
			maxAge = 0
		}
		w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(maxAge))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img.Data)
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

// liveAllowed authorizes one live Telegram result by resolving it to its local
// record first.
//
// A result with no local row has nowhere to carry a per-channel assignment, so
// only the global "view channels" permission can admit it. A lookup failure
// hides the result: this is a visibility decision, and failing open would show
// a channel the caller may not see.
func (s *Server) liveAllowed(ctx context.Context, authr *channelstore.Authorizer, key, rawID string) bool {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		return authr.CanList(channelstore.KindChannel)
	}
	t := channelTarget{telegramID: id}
	if s.cfg.Channels != nil {
		row, ok, err := s.cfg.Channels.Get(ctx, key, id)
		if err != nil {
			s.logf("gateway: client=%s live authorization lookup failed for %s: %v", key, rawID, err)
			return authr.CanList(channelstore.KindChannel)
		}
		t.found, t.localID = ok, row.ID
	}
	return access(authr, t).allowed
}
