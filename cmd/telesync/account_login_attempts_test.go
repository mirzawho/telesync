package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/channelstore"
)

func TestAccountLoginAttemptStoreAdapterRoundTripsOwnerScopedState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "channels.db"), channelstore.Options{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	owner, err := store.CreateUser(ctx, "Login owner", "login-owner", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	accountID, err := accountid.New()
	if err != nil {
		t.Fatalf("create account id: %v", err)
	}
	if err := store.CreatePending(ctx, owner.ID, accountID, "official", channelstore.NetworkTelegram); err != nil {
		t.Fatalf("create pending account: %v", err)
	}

	want := accountauth.LoginAttempt{
		ID:          "lga_0123456789abcdefghjkmnpqrs",
		OwnerUserID: owner.ID, AccountID: accountID,
		ServerKey: "official", NetworkType: channelstore.NetworkTelegram,
		Method: accountauth.MethodPhone, State: accountauth.StateWaitingForCode,
		CodeSubmissions: 2, PhoneMasked: "+1***1111", Delivery: accountauth.DeliverySMS,
		DeliveryHint: "ending in 11", CodeLength: 5, NextStep: "code",
		QRToken: "short-lived-login-token", QRExpiresAt: now.Add(time.Minute),
		ExpiresAt: now.Add(5 * time.Minute), ResendAfter: now.Add(30 * time.Second),
		CreatedAt: now, UpdatedAt: now,
	}
	attempts := loginAttemptStoreAdapter{store: store.LoginAttempts()}

	created, err := attempts.Create(ctx, want)
	if err != nil {
		t.Fatalf("create login attempt: %v", err)
	}
	assertLoginAttemptEqual(t, "created", created, want)

	otherOwner, err := store.CreateUser(ctx, "Other owner", "other-owner", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("create other owner: %v", err)
	}
	if _, err := attempts.ByID(ctx, otherOwner.ID, want.ID); !errors.Is(err, accountauth.ErrNotFound) {
		t.Fatalf("foreign owner lookup = %v, want accountauth.ErrNotFound", err)
	}

	want.State = accountauth.StateWaitingForPassword
	want.CodeSubmissions++
	want.NextStep = "password"
	want.QRToken = ""
	want.QRExpiresAt = time.Time{}
	if err := attempts.UpdateState(ctx, owner.ID, want); err != nil {
		t.Fatalf("update login attempt: %v", err)
	}
	got, err := attempts.ByID(ctx, owner.ID, want.ID)
	if err != nil {
		t.Fatalf("read updated attempt: %v", err)
	}
	assertLoginAttemptEqual(t, "updated", got, want)

	incomplete, err := attempts.Incomplete(ctx, want.ExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("list incomplete attempts: %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("incomplete attempts = %d, want 1", len(incomplete))
	}
	assertLoginAttemptEqual(t, "incomplete", incomplete[0], want)

	deleted, err := attempts.DeleteExpired(ctx, want.ExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("delete expired attempts: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted attempts = %d, want 1", deleted)
	}
	if _, err := attempts.ByID(ctx, owner.ID, want.ID); !errors.Is(err, accountauth.ErrNotFound) {
		t.Fatalf("lookup after expiry = %v, want accountauth.ErrNotFound", err)
	}
}

func assertLoginAttemptEqual(t *testing.T, label string, got, want accountauth.LoginAttempt) {
	t.Helper()
	if got.ID != want.ID ||
		got.OwnerUserID != want.OwnerUserID ||
		got.AccountID != want.AccountID ||
		got.ServerKey != want.ServerKey ||
		got.NetworkType != want.NetworkType ||
		got.Method != want.Method ||
		got.State != want.State ||
		got.CodeSubmissions != want.CodeSubmissions ||
		got.PhoneMasked != want.PhoneMasked ||
		got.Delivery != want.Delivery ||
		got.DeliveryHint != want.DeliveryHint ||
		got.CodeLength != want.CodeLength ||
		got.NextStep != want.NextStep ||
		got.QRToken != want.QRToken ||
		!got.QRExpiresAt.Equal(want.QRExpiresAt) ||
		!got.ExpiresAt.Equal(want.ExpiresAt) ||
		!got.ResendAfter.Equal(want.ResendAfter) ||
		!got.CreatedAt.Equal(want.CreatedAt) ||
		!got.UpdatedAt.Equal(want.UpdatedAt) ||
		!got.AuthorizedAt.Equal(want.AuthorizedAt) {
		t.Errorf("%s login attempt did not preserve persisted fields", label)
	}
}
