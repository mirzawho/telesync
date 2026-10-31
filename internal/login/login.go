// Package login implements the one-time interactive authorization flow.
//
// The flow only starts when the stored session is not already authorized.
// Phone numbers, verification codes and 2FA passwords are never logged, and the
// session is re-checked after authentication so a silent failure cannot be
// reported as success.
package login

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
)

// MaxAttempts is how many times a failed authorization is retried before the
// auth command gives up.
const MaxAttempts = 3

// Session is a connected client that can report its authorization status.
type Session interface {
	Run(ctx context.Context, fn func(context.Context) error) error
}

// Flow drives status, prompting and authorization.
type Flow interface {
	// Status returns the authenticated user, or nil when unauthorized.
	Status(ctx context.Context) (*tg.User, error)
	// Phone asks the user for a phone number.
	Phone(ctx context.Context) (string, error)
	// Authenticate runs the interactive login (code, and 2FA if required).
	Authenticate(ctx context.Context, phone string) error
}

// Result describes what happened during login.
type Result struct {
	AlreadyAuthorized bool
	Authenticated     bool
	Attempts          int
	UserID            int64
	Username          string
}

// Run performs login for one client with the default attempt budget.
//
// phone is the configured phone number; when empty the user is prompted.
func Run(ctx context.Context, sess Session, flow Flow, client, phone string) (Result, error) {
	return RunWithAttempts(ctx, sess, flow, client, phone, MaxAttempts)
}

// RunWithAttempts performs login, retrying up to attempts times when the
// session is still unauthorized after an apparently successful flow.
func RunWithAttempts(
	ctx context.Context,
	sess Session,
	flow Flow,
	client, phone string,
	attempts int,
) (Result, error) {
	if attempts < 1 {
		attempts = 1
	}
	res := Result{}

	err := sess.Run(ctx, func(cbCtx context.Context) error {
		user, err := flow.Status(cbCtx)
		if err != nil {
			return fmt.Errorf("read authorization status: %w", err)
		}
		if user != nil {
			res.AlreadyAuthorized = true
			res.UserID = user.ID
			res.Username = user.Username
			return nil
		}

		var lastErr error
		for attempt := 1; attempt <= attempts; attempt++ {
			if err := cbCtx.Err(); err != nil {
				return err
			}
			// Prompt again on every attempt: a retry usually means the number
			// or the code was mistyped, so the user must be able to correct it.
			attemptPhone := phone
			if strings.TrimSpace(attemptPhone) == "" {
				v, err := flow.Phone(cbCtx)
				if err != nil {
					return fmt.Errorf("read phone number: %w", err)
				}
				attemptPhone = strings.TrimSpace(v)
				if attemptPhone == "" {
					return fmt.Errorf("a phone number is required to log in")
				}
				// Remember it so a retry does not ask for the same number twice.
				phone = attemptPhone
			}

			if err := flow.Authenticate(cbCtx, attemptPhone); err != nil {
				lastErr = err
				continue
			}
			res.Attempts = attempt

			// Authenticate returning nil only means the RPCs succeeded. Confirm
			// the account really is signed in before reporting success.
			user, err := flow.Status(cbCtx)
			if err != nil {
				return fmt.Errorf("verify authorization after login: %w", err)
			}
			if user == nil {
				lastErr = fmt.Errorf("the login flow finished but the session is still not authorized")
				continue
			}

			res.Authenticated = true
			res.UserID = user.ID
			res.Username = user.Username
			return nil
		}

		if lastErr == nil {
			lastErr = fmt.Errorf("login did not complete")
		}
		return fmt.Errorf("after %d attempt(s) the session is still not authorized: %w; "+
			"no session was saved for this attempt", attempts, lastErr)
	})
	if err != nil {
		return res, fmt.Errorf("%s: %w", client, err)
	}
	return res, nil
}
