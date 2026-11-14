package gotdclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth/qrlogin"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/profile"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/sessionstore"
)

// AccountAuthFactoryConfig resolves the already-configured server definition
// for one persisted login attempt and binds its temporary client to that
// attempt's permanent account identity.
type AccountAuthFactoryConfig struct {
	// ResolveSpec must return the exact server definition named by the attempt.
	// It must not fall back to another client or endpoint.
	ResolveSpec func(accountauth.LoginAttempt) (*profile.Spec, error)
	// SessionDir contains account-id-derived sealed AccountBox files.
	SessionDir string
	// Encrypter is the configured session encryption key.
	Encrypter *secrets.Encrypter
	// KeyVersion participates in AccountBox AAD and must match the account row.
	KeyVersion int
}

// NewAccountAuthFactory returns the concrete MTProto factory for
// accountauth.Config.MTProto. It creates one lazy gotd client for each attempt;
// accountauth.Service retains that adapter across HTTP steps and closes it
// before the permanent account Runner is started.
func NewAccountAuthFactory(cfg AccountAuthFactoryConfig) (func(accountauth.LoginAttempt) (accountauth.MTProtoAuth, error), error) {
	if cfg.ResolveSpec == nil {
		return nil, errors.New("gotdclient: an account server resolver is required")
	}
	if strings.TrimSpace(cfg.SessionDir) == "" {
		return nil, errors.New("gotdclient: an account session directory is required")
	}
	if cfg.Encrypter == nil {
		return nil, errors.New("gotdclient: session encryption is required")
	}
	if cfg.KeyVersion < 1 {
		return nil, errors.New("gotdclient: a session key version is required")
	}
	return func(attempt accountauth.LoginAttempt) (accountauth.MTProtoAuth, error) {
		spec, err := cfg.ResolveSpec(attempt)
		if err != nil {
			return nil, fmt.Errorf("gotdclient: resolve the login server definition: %w", err)
		}
		if err := validateAccountAuthSpec(attempt, spec); err != nil {
			return nil, err
		}
		return NewAccountAuth(AccountAuthConfig{
			Attempt:    attempt,
			Spec:       spec,
			SessionDir: cfg.SessionDir,
			Encrypter:  cfg.Encrypter,
			KeyVersion: cfg.KeyVersion,
		})
	}, nil
}

// AccountAuthConfig constructs one attempt's temporary account-bound auth
// client. The profile carries the exact official/custom route; only its session
// storage is replaced with an encrypted AccountBox.
type AccountAuthConfig struct {
	Attempt    accountauth.LoginAttempt
	Spec       *profile.Spec
	SessionDir string
	Encrypter  *secrets.Encrypter
	KeyVersion int
}

// AccountAuth is one temporary gotd client for one login attempt.
//
// It runs until Close, including between HTTP requests. Reopening its
// AccountBox in another gotd client before Close would compete for the same auth
// key and is deliberately not supported.
type AccountAuth struct {
	client *telegram.Client
	auth   *accountauth.GotdMTProto
	box    *sessionstore.AccountBox

	opMu     sync.Mutex
	mu       sync.Mutex
	closeMu  sync.Mutex
	started  bool
	closed   bool
	canceled bool
	cancel   context.CancelFunc
	ready    chan struct{}
	done     chan struct{}
}

// NewAccountAuth constructs, but does not dial, one gotd auth session.
//
// The first auth operation starts Client.Run and waits for gotd's readiness
// callback. The same client then handles every HTTP step until Close.
func NewAccountAuth(cfg AccountAuthConfig) (*AccountAuth, error) {
	if err := validateAccountAuthSpec(cfg.Attempt, cfg.Spec); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.SessionDir) == "" {
		return nil, errors.New("gotdclient: an account session directory is required")
	}
	if cfg.Encrypter == nil {
		return nil, errors.New("gotdclient: session encryption is required")
	}
	if cfg.KeyVersion < 1 {
		return nil, errors.New("gotdclient: a session key version is required")
	}
	box, err := sessionstore.NewAccountBox(sessionstore.AccountBoxConfig{
		Dir:         cfg.SessionDir,
		AccountID:   cfg.Attempt.AccountID,
		Encrypter:   cfg.Encrypter,
		KeyVersion:  cfg.KeyVersion,
		NetworkKey:  cfg.Attempt.ServerKey,
		NetworkType: cfg.Attempt.NetworkType,
	})
	if err != nil {
		return nil, fmt.Errorf("gotdclient: create the account-bound session store: %w", err)
	}
	codeHash, err := box.LoadPhoneCodeHash()
	if err != nil && !errors.Is(err, sessionstore.ErrPhoneCodeHashNotFound) {
		return nil, fmt.Errorf("gotdclient: load private login state: %w", err)
	}
	options := cfg.Spec.Options(nil, io.Discard)
	options.SessionStorage = box
	client := telegram.NewClient(cfg.Spec.AppID, cfg.Spec.AppHash, options)
	qr := qrlogin.NewQR(client.API(), cfg.Spec.AppID, cfg.Spec.AppHash, qrlogin.Options{})
	flow, err := accountauth.NewGotdMTProto(accountauth.GotdMTProtoConfig{
		Auth:          client.Auth(),
		QR:            qr,
		Key:           cfg.Attempt.ServerKey,
		PhoneCodeHash: codeHash,
	})
	if err != nil {
		return nil, err
	}
	return &AccountAuth{
		client: client,
		auth:   flow,
		box:    box,
	}, nil
}

func validateAccountAuthSpec(attempt accountauth.LoginAttempt, spec *profile.Spec) error {
	if attempt.OwnerUserID <= 0 || strings.TrimSpace(attempt.AccountID) == "" {
		return errors.New("gotdclient: the login attempt has no owner or account identity")
	}
	if spec == nil {
		return errors.New("gotdclient: the login attempt has no server profile")
	}
	if attempt.ServerKey == "" || spec.Key != attempt.ServerKey {
		return errors.New("gotdclient: the resolved profile does not match the login server key")
	}
	wantNetwork := ""
	switch spec.Kind {
	case profile.Official:
		wantNetwork = sessionstore.NetworkTelegram
	case profile.Custom:
		wantNetwork = sessionstore.NetworkCustom
	default:
		return errors.New("gotdclient: the resolved profile has an unknown network kind")
	}
	if attempt.NetworkType != wantNetwork {
		return errors.New("gotdclient: the resolved profile does not match the login network kind")
	}
	if spec.AppID <= 0 || strings.TrimSpace(spec.AppHash) == "" {
		return errors.New("gotdclient: the server profile has no application credentials")
	}
	return nil
}

func (a *AccountAuth) run(ctx context.Context) {
	_ = a.client.Run(ctx, func(runCtx context.Context) error {
		close(a.ready)
		<-runCtx.Done()
		return runCtx.Err()
	})
	close(a.done)
}

func (a *AccountAuth) ensureReady(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return accountauth.ErrUnavailable
	}
	if !a.started {
		runCtx, cancel := context.WithCancel(context.Background())
		a.started = true
		a.cancel = cancel
		a.ready = make(chan struct{})
		a.done = make(chan struct{})
		go a.run(runCtx)
	}
	ready, done := a.ready, a.done
	a.mu.Unlock()

	select {
	case <-ready:
		return nil
	case <-done:
		return accountauth.ErrUnavailable
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StartCode begins phone authorization. The phone reaches gotd for this one
// attempt and is kept only in its live in-memory adapter.
func (a *AccountAuth) StartCode(ctx context.Context, req accountauth.StartRequest) (accountauth.CodeChallenge, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureReady(ctx); err != nil {
		return accountauth.CodeChallenge{}, err
	}
	challenge, err := a.auth.StartCode(ctx, req)
	if err != nil {
		return accountauth.CodeChallenge{}, err
	}
	if challenge.PhoneCodeHash != "" {
		if err := a.box.StorePhoneCodeHash(challenge.PhoneCodeHash); err != nil {
			return accountauth.CodeChallenge{}, accountauth.ErrUnavailable
		}
	}
	return challenge, nil
}

// SubmitCode submits a transient code to the attempt's existing gotd client.
func (a *AccountAuth) SubmitCode(ctx context.Context, code string) (accountauth.CodeResult, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureReady(ctx); err != nil {
		return accountauth.CodeResult{}, err
	}
	result, err := a.auth.SubmitCode(ctx, code)
	if errors.Is(err, accountauth.ErrCodeExpired) {
		_ = a.box.ClearPhoneCodeHash()
	}
	return result, err
}

// SubmitPassword submits a transient 2FA password through gotd's SRP helper.
func (a *AccountAuth) SubmitPassword(ctx context.Context, password string) (accountauth.CodeResult, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureReady(ctx); err != nil {
		return accountauth.CodeResult{}, err
	}
	return a.auth.SubmitPassword(ctx, password)
}

// StartQR starts the gotd QR login flow on this same account-bound client.
func (a *AccountAuth) StartQR(ctx context.Context, req accountauth.StartRequest) (accountauth.QRChallenge, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureReady(ctx); err != nil {
		return accountauth.QRChallenge{}, err
	}
	return a.auth.StartQR(ctx, req)
}

// PollQR checks confirmation on the existing gotd client.
func (a *AccountAuth) PollQR(ctx context.Context, token string) (accountauth.CodeResult, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureReady(ctx); err != nil {
		return accountauth.CodeResult{}, err
	}
	return a.auth.PollQR(ctx, token)
}

// RefreshQR exports a replacement QR token on the existing gotd client.
func (a *AccountAuth) RefreshQR(ctx context.Context) (accountauth.QRChallenge, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureReady(ctx); err != nil {
		return accountauth.QRChallenge{}, err
	}
	return a.auth.RefreshQR(ctx)
}

// Resend requests another code on the same gotd client and persists only its
// encrypted, account-bound server handle.
func (a *AccountAuth) Resend(ctx context.Context, phoneCodeHash string) (accountauth.CodeChallenge, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureReady(ctx); err != nil {
		return accountauth.CodeChallenge{}, err
	}
	challenge, err := a.auth.Resend(ctx, phoneCodeHash)
	if err != nil {
		return accountauth.CodeChallenge{}, err
	}
	if challenge.PhoneCodeHash != "" {
		if err := a.box.StorePhoneCodeHash(challenge.PhoneCodeHash); err != nil {
			return accountauth.CodeChallenge{}, accountauth.ErrUnavailable
		}
	}
	return challenge, nil
}

// Cancel marks the attempt for destructive cleanup and stops its temporary
// client. Close waits for gotd's final AccountBox write before discarding it.
func (a *AccountAuth) Cancel(ctx context.Context) {
	a.mu.Lock()
	a.canceled = true
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.opMu.Lock()
	a.auth.Cancel(ctx)
	a.opMu.Unlock()
}

// Close is the authorization handoff boundary. For a successful login it
// flushes and preserves the AccountBox while clearing private code state. After
// Cancel it removes the unfinished session and code-state files instead.
func (a *AccountAuth) Close() error {
	a.closeMu.Lock()
	defer a.closeMu.Unlock()

	a.mu.Lock()
	a.closed = true
	cancel, started, done := a.cancel, a.started, a.done
	canceled := a.canceled
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if started {
		<-done
	}
	a.opMu.Lock()
	closeErr := a.auth.Close()
	a.opMu.Unlock()

	var stateErr error
	if canceled {
		stateErr = a.box.Discard()
	} else {
		stateErr = a.box.ClearPhoneCodeHash()
	}
	return errors.Join(closeErr, stateErr)
}

var _ accountauth.MTProtoAuth = (*AccountAuth)(nil)
