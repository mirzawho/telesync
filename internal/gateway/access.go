package gateway

import (
	"context"
	"net/http"
	"strconv"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// channelTarget identifies a Telegram channel named in the client namespace.
//
// Every Telegram route names a channel by its Telegram id. Authorization is
// against the local managed_channels row (its local id), so the Telegram id is
// resolved first; a channel with no local row cannot carry a per-channel
// assignment and is governed by the global "view channels" permission alone.
type channelTarget struct {
	telegramID int64
	// localID is the managed_channels row, zero when there is none.
	localID int64
	found   bool
}

// channelAccess is what one caller may do with one channel.
type channelAccess struct {
	// allowed is whether the caller may view it at all.
	allowed bool
}

// visible reports whether the caller may see one resource of kind whose local
// id is localID, for a resource that is recorded locally (localID > 0) or not
// (localID == 0: it carries no assignment and only the global view admits it).
//
// Every route that returns or names a resource asks this one question, so a
// list, a detail route, a nested route and a search cannot disagree: an
// explicit per-resource deny overrides the global view, the global view admits
// a resource with no assignment, and without the global view only an explicit
// allow does. A resource that does not exist is not visible.
func visible(authr *channelstore.Authorizer, kind channelstore.ResourceKind, action channelstore.Action, localID int64) bool {
	if authr == nil || localID < 0 {
		return false
	}
	if action == channelstore.ActionView && localID == 0 {
		// No local record: no assignment can exist, so the global view alone
		// decides. Asked as the global permission, never as resource 0, which
		// no assignment can name.
		return authr.CanList(kind)
	}
	return authr.Allowed(kind, action, localID)
}

// resolveTarget finds the local record behind a Telegram result, looked up
// batchwise for a whole listing so one page costs one query and not one per
// channel. A result with no record keeps localID 0: it carries no assignment,
// so only the global view admits it.
func (s *Server) resolveTargets(ctx context.Context, key string, telegramIDs []int64) (map[int64]int64, error) {
	store, ok := s.cfg.Channels.(TargetsByTelegramID)
	if s.cfg.Channels == nil || !ok || len(telegramIDs) == 0 {
		return map[int64]int64{}, nil
	}
	return store.LocalIDsByChannelID(ctx, key, telegramIDs)
}

// TargetsByTelegramID finds the local channel records behind Telegram IDs in
// one query. The gateway only requires ManagedStore; a store without this
// method resolves each id on its own.
type TargetsByTelegramID interface {
	LocalIDsByChannelID(ctx context.Context, clientKey string, channelIDs []int64) (map[int64]int64, error)
}

// targetOf resolves one Telegram channel on its own, for a route that names a
// single one.
func (s *Server) targetOf(ctx context.Context, key string, telegramID int64) (channelTarget, error) {
	t := channelTarget{telegramID: telegramID}
	if s.cfg.Channels == nil || telegramID <= 0 {
		return t, nil
	}
	row, ok, err := s.cfg.Channels.Get(ctx, key, telegramID)
	if err != nil {
		return t, err
	}
	t.found, t.localID = ok, row.ID
	return t, nil
}

// access decides a view.
//
// With the global "view channels" permission every channel is visible, except
// one whose local row carries an explicit view:deny. Without it, only a channel
// whose local row carries an explicit view:allow is visible; a Telegram channel
// with no local row has nowhere to store such an assignment and stays hidden.
func access(authr *channelstore.Authorizer, t channelTarget) channelAccess {
	localID := int64(0)
	if t.found {
		localID = t.localID
	}
	return channelAccess{allowed: visible(authr, channelstore.KindChannel, channelstore.ActionView, localID)}
}

// allowedAnyChannel reports whether the caller may see any channel at all.
func (s *Server) allowedAnyChannel(r *http.Request, action channelstore.Action) bool {
	authr := actorAuth(r)
	if action == channelstore.ActionView {
		return authr.CanList(channelstore.KindChannel) || authr.HasAny(channelstore.KindChannel)
	}
	return authr.HasAny(channelstore.KindChannel)
}

// requireChannelView authorizes GET-style access to one channel.
//
// The local record is looked up first: a channel that is not managed locally is
// governed by the global permission only. Metadata lookup failures are not fatal
// — the global permission alone still decides — but they are logged.
func (s *Server) requireChannelView(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key string, telegramID int64, requestID string) (channelTarget, bool) {

	t, err := s.targetOf(ctx, key, telegramID)
	if err != nil {
		s.logf("gateway: client=%s request_id=%s channel lookup failed: %v", key, requestID, err)
	}
	if !access(actorAuth(r), t).allowed {
		s.forbidden(w, requestID)
		return t, false
	}
	return t, true
}

// applyChannelFilter keeps the channels the caller may see and restates the
// page metadata, so `total` counts what the caller may see rather than what
// exists. With a global view and no denies nothing is removed.
func (s *Server) applyChannelFilter(ctx context.Context, r *http.Request, key string,
	result rpc.ChannelPage) (rpc.ChannelPage, error) {

	authr := actorAuth(r)
	kept := make([]rpc.Channel, 0, len(result.Channels))
	removed := 0
	global := authr.CanList(channelstore.KindChannel)
	if global && !authr.HasDenies(channelstore.KindChannel, channelstore.ActionView) {
		// Nothing is denied per channel, so no dialog has to be resolved: the
		// global view admits every one of them and the store is not read.
		return result, nil
	}
	ids := make([]int64, 0, len(result.Channels))
	for _, ch := range result.Channels {
		if ch.ID > 0 {
			ids = append(ids, ch.ID)
		}
	}
	local, err := s.resolveTargets(ctx, key, ids)
	if err != nil {
		return rpc.ChannelPage{}, err
	}
	for _, ch := range result.Channels {
		localID := local[ch.ID]
		// A dialog with no local record carries no assignment; visible() lets
		// only the global view admit it, so a restricted caller never does.
		if visible(authr, channelstore.KindChannel, channelstore.ActionView, localID) {
			kept = append(kept, ch)
		} else {
			removed++
		}
	}
	if removed > 0 {
		// total counted every dialog upstream returned, including the ones just
		// removed; it is restated as what the caller may see. has_more keeps
		// Telegram's answer, because paging continues upstream.
		page := result.Page
		page.Total = len(kept)
		result.Page = page
	}
	result.Channels = kept
	return result, nil
}

// requireChannelObject is requireChannelView for a route that names the channel
// by a decimal id in the path.
func (s *Server) requireChannelObject(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key, rawID, requestID string) (int64, bool) {

	channelID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || channelID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "channel_id must be a positive integer.", requestID)
		return 0, false
	}
	if _, ok := s.requireChannelView(ctx, w, r, key, channelID, requestID); !ok {
		return 0, false
	}
	return channelID, true
}
