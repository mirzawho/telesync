package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/channelstore"
)

// This file is the account authorization pipeline (v0.0.5 contract §6.2).
//
// Every account operation resolves in one order, and never in another:
//
//	authenticated principal → resolve account_id → verify ownership
//	                      → verify application permission → account's worker
//
// Ownership is decided by owner-scoped SQL, never by a comparison made after a
// broad read. A client-supplied owner is never an authorization decision, and
// an account another user holds answers not-found — never forbidden, which
// would confirm the id exists and turn account ids into an enumeration oracle.

// Account errors returned to API clients.
const (
	// CodeAccountSelectionRequired means the caller holds more than one
	// eligible account on that server and the request named none.
	CodeAccountSelectionRequired = "account_selection_required"
	// CodeAccountNotOwned is the documented reason an account is unreachable.
	// The HTTP answer is still 404, so it is a description and never a
	// confirmation that the id exists.
	CodeAccountNotOwned = "account_not_owned"
	// CodeAccountNotConnected means the account exists but has no live worker.
	CodeAccountNotConnected = "account_not_connected"
	// CodeSessionEncryptionUnavailable means no key could seal or open a
	// session, so login cannot proceed.
	CodeSessionEncryptionUnavailable = "session_encryption_unavailable"
)

// AccountStore is what the gateway needs from the account repository.
//
// Every method is owner-scoped: the store itself refuses to answer for another
// owner, so the gateway's checks are a second line rather than the only one.
type AccountStore interface {
	ByID(ctx context.Context, ownerUserID int64, id string) (channelstore.Account, error)
	ListByOwner(ctx context.Context, ownerUserID int64) ([]channelstore.Account, error)
	SetStatus(ctx context.Context, ownerUserID int64, id, status, errorCode string) error
	Delete(ctx context.Context, ownerUserID int64, id string) error
}

// AccountChannels is the account-scoped channel registration and read seam.
//
// It is deliberately narrower than the whole channel store: an account route
// reaches a channel through its account and nothing else.
type AccountChannels interface {
	RegisterForAccount(ctx context.Context, ch channelstore.Channel, opts ...channelstore.RegisterOption) (channelstore.Channel, error)
	GetForAccount(ctx context.Context, accountID string, channelID int64) (channelstore.Channel, bool, error)
	GetByIDForAccount(ctx context.Context, accountID string, id int64) (channelstore.Channel, bool, error)
	ListChannelsForAccount(ctx context.Context, accountID string) ([]channelstore.Channel, error)
	ManagedIDsForAccount(ctx context.Context, accountID string) (map[int64]bool, error)
	AccountOfChannel(ctx context.Context, localID int64) (string, error)
}

// accountResolver is the gateway's half of the accountauth.AccountResolver
// contract: the two methods a legacy keyed route needs, and nothing else. The
// full interface is asserted in the internal test that pins the seam.
type accountResolver interface {
	Resolve(ctx context.Context, owner int64, serverKey string) (string, error)
	Owns(ctx context.Context, owner int64, accountID string) (bool, error)
}

// storeAccountResolver turns "which Telegram identity" into an account.
//
// It is the ONE seam every legacy {key} route goes through, and it never falls
// back: not to a central session, not to another user's account, and not to
// "the first administrator". An owner with no account on that server has no
// account there, and an owner with several must say which one.
type storeAccountResolver struct {
	accounts AccountStore
}

// NewStoreAccountResolver builds the resolver over the account repository.
//
// It returns the accountauth.AccountResolver contract directly, so a caller
// can match ErrAccountSelectionRequired and ErrNoEligibleAccount from that
// package and nothing has to translate between the two.
func NewStoreAccountResolver(accounts AccountStore) accountauth.AccountResolver {
	return storeAccountResolver{accounts: accounts}
}

// Resolve returns the single eligible account for owner on serverKey.
//
// "Eligible" is the owner's own, on that server, and not removed. A quarantined
// account belongs to no owner and is never eligible: it is a legacy central
// session, not someone's identity.
func (r storeAccountResolver) Resolve(ctx context.Context, owner int64, serverKey string) (string, error) {
	if owner <= 0 || strings.TrimSpace(serverKey) == "" {
		return "", accountauth.ErrNoEligibleAccount
	}
	accounts, err := r.accounts.ListByOwner(ctx, owner)
	if err != nil {
		return "", err
	}
	var found []string
	for _, a := range accounts {
		if a.ServerKey != serverKey {
			continue
		}
		if !accountAvailableForRouting(a) {
			continue
		}
		found = append(found, a.ID)
	}
	switch len(found) {
	case 0:
		return "", accountauth.ErrNoEligibleAccount
	case 1:
		return found[0], nil
	default:
		return "", accountauth.ErrAccountSelectionRequired
	}
}

// Owns reports whether owner holds accountID.
//
// The lookup is owner-scoped in the store, so an id belonging to someone else
// is indistinguishable from one that was never minted.
func (r storeAccountResolver) Owns(ctx context.Context, owner int64, accountID string) (bool, error) {
	if owner <= 0 || strings.TrimSpace(accountID) == "" {
		return false, nil
	}
	if _, err := r.accounts.ByID(ctx, owner, accountID); err != nil {
		if errors.Is(err, channelstore.ErrAccountNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// accountRequest is one request resolved to the account it will act as.
//
// It carries the account id and the server key the request named, so a handler
// can route to the right worker without re-deriving either.
type accountRequest struct {
	accountID string
	serverKey string
	// keyed is true when the request used the legacy {key} form and the account
	// was resolved rather than named.
	keyed bool
}

// accountIDPattern is the shape of an opaque account id, matching the worker
// side's check. It keeps a server key or a username from being mistaken for one.
const accountIDPrefix = "acc_"

// resolveAccount resolves the account one request acts as.
//
// An explicit account id must belong to the caller: an id that names another
// user's account is not-found here and never reaches the worker. A keyed
// request resolves through the account resolver, which never falls back.
func (s *Server) resolveAccount(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key, accountID, requestID string) (accountRequest, bool) {

	owner := actorOf(r).user.ID
	if owner <= 0 {
		// The operator token is a credential, not a user. It holds application
		// permissions but owns no Telegram account, so no account operation is
		// reachable through it (brief §19).
		s.forbidden(w, requestID)
		return accountRequest{}, false
	}

	// A deployment with no account repository cannot say which account a
	// request acts as, and the one thing it must never do is guess. Answering
	// here is what keeps that case an explicit refusal instead of a nil
	// dereference in the authorization path.
	if s.cfg.Accounts == nil || s.cfg.Resolver == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeAccountNotConnected,
			"Personal Telegram accounts are not configured on this deployment.", requestID)
		return accountRequest{}, false
	}

	if accountID != "" {
		rec, err := s.cfg.Accounts.ByID(ctx, owner, accountID)
		if err != nil {
			if errors.Is(err, channelstore.ErrAccountNotFound) {
				// Not theirs, or never existed: the same answer either way.
				s.fail(w, http.StatusNotFound, CodeNotFound,
					"No account of yours has that id.", requestID)
				return accountRequest{}, false
			}
			s.storeError(w, "account_lookup", err, requestID)
			return accountRequest{}, false
		}
		if !accountAvailableForRouting(rec) {
			s.fail(w, http.StatusServiceUnavailable, CodeAccountNotConnected,
				"That account is not authorized and available for Telegram operations.", requestID)
			return accountRequest{}, false
		}
		serverKey := key
		if serverKey == "" {
			if rec, err := s.cfg.Accounts.ByID(ctx, owner, accountID); err == nil {
				serverKey = rec.ServerKey
			}
		}
		return accountRequest{accountID: accountID, serverKey: serverKey}, true
	}

	id, err := s.cfg.Resolver.Resolve(ctx, owner, key)
	switch {
	case errors.Is(err, accountauth.ErrAccountSelectionRequired):
		s.fail(w, http.StatusBadRequest, CodeAccountSelectionRequired,
			"More than one account of yours is available for this server; name the account in the path.",
			requestID)
		return accountRequest{}, false
	case errors.Is(err, accountauth.ErrNoEligibleAccount):
		s.fail(w, http.StatusNotFound, CodeAccountNotOwned,
			"No account of yours is available for this server.", requestID)
		return accountRequest{}, false
	case err != nil:
		s.storeError(w, "account_resolve", err, requestID)
		return accountRequest{}, false
	}
	return accountRequest{accountID: id, serverKey: key, keyed: true}, true
}

// requireAccountPermission runs the whole pipeline for one resource route.
//
// Ownership comes first, then the application permission, and only then is a
// worker resolved. A request that fails either check never reaches Telegram.
func (s *Server) requireAccountPermission(ctx context.Context, w http.ResponseWriter, r *http.Request,
	accountID string, kind channelstore.ResourceKind, action channelstore.Action, localID int64,
	requestID string) bool {

	owner := actorOf(r).user.ID
	if owner <= 0 {
		s.forbidden(w, requestID)
		return false
	}
	if _, err := s.cfg.Accounts.ByID(ctx, owner, accountID); err != nil {
		if errors.Is(err, channelstore.ErrAccountNotFound) {
			s.fail(w, http.StatusNotFound, CodeNotFound, "No account of yours has that id.", requestID)
			return false
		}
		s.storeError(w, "account_lookup", err, requestID)
		return false
	}
	if !visible(actorAuth(r), kind, action, localID) {
		s.forbidden(w, requestID)
		return false
	}
	return true
}

// requireChannelForAccount resolves a channel through the account that owns it.
//
// A valid channel id in a URL must not reach past the account in the same URL:
// the row is looked up under that account, so another account's registration is
// simply not there.
func (s *Server) requireChannelForAccount(ctx context.Context, w http.ResponseWriter, r *http.Request,
	accountID, rawChannelID, requestID string) (channelstore.Channel, bool) {

	channelID, err := parseChannelID(rawChannelID)
	if err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return channelstore.Channel{}, false
	}
	store := s.cfg.AccountChannels
	if store == nil {
		if s.cfg.Channels != nil {
			store, _ = s.cfg.Channels.(AccountChannels)
		}
	}
	if store == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"Local channel storage is not configured.", requestID)
		return channelstore.Channel{}, false
	}
	row, found, err := store.GetForAccount(ctx, accountID, channelID)
	if err != nil {
		s.storeError(w, "channel_lookup", err, requestID)
		return channelstore.Channel{}, false
	}
	if !found {
		// Not registered under this account: it may not even be a channel the
		// caller could name. 404, never 403.
		s.fail(w, http.StatusNotFound, CodeNotFound,
			"This channel is not registered for that account.", requestID)
		return channelstore.Channel{}, false
	}
	if !visible(actorAuth(r), channelstore.KindChannel, channelstore.ActionView, row.ID) {
		s.forbidden(w, requestID)
		return channelstore.Channel{}, false
	}
	return row, true
}

// accountView is the public shape of one Telegram account.
//
// It carries the identity a user needs to recognise their account and the
// connection state they need to act on it. It never carries a session, a phone
// number in the clear, or any other secret.
type accountView struct {
	ID                string `json:"id"`
	ServerKey         string `json:"server_key"`
	NetworkType       string `json:"network_type"`
	RemoteUserID      int64  `json:"remote_user_id,omitempty"`
	RemoteUsername    string `json:"remote_username,omitempty"`
	RemoteDisplayName string `json:"remote_display_name,omitempty"`
	PhoneNumberMasked string `json:"phone_masked,omitempty"`
	Status            string `json:"status"`
	AuthorizedAt      any    `json:"authorized_at"`
	LastConnectedAt   any    `json:"last_connected_at"`
	LastErrorCode     string `json:"last_error_code,omitempty"`
	// Channels is how many local channel registrations this account holds. It
	// is a count, never the rows themselves.
	Channels int `json:"channels"`
}

func accountViewOf(a channelstore.Account, channels int) accountView {
	return accountView{
		ID:                a.ID,
		ServerKey:         a.ServerKey,
		NetworkType:       a.NetworkType,
		RemoteUserID:      a.RemoteUserID,
		RemoteUsername:    a.RemoteUsername,
		RemoteDisplayName: a.RemoteDisplayName,
		PhoneNumberMasked: a.PhoneNumberMasked,
		Status:            a.Status,
		AuthorizedAt:      formatTime(a.AuthorizedAt),
		LastConnectedAt:   formatTime(a.LastConnectedAt),
		LastErrorCode:     a.LastErrorCode,
		Channels:          channels,
	}
}

func (s *Server) accountViewOf(ctx context.Context, a channelstore.Account, channels int) accountView {
	view := accountViewOf(a, channels)
	if s.cfg.AccountStatus != nil {
		if status := s.cfg.AccountStatus(ctx, a); status != "" {
			view.Status = status
		}
	}
	return view
}

// parseChannelID validates a channel id named in a path.
func parseChannelID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("channel_id must be a positive integer.")
	}
	return id, nil
}

// storeAccounts adapts the account repository to the gateway's narrower seam.

// legacyChannelAccess is the account a legacy {key} route resolves to when no
// account store is wired: the quarantined account of that server key.
//
// It is the transition state R8 describes. A channel joined through the legacy
// path lands on the quarantined account of the server it was joined through, so
// a key addresses exactly that record and nothing else. It never resolves to a
// user's account, and it is replaced the moment Accounts is configured.
type legacyChannelAccess struct {
	accounts LegacyAccountLookup
}

// LegacyAccountLookup maps a legacy client key to the account holding its
// channels. It is the store's ResolveLegacy.
type LegacyAccountLookup interface {
	ResolveLegacy(ctx context.Context, legacyKey string) (string, error)
}

// resolvedChannelStore is the channel lookups one resolved account makes.
//
// A request that named an account reaches its own registrations and no other's.
// That is the whole of what "the channel belongs to the requesting user" means
// at the store: the row is looked up under the account, so another account's
// registration is simply not there.
type resolvedChannelStore struct {
	base      AccountChannels
	accountID string
	// ownerUserID is the caller, for the write that records a registration.
	ownerUserID int64
}

// Get resolves one Telegram channel under this account.
func (r resolvedChannelStore) Get(ctx context.Context, _ string, channelID int64) (channelstore.Channel, bool, error) {
	if r.base == nil {
		return channelstore.Channel{}, false, nil
	}
	return r.base.GetForAccount(ctx, r.accountID, channelID)
}

// ManagedIDs marks search results this account registered.
func (r resolvedChannelStore) ManagedIDs(_ context.Context, _ string) (map[int64]bool, error) {
	if r.base == nil {
		return map[int64]bool{}, nil
	}
	return r.base.ManagedIDsForAccount(context.Background(), r.accountID)
}

// Upsert records a channel under this account, never under a quarantined one.
func (r resolvedChannelStore) Upsert(ctx context.Context, ch channelstore.Channel) (channelstore.Channel, error) {
	if r.base == nil {
		return channelstore.Channel{}, errors.New("gateway: local channel storage is not configured")
	}
	ch.AccountID = r.accountID
	opts := []channelstore.RegisterOption{}
	if r.ownerUserID > 0 {
		opts = append(opts, channelstore.WithOwner(r.ownerUserID))
	}
	return r.base.RegisterForAccount(ctx, ch, opts...)
}

// scopedToAccount narrows the channel store a request sees to one account.
//
// Every handler below reads the store through the request, so scoping it here
// is what makes "the channel belongs to the requesting user" true for a list, a
// detail route, a nested route and a search alike. A store that is not the
// account-scoped one is left alone.
func (s *Server) scopedToAccount(ctx context.Context, r *http.Request, accountID string) *http.Request {
	base := s.accountChannels()
	if base == nil {
		return r
	}
	owner := actorOf(r).user.ID
	if owner <= 0 {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), scopedStoreKey{},
		resolvedChannelStore{base: base, accountID: accountID, ownerUserID: owner}))
}

// scopedStoreKey is where a request carries its account-scoped channel store.
type scopedStoreKey struct{}

// scopeStore is the request-independent form of scopedToAccount, for helpers
// that hold a context rather than the request.
func (s *Server) scopeStore(ctx context.Context, r *http.Request, accountID string) context.Context {
	base := s.accountChannels()
	if base == nil {
		return ctx
	}
	owner := actorOf(r).user.ID
	if owner <= 0 {
		return ctx
	}
	return context.WithValue(ctx, scopedStoreKey{},
		resolvedChannelStore{base: base, accountID: accountID, ownerUserID: owner})
}

// storeFor is the channel store behind one context.
func (s *Server) storeFor(ctx context.Context) ManagedStore {
	if scoped, ok := ctx.Value(scopedStoreKey{}).(resolvedChannelStore); ok {
		return scoped
	}
	return s.cfg.Channels
}

// channelsOf is the channel store a request reaches: the account-scoped one
// when the request named an account, and the deployment's store otherwise.
func (s *Server) channelsOf(r *http.Request) ManagedStore {
	if scoped, ok := r.Context().Value(scopedStoreKey{}).(resolvedChannelStore); ok {
		return scoped
	}
	return s.cfg.Channels
}

// accountWorkerRegistry is the worker registry behind the account routing seam.
//
// The routing key is the account id and nothing else (contract §5): a request
// that named an account reaches that account's worker, and one that named a
// server key reaches the worker of the account it resolved to. There is no
// fallback of any kind — an account with no worker is unavailable, and no other
// account's worker is ever substituted for it.
type accountWorkerRegistry struct {
	byAccount map[string]Worker
}

// Worker resolves one account's worker.
func (r accountWorkerRegistry) Worker(accountID string) (Worker, bool) {
	if r.byAccount == nil {
		return nil, false
	}
	w, ok := r.byAccount[accountID]
	return w, ok
}

// accountScopedWorkers narrows the deployment's registry to the accounts a
// caller owns.
//
// It is what makes "the caller reaches only their own accounts" true at the
// routing seam rather than only at the SQL: a worker registered for someone
// else's account is simply not in the map. An owner with no account at all gets
// an empty registry, so a request cannot resolve to a session that is not
// theirs.
func (s *Server) accountScopedWorkers(ctx context.Context, owner int64) Registry {
	out := accountWorkerRegistry{byAccount: map[string]Worker{}}
	if owner <= 0 || s.cfg.Accounts == nil || s.cfg.Workers == nil {
		return out
	}
	accounts, err := s.cfg.Accounts.ListByOwner(ctx, owner)
	if err != nil {
		s.logf("gateway: op=account_routing owner=%d: cannot list accounts: %v", owner, err)
		return out
	}
	for _, a := range accounts {
		if !accountAvailableForRouting(a) {
			continue
		}
		if w, ok := s.cfg.Workers.Worker(a.ID); ok {
			out.byAccount[a.ID] = w
		}
	}
	return out
}

func accountAvailableForRouting(a channelstore.Account) bool {
	switch a.Status {
	case channelstore.AccountStatusConnecting, channelstore.AccountStatusQuarantined,
		channelstore.AccountStatusRemoved, channelstore.AccountStatusDisabled:
		return false
	default:
		return true
	}
}

// accountIDOf is the Telegram account one caller acts as, as of this request.
//
// It is the resolved account id and nothing else: never a server key, never a
// username, never a phone number, and never a value a request body supplied. A
// request that named no account has none here, and the work it starts is
// refused rather than run under somebody else's session.
func accountIDOf(a *actor) string {
	if a == nil {
		return ""
	}
	return a.accountID
}

// forAccount is one request bound to the account it will act as.
//
// The binding is made once, at the authorization pipeline, and is what a
// background job carries into the durable queue: a send recorded against one
// user's account is never delivered through another's session.
func withAccount(r *http.Request, accountID string) *http.Request {
	a := actorOf(r)
	bound := *a
	bound.accountID = accountID
	return withActor(r, &bound)
}

func withRouteSelector(r *http.Request, selector string) *http.Request {
	a := actorOf(r)
	bound := *a
	bound.routeSelector = selector
	return withActor(r, &bound)
}

func routeSelectorOf(r *http.Request, serverKey string) string {
	if selector := actorOf(r).routeSelector; selector != "" {
		return selector
	}
	return serverKey
}

// accountRouteIDForResponse identifies responses whose request explicitly
// named /telegram/accounts/{account_id}. Legacy keyed routes keep their
// existing response contract; account routes never make the server key stand
// in for the authenticated session identity.
func accountRouteIDForResponse(r *http.Request) string {
	if strings.HasPrefix(actorOf(r).routeSelector, "accounts/") {
		return accountIDOf(actorOf(r))
	}
	return ""
}
