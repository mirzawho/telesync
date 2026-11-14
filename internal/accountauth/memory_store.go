package accountauth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrAttemptExists means an attempt id was used twice. Ids are server-generated
// and never reused, so this is a bug rather than a user error.
var ErrAttemptExists = errors.New("accountauth: login attempt already exists")

// MemoryLoginAttemptStore is an in-memory LoginAttemptStore.
//
// It is the reference for the SQLite implementation: same owner scoping, same
// refusal to answer for another owner, same terminal-state rules. It is not a
// production store — a restart forgets it, which is why the real one lives in
// the database.
type MemoryLoginAttemptStore struct {
	mux      sync.Mutex
	attempts map[string]LoginAttempt
}

// NewMemoryLoginAttemptStore returns an empty in-memory store.
func NewMemoryLoginAttemptStore() *MemoryLoginAttemptStore {
	return &MemoryLoginAttemptStore{attempts: map[string]LoginAttempt{}}
}

// Create records a new attempt.
func (m *MemoryLoginAttemptStore) Create(_ context.Context, attempt LoginAttempt) (LoginAttempt, error) {
	m.mux.Lock()
	defer m.mux.Unlock()

	if attempt.ID == "" {
		return LoginAttempt{}, errors.New("accountauth: a login attempt needs an id")
	}
	if attempt.OwnerUserID <= 0 {
		return LoginAttempt{}, ErrNotAuthorized
	}
	if _, ok := m.attempts[attempt.ID]; ok {
		return LoginAttempt{}, ErrAttemptExists
	}
	m.attempts[attempt.ID] = attempt
	return attempt, nil
}

// ByID returns the attempt only if owner owns it.
//
// Another owner's attempt is "not found": the id never confirms that an attempt
// exists to someone who does not own it.
func (m *MemoryLoginAttemptStore) ByID(_ context.Context, ownerUserID int64, id string) (LoginAttempt, error) {
	m.mux.Lock()
	defer m.mux.Unlock()

	attempt, ok := m.attempts[id]
	if !ok || attempt.OwnerUserID != ownerUserID {
		return LoginAttempt{}, ErrNotFound
	}
	return attempt, nil
}

// UpdateState persists a state change.
func (m *MemoryLoginAttemptStore) UpdateState(_ context.Context, ownerUserID int64, attempt LoginAttempt) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	existing, ok := m.attempts[attempt.ID]
	if !ok || existing.OwnerUserID != ownerUserID {
		return ErrNotFound
	}
	m.attempts[attempt.ID] = attempt
	return nil
}

// TouchResend sets when a code may be requested again.
func (m *MemoryLoginAttemptStore) TouchResend(_ context.Context, ownerUserID int64, id string, resendAfter time.Time) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	attempt, ok := m.attempts[id]
	if !ok || attempt.OwnerUserID != ownerUserID {
		return ErrNotFound
	}
	attempt.ResendAfter = resendAfter
	m.attempts[id] = attempt
	return nil
}

// DeleteExpired removes attempts that expired before at.
func (m *MemoryLoginAttemptStore) DeleteExpired(_ context.Context, at time.Time) (int, error) {
	m.mux.Lock()
	defer m.mux.Unlock()

	removed := 0
	for id, attempt := range m.attempts {
		if !attempt.ExpiresAt.IsZero() && attempt.ExpiresAt.Before(at) {
			delete(m.attempts, id)
			removed++
		}
	}
	return removed, nil
}

// Incomplete lists attempts that stopped halfway and have expired.
func (m *MemoryLoginAttemptStore) Incomplete(ctx context.Context, at time.Time) ([]LoginAttempt, error) {
	m.mux.Lock()
	defer m.mux.Unlock()

	out := make([]LoginAttempt, 0)
	for _, attempt := range m.attempts {
		if attempt.Terminal() {
			continue
		}
		if !attempt.ExpiresAt.IsZero() && attempt.ExpiresAt.Before(at) {
			out = append(out, attempt)
		}
	}
	return out, nil
}

// DeleteByOwner removes every attempt of one owner.
func (m *MemoryLoginAttemptStore) DeleteByOwner(_ context.Context, ownerUserID int64) (int, error) {
	m.mux.Lock()
	defer m.mux.Unlock()

	removed := 0
	for id, attempt := range m.attempts {
		if attempt.OwnerUserID == ownerUserID {
			delete(m.attempts, id)
			removed++
		}
	}
	return removed, nil
}

var (
	_ LoginAttemptStore           = (*MemoryLoginAttemptStore)(nil)
	_ IncompleteLoginAttemptStore = (*MemoryLoginAttemptStore)(nil)
)
