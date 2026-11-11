package gotdclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/tgapi"
)

// findLimit bounds how many search hits one query returns.
const findLimit = 20

// publicSearchTimeout bounds the public-directory search inside a text query.
const publicSearchTimeout = 5 * time.Second

// FindChannels resolves one discovery query against Telegram.
//
// Telegram has no single "find any channel" call, so the method depends on the
// query:
//
//   - username: contacts.resolveUsername (exact, authoritative).
//   - invite:   messages.checkChatInvite (also reports existing membership).
//   - id:       this worker's peer knowledge. A bare number carries no access
//     hash, so an id this client has never seen is reported as unresolvable
//     rather than guessed at.
//   - text:     contacts.search (chats the account knows) plus
//     messages.searchGlobal restricted to broadcast channels.
//
// Nothing here joins a channel or writes anything anywhere.
func (m *Manager) FindChannels(ctx context.Context, q tgapi.ChannelQuery) ([]tgapi.ChannelInfo, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return nil, err
	}

	switch q.Kind {
	case tgapi.QueryUsername:
		return m.findByUsername(ctx, api, q.Username)
	case tgapi.QueryInvite:
		return m.findByInvite(ctx, api, q.Invite)
	case tgapi.QueryID:
		return m.findByID(ctx, q.ChannelID)
	case tgapi.QueryText:
		return m.findByText(ctx, api, q.Text)
	default:
		return nil, fmt.Errorf("unsupported query kind %q", q.Kind)
	}
}

func (m *Manager) findByUsername(ctx context.Context, api *tg.Client, username string) ([]tgapi.ChannelInfo, error) {
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		if tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID") {
			return []tgapi.ChannelInfo{}, nil
		}
		return nil, ClassifyError(err)
	}
	ch, ok := firstChannel(res.Chats)
	if !ok {
		// The username exists but names a user or a basic group.
		return []tgapi.ChannelInfo{}, nil
	}
	if info, ok := m.enrich(ctx, api, ch); ok {
		return []tgapi.ChannelInfo{info}, nil
	}
	return []tgapi.ChannelInfo{channelInfoFromChannel(ch, membershipOf(ch))}, nil
}

func (m *Manager) findByInvite(ctx context.Context, api *tg.Client, hash string) ([]tgapi.ChannelInfo, error) {
	res, err := api.MessagesCheckChatInvite(ctx, hash)
	if err != nil {
		return nil, ClassifyError(err)
	}
	switch invite := res.(type) {
	case *tg.ChatInviteAlready:
		ch, ok := invite.Chat.(*tg.Channel)
		if !ok {
			return []tgapi.ChannelInfo{}, nil
		}
		if info, ok := m.enrich(ctx, api, ch); ok {
			return []tgapi.ChannelInfo{info}, nil
		}
		return []tgapi.ChannelInfo{channelInfoFromChannel(ch, tgapi.MembershipMember)}, nil

	case *tg.ChatInvite:
		// Not a member. The invite reveals the title and whether an
		// administrator must approve a join, but never the channel id: that
		// only becomes known after joining.
		info := tgapi.ChannelInfo{
			Title:       invite.Title,
			Description: invite.About,
			Kind:        inviteKind(invite),
			Verified:    invite.Verified,
			Scam:        invite.Scam,
			Fake:        invite.Fake,
			Membership:  tgapi.MembershipNotMember,
			// No confirmed membership means no readable history, so no
			// permission can be granted.
			Permissions: tgapi.Permissions{},
		}
		if invite.ParticipantsCount > 0 {
			n := invite.ParticipantsCount
			info.ParticipantsCount = &n
		}
		if photo, ok := invite.Photo.(*tg.Photo); ok && photo.ID != 0 {
			info.HasPhoto = true
			info.PhotoID = photo.ID
		}
		return []tgapi.ChannelInfo{info}, nil

	case *tg.ChatInvitePeek:
		ch, ok := invite.Chat.(*tg.Channel)
		if !ok {
			return []tgapi.ChannelInfo{}, nil
		}
		// A peek is read-only anonymous access, not membership.
		info := channelInfoFromChannel(ch, tgapi.MembershipNotMember)
		info.Permissions = tgapi.Permissions{}
		return []tgapi.ChannelInfo{info}, nil

	default:
		return nil, fmt.Errorf("unexpected checkChatInvite result %T", res)
	}
}

func (m *Manager) findByID(ctx context.Context, id int64) ([]tgapi.ChannelInfo, error) {
	ch, err := m.knownChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return nil, err
	}
	if info, ok := m.enrich(ctx, api, ch); ok {
		return []tgapi.ChannelInfo{info}, nil
	}
	return []tgapi.ChannelInfo{channelInfoFromChannel(ch, membershipOf(ch))}, nil
}

// findByText searches the account's own chats and Telegram's public directory.
//
// contacts.search only covers chats the account already knows; searchGlobal
// covers the public directory. The public search is best-effort: it runs inside
// its own short deadline, and if Telegram accepts it (as it does on some
// backends) but never answers, the account's own results are still returned
// instead of failing a search that did partly work. The request only fails when
// neither method produced anything.
func (m *Manager) findByText(ctx context.Context, api *tg.Client, text string) ([]tgapi.ChannelInfo, error) {
	var found []*tg.Channel
	attempted, failed := 0, 0
	var firstErr error

	record := func(err error) {
		attempted++
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	res, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{
		Q:          text,
		Limit:      findLimit,
		Broadcasts: true,
	})
	record(err)
	if err == nil {
		found = append(found, channelsOf(res.Chats)...)
	}

	// A separate, bounded context: public search must not hold up the answer
	// this API can give from the account's own chats.
	globalCtx, cancelGlobal := context.WithTimeout(ctx, publicSearchTimeout)
	global, err := api.MessagesSearchGlobal(globalCtx, &tg.MessagesSearchGlobalRequest{
		Q:      text,
		Filter: &tg.InputMessagesFilterEmpty{},
		// OffsetPeer is a required field of searchGlobal; an empty peer starts
		// from the first page.
		OffsetPeer:     &tg.InputPeerEmpty{},
		BroadcastsOnly: true,
		Limit:          findLimit,
	})
	cancelGlobal()
	record(err)
	if err == nil {
		if chats, ok := globalChats(global); ok {
			found = append(found, channelsOf(chats)...)
		}
	}

	if attempted > 0 && failed == attempted {
		// Neither method worked. An empty list would read as "no such
		// channel", which is a different and untruthful answer.
		return nil, ClassifyError(firstErr)
	}
	if len(found) == 0 {
		return []tgapi.ChannelInfo{}, nil
	}

	out := make([]tgapi.ChannelInfo, 0, len(found))
	for _, ch := range found {
		m.remember(ch)
		out = append(out, channelInfoFromChannel(ch, membershipOf(ch)))
	}
	return tgapi.DedupeChannels(out), nil
}

// globalChats extracts the chat list of a searchGlobal result.
func globalChats(res tg.MessagesMessagesClass) ([]tg.ChatClass, bool) {
	switch v := res.(type) {
	case *tg.MessagesChannelMessages:
		return v.Chats, true
	case *tg.MessagesMessages:
		return v.Chats, true
	case *tg.MessagesMessagesSlice:
		return v.Chats, true
	default:
		return nil, false
	}
}

// channelsOf keeps the full, non-minimal channels of a chat list.
func channelsOf(chats []tg.ChatClass) []*tg.Channel {
	out := make([]*tg.Channel, 0, len(chats))
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok && !ch.Min {
			out = append(out, ch)
		}
	}
	return out
}

// enrich fills in the fields only a full-channel read returns.
//
// It is best-effort: a non-member cannot read a full channel, and a failed
// enrichment must not turn a good search hit into an error.
func (m *Manager) enrich(ctx context.Context, api *tg.Client, ch *tg.Channel) (tgapi.ChannelInfo, bool) {
	if ch.AccessHash == 0 {
		return tgapi.ChannelInfo{}, false
	}
	m.remember(ch)

	full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{
		ChannelID: ch.ID, AccessHash: ch.AccessHash,
	})
	if err != nil {
		return tgapi.ChannelInfo{}, false
	}
	channelFull, ok := full.FullChat.(*tg.ChannelFull)
	if !ok {
		return tgapi.ChannelInfo{}, false
	}

	info := channelInfoFromChannel(ch, membershipOf(ch))
	info.Description = channelFull.About
	if channelFull.ParticipantsCount > 0 {
		n := channelFull.ParticipantsCount
		info.ParticipantsCount = &n
	}
	return info, true
}

// ResolveChannel returns one channel's current Telegram state.
func (m *Manager) ResolveChannel(ctx context.Context, ref tgapi.ChannelRef) (tgapi.ChannelInfo, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.ChannelInfo{}, err
	}
	var ch *tg.Channel
	if ref.IsInvite() {
		ch, err = m.resolveOne(ctx, api, ref)
	} else {
		ch, err = m.knownChannelForCurrentState(ctx, ref.ID)
	}
	if err != nil {
		return tgapi.ChannelInfo{}, err
	}
	if info, ok := m.enrich(ctx, api, ch); ok {
		return info, nil
	}
	return channelInfoFromChannel(ch, membershipOf(ch)), nil
}

// JoinChannel joins the channel when the account is not already a member.
//
// A pending join request is reported as pending, never as membership.
func (m *Manager) JoinChannel(ctx context.Context, ref tgapi.ChannelRef) (tgapi.JoinResult, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.JoinResult{}, err
	}

	// An invite to a channel the account is not in reveals nothing but the
	// hash, so joining is done by importing the hash itself.
	if ref.IsInvite() {
		return m.joinByInvite(ctx, api, ref.Invite)
	}

	ch, err := m.knownChannel(ctx, ref.ID)
	if err != nil {
		return tgapi.JoinResult{}, err
	}
	if !ch.Left {
		// Already a member: a join call could only answer
		// USER_ALREADY_PARTICIPANT, so it is skipped entirely.
		info := channelInfoFromChannel(ch, tgapi.MembershipMember)
		m.complete(ctx, api, &info)
		return tgapi.JoinResult{Channel: info, Outcome: tgapi.JoinAlreadyMember}, nil
	}

	res, err := api.ChannelsJoinChannel(ctx, &tg.InputChannel{
		ChannelID: ch.ID, AccessHash: ch.AccessHash,
	})
	if err != nil {
		return tgapi.JoinResult{}, classifyJoinError(err)
	}
	return m.joinSucceeded(ctx, api, ref, res, ch)
}

// joinByInvite imports a private invite hash.
func (m *Manager) joinByInvite(ctx context.Context, api *tg.Client, hash string) (tgapi.JoinResult, error) {
	checked, err := api.MessagesCheckChatInvite(ctx, hash)
	if err != nil {
		return tgapi.JoinResult{}, ClassifyError(err)
	}
	if already, ok := checked.(*tg.ChatInviteAlready); ok {
		ch, ok := already.Chat.(*tg.Channel)
		if !ok {
			return tgapi.JoinResult{}, tgapi.ErrChannelNotFound
		}
		info := channelInfoFromChannel(ch, tgapi.MembershipMember)
		m.complete(ctx, api, &info)
		return tgapi.JoinResult{Channel: info, Outcome: tgapi.JoinAlreadyMember}, nil
	}

	res, err := api.MessagesImportChatInvite(ctx, hash)
	if err != nil {
		return tgapi.JoinResult{}, classifyJoinError(err)
	}
	return m.joinSucceeded(ctx, api, tgapi.ChannelRef{Invite: hash}, res, nil)
}

// joinSucceeded turns a successful join result into the reported outcome.
func (m *Manager) joinSucceeded(ctx context.Context, api *tg.Client, ref tgapi.ChannelRef,
	res tg.MessagesChatInviteJoinResultClass, before *tg.Channel) (tgapi.JoinResult, error) {

	joined, ok := res.(*tg.MessagesChatInviteJoinResultOk)
	if !ok {
		return tgapi.JoinResult{}, fmt.Errorf("unexpected join result %T", res)
	}
	// Telegram answering with updates that mention no channel is not evidence
	// of membership; re-read instead of assuming.
	if ch, ok := channelFromUpdates(joined.Updates); ok {
		info := channelInfoFromChannel(ch, tgapi.MembershipMember)
		m.remember(ch)
		m.complete(ctx, api, &info)
		return tgapi.JoinResult{Channel: info, Outcome: tgapi.JoinJoined}, nil
	}
	if before == nil {
		return tgapi.JoinResult{}, tgapi.ErrJoinUnconfirmed
	}
	info := channelInfoFromChannel(before, tgapi.MembershipMember)
	return tgapi.JoinResult{Channel: info, Outcome: tgapi.JoinJoined}, nil
}

// channelFromUpdates picks the channel a join result carries.
func channelFromUpdates(updates tg.UpdatesClass) (*tg.Channel, bool) {
	var chats []tg.ChatClass
	switch v := updates.(type) {
	case *tg.Updates:
		chats = v.Chats
	case *tg.UpdatesCombined:
		chats = v.Chats
	case *tg.UpdatesTooLong:
		return nil, false
	default:
		return nil, false
	}
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok && ch.AccessHash != 0 {
			return ch, true
		}
	}
	return nil, false
}

// classifyJoinError maps Telegram's join errors onto tgapi errors by code.
//
// It matches Telegram's documented error names, not substrings of arbitrary
// messages: a pending request is the documented INVITE_REQUEST_SENT answer.
func classifyJoinError(err error) error {
	switch {
	case tgerr.Is(err, "INVITE_REQUEST_SENT"):
		return tgapi.ErrJoinPending
	case tgerr.Is(err, "USER_ALREADY_PARTICIPANT", "USER_ALREADY_PARTICIPANT_INVITE"):
		return tgapi.ErrAlreadyParticipant
	case tgerr.Is(err, "INVITE_HASH_EXPIRED"):
		return tgapi.ErrInviteExpired
	case tgerr.Is(err, "INVITE_HASH_INVALID", "INVITE_HASH_EMPTY"):
		return tgapi.ErrInviteInvalid
	case tgerr.Is(err, "USERS_TOO_MUCH", "CHANNELS_TOO_MUCH", "USER_CHANNELS_TOO_MUCH"):
		return tgapi.ErrMembershipLimit
	case tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "PEER_ID_INVALID"):
		return errors.Join(tgapi.ErrChannelNotFound, err)
	}
	return ClassifyError(err)
}

// LeaveChannel makes the account leave the channel.
//
// It is the MTProto operation itself (channels.leaveChannel), not a notification
// or a mute: Telegram records the account as gone. An account that is not in the
// channel is tgapi.ErrNotAMember, so a retried leave is not an error, and an id
// this worker cannot address is reported as unresolved rather than guessed at.
//
// NOT verified against a live Telegram backend or the in-memory tgtest server:
// a leave after a test's History call could be sent but its answer never routed
// back through the test transport, so the leave adapter is covered by the
// worker's Leave tests (which use a fake adapter) and by every read path it
// shares with JoinChannel. See GOTD_NOTES.md.
func (m *Manager) LeaveChannel(ctx context.Context, ref tgapi.ChannelRef) error {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return err
	}
	if ref.IsInvite() {
		// An invite hash names no channel until it is resolved, and a stored
		// channel record always carries the numeric id.
		return tgapi.ErrNotAMember
	}
	ch, err := m.channelForLeave(ctx, ref.ID)
	if err != nil {
		return err
	}
	if ch.Left {
		return tgapi.ErrNotAMember
	}

	if _, err := api.ChannelsLeaveChannel(ctx, &tg.InputChannel{
		ChannelID: ch.ID, AccessHash: ch.AccessHash,
	}); err != nil {
		// Only Telegram's own "you are not in it" answer is the idempotent
		// case; a channel the account cannot see is a real failure the caller
		// must be able to report.
		if tgerr.Is(err, "USER_NOT_PARTICIPANT") {
			return errors.Join(tgapi.ErrNotAMember, err)
		}
		return ClassifyError(err)
	}
	return nil
}

// firstLeftChannel finds a channel Telegram reports the account has left.
func firstLeftChannel(chats []tg.ChatClass) (*tg.Channel, bool) {
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok && ch.Left {
			return ch, true
		}
	}
	return nil, false
}

// DownloadChannelAvatar fetches the channel's profile photo bytes.
func (m *Manager) DownloadChannelAvatar(ctx context.Context, ref tgapi.ChannelRef) (tgapi.AvatarFile, error) {
	client, api, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.AvatarFile{}, err
	}
	ch, err := m.resolveOne(ctx, api, ref)
	if err != nil {
		return tgapi.AvatarFile{}, err
	}
	photo, ok := ch.Photo.(*tg.ChatPhoto)
	if !ok || photo.PhotoID == 0 {
		return tgapi.AvatarFile{}, tgapi.ErrNoAvatar
	}

	loc := &tg.InputPeerPhotoFileLocation{
		Big:     true,
		Peer:    &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		PhotoID: photo.PhotoID,
	}

	var buf bytes.Buffer
	if _, err := downloader.NewDownloader().Download(client.API(), loc).
		Stream(ctx, &limitedWriter{w: &buf, limit: maxSmallFileBytes}); err != nil {
		return tgapi.AvatarFile{}, ClassifyError(err)
	}
	if buf.Len() == 0 {
		return tgapi.AvatarFile{}, tgapi.ErrNoAvatar
	}
	return tgapi.AvatarFile{
		// Telegram serves chat photos as JPEG; the gateway sniffs the bytes
		// anyway, so a wrong guess here cannot mislabel the response.
		MimeType: "image/jpeg",
		PhotoID:  photo.PhotoID,
		Data:     buf.Bytes(),
	}, nil
}

// resolveOne turns a reference into a *tg.Channel with a usable access hash.
func (m *Manager) resolveOne(ctx context.Context, api *tg.Client, ref tgapi.ChannelRef) (*tg.Channel, error) {
	if ref.IsInvite() {
		res, err := api.MessagesCheckChatInvite(ctx, ref.Invite)
		if err != nil {
			return nil, ClassifyError(err)
		}
		switch invite := res.(type) {
		case *tg.ChatInviteAlready:
			if ch, ok := invite.Chat.(*tg.Channel); ok {
				m.remember(ch)
				return ch, nil
			}
		case *tg.ChatInvitePeek:
			if ch, ok := invite.Chat.(*tg.Channel); ok {
				m.remember(ch)
				return ch, nil
			}
		}
		// An invite to a channel the account has not joined does not reveal
		// the channel id; there is nothing to address by id yet.
		return nil, tgapi.ErrNotAMember
	}
	return m.knownChannel(ctx, ref.ID)
}

// knownChannel resolves a numeric id to a channel with its access hash.
//
// The hash comes from this worker's peer cache, which is filled by dialogs,
// search and every successful resolve. A number alone is not enough: MTProto
// addresses a channel by (id, access hash), so an unseen id is reported as
// unresolvable rather than guessed at.
func (m *Manager) knownChannel(ctx context.Context, id int64) (*tg.Channel, error) {
	return m.knownChannelWithFallback(ctx, id, true)
}

// knownChannelForCurrentState requires Telegram to return a channel object;
// dialog metadata alone is insufficient evidence for current rights.
func (m *Manager) knownChannelForCurrentState(ctx context.Context, id int64) (*tg.Channel, error) {
	return m.knownChannelWithFallback(ctx, id, false)
}

func (m *Manager) knownChannelWithFallback(ctx context.Context, id int64, allowDialogFallback bool) (*tg.Channel, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return nil, err
	}

	dialogs, err := m.Dialogs(ctx)
	if err == nil {
		for _, d := range dialogs {
			if d.ID == id && d.AccessHash != 0 {
				res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
					&tg.InputChannel{ChannelID: d.ID, AccessHash: d.AccessHash},
				})
				if err == nil {
					if ch, ok := firstChannel(res.GetChats()); ok {
						m.remember(ch)
						return ch, nil
					}
				} else if !allowDialogFallback {
					return nil, ClassifyError(err)
				}
				if !allowDialogFallback {
					return nil, tgapi.ErrChannelNotFound
				}
				// Other operations retain their historical dialog fallback. The
				// current permission path above must not treat it as authoritative.
				m.remember(&tg.Channel{ID: d.ID, AccessHash: d.AccessHash})
				return &tg.Channel{
					ID: d.ID, AccessHash: d.AccessHash, Title: d.Title, Username: d.Username,
					Broadcast: d.Kind == "channel", Megagroup: d.Kind == "group",
				}, nil
			}
		}
	}

	if hash, ok := m.peerHash(id); ok {
		res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: id, AccessHash: hash},
		})
		if err != nil {
			return nil, ClassifyError(err)
		}
		if ch, ok := firstChannel(res.GetChats()); ok {
			m.remember(ch)
			return ch, nil
		}
		return nil, tgapi.ErrChannelNotFound
	}

	return nil, tgapi.ErrNumericIDUnresolved
}

// channelForLeave resolves one channel to an object the account can leave.
//
// A number alone is not addressable: MTProto wants the access hash, which comes
// from dialogs, search or the peer cache. Unlike knownChannel, a channel the
// account already left is a usable answer here — it is exactly what "leave
// again" means.
func (m *Manager) channelForLeave(ctx context.Context, id int64) (*tg.Channel, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return nil, err
	}
	var hash int64
	for _, d := range m.dialogsOrNothing(ctx) {
		if d.ID == id && d.AccessHash != 0 {
			hash = d.AccessHash
			break
		}
	}
	if hash == 0 {
		if h, ok := m.peerHash(id); ok {
			hash = h
		}
	}
	if hash == 0 {
		return nil, tgapi.ErrNumericIDUnresolved
	}
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: id, AccessHash: hash},
	})
	if err != nil {
		return nil, ClassifyError(err)
	}
	// A channel the account has already left is still listed here with Left
	// set, which is exactly what a retried leave needs to see.
	if ch, ok := firstLeftChannel(res.GetChats()); ok && ch.ID == id {
		return ch, nil
	}
	if ch, ok := firstChannel(res.GetChats()); ok && ch.ID == id {
		m.remember(ch)
		return ch, nil
	}
	return nil, tgapi.ErrChannelNotFound
}

// dialogsOrNothing is the dialog list, or an empty one when it cannot be read:
// the peer cache is enough to address a channel whose dialog is gone.
func (m *Manager) dialogsOrNothing(ctx context.Context) []tgapi.Channel {
	dialogs, err := m.Dialogs(ctx)
	if err != nil {
		return nil
	}
	return dialogs
}

// complete adds the fields only a full-channel read provides.
func (m *Manager) complete(ctx context.Context, api *tg.Client, info *tgapi.ChannelInfo) {
	if info.AccessHash == 0 {
		return
	}
	full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{
		ChannelID: info.ID, AccessHash: info.AccessHash,
	})
	if err != nil {
		return
	}
	channelFull, ok := full.FullChat.(*tg.ChannelFull)
	if !ok {
		return
	}
	if channelFull.About != "" {
		info.Description = channelFull.About
	}
	if channelFull.ParticipantsCount > 0 {
		n := channelFull.ParticipantsCount
		info.ParticipantsCount = &n
	}
}

// channelInfoFromChannel converts Telegram's channel into the neutral shape.
func channelInfoFromChannel(ch *tg.Channel, membership tgapi.Membership) tgapi.ChannelInfo {
	info := tgapi.ChannelInfo{
		ID:          ch.ID,
		AccessHash:  ch.AccessHash,
		Title:       ch.Title,
		Username:    ch.Username,
		Kind:        ChannelKind(ch),
		Verified:    ch.Verified,
		Scam:        ch.Scam,
		Fake:        ch.Fake,
		Membership:  membership,
		Permissions: tgapi.NormalizePermissions(channelAccess(ch, membership)),
		HasPhoto:    hasChatPhoto(ch),
		PhotoID:     chatPhotoID(ch),
	}
	if ch.ParticipantsCount > 0 {
		n := ch.ParticipantsCount
		info.ParticipantsCount = &n
	}
	return info
}

// channelAccess turns a channel into the single permission input.
func channelAccess(ch *tg.Channel, membership tgapi.Membership) tgapi.ChannelAccess {
	a := tgapi.ChannelAccess{
		Known:     membership != tgapi.MembershipUnknown,
		Member:    membership == tgapi.MembershipMember,
		Left:      ch.Left,
		Creator:   ch.Creator,
		Broadcast: ch.Broadcast,
	}
	if admins, ok := ch.GetAdminRights(); ok {
		a.Admin = true
		a.PostMessages = admins.PostMessages
		a.EditMessages = admins.EditMessages
		a.DeleteMessages = admins.DeleteMessages
	}
	if banned, ok := ch.GetBannedRights(); ok {
		a.ViewDenied = banned.ViewMessages
		a.SendDenied = banned.SendMessages
	}
	if defaults, ok := ch.GetDefaultBannedRights(); ok {
		a.DefaultSendDenied = defaults.SendMessages
	}
	return a
}

// membershipOf reads the membership state a channel object implies.
func membershipOf(ch *tg.Channel) tgapi.Membership {
	if ch.Left {
		return tgapi.MembershipNotMember
	}
	return tgapi.MembershipMember
}

func inviteKind(invite *tg.ChatInvite) string {
	if invite.Broadcast || (!invite.Megagroup && invite.Channel) {
		return "channel"
	}
	return "group"
}

// firstChannel returns the first full channel of a chat list.
func firstChannel(chats []tg.ChatClass) (*tg.Channel, bool) {
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok && !ch.Min && ch.AccessHash != 0 {
			return ch, true
		}
	}
	return nil, false
}
