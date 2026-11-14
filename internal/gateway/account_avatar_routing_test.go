package gateway_test

import (
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/mirza/telesync/internal/avatarcache"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

func TestCanonicalAvatarRoutesCacheByResolvedAccount(t *testing.T) {
	cache, err := avatarcache.Open(filepath.Join(t.TempDir(), "avatars"), avatarcache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	workerA := readyWorker()
	workerA.avatar = rpc.AvatarFile{MimeType: "image/jpeg", PhotoID: 1, Data: []byte("owner-a")}
	workerB := readyWorker()
	workerB.avatar = rpc.AvatarFile{MimeType: "image/png", PhotoID: 2, Data: []byte("owner-b")}
	env := newAccountEnv(t, registry{}, func(c *gateway.Config) { c.Avatars = cache })
	globalView := []channelstore.GlobalPermission{channelstore.PermissionChannelsView}
	ownerA := env.account(t, "avatar-owner-a", globalView...)
	ownerB := env.account(t, "avatar-owner-b", globalView...)
	accountA := env.sessionAccount(t, ownerA, "official", channelstore.NetworkTelegram, workerA)
	accountB := env.sessionAccount(t, ownerB, "official", channelstore.NetworkTelegram, workerB)
	env.ownedChannelRow(t, accountA, "official", 700)
	env.ownedChannelRow(t, accountB, "official", 700)

	readAvatar := func(who, path, want string) {
		t.Helper()
		resp := env.send(t, who, http.MethodGet, path, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, body=%s", path, resp.StatusCode, bodyOf(resp))
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("GET %s body = %q, want %q", path, got, want)
		}
	}

	readAvatar(ownerA.token,
		"/api/v1/telegram/accounts/"+accountA+"/channels/700/avatar", "owner-a")
	readAvatar(ownerB.token,
		"/api/v1/telegram/accounts/"+accountB+"/channels/700/avatar", "owner-b")

	// A legacy keyed route is safe only after resolving this owner's sole
	// account on the named server, and must address the same account cache.
	readAvatar(ownerA.token, "/api/v1/telegram/official/channels/700/avatar", "owner-a")
	if workerA.callCount() != 1 || workerB.callCount() != 1 {
		t.Fatalf("avatar RPC counts A=%d B=%d, want one fetch per account and a cache hit on A's keyed route",
			workerA.callCount(), workerB.callCount())
	}
}
