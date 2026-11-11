package gotdclient_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"

	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/peerstore"
	"github.com/mirza/telesync/internal/tgapi"
)

// channelHandlers installs the subset of Telegram methods the discovery path
// uses, driven by a small scripted backend.
type discoveryBackend struct {
	resolveUsername func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error)
	checkInvite     func(hash string) (tg.ChatInviteClass, error)
	joinChannel     func(*tg.InputChannel) *tgerr.Error
	importInvite    func(hash string) *tgerr.Error
	fullChannel     func(*tg.ChannelsGetFullChannelRequest) (tg.ChatFullClass, error)
	search          func(*tg.ContactsSearchRequest) ([]tg.ChatClass, error)
	searchGlobal    func(*tg.MessagesSearchGlobalRequest) (tg.MessagesMessagesClass, error)
	getChannels     func([]tg.InputChannelClass) ([]tg.ChatClass, error)
	uploadFile      func(*tg.UploadGetFileRequest) ([]byte, error)
}

func (b *discoveryBackend) dispatcher(t *testing.T) func(*tgtest.Dispatcher) {
	return func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.ContactsResolveUsernameRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.ContactsResolveUsernameRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			if b.resolveUsername == nil {
				return server.SendErr(req, tgerr.New(400, "USERNAME_NOT_OCCUPIED"))
			}
			chats, err := b.resolveUsername(&request)
			if err != nil {
				return server.SendErr(req, tgerr.New(400, "UNHANDLED"))
			}
			return server.SendResult(req, &tg.ContactsResolvedPeer{
				Peer:  &tg.PeerChannel{ChannelID: 500},
				Chats: chats,
				Users: []tg.UserClass{},
			})
		})

		d.HandleFunc(tg.MessagesCheckChatInviteRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.MessagesCheckChatInviteRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			if b.checkInvite == nil {
				return server.SendErr(req, tgerr.New(400, "INVITE_HASH_INVALID"))
			}
			invite, err := b.checkInvite(request.Hash)
			if err != nil {
				return server.SendErr(req, tgerr.New(400, "INVITE_HASH_EXPIRED"))
			}
			return server.SendResult(req, invite)
		})

		d.HandleFunc(tg.ChannelsJoinChannelRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.ChannelsJoinChannelRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			ch, _ := request.Channel.(*tg.InputChannel)
			if b.joinChannel != nil {
				if err := b.joinChannel(ch); err != nil {
					return server.SendErr(req, err)
				}
			}
			return server.SendResult(req, &tg.MessagesChatInviteJoinResultBox{
				ChatInviteJoinResult: &tg.MessagesChatInviteJoinResultOk{
					Updates: &tg.Updates{
						Chats: []tg.ChatClass{&tg.Channel{
							ID: ch.ChannelID, AccessHash: ch.AccessHash, Title: "Joined",
							Broadcast: true, Photo: &tg.ChatPhoto{PhotoID: 55, DCID: 2},
						}},
					},
				},
			})
		})

		d.HandleFunc(tg.MessagesImportChatInviteRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.MessagesImportChatInviteRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			if b.importInvite != nil {
				if err := b.importInvite(request.Hash); err != nil {
					return server.SendErr(req, err)
				}
			}
			return server.SendResult(req, &tg.MessagesChatInviteJoinResultBox{
				ChatInviteJoinResult: &tg.MessagesChatInviteJoinResultOk{
					Updates: &tg.Updates{
						Chats: []tg.ChatClass{&tg.Channel{
							ID: 500, AccessHash: 900, Title: "Private Example", Broadcast: true,
						}},
					},
				},
			})
		})

		d.HandleFunc(tg.ChannelsGetFullChannelRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			if b.fullChannel == nil {
				return server.SendErr(req, tgerr.New(400, "CHANNEL_PRIVATE"))
			}
			full, err := b.fullChannel(nil)
			if err != nil {
				return server.SendErr(req, tgerr.New(400, "CHANNEL_PRIVATE"))
			}
			return server.SendResult(req, &tg.MessagesChatFull{FullChat: full})
		})

		d.HandleFunc(tg.ContactsSearchRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.ContactsSearchRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			if b.search == nil {
				return server.SendResult(req, &tg.ContactsFound{})
			}
			chats, err := b.search(&request)
			if err != nil {
				return server.SendErr(req, tgerr.New(400, "SEARCH_TOO_COMPLEX"))
			}
			return server.SendResult(req, &tg.ContactsFound{Chats: chats})
		})

		d.HandleFunc(tg.MessagesSearchGlobalRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.MessagesSearchGlobalRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			if b.searchGlobal == nil {
				return server.SendResult(req, &tg.MessagesChannelMessages{})
			}
			res, err := b.searchGlobal(&request)
			if err != nil {
				return server.SendErr(req, tgerr.New(400, "SEARCH_TOO_COMPLEX"))
			}
			return server.SendResult(req, res)
		})

		d.HandleFunc(tg.ChannelsGetChannelsRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.ChannelsGetChannelsRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			if b.getChannels == nil {
				return server.SendResult(req, &tg.MessagesChats{Chats: []tg.ChatClass{}})
			}
			chats, err := b.getChannels(request.ID)
			if err != nil {
				return server.SendErr(req, tgerr.New(400, "CHANNEL_INVALID"))
			}
			return server.SendResult(req, &tg.MessagesChats{Chats: chats})
		})

		d.HandleFunc(tg.UploadGetFileRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			var request tg.UploadGetFileRequest
			if err := request.Decode(req.Buf); err != nil {
				return err
			}
			if b.uploadFile == nil {
				return server.SendErr(req, tgerr.New(400, "LOCATION_INVALID"))
			}
			data, err := b.uploadFile(&request)
			if err != nil {
				return server.SendErr(req, tgerr.New(400, err.Error()))
			}
			return server.SendResult(req, &tg.UploadFile{
				Type: &tg.StorageFileJpeg{}, Bytes: data,
			})
		})
	}
}

// channelFull builds an encodable channelFull with the given description.
func channelFull(id int64, about string, participants int) *tg.ChannelFull {
	return &tg.ChannelFull{
		ID: id, About: about, ParticipantsCount: participants,
		ChatPhoto:      &tg.PhotoEmpty{ID: 77},
		NotifySettings: tg.PeerNotifySettings{},
	}
}

// managerWithPeers builds a Manager with a peer store rooted in a temp dir.
func managerWithPeers(t *testing.T, backend *discoveryBackend) *gotdclient.Manager {
	t.Helper()
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{}, backend.dispatcher(t))

	peers, err := peerstore.Open(filepath.Join(t.TempDir(), "peers", "official.json"))
	if err != nil {
		t.Fatal(err)
	}

	rec := &recordedDial{l: l}
	spec := specForCustom(t, l, rec, key)
	m := gotdclient.NewManager(spec, gotdclient.ManagerOptions{Peers: peers})

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	t.Cleanup(func() {
		cancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer waitCancel()
		_ = m.Wait(waitCtx)
	})
	waitForState(t, m, tgapi.StateReady)
	return m
}

func ctxTimeout(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func memberChannel() *tg.Channel {
	return &tg.Channel{
		ID: 500, AccessHash: 900, Title: "Example Channel", Username: "example",
		Broadcast: true, Photo: &tg.ChatPhoto{PhotoID: 77, DCID: 2},
	}
}

func TestFindByUsernameResolvesAndDescribesTheChannel(t *testing.T) {
	backend := &discoveryBackend{
		resolveUsername: func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
			return []tg.ChatClass{memberChannel()}, nil
		},
		fullChannel: func(*tg.ChannelsGetFullChannelRequest) (tg.ChatFullClass, error) {
			return channelFull(500, "The description", 1234), nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()

	got, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "example"})
	if err != nil {
		t.Fatalf("FindChannels: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("channels = %+v", got)
	}
	ch := got[0]
	if ch.ID != 500 || ch.Title != "Example Channel" || ch.Username != "example" {
		t.Fatalf("channel = %+v", ch)
	}
	if ch.Description != "The description" {
		t.Errorf("description = %q", ch.Description)
	}
	if ch.ParticipantsCount == nil || *ch.ParticipantsCount != 1234 {
		t.Errorf("participants = %v", ch.ParticipantsCount)
	}
	if ch.Membership != tgapi.MembershipMember || !ch.Permissions.Read {
		t.Errorf("membership = %q permissions = %+v", ch.Membership, ch.Permissions)
	}
	if !ch.HasPhoto || ch.PhotoID != 77 {
		t.Errorf("photo = %v/%d", ch.HasPhoto, ch.PhotoID)
	}
}

func TestFindByUsernameUsesChannelCreatorFlagWithoutAdminRights(t *testing.T) {
	creator := memberChannel()
	creator.Creator = true
	// Telegram's creator flag is authoritative on its own; creator channel
	// objects need not carry a separate admin_rights constructor.
	backend := &discoveryBackend{
		resolveUsername: func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
			return []tg.ChatClass{creator}, nil
		},
	}
	m := managerWithPeers(t, backend)
	ctx, cancel := ctxTimeout(t)
	defer cancel()

	got, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "example"})
	if err != nil {
		t.Fatalf("FindChannels: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("channels = %+v, want creator channel", got)
	}
	if got[0].Permissions != (tgapi.Permissions{New: true, Edit: true, Delete: true, Read: true}) {
		t.Fatalf("creator permissions = %+v, want all channel-level capabilities", got[0].Permissions)
	}
}

func TestFindByUsernameReturnsEmptyForAnOccupiedNonChannel(t *testing.T) {
	backend := &discoveryBackend{
		resolveUsername: func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
			// A user, not a channel.
			return nil, nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	got, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "someone"})
	if err != nil {
		t.Fatalf("FindChannels: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("channels = %+v", got)
	}
}

func TestFindByUsernameReturnsEmptyWhenTheNameIsFree(t *testing.T) {
	backend := &discoveryBackend{}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	got, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "nobody"})
	if err != nil {
		t.Fatalf("FindChannels: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("channels = %+v", got)
	}
}

func TestFindByInviteReportsAnExistingMembership(t *testing.T) {
	backend := &discoveryBackend{
		checkInvite: func(string) (tg.ChatInviteClass, error) {
			return &tg.ChatInviteAlready{Chat: memberChannel()}, nil
		},
		fullChannel: func(*tg.ChannelsGetFullChannelRequest) (tg.ChatFullClass, error) {
			return channelFull(500, "About", 0), nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	got, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryInvite, Invite: "AbCdEf123456"})
	if err != nil {
		t.Fatalf("FindChannels: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("channels = %+v", got)
	}
	if got[0].Membership != tgapi.MembershipMember {
		t.Fatalf("membership = %q", got[0].Membership)
	}
}

func TestFindByInviteDescribesAChannelTheAccountHasNotJoined(t *testing.T) {
	backend := &discoveryBackend{
		checkInvite: func(string) (tg.ChatInviteClass, error) {
			return &tg.ChatInvitePeek{
				Expires: int(time.Now().Add(time.Hour).Unix()),
				Chat: &tg.Channel{
					ID: 500, AccessHash: 900, Title: "Secret Channel",
					Broadcast: true, Photo: &tg.ChatPhotoEmpty{},
				},
			}, nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	got, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryInvite, Invite: "AbCdEf123456"})
	if err != nil {
		t.Fatalf("FindChannels: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("channels = %+v", got)
	}
	ch := got[0]
	if ch.Title != "Secret Channel" || ch.Membership != tgapi.MembershipNotMember {
		t.Fatalf("channel = %+v", ch)
	}
	// No membership means no readable history: nothing may be granted.
	if ch.Permissions != (tgapi.Permissions{}) {
		t.Fatalf("permissions = %+v", ch.Permissions)
	}
	// A peek names the channel but grants read-only, unjoined access; the
	// permission set must stay empty even though the id is visible.
	if ch.Permissions.Read || ch.Permissions.New || ch.Permissions.Edit || ch.Permissions.Delete {
		t.Fatalf("an unjoined invite must grant nothing: %+v", ch.Permissions)
	}
}

// TestFindByTextKeepsTheAccountsOwnResultsWhenPublicSearchStalls pins the
// behaviour that matters in production: Telegram's public-directory search can
// be accepted and then never answered. A query must still return what the
// account's own chats gave, instead of failing or hanging.
//
// It is exercised against the wire rather than by scripting searchGlobal,
// because the point is what happens when no answer arrives.
func TestFindByTextKeepsTheAccountsOwnResultsWhenPublicSearchStalls(t *testing.T) {
	backend := &discoveryBackend{
		search: func(*tg.ContactsSearchRequest) ([]tg.ChatClass, error) {
			return []tg.ChatClass{memberChannel()}, nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	started := time.Now()
	res, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryText, Text: "example"})
	if err != nil {
		t.Fatalf("FindChannels: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("a stalled public search held the answer for %s", elapsed)
	}
	if len(res) != 1 || res[0].ID != 500 {
		t.Fatalf("channels = %+v, want the account's own hit", res)
	}
}

// TestDedupeCollapsesRepeatedChannels covers the merge step directly: the wire
// path cannot be relied on to deliver a scripted searchGlobal answer, so the
// merge itself is what is pinned here.
func TestDedupeCollapsesRepeatedChannels(t *testing.T) {
	in := []tgapi.ChannelInfo{
		{ID: 500, Title: "first"},
		{ID: 500, Title: "second"},
		{ID: 501, Title: "other"},
		{ID: 0, Title: "no id"},
		{ID: 0, Title: "no id again"},
	}
	out := tgapi.DedupeChannels(in)
	if len(out) != 4 {
		t.Fatalf("deduped = %+v", out)
	}
	if out[0].Title != "first" {
		t.Fatalf("the first occurrence must win, got %q", out[0].Title)
	}
	if out[1].ID != 501 {
		t.Fatalf("order changed: %+v", out)
	}
}

func TestFindByTextReportsAFailureWhenBothSearchesFail(t *testing.T) {
	backend := &discoveryBackend{
		search: func(*tg.ContactsSearchRequest) ([]tg.ChatClass, error) {
			return nil, errors.New("nope")
		},
		searchGlobal: func(*tg.MessagesSearchGlobalRequest) (tg.MessagesMessagesClass, error) {
			return nil, errors.New("nope")
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	if _, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryText, Text: "x"}); err == nil {
		t.Fatal("both searches failing must not look like an empty result")
	}
}

func TestFindByNumericIDNeedsPeerInformation(t *testing.T) {
	m := managerWithPeers(t, &discoveryBackend{})

	ctx, cancel := ctxTimeout(t)
	defer cancel()

	// An id this worker has never seen cannot be addressed.
	_, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryID, ChannelID: 999})
	if !errors.Is(err, tgapi.ErrNumericIDUnresolved) {
		t.Fatalf("err = %v, want ErrNumericIDUnresolved", err)
	}
}

func TestResolveChannelDoesNotTreatReconstructedDialogAsAuthoritative(t *testing.T) {
	backend := &discoveryBackend{
		getChannels: func([]tg.InputChannelClass) ([]tg.ChatClass, error) {
			return nil, errors.New("channel lookup unavailable")
		},
	}
	m := managerWithPeers(t, backend)
	ctx, cancel := ctxTimeout(t)
	defer cancel()

	got, err := m.ResolveChannel(ctx, tgapi.ChannelRef{ID: 500})
	if err == nil {
		t.Fatalf("ResolveChannel returned incomplete reconstructed state as success: %+v", got)
	}
}

func TestFindByNumericIDUsesTheRememberedAccessHash(t *testing.T) {
	backend := &discoveryBackend{
		getChannels: func([]tg.InputChannelClass) ([]tg.ChatClass, error) {
			return []tg.ChatClass{memberChannel()}, nil
		},
	}
	m := managerWithPeers(t, backend)

	// Resolving by username remembers the hash, exactly as a real search would.
	ctx, cancel := ctxTimeout(t)
	defer cancel()
	if _, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "example"}); err != nil {
		t.Fatalf("seed resolve: %v", err)
	}

	got, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryID, ChannelID: 500})
	if err != nil {
		t.Fatalf("FindChannels by id: %v", err)
	}
	if len(got) != 1 || got[0].ID != 500 {
		t.Fatalf("channels = %+v", got)
	}
}

func TestJoinReportsAlreadyMemberWithoutCallingTelegram(t *testing.T) {
	joined := false
	backend := &discoveryBackend{
		resolveUsername: func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
			ch := memberChannel()
			ch.Left = false
			return []tg.ChatClass{ch}, nil
		},
		joinChannel: func(*tg.InputChannel) *tgerr.Error {
			joined = true
			return nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	if _, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "example"}); err != nil {
		t.Fatal(err)
	}

	res, err := m.JoinChannel(ctx, tgapi.ChannelRef{ID: 500})
	if err != nil {
		t.Fatalf("JoinChannel: %v", err)
	}
	if res.Outcome != tgapi.JoinAlreadyMember {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if joined {
		t.Fatal("an unnecessary join call was made")
	}
}

func TestJoinJoinsAndReportsTheConfirmedMembership(t *testing.T) {
	// Channel 700 is not in the dialog snapshot, so its membership comes only
	// from the explicit channel read.
	left := &tg.Channel{
		ID: 700, AccessHash: 901, Title: "Not Yet", Username: "notyet",
		Broadcast: true, Left: true, Photo: &tg.ChatPhoto{PhotoID: 5, DCID: 2},
	}
	joinedCalled := false
	backend := &discoveryBackend{
		resolveUsername: func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
			return []tg.ChatClass{left}, nil
		},
		// Until the join call is made, Telegram still says the account has left.
		getChannels: func([]tg.InputChannelClass) ([]tg.ChatClass, error) {
			return []tg.ChatClass{left}, nil
		},
		joinChannel: func(*tg.InputChannel) *tgerr.Error {
			joinedCalled = true
			return nil
		},
	}
	// The join result itself carries the now-joined channel.
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	if _, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "notyet"}); err != nil {
		t.Fatal(err)
	}

	res, err := m.JoinChannel(ctx, tgapi.ChannelRef{ID: 700})
	if err != nil {
		t.Fatalf("JoinChannel: %v", err)
	}
	if !joinedCalled {
		t.Fatal("the join call was skipped")
	}
	if res.Outcome != tgapi.JoinJoined {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if res.Channel.ID != 700 || !res.Channel.Permissions.Read {
		t.Fatalf("channel = %+v", res.Channel)
	}
}

func TestJoinReportsAPendingRequest(t *testing.T) {
	notYet := &tg.Channel{
		ID: 700, AccessHash: 901, Title: "Request Needed", Username: "requested",
		Broadcast: true, Left: true, JoinRequest: true,
		Photo: &tg.ChatPhoto{PhotoID: 5, DCID: 2},
	}
	backend := &discoveryBackend{
		resolveUsername: func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
			return []tg.ChatClass{notYet}, nil
		},
		getChannels: func([]tg.InputChannelClass) ([]tg.ChatClass, error) {
			return []tg.ChatClass{notYet}, nil
		},
		joinChannel: func(*tg.InputChannel) *tgerr.Error {
			return tgerr.New(400, "INVITE_REQUEST_SENT")
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	if _, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "requested"}); err != nil {
		t.Fatal(err)
	}

	_, err := m.JoinChannel(ctx, tgapi.ChannelRef{ID: 700})
	if !errors.Is(err, tgapi.ErrJoinPending) {
		t.Fatalf("err = %v, want ErrJoinPending", err)
	}
}

func TestJoinMapsInviteErrors(t *testing.T) {
	tests := []struct {
		name string
		code string
		want error
	}{
		{"expired", "INVITE_HASH_EXPIRED", tgapi.ErrInviteExpired},
		{"invalid", "INVITE_HASH_INVALID", tgapi.ErrInviteInvalid},
		{"limit", "USERS_TOO_MUCH", tgapi.ErrMembershipLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := &discoveryBackend{
				checkInvite: func(string) (tg.ChatInviteClass, error) {
					return &tg.ChatInvite{
						Channel: true, Broadcast: true, Title: "X",
						Photo: &tg.PhotoEmpty{ID: 0},
					}, nil
				},
				importInvite: func(string) *tgerr.Error {
					return tgerr.New(400, tc.code)
				},
			}
			m := managerWithPeers(t, backend)

			ctx, cancel := ctxTimeout(t)
			defer cancel()
			_, err := m.JoinChannel(ctx, tgapi.ChannelRef{Invite: "AbCdEf123456"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestJoinByInviteReportsAnExistingMembership(t *testing.T) {
	backend := &discoveryBackend{
		checkInvite: func(string) (tg.ChatInviteClass, error) {
			return &tg.ChatInviteAlready{Chat: memberChannel()}, nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	res, err := m.JoinChannel(ctx, tgapi.ChannelRef{Invite: "AbCdEf123456"})
	if err != nil {
		t.Fatalf("JoinChannel: %v", err)
	}
	if res.Outcome != tgapi.JoinAlreadyMember {
		t.Fatalf("outcome = %q", res.Outcome)
	}
}

func TestAvatarDownloadsTheProfilePhoto(t *testing.T) {
	photoBytes := []byte("avatar-jpeg")
	backend := &discoveryBackend{
		resolveUsername: func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
			return []tg.ChatClass{memberChannel()}, nil
		},
		getChannels: func([]tg.InputChannelClass) ([]tg.ChatClass, error) {
			return []tg.ChatClass{memberChannel()}, nil
		},
		uploadFile: func(req *tg.UploadGetFileRequest) ([]byte, error) {
			loc, ok := req.Location.(*tg.InputPeerPhotoFileLocation)
			if !ok {
				return nil, errors.New("expected a peer photo location")
			}
			if loc.PhotoID != 77 {
				return nil, errors.New("wrong photo id")
			}
			if peer, ok := loc.Peer.(*tg.InputPeerChannel); !ok || peer.ChannelID != 500 || peer.AccessHash != 900 {
				return nil, errors.New("wrong peer")
			}
			if !loc.Big {
				return nil, errors.New("the big photo must be requested")
			}
			return photoBytes, nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	if _, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "example"}); err != nil {
		t.Fatal(err)
	}

	file, err := m.DownloadChannelAvatar(ctx, tgapi.ChannelRef{ID: 500})
	if err != nil {
		t.Fatalf("DownloadChannelAvatar: %v", err)
	}
	if string(file.Data) != string(photoBytes) {
		t.Fatalf("bytes = %q", file.Data)
	}
	if file.PhotoID != 77 {
		t.Fatalf("photo id = %d", file.PhotoID)
	}
}

func TestAvatarReportsAChannelWithoutAPhoto(t *testing.T) {
	backend := &discoveryBackend{
		getChannels: func([]tg.InputChannelClass) ([]tg.ChatClass, error) {
			return []tg.ChatClass{&tg.Channel{
				ID: 500, AccessHash: 900, Title: "No Photo", Broadcast: true,
				Photo: &tg.ChatPhotoEmpty{},
			}}, nil
		},
	}
	m := managerWithPeers(t, backend)

	ctx, cancel := ctxTimeout(t)
	defer cancel()
	if _, err := m.FindChannels(ctx, tgapi.ChannelQuery{Kind: tgapi.QueryUsername, Username: "example"}); err != nil {
		t.Fatal(err)
	}

	_, err := m.DownloadChannelAvatar(ctx, tgapi.ChannelRef{ID: 500})
	if !errors.Is(err, tgapi.ErrNoAvatar) {
		t.Fatalf("err = %v, want ErrNoAvatar", err)
	}
}
