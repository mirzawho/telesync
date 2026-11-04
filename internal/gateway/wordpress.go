package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/wordpress"
)

// WordPressStore is the local WordPress site repository.
type WordPressStore interface {
	CreateWordPressSite(ctx context.Context, rec channelstore.WordPressSite) (channelstore.WordPressSite, error)
	GetWordPressSite(ctx context.Context, id int64) (channelstore.WordPressSite, bool, error)
	ListWordPressSites(ctx context.Context) ([]channelstore.WordPressSite, error)
	UpdateWordPressSite(ctx context.Context, rec channelstore.WordPressSite) (channelstore.WordPressSite, error)
	DeleteWordPressSite(ctx context.Context, id int64) error
}

// WordPressClient is the remote side of a registration: it authenticates
// against the site and reads its title and icon.
type WordPressClient interface {
	Probe(ctx context.Context, baseURL, username, password string) (wordpress.SiteInfo, error)
	CreatePost(ctx context.Context, baseURL, username, password string, in wordpress.PostInput) (wordpress.PostResult, error)
	UploadMedia(ctx context.Context, baseURL, username, password, filename, mimeType string, data []byte) (wordpress.MediaRef, error)
	FindTag(ctx context.Context, baseURL, username, password, name string) (wordpress.TagRef, bool, error)
	CreateTag(ctx context.Context, baseURL, username, password, name string) (wordpress.TagRef, error)
	// ListTerms reads one page of a site's categories or tags, so a publishing
	// form can offer the destination's own terms rather than guessing at them.
	ListTerms(ctx context.Context, baseURL, username, password, taxonomy, search string, page, perPage int) (wordpress.TermPage, error)
}

// SecretBox seals a credential for storage and opens a stored one.
//
// A web interface may implement it with a package-level encryptor, which the
// different method sets cannot express.
type SecretBox interface {
	Seal(plaintext string) (secrets.Box, error)
	Open(box secrets.Box) (string, error)
}

// WordPressCategories is the store's site↔category link. The gateway needs it
// for the site routes that carry categories; without it a site view reports an
// empty list and a request that names categories is refused.
type WordPressCategories interface {
	ReplaceSiteCategories(ctx context.Context, siteID int64, categoryIDs []int64) error
	SiteCategories(ctx context.Context, siteID int64) ([]int64, error)
	CategoriesOfSites(ctx context.Context, siteIDs []int64) (map[int64][]int64, error)
}

// WordPressSiteService is the reusable half of the site management: the only
// code that opens a stored credential. A future publishing task calls it to get
// a site's password and never sees one through the HTTP API.
type WordPressSiteService interface {
	// Credential returns a site's application password, decrypted.
	Credential(ctx context.Context, id int64) (channelstore.WordPressSite, string, error)
}

// Credential returns one site's decrypted credential. Only backend code that is
// about to present it to the site may call this; no route does.
func (s *Server) Credential(ctx context.Context, id int64) (channelstore.WordPressSite, string, error) {
	if s.cfg.WordPress == nil || s.cfg.Secrets == nil {
		return channelstore.WordPressSite{}, "", errWordPressUnconfigured
	}
	rec, found, err := s.cfg.WordPress.GetWordPressSite(ctx, id)
	if err != nil {
		return channelstore.WordPressSite{}, "", err
	}
	if !found {
		return channelstore.WordPressSite{}, "", channelstore.ErrSiteNotFound
	}
	password, err := s.cfg.Secrets.Open(rec.Credential)
	if err != nil {
		return channelstore.WordPressSite{}, "", err
	}
	return rec, password, nil
}

var errWordPressUnconfigured = errors.New("gateway: wordpress sites are not configured")

// wordpressPrefix is the route; the local site id follows it when present.
const wordpressPrefix = "/api/v1/local/wordpress"

// maxWordpressBody bounds a registration body. The longest field is a URL.
const maxWordpressBody = 16 << 10

// wordpressSiteView is the public shape of one registered site.
//
// It never carries the password, the sealed credential, its nonce or the
// authorization header: only what a management list shows.
type wordpressSiteView struct {
	ID int64 `json:"id"`
	// URL is the canonical site URL the record was registered with.
	URL string `json:"url"`
	// Domain is that URL's host, for display.
	Domain   string  `json:"domain"`
	Title    string  `json:"title"`
	IconURL  *string `json:"site_icon_url"`
	Username string  `json:"username"`
	// CategoryIDs are the global categories the site publishes into, by id;
	// the management UI resolves them against GET /api/v1/local/categories.
	CategoryIDs []int64 `json:"category_ids"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func wordpressSiteViewOf(rec channelstore.WordPressSite) wordpressSiteView {
	return wordpressSiteViewWith(rec, nil)
}

func wordpressSiteViewWith(rec channelstore.WordPressSite, categoryIDs []int64) wordpressSiteView {
	domain := rec.URL
	if u, err := url.Parse(rec.URL); err == nil && u.Hostname() != "" {
		domain = u.Hostname()
	}
	return wordpressSiteView{
		ID:          rec.ID,
		URL:         rec.URL,
		Domain:      domain,
		Title:       rec.Title,
		IconURL:     rec.IconURL,
		Username:    rec.Username,
		CategoryIDs: append([]int64{}, categoryIDs...),
		CreatedAt:   RFC3339String(rec.CreatedAt),
		UpdatedAt:   RFC3339String(rec.UpdatedAt),
	}
}

// wordpressRequest is the accepted body for create and update.
//
// A nil field on update is left unchanged; on create url, username and
// password are all required.
//
// Categories: on create, CategoryIDs files the site; on update the three
// category fields are mutually exclusive — CategoryIDs replaces the whole set,
// AddCategoryIDs and RemoveCategoryIDs add to or drop from it, so one category
// can be toggled without listing every other one.
type wordpressRequest struct {
	URL               *string `json:"url"`
	Username          *string `json:"username"`
	Password          *string `json:"password"`
	CategoryIDs       []int64 `json:"category_ids"`
	AddCategoryIDs    []int64 `json:"add_category_ids"`
	RemoveCategoryIDs []int64 `json:"remove_category_ids"`
}

// categoryChange is the validated category part of a request.
type categoryChange struct {
	// kind is "set", "add", "remove" or "" (nothing to do).
	kind string
	ids  []int64
}

// categoriesOf validates the category fields of req for create or update.
//
// On update at most one of the three forms may be given: they answer different
// questions, and a request that gives two leaves the order between them
// undefined.
func categoriesOf(req wordpressRequest, update bool) (categoryChange, error) {
	given := 0
	for _, ids := range [][]int64{req.CategoryIDs, req.AddCategoryIDs, req.RemoveCategoryIDs} {
		if ids != nil {
			given++
		}
	}
	switch {
	case given == 0:
		return categoryChange{}, nil
	case given > 1:
		return categoryChange{}, errors.New("give only one of category_ids, add_category_ids or remove_category_ids.")
	case !update && req.CategoryIDs == nil:
		return categoryChange{}, errors.New("category_ids files a new site; add_category_ids and remove_category_ids are for an update.")
	}
	change := categoryChange{ids: req.CategoryIDs}
	switch {
	case req.CategoryIDs != nil:
		change.kind = "set"
	case req.AddCategoryIDs != nil:
		change.kind, change.ids = "add", req.AddCategoryIDs
	case req.RemoveCategoryIDs != nil:
		change.kind, change.ids = "remove", req.RemoveCategoryIDs
	}
	for _, id := range change.ids {
		if id <= 0 {
			return categoryChange{}, errors.New("category ids must be positive integers.")
		}
	}
	return change, nil
}

// categoryStore is the store's category link, or nil when it does not
// implement it.
func (s *Server) categoryStore() WordPressCategories {
	store, _ := s.cfg.WordPress.(WordPressCategories)
	return store
}

// categoryError maps a link failure that is the caller's to fix.
func (s *Server) categoryError(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, channelstore.ErrCategoryNotFound):
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"One of the categories does not exist.", requestID)
	case errors.Is(err, channelstore.ErrSiteNotFound):
		s.wordpressSiteNotFound(w, requestID)
	default:
		s.storeError(w, "wordpress_site_categories", err, requestID)
	}
}

// wordpress routes one request whose path is under wordpressPrefix. The caller
// is already authenticated.
func (s *Server) wordpress(w http.ResponseWriter, r *http.Request, requestID string) {
	if s.cfg.WordPress == nil || s.cfg.WordPressClient == nil || s.cfg.Secrets == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"WordPress site storage is not configured.", requestID)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, s.fullPath(wordpressPrefix))
	authr := actorAuth(r)
	canManage := authr.Global(channelstore.PermissionWordPressManage)

	switch rest {
	case "", "/":
		switch r.Method {
		case http.MethodGet:
			if !authr.CanList(channelstore.KindWordPress) && !authr.HasAny(channelstore.KindWordPress) {
				s.forbidden(w, requestID)
				return
			}
			s.listWordPressSites(w, r, requestID)
		case http.MethodPost:
			if !canManage {
				s.forbidden(w, requestID)
				return
			}
			s.createWordPressSite(w, r, requestID)
		default:
			w.Header().Set("Allow", "GET, POST")
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Use GET to list WordPress sites or POST to register one.", requestID)
		}
		return
	}

	raw := rest[1:]
	if strings.Contains(raw, "/") {
		// Under a site there is the publishing route and the two taxonomy
		// listings a publishing form needs. Nothing else is nested.
		for _, nested := range []struct {
			suffix string
			method string
		}{
			{"/posts", http.MethodPost},
			{"/categories", http.MethodGet},
			{"/tags", http.MethodGet},
		} {
			if !strings.HasSuffix(raw, nested.suffix) {
				continue
			}
			if r.Method != nested.method {
				w.Header().Set("Allow", nested.method)
				s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
					fmt.Sprintf("Use %s for /wordpress/{id}%s.", nested.method, nested.suffix), requestID)
				return
			}
			id, err := parseID(strings.TrimSuffix(raw, nested.suffix))
			if err != nil {
				s.fail(w, http.StatusBadRequest, CodeInvalidInput, "id must be a positive integer.", requestID)
				return
			}
			switch nested.suffix {
			case "/posts":
				s.publishWordPress(w, r, id, requestID)
			case "/categories":
				s.wordPressTerms(w, r, id, "categories", requestID)
			default:
				s.wordPressTerms(w, r, id, "tags", requestID)
			}
			return
		}
		s.notFound(w, requestID)
		return
	}
	// The method decides first, so an unsupported one is a 405 whatever the id
	// looks like.
	switch r.Method {
	case http.MethodGet, http.MethodPatch, http.MethodDelete:
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"Use GET for one WordPress site, PATCH to update it or DELETE to remove it.", requestID)
		return
	}
	id, err := parseID(raw)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "id must be a positive integer.", requestID)
		return
	}
	// A site the caller may not touch is 404 before anything else it sent is
	// read: a registration the caller cannot see must not be told apart from
	// one that does not exist, or whether the body it sent was valid.
	action := channelstore.ActionView
	if r.Method != http.MethodGet {
		action = channelstore.ActionUpdate
		if r.Method == http.MethodDelete {
			action = channelstore.ActionDelete
		}
		if !canManage {
			s.forbidden(w, requestID)
			return
		}
	}
	if _, found, err := s.cfg.WordPress.GetWordPressSite(r.Context(), id); err != nil {
		s.storeError(w, "get_wordpress_site", err, requestID)
		return
	} else if !found || !siteViewer(authr, action, id) || !siteViewer(authr, channelstore.ActionView, id) {
		s.wordpressSiteNotFound(w, requestID)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getWordPressSite(w, r, id, requestID)
	case http.MethodPatch:
		s.updateWordPressSite(w, r, id, requestID, action)
	default:
		s.deleteWordPressSite(w, r, id, requestID)
	}
}

// wordPressTerms serves GET /api/v1/local/wordpress/{id}/categories and /tags.
//
// It reads the destination site's own taxonomy through the backend's WordPress
// connection, so the browser never holds a WordPress credential and no
// registered site is called from the page directly. The terms are the site's
// rows: their ids mean something only on that site, which is why the form drops
// them when the site changes.
func (s *Server) wordPressTerms(w http.ResponseWriter, r *http.Request, id int64, taxonomy, requestID string) {
	ctx := r.Context()
	a := actorOf(r)
	site, password, err := s.credentialFor(ctx, a, id)
	if err != nil {
		s.publishFailure(w, err, requestID)
		return
	}

	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	search := strings.TrimSpace(q.Get("search"))

	result, err := s.cfg.WordPressClient.ListTerms(ctx, site.URL, site.Username, password, taxonomy, search, page, perPage)
	if err != nil {
		s.publishFailure(w, mapWordPressReadError(err), requestID)
		return
	}

	terms := result.Terms
	if terms == nil {
		terms = []wordpress.TermRef{}
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{
			"terms": terms,
			"page": map[string]any{
				"page":        result.Page,
				"per_page":    result.PerPage,
				"total":       result.Total,
				"total_pages": result.TotalPages,
				"has_more":    result.Page < result.TotalPages,
			},
		},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// mapWordPressReadError turns a read failure into the route's answer. A read
// that the site refused is reported as what it is: it never becomes a
// publishing failure, and no credential is mentioned in the answer.
func mapWordPressReadError(err error) *publishError {
	switch {
	case errors.Is(err, wordpress.ErrUnauthorized):
		return &publishError{http.StatusUnauthorized, CodePublishWordPressAuth,
			"The WordPress credentials were refused."}
	case errors.Is(err, wordpress.ErrForbidden):
		return &publishError{http.StatusForbidden, CodePublishWordPressForbidden,
			"The WordPress user may not read this taxonomy."}
	case errors.Is(err, wordpress.ErrTimeout):
		return &publishError{http.StatusGatewayTimeout, CodePublishWordPressTimeout,
			"The WordPress site did not answer in time."}
	case errors.Is(err, wordpress.ErrUnreachable), errors.Is(err, wordpress.ErrBlockedAddress):
		return &publishError{http.StatusBadGateway, CodePublishWordPressUnreachable,
			"The WordPress site could not be reached."}
	case errors.Is(err, wordpress.ErrIncompatible):
		return &publishError{http.StatusBadGateway, CodePublishWordPressIncompatible,
			"The WordPress site did not answer as a REST API."}
	}
	return &publishError{http.StatusBadGateway, CodePublishFailed,
		"The WordPress taxonomy could not be read."}
}

// siteViewer authorizes one action on one site.
//
// It is the visible() rule every resource follows: an explicit per-site
// assignment decides, then the global permission, and an explicit deny takes
// the write away with the view. Updating or deleting a site is the global
// manage right, narrowed to the sites the caller was granted, and it also
// needs the view: an account that may not see a site may not change it.
//
// Writing a site is additionally the global manage right at the route, which
// is where a per-site grant is kept from replacing a credential on its own.
func siteViewer(authr *channelstore.Authorizer, action channelstore.Action, id int64) bool {
	return visible(authr, channelstore.KindWordPress, action, id)
}

func (s *Server) getWordPressSite(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	rec, found, err := s.cfg.WordPress.GetWordPressSite(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_wordpress_site", err, requestID)
		return
	}
	if !found || !siteViewer(actorAuth(r), channelstore.ActionView, id) {
		s.wordpressSiteNotFound(w, requestID)
		return
	}
	var categories []int64
	if links := s.categoryStore(); links != nil {
		bySite, err := links.CategoriesOfSites(r.Context(), []int64{id})
		if err != nil {
			s.categoryError(w, err, requestID)
			return
		}
		categories = bySite[id]
	}
	view := wordpressSiteViewWith(rec, categories)
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"site": view},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// parseWordPressListQuery validates the list's search text and category filter.
//
// Unlike the paginated listings this collection has no page, per_page or q
// parameter set of its own: the site list is returned in full and filtered
// before the permissions drop what the caller may not see.
func parseWordPressListQuery(values map[string][]string) (string, []int64, error) {
	query := ""
	for key, raw := range values {
		if key == "category_ids" {
			continue
		}
		if key != "q" {
			return "", nil, errors.New("unknown query parameter " + strconv.Quote(key) + ".")
		}
		if len(raw) != 1 {
			return "", nil, errors.New("q must be given exactly once.")
		}
		query = raw[0]
	}
	if len([]rune(query)) > channelstore.MaxCategoryDescriptionLen {
		return "", nil, errors.New("q must be at most " +
			strconv.Itoa(channelstore.MaxCategoryDescriptionLen) + " characters.")
	}
	categories, err := parseCategoryFilterIDs(values)
	if err != nil {
		return "", nil, err
	}
	return query, categories, nil
}

// wordpressSiteMatches applies the list's search text to the fields a
// management list shows: the title and the site's own URL, which is where the
// domain comes from. It is matched literally, ignoring case.
func wordpressSiteMatches(rec channelstore.WordPressSite, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	domain := rec.URL
	if u, err := url.Parse(rec.URL); err == nil && u.Hostname() != "" {
		domain = u.Hostname()
	}
	return strings.Contains(strings.ToLower(rec.Title), q) ||
		strings.Contains(strings.ToLower(rec.URL), q) ||
		strings.Contains(strings.ToLower(domain), q)
}

// siteCategoryFilter builds the set of site ids a category filter admits, or
// nil when there is no filter. An empty set is no filter, not "nothing".
func (s *Server) siteCategoryFilter(ctx context.Context, ids []int64) (map[int64]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	bySite := map[int64]bool{}
	for _, categoryID := range ids {
		// The category's own membership list is the one query that reads the
		// link table; a site filed under any of the selected categories
		// (OR semantics) is admitted.
		rows, err := s.wordPressSiteCategoryRows(ctx, categoryID)
		if err != nil {
			return nil, err
		}
		for _, siteID := range rows {
			bySite[siteID] = true
		}
	}
	return bySite, nil
}

// wordPressCategoryMembers is the store's category membership listing for
// sites. The gateway only needs it while filtering a list; a store without it
// answers a category filter with a 503 rather than silently dropping the
// filter and inventing a broader answer.
type wordPressCategoryMembers interface {
	SiteCategoryMembers(ctx context.Context, categoryID int64) ([]int64, error)
}

// wordPressSiteCategoryRows reads one category's site membership, or reports
// that the store cannot answer the filter at all.
func (s *Server) wordPressSiteCategoryRows(ctx context.Context, categoryID int64) ([]int64, error) {
	members, ok := s.cfg.Channels.(wordPressCategoryMembers)
	if !ok {
		return nil, errors.New("WordPress site categories are not configured.")
	}
	return members.SiteCategoryMembers(ctx, categoryID)
}

func (s *Server) listWordPressSites(w http.ResponseWriter, r *http.Request, requestID string) {
	query, categoryIDs, err := parseWordPressListQuery(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	sites, err := s.cfg.WordPress.ListWordPressSites(r.Context())
	if err != nil {
		s.storeError(w, "list_wordpress_sites", err, requestID)
		return
	}
	// The global view admits every site except one with an explicit
	// view:deny; without it only a site with an explicit view:allow is
	// listed. Filtering the slice is enough because the collection is not
	// paginated, so no total or page ever counts a hidden site. The search
	// text and the category filter narrow the same slice first, so the
	// answer is still the caller's own visibility and never a wider one.
	filtered, err := s.siteCategoryFilter(r.Context(), categoryIDs)
	if err != nil {
		s.categoryError(w, err, requestID)
		return
	}
	authr := actorAuth(r)
	siteVisible := sites[:0]
	for _, rec := range sites {
		if !wordpressSiteMatches(rec, query) {
			continue
		}
		if filtered != nil && !filtered[rec.ID] {
			continue
		}
		if visible(authr, channelstore.KindWordPress, channelstore.ActionView, rec.ID) {
			siteVisible = append(siteVisible, rec)
		}
	}
	sites = siteVisible
	bySite := map[int64][]int64{}
	if links := s.categoryStore(); links != nil {
		ids := make([]int64, 0, len(sites))
		for _, rec := range sites {
			ids = append(ids, rec.ID)
		}
		bySite, err = links.CategoriesOfSites(r.Context(), ids)
		if err != nil {
			s.categoryError(w, err, requestID)
			return
		}
	}
	views := make([]wordpressSiteView, 0, len(sites))
	for _, rec := range sites {
		views = append(views, wordpressSiteViewWith(rec, bySite[rec.ID]))
	}
	// The collection is not paginated, so total is simply the number of sites
	// the caller may see — the same number the dashboard's count reports.
	meta := metaFor(requestID, s.cfg.Now())
	meta["total"] = len(sites)
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"sites": views},
		Meta: meta,
	})
}

func (s *Server) createWordPressSite(w http.ResponseWriter, r *http.Request, requestID string) {
	if !actorAuth(r).Global(channelstore.PermissionWordPressManage) {
		s.forbidden(w, requestID)
		return
	}
	req, err := decodeWordpressRequest(w, r)
	if err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if req.URL == nil || req.Username == nil || req.Password == nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "url, username and password are required.", requestID)
		return
	}
	if strings.TrimSpace(*req.Username) == "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "username must not be empty.", requestID)
		return
	}
	if *req.Password == "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "password must not be empty.", requestID)
		return
	}
	change, err := categoriesOf(req, false)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	links := s.categoryStore()
	if change.kind != "" && links == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"WordPress site categories are not configured.", requestID)
		return
	}
	base, err := wordpress.NormalizeBaseURL(*req.URL)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}

	info, err := s.cfg.WordPressClient.Probe(r.Context(), base, strings.TrimSpace(*req.Username), *req.Password)
	if err != nil {
		s.wordpressProbeError(w, "create_wordpress_site", base, err, requestID)
		return
	}
	box, err := s.cfg.Secrets.Seal(*req.Password)
	if err != nil {
		s.storeError(w, "seal_wordpress_credential", err, requestID)
		return
	}
	saved, err := s.cfg.WordPress.CreateWordPressSite(r.Context(), channelstore.WordPressSite{
		URL:        base,
		Title:      info.Title,
		IconURL:    info.SiteIconURL,
		Username:   strings.TrimSpace(*req.Username),
		Credential: box,
	})
	if err != nil {
		s.wordpressStoreError(w, "create_wordpress_site", err, requestID)
		return
	}
	// A category the site does not get is no registration at all: the site is
	// written first because its id is what the link refers to, and undone when
	// a category turns out not to exist.
	ids := []int64{}
	if change.kind == "set" && len(change.ids) > 0 {
		if err := links.ReplaceSiteCategories(r.Context(), saved.ID, change.ids); err != nil {
			if undoErr := s.cfg.WordPress.DeleteWordPressSite(context.Background(), saved.ID); undoErr != nil {
				s.logf("gateway: op=create_wordpress_site request_id=%s site=%d: undoing a failed registration: %v",
					requestID, saved.ID, undoErr)
			}
			s.categoryError(w, err, requestID)
			return
		}
	}
	if links != nil {
		bySite, err := links.CategoriesOfSites(r.Context(), []int64{saved.ID})
		if err != nil {
			s.categoryError(w, err, requestID)
			return
		}
		ids = bySite[saved.ID]
	}
	s.siteSaved(w, http.StatusCreated, wordpressSiteViewWith(saved, ids), requestID)
}

// siteSaved answers a successful registration or update.
//
// The answer is a complete document of its own: the redirect the rest of the
// local API answers a write with is a 3xx, which a fetch-based client reads as
// "not ok", so the caller is told what to do through a status and fields
// instead.
func (s *Server) siteSaved(w http.ResponseWriter, status int, view wordpressSiteView, requestID string) {
	writeJSON(w, status, envelope{
		Data: map[string]any{
			"site": view,
			"saved": map[string]any{
				"id":          view.ID,
				"refresh":     true,
				"redirect_to": fmt.Sprintf("%s/%d", wordpressPrefix, view.ID),
			},
		},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// action is the right the routing layer already checked for the site; an
// explicit deny of it was answered with 404 before this ran.
func (s *Server) updateWordPressSite(w http.ResponseWriter, r *http.Request, id int64, requestID string, action channelstore.Action) {
	if !actorAuth(r).Global(channelstore.PermissionWordPressManage) {
		s.forbidden(w, requestID)
		return
	}
	if !siteViewer(actorAuth(r), action, id) || !siteViewer(actorAuth(r), channelstore.ActionView, id) {
		s.wordpressSiteNotFound(w, requestID)
		return
	}
	req, err := decodeWordpressRequest(w, r)
	if err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	change, err := categoriesOf(req, true)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	links := s.categoryStore()
	if change.kind != "" && links == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"WordPress site categories are not configured.", requestID)
		return
	}
	// Categories on their own are a complete update: they are checked and
	// written without asking the remote site anything, since the record's
	// own fields do not change.
	if req.URL == nil && req.Username == nil && req.Password == nil {
		if change.kind == "" {
			s.fail(w, http.StatusBadRequest, CodeInvalidInput,
				"Give at least one of url, username, password or categories.", requestID)
			return
		}
		if _, found, err := s.cfg.WordPress.GetWordPressSite(r.Context(), id); err != nil {
			s.storeError(w, "get_wordpress_site", err, requestID)
			return
		} else if !found {
			s.wordpressSiteNotFound(w, requestID)
			return
		}
		if err := s.applyCategoryChange(r.Context(), links, id, change); err != nil {
			s.categoryError(w, err, requestID)
			return
		}
		rec, _, err := s.cfg.WordPress.GetWordPressSite(r.Context(), id)
		if err != nil {
			s.storeError(w, "get_wordpress_site", err, requestID)
			return
		}
		bySite, err := links.CategoriesOfSites(r.Context(), []int64{id})
		if err != nil {
			s.categoryError(w, err, requestID)
			return
		}
		s.siteSaved(w, http.StatusOK, wordpressSiteViewWith(rec, bySite[id]), requestID)
		return
	}
	if err := checkOptionalText(req.Username, "username"); err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	if err := checkOptionalText(req.Password, "password"); err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}

	current, found, err := s.cfg.WordPress.GetWordPressSite(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_wordpress_site", err, requestID)
		return
	}
	if !found {
		s.wordpressSiteNotFound(w, requestID)
		return
	}

	// The next state is built from the current record first, so every field
	// the request omits keeps its stored value and its stored credential.
	next := current
	if req.URL != nil {
		base, err := wordpress.NormalizeBaseURL(*req.URL)
		if err != nil {
			s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
			return
		}
		next.URL = base
	}
	if req.Username != nil {
		next.Username = strings.TrimSpace(*req.Username)
	}
	if req.Password != nil {
		box, err := s.cfg.Secrets.Seal(*req.Password)
		if err != nil {
			s.storeError(w, "seal_wordpress_credential", err, requestID)
			return
		}
		next.Credential = box
	}

	// The credential the probe presents is the one that will be stored: a new
	// password if given, otherwise the current one, which the key opens.
	password := ""
	if req.Password != nil {
		password = *req.Password
	} else {
		password, err = s.cfg.Secrets.Open(current.Credential)
		if err != nil {
			s.logf("gateway: op=update_wordpress_site request_id=%s site=%d: the stored credential cannot be opened: %v",
				requestID, id, err)
			s.wordpressProbeError(w, "update_wordpress_site", next.URL, err, requestID)
			return
		}
	}

	info, err := s.cfg.WordPressClient.Probe(r.Context(), next.URL, next.Username, password)
	if err != nil {
		s.wordpressProbeError(w, "update_wordpress_site", next.URL, err, requestID)
		return
	}
	// Title and icon always come from the site that just answered. An icon it
	// no longer reports replaces the old one rather than leaving a stale URL.
	next.Title = info.Title
	next.IconURL = info.SiteIconURL

	saved, err := s.cfg.WordPress.UpdateWordPressSite(r.Context(), next)
	if err != nil {
		s.wordpressStoreError(w, "update_wordpress_site", err, requestID)
		return
	}
	// The record is already written; a category it was given replaces the old
	// set. A category that does not exist is reported and the stored set is
	// left as it was, rather than half-applied.
	var categories []int64
	if links != nil {
		if err := s.applyCategoryChange(r.Context(), links, id, change); err != nil {
			s.categoryError(w, err, requestID)
			return
		}
	}
	if links != nil {
		bySite, err := links.CategoriesOfSites(r.Context(), []int64{id})
		if err != nil {
			s.categoryError(w, err, requestID)
			return
		}
		categories = bySite[id]
	}
	s.siteSaved(w, http.StatusOK, wordpressSiteViewWith(saved, categories), requestID)
}

// applyCategoryChange writes one category change for a site.
//
// "set" replaces the site's categories; "add" and "remove" read the current
// set and write it with the ids added or dropped, so toggling one category
// never touches the others.
func (s *Server) applyCategoryChange(ctx context.Context, links WordPressCategories, siteID int64, change categoryChange) error {
	switch change.kind {
	case "set":
		return links.ReplaceSiteCategories(ctx, siteID, change.ids)
	case "add", "remove":
		current, err := links.SiteCategories(ctx, siteID)
		if err != nil {
			return err
		}
		if change.kind == "add" {
			return links.ReplaceSiteCategories(ctx, siteID, append(current, change.ids...))
		}
		drop := map[int64]bool{}
		for _, id := range change.ids {
			drop[id] = true
		}
		kept := make([]int64, 0, len(current))
		for _, id := range current {
			if !drop[id] {
				kept = append(kept, id)
			}
		}
		return links.ReplaceSiteCategories(ctx, siteID, kept)
	}
	return nil
}

func (s *Server) deleteWordPressSite(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	if !actorAuth(r).Global(channelstore.PermissionWordPressManage) {
		s.forbidden(w, requestID)
		return
	}
	if !siteViewer(actorAuth(r), channelstore.ActionDelete, id) || !siteViewer(actorAuth(r), channelstore.ActionView, id) {
		s.wordpressSiteNotFound(w, requestID)
		return
	}
	// Only the local registration goes: nothing on the remote site is touched,
	// and no other local table references it.
	if err := s.cfg.WordPress.DeleteWordPressSite(context.Background(), id); err != nil {
		if errors.Is(err, channelstore.ErrSiteNotFound) {
			s.wordpressSiteNotFound(w, requestID)
			return
		}
		s.storeError(w, "delete_wordpress_site", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"deleted": true, "id": id, "redirect_to": wordpressPrefix},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// checkOptionalText rejects a supplied but blank value.
func checkOptionalText(v *string, field string) error {
	if v != nil && strings.TrimSpace(*v) == "" {
		return errors.New(field + " must not be empty.")
	}
	return nil
}

func decodeWordpressRequest(w http.ResponseWriter, r *http.Request) (wordpressRequest, error) {
	var req wordpressRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxWordpressBody); err != nil {
		if errors.Is(err, errUnsupportedMedia) {
			return wordpressRequest{}, err
		}
		return wordpressRequest{}, errors.New("the body must be one JSON object with url, username and password.")
	}
	return req, nil
}

func (s *Server) wordpressSiteNotFound(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusNotFound, CodeNotFound, "No WordPress site has that id.", requestID)
}

// wordpressProbeError maps a failed validation against the remote site.
//
// The message states what the site answered; the error it came from is logged
// without the credential, which none of these errors carries.
func (s *Server) wordpressProbeError(w http.ResponseWriter, op, site string, err error, requestID string) {
	s.logf("gateway: op=%s request_id=%s site=%q: %v", op, requestID, site, err)
	switch {
	case errors.Is(err, wordpress.ErrInvalidURL):
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
	case errors.Is(err, wordpress.ErrBlockedAddress):
		s.fail(w, http.StatusBadRequest, CodeWordPressBlocked,
			"The site URL resolves to an address this server refuses to connect to.", requestID)
	case errors.Is(err, wordpress.ErrUnauthorized):
		s.fail(w, http.StatusUnauthorized, CodeWordPressAuth,
			"The site refused the username or application password (HTTP 401).", requestID)
	case errors.Is(err, wordpress.ErrForbidden):
		s.fail(w, http.StatusForbidden, CodeWordPressForbidden,
			"The WordPress account cannot read the site settings (HTTP 403): "+
				"it needs the manage_options capability, which an Administrator has.", requestID)
	case errors.Is(err, wordpress.ErrIncompatible):
		s.fail(w, http.StatusBadGateway, CodeWordPressIncompatible,
			"The site did not answer as a WordPress REST API: "+strings.TrimPrefix(err.Error(), wordpress.ErrIncompatible.Error()+": ")+".", requestID)
	case errors.Is(err, wordpress.ErrTimeout):
		s.fail(w, http.StatusGatewayTimeout, CodeWordPressTimeout,
			"The site did not answer in time.", requestID)
	case errors.Is(err, wordpress.ErrUnreachable):
		s.fail(w, http.StatusBadGateway, CodeWordPressUnreachable,
			"The site could not be reached.", requestID)
	default:
		s.fail(w, http.StatusBadGateway, CodeWordPressUnreachable,
			"The site could not be reached.", requestID)
	}
}

// wordpressStoreError maps a repository failure from a write.
func (s *Server) wordpressStoreError(w http.ResponseWriter, op string, err error, requestID string) {
	switch {
	case errors.Is(err, channelstore.ErrSiteNotFound):
		s.wordpressSiteNotFound(w, requestID)
	case errors.Is(err, channelstore.ErrDuplicateSite):
		s.fail(w, http.StatusConflict, CodeDuplicateSite,
			"A WordPress site with that URL is already registered.", requestID)
	case errors.Is(err, channelstore.ErrInvalidSite):
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
	default:
		s.storeError(w, op, err, requestID)
	}
}
