package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
)

// Tokens issues and verifies access tokens.
type Tokens interface {
	Issue(userID int64) (string, time.Time, error)
	Parse(token string) (int64, error)
}

// Error codes for authentication, authorization and account writes.
const (
	CodeForbidden    = "forbidden"
	CodeInvalidCreds = "invalid_credentials"
	CodeUserInactive = "user_inactive"
)

// loginPath is the public login route; mePath is the caller's own profile;
// changePasswordPath is how a caller changes their own password.
const (
	loginPath          = "/api/v1/auth/login"
	mePath             = "/api/v1/auth/me"
	changePasswordPath = "/api/v1/auth/password"
)

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	// UserID is recognized so a client that posts one is not rejected for
	// quoting an unknown field, and is then ignored. The subject is always the
	// bearer's account: see the handler below.
	UserID int64 `json:"user_id,omitempty"`
}

// changePassword serves POST /api/v1/auth/password: the caller changes their
// own password and gets a fresh token back.
//
// The subject is always the caller's own account. The body names no one: a
// `user_id` field in it is ignored on purpose, so this route can never stand
// in for the admin user routes, which are the only place one account may set
// another's password.
//
// The account's previous HS256 token is NOT revoked. Those tokens are
// stateless: Parse checks the signature and the claims only, and nothing on
// either side records a password change. What invalidates a token on its next
// request is the account itself: `loadActor` re-reads the user and refuses a
// missing or inactive account. The token this route answers with is the
// caller's fresh one; the old one carries on until it expires. That is the
// existing design (internal/auth/token.go), stated as it is.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"Changing a password must be sent as POST.", requestID)
		return
	}
	if s.cfg.Users == nil || auth.Missing(s.cfg.Tokens) {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"User login is not configured: set api.jwt.secret_env (or api.jwt.secret) in the configuration.",
			requestID)
		return
	}
	var req changePasswordRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"current_password and new_password are required.", requestID)
		return
	}

	// The caller is the subject: `requireAuth` resolved their account from the
	// bearer token, and the body carries no identity of its own. Their stored
	// hash is read here in case the account changed since authentication.
	//
	// A credential with no account behind it — the operator token — has no
	// password to change and never reaches this route's write. That is refused
	// outright rather than answered as a wrong password: see errNotAnAccount's
	// own 403 elsewhere.
	user := actorOf(r).user
	if user.ID <= 0 {
		s.forbidden(w, requestID)
		return
	}
	stored, found, err := s.cfg.Users.GetUser(r.Context(), user.ID)
	if err != nil {
		s.storeError(w, "change_password", err, requestID)
		return
	}
	if !found {
		auth.BurnPasswordCheck(req.CurrentPassword)
		s.invalidLogin(w, requestID)
		return
	}
	ok, err := auth.VerifyPassword(stored.PasswordHash, req.CurrentPassword)
	if err != nil {
		s.logf("gateway: op=change_password request_id=%s user_id=%d has an unusable password hash",
			requestID, user.ID)
		s.invalidLogin(w, requestID)
		return
	}
	if !ok {
		s.invalidLogin(w, requestID)
		return
	}

	if err := validateNewPassword(req.NewPassword); err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.storeError(w, "change_password", err, requestID)
		return
	}
	// The same write the admin user routes use: one confirmed change to the
	// stored hash, nothing else on the row.
	if _, err := s.cfg.Users.UpdateUser(r.Context(), user.ID,
		channelstore.UserUpdate{PasswordHash: &hash}); err != nil {
		s.storeError(w, "change_password", err, requestID)
		return
	}

	token, exp, err := s.cfg.Tokens.Issue(user.ID)
	if err != nil {
		s.storeError(w, "change_password", err, requestID)
		return
	}
	ttl := int(exp.Sub(s.cfg.Now()).Seconds())
	if ttl < 1 {
		ttl = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   ttl,
	})
}

// validateNewPassword applies the same policy the user routes enforce on a new
// password.
func validateNewPassword(password string) error {
	if len(password) < channelstore.MinPasswordLen {
		return fmt.Errorf("new_password must be at least %d characters", channelstore.MinPasswordLen)
	}
	if len(password) > channelstore.MaxPasswordLen {
		return fmt.Errorf("new_password must be at most %d characters", channelstore.MaxPasswordLen)
	}
	return nil
}

// actor is the authenticated caller, as of this request.
type actor struct {
	user channelstore.User
	auth *channelstore.Authorizer
	// permissionSet is nil for a caller without an account.
	permissionSet *channelstore.PermissionSet
}

// principalKey is the context key carrying the actor.
type principalKey struct{}

func withActor(r *http.Request, a *actor) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, a))
}

func actorOf(r *http.Request) *actor {
	if a, ok := r.Context().Value(principalKey{}).(*actor); ok {
		return a
	}
	return &actor{auth: channelstore.NewAuthorizer(nil, nil)}
}

// actorAuth is the caller's authorizer; an unauthenticated request gets one
// that denies everything.
func actorAuth(r *http.Request) *channelstore.Authorizer {
	return actorOf(r).auth
}

// errNotAnAccount is the answer for a credential with no account behind it.
var errNotAnAccount = errors.New("the operator token cannot remove a local channel; " +
	"use an account holding the channel's delete permission.")

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// login serves POST /api/v1/auth/login.
//
// Wrong password and unknown username are the same 401, and an unknown username
// still costs one password verification, so neither the answer nor its timing
// says whether an account exists. An inactive account is refused only after its
// password checks out: a stranger learns nothing it could not learn anyway,
// and the account holder is told why.
func (s *Server) login(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"Logging in must be sent as POST.", requestID)
		return
	}
	if s.cfg.Users == nil || auth.Missing(s.cfg.Tokens) {
		// A deployment without api.jwt.secret_env (or api.jwt.secret) has no
		// signer. Answering 503 says how to fix it; dereferencing the nil
		// signer would crash the request, and with it every other request the
		// server is handling.
		s.logf("gateway: op=login request_id=%s login is not configured: " +
			"set api.jwt.secret_env (or api.jwt.secret) in the configuration")
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"User login is not configured: set api.jwt.secret_env (or api.jwt.secret) in the configuration.",
			requestID)
		return
	}
	var req loginRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		s.invalidLogin(w, requestID)
		return
	}

	user, found, err := s.cfg.Users.UserByUsername(r.Context(), username)
	if err != nil {
		s.storeError(w, "login", err, requestID)
		return
	}
	if !found {
		auth.BurnPasswordCheck(req.Password)
		s.invalidLogin(w, requestID)
		return
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil {
		// An unreadable stored hash is an operator problem, not a hint for the
		// caller. It is logged without the hash.
		s.logf("gateway: op=login request_id=%s user_id=%d has an unusable password hash", requestID, user.ID)
		s.invalidLogin(w, requestID)
		return
	}
	if !ok {
		s.invalidLogin(w, requestID)
		return
	}
	if !user.IsActive {
		s.fail(w, http.StatusForbidden, CodeUserInactive, "This account is inactive.", requestID)
		return
	}

	token, exp, err := s.cfg.Tokens.Issue(user.ID)
	if err != nil {
		s.storeError(w, "login", err, requestID)
		return
	}
	ttl := int(exp.Sub(s.cfg.Now()).Seconds())
	if ttl < 1 {
		ttl = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   ttl,
	})
}

func (s *Server) invalidLogin(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusUnauthorized, CodeInvalidCreds, "The username or password is incorrect.", requestID)
}

// me serves GET /api/v1/auth/me: the caller's profile and stored permissions,
// as they are in the database for this request.
func (s *Server) me(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Only GET is supported.", requestID)
		return
	}
	a := actorOf(r)
	view := userViewOf(a.user, a.permissionSet)
	// What the panel needs for its navigation: which parts of the API this
	// caller may read. A summary of the permission rules, not a new grant —
	// each route still checks for itself.
	view.Access = &accessView{
		Channels:       a.auth.CanList(channelstore.KindChannel),
		ChannelsCreate: a.auth.Global(channelstore.PermissionChannelsCreate),
		Categories:     a.auth.CanList(channelstore.KindCategory),
		Users:          a.auth.CanList(channelstore.KindUser),
		Servers:        a.auth.Global(channelstore.PermissionServersView),
		Content:        a.auth.Global(channelstore.PermissionContentPublish),
		Automation:     a.auth.Global(channelstore.PermissionAutomationView),
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: view,
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// bearerOf extracts the bearer credential from a request.
func bearerOf(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

// authenticate resolves the caller of a protected request.
//
// The token names a user, and that user is re-read on every request, so a
// deactivated, deleted or re-permissioned account takes effect on its next
// request rather than when its token expires.
func (s *Server) authenticate(r *http.Request) (*actor, error) {
	return s.authenticateCredential(r, bearerOf(r))
}

// authenticateCredential is authenticate for one credential, read from a header
// or from a JSON body.
func (s *Server) authenticateCredential(r *http.Request, credential string) (*actor, error) {
	if credential == "" {
		return nil, auth.ErrTokenMissing
	}
	// The operator token is the deployment's own credential. It holds every
	// permission but has no account, so the account routes stay closed to it.
	if s.cfg.Operator != "" && subtle.ConstantTimeCompare([]byte(credential), []byte(s.cfg.Operator)) == 1 {
		return &actor{
			user: channelstore.User{Username: "operator", IsActive: true},
			auth: channelstore.NewAuthorizer(nil, nil).MarkService(),
		}, nil
	}
	if auth.Missing(s.cfg.Tokens) || s.cfg.Users == nil {
		return nil, auth.ErrTokenMissing
	}
	id, err := s.cfg.Tokens.Parse(credential)
	if err != nil {
		return nil, err
	}
	return s.loadActor(r.Context(), id)
}

// loadActor reads the caller's current account and permissions.
func (s *Server) loadActor(ctx context.Context, id int64) (*actor, error) {
	user, found, err := s.cfg.Users.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found || !user.IsActive {
		// The token is well formed but no longer belongs to a usable account:
		// it is treated exactly like an invalid one.
		return nil, auth.ErrTokenSubject
	}
	authorizer, err := s.cfg.Users.AuthorizerFor(ctx, id)
	if err != nil {
		return nil, err
	}
	set, err := s.cfg.Users.PermissionsOf(ctx, id)
	if err != nil {
		return nil, err
	}
	return &actor{user: user, auth: authorizer, permissionSet: &set}, nil
}

// requireAuth wraps a handler so it only runs for an authenticated caller.
//
// The caller's context carries the request timeout, the same bound every
// Telegram route runs under.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := requestIDFrom(r)
		a, err := s.authenticate(r)
		if err != nil {
			s.authFailure(w, err, requestID)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
		defer cancel()
		next(w, withActor(r.WithContext(ctx), a))
	}
}

// authFailure answers an authentication error.
//
// Every reason a credential is unusable is one 401; only an expired token says
// so, because a client can then log in again instead of guessing.
func (s *Server) authFailure(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, auth.ErrTokenExpired):
		w.Header().Set("WWW-Authenticate", `Bearer realm="telesync", error="invalid_token"`)
		s.fail(w, http.StatusUnauthorized, CodeUnauthorized,
			"The access token has expired; log in again.", requestID)
	case errors.Is(err, auth.ErrTokenMissing), errors.Is(err, auth.ErrTokenMalformed),
		errors.Is(err, auth.ErrTokenSignature), errors.Is(err, auth.ErrTokenIssuer),
		errors.Is(err, auth.ErrTokenSubject):
		w.Header().Set("WWW-Authenticate", `Bearer realm="telesync"`)
		s.fail(w, http.StatusUnauthorized, CodeUnauthorized,
			"A valid access token is required; log in at "+s.fullPath(loginPath)+" first.", requestID)
	default:
		s.storeError(w, "auth", err, requestID)
	}
}

// forbidden is the single 403 answer, so a caller cannot tell a missing
// permission for one resource from a missing permission for another.
func (s *Server) forbidden(w http.ResponseWriter, requestID string) {
	s.fail(w, http.StatusForbidden, CodeForbidden,
		"You do not have permission to perform this action.", requestID)
}

// storeError logs and answers a repository failure without exposing it.
func (s *Server) storeError(w http.ResponseWriter, op string, err error, requestID string) {
	s.logf("gateway: op=%s request_id=%s error: %v", op, requestID, err)
	s.fail(w, http.StatusInternalServerError, CodeStoreError, genericStoreMessage, requestID)
}

const genericStoreMessage = "The local store could not complete the request."
