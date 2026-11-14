package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/channelstore"
)

// loginAttemptStoreAdapter translates the persistence record into the
// accountauth package's contract without making either package depend on the
// other. The SQL store remains the owner-scoped source of truth.
type loginAttemptStoreAdapter struct {
	store *channelstore.LoginAttempts
}

var (
	_ accountauth.LoginAttemptStore           = loginAttemptStoreAdapter{}
	_ accountauth.IncompleteLoginAttemptStore = loginAttemptStoreAdapter{}
)

func (a loginAttemptStoreAdapter) Create(
	ctx context.Context,
	attempt accountauth.LoginAttempt,
) (accountauth.LoginAttempt, error) {
	created, err := a.store.Create(ctx, loginAttemptToStore(attempt))
	if err != nil {
		return accountauth.LoginAttempt{}, loginAttemptStoreError(err)
	}
	return loginAttemptFromStore(created), nil
}

func (a loginAttemptStoreAdapter) ByID(
	ctx context.Context,
	ownerUserID int64,
	id string,
) (accountauth.LoginAttempt, error) {
	attempt, err := a.store.ByID(ctx, ownerUserID, id)
	if err != nil {
		return accountauth.LoginAttempt{}, loginAttemptStoreError(err)
	}
	return loginAttemptFromStore(attempt), nil
}

func (a loginAttemptStoreAdapter) UpdateState(
	ctx context.Context,
	ownerUserID int64,
	attempt accountauth.LoginAttempt,
) error {
	return loginAttemptStoreError(a.store.UpdateState(ctx, ownerUserID, loginAttemptToStore(attempt)))
}

func (a loginAttemptStoreAdapter) TouchResend(
	ctx context.Context,
	ownerUserID int64,
	id string,
	resendAfter time.Time,
) error {
	return loginAttemptStoreError(a.store.TouchResend(ctx, ownerUserID, id, resendAfter))
}

func (a loginAttemptStoreAdapter) DeleteExpired(ctx context.Context, at time.Time) (int, error) {
	count, err := a.store.DeleteExpired(ctx, at)
	return count, loginAttemptStoreError(err)
}

func (a loginAttemptStoreAdapter) DeleteByOwner(ctx context.Context, ownerUserID int64) (int, error) {
	count, err := a.store.DeleteByOwner(ctx, ownerUserID)
	return count, loginAttemptStoreError(err)
}

func (a loginAttemptStoreAdapter) Incomplete(
	ctx context.Context,
	at time.Time,
) ([]accountauth.LoginAttempt, error) {
	attempts, err := a.store.Incomplete(ctx, at)
	if err != nil {
		return nil, loginAttemptStoreError(err)
	}
	out := make([]accountauth.LoginAttempt, len(attempts))
	for i, attempt := range attempts {
		out[i] = loginAttemptFromStore(attempt)
	}
	return out, nil
}

func loginAttemptToStore(attempt accountauth.LoginAttempt) channelstore.LoginAttempt {
	return channelstore.LoginAttempt{
		ID: attempt.ID, OwnerUserID: attempt.OwnerUserID, AccountID: attempt.AccountID,
		ServerKey: attempt.ServerKey, NetworkType: attempt.NetworkType,
		Method: attempt.Method, State: attempt.State,
		CodeSubmissions: attempt.CodeSubmissions, PhoneMasked: attempt.PhoneMasked,
		Delivery: attempt.Delivery, DeliveryHint: attempt.DeliveryHint,
		CodeLength: attempt.CodeLength, NextStep: attempt.NextStep,
		ErrorCode: attempt.ErrorCode, QRToken: attempt.QRToken,
		QRExpiresAt: attempt.QRExpiresAt, ExpiresAt: attempt.ExpiresAt,
		ResendAfter: attempt.ResendAfter, CreatedAt: attempt.CreatedAt,
		UpdatedAt: attempt.UpdatedAt, AuthorizedAt: attempt.AuthorizedAt,
	}
}

func loginAttemptFromStore(attempt channelstore.LoginAttempt) accountauth.LoginAttempt {
	return accountauth.LoginAttempt{
		ID: attempt.ID, OwnerUserID: attempt.OwnerUserID, AccountID: attempt.AccountID,
		ServerKey: attempt.ServerKey, NetworkType: attempt.NetworkType,
		Method: attempt.Method, State: attempt.State,
		CodeSubmissions: attempt.CodeSubmissions, PhoneMasked: attempt.PhoneMasked,
		Delivery: attempt.Delivery, DeliveryHint: attempt.DeliveryHint,
		CodeLength: attempt.CodeLength, NextStep: attempt.NextStep,
		ErrorCode: attempt.ErrorCode, QRToken: attempt.QRToken,
		QRExpiresAt: attempt.QRExpiresAt, ExpiresAt: attempt.ExpiresAt,
		ResendAfter: attempt.ResendAfter, CreatedAt: attempt.CreatedAt,
		UpdatedAt: attempt.UpdatedAt, AuthorizedAt: attempt.AuthorizedAt,
	}
}

func loginAttemptStoreError(err error) error {
	switch {
	case errors.Is(err, channelstore.ErrLoginAttemptNotFound):
		return fmt.Errorf("%w: %v", accountauth.ErrNotFound, err)
	case errors.Is(err, channelstore.ErrLoginAttemptExists):
		return fmt.Errorf("%w: %v", accountauth.ErrAttemptExists, err)
	default:
		return err
	}
}
