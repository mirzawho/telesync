package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// The local channel detail route and the Telegram channel detail route answer
// the same channel object from two data sources: Telegram's live state and the
// locally persisted snapshot. These tests compare the two contracts key by key,
// reading only what each handler really serializes.

// channelKeys is the complete key set of the canonical channel object.
var channelKeys = []string{
	"avatar_available", "avatar_url", "description", "fake", "id", "is_managed",
	"is_member", "membership_status", "participants_count", "permissions",
	"scam", "title", "type", "username", "verified",
}

// permissionKeys is the key set of the nested permissions object.
var permissionKeys = []string{"delete", "edit", "new", "read"}

// keysOf decodes one channel object into a generic map, so a key that is
// omitted or renamed cannot be hidden by a struct's zero value.
func keysOf(t *testing.T, raw any) map[string]any {
	t.Helper()
	object, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("channel is %T, not an object", raw)
	}
	return object
}

// assertSameKeys fails unless got holds exactly want, and no more.
func assertSameKeys(t *testing.T, what string, got map[string]any, want []string) {
	t.Helper()
	names := make([]string, 0, len(got))
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("%s keys = %v, want %v", what, names, want)
	}
}

// assertChannelObject checks the shape, and the types, of one channel object.
func assertChannelObject(t *testing.T, what string, raw any) map[string]any {
	t.Helper()
	object := keysOf(t, raw)
	assertSameKeys(t, what, object, channelKeys)

	if id, ok := object["id"].(string); !ok || id == "" {
		t.Errorf("%s id = %#v, want a non-empty string", what, object["id"])
	}
	for _, name := range []string{"type", "title", "membership_status"} {
		if _, ok := object[name].(string); !ok {
			t.Errorf("%s %s = %#v, want a string", what, name, object[name])
		}
	}
	// An absent optional string is null-or-missing inside the object; when the
	// object has the key, it must still be a string.
	for _, name := range []string{"username", "description", "avatar_url"} {
		if value, present := object[name]; present {
			if _, ok := value.(string); !ok {
				t.Errorf("%s %s = %#v, want a string", what, name, value)
			}
		}
	}
	for _, name := range []string{"avatar_available", "is_member", "is_managed", "verified", "scam", "fake"} {
		if _, ok := object[name].(bool); !ok {
			t.Errorf("%s %s = %#v, want a boolean", what, name, object[name])
		}
	}
	if value, present := object["participants_count"]; present && value != nil {
		if _, ok := value.(float64); !ok {
			t.Errorf("%s participants_count = %#v, want a number or null", what, value)
		}
	}
	permissions, ok := object["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("%s permissions = %#v, want an object", what, object["permissions"])
	}
	assertSameKeys(t, what+" permissions", permissions, permissionKeys)
	for _, name := range permissionKeys {
		if _, ok := permissions[name].(bool); !ok {
			t.Errorf("%s permissions.%s = %#v, want a boolean", what, name, permissions[name])
		}
	}
	return object
}

// channelObjects reads {data:{channel:{...}}} from a response body.
func channelObjects(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload struct {
		Data map[string]json.RawMessage `json:"data"`
		Meta map[string]any             `json:"meta"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if _, ok := payload.Meta["request_id"]; !ok {
		t.Fatalf("meta has no request_id: %s", body)
	}
	if _, ok := payload.Meta["generated_at"]; !ok {
		t.Fatalf("meta has no generated_at: %s", body)
	}
	raw, ok := payload.Data["channel"]
	if !ok {
		t.Fatalf("data has no channel: %s", body)
	}
	var channel any
	if err := json.Unmarshal(raw, &channel); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	return keysOf(t, channel)
}

func TestBothChannelDetailRoutesAnswerTheSameChannelObject(t *testing.T) {
	env, workers := channelsEnv(t, "official")

	// Telegram's live answer, for the same channel.
	workers["official"].channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "1234567890", Type: "channel", Title: "کانال نمونه", Username: "sample",
		Description: "About کانال نمونه", AvatarAvailable: true, HasPhoto: true, AvatarVersion: 77,
		IsMember: true, MembershipStatus: "member", Verified: true, Scam: true, Fake: true,
		ParticipantsCount: intPtr(1234),
		Permissions:       rpc.Permissions{Read: true},
	}}

	// The same facts as the record: what the worker reported when the channel
	// was joined, written by the same store the join route writes.
	joined, err := env.store.Upsert(context.Background(), channelstore.Channel{
		ClientKey: "official", ChannelID: 1234567890, PeerType: channelstore.PeerChannel,
		Title: "کانال نمونه", Username: "sample", Description: "About کانال نمونه",
		AvatarPhotoID: 77, AvatarAvailable: true,
		MembershipStatus: channelstore.StatusMember,
		Verified:         true, Scam: true, Fake: true, NoticesKnown: true,
		Permissions: tgapi.Permissions{Read: true}, PermissionsKnown: true,
		ParticipantsCount: intPtr(1234),
	})
	if err != nil {
		t.Fatal(err)
	}

	live := env.send(t, defaultCaller, http.MethodGet, "/api/v1/telegram/official/channels/1234567890", "")
	if live.StatusCode != http.StatusOK {
		t.Fatalf("telegram detail = %d", live.StatusCode)
	}
	local := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, joined.ID), "")
	if local.StatusCode != http.StatusOK {
		t.Fatalf("local detail = %d", local.StatusCode)
	}

	liveChannel := assertChannelObject(t, "telegram", channelObjectsOf(t, readAll(t, live)))
	localChannel := assertChannelObject(t, "local", channelObjectsOf(t, readAll(t, local)))

	// The same values, field for field: both describe this channel.
	for name := range liveChannel {
		if name == "avatar_url" || name == "permissions" {
			continue
		}
		if fmt.Sprint(liveChannel[name]) != fmt.Sprint(localChannel[name]) {
			t.Errorf("%s: telegram = %#v, local = %#v", name, liveChannel[name], localChannel[name])
		}
	}
	if fmt.Sprint(liveChannel["permissions"]) != fmt.Sprint(localChannel["permissions"]) {
		t.Errorf("permissions: telegram = %v, local = %v", liveChannel["permissions"], localChannel["permissions"])
	}
	// The avatar URL names the route that actually serves the bytes: the live
	// channel's through the Telegram route, the snapshot's through the local one.
	if got, want := liveChannel["avatar_url"], "/api/v1/telegram/official/channels/1234567890/avatar"; got != want {
		t.Errorf("telegram avatar_url = %v, want %v", got, want)
	}
	if got, want := localChannel["avatar_url"], fmt.Sprintf("%s/%d/avatar", localChannelsPath, joined.ID); got != want {
		t.Errorf("local avatar_url = %v, want %v", got, want)
	}
}

func TestLocalChannelDetailIdentifiesTheChannelByItsTelegramID(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	// The Telegram id and the local id are deliberately different numbers.
	row := env.localRow(t, "official", 555, "first", "")
	if row.ID == 555 {
		t.Skip("the local id happened to equal the Telegram id")
	}
	env.localRow(t, "official", 444, "second", "") // local id row.ID+1

	channel := channelObjectsOf(t, readAll(t, env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), "")))
	if channel["id"] != "555" {
		t.Fatalf("id = %#v, want the Telegram id as a string", channel["id"])
	}
	if channel["is_managed"] != true {
		t.Fatalf("is_managed = %#v, want true for a managed channel", channel["is_managed"])
	}
	// The path still addresses the record by the local id.
	if resp := env.send(t, defaultCaller, http.MethodGet, localChannelsPath+"/555", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the Telegram id as a path id = %d, want 404", resp.StatusCode)
	}
}

func TestLocalChannelDetailUsesLivePermissionsForUnknownSnapshot(t *testing.T) {
	env, workers := channelsEnv(t, "official", "the-cat")
	row := env.localRow(t, "the-cat", 98, "Stored title", "linkdooni")
	// The other server knows the same numeric remote id but a different role.
	env.localRow(t, "official", 98, "Different channel", "other")
	workers["the-cat"].channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Title: "Live title", IsMember: true, MembershipStatus: "member",
		Permissions: rpc.Permissions{New: true, Edit: true, Delete: true, Read: true},
	}}
	workers["official"].channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Title: "Other connection", IsMember: true, MembershipStatus: "member",
		Permissions: rpc.Permissions{Read: true},
	}}
	officialCalls := workers["official"].callCount()
	catCalls := workers["the-cat"].callCount()

	resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("local detail = %d: %s", resp.StatusCode, readAll(t, resp))
	}
	channel := channelObjectsOf(t, readAll(t, resp))
	if channel["id"] != "98" || channel["title"] != "Stored title" {
		t.Errorf("local metadata = id %v title %v; want remote id 98 and stored title", channel["id"], channel["title"])
	}
	permissions := channel["permissions"].(map[string]any)
	for name, value := range map[string]bool{"new": true, "edit": true, "delete": true, "read": true} {
		if permissions[name] != value {
			t.Errorf("live permission %s = %v, want %v; all permissions = %+v", name, permissions[name], value, permissions)
		}
	}
	if got := workers["the-cat"].callCount(); got != catCalls+1 {
		t.Errorf("stored connection worker calls = %d, want %d", got, catCalls+1)
	}
	if got := workers["official"].callCount(); got != officialCalls {
		t.Errorf("other connection worker calls = %d, want unchanged %d", got, officialCalls)
	}
	workers["the-cat"].mu.Lock()
	ref := workers["the-cat"].lastRef
	workers["the-cat"].mu.Unlock()
	if ref.ChannelID != 98 {
		t.Errorf("worker remote channel id = %d, want 98 (local row id %d must not be used)", ref.ChannelID, row.ID)
	}
	stored, found, err := env.store.GetByID(context.Background(), row.ID)
	if err != nil || !found {
		t.Fatalf("stored row found=%v err=%v", found, err)
	}
	if stored.PermissionsKnown || stored.Permissions != (tgapi.Permissions{}) {
		t.Errorf("GET mutated stored permission snapshot: %+v known=%v", stored.Permissions, stored.PermissionsKnown)
	}
	if !stored.UpdatedAt.Equal(row.UpdatedAt) || !stored.LastSyncedAt.Equal(row.LastSyncedAt) {
		t.Errorf("GET changed stored timestamps: updated %v → %v, synced %v → %v",
			row.UpdatedAt, stored.UpdatedAt, row.LastSyncedAt, stored.LastSyncedAt)
	}
}

func TestLocalChannelDetailIncludesMappedClientKeyForExplorerDestination(t *testing.T) {
	env, workers := channelsEnv(t, "cat", "official")
	// Match the reported local identity: local row 2 maps to remote channel 98
	// on the Cat connection. Detail must expose the existing mapping, not ask
	// the frontend to infer it from the server filter or username.
	env.localRow(t, "official", 17, "Other", "")
	row := env.localRow(t, "cat", 98, "لینکدونی", "linkdooni")
	workers["cat"].channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Title: "لینکدونی", IsMember: true, MembershipStatus: "member",
		Permissions: rpc.Permissions{New: true, Edit: true, Delete: true, Read: true},
	}}

	resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("local detail = %d: %s", resp.StatusCode, readAll(t, resp))
	}
	var body struct {
		Data struct {
			ClientKey string         `json:"client_key"`
			Channel   map[string]any `json:"channel"`
		} `json:"data"`
	}
	decodeBody(t, resp, &body)
	if body.Data.ClientKey != "cat" {
		t.Errorf("data.client_key = %q, want authoritative local mapping %q", body.Data.ClientKey, "cat")
	}
	if body.Data.Channel["id"] != "98" {
		t.Errorf("data.channel.id = %v, want remote channel id 98", body.Data.Channel["id"])
	}
}

func TestLocalChannelDetailFailsWhenLivePermissionsUnavailable(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 98, "Stored title", "linkdooni")
	workers["official"].channelE = rpc.ErrUnavailable

	resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("local detail status = %d, want 503 when current rights are unavailable: %s", resp.StatusCode, readAll(t, resp))
	}
	if body := readAll(t, resp); !strings.Contains(body, `"code":"worker_unavailable"`) {
		t.Fatalf("error = %s, want worker_unavailable", body)
	}
}

func TestLocalChannelDetailKeepsStoredMetadataAndUsesLivePermissions(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 900, "Stored title", "")
	// The live metadata disagrees with the record. This endpoint keeps the local
	// metadata but uses the current Telegram permission result.
	workers["official"].channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "900", Title: "Live title", IsMember: true, MembershipStatus: "member",
		Permissions: rpc.Permissions{Read: true},
	}}
	before := workers["official"].callCount()

	channel := channelObjectsOf(t, readAll(t,
		env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), "")))
	if channel["title"] != "Stored title" {
		t.Fatalf("title = %v, want the stored snapshot", channel["title"])
	}
	if got := workers["official"].callCount(); got != before+1 {
		t.Fatalf("the local detail route made %d worker calls, want one", got-before)
	}
	if channel["is_member"] != true || channel["membership_status"] != "member" {
		t.Fatalf("membership = %v / %v", channel["is_member"], channel["membership_status"])
	}
	permissions := channel["permissions"].(map[string]any)
	if permissions["read"] != true || permissions["new"] != false || permissions["edit"] != false || permissions["delete"] != false {
		t.Fatalf("permissions = %+v, want live read-only member", permissions)
	}

	// A row that never confirmed membership says so, rather than inventing it.
	pending, err := env.store.Upsert(context.Background(), channelstore.Channel{
		ClientKey: "official", ChannelID: 901, PeerType: channelstore.PeerChannel,
		Title: "Pending", MembershipStatus: channelstore.StatusJoinPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	workers["official"].channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "901", Type: "channel", MembershipStatus: "not_member",
	}}
	got := channelObjectsOf(t, readAll(t,
		env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, pending.ID), "")))
	if got["is_member"] != false || got["membership_status"] != "join_pending" {
		t.Fatalf("pending membership = %v / %v", got["is_member"], got["membership_status"])
	}
	// A join request that was never answered has no Telegram permissions.
	perms := got["permissions"].(map[string]any)
	for name, value := range perms {
		if value != false {
			t.Errorf("pending permissions.%s = %v, want false", name, value)
		}
	}
}

func TestLocalChannelDetailReflectsPermissionChangesOnEachRequest(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 901, "Changes", "")
	cases := []struct {
		name string
		live rpc.Permissions
		want rpc.Permissions
	}{
		{name: "member", live: rpc.Permissions{Read: true}, want: rpc.Permissions{Read: true}},
		{name: "promoted", live: rpc.Permissions{New: true, Edit: true, Delete: true, Read: true}, want: rpc.Permissions{New: true, Edit: true, Delete: true, Read: true}},
		{name: "demoted", live: rpc.Permissions{}, want: rpc.Permissions{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workers["official"].channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
				ID: "901", Type: "channel", IsMember: tc.live.Read,
				MembershipStatus: "member", Permissions: tc.live,
			}}
			got := channelObjectsOf(t, readAll(t,
				env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), "")))
			permissions := got["permissions"].(map[string]any)
			for name, want := range map[string]bool{
				"new": tc.want.New, "edit": tc.want.Edit,
				"delete": tc.want.Delete, "read": tc.want.Read,
			} {
				if permissions[name] != want {
					t.Errorf("permissions.%s = %v, want %v", name, permissions[name], want)
				}
			}
		})
	}
}

func TestLocalChannelDetailKeepsWhatTelegramDidNotReport(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row, err := env.store.Upsert(context.Background(), channelstore.Channel{
		ClientKey: "official", ChannelID: 9012, PeerType: channelstore.PeerChannel,
		Title: "Quiet", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}

	channel := channelObjectsOf(t, readAll(t,
		env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), "")))
	if value, present := channel["participants_count"]; present && value != nil {
		t.Fatalf("participants_count = %v, want null", value)
	}
	// No photo version was ever reported, so there is no avatar and no URL to
	// claim one at.
	if channel["avatar_available"] != false {
		t.Fatalf("avatar_available = %v, want false", channel["avatar_available"])
	}
	if _, present := channel["avatar_url"]; present {
		t.Fatalf("avatar_url = %v, want it absent", channel["avatar_url"])
	}
}

func channelObjectsOf(t *testing.T, body string) map[string]any {
	t.Helper()
	return channelObjects(t, []byte(body))
}

func TestTheLocalChannelListKeepsItsOwnListingContract(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	env.localRow(t, "official", 1, "News", "")

	page := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet, localChannelsPath, ""))
	if len(page.Data.Channels) != 1 {
		t.Fatalf("list = %+v", page.Data.Channels)
	}
	// The list is still the record listing: it names the local id and the
	// client, which the detail contract does not.
	listed := page.Data.Channels[0]
	if listed.ID == 0 || listed.ClientKey != "official" || listed.ChannelID != 1 {
		t.Fatalf("listed = %+v", listed)
	}
}

// TestTheJoinedChannelReadsBackThroughBothRoutes is the whole chain from one
// route to the other: a join records what Telegram reported, and the local
// detail answers the same channel object from that record, field for field.
func TestTheJoinedChannelReadsBackThroughBothRoutes(t *testing.T) {
	worker := readyWorker()
	live := rpc.ChannelInfo{
		ID: "500", Type: "group", Title: "Studio", Username: "studio",
		Description: "About Studio", AvatarAvailable: true, HasPhoto: true,
		AvatarVersion: 12, IsMember: true, MembershipStatus: "member",
		Verified: true, Scam: true, ParticipantsCount: intPtr(42),
		Permissions: rpc.Permissions{New: true, Read: true},
	}
	worker.join = rpc.JoinResult{Channel: live, Outcome: rpc.OutcomeJoined}
	worker.channel = rpc.ChannelResult{Channel: live}
	env, workers := channelsEnv(t, "official")
	workers["official"].join = worker.join
	workers["official"].channel = worker.channel

	if resp := env.send(t, defaultCaller, http.MethodPost, "/api/v1/telegram/official/channels/500/join", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d", resp.StatusCode)
	}
	joined, ok, err := env.store.Get(context.Background(), "official", 500)
	if err != nil || !ok {
		t.Fatalf("the join recorded no channel: ok=%v err=%v", ok, err)
	}
	localID := joined.ID

	liveChannel := assertChannelObject(t, "telegram",
		channelObjectsOf(t, readAll(t, env.send(t, defaultCaller, http.MethodGet, "/api/v1/telegram/official/channels/500", ""))))
	localChannel := assertChannelObject(t, "local",
		channelObjectsOf(t, readAll(t, env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, localID), ""))))

	for name := range liveChannel {
		if name == "avatar_url" {
			continue
		}
		if fmt.Sprint(liveChannel[name]) != fmt.Sprint(localChannel[name]) {
			t.Errorf("%s: telegram = %#v, local = %#v", name, liveChannel[name], localChannel[name])
		}
	}
	if localChannel["avatar_url"] != fmt.Sprintf("%s/%d/avatar", localChannelsPath, localID) {
		t.Errorf("local avatar_url = %v", localChannel["avatar_url"])
	}
}
