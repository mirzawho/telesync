package supervisor

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// This file is the account-keyed worker lifecycle: one worker per user-owned
// Telegram account, started after a successful login, reconnected from its saved
// session at startup, and stopped when the account is removed or its owner is
// disabled.
//
// The account id is the only identity. It never falls back to a server key, a
// username or a phone number.

// ErrWorkerBudgetExhausted means the account could not start right now because
// the worker pool is full.
//
// It is a report, not a refusal: the account is registered and starts as soon as
// a worker slot frees up. A caller may surface it ("this account is not
// connected yet") without losing the account.
var ErrWorkerBudgetExhausted = errors.New("supervisor: the worker budget is exhausted")

// ErrAccountNotSupervised means the account has no worker here. No other
// account's worker is ever substituted for it.
var ErrAccountNotSupervised = errors.New("supervisor: that account is not supervised here")

// accountSlot is one supervised account's stop handle and completion signal.
type accountSlot struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// RunAccounts supervises one worker per account and blocks until ctx ends.
//
// Saved sessions are reconnected here at startup. An account that exceeds the
// worker budget is deferred until a slot frees up; it is never dropped. A worker
// that exhausts its restart budget does not stop the supervisor: the other
// accounts keep running and the failed one stays visible in States.
//
// Ending ctx is a graceful shutdown: every worker is asked to stop and Run
// returns once they have.
func (s *Supervisor) RunAccounts(ctx context.Context, accounts []AccountRef) error {
	s.begin()
	if ctx.Err() != nil {
		_ = s.StopAll()
		return nil
	}
	for _, ref := range accounts {
		if err := s.Add(ref); err != nil {
			s.opts.Logf("supervisor: %s: %v", ref.ScopeKey(), err)
		}
	}

	<-ctx.Done()
	s.stopping()
	s.StopAll()
	s.wg.Wait()
	return nil
}

// Add starts supervising one account. It is how a worker starts after a
// successful login and how a saved session is reconnected at startup.
//
// The account is registered whether or not a worker slot is free right now: an
// account over the budget is queued and starts when a slot frees up. The error
// is ErrWorkerBudgetExhausted in that case, so the caller is told the account is
// not connected yet instead of being left to guess.
func (s *Supervisor) Add(ref AccountRef) error {
	s.begin()
	if err := ref.Valid(); err != nil {
		return err
	}
	if _, ok := s.prober.(AccountReadyProber); !ok {
		return ErrAccountReadinessUnsupported
	}
	paths, err := AccountPaths(s.opts.RunDir, s.opts.SessionDir, s.opts.PeerDir, ref.ID)
	if err != nil {
		return err
	}

	t := workerTarget{
		AccountID:   ref.ID,
		OwnerUserID: ref.OwnerUserID,
		SocketPath:  paths.SocketPath,
		Args:        s.argsFor("", ref.ID),
		Metadata:    ref.Metadata(),
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSupervisorStopped
	}
	if _, exists := s.state[ref.ID]; exists {
		previous, hasPrevious := s.accountRefs[ref.ID]
		s.mu.Unlock()
		if !hasPrevious || !sameAccountRef(previous, ref) {
			return fmt.Errorf("%w: %s", ErrAccountMetadataConflict, ref.ID)
		}
		return nil
	}
	s.state[ref.ID] = &State{
		AccountID: ref.ID, OwnerUserID: ref.OwnerUserID, Phase: PhaseStarting,
	}
	s.accountRefs[ref.ID] = cloneAccountRef(ref)
	tctx, cancel := context.WithCancel(s.ctx)
	slot := &accountSlot{cancel: cancel, done: make(chan struct{})}
	s.slotsByID[ref.ID] = slot
	held := s.takeSlot()
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer close(slot.done)
		s.supervise(tctx, t, true, held)
	}()

	if !held {
		return fmt.Errorf("%w: account %s is registered and will start when a worker slot frees up",
			ErrWorkerBudgetExhausted, ref.ID)
	}
	return nil
}

// Remove stops one account's worker and drops it from supervision. It is what
// account removal and logout do.
//
// The worker is asked to stop and Remove returns once it has, so the account's
// session is flushed and its socket is closed before the caller moves on.
func (s *Supervisor) Remove(accountID string) error {
	s.mu.Lock()
	slot := s.slotsByID[accountID]
	_, known := s.state[accountID]
	s.mu.Unlock()
	if !known {
		return fmt.Errorf("%w: %s", ErrAccountNotSupervised, accountID)
	}

	if slot != nil {
		slot.cancel()
		<-slot.done
	}
	s.unregister(accountID)
	s.opts.Logf("supervisor: %s: worker stopped", accountID)
	return nil
}

// StopAll stops every supervised account's worker. It is what a disabled owner
// and a graceful shutdown do.
func (s *Supervisor) StopAll() error {
	s.mu.Lock()
	s.closed = true
	slots := make([]*accountSlot, 0, len(s.slotsByID))
	for _, slot := range s.slotsByID {
		slots = append(slots, slot)
	}
	ids := make([]string, 0, len(s.state))
	for id := range s.state {
		ids = append(ids, id)
	}
	stop := s.stopAll
	s.mu.Unlock()

	if stop != nil {
		stop()
	}
	for _, slot := range slots {
		slot.cancel()
	}
	for _, slot := range slots {
		<-slot.done
	}
	for _, id := range ids {
		s.unregister(id)
	}
	return nil
}

// AccountState reports one account's worker state.
func (s *Supervisor) AccountState(accountID string) (State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[accountID]
	if !ok {
		return State{}, false
	}
	return *st, true
}

// WorkerBudget is how many account workers may run at once.
func (s *Supervisor) WorkerBudget() int { return s.opts.MaxWorkers }

// AccountRestartBase is the delay an account worker's restart sequence actually
// starts from, as TELESYNC_ACCOUNT_RECONNECT_BACKOFF sets it.
func (s *Supervisor) AccountRestartBase() time.Duration { return s.restartBase(true) }

// begin creates the supervisor's own lifetime context on first use, so a worker
// added after a login runs under the same lifetime as the ones started at
// startup and is stopped by the same shutdown.
func (s *Supervisor) begin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil {
		s.ctx, s.stopAll = context.WithCancel(context.Background())
	}
}

func cloneAccountRef(ref AccountRef) AccountRef {
	if ref.Server != nil {
		server := *ref.Server
		ref.Server = &server
	}
	return ref
}

func sameAccountRef(a, b AccountRef) bool {
	if a.ID != b.ID || a.OwnerUserID != b.OwnerUserID || a.ServerKey != b.ServerKey ||
		a.NetworkType != b.NetworkType || a.AppID != b.AppID || a.APIHash != b.APIHash {
		return false
	}
	if a.Server == nil || b.Server == nil {
		return a.Server == nil && b.Server == nil
	}
	return *a.Server == *b.Server
}

// unregister drops a target's bookkeeping.
func (s *Supervisor) unregister(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, key)
	delete(s.slotsByID, key)
	delete(s.accountRefs, key)
}
