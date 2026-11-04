package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/avatarcache"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// LocalChannelStore is the local channel record the local channel API reads,
// lists and removes.
//
// It is the same table the join route writes: a local channel is a managed
// channel, addressed here by its local row id rather than by its Telegram id.
// The gateway only requires ManagedStore; the local channel routes answer 503
// unless the configured store has these methods too.
type LocalChannelStore interface {
	ManagedStore
	GetByID(ctx context.Context, id int64) (channelstore.Channel, bool, error)
	// ListChannels lists one page across every client, with the matching total.
	ListChannels(ctx context.Context, q channelstore.ChannelListQuery) ([]channelstore.Channel, int, error)
	// ListChannelsByIDs lists the rows among ids in the listing order.
	ListChannelsByIDs(ctx context.Context, ids []int64) ([]channelstore.Channel, error)
	// DeleteChannelRow removes one record by its local id. Telegram is not
	// contacted; the permission assignments that reference the row go with it.
	DeleteChannelRow(ctx context.Context, id int64) error
}

// localChannelsPrefix is the collection route; the local id follows it.
const localChannelsPrefix = "/api/v1/local/channels"

// maxLeaveBody bounds a leave request body. It holds one boolean and an
// optional token.
const maxLeaveBody = 16 << 10

// CodePartialFailure means Telegram's part of an operation succeeded and the
// local part did not.
const CodePartialFailure = "partial_failure"

// localChannelView is the public shape of one local channel record.
//
// ID is the local row id: the primary key this API addresses the record by, and
// what the record listing and the leave and category routes take. ChannelID is
// Telegram's id for the channel. The two are never interchangeable.
//
// The detail route answers the canonical channelView instead (see
// localChannelDetail); this shape is the listing's.
type localChannelView struct {
	ID        int64  `json:"id"`
	ClientKey string `json:"client_key"`
	// ChannelID is Telegram's channel id, a number because it is the same value
	// the Telegram routes report for the channel.
	ChannelID         int64  `json:"channel_id"`
	Type              string `json:"type"`
	Title             string `json:"title"`
	Username          string `json:"username,omitempty"`
	Description       string `json:"description,omitempty"`
	AvatarAvailable   bool   `json:"avatar_available"`
	AvatarURL         string `json:"avatar_url,omitempty"`
	MembershipStatus  string `json:"membership_status"`
	ParticipantsCount *int   `json:"participants_count"`
	// CreatedAt is when the channel was first recorded; UpdatedAt and
	// LastSyncedAt are when the record was last written from Telegram.
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	LastSyncedAt string `json:"last_synced_at"`
}

func localChannelViewOf(ch channelstore.Channel) localChannelView {
	out := localChannelView{
		ID:                ch.ID,
		ClientKey:         ch.ClientKey,
		ChannelID:         ch.ChannelID,
		Type:              ch.PeerType,
		Title:             ch.Title,
		Username:          ch.Username,
		Description:       ch.Description,
		AvatarAvailable:   ch.AvatarPhotoID != 0,
		MembershipStatus:  ch.MembershipStatus,
		ParticipantsCount: ch.ParticipantsCount,
		CreatedAt:         timestampStringOrEmpty(ch.CreatedAt),
		UpdatedAt:         timestampStringOrEmpty(ch.UpdatedAt),
		LastSyncedAt:      timestampStringOrEmpty(ch.LastSyncedAt),
	}
	if out.Type == "" {
		out.Type = channelstore.PeerChannel
	}
	if out.AvatarAvailable {
		out.AvatarURL = fmt.Sprintf("%s/%d/avatar", localChannelsPrefix, ch.ID)
	}
	return out
}

// localChannelDetail builds the canonical channel object from the stored
// snapshot, the same one the Telegram channel detail answers with.
//
// Nothing here asks Telegram: every value is what the last write recorded.
// Values the record says are unknown stay unknown rather than being guessed:
// a membership that was never confirmed is not_member, and notices or
// permissions that were never reported are false.
func localChannelDetail(ch channelstore.Channel) channelView {
	membership := ch.MembershipStatus
	if membership == "" {
		membership = channelstore.StatusUnknown
	}
	info := rpc.ChannelInfo{
		Type:             ch.PeerType,
		Title:            ch.Title,
		Username:         ch.Username,
		Description:      ch.Description,
		AvatarAvailable:  ch.AvatarPhotoID != 0,
		IsMember:         membership == channelstore.StatusMember,
		MembershipStatus: membership,
		Verified:         ch.Verified,
		Scam:             ch.Scam,
		Fake:             ch.Fake,
		// A record is managed because it exists: that is what the table holds.
		ParticipantsCount: ch.ParticipantsCount,
	}
	if ch.PermissionsKnown {
		info.Permissions = rpc.Permissions{
			New:    ch.Permissions.New,
			Edit:   ch.Permissions.Edit,
			Delete: ch.Permissions.Delete,
			Read:   ch.Permissions.Read,
		}
	}
	id := strconv.FormatInt(ch.ChannelID, 10)
	avatar := ""
	if info.AvatarAvailable {
		avatar = fmt.Sprintf("%s/%d/avatar", localChannelsPrefix, ch.ID)
	}
	return channelViewOf(avatar, id, info, true)
}

// leaveRequest is the accepted body for the leave route.
//
// An omitted body means {"telegram": false}: the local record goes, the
// Telegram account stays a member. A credential is never read from the body.
type leaveRequest struct {
	Telegram *bool
	// telegramNull records that the body said "telegram": null, which
	// encoding/json would otherwise read as the field being absent.
	telegramNull bool
}

// UnmarshalJSON reads the body strictly.
//
// Without it, encoding/json would decode "telegram": null into a nil pointer,
// the same as an omitted field, and a client that sent null would silently get
// the default. The rest is what the decoder does anyway: an unknown field or a
// wrong type is an error.
func (l *leaveRequest) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key, value := range raw {
		switch key {
		case "telegram":
			if string(bytes.TrimSpace(value)) == "null" {
				l.telegramNull = true
				continue
			}
			var v bool
			if err := json.Unmarshal(value, &v); err != nil {
				return fmt.Errorf("telegram must be true or false: %w", err)
			}
			l.Telegram = &v
		default:
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return nil
}

// localChannelStore returns the configured store, or answers 503 when local
// channel storage is not available.
func (s *Server) localChannelStore(w http.ResponseWriter, requestID string) (LocalChannelStore, bool) {
	store, ok := s.cfg.Channels.(LocalChannelStore)
	if s.cfg.Channels == nil || !ok {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"Local channel storage is not configured.", requestID)
		return nil, false
	}
	return store, true
}

// localChannels routes a request whose path is under localChannelsPrefix. The
// caller is already authenticated; each handler decides what it may do.
func (s *Server) localChannels(w http.ResponseWriter, r *http.Request, requestID string) {
	rest := strings.TrimPrefix(r.URL.Path, s.fullPath(localChannelsPrefix))
	switch rest {
	case "", "/":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Use GET to list local channels. A channel is removed through POST .../{id}/leave.", requestID)
			return
		}
		s.listLocalChannels(w, r, requestID)
		return
	}

	raw := strings.TrimPrefix(rest, "/")
	parts := strings.SplitN(raw, "/", 2)
	rawID := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	// The method decides first, so an unsupported one is a 405 whatever the id
	// looks like.
	switch sub {
	case "categories":
		if r.Method != http.MethodGet && r.Method != http.MethodPut {
			w.Header().Set("Allow", "GET, PUT")
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Use GET to list a channel's categories or PUT to set them.", requestID)
			return
		}
	case "":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Use GET for one local channel, or POST .../{id}/leave to remove it.", requestID)
			return
		}
	case "messages", "avatar":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Only GET is supported.", requestID)
			return
		}
	case "leave":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Leaving changes state and must be sent as POST.", requestID)
			return
		}
	default:
		s.notFound(w, requestID)
		return
	}

	id, err := parseID(rawID)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "id must be a positive integer.", requestID)
		return
	}
	switch sub {
	case "categories":
		if r.Method == http.MethodGet {
			s.listChannelCategories(w, r, id, requestID)
		} else {
			s.setChannelCategories(w, r, id, requestID)
		}
	case "":
		s.getLocalChannel(w, r, id, requestID)
	case "messages":
		s.localChannelMessages(w, r, id, requestID)
	case "avatar":
		s.localChannelAvatar(w, r, id, requestID)
	default:
		s.leaveLocalChannel(w, r, id, requestID)
	}
}

func (s *Server) listLocalChannels(w http.ResponseWriter, r *http.Request, requestID string) {
	page, perPage, q, categoryIDs, err := parseLocalChannelQuery(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	store, ok := s.localChannelStore(w, requestID)
	if !ok {
		return
	}

	var (
		rows  []channelstore.Channel
		total int
	)
	// The global view lists every record unless one carries an explicit deny;
	// without the global view the list is the per-record allows alone. The
	// denied records are dropped first, so the page, the order and the total
	// describe only what the caller may see.
	//
	// The category filter is passed to the store both ways, so it is applied
	// before pagination and before the total — alongside the search text, and
	// inside the caller's own visibility.
	authr := actorAuth(r)
	global := authr.CanList(channelstore.KindChannel)
	denies := authr.HasDenies(channelstore.KindChannel, channelstore.ActionView)
	restricted := !global || denies
	ids := authr.ResourceIDs(channelstore.KindChannel, channelstore.ActionView)
	if !restricted {
		rows, total, err = store.ListChannels(r.Context(), channelstore.ChannelListQuery{
			Page: page, PerPage: perPage, Query: q, CategoryIDs: categoryIDs,
		})
	} else {
		var all []channelstore.Channel
		all, err = s.channelsVisibleTo(r.Context(), store, authr, q, categoryIDs, global, ids)
		if err == nil {
			visible := make([]channelstore.Channel, 0, len(all))
			for _, ch := range all {
				if localChannelMatches(ch, q) && visibleChannel(authr, ch.ID) {
					visible = append(visible, ch)
				}
			}
			total = len(visible)
			rows = pageSlice(visible, page, perPage)
		}
	}
	if err != nil {
		s.storeError(w, "list_local_channels", err, requestID)
		return
	}

	views := make([]localChannelView, 0, len(rows))
	for _, ch := range rows {
		views = append(views, localChannelViewOf(ch))
	}
	meta := metaFor(requestID, s.cfg.Now())
	meta["page"] = page
	meta["per_page"] = perPage
	meta["total"] = total
	meta["total_pages"] = (total + perPage - 1) / perPage
	meta["has_more"] = page*perPage < total
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{"channels": views}, Meta: meta})
}

// getLocalChannel serves GET /local/channels/{id}: the stored snapshot of one
// managed channel, in the canonical channel object.
func (s *Server) getLocalChannel(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	ch, ok := s.requireLocalChannel(w, r, id, channelstore.ActionView, requestID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"channel": localChannelDetail(ch)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) localChannelMessages(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	ch, ok := s.requireLocalChannel(w, r, id, channelstore.ActionView, requestID)
	if !ok {
		return
	}
	page, perPage, err := parsePaging(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	worker, ok := s.workerFor(w, ch.ClientKey, requestID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()
	// Telegram's id, on the row's own client: the request never names a
	// Telegram id, so none can be substituted for the record's.
	result, err := worker.Messages(ctx, ch.ChannelID, page, perPage)
	if err != nil {
		s.upstreamError(w, ch.ClientKey, "local_channel_messages", err, requestID)
		return
	}
	messages := result.Messages
	if messages == nil {
		messages = []rpc.Message{}
	}
	views := make([]messageView, 0, len(messages))
	for _, msg := range messages {
		views = append(views, messageViewOf(ch.ClientKey, ch.ChannelID, msg))
	}
	// The same shape the Telegram messages route answers with: nothing is
	// stored, and the page describes Telegram's own pagination.
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"messages": views},
		Meta: pageMeta(requestID, s.cfg.Now(), result.Page, false),
	})
}

func (s *Server) localChannelAvatar(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	ch, ok := s.requireLocalChannel(w, r, id, channelstore.ActionView, requestID)
	if !ok {
		return
	}
	worker, ok := s.workerFor(w, ch.ClientKey, requestID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()

	// The existing avatar path, keyed by the client and Telegram id the record
	// names, so the same image is cached once whichever route asked for it.
	ref := rpc.ChannelRef{ChannelID: ch.ChannelID}
	if s.cfg.Avatars == nil {
		s.serveAvatarDirect(ctx, w, ch.ClientKey, worker, ref, requestID)
		return
	}
	img, err := s.cfg.Avatars.Get(ctx, ch.ClientKey, ch.ChannelID, func(fctx context.Context) ([]byte, string, int64, error) {
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
		s.upstreamError(w, ch.ClientKey, "local_channel_avatar", err, requestID)
		return
	}
	s.writeImage(w, img)
}

// leaveLocalChannel serves POST /local/channels/{id}/leave.
//
// The order is fixed and deliberate. With telegram true, Telegram leaves first
// and the record is deleted only after Telegram confirmed. A failed leave keeps
// the record, so the operation can be retried; a delete that fails after a
// confirmed leave is reported as the partial success it is, because a network
// operation and a database row are not one transaction.
func (s *Server) leaveLocalChannel(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	store, ok := s.localChannelStore(w, requestID)
	if !ok {
		return
	}
	// The record and the caller come before the body: a record the caller may
	// not remove is 404/403 whatever it sent, so a refused caller cannot use
	// the body validator to probe the route.
	caller, err := s.leaveCaller(r)
	if err != nil {
		s.fail(w, statusOfAuthError(err), codeOfAuthError(err), s.authMessage(err), requestID)
		return
	}
	ch, found, lookupErr := store.GetByID(r.Context(), id)
	if lookupErr != nil {
		s.storeError(w, "get_local_channel", lookupErr, requestID)
		return
	}
	if !found {
		s.localChannelNotFound(w, requestID)
		return
	}
	if !s.localAllowed(caller.auth, ch.ID, channelstore.ActionDelete) {
		s.forbidden(w, requestID)
		return
	}
	req, err := decodeLeaveRequest(w, r)
	if err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}

	unlock := acquireLeaveLock(id)
	defer unlock()

	telegram := req.Telegram != nil && *req.Telegram
	if telegram {
		if !s.leaveOnTelegram(w, ch, requestID) {
			return
		}
	}

	deleteErr := store.DeleteChannelRow(r.Context(), id)
	if deleteErr != nil && !errors.Is(deleteErr, channelstore.ErrChannelRowNotFound) {
		// The store logs the cause; the caller is told what it can act on and
		// never what the database said.
		s.logf("gateway: op=leave_local_channel request_id=%s client=%s local_id=%d telegram_left=%v delete failed: %v",
			requestID, ch.ClientKey, id, telegram, deleteErr)
		if telegram {
			s.fail(w, http.StatusInternalServerError, CodePartialFailure,
				"The Telegram account left the channel, but the local record could not be deleted. "+
					"Send {\"telegram\": false} to retry removing only the record.", requestID)
			return
		}
		s.storeError(w, "leave_local_channel", deleteErr, requestID)
		return
	}

	meta := metaFor(requestID, s.cfg.Now())
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"local_deleted": true, "telegram_left": telegram},
		Meta: meta,
	})
}

// leaveCaller reads the leave body; the caller is the one the Authorization
// header resolved.
func (s *Server) leaveCaller(r *http.Request) (*actor, error) {
	a, err := s.authenticate(r)
	if err != nil {
		return nil, err
	}
	if a.user.ID <= 0 {
		return nil, errNotAnAccount
	}
	return a, nil
}

// decodeLeaveRequest reads the leave route's body, after the record and the
// caller have been authorized.
func decodeLeaveRequest(w http.ResponseWriter, r *http.Request) (leaveRequest, error) {
	if !hasBody(r) {
		return leaveRequest{}, nil
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		return leaveRequest{}, errUnsupportedMedia
	}

	var req leaveRequest
	if err := decodeOptionalBody(http.MaxBytesReader(w, r.Body, maxLeaveBody), &req); err != nil {
		return leaveRequest{}, err
	}
	// An explicit null is a JSON value of the wrong type, not an omission:
	// encoding/json decodes null into a *bool as "absent", so it is caught here
	// by the marker the field's own decoder left behind.
	if req.telegramNull {
		return leaveRequest{}, errors.New("telegram must be true or false.")
	}
	return req, nil
}

// hasBody reports whether the request carries bytes: a zero Content-Length
// with no transfer encoding is a request without a body.
func hasBody(r *http.Request) bool {
	return r.ContentLength != 0 || len(r.TransferEncoding) > 0
}

// decodeOptionalBody reads one JSON object from a body of any length, including
// zero bytes.
//
// Unknown fields are refused, so a misspelled field is reported rather than
// silently meaning false, and an extra value is refused, so a truncated attempt
// cannot look complete.
func decodeOptionalBody(body io.Reader, v any) error {
	if body == nil {
		return nil
	}
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%w: %s", errInvalidBody, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errInvalidBody
	}
	return nil
}

// leaveOnTelegram makes the record's client leave the channel, before anything
// local is removed. It answers the error itself and reports false when it
// failed.
func (s *Server) leaveOnTelegram(w http.ResponseWriter, ch channelstore.Channel, requestID string) bool {
	worker, ok := s.workerFor(w, ch.ClientKey, requestID)
	if !ok {
		return false
	}
	leaver, ok := worker.(leaverOf)
	if !ok {
		s.fail(w, http.StatusBadGateway, CodeWorkerError,
			"The Telegram client cannot leave channels.", requestID)
		return false
	}
	// The bounds are the gateway's, not the caller's: a client that gives up
	// must not abort a leave Telegram may already have recorded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), s.cfg.RequestTimeout)
	defer cancel()
	if err := leaver.LeaveChannel(ctx, ch.ChannelID); err != nil {
		s.upstreamError(w, ch.ClientKey, "leave_local_channel", err, requestID)
		return false
	}
	return true
}

// leaverOf is the worker operation set this API needs beyond reading.
type leaverOf interface {
	LeaveChannel(ctx context.Context, channelID int64) error
}

// workerFor returns the worker of the client the record names.
//
// A client that is no longer configured is 404, the same answer the Telegram
// routes give an unknown key, and never a different client's worker.
func (s *Server) workerFor(w http.ResponseWriter, clientKey, requestID string) (Worker, bool) {
	if s.cfg.Workers == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"The Telegram client is currently unavailable.", requestID)
		return nil, false
	}
	worker, ok := s.cfg.Workers.Worker(clientKey)
	if !ok {
		s.fail(w, http.StatusNotFound, CodeUnknownClient,
			"The Telegram client this channel belongs to is no longer configured.", requestID)
		return nil, false
	}
	return worker, true
}

// requireLocalChannel resolves the record and checks one action on it.
//
// It answers 404 for a missing record before permission is considered, so a
// caller never learns "forbidden" about a row that is not there; an existing
// record the caller may not touch is 403, and the answer does not say why.
func (s *Server) requireLocalChannel(w http.ResponseWriter, r *http.Request, id int64,
	action channelstore.Action, requestID string) (channelstore.Channel, bool) {

	store, ok := s.localChannelStore(w, requestID)
	if !ok {
		return channelstore.Channel{}, false
	}
	ch, found, err := store.GetByID(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_local_channel", err, requestID)
		return channelstore.Channel{}, false
	}
	if !found {
		s.localChannelNotFound(w, requestID)
		return channelstore.Channel{}, false
	}
	if !s.localAllowed(actorAuth(r), ch.ID, action) {
		s.forbidden(w, requestID)
		return channelstore.Channel{}, false
	}
	return ch, true
}

// localAllowed applies the channel permission model to one local record.
//
// The record is addressed by its local id, which is the id a per-channel grant
// names, and the answer is the visible() rule every route uses: the global view
// admits a record unless one carries an explicit deny, and without it only an
// explicit allow does. Update and delete are always per-channel assignments.
func (s *Server) localAllowed(authr *channelstore.Authorizer, localID int64, action channelstore.Action) bool {
	return visible(authr, channelstore.KindChannel, action, localID)
}

func (s *Server) localChannelNotFound(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusNotFound, CodeNotFound, "No local channel has that id.", requestID)
}

// statusOfAuthError is the status for an authentication failure of the leave
// route, or zero when err is not one.
func statusOfAuthError(err error) int {
	switch {
	case errors.Is(err, auth.ErrTokenExpired):
		return http.StatusUnauthorized
	case errors.Is(err, auth.ErrTokenMissing), errors.Is(err, auth.ErrTokenMalformed),
		errors.Is(err, auth.ErrTokenSignature), errors.Is(err, auth.ErrTokenIssuer),
		errors.Is(err, auth.ErrTokenSubject):
		return http.StatusUnauthorized
	case err == errNotAnAccount:
		return http.StatusForbidden
	}
	return 0
}

// codeOfAuthError is the documented code for statusOfAuthError's status.
func codeOfAuthError(err error) string {
	if err == errNotAnAccount {
		return CodeForbidden
	}
	return CodeUnauthorized
}

// authMessage is the public message for an authentication failure. It never
// carries the credential or why it failed.
func (s *Server) authMessage(err error) string {
	if err == errNotAnAccount {
		return "A local channel is removed by an account holding the channel's delete permission; the operator token is not an account."
	}
	return "A valid access token is required; log in at " + s.fullPath(loginPath) + " first."
}

// parseLocalChannelQuery validates page, per_page, q and the category filter
// the channels catalogue takes. The shared page/Q parser is strict about
// unknown parameters, so `category_ids` is taken out first and read on its own
// — the category and user listings keep rejecting it.
func parseLocalChannelQuery(q map[string][]string) (int, int, string, []int64, error) {
	categories, err := parseCategoryFilterIDs(q)
	if err != nil {
		return 0, 0, "", nil, err
	}
	pageQ := make(map[string][]string, len(q))
	for key, values := range q {
		if key == "category_ids" {
			continue
		}
		pageQ[key] = values
	}
	page, perPage, query, err := parsePageQuery(pageQ, channelstore.MaxCategoryDescriptionLen)
	if err != nil {
		return 0, 0, "", nil, err
	}
	return page, perPage, query, categories, nil
}

// channelsVisibleTo reads the records the listing may contain when the caller
// does not simply hold the global view.
//
// The SQL is scoped to the caller's own assignments: `user_channels_permission`
// joined to the record and to the caller's id, with an allow for the global
// view under no deny and a deny for the global view with denies. A record with
// no assignment is therefore never selected for a caller who may not see all of
// them. A store that cannot run the query lists the per-record grants instead.
//
// categoryIDs is the list's category filter: it narrows the same rows, and an
// empty set is no filter at all.
func (s *Server) channelsVisibleTo(ctx context.Context, store LocalChannelStore, authr *channelstore.Authorizer,
	q string, categoryIDs []int64, global bool, ids map[int64]bool) ([]channelstore.Channel, error) {

	query := channelstore.ChannelListQuery{Query: q, CategoryIDs: categoryIDs}
	userID := authr.UserID()
	if global {
		denied := authr.DeniedIDs(channelstore.KindChannel, channelstore.ActionView)
		if except, ok := store.(ChannelExcluder); ok {
			return except.ListChannelsExcept(ctx, query, denied)
		}
		return store.ListChannelsByIDs(ctx, allBut(idList(authr.ResourceIDs(channelstore.KindChannel, channelstore.ActionView)), denied))
	}
	if scoped, ok := store.(ChannelScope); ok && userID > 0 {
		return scoped.ListChannelsForUser(ctx, userID, query)
	}
	return store.ListChannelsByIDs(ctx, idList(ids))
}

// ChannelScope is the listing scoped to one caller's own assignments, run as
// one query.
type ChannelScope interface {
	ListChannelsForUser(ctx context.Context, userID int64, q channelstore.ChannelListQuery) ([]channelstore.Channel, error)
}

// ChannelExcluder is the store query for a global view narrowed by explicit
// denies. The gateway only requires ManagedStore; a store without it lists the
// per-record grants instead.
type ChannelExcluder interface {
	ListChannelsExcept(ctx context.Context, q channelstore.ChannelListQuery, excluded []int64) ([]channelstore.Channel, error)
}

// allBut removes the ids in without from ids.
func allBut(ids, without []int64) []int64 {
	drop := make(map[int64]bool, len(without))
	for _, id := range without {
		drop[id] = true
	}
	out := ids[:0]
	for _, id := range ids {
		if !drop[id] {
			out = append(out, id)
		}
	}
	return out
}

// visibleChannel is the card-list rule for one local channel record: the
// global view unless that record carries an explicit deny, and an explicit
// allow without it.
func visibleChannel(authr *channelstore.Authorizer, localID int64) bool {
	return visible(authr, channelstore.KindChannel, channelstore.ActionView, localID)
}

// localChannelMatches applies the list's q filter, over the same fields the
// repository searches.
func localChannelMatches(ch channelstore.Channel, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	return strings.Contains(strings.ToLower(ch.Title), q) ||
		strings.Contains(strings.ToLower(ch.Username), q) ||
		strings.Contains(strings.ToLower(ch.ClientKey), q)
}

// localLeaves serializes the leave route per local record within this process.
//
// Two concurrent leaves of one record would otherwise both reach Telegram, and
// a retry that arrives while the first is still running would be told the
// record is gone while it is about to be. The second waits, then sees the
// first's outcome. Across processes the daemon is the only writer of the
// database, so there is nothing more to coordinate.
var localLeaves = struct {
	mu    sync.Mutex
	locks map[int64]*leaveLock
}{locks: map[int64]*leaveLock{}}

type leaveLock struct {
	mu   sync.Mutex
	refs int
}

// acquireLeaveLock takes the lock for one record and returns its release.
func acquireLeaveLock(id int64) func() {
	localLeaves.mu.Lock()
	l := localLeaves.locks[id]
	if l == nil {
		l = &leaveLock{}
		localLeaves.locks[id] = l
	}
	l.refs++
	localLeaves.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		localLeaves.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(localLeaves.locks, id)
		}
		localLeaves.mu.Unlock()
	}
}

// listChannelCategories answers GET /local/channels/{id}/categories: the
// categories this channel is filed under.
func (s *Server) listChannelCategories(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	ch, ok := s.requireLocalChannel(w, r, id, channelstore.ActionView, requestID)
	if !ok {
		return
	}
	store, ok := s.channelCategoriesStore(w, requestID)
	if !ok {
		return
	}
	ids, err := store.AssignmentCounts(r.Context(), ch.ClientKey, strconv.FormatInt(ch.ChannelID, 10))
	if err != nil {
		s.storeError(w, "list_channel_categories", err, requestID)
		return
	}
	categoryIDs := make([]int64, 0, len(ids))
	for categoryID := range ids {
		categoryIDs = append(categoryIDs, categoryID)
	}
	sort.Slice(categoryIDs, func(i, j int) bool { return categoryIDs[i] < categoryIDs[j] })
	categories := make([]categoryView, 0, len(categoryIDs))
	if byIDs, ok := s.cfg.Categories.(CategoryByIDs); ok && len(categoryIDs) > 0 {
		found, err := byIDs.ListCategoriesByIDs(r.Context(), categoryIDs)
		if err != nil {
			s.storeError(w, "list_channel_categories", err, requestID)
			return
		}
		// Filing alone does not admit the category: each one is shown only if
		// the caller may see that category too.
		authr := actorAuth(r)
		for _, category := range found {
			if visible(authr, channelstore.KindCategory, channelstore.ActionView, category.ID) {
				categories = append(categories, categoryViewOf(category))
			}
		}
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"categories": categories},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// channelCategoriesRequest is the body for PUT /local/channels/{id}/categories.
type channelCategoriesRequest struct {
	CategoryIDs []int64 `json:"category_ids"`
}

// setChannelCategories answers PUT /local/channels/{id}/categories: the list is
// the complete set of categories the channel is filed under.
//
// The channel is reached through its own record, so a caller only files a
// channel it may see; each category must exist and be visible to it too, since
// filing is a change to what that category holds.
func (s *Server) setChannelCategories(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	ch, ok := s.requireLocalChannel(w, r, id, channelstore.ActionView, requestID)
	if !ok {
		return
	}
	store, ok := s.channelCategoriesStore(w, requestID)
	if !ok {
		return
	}
	var req channelCategoriesRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxCategoryBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if req.CategoryIDs == nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "category_ids is required.", requestID)
		return
	}
	seen := map[int64]bool{}
	for _, categoryID := range req.CategoryIDs {
		if categoryID <= 0 {
			s.fail(w, http.StatusBadRequest, CodeInvalidInput, "category_ids must be positive integers.", requestID)
			return
		}
		if seen[categoryID] {
			s.fail(w, http.StatusBadRequest, CodeInvalidInput, "category_ids lists a category twice.", requestID)
			return
		}
		seen[categoryID] = true
		if !s.categoryAction(w, r, categoryID, channelstore.ActionView, requestID) {
			return
		}
	}
	if err := store.ReplaceCategories(r.Context(), ch.ClientKey, ch.ChannelID, req.CategoryIDs); err != nil {
		s.categoryWriteError(w, "set_channel_categories", err, requestID)
		return
	}
	s.listChannelCategories(w, r, id, requestID)
}

// channelCategoriesStore returns the store that files channels under
// categories, or answers 503 when the local stores are not configured.
func (s *Server) channelCategoriesStore(w http.ResponseWriter, requestID string) (ChannelCategories, bool) {
	store, ok := s.cfg.Channels.(ChannelCategories)
	if s.cfg.Channels == nil || !ok || s.cfg.Categories == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"Local channel and category storage is not configured.", requestID)
		return nil, false
	}
	return store, true
}
