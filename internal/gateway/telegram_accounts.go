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

// This file serves the account-scoped routes (v0.0.5 contract §6.1).
//
// They are the routes that name an identity explicitly, so nothing here has to
// guess which account a request acts as. Each one runs the authorization
// pipeline: the caller, then the account they own, then the application
// permission, and only then the account's worker.

// The account routes, as one prefix and the paths below it.
const (
	accountsPrefix = "/api/v1/me/telegram-accounts"
	// accountsResources is the account-scoped resource family, where a request
	// names the account it acts as.
	accountsResources = "/api/v1/telegram/accounts/"
)

// Login error codes, matching the login state machine's contract.
const (
	CodeLoginAttemptExpired       = "login_attempt_expired"
	CodeLoginAttemptInvalid       = "login_attempt_invalid"
	CodeLoginRateLimited          = "login_rate_limited"
	CodeUnsupportedLoginMethod    = "unsupported_login_method"
	CodeLoginFailed               = "login_failed"
	CodeLoginPasswordRequired     = "login_password_required"
	CodeLoginCodeRequired         = "login_code_required"
	CodeLoginNotConfigured        = "login_not_configured"
	CodeAccountAlreadyExists      = "account_already_exists"
	CodeAccountSessionUnavailable = "account_session_unavailable"
)

// accountLoginService is the login state machine the account API drives.
//
// It is the accountauth.LoginService contract, named here so the gateway does
// not depend on the whole login package to serve four routes. A login is
// Telesync's second authentication system and never issues an application
// token.
type accountLoginService interface {
	Start(ctx context.Context, owner int64, req accountauth.StartRequest) (accountauth.LoginAttempt, error)
	Status(ctx context.Context, owner int64, attemptID string) (accountauth.LoginAttempt, error)
	SubmitCode(ctx context.Context, owner int64, attemptID, code string) (accountauth.LoginAttempt, error)
	SubmitPassword(ctx context.Context, owner int64, attemptID, password string) (accountauth.LoginAttempt, error)
	Cancel(ctx context.Context, owner int64, attemptID string) error
}

// accounts serves the /api/v1/me/telegram-accounts family.
//
// Everything under it is the caller's own. A credential with no account behind
// it — the operator token — reaches none of it: it holds application
// permissions, and application permissions are not a Telegram session.
func (s *Server) accounts(w http.ResponseWriter, r *http.Request, requestID string) {
	if s.cfg.Accounts == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail,
			"Telegram account storage is not configured.", requestID)
		return
	}
	owner := actorOf(r).user.ID
	if owner <= 0 {
		s.forbidden(w, requestID)
		return
	}

	rest, _ := strings.CutPrefix(r.URL.Path, s.fullPath(accountsPrefix))
	rest = strings.Trim(rest, "/")
	if rest == "" {
		s.listAccounts(w, r, owner, requestID)
		return
	}
	segments := strings.Split(rest, "/")

	if segments[0] == "login" {
		s.serveLogin(w, r, owner, segments[1:], requestID)
		return
	}
	if len(segments) == 1 {
		s.accountResource(w, r, owner, segments[0], requestID)
		return
	}
	if len(segments) == 2 {
		s.accountAction(w, r, owner, segments[0], segments[1], requestID)
		return
	}
	s.notFound(w, requestID)
}

// listAccounts serves GET /api/v1/me/telegram-accounts.
//
// The listing is the caller's own accounts and nothing else. It carries no
// session bytes, no phone number in the clear and no credential of any kind.
func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request, owner int64, requestID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Only GET is supported.", requestID)
		return
	}
	for name := range r.URL.Query() {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"unknown query parameter "+strconv.Quote(name)+".", requestID)
		return
	}
	accounts, err := s.cfg.Accounts.ListByOwner(r.Context(), owner)
	if err != nil {
		s.storeError(w, "list_accounts", err, requestID)
		return
	}
	views := make([]accountView, 0, len(accounts))
	for _, a := range accounts {
		views = append(views, s.accountViewOf(r.Context(), a, s.accountChannelCount(r.Context(), a.ID)))
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"accounts": views},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// accountChannelCount is how many local channels one account registered.
func (s *Server) accountChannelCount(ctx context.Context, accountID string) int {
	store := s.accountChannels()
	if store == nil {
		return 0
	}
	rows, err := store.ListChannelsForAccount(ctx, accountID)
	if err != nil {
		return 0
	}
	return len(rows)
}

// accountChannels is the account-scoped channel seam, or nil.
func (s *Server) accountChannels() AccountChannels {
	if s.cfg.AccountChannels != nil {
		return s.cfg.AccountChannels
	}
	store, _ := s.cfg.Channels.(AccountChannels)
	return store
}

// accountResource serves GET and DELETE /api/v1/me/telegram-accounts/{account_id}.
func (s *Server) accountResource(w http.ResponseWriter, r *http.Request, owner int64, id, requestID string) {
	rec, err := s.cfg.Accounts.ByID(r.Context(), owner, id)
	if err != nil {
		if errors.Is(err, channelstore.ErrAccountNotFound) {
			s.fail(w, http.StatusNotFound, CodeNotFound, "No account of yours has that id.", requestID)
			return
		}
		s.storeError(w, "account_lookup", err, requestID)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, envelope{
			Data: map[string]any{"account": s.accountViewOf(r.Context(), rec, s.accountChannelCount(r.Context(), rec.ID))},
			Meta: metaFor(requestID, s.cfg.Now()),
		})
	case http.MethodDelete:
		if !s.stopAccountRunner(w, r.Context(), owner, rec.ID, requestID) {
			return
		}
		if err := s.cfg.Accounts.Delete(r.Context(), owner, id); err != nil {
			s.storeError(w, "delete_account", err, requestID)
			return
		}
		rec.Status = channelstore.AccountStatusRemoved
		if !s.clearAccountSession(w, r.Context(), owner, rec, requestID) {
			return
		}
		writeJSON(w, http.StatusOK, envelope{
			Data: map[string]any{"deleted": true, "id": rec.ID},
			Meta: metaFor(requestID, s.cfg.Now()),
		})
	default:
		w.Header().Set("Allow", "GET, DELETE")
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"This account resource supports GET and DELETE.", requestID)
	}
}

// accountAction serves the two state-changing actions on one account.
//
// Both are owner-scoped in the store, so an id belonging to someone else is
// not-found exactly like one that was never minted.
func (s *Server) accountAction(w http.ResponseWriter, r *http.Request, owner int64, id, action, requestID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"This action changes state and must be sent as POST.", requestID)
		return
	}
	rec, err := s.cfg.Accounts.ByID(r.Context(), owner, id)
	if err != nil {
		if errors.Is(err, channelstore.ErrAccountNotFound) {
			s.fail(w, http.StatusNotFound, CodeNotFound, "No account of yours has that id.", requestID)
			return
		}
		s.storeError(w, "account_lookup", err, requestID)
		return
	}
	status := channelstore.AccountStatusDisconnected
	switch action {
	case "disconnect":
		status = channelstore.AccountStatusDisconnected
	case "logout":
		status = channelstore.AccountStatusAuthenticationRequired
	default:
		s.notFound(w, requestID)
		return
	}
	if !s.stopAccountRunner(w, r.Context(), owner, rec.ID, requestID) {
		return
	}
	if err := s.cfg.Accounts.SetStatus(r.Context(), owner, rec.ID, status, ""); err != nil {
		s.storeError(w, "account_"+action, err, requestID)
		return
	}
	rec.Status = status
	if action == "logout" && !s.clearAccountSession(w, r.Context(), owner, rec, requestID) {
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"id": rec.ID, "status": status},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

func (s *Server) accountAuthorized(ctx context.Context, owner int64, attempt accountauth.LoginAttempt) {
	if attempt.State != accountauth.StateAuthorized || attempt.AccountID == "" ||
		s.cfg.AccountAuthorized == nil {
		return
	}
	s.cfg.AccountAuthorized(ctx, owner, attempt)
}

func (s *Server) stopAccountRunner(w http.ResponseWriter, ctx context.Context, owner int64, accountID, requestID string) bool {
	if s.cfg.StopAccount == nil {
		return true
	}
	if err := s.cfg.StopAccount(ctx, owner, accountID); err != nil {
		s.logf("gateway: op=account_runner_stop account=%s: %v", accountID, err)
		s.fail(w, http.StatusServiceUnavailable, CodeAccountNotConnected,
			"The account Runner could not be stopped safely.", requestID)
		return false
	}
	return true
}

func (s *Server) clearAccountSession(
	w http.ResponseWriter,
	ctx context.Context,
	owner int64,
	account channelstore.Account,
	requestID string,
) bool {
	if s.cfg.ClearAccountSession == nil {
		return true
	}
	if err := s.cfg.ClearAccountSession(ctx, owner, account); err != nil {
		s.logf("gateway: op=account_session_clear account=%s: %v", account.ID, err)
		s.fail(w, http.StatusServiceUnavailable, CodeAccountSessionUnavailable,
			"The account session could not be cleared safely.", requestID)
		return false
	}
	return true
}

// serveLogin serves the login attempt family under the account routes.
//
// Telesync web authentication and MTProto authentication stay two separate
// systems: this is the second, and it never issues an application token.
func (s *Server) serveLogin(w http.ResponseWriter, r *http.Request, owner int64, rest []string, requestID string) {
	// "start" is a route, not an attempt id. Handle it before the generic
	// attempt-id branch so POST /login/start starts an attempt.
	if len(rest) == 1 && rest[0] == "start" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Starting a login must be sent as POST.", requestID)
			return
		}
		if s.cfg.Logins == nil {
			s.fail(w, http.StatusServiceUnavailable, CodeLoginNotConfigured,
				"Telegram login is not configured in this deployment.", requestID)
			return
		}
		s.startLogin(w, r, owner, requestID)
		return
	}

	// An attempt is named by its id, and ids are not guessable: a request for
	// one this deployment does not hold answers 404 whether or not a login
	// service is configured at all. Anything else would let a guessed id tell a
	// caller whether login exists here.
	if len(rest) > 0 {
		attemptID := rest[0]
		if len(rest) == 1 {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
					"Reading a login attempt is only GET.", requestID)
				return
			}
			if s.cfg.Logins == nil {
				s.fail(w, http.StatusNotFound, CodeLoginAttemptInvalid,
					"No login attempt of yours has that id.", requestID)
				return
			}
			s.loginStatus(w, r, owner, attemptID, requestID)
			return
		}
		if len(rest) == 2 {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
					"This login step changes state and must be sent as POST.", requestID)
				return
			}
			if s.cfg.Logins == nil {
				s.fail(w, http.StatusNotFound, CodeLoginAttemptInvalid,
					"No login attempt of yours has that id.", requestID)
				return
			}
			s.loginStep(w, r, owner, attemptID, rest[1], requestID)
			return
		}
		s.notFound(w, requestID)
		return
	}

	if s.cfg.Logins == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeLoginNotConfigured,
			"Telegram login is not configured in this deployment.", requestID)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"Starting a login must be sent as POST.", requestID)
		return
	}
	s.startLogin(w, r, owner, requestID)
}

// loginStartRequest begins one login.
type loginStartRequest struct {
	ServerKey string `json:"server_key"`
	// NetworkType is accepted for compatibility with early v0.0.5 clients,
	// but the gateway derives the authoritative value from the configured
	// server definition and rejects a mismatch.
	NetworkType string `json:"network_type,omitempty"`
	Method      string `json:"method"`
	Phone       string `json:"phone_number"`
}

// loginNetworkType resolves a configured, enabled server key into the
// network/session type the login service requires. The client chooses only the
// server key; it cannot redirect a custom server through the official path or
// vice versa.
func (s *Server) loginNetworkType(serverKey string) (string, bool) {
	for _, server := range s.cfg.Servers {
		if server.Key != serverKey || !server.Enabled {
			continue
		}
		switch server.Type {
		case "official":
			return channelstore.NetworkTelegram, true
		case "custom":
			return channelstore.NetworkCustom, true
		default:
			return "", false
		}
	}
	return "", false
}

// startLogin serves POST /api/v1/me/telegram-accounts/login/start.
func (s *Server) startLogin(w http.ResponseWriter, r *http.Request, owner int64, requestID string) {
	var req loginStartRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	serverKey := strings.TrimSpace(req.ServerKey)
	if serverKey == "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"server_key is required: it names the server definition, not an identity.", requestID)
		return
	}
	networkType, ok := s.loginNetworkType(serverKey)
	if !ok {
		s.fail(w, http.StatusNotFound, CodeUnknownClient,
			"The selected server is not configured or enabled.", requestID)
		return
	}
	if suppliedType := strings.TrimSpace(req.NetworkType); suppliedType != "" && suppliedType != networkType {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"network_type must match the selected server definition.", requestID)
		return
	}
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = accountauth.MethodPhone
	}
	if method == accountauth.MethodPhone && strings.TrimSpace(req.Phone) == "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"phone_number is required for phone login.", requestID)
		return
	}
	attempt, err := s.cfg.Logins.Start(r.Context(), owner, accountauth.StartRequest{
		ServerKey:   serverKey,
		NetworkType: networkType,
		Method:      method,
		Phone:       req.Phone,
	})
	if err != nil {
		s.loginError(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusCreated, envelope{
		Data: map[string]any{"attempt": loginAttemptViewOf(attempt)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// loginStatus serves GET /api/v1/me/telegram-accounts/login/{attempt_id}.
func (s *Server) loginStatus(w http.ResponseWriter, r *http.Request, owner int64, attemptID, requestID string) {
	attempt, err := s.cfg.Logins.Status(r.Context(), owner, attemptID)
	if err != nil {
		s.loginError(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		Data: map[string]any{"attempt": loginAttemptViewOf(attempt)},
		Meta: metaFor(requestID, s.cfg.Now()),
	})
}

// loginStep serves the three steps a login attempt accepts.
func (s *Server) loginStep(w http.ResponseWriter, r *http.Request, owner int64, attemptID, step, requestID string) {
	switch step {
	case "code":
		var req struct {
			Code string `json:"code"`
		}
		if err := decodeJSONBody(w, r, &req); err != nil {
			s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
			return
		}
		attempt, err := s.cfg.Logins.SubmitCode(r.Context(), owner, attemptID, req.Code)
		if err != nil {
			s.loginError(w, err, requestID)
			return
		}
		s.accountAuthorized(r.Context(), owner, attempt)
		writeJSON(w, http.StatusOK, envelope{
			Data: map[string]any{"attempt": loginAttemptViewOf(attempt)},
			Meta: metaFor(requestID, s.cfg.Now()),
		})
	case "password":
		var req struct {
			Password string `json:"password"`
		}
		if err := decodeJSONBody(w, r, &req); err != nil {
			s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
			return
		}
		attempt, err := s.cfg.Logins.SubmitPassword(r.Context(), owner, attemptID, req.Password)
		if err != nil {
			s.loginError(w, err, requestID)
			return
		}
		s.accountAuthorized(r.Context(), owner, attempt)
		writeJSON(w, http.StatusOK, envelope{
			Data: map[string]any{"attempt": loginAttemptViewOf(attempt)},
			Meta: metaFor(requestID, s.cfg.Now()),
		})
	case "cancel":
		if err := s.cfg.Logins.Cancel(r.Context(), owner, attemptID); err != nil {
			s.loginError(w, err, requestID)
			return
		}
		writeJSON(w, http.StatusOK, envelope{
			Data: map[string]any{"canceled": true, "id": attemptID},
			Meta: metaFor(requestID, s.cfg.Now()),
		})
	default:
		s.notFound(w, requestID)
	}
}

// loginAttemptViewOf is the public shape of one login attempt.
//
// It carries what the user needs to see what to do next, and nothing else: no
// code, no password, no QR token, and no submission counter a brute force could
// read.
func loginAttemptViewOf(a accountauth.LoginAttempt) map[string]any {
	return map[string]any{
		"id":            a.ID,
		"server_key":    a.ServerKey,
		"network_type":  a.NetworkType,
		"method":        a.Method,
		"state":         a.State,
		"next_step":     a.NextStep,
		"account_id":    a.AccountID,
		"phone_masked":  a.PhoneMasked,
		"delivery":      a.Delivery,
		"code_length":   a.CodeLength,
		"error_code":    a.ErrorCode,
		"expires_at":    RFC3339String(a.ExpiresAt),
		"resend_after":  formatTime(a.ResendAfter),
		"created_at":    RFC3339String(a.CreatedAt),
		"updated_at":    RFC3339String(a.UpdatedAt),
		"authorized_at": formatTime(a.AuthorizedAt),
		"has_qr":        a.QRToken != "",
		"qr_expires_at": formatTime(a.QRExpiresAt),
		"delivery_hint": sanitizeHint(a.DeliveryHint),
	}
}

// sanitizeHint renders a server-supplied description.
//
// The hint is third-party text from the remote server (an email pattern, an SMS
// prefix). It is untrusted: it is stripped of markup and control characters
// before it can reach a panel, and it never influences control flow.
func sanitizeHint(raw string) string {
	var sb strings.Builder
	for _, r := range raw {
		switch {
		case r == '<' || r == '>' || r == '&' || r == '"' || r == '\\' || r == '`':
			continue
		case r < 0x20 || r == 0x7f:
			continue
		default:
			sb.WriteRune(r)
		}
	}
	return strings.TrimSpace(sb.String())
}

// loginError maps a login state machine failure onto the documented codes.
func (s *Server) loginError(w http.ResponseWriter, err error, requestID string) {
	status, code, message := classifyLogin(err)
	if code == CodeStoreError {
		s.storeError(w, "login", err, requestID)
		return
	}
	s.fail(w, status, code, message, requestID)
}

func classifyLogin(err error) (int, string, string) {
	switch {
	case errors.Is(err, accountauth.ErrNotFound):
		// An attempt another owner holds is this answer too: no owner oracle.
		return http.StatusNotFound, CodeLoginAttemptInvalid,
			"No login attempt of yours has that id."
	case errors.Is(err, accountauth.ErrAttemptExpired), errors.Is(err, accountauth.ErrQRExpired):
		return http.StatusGone, CodeLoginAttemptExpired,
			"This login attempt has expired; start a new one."
	case errors.Is(err, accountauth.ErrAttemptFinished), errors.Is(err, accountauth.ErrInvalidTransition):
		return http.StatusConflict, CodeLoginAttemptInvalid,
			"This login attempt cannot take that step."
	case errors.Is(err, accountauth.ErrRateLimited):
		return http.StatusTooManyRequests, CodeLoginRateLimited,
			"Too many code submissions for this attempt; start a new one."
	case errors.Is(err, accountauth.ErrUnsupportedMethod):
		return http.StatusBadRequest, CodeUnsupportedLoginMethod,
			"This server does not support that login method."
	case errors.Is(err, accountauth.ErrWrongCode), errors.Is(err, accountauth.ErrCodeExpired):
		return http.StatusUnauthorized, CodeLoginCodeRequired,
			"The login code was not accepted."
	case errors.Is(err, accountauth.ErrWrongPassword):
		return http.StatusUnauthorized, CodeLoginPasswordRequired,
			"The two-step verification password was not accepted."
	case errors.Is(err, accountauth.ErrPasswordRequired):
		return http.StatusConflict, CodeLoginPasswordRequired,
			"Two-step verification is required for this account."
	case errors.Is(err, accountauth.ErrFloodWait):
		return http.StatusTooManyRequests, CodeRateLimited,
			"The server asked to wait before trying again."
	case errors.Is(err, accountauth.ErrNotAuthorized):
		return http.StatusForbidden, CodeForbidden,
			"This credential cannot start a Telegram login."
	case errors.Is(err, accountauth.ErrUnavailable):
		return http.StatusServiceUnavailable, CodeAccountSessionUnavailable,
			"The account session is unavailable."
	case errors.Is(err, channelstore.ErrDuplicateAccount):
		return http.StatusConflict, CodeAccountAlreadyExists,
			"That remote account is already registered for this user."
	}
	var coded interface{ Error() string }
	if errors.As(err, &coded) {
		return http.StatusBadGateway, CodeLoginFailed, "The Telegram server did not complete the login."
	}
	return http.StatusInternalServerError, CodeStoreError, "The login attempt could not be recorded."
}

// --- the account-scoped resource family -----------------------------------

// accountsResource serves account-scoped Telegram resources:
// /api/v1/telegram/accounts/{account_id}/channels/... and
// /api/v1/telegram/accounts/{account_id}/messages/copy.
//
// It is the same operation set as the keyed routes, with the identity named
// instead of resolved. The prefix below is what the keyed routes use, so the
// two families share one router and cannot drift apart.
func (s *Server) accountsResource(w http.ResponseWriter, r *http.Request, requestID string) {
	a, err := s.authenticate(r)
	if err != nil {
		s.authFailure(w, err, requestID)
		return
	}
	r = withActor(r, a)

	rest, _ := strings.CutPrefix(r.URL.Path, s.fullPath(accountsResources))
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	if len(segments) < 2 || segments[0] == "" {
		s.notFound(w, requestID)
		return
	}
	accountID, resource := segments[0], segments[1]
	isChannelResource := resource == "channels"
	isCopyResource := resource == "messages" && len(segments) == 3 && segments[2] == "copy"
	if !isChannelResource && !isCopyResource {
		s.notFound(w, requestID)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)

	req, ok := s.resolveAccount(ctx, w, r, "", accountID, requestID)
	if !ok {
		return
	}
	if req.accountID != accountID {
		// Defensive: the resolver never renames an explicitly named account.
		s.notFound(w, requestID)
		return
	}
	// A numeric Telegram id is usable through this account only when that
	// account has its own local registration. Global application visibility
	// cannot substitute for account ownership. Joining is the one exception:
	// that operation creates the registration it cannot yet look up.
	if isChannelResource && len(segments) >= 3 {
		_, channelIDErr := parseChannelID(segments[2])
		isJoin := len(segments) >= 4 && segments[3] == "join"
		if channelIDErr == nil && !isJoin {
			if _, found := s.requireChannelForAccount(ctx, w, r, accountID, segments[2], requestID); !found {
				return
			}
		}
	}
	owner := actorOf(r).user.ID
	worker, found := s.accountScopedWorkers(ctx, owner).Worker(req.accountID)
	if !found {
		s.fail(w, http.StatusServiceUnavailable, CodeAccountNotConnected,
			"No worker serves that account.", requestID)
		return
	}
	// The account-scoped family speaks the same paths as the keyed one, with
	// the account id where the key was, so it reuses the whole router.
	inner := "/api/v1/telegram/" + accountID + "/" + strings.Join(segments[1:], "/")
	rewritten := r.Clone(ctx)
	rewritten.URL.Path = s.cfg.BasePath + inner
	rewritten = withRouteSelector(rewritten, "accounts/"+accountID)
	routeSegments := append([]string{accountID}, segments[1:]...)
	s.routeTelegram(ctx, w, rewritten, req.accountID, req.serverKey, worker, resource, routeSegments, requestID)
}
