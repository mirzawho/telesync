package gateway

import (
	"context"
	"net/http"

	"github.com/mirza/telesync/internal/channelstore"
)

// serverUser is the Telegram account a server is signed in as, for the
// dashboard's server status. It is null whenever the identity is not known (no
// worker, an unreachable one, an account not authorized), and such a row never
// claims one. It is the server's own account, never the caller's.
type serverUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// requireServersView answers 403 unless the caller may read the server status
// rows, which carry servers' Telegram accounts. The operator token holds it.
func (s *Server) requireServersView(w http.ResponseWriter, r *http.Request, requestID string) bool {
	if !actorAuth(r).Global(channelstore.PermissionServersView) {
		s.forbidden(w, requestID)
		return false
	}
	return true
}

// serverUser asks the worker who the server is signed in as, and answers nil
// whenever that is not known. A failure here never fails the listing: the
// connection facts are still reported.
func (s *Server) serverUser(ctx context.Context, info ServerInfo, requestID string) *serverUser {
	if !info.Registered {
		return nil
	}
	worker, ok := s.cfg.Workers.Worker(info.Key)
	if !ok {
		return nil
	}
	profile, err := worker.Profile(ctx)
	if err != nil {
		s.logf("gateway: client=%s op=servers request_id=%s profile failed: %v", info.Key, requestID, err)
		return nil
	}
	if !profile.Authorized || profile.User == nil {
		return nil
	}
	return &serverUser{
		ID:        profile.User.ID,
		FirstName: profile.User.FirstName,
		LastName:  profile.User.LastName,
		Username:  profile.User.Username,
	}
}
