package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
)

// UserStore is the account repository the whole local user API needs: the
// gateway authenticates against it and manages accounts through it.
type UserStore interface {
	CreateUser(ctx context.Context, fullname, username, passwordHash string, active bool) (channelstore.User, error)
	GetUser(ctx context.Context, id int64) (channelstore.User, bool, error)
	UserByUsername(ctx context.Context, username string) (channelstore.User, bool, error)
	ListUsers(ctx context.Context, f channelstore.UserFilter) ([]channelstore.User, int, error)
	UpdateUser(ctx context.Context, id int64, update channelstore.UserUpdate) (channelstore.User, error)
	DeleteUser(ctx context.Context, id int64) error
	PermissionsOf(ctx context.Context, userID int64) (channelstore.PermissionSet, error)
	AuthorizerFor(ctx context.Context, userID int64) (*channelstore.Authorizer, error)
	ReplacePermissions(ctx context.Context, userID int64, set channelstore.PermissionSet) error
	UserIDsWithAnyPermission(ctx context.Context) ([]int64, error)
}

// usersPrefix is the account-collection route; the user id follows it.
const usersPrefix = "/api/v1/local/users"

// maxUserBody bounds a request body.
const maxUserBody = 64 << 10

// userView is the safe public shape of an account: never the hash.
type userView struct {
	ID          int64            `json:"id"`
	Fullname    string           `json:"fullname"`
	Username    string           `json:"username"`
	IsActive    bool             `json:"is_active"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
	Permissions *permissionsView `json:"permissions,omitempty"`
	// Access is what this caller may read, for a client's navigation. It is
	// set only on the caller's own session (GET /api/v1/auth/me).
	Access *accessView `json:"access,omitempty"`
}

// accessView summarises the caller's read access to the local API's sections.
type accessView struct {
	Channels   bool `json:"channels"`
	Categories bool `json:"categories"`
	Users      bool `json:"users"`
	// Servers is the global "servers.view" permission: the Telegram servers'
	// status page.
	Servers bool `json:"servers"`
	// Content is the global "content.publish" permission: whether this caller
	// may publish a prepared message to a destination it can see.
	Content bool `json:"content"`
}

type permissionsView struct {
	Global []string `json:"global"`
	// Channels, Categories and WordPress hold only explicit assignments. An
	// omitted action means "no override": the global permission, or a deny,
	// decides. Writing a WordPress site needs the global "wordpress.create"
	// permission, so a wordpress entry's update and delete are reported but
	// grant nothing on their own.
	Channels   []resourcePermissionsView `json:"channels"`
	Categories []resourcePermissionsView `json:"categories"`
	WordPress  []resourcePermissionsView `json:"wordpress"`
}

type resourcePermissionsView struct {
	ChannelID  int64  `json:"channel_id,omitempty"`
	CategoryID int64  `json:"category_id,omitempty"`
	SiteID     int64  `json:"site_id,omitempty"`
	View       string `json:"view,omitempty"`
	Update     string `json:"update,omitempty"`
	Delete     string `json:"delete,omitempty"`
}

func userViewOf(u channelstore.User, set *channelstore.PermissionSet) userView {
	out := userView{
		ID:        u.ID,
		Fullname:  u.Fullname,
		Username:  u.Username,
		IsActive:  u.IsActive,
		CreatedAt: timestampStringOrEmpty(u.CreatedAt),
		UpdatedAt: timestampStringOrEmpty(u.UpdatedAt),
	}
	if set == nil {
		return out
	}
	out.Permissions = permissionsViewOf(set)
	return out
}

// timestampStringOrEmpty is formatTime as a string, for a view field that is
// always present.
func timestampStringOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return RFC3339String(t)
}

func permissionsViewOf(set *channelstore.PermissionSet) *permissionsView {
	view := &permissionsView{Global: []string{}, Channels: []resourcePermissionsView{},
		Categories: []resourcePermissionsView{}, WordPress: []resourcePermissionsView{}}
	if set == nil {
		return view
	}
	for _, p := range set.Global {
		view.Global = append(view.Global, string(p))
	}
	if view.Global == nil {
		view.Global = []string{}
	}
	for _, c := range set.Channels {
		view.Channels = append(view.Channels, resourcePermissionsViewOf(c, "channel"))
	}
	for _, c := range set.Categories {
		view.Categories = append(view.Categories, resourcePermissionsViewOf(c, "category"))
	}
	for _, c := range set.Wordsites {
		view.WordPress = append(view.WordPress, resourcePermissionsViewOf(c, "site"))
	}
	return view
}

func resourcePermissionsViewOf(rp channelstore.ResourcePermissions, kind string) resourcePermissionsView {
	out := resourcePermissionsView{}
	switch kind {
	case "channel":
		out.ChannelID = rp.ResourceID
	case "site":
		out.SiteID = rp.ResourceID
	default:
		out.CategoryID = rp.ResourceID
	}
	for _, p := range rp.Permissions {
		switch p.Action {
		case channelstore.ActionView:
			out.View = string(p.Effect)
		case channelstore.ActionUpdate:
			out.Update = string(p.Effect)
		case channelstore.ActionDelete:
			out.Delete = string(p.Effect)
		}
	}
	return out
}

// createUserRequest is the accepted body for POST /local/users.
type createUserRequest struct {
	Fullname string  `json:"fullname"`
	Username string  `json:"username"`
	Password *string `json:"password"`
	IsActive *bool   `json:"is_active"`
}

// updateUserRequest is the accepted body for PUT /local/users/{id}. A nil field
// is left unchanged; an unknown field (such as password_hash) is refused.
type updateUserRequest struct {
	Fullname *string `json:"fullname"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	IsActive *bool   `json:"is_active"`
}

// permissionsRequest is the body for PUT /local/users/{id}/permissions.
//
// It replaces the whole set: every list is the complete assignment, and
// anything not listed is removed.
type permissionsRequest struct {
	GlobalPermissions   []string                   `json:"global_permissions"`
	ChannelPermissions  []channelPermissionsEntry  `json:"channel_permissions"`
	CategoryPermissions []categoryPermissionsEntry `json:"category_permissions"`
	// WordPressPermissions is optional: a client that does not manage site
	// access (the administrators dialog) omits it, and the stored per-site
	// grants are kept. Sending it — even as [] — replaces them.
	WordPressPermissions []wordpressPermissionsEntry `json:"wordpress_permissions"`
	// WordPressPermissionsSet records that the body named the field at all.
	WordPressPermissionsSet bool `json:"-"`
}

// UnmarshalJSON reads the body and remembers which optional lists it carried.
func (p *permissionsRequest) UnmarshalJSON(data []byte) error {
	type plain permissionsRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = permissionsRequest(decoded)
	_, p.WordPressPermissionsSet = raw["wordpress_permissions"]
	return nil
}

type channelPermissionsEntry struct {
	ChannelID   int64             `json:"channel_id"`
	Permissions map[string]string `json:"permissions"`
}

type categoryPermissionsEntry struct {
	CategoryID  int64             `json:"category_id"`
	Permissions map[string]string `json:"permissions"`
}

type wordpressPermissionsEntry struct {
	SiteID      int64             `json:"site_id"`
	Permissions map[string]string `json:"permissions"`
}

// serveLocal routes everything under /api/v1/local: local accounts, categories
// and channel records. The caller is already authenticated. The first two never
// reach a worker; a local channel record reaches Telegram only through the
// worker of the client it names.
func (s *Server) serveLocal(w http.ResponseWriter, r *http.Request, requestID string) {
	switch {
	case underPath(r.URL.Path, s.fullPath(wordpressPrefix)):
		s.wordpress(w, r, requestID)
	case underPath(r.URL.Path, s.fullPath(categoriesPrefix)):
		if s.cfg.Categories == nil {
			s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
				"Local category storage is not configured.", requestID)
			return
		}
		s.categories(w, r, requestID)
	case underPath(r.URL.Path, s.fullPath(usersPrefix)):
		if s.cfg.Users == nil {
			s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
				"Local user storage is not configured.", requestID)
			return
		}
		s.users(w, r, requestID)
	case underPath(r.URL.Path, s.fullPath(localChannelsPrefix)):
		if s.cfg.Channels == nil {
			s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
				"Local channel storage is not configured.", requestID)
			return
		}
		s.localChannels(w, r, requestID)
	default:
		w.Header().Set("Allow", http.MethodGet)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Only GET is supported.", requestID)
	}
}

// users routes a request whose path is under usersPrefix. The caller is
// already authenticated; each handler decides what it may do.
func (s *Server) users(w http.ResponseWriter, r *http.Request, requestID string) {
	authr := actorAuth(r)
	rest := strings.TrimPrefix(r.URL.Path, s.fullPath(usersPrefix))

	switch {
	case rest == "" || rest == "/":
		canManage := authr.Global(channelstore.PermissionUsersManage)
		switch r.Method {
		case http.MethodGet:
			// Anyone may list; without "view users" the list is narrowed to the
			// caller (and, for an administrator, the accounts it manages).
			s.listUsers(w, r, requestID, canManage)
		case http.MethodPost:
			if !canManage {
				s.forbidden(w, requestID)
				return
			}
			s.createUser(w, r, requestID)
		default:
			w.Header().Set("Allow", "GET, POST")
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Use GET to list users or POST to create one.", requestID)
		}
		return
	}

	raw := strings.TrimPrefix(rest, "/")
	parts := strings.SplitN(raw, "/", 2)
	rawID := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	if sub != "" && sub != "permissions" {
		s.notFound(w, requestID)
		return
	}
	if sub == "permissions" && r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"Use GET to read a user's permissions or PUT to replace them.", requestID)
		return
	}
	if sub == "" {
		// The method decides first, so an unsupported one is a 405 whatever
		// the id looks like.
		switch r.Method {
		case http.MethodGet, http.MethodPut, http.MethodDelete:
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Use GET, PUT or DELETE on a user.", requestID)
			return
		}
	}
	id, err := parseID(rawID)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "id must be a positive integer.", requestID)
		return
	}

	switch {
	case sub == "" && r.Method == http.MethodGet:
		s.getUser(w, r, id, requestID)
	case sub == "" && r.Method == http.MethodPut:
		s.updateUser(w, r, id, requestID)
	case sub == "":
		s.deleteUser(w, r, id, requestID)
	default:
		s.userPermissions(w, r, id, requestID)
	}
}

// canReadUser reports whether the caller may see one account's profile.
func canReadUser(authr *channelstore.Authorizer, callerID, id int64) bool {
	return authr.Global(channelstore.PermissionUsersManage) ||
		authr.Global(channelstore.PermissionUsersView) ||
		callerID == id
}

// canSeeAllUsers reports whether a list may include accounts the caller does
// not hold a global "view users" permission for.
func canSeeAllUsers(authr *channelstore.Authorizer) bool {
	return authr.Global(channelstore.PermissionUsersView)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, requestID string, manageableOnly bool) {
	page, perPage, q, err := parseUserQuery(r.URL.Query())
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	filter := channelstore.UserFilter{Page: page, PerPage: perPage, Query: q}
	if !canSeeAllUsers(actorAuth(r)) {
		// Without "view users" the caller sees only itself and the accounts it
		// could administer, which needs "manage users".
		filter.IDs = []int64{}
		if manageableOnly {
			ids, err := s.cfg.Users.UserIDsWithAnyPermission(r.Context())
			if err != nil {
				s.storeError(w, "list_users", err, requestID)
				return
			}
			filter.IDs = ids
		}
		if a := actorOf(r).user; a.ID > 0 {
			filter.IDs = append(filter.IDs, a.ID)
		}
	}

	users, total, err := s.cfg.Users.ListUsers(r.Context(), filter)
	if err != nil {
		s.storeError(w, "list_users", err, requestID)
		return
	}
	views := make([]userView, 0, len(users))
	for _, u := range users {
		views = append(views, userViewOf(u, nil))
	}
	meta := metaFor(requestID, s.cfg.Now())
	meta["page"] = page
	meta["per_page"] = perPage
	meta["total"] = total
	meta["total_pages"] = (total + perPage - 1) / perPage
	meta["has_more"] = page*perPage < total
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{"users": views}, Meta: meta})
}

func (s *Server) requireUserVisible(w http.ResponseWriter, r *http.Request, id int64, requestID string) (channelstore.User, bool) {
	user, found, err := s.cfg.Users.GetUser(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_user", err, requestID)
		return channelstore.User{}, false
	}
	if !found {
		s.userNotFound(w, requestID)
		return channelstore.User{}, false
	}
	caller := actorOf(r)
	if !canReadUser(caller.auth, caller.user.ID, id) {
		// 403, not 404: the caller may know the account exists; what it lacks
		// is the right to see it.
		s.forbidden(w, requestID)
		return channelstore.User{}, false
	}
	return user, true
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	user, ok := s.requireUserVisible(w, r, id, requestID)
	if !ok {
		return
	}
	set, err := s.cfg.Users.PermissionsOf(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_user_permissions", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"user": userViewOf(user, &set)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, requestID string) {
	var req createUserRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if req.Password == nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "password is required.", requestID)
		return
	}
	if err := channelstore.ValidatePassword(*req.Password); err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	if _, _, err := channelstore.NormalizeUser(req.Fullname, req.Username); err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	hash, err := auth.HashPassword(*req.Password)
	if err != nil {
		s.storeError(w, "create_user", err, requestID)
		return
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	user, err := s.cfg.Users.CreateUser(r.Context(), req.Fullname, req.Username, hash, active)
	if err != nil {
		s.userWriteError(w, "create_user", err, requestID)
		return
	}
	writeJSON(w, http.StatusCreated, envelope{
		Data: map[string]any{"user": userViewOf(user, nil)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	caller := actorOf(r)
	if !caller.auth.Global(channelstore.PermissionUsersManage) {
		s.forbidden(w, requestID)
		return
	}
	if _, found, err := s.cfg.Users.GetUser(r.Context(), id); err != nil {
		s.storeError(w, "update_user", err, requestID)
		return
	} else if !found {
		s.userNotFound(w, requestID)
		return
	}

	var req updateUserRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if req.Fullname == nil && req.Username == nil && req.Password == nil && req.IsActive == nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"Name at least one of fullname, username, password or is_active.", requestID)
		return
	}
	if req.IsActive != nil && !*req.IsActive && id == caller.user.ID {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "You cannot deactivate your own account.", requestID)
		return
	}

	update := channelstore.UserUpdate{
		Fullname: req.Fullname,
		Username: req.Username,
		IsActive: req.IsActive,
	}
	if req.Password != nil {
		if err := channelstore.ValidatePassword(*req.Password); err != nil {
			s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
			return
		}
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			s.storeError(w, "update_user", err, requestID)
			return
		}
		update.PasswordHash = &hash
	}

	user, err := s.cfg.Users.UpdateUser(r.Context(), id, update)
	if err != nil {
		s.userWriteError(w, "update_user", err, requestID)
		return
	}
	set, err := s.cfg.Users.PermissionsOf(r.Context(), id)
	if err != nil {
		s.storeError(w, "update_user_permissions", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"user": userViewOf(user, &set)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	caller := actorOf(r)
	if !caller.auth.Global(channelstore.PermissionUsersManage) {
		s.forbidden(w, requestID)
		return
	}
	if id == caller.user.ID {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "You cannot delete your own account.", requestID)
		return
	}
	if err := s.cfg.Users.DeleteUser(r.Context(), id); err != nil {
		s.userWriteError(w, "delete_user", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"deleted": true, "id": id},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// userPermissions serves GET and PUT /local/users/{id}/permissions.
//
// Reading one's own permissions needs no permission; reading anyone else's, and
// changing anyone's, needs "manage users". Nobody, administrators included, may
// change their own set: an administrator who could would be able to widen it.
func (s *Server) userPermissions(w http.ResponseWriter, r *http.Request, id int64, requestID string) {
	caller := actorOf(r)
	self := caller.user.ID == id

	if !self && !caller.auth.Global(channelstore.PermissionUsersManage) {
		if r.Method == http.MethodGet && caller.auth.Global(channelstore.PermissionUsersView) {
			// falls through to the read below
		} else {
			s.forbidden(w, requestID)
			return
		}
	}
	if r.Method == http.MethodPut && self {
		s.fail(w, http.StatusForbidden, CodeForbidden,
			"You cannot change your own permissions; ask another administrator.", requestID)
		return
	}

	if r.Method == http.MethodGet {
		if !self {
			if _, found, err := s.cfg.Users.GetUser(r.Context(), id); err != nil {
				s.storeError(w, "get_user", err, requestID)
				return
			} else if !found {
				s.userNotFound(w, requestID)
				return
			}
		}
		set, err := s.cfg.Users.PermissionsOf(r.Context(), id)
		if err != nil {
			s.storeError(w, "get_permissions", err, requestID)
			return
		}
		writeJSON(w, http.StatusOK, envelope{
			Data: map[string]any{"permissions": permissionsViewOf(&set)},
			Meta: metaFor(requestID, s.cfg.Now()),
		})
		return
	}

	if _, found, err := s.cfg.Users.GetUser(r.Context(), id); err != nil {
		s.storeError(w, "get_user", err, requestID)
		return
	} else if !found {
		s.userNotFound(w, requestID)
		return
	}

	var req permissionsRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	set, err := permissionSetOf(req)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	if !req.WordPressPermissionsSet {
		// The body did not speak about sites: keep what is stored rather than
		// revoking it as a side effect of saving channels.
		stored, err := s.cfg.Users.PermissionsOf(r.Context(), id)
		if err != nil {
			s.storeError(w, "get_permissions", err, requestID)
			return
		}
		set.Wordsites = stored.Wordsites
	}
	if err := s.replacePermissions(r, id, set); err != nil {
		switch {
		case errors.Is(err, channelstore.ErrResourceNotFound), errors.Is(err, channelstore.ErrInvalidUser):
			s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		case errors.Is(err, errWouldStrandAdministrators):
			s.fail(w, http.StatusConflict, CodeConflict,
				"This is the last active account that can manage users; give the permission to another account first.", requestID)
		default:
			s.userWriteError(w, "replace_permissions", err, requestID)
		}
		return
	}
	stored, err := s.cfg.Users.PermissionsOf(r.Context(), id)
	if err != nil {
		s.storeError(w, "get_permissions", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"permissions": permissionsViewOf(&stored)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// errWouldStrandAdministrators means the change would leave no active account
// able to manage users and permissions.
var errWouldStrandAdministrators = errors.New("gateway: the change would leave no active user administrator")

// replacePermissions writes the set, refusing one that would remove
// users.manage from the last active administrator.
//
// The repository cannot enforce this itself: it cannot know that the set it is
// writing is the only source of that permission. The check runs before the
// write, in the same order as everything else here: a failed check leaves the
// stored set untouched.
func (s *Server) replacePermissions(r *http.Request, id int64, set channelstore.PermissionSet) error {
	ctx := r.Context()
	if !containsGlobal(set.Global, channelstore.PermissionUsersManage) {
		others, err := s.otherActiveAdministrators(ctx, id)
		if err != nil {
			return err
		}
		if others == 0 {
			current, found, err := s.cfg.Users.GetUser(ctx, id)
			if err != nil {
				return err
			}
			if found && current.IsActive {
				existing, err := s.cfg.Users.PermissionsOf(ctx, id)
				if err != nil {
					return err
				}
				if containsGlobal(existing.Global, channelstore.PermissionUsersManage) {
					return errWouldStrandAdministrators
				}
			}
		}
	}
	return s.cfg.Users.ReplacePermissions(ctx, id, set)
}

// otherActiveAdministrators counts the active accounts, other than id, that
// hold users.manage.
func (s *Server) otherActiveAdministrators(ctx context.Context, id int64) (int, error) {
	ids, err := s.cfg.Users.UserIDsWithAnyPermission(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, candidate := range ids {
		if candidate == id {
			continue
		}
		user, found, err := s.cfg.Users.GetUser(ctx, candidate)
		if err != nil {
			return 0, err
		}
		if !found || !user.IsActive {
			continue
		}
		set, err := s.cfg.Users.PermissionsOf(ctx, candidate)
		if err != nil {
			return 0, err
		}
		if containsGlobal(set.Global, channelstore.PermissionUsersManage) {
			count++
		}
	}
	return count, nil
}

func containsGlobal(set []channelstore.GlobalPermission, want channelstore.GlobalPermission) bool {
	for _, p := range set {
		if p == want {
			return true
		}
	}
	return false
}

// permissionSetOf turns the request body into the repository's set.
func permissionSetOf(req permissionsRequest) (channelstore.PermissionSet, error) {
	set := channelstore.PermissionSet{
		Global:     make([]channelstore.GlobalPermission, 0, len(req.GlobalPermissions)),
		Channels:   make([]channelstore.ResourcePermissions, 0, len(req.ChannelPermissions)),
		Categories: make([]channelstore.ResourcePermissions, 0, len(req.CategoryPermissions)),
		Wordsites:  make([]channelstore.ResourcePermissions, 0, len(req.WordPressPermissions)),
	}
	for _, raw := range req.GlobalPermissions {
		p := channelstore.GlobalPermission(strings.TrimSpace(raw))
		if !p.Valid() {
			return set, errors.New("unknown global permission " + strconv.Quote(raw) + "; see GET /api/v1/auth/me for the list")
		}
		set.Global = append(set.Global, p)
	}
	for _, entry := range req.ChannelPermissions {
		rp, err := resourcePermissionsOf("channel", entry.ChannelID, entry.Permissions)
		if err != nil {
			return set, err
		}
		set.Channels = append(set.Channels, rp)
	}
	for _, entry := range req.CategoryPermissions {
		rp, err := resourcePermissionsOf("category", entry.CategoryID, entry.Permissions)
		if err != nil {
			return set, err
		}
		set.Categories = append(set.Categories, rp)
	}
	for _, entry := range req.WordPressPermissions {
		rp, err := resourcePermissionsOf("site", entry.SiteID, entry.Permissions)
		if err != nil {
			return set, err
		}
		set.Wordsites = append(set.Wordsites, rp)
	}
	if err := requireViewForManage(set.Global); err != nil {
		return set, err
	}
	return set, nil
}

// requireViewForManage refuses a set that manages a kind without being able to
// see it: users.manage without users.view, channels.create without
// channels.view, categories.create without categories.view.
func requireViewForManage(globals []channelstore.GlobalPermission) error {
	pairs := []struct {
		manage channelstore.GlobalPermission
		view   channelstore.GlobalPermission
	}{
		{channelstore.PermissionUsersManage, channelstore.PermissionUsersView},
		{channelstore.PermissionChannelsCreate, channelstore.PermissionChannelsView},
		{channelstore.PermissionCategoriesCreate, channelstore.PermissionCategoriesView},
	}
	for _, pair := range pairs {
		if containsGlobal(globals, pair.manage) && !containsGlobal(globals, pair.view) {
			return errors.New(string(pair.manage) + " needs " + string(pair.view))
		}
	}
	return nil
}

func resourcePermissionsOf(label string, id int64, actions map[string]string) (channelstore.ResourcePermissions, error) {
	out := channelstore.ResourcePermissions{ResourceID: id}
	if id <= 0 {
		return out, errors.New(label + "_id is required and must be a positive integer")
	}
	for rawAction, rawEffect := range actions {
		action, effect := channelstore.Action(rawAction), channelstore.Effect(rawEffect)
		if !channelstore.ValidAction(action) || action == channelstore.ActionCreate {
			return out, errors.New("unknown " + label + " action " + strconv.Quote(rawAction) +
				"; use view, update or delete")
		}
		if !channelstore.ValidEffect(effect) {
			return out, errors.New("effect for " + label + " action " + strconv.Quote(rawAction) +
				" must be \"allow\" or \"deny\"")
		}
		out.Permissions = append(out.Permissions, channelstore.ResourcePermission{Action: action, Effect: effect})
	}
	return out, nil
}

// userWriteError maps a repository failure from a write.
func (s *Server) userWriteError(w http.ResponseWriter, op string, err error, requestID string) {
	switch {
	case errors.Is(err, channelstore.ErrUserNotFound):
		s.userNotFound(w, requestID)
	case errors.Is(err, channelstore.ErrDuplicateUsername):
		s.fail(w, http.StatusConflict, CodeConflict, "That username is already in use.", requestID)
	case errors.Is(err, channelstore.ErrLastAdministrator):
		s.fail(w, http.StatusConflict, CodeConflict,
			"This is the last active account that can manage users; create or promote another one first.", requestID)
	case errors.Is(err, channelstore.ErrInvalidUser), errors.Is(err, channelstore.ErrResourceNotFound):
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
	default:
		s.storeError(w, op, err, requestID)
	}
}

func (s *Server) userNotFound(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusNotFound, CodeNotFound, "No user has that id.", requestID)
}

// parseUserQuery validates page, per_page and q.
func parseUserQuery(q map[string][]string) (int, int, string, error) {
	return parsePageQuery(q, channelstore.MaxFullnameLen)
}
