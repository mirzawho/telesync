package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/mirza/telesync/internal/channelstore"
)

// statsPrefix is the dashboard's one counts route. There is nothing below it:
// the panel asks for four numbers in one request instead of four list calls.
const statsPrefix = "/api/v1/local/stats/dashboard"

// dashboardStatsView is the response's data.stats.
//
// Every field is a pointer and none is omitempty. A null field says the caller
// may not see that collection at all — "you may not look" and "there is nothing
// there" must not be told apart — while a number counts exactly the rows the
// matching list route would paginate through for the same caller. A missing key
// would read as null to a caller that does not check, so all four are always
// present.
type dashboardStatsView struct {
	Categories     *int `json:"categories"`
	Channels       *int `json:"channels"`
	WordPressSites *int `json:"wordpress_sites"`
	Administrators *int `json:"administrators"`
}

// dashboardStats serves GET /api/v1/local/stats/dashboard.
//
// Each collection is counted the way its list is read — the same visible()
// rule, so a number can never name a row the caller may not open — and is null
// when the caller may not see that collection at all. Accounts are the one
// exception: they are never row-narrowed, so the count is every account or
// nothing, decided by the global user right alone.
//
// The request never writes, marks anything read, or publishes.
func (s *Server) dashboardStats(w http.ResponseWriter, r *http.Request, requestID string) {
	// There is no collection below the route and no operation but the count.
	if rest := strings.TrimPrefix(r.URL.Path, s.fullPath(statsPrefix)); rest != "" && rest != "/" {
		s.dashboardMethod(w, requestID)
		return
	}
	if r.Method != http.MethodGet {
		s.dashboardMethod(w, requestID)
		return
	}
	// The three stores every count is drawn from. One that is missing is a 503,
	// never a count of 0: "not configured" and "nothing stored" are different
	// answers.
	if s.missingStatsStores() {
		s.statsUnconfigured(w, requestID)
		return
	}
	store, ok := s.cfg.Channels.(LocalChannelStore)
	if !ok {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"Local channel storage is not configured.", requestID)
		return
	}
	// A registered site store the request could not read is the same 503 the
	// WordPress routes answer. With no site store at all the route still
	// answers: the sites are simply not shown.
	if s.cfg.WordPress != nil && (s.cfg.Secrets == nil || s.cfg.WordPressClient == nil) {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"WordPress site storage is not configured.", requestID)
		return
	}

	ctx := r.Context()
	authr := actorAuth(r)
	stats := dashboardStatsView{}

	if authr.CanList(channelstore.KindCategory) || authr.HasAny(channelstore.KindCategory) {
		_, total, err := s.visibleCategories(ctx, authr, channelstore.ListQuery{Page: 1, PerPage: 1})
		if err != nil {
			s.storeError(w, "dashboard_categories", err, requestID)
			return
		}
		stats.Categories = &total
	}
	if authr.CanList(channelstore.KindChannel) || authr.HasAny(channelstore.KindChannel) {
		total, err := s.visibleChannelCount(ctx, store, authr)
		if err != nil {
			s.storeError(w, "dashboard_channels", err, requestID)
			return
		}
		stats.Channels = &total
	}
	if s.cfg.WordPress != nil && (authr.CanList(channelstore.KindWordPress) || authr.HasAny(channelstore.KindWordPress)) {
		sites, err := s.cfg.WordPress.ListWordPressSites(ctx)
		if err != nil {
			s.storeError(w, "dashboard_wordpress_sites", err, requestID)
			return
		}
		total := 0
		for _, rec := range sites {
			if visible(authr, channelstore.KindWordPress, channelstore.ActionView, rec.ID) {
				total++
			}
		}
		stats.WordPressSites = &total
	}
	if authr.Global(channelstore.PermissionUsersView) || authr.Global(channelstore.PermissionUsersManage) {
		_, total, err := s.cfg.Users.ListUsers(ctx, channelstore.UserFilter{Page: 1, PerPage: 20})
		if err != nil {
			s.storeError(w, "dashboard_administrators", err, requestID)
			return
		}
		stats.Administrators = &total
	}

	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"stats": stats},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// dashboardMethod is the route's single 405: the count is one GET on the route
// itself, so anything else — a method, or a path below it — is refused.
func (s *Server) dashboardMethod(w http.ResponseWriter, requestID string) {
	w.Header().Set("Allow", http.MethodGet)
	s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
		"Use GET to read the dashboard counts.", requestID)
}

// missingStatsStores reports whether one of the three stores the counts are
// drawn from is not configured.
func (s *Server) missingStatsStores() bool {
	return s.cfg.Users == nil || s.cfg.Channels == nil || s.cfg.Categories == nil
}

// statsUnconfigured is the 503 for a deployment without those stores.
func (s *Server) statsUnconfigured(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
		"Dashboard counts need the local stores to be configured.", requestID)
}

// visibleChannelCount is the local channel list's total for one caller: how many
// managed channels it may view.
//
// It mirrors listLocalChannels' branch exactly, so the dashboard's number and
// the list's meta.total cannot disagree: the global view counts every record
// unless one carries an explicit deny, and without it the count is the records
// with an explicit view:allow.
func (s *Server) visibleChannelCount(ctx context.Context, store LocalChannelStore,
	authr *channelstore.Authorizer) (int, error) {

	global := authr.CanList(channelstore.KindChannel)
	if global && !authr.HasDenies(channelstore.KindChannel, channelstore.ActionView) {
		// Nothing is denied per channel, so the repository counts them all.
		_, total, err := store.ListChannels(ctx, channelstore.ChannelListQuery{})
		return total, err
	}
	all, err := s.channelsVisibleTo(ctx, store, authr, "", nil, global,
		authr.ResourceIDs(channelstore.KindChannel, channelstore.ActionView))
	if err != nil {
		return 0, err
	}
	total := 0
	for _, ch := range all {
		if visibleChannel(authr, ch.ID) {
			total++
		}
	}
	return total, nil
}
