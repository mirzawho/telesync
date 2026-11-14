package accountauth

import (
	"context"
	"fmt"
	"time"
)

// Recover settles what an interrupted login left behind.
//
// It runs at startup and on demand. An attempt that never reached authorized is
// expired, its MTProto resources are released, and — if it had reached the
// account step — the account record it created is returned so the account store
// can drop a session nobody ever finished. An authorized attempt is never
// touched: that session is real and belongs to its owner.
func (s *Service) Recover(ctx context.Context) (RecoveryReport, error) {
	s.mux.Lock()
	now := s.now().UTC()
	s.mux.Unlock()

	report := RecoveryReport{CheckedAt: now}

	// Every live attempt is settled, whether or not its store can list them for
	// us: a login that outlived its TTL has no session worth keeping.
	for _, attempt := range s.dangling(ctx, now) {
		if attempt.Terminal() {
			continue
		}
		report.Incomplete++

		if err := s.closeMTProto(ctx, attempt, true, true, true); err != nil {
			return report, fmt.Errorf("accountauth: close an interrupted login session: %w", err)
		}
		report.Released++

		attempt.State = StateExpired
		clearQRToken(&attempt)
		attempt.NextStep = nextStep(attempt.State, attempt.Method)
		attempt.ErrorCode = CodeInterrupted
		attempt.UpdatedAt = now
		if err := s.attempts.UpdateState(ctx, attempt.OwnerUserID, attempt); err != nil {
			return report, fmt.Errorf("accountauth: settle an interrupted login: %w", err)
		}

		if attempt.AccountID != "" {
			if err := s.accounts.ReleasePending(ctx, attempt.OwnerUserID, attempt.AccountID); err != nil {
				return report, fmt.Errorf("accountauth: release an unfinished account: %w", err)
			}
			report.AccountsReleased++
		}
	}

	// Records already settled are purged; a record settled above is kept until
	// the next pass so an owner can still see what happened to their attempt.
	if removed, err := s.attempts.DeleteExpired(ctx, now.Add(s.ttl)); err == nil {
		report.Purged = removed
	}
	return report, nil
}

// dangling lists the attempts that stopped halfway. A store that can list them
// is asked; otherwise the service asks for each one it knows about.
func (s *Service) dangling(ctx context.Context, at time.Time) []LoginAttempt {
	if lister, ok := s.attempts.(IncompleteLoginAttemptStore); ok {
		attempts, err := lister.Incomplete(ctx, at)
		if err != nil {
			return nil
		}
		return attempts
	}
	return nil
}

// CodeInterrupted marks an attempt that a restart cut short.
const CodeInterrupted = "login_attempt_interrupted"

// RecoveryReport says what a recovery pass settled.
//
// Every count is a fact about records and resources: nothing here is a claim
// about a live Telegram session.
type RecoveryReport struct {
	CheckedAt        time.Time `json:"checked_at"`
	Incomplete       int       `json:"incomplete"`
	Released         int       `json:"released"`
	AccountsReleased int       `json:"accounts_released"`
	Purged           int       `json:"purged"`
}

var _ Recovery = (*Service)(nil)
