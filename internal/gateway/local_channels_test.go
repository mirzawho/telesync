package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

// The local channel API addresses a channel by its local row id and reaches
// Telegram through the worker of the client the row names.

// channelsEnv is the gateway over a real store, signed in as the default
// administrator: it lists every record through the global channel view, and
// each test grants delete where a removal is meant to succeed.
func channelsEnv(t *testing.T, workers ...string) (*accountEnv, map[string]*fakeWorker) {
	t.Helper()
	reg := registry{}
	fakes := map[string]*fakeWorker{}
	for _, key := range workers {
		w := readyWorker()
		reg[key] = w
		fakes[key] = w
	}
	env := newAccountEnv(t, reg)
	env.signedIn = true
	return env, fakes
}

type localChannelJSON struct {
	ID                int64  `json:"id"`
	ClientKey         string `json:"client_key"`
	AccountID         string `json:"account_id"`
	ChannelID         int64  `json:"channel_id"`
	Type              string `json:"type"`
	Title             string `json:"title"`
	Username          string `json:"username"`
	Description       string `json:"description"`
	AvatarAvailable   bool   `json:"avatar_available"`
	AvatarURL         string `json:"avatar_url"`
	MembershipStatus  string `json:"membership_status"`
	ParticipantsCount *int   `json:"participants_count"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	LastSyncedAt      string `json:"last_synced_at"`
}

type localChannelPage struct {
	Data struct {
		Channels []localChannelJSON `json:"channels"`
	} `json:"data"`
	Meta struct {
		Page       int  `json:"page"`
		PerPage    int  `json:"per_page"`
		Total      int  `json:"total"`
		TotalPages int  `json:"total_pages"`
		HasMore    bool `json:"has_more"`
	} `json:"meta"`
}

// channelDetailJSON mirrors the canonical channel detail body those tests read
// by hand; internal/gateway's contract test checks the full shape.
type channelDetailJSON struct {
	Data struct {
		Channel struct {
			ID                string `json:"id"`
			Type              string `json:"type"`
			Title             string `json:"title"`
			Username          string `json:"username"`
			Description       string `json:"description"`
			AvatarAvailable   bool   `json:"avatar_available"`
			AvatarURL         string `json:"avatar_url"`
			IsMember          bool   `json:"is_member"`
			IsManaged         bool   `json:"is_managed"`
			MembershipStatus  string `json:"membership_status"`
			Verified          bool   `json:"verified"`
			Scam              bool   `json:"scam"`
			Fake              bool   `json:"fake"`
			ParticipantsCount *int   `json:"participants_count"`
		} `json:"channel"`
	} `json:"data"`
}

const localChannelsPath = "/api/v1/local/channels"

func (e *accountEnv) localRow(t *testing.T, key string, telegramID int64, title, username string) channelstore.Channel {
	t.Helper()
	account, ok := e.testEnv.account[key]
	if !ok {
		t.Fatalf("no fixture account for server key %q", key)
	}
	row, err := e.store.RegisterForAccount(context.Background(), channelstore.Channel{
		AccountID:         account.ID,
		ClientKey:         key,
		ChannelID:         telegramID,
		PeerType:          channelstore.PeerChannel,
		Title:             title,
		Username:          username,
		Description:       "About " + title,
		AvatarPhotoID:     77,
		MembershipStatus:  channelstore.StatusMember,
		ParticipantsCount: intPtr(1234),
	}, channelstore.WithOwner(account.OwnerUserID))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	return row
}

func intPtr(v int) *int { return &v }

func TestLocalChannelsListPaginatesSearchesAndReportsTotals(t *testing.T) {
	env, _ := channelsEnv(t, "official", "the-cat")
	for i := int64(1); i <= 5; i++ {
		env.localRow(t, "official", i, fmt.Sprintf("News %d", i), "")
	}
	env.localRow(t, "the-cat", 900, "Studio", "studio")

	page := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet, localChannelsPath+"?page=2&per_page=4", ""))
	if page.Meta.Total != 6 || page.Meta.Page != 2 || page.Meta.PerPage != 4 || page.Meta.TotalPages != 2 || page.Meta.HasMore {
		t.Fatalf("meta = %+v", page.Meta)
	}
	if len(page.Data.Channels) != 2 {
		t.Fatalf("page 2 = %d rows, want 2", len(page.Data.Channels))
	}

	found := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet, localChannelsPath+"?q=studio", ""))
	if found.Meta.Total != 1 || len(found.Data.Channels) != 1 {
		t.Fatalf("q=studio = %+v", found)
	}
	ch := found.Data.Channels[0]
	if ch.ClientKey != "the-cat" || ch.ChannelID != 900 {
		t.Fatalf("channel = %+v", ch)
	}
}

func TestLocalChannelListUnionsOnlyOwnedAccountsAndFiltersByAccount(t *testing.T) {
	env, _ := channelsEnv(t)
	ctx := context.Background()
	nextRemoteID := int64(10)
	newAccount := func(owner int64, status string) channelstore.Account {
		t.Helper()
		id, err := accountid.New()
		if err != nil {
			t.Fatal(err)
		}
		a, err := env.store.Create(ctx, channelstore.Account{
			ID: id, OwnerUserID: owner, ServerKey: "official", NetworkType: channelstore.NetworkTelegram,
			RemoteUserID: nextRemoteID, Status: status,
		})
		nextRemoteID++
		if err != nil {
			t.Fatalf("create account: %v", err)
		}
		return a
	}
	register := func(account channelstore.Account, title string) channelstore.Channel {
		t.Helper()
		row, err := env.store.RegisterForAccount(ctx, channelstore.Channel{
			AccountID: account.ID, ClientKey: account.ServerKey, ChannelID: 777,
			PeerType: channelstore.PeerChannel, Title: title, MembershipStatus: channelstore.StatusMember,
		}, channelstore.WithOwner(account.OwnerUserID))
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		return row
	}
	first := register(newAccount(env.defaults.user.ID, channelstore.AccountStatusConnected), "Shared A")
	second := register(newAccount(env.defaults.user.ID, channelstore.AccountStatusDisconnected), "Shared B")
	foreign := env.account(t, "local-list-foreign", channelstore.GlobalPermissions()...)
	foreignRow := register(newAccount(foreign.user.ID, channelstore.AccountStatusConnected), "Foreign")

	all := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet, localChannelsPath, ""))
	if all.Meta.Total != 2 || len(all.Data.Channels) != 2 {
		t.Fatalf("all owned channels = %d/%d, want 2/2", all.Meta.Total, len(all.Data.Channels))
	}
	ids := map[int64]bool{}
	for _, ch := range all.Data.Channels {
		ids[ch.ID] = true
	}
	if !ids[first.ID] || !ids[second.ID] {
		t.Fatalf("same remote channel registrations collapsed: got local ids %v", ids)
	}

	filtered := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet,
		localChannelsPath+"?account_ids="+first.AccountID+","+second.AccountID, ""))
	if filtered.Meta.Total != 2 || len(filtered.Data.Channels) != 2 {
		t.Fatalf("account-filtered channels = %+v", filtered)
	}
	for _, ch := range filtered.Data.Channels {
		if ch.AccountID != first.AccountID && ch.AccountID != second.AccountID {
			t.Fatalf("account filter returned an unselected account: %+v", ch)
		}
	}
	foreignFilter := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet,
		localChannelsPath+"?account_ids="+foreignRow.AccountID, ""))
	if foreignFilter.Meta.Total != 0 || len(foreignFilter.Data.Channels) != 0 {
		t.Fatalf("foreign account filter exposed rows: %+v", foreignFilter)
	}
	unknownID, err := accountid.New()
	if err != nil {
		t.Fatal(err)
	}
	unknownFilter := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet,
		localChannelsPath+"?account_ids="+unknownID, ""))
	if unknownFilter.Meta.Total != 0 || len(unknownFilter.Data.Channels) != 0 {
		t.Fatalf("unknown account filter exposed rows: %+v", unknownFilter)
	}
}

func TestLocalChannelDetailAndHistoryResolveStoredAccountAndEnforceOwner(t *testing.T) {
	env, _ := channelsEnv(t)
	ctx := context.Background()
	makeRegistered := func(owner channelstore.User, remoteID int64) (channelstore.Channel, *fakeWorker) {
		t.Helper()
		id, err := accountid.New()
		if err != nil {
			t.Fatal(err)
		}
		account, err := env.store.Create(ctx, channelstore.Account{
			ID: id, OwnerUserID: owner.ID, ServerKey: "official", NetworkType: channelstore.NetworkTelegram,
			RemoteUserID: remoteID, Status: channelstore.AccountStatusDisconnected,
		})
		if err != nil {
			t.Fatalf("create account: %v", err)
		}
		row, err := env.store.RegisterForAccount(ctx, channelstore.Channel{
			AccountID: account.ID, ClientKey: "official", ChannelID: 900,
			PeerType: channelstore.PeerChannel, Title: "Stored", MembershipStatus: channelstore.StatusMember,
		}, channelstore.WithOwner(owner.ID))
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		worker := readyWorker()
		env.workers[account.ID] = worker
		return row, worker
	}
	owned, ownWorker := makeRegistered(env.defaults.user, 901)
	ownedSecond, secondWorker := makeRegistered(env.defaults.user, 903)
	foreign, foreignWorker := makeRegistered(env.account(t, "local-detail-foreign", channelstore.GlobalPermissions()...).user, 902)

	if resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d?account_ids=wrong", localChannelsPath, owned.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("detail must use the stored account, not a query filter: %d", resp.StatusCode)
	}
	if ownWorker.callCount() != 1 {
		t.Fatalf("stored account worker calls = %d, want 1", ownWorker.callCount())
	}
	if resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d/messages", localChannelsPath, ownedSecond.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("owned history = %d, want 200", resp.StatusCode)
	}
	if secondWorker.lastChan != 900 || ownWorker.callCount() != 1 {
		t.Fatalf("history did not use the row's stored account: first calls=%d second channel=%d",
			ownWorker.callCount(), secondWorker.lastChan)
	}
	if resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d/messages", localChannelsPath, foreign.ID), ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign local history = %d, want 404", resp.StatusCode)
	}
	if foreignWorker.callCount() != 0 {
		t.Fatalf("foreign account worker was called %d times", foreignWorker.callCount())
	}
}

func TestLocalChannelGetReturnsTheStoredSnapshotInTheCanonicalShape(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row := env.localRow(t, "official", 1234567890, "کانال نمونه", "sample")

	got := decode[channelDetailJSON](t, env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), ""))
	c := got.Data.Channel
	// The object's id is Telegram's; the path named the record by its local id.
	if c.ID != "1234567890" {
		t.Fatalf("id = %q, want the Telegram id as a string", c.ID)
	}
	if c.Title != "کانال نمونه" || c.Username != "sample" {
		t.Fatalf("channel = %+v", c)
	}
	if c.Type != "channel" || c.MembershipStatus != "member" || !c.IsMember || !c.IsManaged {
		t.Fatalf("channel = %+v", c)
	}
	if c.AvatarURL != fmt.Sprintf("%s/%d/avatar", localChannelsPath, row.ID) {
		t.Fatalf("avatar_url = %q", c.AvatarURL)
	}
	if c.ParticipantsCount == nil || *c.ParticipantsCount != 1234 {
		t.Fatalf("participants = %v", c.ParticipantsCount)
	}
	// The record listing still names the stored identity.
	list := decode[localChannelPage](t, env.send(t, defaultCaller, http.MethodGet, localChannelsPath, ""))
	if list.Data.Channels[0].ID != row.ID || list.Data.Channels[0].ClientKey != "official" {
		t.Fatalf("list row = %+v", list.Data.Channels[0])
	}
}

func TestLocalChannelGetDoesNotAcceptTheTelegramID(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	env.localRow(t, "official", 500, "first", "")
	env.localRow(t, "official", 400, "second", "") // local id 2

	// Local id 1 is the channel whose Telegram id is 500.
	got := decode[channelDetailJSON](t, env.send(t, defaultCaller, http.MethodGet, localChannelsPath+"/1", ""))
	if got.Data.Channel.ID != "500" {
		t.Fatalf("local id 1 = Telegram id %q, want 500", got.Data.Channel.ID)
	}
	// The Telegram id, used as a local id, is not a row.
	if resp := env.send(t, defaultCaller, http.MethodGet, localChannelsPath+"/500", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET with the Telegram id = %d, want 404", resp.StatusCode)
	}
}

func TestLocalChannelRoutesValidateIDsAndMethods(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, localChannelsPath + "/0", http.StatusBadRequest},
		{http.MethodGet, localChannelsPath + "/abc", http.StatusBadRequest},
		{http.MethodGet, localChannelsPath + "/999", http.StatusNotFound},
		{http.MethodGet, localChannelsPath + "?page=0", http.StatusBadRequest},
		{http.MethodGet, localChannelsPath + "?per_page=1000", http.StatusBadRequest},
		{http.MethodGet, localChannelsPath + "?sort=title", http.StatusBadRequest},
		{http.MethodPut, localChannelsPath + "/1", http.StatusMethodNotAllowed},
		{http.MethodPost, localChannelsPath, http.StatusMethodNotAllowed},
		{http.MethodGet, localChannelsPath + "/1/nothing", http.StatusNotFound},
	}
	for _, tc := range cases {
		resp := env.send(t, defaultCaller, tc.method, tc.path, "")
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}

func TestLocalChannelMessagesComeFromTheChannelsClientAndAreNotStored(t *testing.T) {
	env, workers := channelsEnv(t, "official", "the-cat")
	row := env.localRow(t, "the-cat", 987654321, "Studio", "studio")
	workers["the-cat"].messages = rpc.MessagePage{
		Messages: []rpc.Message{{ID: 7, Text: "hello"}},
		Page:     rpc.PageInfo{Page: 3, PerPage: 5, HasMore: true, NextPage: 4},
	}

	var body struct {
		Data struct {
			Messages []struct {
				ID   int    `json:"id"`
				Text string `json:"text"`
			} `json:"messages"`
		} `json:"data"`
		Meta struct {
			Page    int  `json:"page"`
			PerPage int  `json:"per_page"`
			HasMore bool `json:"has_more"`
		} `json:"meta"`
	}
	resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d/messages?page=3&per_page=5", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("messages = %d", resp.StatusCode)
	}
	decodeBody(t, resp, &body)
	if len(body.Data.Messages) != 1 || body.Data.Messages[0].ID != 7 {
		t.Fatalf("messages = %+v", body.Data.Messages)
	}
	if body.Meta.Page != 3 || body.Meta.PerPage != 5 || !body.Meta.HasMore {
		t.Fatalf("meta = %+v", body.Meta)
	}
	// The Telegram id of the row, on the row's client, and nothing else.
	if workers["the-cat"].lastChan != 987654321 || workers["the-cat"].lastPage != 3 {
		t.Fatalf("worker saw channel=%d page=%d", workers["the-cat"].lastChan, workers["the-cat"].lastPage)
	}
	if workers["official"].callCount() != 0 {
		t.Fatal("the row's client is the-only one that may be asked")
	}

	// Nothing from Telegram was written to the database.
	rows, total, err := env.store.ListChannels(context.Background(), channelstore.ChannelListQuery{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].ID != row.ID {
		t.Fatalf("store = %+v (total %d)", rows, total)
	}
}

func TestLocalChannelMessagesReportUpstreamFailures(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")

	workers["official"].msgE = rpc.ErrNotConnected
	resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d/messages", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no connection = %d, want 503", resp.StatusCode)
	}
	workers["official"].msgE = rpc.ErrChannelNotFound
	resp = env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d/messages", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("channel gone upstream = %d, want 404", resp.StatusCode)
	}
	if resp := env.send(t, defaultCaller, http.MethodGet, localChannelsPath+"/999/messages", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown row = %d, want 404", resp.StatusCode)
	}
}

func TestLocalChannelAvatarUsesTheRowsClient(t *testing.T) {
	env, workers := channelsEnv(t, "official", "the-cat")
	row := env.localRow(t, "the-cat", 987654321, "Studio", "")
	workers["the-cat"].avatar = rpc.AvatarFile{MimeType: "image/jpeg", PhotoID: 9, Data: []byte("\xff\xd8jpeg")}

	resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d/avatar", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("avatar = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type = %q", ct)
	}
	if workers["the-cat"].lastRef.ChannelID != 987654321 {
		t.Fatalf("avatar ref = %+v", workers["the-cat"].lastRef)
	}
	if workers["official"].callCount() != 0 {
		t.Fatal("another client must not answer for this row")
	}

	workers["the-cat"].avatarE = rpc.ErrNoAvatar
	if resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d/avatar", localChannelsPath, row.ID), ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no avatar = %d, want 404", resp.StatusCode)
	}
}

func TestLocalChannelLeaveWithoutABodyRemovesOnlyTheRecord(t *testing.T) {
	for _, body := range []struct{ name, contentType, body string }{
		{"no body", "", ""},
		{"empty object", "application/json", `{}`},
		{"explicit false", "application/json", `{"telegram": false}`},
	} {
		t.Run(body.name, func(t *testing.T) {
			env, workers := channelsEnv(t, "official")
			row := env.localRow(t, "official", 500, "first", "")
			env.grantDeleteOn(t, row.ID)

			resp := env.sendBody(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), body.contentType, body.body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("leave = %d", resp.StatusCode)
			}
			var out struct {
				Data struct {
					LocalDeleted bool `json:"local_deleted"`
					TelegramLeft bool `json:"telegram_left"`
				} `json:"data"`
			}
			decodeBody(t, resp, &out)
			if !out.Data.LocalDeleted || out.Data.TelegramLeft {
				t.Fatalf("data = %+v", out.Data)
			}
			if workers["official"].leaveCalls != 0 {
				t.Fatal("Telegram must not be asked to leave")
			}
			if _, found, err := env.store.GetByID(context.Background(), row.ID); err != nil || found {
				t.Fatalf("row still present: found=%v err=%v", found, err)
			}
			if resp := env.sendBody(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), "", ""); resp.StatusCode != http.StatusNotFound {
				t.Fatalf("second leave = %d, want 404", resp.StatusCode)
			}
		})
	}
}

func TestLocalChannelLeaveWithTelegramCallsTelegramBeforeDeleting(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	env.grantDeleteOn(t, row.ID)

	resp := env.send(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID),
		env.leaveBody(t, map[string]any{"telegram": true}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("leave = %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			LocalDeleted bool `json:"local_deleted"`
			TelegramLeft bool `json:"telegram_left"`
		} `json:"data"`
	}
	decodeBody(t, resp, &out)
	if !out.Data.LocalDeleted || !out.Data.TelegramLeft {
		t.Fatalf("data = %+v", out.Data)
	}
	if len(workers["official"].leaveIDs) != 1 || workers["official"].leaveIDs[0] != 500 {
		t.Fatalf("leave calls = %+v", workers["official"].leaveIDs)
	}
	if _, found, err := env.store.GetByID(context.Background(), row.ID); err != nil || found {
		t.Fatalf("row should be gone after a successful leave: found=%v err=%v", found, err)
	}
}

func TestLocalChannelLeaveKeepsTheRowWhenTelegramFails(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	env.grantDeleteOn(t, row.ID)
	workers["official"].leaveE = rpc.ErrChannelNotFound

	resp := env.send(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID),
		env.leaveBody(t, map[string]any{"telegram": true}))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("leave = %d, want 404", resp.StatusCode)
	}
	if _, found, err := env.store.GetByID(context.Background(), row.ID); err != nil || !found {
		t.Fatalf("the row must survive a failed leave: found=%v err=%v", found, err)
	}
}

func TestLocalChannelLeaveRejectsAnUnknownClientWithoutDeleting(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row, err := env.store.Upsert(context.Background(), channelstore.Channel{
		ClientKey: "gone", ChannelID: 500, PeerType: channelstore.PeerChannel,
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.grantDeleteOn(t, row.ID)

	resp := env.send(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID),
		env.leaveBody(t, map[string]any{"telegram": true}))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("leave = %d, want 404", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), row.ID); !found {
		t.Fatal("the row must survive")
	}
}

func TestLocalChannelLeaveValidatesItsBody(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	env.grantDeleteOn(t, row.ID)

	cases := []struct {
		name        string
		contentType string
		body        string
		want        int
	}{
		{"invalid json", "application/json", `{"telegram": tru}`, http.StatusBadRequest},
		{"wrong type", "application/json", `{"telegram": "yes"}`, http.StatusBadRequest},
		{"null", "application/json", `{"telegram": null}`, http.StatusBadRequest},
		{"unknown field", "application/json", `{"telegram": true, "force": true}`, http.StatusBadRequest},
		{"trailing value", "application/json", `{"telegram": false} {}`, http.StatusBadRequest},
		{"not json type", "text/plain", `{"telegram": false}`, http.StatusUnsupportedMediaType},
		{"array", "application/json", `[false]`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		req := newRawRequest(http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), tc.body)
		req.Header.Set("Authorization", "Bearer "+env.defaults.token)
		req.Header.Set("Content-Type", tc.contentType)
		resp := env.doRaw(t, req)
		if resp.StatusCode != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
	if _, found, _ := env.store.GetByID(context.Background(), row.ID); !found {
		t.Fatal("a rejected leave must not delete the row")
	}
}

func TestLocalChannelRoutesRequireAuthAndPerChannelAccess(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	other := env.localRow(t, "official", 600, "second", "")

	for _, path := range []string{
		localChannelsPath,
		fmt.Sprintf("%s/%d", localChannelsPath, row.ID),
		fmt.Sprintf("%s/%d/messages", localChannelsPath, row.ID),
		fmt.Sprintf("%s/%d/avatar", localChannelsPath, row.ID),
	} {
		if resp := env.send(t, "", http.MethodGet, path, ""); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401", path, resp.StatusCode)
		}
	}

	who := env.account(t, "alice") // no permissions at all
	if resp := env.send(t, who.token, http.MethodGet, localChannelsPath, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("list as a caller with no channels = %d, want an empty 200", resp.StatusCode)
	}
	for _, path := range []string{
		fmt.Sprintf("%s/%d", localChannelsPath, row.ID),
		fmt.Sprintf("%s/%d/messages", localChannelsPath, row.ID),
		fmt.Sprintf("%s/%d/avatar", localChannelsPath, row.ID),
	} {
		if resp := env.send(t, who.token, http.MethodGet, path, ""); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s as a caller without a grant = %d, want 403", path, resp.StatusCode)
		}
	}

	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: row.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
	}})
	if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, row.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("granted channel = %d, want 200", resp.StatusCode)
	}
	page := decode[localChannelPage](t, env.send(t, who.token, http.MethodGet, localChannelsPath, ""))
	if page.Meta.Total != 1 || len(page.Data.Channels) != 1 || page.Data.Channels[0].ID != row.ID {
		t.Fatalf("list = %+v", page)
	}
	if other.ID == row.ID {
		t.Fatal("the two rows must differ")
	}

	// The removal needs the per-channel delete right, which a view-only grant
	// does not carry.
	if resp := env.send(t, who.token, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), `{"telegram": false}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("leave without the delete right = %d, want 403", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), row.ID); !found {
		t.Fatal("a forbidden leave must not delete the row")
	}
}

func TestManageChannelsRemovesARecordAndStaysInsideItsSelection(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	everything := env.localRow(t, "official", 500, "every", "")
	picked := env.localRow(t, "official", 501, "picked", "")
	unpicked := env.localRow(t, "official", 502, "unpicked", "")
	leave := func(who identity, row channelstore.Channel) *http.Response {
		return env.send(t, who.token, http.MethodPost,
			fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), `{"telegram": false}`)
	}

	// The panel's "manage channels" is the global channels.create with
	// channels.view and no channel picked: it removes any record.
	manager := env.account(t, "manager", channelstore.PermissionChannelsView, channelstore.PermissionChannelsCreate)
	if resp := leave(manager, everything); resp.StatusCode != http.StatusOK {
		t.Fatalf("a manage channels account leaving a channel = %d, want 200", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), everything.ID); found {
		t.Fatal("the record should be gone")
	}

	// The same permission, scoped to one channel, reaches only that channel.
	// The dialog stores a selection as the picked channel's view grant plus an
	// explicit write deny on the rest, so it is a real restriction: it survives
	// the records it names and no channel added later slips past it.
	scoped := env.account(t, "scoped", channelstore.PermissionChannelsView, channelstore.PermissionChannelsCreate)
	env.grantChannels(t, scoped,
		channelstore.ResourcePermissions{ResourceID: picked.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		}},
		channelstore.ResourcePermissions{ResourceID: unpicked.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionUpdate, Effect: channelstore.EffectDeny},
			{Action: channelstore.ActionDelete, Effect: channelstore.EffectDeny},
		}},
	)
	if resp := leave(scoped, picked); resp.StatusCode != http.StatusOK {
		t.Fatalf("the scoped manager leaving its own channel = %d, want 200", resp.StatusCode)
	}
	if resp := leave(scoped, unpicked); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the scoped manager leaving another channel = %d, want 403", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), unpicked.ID); !found {
		t.Fatal("a refused leave must not delete the row")
	}

	// A channel recorded after the selection is outside it, so the scoped
	// manager cannot take that one either.
	late := env.localRow(t, "official", 503, "late", "")
	if resp := leave(scoped, late); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the scoped manager leaving a later channel = %d, want 403", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), late.ID); !found {
		t.Fatal("a refused leave must not delete the row")
	}
}

func TestLocalChannelLeaveReadsNoCredentialFromTheBody(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	who := env.account(t, "alice")
	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: row.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
	}})

	// The body is read strictly: a credential sent there is an unknown field,
	// and the header remains the only credential this route takes.
	body := fmt.Sprintf(`{"telegram": false, "token": %q}`, who.token)
	if resp := env.send(t, who.token, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), body); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("leave with a token in the body = %d, want 400", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), row.ID); !found {
		t.Fatal("a refused leave must not delete the row")
	}
	if resp := env.send(t, who.token, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), `{"telegram": false}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("leave with the header's token = %d", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), row.ID); found {
		t.Fatal("the row should be gone")
	}
}

func TestLocalChannelLeaveRefusesTheOperatorToken(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")

	// The operator token authenticates no account, and only an account may
	// remove a record; the removal is refused and the record stays.
	resp := env.send(t, operatorToken, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), `{"telegram": false}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("leave as the operator = %d, want 403", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), row.ID); !found {
		t.Fatal("the operator's leave must not delete the row")
	}

	// A request with no credential at all is not authenticated, whatever its
	// body says.
	req := newRawRequest(http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), `{"telegram": false}`)
	req.Header.Set("Content-Type", "application/json")
	if resp := env.doRaw(t, req); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("leave without a credential = %d, want 401", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), row.ID); !found {
		t.Fatal("a refused leave must not delete the row")
	}
}

func TestLocalChannelLeaveLeavesTheCallersStoredGrantsAlone(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	ungranted := env.localRow(t, "official", 200, "first", "")
	other := env.localRow(t, "official", 600, "second", "")

	// A caller holding only a per-channel view grant, so only that record is
	// visible to it and its own session can be checked directly.
	who := env.account(t, "alice")
	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: other.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
	}})
	if resp := env.send(t, who.token, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, other.ID), `{}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("leave = %d", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), other.ID); found {
		t.Fatal("the record was not removed")
	}
	// The caller's session is what it was: the leave widened nothing that
	// outlives the request, so the record it was never granted stays out of
	// reach.
	if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, ungranted.ID), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unassigned record = %d, want 403", resp.StatusCode)
	}
	// The operator token holds no per-channel assignment, so it can neither
	// read the record nor remove it, and the record is left alone.
	if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("%s/%d", localChannelsPath, ungranted.ID), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unassigned record as the operator = %d, want 403", resp.StatusCode)
	}
	if resp := env.send(t, operatorToken, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, ungranted.ID), `{}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("leave as the operator = %d, want 403", resp.StatusCode)
	}
	if _, found, _ := env.store.GetByID(context.Background(), ungranted.ID); !found {
		t.Fatal("the record was removed by a caller that could not be checked")
	}
}

func TestLocalChannelLeaveReportsAPartialFailure(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	env.grantDeleteOn(t, row.ID)
	base := env.handlerConfig()
	env.useConfig(failingDeleteConfig(env, &failingDeleteStore{Store: env.store}))

	resp := env.send(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID),
		env.leaveBody(t, map[string]any{"telegram": true}))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("partial failure = %d, want 500", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "partial_failure" {
		t.Fatalf("code = %q, want partial_failure", body.Error.Code)
	}
	if !strings.Contains(body.Error.Message, "Telegram account left") {
		t.Fatalf("the message must say Telegram's part succeeded: %q", body.Error.Message)
	}
	if len(workers["official"].leaveIDs) != 1 || workers["official"].leaveIDs[0] != 500 {
		t.Fatalf("Telegram was not asked to leave: %+v", workers["official"].leaveIDs)
	}
	if _, found, _ := env.store.GetByID(context.Background(), row.ID); !found {
		t.Fatal("the record must survive a failed delete, to be retried")
	}

	// Retrying with telegram false removes the record and never asks Telegram.
	env.useConfig(base)
	if resp := env.send(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), `{}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("retry = %d", resp.StatusCode)
	}
	if len(workers["official"].leaveIDs) != 1 {
		t.Fatalf("the retry asked Telegram again: %+v", workers["official"].leaveIDs)
	}
}

func TestLocalChannelLocalOnlyDeleteFailureIsAStoreError(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	env.grantDeleteOn(t, row.ID)
	env.useConfig(failingDeleteConfig(env, &failingDeleteStore{Store: env.store}))

	resp := env.send(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), `{}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failed delete = %d, want 500", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "store_error" {
		t.Fatalf("code = %q, want store_error", body.Error.Code)
	}
	if workers["official"].leaveCalls != 0 {
		t.Fatal("Telegram must not be asked for a local-only removal")
	}
}

// failingDeleteConfig is the suite's configuration with the channel store
// replaced by one whose delete fails.
func failingDeleteConfig(env *accountEnv, store gateway.ManagedStore) gateway.Config {
	cfg := env.handlerConfig()
	cfg.Channels = store
	return cfg
}

// failingDeleteStore is the real store whose DeleteChannelRow always fails.
type failingDeleteStore struct {
	*channelstore.Store
}

func (failingDeleteStore) DeleteChannelRow(context.Context, int64) error {
	return errors.New("disk I/O error: /var/lib/telesync/state.db")
}

func TestLocalChannelReportedErrorsHideTheUpstreamFailure(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	workers["official"].msgE = &rpc.Error{Code: rpc.CodeInternal, Message: "auth_key_unregistered secret-ish"}

	resp := env.send(t, defaultCaller, http.MethodGet, fmt.Sprintf("%s/%d/messages", localChannelsPath, row.ID), "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("messages = %d", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "worker_error" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if strings.Contains(body.Error.Message, "auth_key") || strings.Contains(body.Error.Message, "secret") {
		t.Fatalf("the upstream message leaked: %q", body.Error.Message)
	}
}

func TestLocalChannelListingStaysLocalAndDetailUsesTelegram(t *testing.T) {
	env, workers := channelsEnv(t, "official")
	env.localRow(t, "official", 500, "first", "")
	env.localRow(t, "official", 600, "second", "")

	if resp := env.send(t, defaultCaller, http.MethodGet, localChannelsPath, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("listing = %d", resp.StatusCode)
	}
	if workers["official"].callCount() != 0 {
		t.Fatalf("the worker was called %d times; listing is local data only", workers["official"].callCount())
	}
	if resp := env.send(t, defaultCaller, http.MethodGet, localChannelsPath+"/1", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("detail = %d", resp.StatusCode)
	}
	if workers["official"].callCount() != 1 {
		t.Fatalf("the worker was called %d times after detail; want one live lookup", workers["official"].callCount())
	}
}

func TestLocalChannelMessagesUseTheGlobalViewForEveryRow(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	who := env.account(t, "viewer", channelstore.PermissionChannelsView)

	if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("%s/%d/messages", localChannelsPath, row.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("messages as a global viewer = %d, want 200", resp.StatusCode)
	}

	// A deny on the row overrides the global view.
	env.grantChannels(t, who, channelstore.ResourcePermissions{ResourceID: row.ID, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectDeny},
	}})
	for _, sub := range []string{"", "/messages", "/avatar"} {
		if resp := env.send(t, who.token, http.MethodGet, fmt.Sprintf("%s/%d%s", localChannelsPath, row.ID, sub), ""); resp.StatusCode != http.StatusForbidden {
			t.Errorf("denied row%s = %d, want 403", sub, resp.StatusCode)
		}
	}
}

func TestLocalChannelDeletionRemovesOnlyTheRowsDependents(t *testing.T) {
	env, _ := channelsEnv(t, "official")
	row := env.localRow(t, "official", 500, "first", "")
	kept := env.localRow(t, "official", 600, "second", "")
	env.grantDeleteOn(t, row.ID)
	ctx := context.Background()

	cat, err := env.store.CreateCategory(ctx, "Technology", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.AssignChannel(ctx, "official", "500", cat.ID); err != nil {
		t.Fatal(err)
	}
	// The permission assignment on the row is what ON DELETE CASCADE removes.
	if err := env.store.ReplacePermissions(ctx, env.defaults.user.ID, channelstore.PermissionSet{
		Global: channelstore.GlobalPermissions(),
		Channels: []channelstore.ResourcePermissions{{ResourceID: row.ID, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
		}}},
	}); err != nil {
		t.Fatal(err)
	}

	if resp := env.send(t, defaultCaller, http.MethodPost, fmt.Sprintf("%s/%d/leave", localChannelsPath, row.ID), `{}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("leave = %d", resp.StatusCode)
	}

	// The permission assignment on the deleted row is gone with it (ON DELETE
	// CASCADE); everything else survives.
	set, err := env.store.PermissionsOf(ctx, env.defaults.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, rp := range set.Channels {
		if rp.ResourceID == row.ID {
			t.Fatalf("the permission assignment survived the row: %+v", rp)
		}
	}
	if _, found, _ := env.store.GetByID(ctx, kept.ID); !found {
		t.Fatal("the other channel row was deleted")
	}
	if _, found, err := env.store.GetCategory(ctx, cat.ID); err != nil || !found {
		t.Fatalf("the category was deleted: found=%v err=%v", found, err)
	}
	if assigns, err := env.store.ListAssignments(ctx, "official", "500"); err != nil || len(assigns) != 1 {
		t.Fatalf("the category assignment should be untouched: %v %v", assigns, err)
	}
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
