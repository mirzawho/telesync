package accountauth_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgmock"

	"github.com/mirza/telesync/internal/accountauth"
)

// fakeAuth stands in for the connected session's *auth.Client. It records what
// it was given so tests can assert that a code or a password never reaches
// anything that could persist it.
type fakeAuth struct {
	sendCode  func(ctx context.Context, phone string, options auth.SendCodeOptions) (tg.AuthSentCodeClass, error)
	signIn    func(ctx context.Context, phone, code, codeHash string) (*tg.AuthAuthorization, error)
	password  func(ctx context.Context, hash auth.PasswordHashFunc) (*tg.AuthAuthorization, error)
	status    *auth.Status
	statusErr error

	sentPhones  []string
	seenCodes   []string
	seenHashes  []string
	passwordOps int
}

func (f *fakeAuth) SendCode(ctx context.Context, phone string, options auth.SendCodeOptions) (tg.AuthSentCodeClass, error) {
	f.sentPhones = append(f.sentPhones, phone)
	if f.sendCode != nil {
		return f.sendCode(ctx, phone, options)
	}
	return &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{Length: 5}, PhoneCodeHash: "hash-1"}, nil
}

func (f *fakeAuth) SignIn(ctx context.Context, phone, code, codeHash string) (*tg.AuthAuthorization, error) {
	f.seenCodes = append(f.seenCodes, code)
	f.seenHashes = append(f.seenHashes, codeHash)
	if f.signIn != nil {
		return f.signIn(ctx, phone, code, codeHash)
	}
	return &tg.AuthAuthorization{User: &tg.User{ID: 1}}, nil
}

func (f *fakeAuth) PasswordWith(ctx context.Context, hash auth.PasswordHashFunc) (*tg.AuthAuthorization, error) {
	f.passwordOps++
	if f.password != nil {
		return f.password(ctx, hash)
	}
	return &tg.AuthAuthorization{User: &tg.User{ID: 1}}, nil
}

func (f *fakeAuth) Status(context.Context) (*auth.Status, error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	if f.status != nil {
		return f.status, nil
	}
	return &auth.Status{Authorized: true, User: &tg.User{ID: 4242, Username: "someone", Phone: "491234567890"}}, nil
}

// fakeQR stands in for the gotd QR flow.
type fakeQR struct {
	export   func(ctx context.Context) (qrlogin.Token, error)
	importFn func(ctx context.Context) (*tg.AuthAuthorization, error)
}

func (f *fakeQR) Export(ctx context.Context, _ ...int64) (qrlogin.Token, error) {
	if f.export != nil {
		return f.export(ctx)
	}
	return qrlogin.NewToken([]byte("token-bytes"), int(time.Now().Add(time.Minute).Unix())), nil
}

func (f *fakeQR) Import(ctx context.Context) (*tg.AuthAuthorization, error) {
	if f.importFn != nil {
		return f.importFn(ctx)
	}
	return nil, nil
}

// testAccountPassword is the server's own SRP material: a safe 2048-bit prime,
// exactly what account.getPassword returns. The password is hashed against it
// by gotd's SRP helper, never by a verifier of ours.
func testAccountPassword(t *testing.T) *tg.AccountPassword {
	t.Helper()
	prime := "C71CAEB9C6B1C9048E6C522F70F13F73980D40238E3E21C14934D037563D930F" +
		"48198A0AA7C14058229493D22530F4DBFA336F6E0AC925139543AED44CCE7C37" +
		"20FD51F69458705AC68CD4FE6B6B13ABDC9746512969328454F18FAF8C595F64" +
		"2477FE96BB2A941D5BCD1D4AC8CC49880708FA9B378E3C4F3A9060BEE67CF9A4" +
		"A4A695811051907E162753B56B0F6B410DBA74D8A84B2A14B3144E0EF1284754" +
		"FD17ED950D5965B4B9DD46582DB1178D169C6BC465B0D6FF9CA3928FEF5B9AE4" +
		"E418FC15E83EBEA0F87FA9FF5EED70050DED2849F47BF959D956850CE929851F" +
		"0D8115F635B105EE2E4E15D04B2454BF6F4FADF034B10403119CD8E3B92FCC5B"
	p, err := hex.DecodeString(prime)
	if err != nil {
		t.Fatal(err)
	}
	return &tg.AccountPassword{
		CurrentAlgo: &tg.PasswordKdfAlgoSHA256SHA256PBKDF2HMACSHA512iter100000SHA256ModPow{
			Salt1: []byte("salt-one"),
			Salt2: []byte("salt-two"),
			G:     3,
			P:     p,
		},
		SRPB:         []byte{0x02, 0x01},
		SRPID:        7,
		SecureRandom: []byte("secure-random-bytes-for-srp"),
	}
}

func newGotd(t *testing.T, cfg accountauth.GotdMTProtoConfig) *accountauth.GotdMTProto {
	t.Helper()
	if cfg.Auth == nil {
		cfg.Auth = &fakeAuth{}
	}
	g, err := accountauth.NewGotdMTProto(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestDeliveryMethodIsReportedAsTheServerSentIt(t *testing.T) {
	for name, tc := range map[string]struct {
		typ      tg.AuthSentCodeTypeClass
		want     string
		wantHint string
	}{
		"sms":          {typ: &tg.AuthSentCodeTypeSMS{Length: 5}, want: accountauth.DeliverySMS},
		"app":          {typ: &tg.AuthSentCodeTypeApp{Length: 6}, want: accountauth.DeliveryTelegramApp},
		"call":         {typ: &tg.AuthSentCodeTypeCall{Length: 5}, want: accountauth.DeliveryCall},
		"flash call":   {typ: &tg.AuthSentCodeTypeFlashCall{Pattern: "abcd"}, want: accountauth.DeliveryFlashCall, wantHint: "abcd"},
		"email":        {typ: &tg.AuthSentCodeTypeEmailCode{EmailPattern: "a***@b.c"}, want: accountauth.DeliveryEmail, wantHint: "a***@b.c"},
		"sms word":     {typ: &tg.AuthSentCodeTypeSMSWord{Beginning: "AB"}, want: accountauth.DeliverySMSWord, wantHint: "AB"},
		"sms phrase":   {typ: &tg.AuthSentCodeTypeSMSPhrase{Beginning: "CD"}, want: accountauth.DeliverySMSPhrase, wantHint: "CD"},
		"missed call":  {typ: &tg.AuthSentCodeTypeMissedCall{}, want: accountauth.DeliveryMissedCall},
		"fragment sms": {typ: &tg.AuthSentCodeTypeFragmentSMS{}, want: accountauth.DeliveryFragmentSMS},
		"firebase sms": {typ: &tg.AuthSentCodeTypeFirebaseSMS{}, want: accountauth.DeliveryFirebaseSMS},
		"unknown":      {typ: &tg.AuthSentCodeTypeSetUpEmailRequired{}, want: accountauth.DeliveryUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			authClient := &fakeAuth{
				sendCode: func(context.Context, string, auth.SendCodeOptions) (tg.AuthSentCodeClass, error) {
					return &tg.AuthSentCode{Type: tc.typ, PhoneCodeHash: "hash-1", Timeout: 60}, nil
				},
			}
			g := newGotd(t, accountauth.GotdMTProtoConfig{Auth: authClient, Phone: "+491234567890"})

			challenge, err := g.StartCode(context.Background(), accountauth.StartRequest{Phone: "+491234567890"})
			if err != nil {
				t.Fatalf("StartCode: %v", err)
			}
			if challenge.Delivery != tc.want {
				t.Fatalf("delivery = %q, want %q", challenge.Delivery, tc.want)
			}
			if tc.wantHint != "" && challenge.DeliveryHint != tc.wantHint {
				t.Fatalf("hint = %q, want %q", challenge.DeliveryHint, tc.wantHint)
			}
			if challenge.ResendAfter.IsZero() {
				t.Fatal("a code round with a server timeout reports no resend time")
			}
		})
	}
}

func TestALoginCodeIsNeverKeptByTheAdapter(t *testing.T) {
	authClient := &fakeAuth{}
	g := newGotd(t, accountauth.GotdMTProtoConfig{Auth: authClient, Phone: "+491234567890"})

	if _, err := g.StartCode(context.Background(), accountauth.StartRequest{Phone: "+491234567890"}); err != nil {
		t.Fatalf("StartCode: %v", err)
	}
	if _, err := g.SubmitCode(context.Background(), "31415"); err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}

	if len(authClient.seenCodes) != 1 || authClient.seenCodes[0] != "31415" {
		t.Fatalf("the code reached the client as %v", authClient.seenCodes)
	}

	// The adapter keeps the code nowhere: only the server's code-round handle
	// survives, and it is not a user secret.
	g.Cancel(context.Background())
	if _, err := g.SubmitCode(context.Background(), "31415"); err == nil {
		t.Fatal("a canceled login still accepts a code")
	}
	if g.CodeRoundHandle() != "" {
		t.Fatal("a canceled login kept the server's code-round handle")
	}
}

func TestTwoStepPasswordGoesThroughGotdSRPAndIsNeverHeld(t *testing.T) {
	var hashedWith []byte
	authClient := &fakeAuth{
		signIn: func(context.Context, string, string, string) (*tg.AuthAuthorization, error) {
			return nil, auth.ErrPasswordAuthNeeded
		},
		password: func(_ context.Context, hash auth.PasswordHashFunc) (*tg.AuthAuthorization, error) {
			// gotd asks the hash function for an SRP answer over a real
			// account.password. A hand-rolled verifier would not be here.
			answer, err := hash(context.Background(), testAccountPassword(t))
			if err != nil {
				return nil, err
			}
			hashedWith = append(hashedWith, answer.M1...)
			return &tg.AuthAuthorization{User: &tg.User{ID: 1}}, nil
		},
	}
	g := newGotd(t, accountauth.GotdMTProtoConfig{Auth: authClient, Phone: "+491234567890"})

	if _, err := g.StartCode(context.Background(), accountauth.StartRequest{Phone: "+491234567890"}); err != nil {
		t.Fatalf("StartCode: %v", err)
	}
	result, err := g.SubmitCode(context.Background(), "31415")
	if err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}
	if !result.PasswordRequired {
		t.Fatal("a 2FA account was not reported as needing a password")
	}

	const password = "a-real-two-step-password"
	if _, err := g.SubmitPassword(context.Background(), password); err != nil {
		t.Fatalf("SubmitPassword: %v", err)
	}
	if authClient.passwordOps != 1 {
		t.Fatalf("the SRP helper was called %d times, want 1", authClient.passwordOps)
	}
	if len(hashedWith) == 0 {
		t.Fatal("no SRP answer was produced")
	}
	// The plaintext password must not be what went to the server: only the SRP
	// answer, computed by gotd, left this call.
	if bytes.Contains(hashedWith, []byte(password)) {
		t.Fatal("the SRP answer contains the password")
	}
}

func TestQRLoginIsRealAndRefusedWhenUnsupported(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) {
		g := newGotd(t, accountauth.GotdMTProtoConfig{
			Auth:  &fakeAuth{},
			Phone: "+491234567890",
		})
		// No QR flow for this server: the answer is explicit and phone login is
		// untouched.
		if _, err := g.StartQR(context.Background(), accountauth.StartRequest{Method: accountauth.MethodQR}); !errors.Is(err, accountauth.ErrUnsupportedMethod) {
			t.Fatalf("StartQR error = %v, want ErrUnsupportedMethod", err)
		}
		if _, err := g.SubmitCode(context.Background(), "12345"); err != nil {
			t.Fatalf("phone login was blocked by a missing QR capability: %v", err)
		}
	})

	t.Run("server refuses the method", func(t *testing.T) {
		qr := &fakeQR{
			export: func(context.Context) (qrlogin.Token, error) {
				return qrlogin.Token{}, tgerr.New(400, "METHOD_INVALID")
			},
		}
		g := newGotd(t, accountauth.GotdMTProtoConfig{Auth: &fakeAuth{}, QR: qr, Phone: "+491234567890"})
		if _, err := g.StartQR(context.Background(), accountauth.StartRequest{Method: accountauth.MethodQR}); !errors.Is(err, accountauth.ErrUnsupportedMethod) {
			t.Fatalf("StartQR error = %v, want ErrUnsupportedMethod", err)
		}
	})

	t.Run("token with expiry", func(t *testing.T) {
		expiry := time.Now().Add(2 * time.Minute)
		qr := &fakeQR{
			export: func(context.Context) (qrlogin.Token, error) {
				return qrlogin.NewToken([]byte("login-token"), int(expiry.Unix())), nil
			},
		}
		g := newGotd(t, accountauth.GotdMTProtoConfig{Auth: &fakeAuth{}, QR: qr, Phone: "+491234567890"})
		challenge, err := g.StartQR(context.Background(), accountauth.StartRequest{Method: accountauth.MethodQR})
		if err != nil {
			t.Fatalf("StartQR: %v", err)
		}
		if challenge.Token == "" {
			t.Fatal("no token was exported")
		}
		if challenge.ExpiresAt.IsZero() {
			t.Fatal("the exported token has no expiry")
		}
		if !challenge.ExpiresAt.Before(expiry.Add(time.Second)) || !challenge.ExpiresAt.After(expiry.Add(-time.Second)) {
			t.Fatalf("expiry = %v, want about %v", challenge.ExpiresAt, expiry)
		}
	})

	t.Run("waiting for confirmation", func(t *testing.T) {
		qr := &fakeQR{importFn: func(context.Context) (*tg.AuthAuthorization, error) { return nil, nil }}
		g := newGotd(t, accountauth.GotdMTProtoConfig{Auth: &fakeAuth{}, QR: qr, Phone: "+491234567890"})
		result, err := g.PollQR(context.Background(), "login-token")
		if err != nil {
			t.Fatalf("PollQR: %v", err)
		}
		if result.Authorized {
			t.Fatal("an unconfirmed token was reported as authorized")
		}
	})

	t.Run("confirmed", func(t *testing.T) {
		qr := &fakeQR{
			importFn: func(context.Context) (*tg.AuthAuthorization, error) {
				return &tg.AuthAuthorization{User: &tg.User{ID: 4242, Username: "someone"}}, nil
			},
		}
		g := newGotd(t, accountauth.GotdMTProtoConfig{
			Auth:  &fakeAuth{status: &auth.Status{Authorized: true, User: &tg.User{ID: 4242, Username: "someone"}}},
			QR:    qr,
			Phone: "+491234567890",
		})
		result, err := g.PollQR(context.Background(), "login-token")
		if err != nil {
			t.Fatalf("PollQR: %v", err)
		}
		if !result.Authorized {
			t.Fatal("a confirmed token was not reported as authorized")
		}
		if result.User == nil || result.User.RemoteUserID != 4242 {
			t.Fatalf("the confirmed identity = %+v", result.User)
		}
	})
}

func TestMTProtoErrorsMapToTheLoginStates(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"wrong code":         {err: tgerr.New(400, "PHONE_CODE_INVALID"), want: accountauth.ErrWrongCode},
		"expired code":       {err: tgerr.New(400, "PHONE_CODE_EXPIRED"), want: accountauth.ErrCodeExpired},
		"wrong password":     {err: tgerr.New(400, "PASSWORD_HASH_INVALID"), want: accountauth.ErrWrongPassword},
		"2FA needed":         {err: auth.ErrPasswordAuthNeeded, want: accountauth.ErrPasswordRequired},
		"flood wait":         {err: tgerr.New(420, "FLOOD_WAIT_30"), want: accountauth.ErrFloodWait},
		"unsupported method": {err: tgerr.New(400, "METHOD_INVALID"), want: accountauth.ErrUnsupportedMethod},
		"invalid number":     {err: tgerr.New(400, "PHONE_NUMBER_INVALID"), want: accountauth.ErrUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			authClient := &fakeAuth{
				signIn: func(context.Context, string, string, string) (*tg.AuthAuthorization, error) {
					return nil, tc.err
				},
			}
			g := newGotd(t, accountauth.GotdMTProtoConfig{Auth: authClient, Phone: "+491234567890"})
			if _, err := g.StartCode(context.Background(), accountauth.StartRequest{Phone: "+491234567890"}); err != nil {
				t.Fatalf("StartCode: %v", err)
			}
			result, err := g.SubmitCode(context.Background(), "12345")
			if errors.Is(tc.want, accountauth.ErrPasswordRequired) {
				if err != nil {
					t.Fatalf("SubmitCode error = %v", err)
				}
				if !result.PasswordRequired {
					t.Fatal("2FA was not reported as the next step")
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("SubmitCode error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTheRealGotdAuthClientSatisfiesTheAdapter(t *testing.T) {
	// Compile-time proof that the adapter is aimed at gotd's own auth client,
	// not at a shape invented here.
	var _ accountauth.GotdAuth = (*auth.Client)(nil)
	var _ accountauth.GotdQR = qrlogin.QR{}
}

func TestGotdAuthClientAgainstAMockedServer(t *testing.T) {
	// A handshake-level check: the adapter really calls auth.sendCode on the
	// wire. This is a simulated server, not a live Telegram session.
	mock := tgmock.New(t)
	mock.ExpectCall(&tg.AuthSendCodeRequest{
		PhoneNumber: "491234567890",
		APIID:       constant.TestAppID,
		APIHash:     constant.TestAppHash,
		Settings:    tg.CodeSettings{},
	}).ThenResult(&tg.AuthSentCode{
		Type:          &tg.AuthSentCodeTypeSMS{Length: 5},
		PhoneCodeHash: "server-hash",
		Timeout:       60,
	})

	client := tg.NewClient(mock)
	authClient := auth.NewClient(client, nil, constant.TestAppID, constant.TestAppHash)
	g, err := accountauth.NewGotdMTProto(accountauth.GotdMTProtoConfig{
		Auth:  authClient,
		Phone: "491234567890",
	})
	if err != nil {
		t.Fatal(err)
	}

	challenge, err := g.StartCode(context.Background(), accountauth.StartRequest{Phone: "491234567890"})
	if err != nil {
		t.Fatalf("StartCode: %v", err)
	}
	if challenge.Delivery != accountauth.DeliverySMS {
		t.Fatalf("delivery = %q, want %q", challenge.Delivery, accountauth.DeliverySMS)
	}
	if challenge.PhoneCodeHash != "server-hash" {
		t.Fatalf("code hash = %q, want the server's own handle", challenge.PhoneCodeHash)
	}
}
