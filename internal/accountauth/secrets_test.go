package accountauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
)

// A login code and a 2FA password are transient. They may exist in the call
// that submits them and nowhere else — not in a record, not in a log line, not
// in a JSON response.

func TestLoginCodeIsNeverWrittenToTheAttemptRecord(t *testing.T) {
	mtproto := &fakeMTProto{codeErr: accountauth.ErrWrongCode}
	svc, store, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)

	const code = "482913"
	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, code); !errors.Is(err, accountauth.ErrWrongCode) {
		t.Fatalf("SubmitCode error = %v, want ErrWrongCode", err)
	}
	if mtproto.lastCode != code {
		t.Fatal("the code never reached the MTProto seam")
	}

	row, err := store.ByID(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	payload, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), code) {
		t.Fatalf("the persisted attempt holds the login code: %s", payload)
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%+v %v", row, row)
	assertNoLeak(t, rendered.String(), code)
}

func TestTwoStepPasswordIsNeverWrittenToTheAttemptRecord(t *testing.T) {
	mtproto := &fakeMTProto{passwordRequired: true, statusUser: authorizedUser()}
	svc, store, _ := newService(t, mtproto)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)

	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "482913"); err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}
	if _, err := svc.Status(context.Background(), owner, attempt.ID); err != nil {
		t.Fatalf("Status: %v", err)
	}

	const password = "correct-horse-battery-staple"
	after, err := svc.SubmitPassword(context.Background(), owner, attempt.ID, password)
	if err != nil {
		t.Fatalf("SubmitPassword: %v", err)
	}
	if after.State != accountauth.StateAuthorized {
		t.Fatalf("state = %q, want %q", after.State, accountauth.StateAuthorized)
	}

	row, err := store.ByID(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	payload, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{password, "correct-horse"} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("the persisted attempt holds %q: %s", secret, payload)
		}
	}
	if strings.Contains(after.NextStep, password) || strings.Contains(after.ErrorCode, password) {
		t.Fatal("the attempt echoes the password back")
	}
}

func TestRejectedSecretsNeverComeBackOutOfAnError(t *testing.T) {
	const (
		code     = "1122334455"
		password = "an-incredibly-specific-passphrase"
	)

	mtproto := &fakeMTProto{codeErr: accountauth.ErrWrongCode}
	svc, _, _ := newService(t, mtproto)
	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)

	_, err = svc.SubmitCode(context.Background(), owner, attempt.ID, code)
	if err == nil {
		t.Fatal("a rejected code was reported as a success")
	}
	assertNoLeak(t, err.Error(), code, "112233")

	wrong := &fakeMTProto{passwordErr: accountauth.ErrWrongPassword, passwordRequired: true}
	svc2, _, _ := newService(t, wrong)
	attempt2, err := svc2.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc2, owner, attempt2.ID)
	if _, err := svc2.SubmitCode(context.Background(), owner, attempt2.ID, code); err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}
	_, err = svc2.SubmitPassword(context.Background(), owner, attempt2.ID, password)
	if err == nil {
		t.Fatal("a rejected password was reported as a success")
	}
	assertNoLeak(t, err.Error(), password, "incredibly-specific")
}

func assertNoLeak(t *testing.T, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("%q leaks %q", text, secret)
		}
	}
}

func TestWrongCodeExpiredCodeAndPasswordMapToTheRightStates(t *testing.T) {
	for name, tc := range map[string]struct {
		codeErr     error
		wantState   string
		wantCode    string
		wantErr     error
		passwordErr error
	}{
		"wrong code":     {codeErr: accountauth.ErrWrongCode, wantState: accountauth.StateWaitingForCode, wantCode: accountauth.CodeWrongCode, wantErr: accountauth.ErrWrongCode},
		"expired code":   {codeErr: accountauth.ErrCodeExpired, wantState: accountauth.StateWaitingForCode, wantCode: accountauth.CodeExpired, wantErr: accountauth.ErrCodeExpired},
		"wrong password": {codeErr: accountauth.ErrPasswordRequired, passwordErr: accountauth.ErrWrongPassword, wantState: accountauth.StateWaitingForPassword, wantCode: accountauth.CodeWrongPassword, wantErr: accountauth.ErrWrongPassword},
	} {
		t.Run(name, func(t *testing.T) {
			mtproto := &fakeMTProto{codeErr: tc.codeErr, passwordErr: tc.passwordErr, passwordRequired: tc.passwordErr != nil}
			svc, _, _ := newService(t, mtproto)
			attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
				ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			connect(t, svc, owner, attempt.ID)

			got, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "00000")
			if tc.passwordErr != nil {
				// The code step only announces that 2FA is next.
				if err != nil {
					t.Fatalf("SubmitCode: %v", err)
				}
				if got.State != accountauth.StateWaitingForPassword {
					t.Fatalf("state = %q, want %q", got.State, accountauth.StateWaitingForPassword)
				}
				got, err = svc.SubmitPassword(context.Background(), owner, attempt.ID, "whatever")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.State, tc.wantState)
			}
			if got.ErrorCode != tc.wantCode {
				t.Fatalf("error code = %q, want %q", got.ErrorCode, tc.wantCode)
			}
		})
	}
}

func TestARejectedCodeDoesNotEndTheAttempt(t *testing.T) {
	// Only the first code is wrong: the second must still be accepted.
	mtproto := &fakeMTProto{firstCodeFails: true, statusUser: authorizedUser()}
	svc, _, _ := newService(t, mtproto)
	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)

	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "00000"); !errors.Is(err, accountauth.ErrWrongCode) {
		t.Fatalf("SubmitCode error = %v, want ErrWrongCode", err)
	}
	// The user may try again with the right code; the attempt is still live.
	after, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345")
	if err != nil {
		t.Fatalf("second SubmitCode: %v", err)
	}
	if after.State != accountauth.StateAuthorized {
		t.Fatalf("state = %q, want %q", after.State, accountauth.StateAuthorized)
	}
}

func TestFloodWaitAndUnsupportedDeliveryMapToStableCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		startErr error
		wantCode string
		wantErr  error
	}{
		"flood wait":           {startErr: accountauth.ErrFloodWait, wantCode: accountauth.CodeFloodWait, wantErr: accountauth.ErrUnavailable},
		"unsupported delivery": {startErr: accountauth.ErrUnsupportedMethod, wantCode: accountauth.CodeUnsupportedLoginMethod, wantErr: accountauth.ErrUnavailable},
		"session unavailable":  {startErr: accountauth.ErrUnavailable, wantCode: accountauth.CodeSessionUnavailable, wantErr: accountauth.ErrUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			mtproto := &fakeMTProto{startErr: tc.startErr}
			svc, _, _ := newService(t, mtproto)
			attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
				ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Start error = %v, want %v", err, tc.wantErr)
			}
			if attempt.State != accountauth.StateFailed {
				t.Fatalf("state = %q, want %q", attempt.State, accountauth.StateFailed)
			}
			if attempt.ErrorCode != tc.wantCode {
				t.Fatalf("error code = %q, want %q", attempt.ErrorCode, tc.wantCode)
			}
		})
	}
}

func TestQRLoginIsRefusedWhenTheServerCannotDoIt(t *testing.T) {
	// A server without QR must say so. Phone login stays available.
	mtproto := &fakeMTProto{}
	svc, _, _ := newService(t, mtproto)

	_, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Method: accountauth.MethodQR,
	})
	if !errors.Is(err, accountauth.ErrUnsupportedMethod) {
		t.Fatalf("Start(qr) error = %v, want ErrUnsupportedMethod", err)
	}

	phone, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start(phone) after a refused qr: %v", err)
	}
	if phone.Method != accountauth.MethodPhone {
		t.Fatalf("method = %q, want %q", phone.Method, accountauth.MethodPhone)
	}
}

func TestQRLoginProducesAShortLivedTokenAndWaitsForConfirmation(t *testing.T) {
	mtproto := &fakeMTProto{
		qrSupportedServer: true,
		qrChallenge: accountauth.QRChallenge{
			Token:     "qr-login-token",
			ExpiresAt: time.Date(2026, 3, 1, 12, 5, 0, 0, time.UTC),
		},
		qrResult: accountauth.CodeResult{Authorized: true, User: authorizedUser()},
	}
	svc, _, clock := newServiceOverWithAccounts(t, mtproto, accountauth.NewMemoryLoginAttemptStore(), &fakeAccounts{})
	if err := svc.SetQRSupported(true); err != nil {
		t.Fatalf("SetQRSupported: %v", err)
	}

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Method: accountauth.MethodQR,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connected, err := svc.Connect(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if connected.State != accountauth.StateWaitingForQR {
		t.Fatalf("state = %q, want %q", connected.State, accountauth.StateWaitingForQR)
	}
	if connected.QRToken == "" {
		t.Fatal("no QR token was issued")
	}
	if connected.QRExpiresAt.IsZero() {
		t.Fatal("the QR token has no expiry")
	}
	if !connected.QRExpiresAt.After(clock.Now()) {
		t.Fatal("the QR token was issued already expired")
	}

	confirmed, err := svc.ConfirmQR(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("ConfirmQR: %v", err)
	}
	if confirmed.State != accountauth.StateAuthorized {
		t.Fatalf("state = %q, want %q", confirmed.State, accountauth.StateAuthorized)
	}
}

func TestQRLoginTokenExpiresAndIsRefreshed(t *testing.T) {
	expiry := time.Date(2026, 3, 1, 12, 5, 0, 0, time.UTC)
	mtproto := &fakeMTProto{
		qrSupportedServer: true,
		qrChallenge:       accountauth.QRChallenge{Token: "qr-login-token", ExpiresAt: expiry},
		qrErr:             accountauth.ErrQRExpired,
	}
	refreshedToken := accountauth.QRChallenge{Token: "qr-login-token-2", ExpiresAt: expiry.Add(15 * time.Minute)}
	svc, _, clock := newServiceOverWithAccounts(t, mtproto, accountauth.NewMemoryLoginAttemptStore(), &fakeAccounts{})
	if err := svc.SetQRSupported(true); err != nil {
		t.Fatal(err)
	}

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Method: accountauth.MethodQR,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := svc.Connect(context.Background(), owner, attempt.ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	clock.Advance(10 * time.Minute)
	if _, err := svc.ConfirmQR(context.Background(), owner, attempt.ID); !errors.Is(err, accountauth.ErrQRExpired) {
		t.Fatalf("ConfirmQR after expiry error = %v, want ErrQRExpired", err)
	}

	// A fresh token is short-lived too: the flow never hands out a permanent
	// credential to display.
	mtproto.qrChallenge = refreshedToken
	mtproto.qrErr = nil
	refreshed, err := svc.RefreshQR(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("RefreshQR: %v", err)
	}
	if refreshed.QRToken == "" {
		t.Fatal("refresh produced no token")
	}
	if !refreshed.QRExpiresAt.After(clock.Now()) {
		t.Fatal("the refreshed token is already expired")
	}
}

func TestQRLoginTokenIsPurgedWhenAttemptBecomesTerminal(t *testing.T) {
	for _, terminal := range []string{"authorized", "canceled", "failed", "expired"} {
		t.Run(terminal, func(t *testing.T) {
			mtproto := &fakeMTProto{
				qrSupportedServer: true,
				qrChallenge: accountauth.QRChallenge{
					Token:     "short-lived-qr-token",
					ExpiresAt: time.Date(2026, 3, 1, 12, 5, 0, 0, time.UTC),
				},
				qrResult: accountauth.CodeResult{Authorized: true, User: authorizedUser()},
			}
			svc, store, clock := newService(t, mtproto)
			if err := svc.SetQRSupported(true); err != nil {
				t.Fatal(err)
			}
			attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
				ServerKey: "cat", NetworkType: "custom", Method: accountauth.MethodQR,
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			connected, err := svc.Connect(context.Background(), owner, attempt.ID)
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			if connected.QRToken != "short-lived-qr-token" {
				t.Fatalf("active attempt QRToken = %q, want display token", connected.QRToken)
			}

			switch terminal {
			case "authorized":
				if _, err := svc.ConfirmQR(context.Background(), owner, attempt.ID); err != nil {
					t.Fatalf("ConfirmQR: %v", err)
				}
			case "canceled":
				if err := svc.Cancel(context.Background(), owner, attempt.ID); err != nil {
					t.Fatalf("Cancel: %v", err)
				}
			case "failed":
				mtproto.qrErr = accountauth.ErrUnavailable
				if _, err := svc.ConfirmQR(context.Background(), owner, attempt.ID); err == nil {
					t.Fatal("ConfirmQR unexpectedly succeeded")
				}
			case "expired":
				clock.Advance(11 * time.Minute)
				if _, err := svc.Status(context.Background(), owner, attempt.ID); !errors.Is(err, accountauth.ErrAttemptExpired) {
					t.Fatalf("Status error = %v, want ErrAttemptExpired", err)
				}
			}

			persisted, err := store.ByID(context.Background(), owner, attempt.ID)
			if err != nil {
				t.Fatalf("ByID: %v", err)
			}
			if persisted.QRToken != "" {
				t.Fatalf("terminal attempt retained QR token %q", persisted.QRToken)
			}
			if persisted.QRExpiresAt.IsZero() == false {
				t.Fatalf("terminal attempt retained QR expiry %v", persisted.QRExpiresAt)
			}
			returned, statusErr := svc.Status(context.Background(), owner, attempt.ID)
			if terminal == "expired" && !errors.Is(statusErr, accountauth.ErrAttemptExpired) {
				t.Fatalf("Status error = %v, want ErrAttemptExpired", statusErr)
			}
			if terminal != "expired" && statusErr != nil {
				t.Fatalf("Status: %v", statusErr)
			}
			if returned.QRToken != "" || !returned.QRExpiresAt.IsZero() {
				t.Fatalf("terminal status returned QR data: token=%q expiry=%v", returned.QRToken, returned.QRExpiresAt)
			}
		})
	}
}

func TestAuthorizationFromAnErrorNeverEchoesAnythingSecretive(t *testing.T) {
	mtproto := &fakeMTProto{codeErr: accountauth.ErrWrongCode}
	svc, _, _ := newService(t, mtproto)
	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)

	_, err = svc.SubmitCode(context.Background(), owner, attempt.ID, "777888")
	var buf bytes.Buffer
	if err != nil {
		buf.WriteString(err.Error())
	}
	got, _ := svc.Status(context.Background(), owner, attempt.ID)
	payload, _ := json.Marshal(got)
	buf.Write(payload)
	fmt.Fprintf(&buf, "%+v %v", got, got)
	assertNoLeak(t, buf.String(), "777888", "491234567890", "auth-key", "hunter2")
}
