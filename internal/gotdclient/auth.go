package gotdclient

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/login"
	"github.com/mirza/telesync/internal/profile"
)

// Prompter collects interactive login input.
type Prompter interface {
	Phone(ctx context.Context, key string) (string, error)
	Code(ctx context.Context, sent *tg.AuthSentCode) (string, error)
	Password(ctx context.Context) (string, error)
}

// LoginRunner runs the interactive login for one client.
type LoginRunner interface {
	Login(ctx context.Context) (login.Result, error)
}

// NewLoginForSpec builds a login runner for one resolved spec.
//
// The spec already carries the instance's own credentials, session path and (for
// custom servers) the endpoint, so an instance never borrows another's
// configuration.
func NewLoginForSpec(spec *profile.Spec, p Prompter, logw io.Writer) (LoginRunner, error) {
	if p == nil {
		return nil, errors.New("interactive input is not available")
	}
	client := spec.NewClient(nil, logw)
	return &loginRunner{
		spec:   spec,
		client: client,
		phone:  spec.Phone,
		flow:   &gotdFlow{client: client, prompter: p, key: spec.Key, out: logw},
	}, nil
}

type loginRunner struct {
	spec   *profile.Spec
	client *telegram.Client
	phone  string
	flow   *gotdFlow
}

func (l *loginRunner) Login(ctx context.Context) (login.Result, error) {
	return login.Run(ctx, l.client, l.flow, l.spec.Key, l.phone)
}

// gotdFlow implements login.Flow on top of gotd's authentication helpers.
type gotdFlow struct {
	client   *telegram.Client
	prompter Prompter
	key      string
	out      io.Writer
}

// Status reports the current user, or nil when unauthorized.
func (g *gotdFlow) Status(ctx context.Context) (*tg.User, error) {
	status, err := g.client.Auth().Status(ctx)
	if err != nil {
		return nil, err
	}
	if !status.Authorized {
		return nil, nil
	}
	return status.User, nil
}

// Phone prompts for a phone number.
func (g *gotdFlow) Phone(ctx context.Context) (string, error) {
	return g.prompter.Phone(ctx, g.key)
}

// Authenticate runs the code (and 2FA password) flow.
func (g *gotdFlow) Authenticate(ctx context.Context, phone string) error {
	flow := auth.NewFlow(&authenticator{prompter: g.prompter, phone: phone}, auth.SendCodeOptions{})
	if err := flow.Run(ctx, g.client.Auth()); err != nil {
		return describeAuthError(err)
	}
	return nil
}

// authenticator supplies the user's answers to gotd's auth flow.
type authenticator struct {
	prompter Prompter
	phone    string
}

func (a *authenticator) Phone(context.Context) (string, error) { return a.phone, nil }

func (a *authenticator) Code(ctx context.Context, sent *tg.AuthSentCode) (string, error) {
	return a.prompter.Code(ctx, sent)
}

func (a *authenticator) Password(ctx context.Context) (string, error) {
	return a.prompter.Password(ctx)
}

var errNoSignUp = errors.New("telesync never creates new Telegram accounts")

func (a *authenticator) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errNoSignUp
}

func (a *authenticator) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errNoSignUp
}

func describeAuthError(err error) error {
	switch {
	case errors.Is(err, auth.ErrPasswordNotProvided):
		return fmt.Errorf("two-step verification is enabled but the password prompt was not answered: %w", err)
	case errors.Is(err, errNoSignUp):
		return fmt.Errorf("this phone number has no Telegram account; telesync does not sign up new accounts: %w", err)
	case tgerr.Is(err, "PHONE_CODE_INVALID"):
		return fmt.Errorf("the login code was rejected; run auth again for a fresh code: %w", err)
	case tgerr.Is(err, "PHONE_CODE_EXPIRED"):
		return fmt.Errorf("the login code expired; run auth again to request a new one: %w", err)
	case tgerr.Is(err, "PASSWORD_HASH_INVALID"):
		return fmt.Errorf("the two-step verification password was rejected: %w", err)
	case tgerr.Is(err, "PHONE_NUMBER_INVALID"), tgerr.Is(err, "PHONE_NUMBER_BANNED"):
		return fmt.Errorf("the phone number is not accepted by the server: %w", err)
	default:
		return fmt.Errorf("authentication failed: %w", err)
	}
}

// Compile-time guarantee.
var _ login.Flow = (*gotdFlow)(nil)
