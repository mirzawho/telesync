package gateway

import (
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

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// CategoryStore is the local category repository.
type CategoryStore interface {
	CreateCategory(ctx context.Context, name, description string) (channelstore.Category, error)
	GetCategory(ctx context.Context, id int64) (channelstore.Category, bool, error)
	ListCategories(ctx context.Context, q channelstore.ListQuery) ([]channelstore.Category, int, error)
	UpdateCategory(ctx context.Context, id int64, name, description string) (channelstore.Category, error)
	DeleteCategory(ctx context.Context, id int64) error
}

// CategoryMembers is the store's view of which channels a category holds. The
// gateway needs it only for the member route; the store implements both.
type CategoryMembers interface {
	CategoryChannels(ctx context.Context, categoryID int64) ([]channelstore.Channel, error)
}

// ChannelCategories is the store's view of which categories a channel is filed
// under; the channel's own route uses it.
type ChannelCategories interface {
	CategoryChannels(ctx context.Context, categoryID int64) ([]channelstore.Channel, error)
	AssignmentCounts(ctx context.Context, clientKey, channelID string) (map[int64]int, error)
	ReplaceCategories(ctx context.Context, clientKey string, channelID int64, categoryIDs []int64) error
}

// CodeConflict is returned when a write collides with existing local state.
const CodeConflict = "conflict"

// categoriesPrefix is the route; the category id follows it when present.
const categoriesPrefix = "/api/v1/local/categories"

// maxCategoryBody bounds a request body. It is far above the largest valid
// name and description and keeps a hostile client from growing the heap.
const maxCategoryBody = 16 << 10

// categoryView is the public shape of one category.
type categoryView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func categoryViewOf(c channelstore.Category) categoryView {
	return categoryView{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		CreatedAt:   RFC3339String(c.CreatedAt),
		UpdatedAt:   RFC3339String(c.UpdatedAt),
	}
}

// categoryRequest is the accepted body for create and update.
//
// Unknown fields and trailing content are rejected: a misspelled field would
// otherwise be silently dropped, and a PUT would clear what it meant to set.
type categoryRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// categories routes a request whose path is already known to be under
// categoriesPrefix. The caller is authenticated; each handler checks what it
// may do to the category it names.
func (s *Server) categories(w http.ResponseWriter, r *http.Request, requestID string) {
	rest := strings.TrimPrefix(r.URL.Path, s.fullPath(categoriesPrefix))
	switch rest {
	case "", "/":
		switch r.Method {
		case http.MethodGet:
			s.listCategories(w, r, requestID)
		case http.MethodPost:
			if !actorAuth(r).Global(channelstore.PermissionCategoriesCreate) {
				s.forbidden(w, requestID)
				return
			}
			s.createCategory(w, r, requestID)
		default:
			w.Header().Set("Allow", "GET, POST")
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Use GET to list categories or POST to create one.", requestID)
		}
		return
	}

	raw := rest[1:]
	if idText, member, ok := strings.Cut(raw, "/"); ok {
		s.categoryMember(w, r, idText, member, requestID)
		return
	}
	// The method decides first, so an unsupported one is a 405 whatever the id
	// looks like.
	switch r.Method {
	case http.MethodGet, http.MethodPut, http.MethodDelete:
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"Use GET, PUT or DELETE on a category.", requestID)
		return
	}
	id, err := parseID(raw)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "id must be a positive integer.", requestID)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getCategory(w, r, id, requestID)
	case http.MethodPut:
		if !s.categoryAction(w, r, id, channelstore.ActionUpdate, requestID) {
			return
		}
		s.updateCategory(w, r, id, requestID)
	default:
		if !s.categoryAction(w, r, id, channelstore.ActionDelete, requestID) {
			return
		}
		s.deleteCategory(w, id, requestID)
	}
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request, requestID string) {
	page, perPage, q, err := parseCategoryQuery(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}

	cats, total, err := s.visibleCategories(r.Context(), actorAuth(r), channelstore.ListQuery{
		Page: page, PerPage: perPage, Query: q,
	})
	if err != nil {
		s.storeError(w, "list_categories", err, requestID)
		return
	}

	views := make([]categoryView, 0, len(cats))
	for _, c := range cats {
		views = append(views, categoryViewOf(c))
	}
	meta := metaFor(requestID, s.cfg.Now())
	meta["page"] = page
	meta["per_page"] = perPage
	meta["total"] = total
	meta["total_pages"] = (total + perPage - 1) / perPage
	meta["has_more"] = page*perPage < total
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{"categories": views}, Meta: meta})
}

func (s *Server) getCategory(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	if !s.categoryAction(w, r, id, channelstore.ActionView, requestID) {
		return
	}
	cat, ok, err := s.cfg.Categories.GetCategory(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_category", err, requestID)
		return
	}
	if !ok {
		s.categoryNotFound(w, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"category": categoryViewOf(cat)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request, requestID string) {
	req, err := decodeCategoryRequest(w, r)
	if err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if req.Name == nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "name is required.", requestID)
		return
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}

	cat, err := s.cfg.Categories.CreateCategory(r.Context(), *req.Name, description)
	if err != nil {
		s.categoryWriteError(w, "create_category", err, requestID)
		return
	}
	if s.cfg.OnCreated != nil {
		s.cfg.OnCreated(channelstore.KindCategory, cat.ID)
	}
	writeJSON(w, http.StatusCreated, envelope{
		Data: map[string]any{"category": categoryViewOf(cat)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) updateCategory(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	req, err := decodeCategoryRequest(w, r)
	if err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	// PUT replaces the resource: both fields are required, so an omitted
	// description clears it instead of silently keeping the old one.
	if req.Name == nil || req.Description == nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"A full category is required: name and description.", requestID)
		return
	}

	cat, err := s.cfg.Categories.UpdateCategory(r.Context(), id, *req.Name, *req.Description)
	if err != nil {
		s.categoryWriteError(w, "update_category", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"category": categoryViewOf(cat)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) deleteCategory(w http.ResponseWriter, id int64, requestID string) {
	if err := s.cfg.Categories.DeleteCategory(context.Background(), id); err != nil {
		if errors.Is(err, channelstore.ErrCategoryNotFound) {
			s.categoryNotFound(w, requestID)
			return
		}
		s.storeError(w, "delete_category", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"deleted": true, "id": id},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// errUnsupportedMedia means the body is not application/json.
var errUnsupportedMedia = errors.New("the body must be application/json.")

// statusOfBodyError maps a body error onto its status.
func statusOfBodyError(err error) int {
	if errors.Is(err, errUnsupportedMedia) {
		return http.StatusUnsupportedMediaType
	}
	return http.StatusBadRequest
}

// parsePageQuery validates page, per_page and q for a local list.
func parsePageQuery(q map[string][]string, maxQuery int) (int, int, string, error) {
	page, perPage, query, err := parseCategoryQuery(q)
	if err != nil {
		return 0, 0, "", err
	}
	if len([]rune(query)) > maxQuery {
		return 0, 0, "", errors.New("q must be at most " + strconv.Itoa(maxQuery) + " characters.")
	}
	return page, perPage, query, nil
}

// decodeCategoryRequest reads and validates the JSON body.
func decodeCategoryRequest(w http.ResponseWriter, r *http.Request) (categoryRequest, error) {
	var req categoryRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxCategoryBody); err != nil {
		if errors.Is(err, errUnsupportedMedia) || errors.Is(err, errInvalidBody) {
			return categoryRequest{}, err
		}
		return categoryRequest{}, errors.New("the body must be one JSON object with a name and an optional description.")
	}
	return req, nil
}

// decodeJSONBody reads one JSON object of at most maxUserBody bytes.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) error {
	return decodeJSONBodyLimited(w, r, v, maxUserBody)
}

// errInvalidBody means the body is not exactly one JSON object.
var errInvalidBody = errors.New("the body must contain exactly one JSON object.")

// decodeJSONBodyLimited reads one JSON object.
//
// Unknown fields are refused, so a misspelled field is reported instead of
// silently dropped, and an extra value after the object is refused, so a
// truncated attempt cannot look complete.
func decodeJSONBodyLimited(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errUnsupportedMedia
	}
	if r.Body == nil {
		return errInvalidBody
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the body is not a valid JSON object for this request: %w", errInvalidBody)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errInvalidBody
	}
	return nil
}

// categoryWriteError maps a repository failure from a write.
func (s *Server) categoryWriteError(w http.ResponseWriter, op string, err error, requestID string) {
	switch {
	case errors.Is(err, channelstore.ErrCategoryNotFound):
		s.categoryNotFound(w, requestID)
	case errors.Is(err, channelstore.ErrChannelRowNotFound):
		s.fail(w, http.StatusNotFound, CodeNotFound,
			"One of the channels does not exist in the local channel list.", requestID)
	case errors.Is(err, channelstore.ErrDuplicateName):
		s.fail(w, http.StatusConflict, CodeConflict,
			"A category with that name already exists.", requestID)
	case errors.Is(err, channelstore.ErrInvalidCategory):
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
	default:
		s.storeError(w, op, err, requestID)
	}
}

func (s *Server) categoryNotFound(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusNotFound, CodeNotFound, "No category has that id.", requestID)
}

// parseCategoryQuery validates page, per_page and q.
func parseCategoryQuery(q map[string][]string) (int, int, string, error) {
	page, perPage := rpc.DefaultPage, rpc.DefaultPerPage
	query := ""
	for key, values := range q {
		if len(values) != 1 {
			return 0, 0, "", errors.New(key + " must be given exactly once.")
		}
		raw := values[0]
		switch key {
		case "page":
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 {
				return 0, 0, "", errors.New("page must be an integer of at least 1.")
			}
			page = v
		case "per_page":
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > rpc.MaxPerPage {
				return 0, 0, "", errors.New("per_page must be an integer between 1 and " +
					strconv.Itoa(rpc.MaxPerPage) + ".")
			}
			perPage = v
		case "q":
			query = values[0]
		default:
			return 0, 0, "", errors.New("unknown query parameter " + strconv.Quote(key) + ".")
		}
	}
	if len([]rune(query)) > channelstore.MaxCategoryDescriptionLen {
		return 0, 0, "", errors.New("q must be at most " +
			strconv.Itoa(channelstore.MaxCategoryDescriptionLen) + " characters.")
	}
	return page, perPage, query, nil
}

func parseID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

// CategoryByIDs is the listing the filtered category page needs.
type CategoryByIDs interface {
	ListCategoriesByIDs(ctx context.Context, ids []int64) ([]channelstore.Category, error)
}

// visibleCategories draws one page of the categories a caller may read and
// counts every category it may see.
//
// It is the one place the category visibility rule lives, so the list route and
// the dashboard's count cannot disagree. The global view reads every category
// except one carrying an explicit view:deny; without it, only a category with an
// explicit view:allow is read. A page is then drawn from exactly those rows, so
// the order, the page size and the total all describe what the caller may read.
func (s *Server) visibleCategories(ctx context.Context, authr *channelstore.Authorizer,
	q channelstore.ListQuery) ([]channelstore.Category, int, error) {

	if authr.CanList(channelstore.KindCategory) && !authr.HasDenies(channelstore.KindCategory, channelstore.ActionView) {
		// Nothing is denied per category, so the repository can search, count
		// and page the whole collection itself.
		return s.cfg.Categories.ListCategories(ctx, q)
	}
	var (
		all []channelstore.Category
		err error
	)
	if authr.CanList(channelstore.KindCategory) {
		// A global view narrowed by explicit denies. There is no "every category
		// but these" query, so the collection is read whole — it is small — and
		// the denied rows are dropped from it below.
		all, err = s.allCategories(ctx)
	} else {
		// Without the global view the page is drawn from exactly the categories
		// the caller may see.
		ids := authr.ResourceIDs(channelstore.KindCategory, channelstore.ActionView)
		all, err = s.cfg.Categories.(CategoryByIDs).ListCategoriesByIDs(ctx, idList(ids))
	}
	if err != nil {
		return nil, 0, err
	}
	shown := make([]channelstore.Category, 0, len(all))
	for _, c := range all {
		if visible(authr, channelstore.KindCategory, channelstore.ActionView, c.ID) && categoryMatches(c, q.Query) {
			shown = append(shown, c)
		}
	}
	// Newest first, the same order the unfiltered list uses.
	sort.Slice(shown, func(i, j int) bool {
		if !shown[i].CreatedAt.Equal(shown[j].CreatedAt) {
			return shown[i].CreatedAt.After(shown[j].CreatedAt)
		}
		return shown[i].ID > shown[j].ID
	})
	return pageSlice(shown, q.Page, q.PerPage), len(shown), nil
}

// allCategories reads the whole category collection in the listing order, for a
// caller whose global view is narrowed by explicit denies. The first call only
// counts, so an empty collection is one query.
func (s *Server) allCategories(ctx context.Context) ([]channelstore.Category, error) {
	_, total, err := s.cfg.Categories.ListCategories(ctx, channelstore.ListQuery{Page: 1, PerPage: 1})
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return []channelstore.Category{}, nil
	}
	all, _, err := s.cfg.Categories.ListCategories(ctx, channelstore.ListQuery{Page: 1, PerPage: total})
	return all, err
}

// categoryMatches applies the list's q filter.
func categoryMatches(c channelstore.Category, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	return strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.Description), q)
}

// pageSlice returns one page of a slice.
func pageSlice[T any](items []T, page, perPage int) []T {
	start := (page - 1) * perPage
	if start >= len(items) {
		return nil
	}
	end := start + perPage
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// idList turns a set into a slice.
func idList(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

// categoryAction checks one action on one local category.
//
// A category that does not exist is 404 before permission is considered, so a
// caller is never told "forbidden" about a row that is not there. A category
// that exists but may not be touched is 403, the same answer for every reason.
func (s *Server) categoryAction(w http.ResponseWriter, r *http.Request, id int64, action channelstore.Action, requestID string) bool {
	if _, found, err := s.cfg.Categories.GetCategory(r.Context(), id); err != nil {
		s.storeError(w, "get_category", err, requestID)
		return false
	} else if !found {
		s.categoryNotFound(w, requestID)
		return false
	}
	if !visible(actorAuth(r), channelstore.KindCategory, action, id) {
		s.forbidden(w, requestID)
		return false
	}
	return true
}

// categoryMember serves /local/categories/{id}/{member}: the channels filed
// under one category. GET lists them; a channel's own categories are set
// through the channel, which is where a channel is known to exist.
func (s *Server) categoryMember(w http.ResponseWriter, r *http.Request, idText, member, requestID string) {
	if member != "channels" {
		s.notFound(w, requestID)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"Use GET to list a category's channels; a channel is filed through its own route.", requestID)
		return
	}
	id, err := parseID(idText)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "id must be a positive integer.", requestID)
		return
	}
	if !s.categoryAction(w, r, id, channelstore.ActionView, requestID) {
		return
	}
	s.listCategoryChannels(w, r, id, requestID)
}

func (s *Server) categoryMemberStore(w http.ResponseWriter, requestID string) (CategoryMembers, bool) {
	members, ok := s.cfg.Categories.(CategoryMembers)
	if !ok {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"Local category membership is not configured.", requestID)
		return nil, false
	}
	return members, true
}

func (s *Server) listCategoryChannels(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	members, ok := s.categoryMemberStore(w, requestID)
	if !ok {
		return
	}
	channels, err := members.CategoryChannels(r.Context(), id)
	if err != nil {
		s.categoryWriteError(w, "list_category_channels", err, requestID)
		return
	}
	// Reaching a category does not admit the channels filed under it: each one
	// is shown only if the caller may see that channel too.
	authr := actorAuth(r)
	views := make([]localChannelView, 0, len(channels))
	for _, ch := range channels {
		if visible(authr, channelstore.KindChannel, channelstore.ActionView, ch.ID) {
			views = append(views, localChannelViewOf(ch))
		}
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"channels": views},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}
