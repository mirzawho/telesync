package channelstore

import (
	"errors"
	"sort"
	"strings"
)

// Action is one operation a permission assignment may grant or deny.
type Action string

const (
	ActionView   Action = "view"
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

// Effect is whether an assignment allows or denies its action.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// ResourceKind names the kind of resource an action applies to. It is the
// permission identifier's prefix ("channel.view", "category.view", ...).
type ResourceKind string

// A kind's plural form is its global permission prefix ("users", "channels",
// "categories"), so globalOf can derive one from the other.
const (
	KindUser      ResourceKind = "users"
	KindChannel   ResourceKind = "channels"
	KindCategory  ResourceKind = "categories"
	KindWordPress ResourceKind = "wordpress"
	// KindServer is the Telegram servers the daemon runs. There are no
	// per-server assignments: the servers come from configuration, not from a
	// local table, so only the global "servers.view" permission decides.
	KindServer ResourceKind = "servers"
)

// GlobalPermission is one permission that is not tied to a single resource.
//
// A global "<kind>.create" permission allows creating that kind of resource: a
// resource that does not exist yet cannot carry a resource-specific grant. A
// global "<kind>.view" permission allows viewing every resource of that kind,
// which is how a list is served without reading every per-resource assignment.
type GlobalPermission string

const (
	PermissionUsersView        GlobalPermission = "users.view"
	PermissionUsersManage      GlobalPermission = "users.manage"
	PermissionChannelsView     GlobalPermission = "channels.view"
	PermissionChannelsCreate   GlobalPermission = "channels.create"
	PermissionCategoriesView   GlobalPermission = "categories.view"
	PermissionCategoriesCreate GlobalPermission = "categories.create"
	PermissionWordPressView    GlobalPermission = "wordpress.view"
	// PermissionWordPressManage follows the ".<kind>.create" shape a global
	// permission must have to map onto create, but it covers every write to
	// every registered site: once a record exists its credential can be
	// replaced through it, so per-site update grants are not offered.
	PermissionWordPressManage GlobalPermission = "wordpress.create"
	// PermissionServersView allows reading the status page: which Telegram
	// servers the daemon runs and whether they are connected. It grants
	// nothing else — no channel, category or account data.
	PermissionServersView GlobalPermission = "servers.view"
	// PermissionContentPublish lets an account publish a prepared message to a
	// destination it can already see. It is not implied by any other right:
	// "channels.create" adds a channel to the inventory, it does not send one
	// a message. Which destinations the account may name is decided by the
	// ordinary visibility rule, not by this permission.
	PermissionContentPublish GlobalPermission = "content.publish"
)

var globalPermissions = []GlobalPermission{
	PermissionUsersView,
	PermissionUsersManage,
	PermissionChannelsView,
	PermissionChannelsCreate,
	PermissionCategoriesView,
	PermissionCategoriesCreate,
	PermissionWordPressView,
	PermissionWordPressManage,
	PermissionServersView,
	PermissionContentPublish,
}

// GlobalPermissions lists every global permission, in a stable order.
func GlobalPermissions() []GlobalPermission {
	return append([]GlobalPermission(nil), globalPermissions...)
}

// Valid reports whether p is a known global permission.
func (p GlobalPermission) Valid() bool {
	for _, known := range globalPermissions {
		if p == known {
			return true
		}
	}
	return false
}

// Kind is the permission's "users", "channels" or "categories" prefix.
func (p GlobalPermission) Kind() ResourceKind {
	kind, _, _ := strings.Cut(string(p), ".")
	return ResourceKind(kind)
}

// Action is the part after the dot.
func (p GlobalPermission) Action() Action {
	_, action, _ := strings.Cut(string(p), ".")
	return Action(action)
}

// ErrResourceNotFound means a permission named a local resource id that does
// not exist in its table.
var ErrResourceNotFound = errors.New("channelstore: resource not found")

// ValidAction reports whether a is one of view, create, update or delete.
func ValidAction(a Action) bool {
	switch a {
	case ActionView, ActionCreate, ActionUpdate, ActionDelete:
		return true
	}
	return false
}

// ValidEffect reports whether e is allow or deny.
func ValidEffect(e Effect) bool {
	return e == EffectAllow || e == EffectDeny
}

// SortedGlobalPermissions returns a copy sorted for comparison and output.
func SortedGlobalPermissions(perms map[GlobalPermission]bool) []GlobalPermission {
	out := make([]GlobalPermission, 0, len(perms))
	for p := range perms {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Grant is one (subject, kind, resource id, action, effect) assignment.
type Grant struct {
	UserID     int64
	Kind       ResourceKind
	ResourceID int64
	Action     Action
	Effect     Effect
}

// Authorizer answers permission questions from a fixed set of grants.
//
// It is built per request from the database state, so a permission change
// takes effect on the next request without waiting for a token to expire. The
// rules, in order:
//
//  1. an explicit resource assignment (allow or deny) decides;
//  2. otherwise the matching global permission decides: it allows view or
//     create on that kind, and nothing else;
//  3. otherwise access is denied.
//
// A view is decided by rule 1-3 alone. Update and delete are resource-specific
// (rule 2 never grants them) and also require the view: a record the caller may
// not see may not be changed, so an explicit view:deny takes the writes with it.
//
// Create is decided only by the global permission: the resource does not exist
// yet, so it cannot have a resource-specific assignment.
type Authorizer struct {
	userID     int64
	service    bool
	global     map[GlobalPermission]bool
	channels   map[int64]map[Action]Effect
	categories map[int64]map[Action]Effect
	wordsites  map[int64]map[Action]Effect
}

// NewAuthorizer builds an Authorizer from stored grants.
//
// A view, update or delete grant that names the wrong kind is ignored; the
// database's foreign keys make that impossible, so it would be a bug, not a
// user's instruction.
func NewAuthorizer(globals []GlobalPermission, grants []Grant) *Authorizer {
	return NewAuthorizerFor(0, globals, grants)
}

// NewAuthorizerFor is NewAuthorizer for one account's grants. The account id
// lets a store scope a query to the same assignments the authorizer answers
// from, instead of filtering rows after reading them.
func NewAuthorizerFor(userID int64, globals []GlobalPermission, grants []Grant) *Authorizer {
	a := &Authorizer{
		userID:     userID,
		global:     make(map[GlobalPermission]bool, len(globals)),
		channels:   map[int64]map[Action]Effect{},
		categories: map[int64]map[Action]Effect{},
		wordsites:  map[int64]map[Action]Effect{},
	}
	for _, g := range globals {
		if g.Valid() {
			a.global[g] = true
		}
	}
	for _, g := range grants {
		var target map[int64]map[Action]Effect
		switch g.Kind {
		case KindChannel:
			target = a.channels
		case KindCategory:
			target = a.categories
		case KindWordPress:
			target = a.wordsites
		default:
			continue
		}
		// The database's CHECK constraints already exclude these; skipping
		// keeps a hand-built grant from widening access.
		if !ValidAction(g.Action) || !ValidEffect(g.Effect) || g.ResourceID <= 0 {
			continue
		}
		actions, ok := target[g.ResourceID]
		if !ok {
			actions = map[Action]Effect{}
			if target != nil {
				target[g.ResourceID] = actions
			}
		}
		actions[g.Action] = g.Effect
	}
	return a
}

// Operator is the authorizer for a caller with no account whose credential is
// the operator token.
//
// It is MarkService without the mutation, for an authorizer shared between
// requests: marking the caller's own copy would make every later request that
// reuses it a service caller.
func (a *Authorizer) Operator() *Authorizer {
	return &Authorizer{service: true}
}

// MarkService makes this authorizer the operator's fixed API token.
//
// The token is configured out of band by the person running the deployment and
// predates every account; refusing it would brick a deployment that relied on
// it. It never belongs to a user, so it holds no user-editable permission set.
func (a *Authorizer) MarkService() *Authorizer {
	if a == nil {
		return &Authorizer{service: true}
	}
	a.service = true
	return a
}

// UserID is the account these grants were read for, or 0 for the operator
// token, which belongs to no account.
func (a *Authorizer) UserID() int64 {
	if a == nil {
		return 0
	}
	return a.userID
}

// Global reports whether the user holds a global permission.
func (a *Authorizer) Global(p GlobalPermission) bool {
	return a != nil && (a.service || a.global[p])
}

// Allowed reports whether the user may perform action on one resource of kind.
//
// A view is decided on its own. Update and delete also need the view: an
// account that may not see a resource may not change it, and no assignment
// must ever be more than what the global right plus the assignment says. An
// explicit view:deny therefore takes an update or delete grant away too.
func (a *Authorizer) Allowed(kind ResourceKind, action Action, resourceID int64) bool {
	if a == nil {
		return false
	}
	if a.service {
		return true
	}
	if action == ActionView || action == ActionCreate {
		return a.allowedHere(kind, action, resourceID)
	}
	return a.allowedHere(kind, action, resourceID) && a.allowedHere(kind, ActionView, resourceID)
}

// allowedHere is the assignment-then-global rule for one action, on its own.
func (a *Authorizer) allowedHere(kind ResourceKind, action Action, resourceID int64) bool {
	if action == ActionCreate {
		return a.global[globalOf(kind, action)]
	}
	if effects, ok := a.resourceSet(kind)[resourceID]; ok && resourceID > 0 {
		if effect, ok := effects[action]; ok {
			return effect == EffectAllow
		}
	}
	if action == ActionView {
		return a.global[globalOf(kind, ActionView)]
	}
	return false
}

// Service reports whether this is the fixed operator token rather than a user.
func (a *Authorizer) Service() bool {
	return a != nil && a.service
}

// CanList reports whether the user may list every resource of kind.
func (a *Authorizer) CanList(kind ResourceKind) bool {
	return a.Global(globalOf(kind, ActionView))
}

// HasAny reports whether the user holds any resource-specific assignment of
// kind, or the fixed service token (for which every resource is allowed).
func (a *Authorizer) HasAny(kind ResourceKind) bool {
	if a == nil {
		return false
	}
	return a.service || len(a.resourceSet(kind)) > 0
}

// HasDenies reports whether any resource of kind carries an explicit deny for
// action, which is what makes a global grant not enough on its own.
func (a *Authorizer) HasDenies(kind ResourceKind, action Action) bool {
	if a == nil || a.service {
		return false
	}
	for _, effects := range a.resourceSet(kind) {
		if effects[action] == EffectDeny {
			return true
		}
	}
	return false
}

// EffectOf reports the effect the caller stored for one action on one resource,
// or "" when there is no assignment.
func (a *Authorizer) EffectOf(kind ResourceKind, action Action, resourceID int64) Effect {
	if a == nil || a.service || resourceID <= 0 {
		return ""
	}
	return a.resourceSet(kind)[resourceID][action]
}

// DeniedIDs returns the ids of every resource of kind that carries an explicit
// deny for action.
func (a *Authorizer) DeniedIDs(kind ResourceKind, action Action) []int64 {
	if a == nil || a.service {
		return nil
	}
	var out []int64
	for id, effects := range a.resourceSet(kind) {
		if effects[action] == EffectDeny {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ResourceIDs returns the ids of every resource of kind the user may perform
// action on, using resource-specific assignments: an assignment of action, for
// an action that leaveResourceAlone says cannot be removed from a resource.
func (a *Authorizer) ResourceIDs(kind ResourceKind, action Action) map[int64]bool {
	out := map[int64]bool{}
	if a == nil || action == ActionCreate || a.service {
		return out
	}
	leavable := leaveResourceAlone(kind, action)
	for id, effects := range a.resourceSet(kind) {
		if effects[action] == EffectAllow || (leavable && effects[ActionView] == EffectAllow) {
			out[id] = true
		}
	}
	return out
}

// leaveResourceAlone reports whether the permission model makes action
// unreachable for a resource of kind: a channel or category is removed by its
// delete assignment, but a WordPress site is registered and removed with the
// global manage right, so a per-site delete is reported yet grants nothing.
func leaveResourceAlone(kind ResourceKind, action Action) bool {
	return kind == KindWordPress && action == ActionDelete
}

func (a *Authorizer) resourceSet(kind ResourceKind) map[int64]map[Action]Effect {
	switch kind {
	case KindChannel:
		return a.channels
	case KindCategory:
		return a.categories
	case KindWordPress:
		return a.wordsites
	default:
		return nil
	}
}

// globalOf maps a kind and action to its global permission identifier.
func globalOf(kind ResourceKind, action Action) GlobalPermission {
	return GlobalPermission(string(kind) + "." + string(action))
}
