package rpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/rpc"
)

// The routing seam is the account id. A request is delivered to the worker of
// the account it was resolved for and to no other, and nothing in a request
// body can assert a different owner.

const (
	accA = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	accB = "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// accountWorker is the client-side handle for one account's worker. It records
// every call it receives, so a test can prove which worker a request reached.
type accountWorker struct {
	account string
	mu      sync.Mutex
	calls   []string
}

func newAccountWorker(account string) *accountWorker { return &accountWorker{account: account} }

func (w *accountWorker) touch(op string) {
	w.mu.Lock()
	w.calls = append(w.calls, op)
	w.mu.Unlock()
}

func (w *accountWorker) received() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

func (w *accountWorker) Health(context.Context) (rpc.Health, error) {
	w.touch("health")
	return rpc.Health{AccountID: w.account, OwnerUserID: 7, State: rpc.StateConnected}, nil
}
func (w *accountWorker) Profile(context.Context) (rpc.Profile, error) {
	w.touch("profile")
	return rpc.Profile{AccountID: w.account}, nil
}
func (w *accountWorker) Channels(context.Context, int, int) (rpc.ChannelPage, error) {
	w.touch("channels")
	return rpc.ChannelPage{}, nil
}
func (w *accountWorker) Messages(context.Context, int64, int, int) (rpc.MessagePage, error) {
	w.touch("messages")
	return rpc.MessagePage{}, nil
}
func (w *accountWorker) Message(context.Context, int64, int) (rpc.MessagePage, error) {
	w.touch("message")
	return rpc.MessagePage{}, nil
}
func (w *accountWorker) Media(context.Context, int64, int, int, rpc.MediaSlice) (rpc.MediaStream, error) {
	w.touch("media")
	return rpc.MediaStream{}, nil
}
func (w *accountWorker) CustomEmoji(context.Context, int64) (rpc.MediaFile, error) {
	w.touch("custom_emoji")
	return rpc.MediaFile{}, nil
}
func (w *accountWorker) Find(context.Context, rpc.FindRequest) (rpc.FindResult, error) {
	w.touch("find")
	return rpc.FindResult{}, nil
}
func (w *accountWorker) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	w.touch("channel")
	return rpc.ChannelResult{}, nil
}
func (w *accountWorker) Join(context.Context, rpc.ChannelRef) (rpc.JoinResult, error) {
	w.touch("join")
	return rpc.JoinResult{}, nil
}
func (w *accountWorker) Avatar(context.Context, rpc.ChannelRef) (rpc.AvatarFile, error) {
	w.touch("avatar")
	return rpc.AvatarFile{}, nil
}
func (w *accountWorker) SendMessage(context.Context, rpc.SendRequest) (rpc.SendResult, error) {
	w.touch("send")
	return rpc.SendResult{}, nil
}
func (w *accountWorker) EditMessage(context.Context, rpc.EditRequest) (rpc.EditResult, error) {
	w.touch("edit")
	return rpc.EditResult{}, nil
}
func (w *accountWorker) DeleteMessage(context.Context, rpc.DeleteMessageRequest) (rpc.DeleteMessageResult, error) {
	w.touch("delete")
	return rpc.DeleteMessageResult{}, nil
}

// accountBackend is the worker-side RPC backend for one account, used to prove
// that a request arriving over one account's socket is served by that
// account's worker.
type accountBackend struct {
	*fakeBackend
	account string
}

func newAccountBackend(account string) *accountBackend {
	return &accountBackend{fakeBackend: &fakeBackend{}, account: account}
}

func (b *accountBackend) Health(context.Context) rpc.Health {
	return rpc.Health{Key: b.account, AccountID: b.account, State: rpc.StateConnected}
}

func TestTheRegistryRoutesByAccountIDAndNeverAcrossAccounts(t *testing.T) {
	reg := rpc.NewAccountRegistry()
	wA := newAccountWorker(accA)
	wB := newAccountWorker(accB)
	if err := reg.Register(accA, wA); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := reg.Register(accB, wB); err != nil {
		t.Fatalf("Register: %v", err)
	}

	gotA, ok := reg.Worker(accA)
	if !ok {
		t.Fatal("account A has no worker")
	}
	if gotA != rpc.Worker(wA) {
		t.Error("account A was routed to a worker that is not its own")
	}
	gotB, ok := reg.Worker(accB)
	if !ok {
		t.Fatal("account B has no worker")
	}
	if gotB != rpc.Worker(wB) {
		t.Error("account B was routed to a worker that is not its own")
	}
	if gotA == gotB {
		t.Fatal("two accounts were served by one worker")
	}
}

func TestAnUnknownAccountIsNotFoundAndNeverFallsBack(t *testing.T) {
	reg := rpc.NewAccountRegistry()
	if err := reg.Register(accA, newAccountWorker(accA)); err != nil {
		t.Fatal(err)
	}

	if _, ok := reg.Worker("acc_cccccccccccccccccccccccccc"); ok {
		t.Fatal("an unknown account was answered by some worker")
	}
	// A server key is not an account. Resolving one must not reach a worker.
	for _, key := range []string{"official", "cat", "the-cat", accA[:len(accA)-1]} {
		if _, ok := reg.Worker(key); ok {
			t.Errorf("the registry resolved %q, which is not an account id", key)
		}
	}
	// The empty key is never a wildcard.
	if _, ok := reg.Worker(""); ok {
		t.Fatal("the empty key resolved to a worker")
	}
}

func TestASecondSessionCannotTakeOverAnAccount(t *testing.T) {
	reg := rpc.NewAccountRegistry()
	first := newAccountWorker(accA)
	if err := reg.Register(accA, first); err != nil {
		t.Fatal(err)
	}
	// A duplicate registration for one account must be refused: otherwise a
	// stale worker could silently replace the live one.
	if err := reg.Register(accA, newAccountWorker(accA)); err == nil {
		t.Fatal("registering a second worker for one account must be refused")
	}
	got, _ := reg.Worker(accA)
	if got != rpc.Worker(first) {
		t.Error("the first worker for the account was replaced")
	}
}

func TestRemovingAnAccountStopsItBeingRoutable(t *testing.T) {
	reg := rpc.NewAccountRegistry()
	if err := reg.Register(accA, newAccountWorker(accA)); err != nil {
		t.Fatal(err)
	}
	if !reg.Unregister(accA) {
		t.Fatal("Unregister reported nothing removed")
	}
	if _, ok := reg.Worker(accA); ok {
		t.Error("a removed account is still routable")
	}
	if reg.Unregister(accA) {
		t.Error("removing an absent account reported success")
	}
}

func TestAccountsOfOneServerKeyStaySeparate(t *testing.T) {
	// Two users, two accounts, one server definition. The server key is not the
	// identity and may not collapse the two.
	reg := rpc.NewAccountRegistry()
	a := newAccountWorker(accA)
	b := newAccountWorker(accB)
	if err := reg.Register(accA, a); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(accB, b); err != nil {
		t.Fatal(err)
	}
	if reg.Len() != 2 {
		t.Fatalf("registered = %d, want 2", reg.Len())
	}
	accounts := reg.Accounts()
	if len(accounts) != 2 || accounts[0] == accounts[1] {
		t.Errorf("Accounts() = %v, want the two distinct account ids", accounts)
	}
}

// A request body that asserts an owner or an account is not believed. The
// account a call acts as is the one the request was routed to, and a request
// part naming another one is refused before it reaches any worker.
func TestARequestThatAssertsAnotherOwnerIsRefused(t *testing.T) {
	backend := &fakeBackend{}
	srv := httptest.NewServer(rpc.NewHandler(backend))
	defer srv.Close()

	body := `{
		"channel_id": 10,
		"text": "hello",
		"owner_user_id": 999,
		"account_id": "` + accB + `",
		"client_key": "cat"
	}`
	res := postSend(t, srv.URL, body)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.StatusCode, raw)
	}
	if backend.gotSend.ChannelID != 0 {
		t.Errorf("a request asserting another owner reached the worker: %+v", backend.gotSend)
	}
}

func TestAWellFormedSendCarriesNoOwnerAssertion(t *testing.T) {
	backend := &fakeBackend{}
	srv := httptest.NewServer(rpc.NewHandler(backend))
	defer srv.Close()

	res := postSend(t, srv.URL, `{"channel_id": 10, "text": "hello"}`)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, raw)
	}
	if backend.gotSend.ChannelID != 10 {
		t.Errorf("the send never reached the worker: %+v", backend.gotSend)
	}
	// The request type has no owner or account field at all, so no downstream
	// code can ever be handed a caller's assertion.
	encoded, err := json.Marshal(rpc.SendRequest{ChannelID: 10, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"owner", "account_id", "client_key", "user_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("rpc.SendRequest carries a caller-settable %s: %s", forbidden, encoded)
		}
	}
}

// postSend posts one multipart send request, the shape the worker socket takes.
func postSend(t *testing.T, base, requestJSON string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormField("request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(requestJSON)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(base+"/messages", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return res
}

// A worker serves one account over its own socket. A request that arrives on
// another account's socket is answered by that account's worker, and never by
// the first one.
func TestEachAccountsSocketServesOnlyItsOwnWorker(t *testing.T) {
	clients := map[string]*rpc.Client{}
	for _, id := range []string{accA, accB} {
		id := id
		backend := newAccountBackend(id)
		srv := httptest.NewServer(rpc.NewHandler(backend))
		t.Cleanup(srv.Close)
		addr := srv.Listener.Addr().String()
		clients[id] = rpc.NewClientWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}, 2*time.Second)
	}

	hA, err := clients[accA].Health(context.Background())
	if err != nil {
		t.Fatalf("A Health: %v", err)
	}
	hB, err := clients[accB].Health(context.Background())
	if err != nil {
		t.Fatalf("B Health: %v", err)
	}
	if hA.AccountID != accA {
		t.Errorf("A's socket answered as %q", hA.AccountID)
	}
	if hB.AccountID != accB {
		t.Errorf("B's socket answered as %q", hB.AccountID)
	}
	if hA.AccountID == hB.AccountID {
		t.Fatal("one worker answered for two accounts")
	}
}

func TestTheAccountKeyIsOpaqueAndValidated(t *testing.T) {
	for _, bad := range []string{
		"", "official", "cat", "+989125550177", "user@example.com", "../acc",
		"acc_" + strings.Repeat("a", 25),
		"acc_" + strings.Repeat("a", 25) + "i",
		"acc_" + strings.Repeat("A", 26),
	} {
		if err := rpc.ValidAccountID(bad); err == nil {
			t.Errorf("ValidAccountID(%q) accepted an identity that is not an opaque account id", bad)
		}
	}
	if err := rpc.ValidAccountID(accA); err != nil {
		t.Errorf("ValidAccountID(%q) = %v", accA, err)
	}
}

func TestAHealthAnswerCarriesTheAccountItServes(t *testing.T) {
	var got rpc.Health
	backend := &fakeBackend{health: rpc.Health{AccountID: accA, OwnerUserID: 7, State: rpc.StateConnected}}
	srv := httptest.NewServer(rpc.NewHandler(backend))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AccountID != accA || got.OwnerUserID != 7 {
		t.Errorf("health = %+v, want the account it serves", got)
	}
}
