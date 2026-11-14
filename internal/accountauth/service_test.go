package accountauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
)

// The login state machine is persisted, bounded and owner-scoped. It is not a
// goroutine: every step is a record transition that another user cannot touch.

const (
	owner     int64 = 7
	otherUser int64 = 8
)

type fakeMTProto struct {
	startErr          error
	codeErr           error
	passwordErr       error
	statusUser        *accountauth.AuthorizedUser
	statusErr         error
	cancelled         int
	started           int
	closed            int
	codeCalls         int
	passwordCall      int
	resendCalls       int
	lastCode          string
	lastPassword      string
	startPhone        string
	sentMethod        string
	sentCodeHash      string
	sentResendAt      time.Time
	passwordRequired  bool
	qrSupportedServer bool
	qrChallenge       accountauth.QRChallenge
	qrResult          accountauth.CodeResult
	qrErr             error
	// firstCodeFails makes only the first code wrong, so a later submission can
	// succeed: a rejected code must not end the attempt.
	firstCodeFails bool
	codesSeen      int
}

func (f *fakeMTProto) StartCode(_ context.Context, req accountauth.StartRequest) (accountauth.CodeChallenge, error) {
	f.started++
	f.startPhone = req.Phone
	if f.startErr != nil {
		return accountauth.CodeChallenge{}, f.startErr
	}
	return accountauth.CodeChallenge{
		Delivery:      f.sentMethod,
		PhoneCodeHash: f.sentCodeHash,
		ResendAfter:   f.sentResendAt,
	}, nil
}

// submittedCodes are the codes a caller sent. Tests assert they never land in
// a record or a log.
var submittedCodes []string

func (f *fakeMTProto) SubmitCode(_ context.Context, code string) (accountauth.CodeResult, error) {
	f.codeCalls++
	f.codesSeen++
	if f.firstCodeFails && f.codesSeen == 1 {
		f.lastCode = code
		submittedCodes = append(submittedCodes, code)
		return accountauth.CodeResult{}, accountauth.ErrWrongCode
	}
	f.lastCode = code
	submittedCodes = append(submittedCodes, code)
	if f.passwordRequired {
		return accountauth.CodeResult{PasswordRequired: true, User: f.statusUser}, nil
	}
	if f.codeErr != nil {
		return accountauth.CodeResult{}, f.codeErr
	}
	return accountauth.CodeResult{Authorized: f.statusUser != nil, User: f.statusUser}, nil
}

func (f *fakeMTProto) SubmitPassword(_ context.Context, password string) (accountauth.CodeResult, error) {
	f.passwordCall++
	f.lastPassword = password
	if f.passwordErr != nil {
		return accountauth.CodeResult{}, f.passwordErr
	}
	if f.statusUser == nil {
		return accountauth.CodeResult{Authorized: true}, nil
	}
	return accountauth.CodeResult{Authorized: true, User: f.statusUser}, nil
}

func (f *fakeMTProto) StartQR(_ context.Context, _ accountauth.StartRequest) (accountauth.QRChallenge, error) {
	if !f.qrSupportedServer {
		return accountauth.QRChallenge{}, accountauth.ErrUnsupportedMethod
	}
	return f.qrChallenge, nil
}

func (f *fakeMTProto) PollQR(_ context.Context, _ string) (accountauth.CodeResult, error) {
	if !f.qrSupportedServer {
		return accountauth.CodeResult{}, accountauth.ErrUnsupportedMethod
	}
	return f.qrResult, f.qrErr
}

func (f *fakeMTProto) RefreshQR(_ context.Context) (accountauth.QRChallenge, error) {
	if !f.qrSupportedServer {
		return accountauth.QRChallenge{}, accountauth.ErrUnsupportedMethod
	}
	if f.qrChallenge.Token == "" {
		return accountauth.QRChallenge{}, accountauth.ErrQRExpired
	}
	return f.qrChallenge, nil
}

func (f *fakeMTProto) Resend(_ context.Context, _ string) (accountauth.CodeChallenge, error) {
	f.resendCalls++
	return accountauth.CodeChallenge{}, nil
}

func (f *fakeMTProto) Cancel(context.Context) {
	f.cancelled++
}

func (f *fakeMTProto) Close() error {
	f.closed++
	return nil
}

func newService(t *testing.T, mtproto accountauth.MTProtoAuth) (*accountauth.Service, accountauth.LoginAttemptStore, *fakeClock) {
	t.Helper()
	return newServiceOver(t, mtproto, accountauth.NewMemoryLoginAttemptStore())
}

// newServiceOver builds a service over a store the caller keeps, so a restart
// can be modelled as a fresh service over the same records.
// fakeAccounts records what the login flow claims about an account. It is the
// seam Agent A implements over SQLite.
type fakeAccounts struct {
	ensured  []accountauth.AccountIdentity
	dropped  []accountauth.AccountIdentity
	pending  []accountauth.AccountIdentity
	nextID   string
	err      error
	sequence int
}

func (f *fakeAccounts) CreatePending(_ context.Context, owner int64, id, serverKey, network string) error {
	if f.err != nil {
		return f.err
	}
	f.pending = append(f.pending, accountauth.AccountIdentity{
		OwnerUserID: owner, AccountID: id, ServerKey: serverKey, NetworkType: network,
	})
	return nil
}

func (f *fakeAccounts) CompletePending(_ context.Context, owner int64, id string, user accountauth.AuthorizedUser) error {
	if f.err != nil {
		return f.err
	}
	f.ensured = append(f.ensured, accountauth.AccountIdentity{
		OwnerUserID: owner, AccountID: id, ServerKey: "cat", NetworkType: "custom", User: user,
	})
	return nil
}

func (f *fakeAccounts) ReleasePending(_ context.Context, owner int64, id string) error {
	if f.err != nil {
		return f.err
	}
	f.dropped = append(f.dropped, accountauth.AccountIdentity{OwnerUserID: owner, AccountID: id})
	return nil
}

func (f *fakeAccounts) Ensure(_ context.Context, identity accountauth.AccountIdentity) (string, error) {
	f.ensured = append(f.ensured, identity)
	if f.err != nil {
		return "", f.err
	}
	if f.nextID != "" {
		return f.nextID, nil
	}
	f.sequence++
	return accountauth.NewAccountID()
}

func (f *fakeAccounts) DropIncomplete(_ context.Context, identity accountauth.AccountIdentity) error {
	f.dropped = append(f.dropped, identity)
	return f.err
}

func newServiceOver(t *testing.T, mtproto accountauth.MTProtoAuth, store accountauth.LoginAttemptStore) (*accountauth.Service, accountauth.LoginAttemptStore, *fakeClock) {
	t.Helper()
	return newServiceOverWithAccounts(t, mtproto, store, &fakeAccounts{})
}

func newServiceOverWithAccounts(t *testing.T, mtproto accountauth.MTProtoAuth, store accountauth.LoginAttemptStore, accounts accountauth.AccountStore) (*accountauth.Service, accountauth.LoginAttemptStore, *fakeClock) {
	t.Helper()
	return newServiceOn(t, mtproto, store, accounts, &fakeClock{now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)})
}

// newServiceOn shares one clock between services, so a "restart" happens at the
// same moment the process died.
func newServiceOn(t *testing.T, mtproto accountauth.MTProtoAuth, store accountauth.LoginAttemptStore, accounts accountauth.AccountStore, clock *fakeClock) (*accountauth.Service, accountauth.LoginAttemptStore, *fakeClock) {
	t.Helper()
	svc, err := accountauth.New(accountauth.Config{
		Attempts: store,
		Accounts: accounts,
		MTProto: func(accountauth.LoginAttempt) (accountauth.MTProtoAuth, error) {
			return mtproto, nil
		},
		Now:         clock.Now,
		AttemptTTL:  10 * time.Minute,
		RateLimit:   5,
		QRSupported: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, store, clock
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// connect drives an attempt from created to the state where the user has
// something to do. It is the same "start login" call the API makes.
func connect(t *testing.T, svc *accountauth.Service, owner int64, id string) accountauth.LoginAttempt {
	t.Helper()
	attempt, err := svc.Connect(context.Background(), owner, id)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return attempt
}

func authorizedUser() *accountauth.AuthorizedUser {
	return &accountauth.AuthorizedUser{
		RemoteUserID:    4242,
		RemoteUsername:  "someone",
		RemoteFirstName: "Some",
		RemoteLastName:  "One",
		PhoneMasked:     "+49•••••7890",
	}
}

func TestLoginAttemptWalksTheHappyPath(t *testing.T) {
	mtproto := &fakeMTProto{sentMethod: accountauth.DeliveryTelegramApp, sentResendAt: time.Date(2026, 3, 1, 12, 1, 0, 0, time.UTC), statusUser: authorizedUser()}
	accounts := &fakeAccounts{}
	svc, _, _ := newServiceOverWithAccounts(t, mtproto, accountauth.NewMemoryLoginAttemptStore(), accounts)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if attempt.State != accountauth.StateWaitingForCode {
		t.Fatalf("state = %q, want Start to send the code and return %q", attempt.State, accountauth.StateWaitingForCode)
	}
	if attempt.AccountID == "" {
		t.Fatal("login start must allocate the stable account identity before creating its MTProto session")
	}
	if len(accounts.pending) != 1 || accounts.pending[0].AccountID != attempt.AccountID {
		t.Fatalf("pending account = %+v, want the attempt's stable account id %q", accounts.pending, attempt.AccountID)
	}
	if attempt.Method != accountauth.MethodPhone {
		t.Fatalf("method = %q, want %q", attempt.Method, accountauth.MethodPhone)
	}
	if attempt.Delivery != accountauth.DeliveryTelegramApp {
		t.Fatalf("delivery = %q, want the method Telegram actually used", attempt.Delivery)
	}
	if attempt.NextStep == "" {
		t.Fatal("the attempt does not say what the user should do next")
	}
	if strings.Contains(attempt.PhoneMasked, "491234567890") {
		t.Fatalf("the attempt exposes the full phone number: %q", attempt.PhoneMasked)
	}
	connected := connect(t, svc, owner, attempt.ID)
	if connected.State != accountauth.StateWaitingForCode {
		t.Fatalf("state = %q, want %q", connected.State, accountauth.StateWaitingForCode)
	}
	if connected.Delivery != accountauth.DeliveryTelegramApp {
		t.Fatalf("delivery = %q, want the method Telegram actually used", connected.Delivery)
	}
	if connected.ResendAfter.IsZero() {
		t.Fatal("the attempt does not report when a resend is allowed")
	}

	after, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345")
	if err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}
	if after.State != accountauth.StateAuthorized {
		t.Fatalf("state = %q, want %q", after.State, accountauth.StateAuthorized)
	}
	if after.AccountID == "" {
		t.Fatal("an authorized attempt carries no account id")
	}
	if after.AccountID != attempt.AccountID {
		t.Fatalf("account id changed during login: start=%q authorized=%q", attempt.AccountID, after.AccountID)
	}
	if len(accounts.ensured) != 1 {
		t.Fatalf("the account store was told about %d identities, want 1", len(accounts.ensured))
	}
	if got := accounts.ensured[0].OwnerUserID; got != owner {
		t.Fatalf("the account was recorded for owner %d, want %d", got, owner)
	}
}

func TestAttemptReusesOneMTProtoSessionAcrossHTTPRequests(t *testing.T) {
	attempts := accountauth.NewMemoryLoginAttemptStore()
	accounts := &fakeAccounts{}
	var factoryCalls int
	var sessions []*fakeMTProto
	svc, err := accountauth.New(accountauth.Config{
		Attempts: attempts,
		Accounts: accounts,
		MTProto: func(accountauth.LoginAttempt) (accountauth.MTProtoAuth, error) {
			factoryCalls++
			session := &fakeMTProto{statusUser: authorizedUser()}
			sessions = append(sessions, session)
			return session, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := svc.Connect(context.Background(), owner, attempt.ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345"); err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("the auth-session factory ran %d times across HTTP steps, want one", factoryCalls)
	}
	if len(sessions) != 1 || sessions[0].started != 1 || sessions[0].codeCalls != 1 {
		t.Fatalf("the attempt did not use one session for both steps: calls=%d sessions=%+v", factoryCalls, sessions)
	}
}

func TestPhoneCodeHashNeverEntersThePublicOrPersistedAttempt(t *testing.T) {
	const codeHash = "private-server-code-hash-marker"
	mtproto := &fakeMTProto{sentMethod: accountauth.DeliverySMS, sentCodeHash: codeHash}
	svc, store, _ := newService(t, mtproto)
	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	status, err := svc.Status(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	persisted, err := store.ByID(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	for name, value := range map[string]any{
		"start response":  attempt,
		"status response": status,
		"attempt row":     persisted,
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), codeHash) {
			t.Fatalf("%s leaks the private phone-code hash: %s", name, raw)
		}
	}
}

func TestAuthorizationStopsTheTemporarySessionForRunnerHandoff(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)
	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)

	after, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345")
	if err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}
	if after.State != accountauth.StateAuthorized {
		t.Fatalf("state = %q, want authorized", after.State)
	}
	if mtproto.closed != 1 {
		t.Fatalf("temporary session closed %d times before handoff, want once", mtproto.closed)
	}
	if mtproto.cancelled != 0 {
		t.Fatal("successful authorization canceled the session instead of preserving it for the account Runner")
	}
}

func TestPhoneLoginPassesFullPhoneTransientlyAndStoresOnlyMasked(t *testing.T) {
	const phone = "+491234567890"
	mtproto := &fakeMTProto{sentMethod: accountauth.DeliverySMS}
	svc, store, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: phone,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if strings.Contains(attempt.PhoneMasked, "491234567890") {
		t.Fatalf("Start returned the full phone: %q", attempt.PhoneMasked)
	}
	if strings.Contains(fmt.Sprintf("%+v", attempt), phone) {
		t.Fatalf("Start returned the full phone in attempt: %+v", attempt)
	}

	connected := connect(t, svc, owner, attempt.ID)
	if mtproto.startPhone != phone {
		t.Fatalf("phone passed to MTProto = %q, want original full number", mtproto.startPhone)
	}
	if strings.Contains(fmt.Sprintf("%+v", connected), phone) {
		t.Fatalf("Connect returned the full phone in attempt: %+v", connected)
	}
	persisted, err := store.ByID(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", persisted), phone) {
		t.Fatalf("persisted attempt contains the full phone: %+v", persisted)
	}
	if persisted.PhoneMasked == "" {
		t.Fatal("persisted attempt has no masked phone")
	}
}

func TestLoginAttemptRejectsAnInvalidTransition(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	connect(t, svc, owner, attempt.ID)

	// A password is not accepted before the server asks for one.
	if _, err := svc.SubmitPassword(context.Background(), owner, attempt.ID, "hunter2"); !errors.Is(err, accountauth.ErrInvalidTransition) {
		t.Fatalf("SubmitPassword in state %q error = %v, want ErrInvalidTransition", attempt.State, err)
	}

	// Cancelling is terminal: the attempt cannot be driven any further.
	if err := svc.Cancel(context.Background(), owner, attempt.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345"); !errors.Is(err, accountauth.ErrAttemptFinished) {
		t.Fatalf("SubmitCode on a canceled attempt error = %v, want ErrAttemptFinished", err)
	}
}

func TestLoginAttemptCannotBeReusedAfterAuthorization(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)
	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345"); err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}
	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345"); !errors.Is(err, accountauth.ErrAttemptFinished) {
		t.Fatalf("second SubmitCode error = %v, want ErrAttemptFinished", err)
	}
	if _, err := svc.SubmitPassword(context.Background(), owner, attempt.ID, "hunter2"); !errors.Is(err, accountauth.ErrAttemptFinished) {
		t.Fatalf("SubmitPassword on an authorized attempt error = %v, want ErrAttemptFinished", err)
	}
	if err := svc.Cancel(context.Background(), owner, attempt.ID); !errors.Is(err, accountauth.ErrAttemptFinished) {
		t.Fatalf("Cancel on an authorized attempt error = %v, want ErrAttemptFinished", err)
	}
}

func TestLoginAttemptExpires(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, clock := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	connect(t, svc, owner, attempt.ID)

	clock.Advance(11 * time.Minute)

	if _, err := svc.Status(context.Background(), owner, attempt.ID); !errors.Is(err, accountauth.ErrAttemptExpired) {
		t.Fatalf("Status after expiry error = %v, want ErrAttemptExpired", err)
	}
	if mtproto.cancelled != 1 || mtproto.closed != 1 {
		t.Fatalf("expired login resources: canceled=%d closed=%d, want one of each", mtproto.cancelled, mtproto.closed)
	}
	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345"); !errors.Is(err, accountauth.ErrAttemptExpired) {
		t.Fatalf("SubmitCode after expiry error = %v, want ErrAttemptExpired", err)
	}
}

func TestAnotherUserCannotInspectCompleteOrCancelAnAttempt(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := svc.Status(context.Background(), otherUser, attempt.ID); !errors.Is(err, accountauth.ErrNotFound) {
		t.Fatalf("Status by another user error = %v, want ErrNotFound", err)
	}
	if _, err := svc.SubmitCode(context.Background(), otherUser, attempt.ID, "12345"); !errors.Is(err, accountauth.ErrNotFound) {
		t.Fatalf("SubmitCode by another user error = %v, want ErrNotFound", err)
	}
	if _, err := svc.SubmitPassword(context.Background(), otherUser, attempt.ID, "hunter2"); !errors.Is(err, accountauth.ErrNotFound) {
		t.Fatalf("SubmitPassword by another user error = %v, want ErrNotFound", err)
	}
	if err := svc.Cancel(context.Background(), otherUser, attempt.ID); !errors.Is(err, accountauth.ErrNotFound) {
		t.Fatalf("Cancel by another user error = %v, want ErrNotFound", err)
	}

	// The owner can still finish: the failed hijack changed nothing.
	connect(t, svc, owner, attempt.ID)
	after, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345")
	if err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}
	if after.State != accountauth.StateAuthorized {
		t.Fatalf("state = %q, want %q", after.State, accountauth.StateAuthorized)
	}
}

func TestRepeatedCodeSubmissionsAreRateLimited(t *testing.T) {
	// Every submission is wrong: the limit must still bind, because a brute
	// force is exactly repeated wrong codes.
	mtproto := &fakeMTProto{codeErr: accountauth.ErrWrongCode}
	svc, _, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)

	var limited error
	for i := 0; i < 12; i++ {
		if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "00000"); err != nil {
			if errors.Is(err, accountauth.ErrRateLimited) {
				limited = err
				break
			}
			if !errors.Is(err, accountauth.ErrWrongCode) {
				t.Fatalf("SubmitCode %d error = %v, want ErrWrongCode", i, err)
			}
		}
	}
	if limited == nil {
		t.Fatal("repeated code submissions were never rate limited")
	}
	if got := mtproto.codeCalls; got > 6 {
		t.Fatalf("the server was called %d times; the limit is 5 per attempt", got)
	}
}

type delayedWrongCodeMTProto struct {
	fakeMTProto
	calls atomic.Int32
}

func (f *delayedWrongCodeMTProto) SubmitCode(context.Context, string) (accountauth.CodeResult, error) {
	f.calls.Add(1)
	time.Sleep(10 * time.Millisecond)
	return accountauth.CodeResult{}, accountauth.ErrWrongCode
}

func TestConcurrentCodeSubmissionsRespectPerAttemptRateLimit(t *testing.T) {
	mtproto := &delayedWrongCodeMTProto{}
	svc, _, _ := newService(t, mtproto)
	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)

	const submissions = 20
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < submissions; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = svc.SubmitCode(context.Background(), owner, attempt.ID, "00000")
		}()
	}
	close(start)
	wg.Wait()

	if got := mtproto.calls.Load(); got > accountauth.DefaultRateLimit {
		t.Fatalf("MTProto received %d concurrent submissions, limit is %d", got, accountauth.DefaultRateLimit)
	}
}

func TestCancellationReleasesMTProtoResources(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.Cancel(context.Background(), owner, attempt.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if mtproto.cancelled == 0 {
		t.Fatal("cancelling left the MTProto resources running")
	}
	if mtproto.closed != 1 {
		t.Fatalf("cancelling closed the MTProto session %d times, want once", mtproto.closed)
	}
}

func TestANewAttemptNeverOverwritesAnAuthorizedAccount(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)

	first, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, first.ID)
	if _, err := svc.SubmitCode(context.Background(), owner, first.ID, "12345"); err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}

	// A second login for the same owner and server must not steal the account
	// the first one just authorized.
	second, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567891",
	})
	if err != nil {
		t.Fatalf("Start(second): %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("a new attempt reused the id of an authorized one")
	}
	if second.State == accountauth.StateAuthorized {
		t.Fatal("a new attempt started out authorized")
	}
}

func TestLoginServiceIsAPersistedRecordNotAGoroutine(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, store, clock := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The state lives in the store: a caller that reads it back later sees the
	// same record, with no live process behind it.
	connect(t, svc, owner, attempt.ID)
	row, err := store.ByID(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if row.State != accountauth.StateWaitingForCode {
		t.Fatalf("persisted state = %q, want %q", row.State, accountauth.StateWaitingForCode)
	}
	if row.ExpiresAt.IsZero() {
		t.Fatal("the persisted attempt has no expiry")
	}
	if row.UpdatedAt.IsZero() {
		t.Fatal("the persisted attempt has no update time")
	}

	// A restart is just a new service over the same store.
	restarted, _, _ := newServiceOver(t, mtproto, store)
	again, err := restarted.Status(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("Status after restart: %v", err)
	}
	if again.State != accountauth.StateWaitingForCode {
		t.Fatalf("state after restart = %q, want %q", again.State, accountauth.StateWaitingForCode)
	}
	_ = clock
}

func TestInvalidStartRequestsAreRefused(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)

	for name, req := range map[string]accountauth.StartRequest{
		"no server":       {NetworkType: "custom", Phone: "+491234567890"},
		"unknown network": {ServerKey: "cat", NetworkType: "carrier-pigeon", Phone: "+491234567890"},
		"no phone":        {ServerKey: "cat", NetworkType: "custom"},
		"unknown method":  {ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890", Method: "email"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Start(context.Background(), owner, req); err == nil {
				t.Fatalf("Start(%s) accepted an invalid request", name)
			}
		})
	}
}

func TestTwoOwnersGetSeparateAttemptsForTheSameRemoteIdentity(t *testing.T) {
	// Two users signing the same number on the same server are two logins, two
	// attempts and two accounts. Nothing is shared.
	accounts := &fakeAccounts{}
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newServiceOverWithAccounts(t, mtproto, accountauth.NewMemoryLoginAttemptStore(), accounts)

	first, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start(owner): %v", err)
	}
	second, err := svc.Start(context.Background(), otherUser, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start(other): %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("two owners shared one login attempt")
	}

	connect(t, svc, owner, first.ID)
	connect(t, svc, otherUser, second.ID)
	if _, err := svc.SubmitCode(context.Background(), owner, first.ID, "12345"); err != nil {
		t.Fatalf("SubmitCode(owner): %v", err)
	}
	if _, err := svc.SubmitCode(context.Background(), otherUser, second.ID, "12345"); err != nil {
		t.Fatalf("SubmitCode(other): %v", err)
	}

	if len(accounts.ensured) != 2 {
		t.Fatalf("the account store saw %d identities, want 2", len(accounts.ensured))
	}
	if accounts.ensured[0].OwnerUserID == accounts.ensured[1].OwnerUserID {
		t.Fatal("two owners ended up on one account record")
	}

	// Neither owner may see the other's attempt.
	if _, err := svc.Status(context.Background(), otherUser, first.ID); !errors.Is(err, accountauth.ErrNotFound) {
		t.Fatalf("Status by another owner error = %v, want ErrNotFound", err)
	}
	if _, err := svc.Status(context.Background(), owner, second.ID); !errors.Is(err, accountauth.ErrNotFound) {
		t.Fatalf("Status by the first owner error = %v, want ErrNotFound", err)
	}
}

func TestStartRefusesANonPositiveOwner(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)

	// The owner is an authorization decision and is never taken from a client
	// payload; a missing owner cannot create an attempt.
	if _, err := svc.Start(context.Background(), 0, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	}); !errors.Is(err, accountauth.ErrNotAuthorized) {
		t.Fatalf("Start with no owner error = %v, want ErrNotAuthorized", err)
	}
}

func TestLoginAttemptJSONCarriesNoSecrets(t *testing.T) {
	mtproto := &fakeMTProto{statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)
	after, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "654321")
	if err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}

	payload, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, secret := range []string{
		"654321", "491234567890", "auth-key", "session", "hunter2",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("the attempt JSON leaks %q: %s", secret, text)
		}
	}
	if strings.Contains(text, "session_bytes") || strings.Contains(text, "sessionCiphertext") {
		t.Fatalf("the attempt JSON carries session bytes: %s", text)
	}
}
