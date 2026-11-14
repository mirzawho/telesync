package rpc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// This file is the worker side of the account routing seam.
//
// The gateway resolves an account and then calls `Worker(account_id)`. The key
// is the opaque account id and nothing else: never a server key, never a
// username, never a phone number. There is no fallback — an account that has no
// worker is unavailable, and no other account's worker is ever substituted.

// Worker is the upstream operation set for one account's worker.
//
// It is the same method set the gateway's Registry resolves, named here so the
// account routing seam can be defined and tested without the worker packages
// depending on the gateway.
type Worker interface {
	Health(ctx context.Context) (Health, error)
	Profile(ctx context.Context) (Profile, error)
	Channels(ctx context.Context, page, perPage int) (ChannelPage, error)
	Messages(ctx context.Context, channelID int64, page, perPage int) (MessagePage, error)
	Message(ctx context.Context, channelID int64, messageID int) (MessagePage, error)
	Media(ctx context.Context, channelID int64, messageID, photoIndex int, slice MediaSlice) (MediaStream, error)
	CustomEmoji(ctx context.Context, documentID int64) (MediaFile, error)
	Find(ctx context.Context, req FindRequest) (FindResult, error)
	Channel(ctx context.Context, ref ChannelRef) (ChannelResult, error)
	Join(ctx context.Context, ref ChannelRef) (JoinResult, error)
	Avatar(ctx context.Context, ref ChannelRef) (AvatarFile, error)
	SendMessage(ctx context.Context, req SendRequest) (SendResult, error)
	EditMessage(ctx context.Context, req EditRequest) (EditResult, error)
	DeleteMessage(ctx context.Context, req DeleteMessageRequest) (DeleteMessageResult, error)
}

// Registry resolves an account id to the worker that serves it.
//
// The key is the account id. It is the single routing seam: every Telegram call
// resolves its account first and then reaches exactly that account's worker.
type Registry interface {
	Worker(accountID string) (Worker, bool)
}

// RegistryFunc adapts a function to Registry.
type RegistryFunc func(accountID string) (Worker, bool)

// Worker implements Registry.
func (f RegistryFunc) Worker(accountID string) (Worker, bool) { return f(accountID) }

// accountIDPattern is the shape of an opaque account id.
//
// It is a check against garbage, not a security boundary: the id is
// server-generated and no caller chooses it. What it does enforce is that a
// server key ("official", "cat"), a username or a phone number can never be
// mistaken for one and silently routed to somebody's worker.
const accountIDCrockford = "0123456789abcdefghjkmnpqrstvwxyz"

// ErrNotAnAccountID means the key offered as an account id is not one.
var ErrNotAnAccountID = errors.New("rpc: that is not an account id")

// ErrAccountNotRoutable means the account has no worker. No other account's
// worker is ever substituted for it.
var ErrAccountNotRoutable = errors.New("rpc: this account has no worker")

// ErrAccountAlreadyRoutable means the account already has a worker. A second
// one is refused so a stale process cannot silently replace the live one.
var ErrAccountAlreadyRoutable = errors.New("rpc: this account already has a worker")

// ValidAccountID reports whether id has the shape of an opaque account id.
func ValidAccountID(id string) error {
	valid := len(id) == len("acc_")+26 && len(id) > len("acc_") && id[:len("acc_")] == "acc_"
	if valid {
		for i := len("acc_"); i < len(id); i++ {
			if !strings.ContainsRune(accountIDCrockford, rune(id[i])) {
				valid = false
				break
			}
		}
	}
	if !valid {
		return ErrNotAnAccountID
	}
	return nil
}

// AccountRegistry is a mutable, account-keyed worker registry.
//
// One account has at most one worker. Registering a second worker for an
// account is refused, so a stale process cannot silently replace the live one.
// Two accounts on the same server definition always get two entries: the server
// key is not an identity.
type AccountRegistry struct {
	mu      sync.RWMutex
	workers map[string]Worker
}

// NewAccountRegistry builds an empty registry.
func NewAccountRegistry() *AccountRegistry {
	return &AccountRegistry{workers: map[string]Worker{}}
}

// Register binds an account to its worker.
func (r *AccountRegistry) Register(accountID string, w Worker) error {
	if err := ValidAccountID(accountID); err != nil {
		return err
	}
	if w == nil {
		return fmt.Errorf("rpc: account %s was registered without a worker", accountID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.workers[accountID]; dup {
		return fmt.Errorf("%w: %s", ErrAccountAlreadyRoutable, accountID)
	}
	r.workers[accountID] = w
	return nil
}

// Unregister drops an account's binding and reports whether one was there.
func (r *AccountRegistry) Unregister(accountID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.workers[accountID]; !ok {
		return false
	}
	delete(r.workers, accountID)
	return true
}

// Worker implements Registry. It never falls back: an account with no worker is
// simply not routable.
func (r *AccountRegistry) Worker(accountID string) (Worker, bool) {
	if ValidAccountID(accountID) != nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workers[accountID]
	return w, ok
}

// Len is how many accounts are routable.
func (r *AccountRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.workers)
}

// Accounts lists the routable account ids, in a stable order.
func (r *AccountRegistry) Accounts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.workers))
	for id := range r.workers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
