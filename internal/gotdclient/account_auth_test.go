package gotdclient_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/exchange"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"
	"github.com/gotd/td/transport"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/profile"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/sessionstore"
)

func TestAccountAuthUsesOneAccountBoxSessionAndHandsItOffAfterAuthorization(t *testing.T) {
	const (
		accountID = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
		codeHash  = "private-phone-code-hash-marker"
		phone     = "+491234567890"
		code      = "31415"
	)
	var (
		mu             sync.Mutex
		signInRequests []*tg.AuthSignInRequest
	)
	dispatcher := tgtest.NewDispatcher()
	dispatcher.HandleFunc(tg.HelpGetConfigRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		return server.SendResult(req, &tg.Config{
			ThisDC:    req.DC,
			DCOptions: []tg.DCOption{{ID: 2, IPAddress: "mt.backend.test", Port: 8443, Static: true}},
		})
	})
	dispatcher.HandleFunc(tg.AuthSendCodeRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		return server.SendResult(req, &tg.AuthSentCode{
			Type:          &tg.AuthSentCodeTypeSMS{Length: 5},
			PhoneCodeHash: codeHash,
			Timeout:       60,
		})
	})
	dispatcher.HandleFunc(tg.AuthSignInRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		var input tg.AuthSignInRequest
		if err := input.Decode(req.Buf); err != nil {
			return err
		}
		mu.Lock()
		signInRequests = append(signInRequests, &input)
		mu.Unlock()
		if input.PhoneNumber != phone || input.PhoneCode != code || input.PhoneCodeHash != codeHash {
			return server.SendErr(req, tgerr.New(400, "PHONE_CODE_INVALID"))
		}
		return server.SendResult(req, &tg.AuthAuthorization{
			User: &tg.User{ID: 4242, FirstName: "Account"},
		})
	})
	dispatcher.HandleFunc(tg.UsersGetUsersRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		return server.SendVector(req, &tg.User{ID: 4242, FirstName: "Account", Phone: "491234567890"})
	})

	listener, publicKey := startAccountAuthServer(t, dispatcher)
	dial := &recordedDial{l: listener}
	spec := specForCustom(t, listener, dial, publicKey)
	encrypter, err := secrets.NewEncrypter(testAccountAuthKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := t.TempDir()
	factory, err := gotdclient.NewAccountAuthFactory(gotdclient.AccountAuthFactoryConfig{
		ResolveSpec: func(attempt accountauth.LoginAttempt) (*profile.Spec, error) {
			if attempt.ServerKey != spec.Key {
				return nil, errors.New("unexpected server key")
			}
			return spec, nil
		},
		SessionDir: sessionDir,
		Encrypter:  encrypter,
		KeyVersion: 1,
	})
	if err != nil {
		t.Fatalf("NewAccountAuthFactory: %v", err)
	}

	attempt := accountauth.LoginAttempt{
		ID:          "lga_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		AccountID:   accountID,
		OwnerUserID: 7,
		ServerKey:   spec.Key,
		NetworkType: "custom",
		Method:      accountauth.MethodPhone,
	}
	mtproto, err := factory(attempt)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	challenge, err := mtproto.StartCode(context.Background(), accountauth.StartRequest{Phone: phone})
	if err != nil {
		t.Fatalf("StartCode: %v", err)
	}
	if challenge.Delivery != accountauth.DeliverySMS {
		t.Fatalf("delivery = %q, want server-reported sms", challenge.Delivery)
	}
	result, err := mtproto.SubmitCode(context.Background(), code)
	if err != nil {
		t.Fatalf("SubmitCode on the same HTTP-independent auth session: %v", err)
	}
	if !result.Authorized || result.User == nil || result.User.RemoteUserID != 4242 {
		t.Fatalf("authorization result = %+v, want server-verified user 4242", result)
	}

	box, err := sessionstore.NewAccountBox(sessionstore.AccountBoxConfig{
		Dir:         sessionDir,
		AccountID:   accountID,
		Encrypter:   encrypter,
		KeyVersion:  1,
		NetworkKey:  spec.Key,
		NetworkType: "custom",
	})
	if err != nil {
		t.Fatalf("open account box: %v", err)
	}
	if _, err := box.LoadPhoneCodeHash(); err != nil {
		t.Fatalf("private phone-code hash was not persisted across auth steps: %v", err)
	}

	// Close is the handoff boundary: it waits for the temporary Client.Run to
	// flush the same AccountBox before a permanent Runner may open it.
	if err := mtproto.Close(); err != nil {
		t.Fatalf("close temporary account session: %v", err)
	}
	if _, err := box.LoadPhoneCodeHash(); !errors.Is(err, sessionstore.ErrPhoneCodeHashNotFound) {
		t.Fatalf("phone-code hash survived authorization handoff: %v", err)
	}
	sessionBytes, err := box.LoadSession(context.Background())
	if err != nil {
		t.Fatalf("load handoff session: %v", err)
	}
	for _, secret := range []string{phone, code, codeHash, "2fa-password-marker"} {
		if containsBytes(sessionBytes, secret) {
			t.Fatalf("session state contains transient value %q", secret)
		}
	}

	mu.Lock()
	if len(signInRequests) != 1 {
		mu.Unlock()
		t.Fatalf("auth.signIn was called %d times, want once", len(signInRequests))
	}
	mu.Unlock()
	dial.mu.Lock()
	firstConnectionCount := len(dial.seen)
	dial.mu.Unlock()
	if firstConnectionCount != 1 {
		t.Fatalf("temporary HTTP login steps opened %d MTProto connections, want one", firstConnectionCount)
	}
	dial.mu.Lock()
	dialTargets := strings.Join(dial.seen, "\n")
	dial.mu.Unlock()
	if !strings.Contains(dialTargets, "mt.backend.test:8443") {
		t.Fatalf("the account auth client did not use its configured custom endpoint: %q", dialTargets)
	}

	// The permanently supervised client can now load the same account-bound
	// session and verify it is authorized; it starts only after the auth client
	// has completely stopped.
	opts := spec.Options(nil, nil)
	opts.SessionStorage = box
	permanent := telegram.NewClient(spec.AppID, spec.AppHash, opts)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = permanent.Run(ctx, func(runCtx context.Context) error {
		status, err := permanent.Auth().Status(runCtx)
		if err != nil {
			return err
		}
		if !status.Authorized || status.User.ID != 4242 {
			return errors.New("the account Runner did not inherit the verified authorization")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("permanent account Runner handoff: %v", err)
	}

	for _, name := range []string{accountID + ".box", accountID + ".login"} {
		raw, err := os.ReadFile(filepath.Join(sessionDir, name))
		if err == nil {
			if containsBytes(raw, phone) || containsBytes(raw, code) || containsBytes(raw, codeHash) {
				t.Fatalf("persisted file %q contains a transient login value", name)
			}
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestAccountAuthFactoryRejectsAnotherServerOrNetworkProfile(t *testing.T) {
	encrypter, err := secrets.NewEncrypter(testAccountAuthKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		attempt accountauth.LoginAttempt
		spec    *profile.Spec
	}{
		"server key": {
			attempt: accountauth.LoginAttempt{
				AccountID: "acc_cccccccccccccccccccccccccc", OwnerUserID: 7,
				ServerKey: "cat", NetworkType: "custom",
			},
			spec: &profile.Spec{Kind: profile.Custom, Key: "official", AppID: 1, AppHash: "hash"},
		},
		"network kind": {
			attempt: accountauth.LoginAttempt{
				AccountID: "acc_dddddddddddddddddddddddddd", OwnerUserID: 7,
				ServerKey: "cat", NetworkType: "custom",
			},
			spec: &profile.Spec{Kind: profile.Official, Key: "cat", AppID: 1, AppHash: "hash"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			factory, err := gotdclient.NewAccountAuthFactory(gotdclient.AccountAuthFactoryConfig{
				ResolveSpec: func(accountauth.LoginAttempt) (*profile.Spec, error) { return tc.spec, nil },
				SessionDir:  t.TempDir(),
				Encrypter:   encrypter,
				KeyVersion:  1,
			})
			if err != nil {
				t.Fatalf("NewAccountAuthFactory: %v", err)
			}
			if _, err := factory(tc.attempt); err == nil {
				t.Fatal("factory accepted a profile from another server or network")
			}
		})
	}
}

func TestAccountAuthCancelDiscardsUnfinishedAccountSession(t *testing.T) {
	const accountID = "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	listener, publicKey := startAccountAuthServer(t, tgtest.NewDispatcher())
	dial := &recordedDial{l: listener}
	spec := specForCustom(t, listener, dial, publicKey)
	encrypter, err := secrets.NewEncrypter(testAccountAuthKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := t.TempDir()
	factory, err := gotdclient.NewAccountAuthFactory(gotdclient.AccountAuthFactoryConfig{
		ResolveSpec: func(accountauth.LoginAttempt) (*profile.Spec, error) { return spec, nil },
		SessionDir:  sessionDir,
		Encrypter:   encrypter,
		KeyVersion:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	mtproto, err := factory(accountauth.LoginAttempt{
		ID: "lga_bbbbbbbbbbbbbbbbbbbbbbbbbb", AccountID: accountID,
		OwnerUserID: 7, ServerKey: spec.Key, NetworkType: "custom", Method: accountauth.MethodPhone,
	})
	if err != nil {
		t.Fatal(err)
	}
	box, err := sessionstore.NewAccountBox(sessionstore.AccountBoxConfig{
		Dir: sessionDir, AccountID: accountID, Encrypter: encrypter, KeyVersion: 1,
		NetworkKey: spec.Key, NetworkType: "custom",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := box.StoreSession(context.Background(), []byte("unfinished session marker")); err != nil {
		t.Fatal(err)
	}
	if err := box.StorePhoneCodeHash("unfinished code hash marker"); err != nil {
		t.Fatal(err)
	}
	mtproto.Cancel(context.Background())
	if err := mtproto.Close(); err != nil {
		t.Fatalf("Close canceled account session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, accountID+".box")); !os.IsNotExist(err) {
		t.Fatalf("unfinished sealed session stat error = %v, want not-exist", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, accountID+".login")); !os.IsNotExist(err) {
		t.Fatalf("unfinished code-state stat error = %v, want not-exist", err)
	}
}

func startAccountAuthServer(t *testing.T, dispatcher *tgtest.Dispatcher) (*pipeListener, exchange.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := tgtest.NewServer(exchange.PrivateKey{RSA: key}, tgtest.UnpackInvoke(dispatcher), tgtest.ServerOptions{DC: 2})
	listener := newPipeListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(ctx, transport.Listen(listener))
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("account-auth test server did not stop in time")
		}
	})
	return listener, server.Key()
}

func testAccountAuthKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, secrets.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func containsBytes(haystack []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
