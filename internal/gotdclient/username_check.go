package gotdclient

import (
	"context"
	"strings"
	"unicode"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// UsernameCheck is a conservative, advisory result. Assignable is nil because
// this precheck cannot prove either assignability or unassignability.
type UsernameCheck struct {
	Username   string `json:"username"`
	Status     string `json:"status"`
	Assignable *bool  `json:"assignable"`
}

// CheckUsername normalizes a public username and checks whether Telegram
// resolves it on this manager's already-selected connection. A no-match only
// means unclaimed at the time of the lookup; it is not a promise of assignment.
func (m *Manager) CheckUsername(ctx context.Context, username string) (UsernameCheck, error) {
	canonical := canonicalUsername(username)
	result := UsernameCheck{Username: canonical, Status: "invalid"}
	if !validCanonicalUsername(canonical) {
		return result, nil
	}

	result.Status = "unknown"
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return result, err
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: canonical})
	if err != nil {
		switch {
		case tgerr.Is(err, "USERNAME_NOT_OCCUPIED"):
			result.Status = "unclaimed"
			return result, nil
		case tgerr.Is(err, "USERNAME_INVALID"):
			result.Status = "invalid"
			return result, nil
		default:
			// Keep backend/transport errors visible to the caller. In particular,
			// do not retry against another endpoint or reinterpret failure as free.
			return result, ClassifyError(err)
		}
	}
	if resolved == nil || len(resolved.Chats) == 0 && len(resolved.Users) == 0 {
		// A successful but empty/malformed answer is not Telegram's explicit
		// USERNAME_NOT_OCCUPIED verdict; leave it unknown rather than infer free.
		return result, nil
	}
	result.Status = "occupied"
	return result, nil
}

func canonicalUsername(username string) string {
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	return strings.ToLower(username)
}

func validCanonicalUsername(username string) bool {
	if len(username) < 5 || len(username) > 32 {
		return false
	}
	for i, r := range username {
		if r > unicode.MaxASCII || !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
		if i == 0 && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}
