package worker

import (
	"context"
	"errors"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// Find resolves one discovery query.
//
// The public query string is parsed by the gateway; the worker receives the
// structured form and validates it again, because it is the last component
// before Telegram and must not forward nonsense.
func (s *Service) Find(ctx context.Context, req rpc.FindRequest) (rpc.FindResult, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.FindResult{}, err
	}
	query, err := queryFromRequest(req)
	if err != nil {
		return rpc.FindResult{}, err
	}

	infos, err := s.tg.FindChannels(ctx, query)
	if err != nil {
		return rpc.FindResult{}, s.mapDiscoveryError(err)
	}
	return rpc.FindResult{Channels: channelInfos(infos)}, nil
}

// Channel resolves one channel reference to its current Telegram state.
func (s *Service) Channel(ctx context.Context, ref rpc.ChannelRef) (rpc.ChannelResult, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.ChannelResult{}, err
	}
	parsed, err := refFromWire(ref)
	if err != nil {
		return rpc.ChannelResult{}, err
	}
	info, err := s.tg.ResolveChannel(ctx, parsed)
	if err != nil {
		return rpc.ChannelResult{}, s.mapDiscoveryError(err)
	}
	return rpc.ChannelResult{Channel: channelInfoDTO(info)}, nil
}

// Join joins the channel when needed.
//
// It reports exactly what Telegram confirmed and never upgrades a pending
// request to membership. It writes nothing: the managed-channel record belongs
// to the gateway, which owns the database.
func (s *Service) Join(ctx context.Context, ref rpc.ChannelRef) (rpc.JoinResult, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.JoinResult{}, err
	}
	parsed, err := refFromWire(ref)
	if err != nil {
		return rpc.JoinResult{}, err
	}

	result, err := s.tg.JoinChannel(ctx, parsed)
	if err != nil {
		// A pending request and an "already a member" answer are states, not
		// failures: they carry the truth the caller needs.
		switch {
		case errors.Is(err, tgapi.ErrJoinPending):
			return rpc.JoinResult{
				Channel: channelInfoDTO(tgapi.ChannelInfo{
					ID: parsed.ID, Membership: tgapi.MembershipJoinPending,
				}),
				Outcome: rpc.OutcomePending,
			}, nil
		case errors.Is(err, tgapi.ErrAlreadyParticipant):
			// Telegram only says this after the idempotent path has confirmed
			// membership; re-resolve so the answer carries real metadata.
			info, resolveErr := s.tg.ResolveChannel(ctx, parsed)
			if resolveErr != nil {
				info = tgapi.ChannelInfo{ID: parsed.ID, Membership: tgapi.MembershipMember}
			}
			return rpc.JoinResult{
				Channel: channelInfoDTO(info), Outcome: rpc.OutcomeAlreadyMember,
			}, nil
		}
		return rpc.JoinResult{}, s.mapDiscoveryError(err)
	}

	return rpc.JoinResult{
		Channel: channelInfoDTO(result.Channel),
		Outcome: outcomeOf(result.Outcome),
	}, nil
}

// Avatar downloads the channel's profile photo bytes.
func (s *Service) Avatar(ctx context.Context, ref rpc.ChannelRef) (rpc.AvatarFile, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.AvatarFile{}, err
	}
	parsed, err := refFromWire(ref)
	if err != nil {
		return rpc.AvatarFile{}, err
	}

	file, err := s.tg.DownloadChannelAvatar(ctx, parsed)
	if err != nil {
		if errors.Is(err, tgapi.ErrNoAvatar) {
			return rpc.AvatarFile{}, rpc.NewError(rpc.CodeNoAvatar,
				"This channel has no avatar.", 0)
		}
		return rpc.AvatarFile{}, s.mapDiscoveryError(err)
	}
	if len(file.Data) == 0 {
		return rpc.AvatarFile{}, rpc.NewError(rpc.CodeNoAvatar, "This channel has no avatar.", 0)
	}
	return rpc.AvatarFile{
		MimeType: file.MimeType,
		PhotoID:  file.PhotoID,
		Data:     file.Data,
	}, nil
}

// Leave makes the account leave the channel.
//
// It reports what Telegram confirmed and writes nothing: the local record
// belongs to the gateway, which owns the database. An account that is not in
// the channel has nothing to leave, which is reported rather than treated as a
// failure.
func (s *Service) Leave(ctx context.Context, ref rpc.ChannelRef) (rpc.LeaveResult, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.LeaveResult{}, err
	}
	parsed, err := refFromWire(ref)
	if err != nil {
		return rpc.LeaveResult{}, err
	}
	if err := s.tg.LeaveChannel(ctx, parsed); err != nil {
		if errors.Is(err, tgapi.ErrNotAMember) {
			// Not a member: there is nothing to leave, which is the outcome the
			// caller asked for, not a failure.
			return rpc.LeaveResult{AlreadyAbsent: true}, nil
		}
		return rpc.LeaveResult{}, s.mapDiscoveryError(err)
	}
	// The channel is gone from this worker's view of the account: the next
	// page of dialogs is fetched fresh rather than from the cached snapshot.
	s.invalidateSnapshot()
	return rpc.LeaveResult{}, nil
}

// queryFromRequest validates a wire query and converts it for the adapter.
func queryFromRequest(req rpc.FindRequest) (tgapi.ChannelQuery, error) {
	switch req.Kind {
	case rpc.FindByUsername:
		q, err := tgapi.ParseChannelQuery("@" + req.Username)
		if err != nil || q.Kind != tgapi.QueryUsername {
			return tgapi.ChannelQuery{}, rpc.NewError(rpc.CodeInvalidInput,
				"A username search needs a valid public username.", 0)
		}
		return q, nil
	case rpc.FindByInvite:
		q, err := tgapi.ParseChannelQuery("https://t.me/+" + req.Invite)
		if err != nil || q.Kind != tgapi.QueryInvite {
			return tgapi.ChannelQuery{}, rpc.NewError(rpc.CodeInvalidInput,
				"An invite search needs a valid invite hash.", 0)
		}
		return q, nil
	case rpc.FindByID:
		if req.ChannelID <= 0 {
			return tgapi.ChannelQuery{}, rpc.NewError(rpc.CodeInvalidInput,
				"A channel search needs a positive channel id.", 0)
		}
		return tgapi.ChannelQuery{Kind: tgapi.QueryID, ChannelID: req.ChannelID}, nil
	case rpc.FindByText:
		q, err := tgapi.ParseChannelQuery(req.Text)
		if err != nil || q.Kind == tgapi.QueryText && q.Text == "" {
			return tgapi.ChannelQuery{}, rpc.NewError(rpc.CodeInvalidInput,
				"A text search needs a non-empty query.", 0)
		}
		// A text query that happens to look like a username or link is still
		// resolved the precise way: it costs one call and cannot be wrong.
		return q, nil
	default:
		return tgapi.ChannelQuery{}, rpc.NewError(rpc.CodeInvalidInput,
			"Unsupported search kind.", 0)
	}
}

// refFromWire validates a channel reference.
func refFromWire(ref rpc.ChannelRef) (tgapi.ChannelRef, error) {
	switch {
	case ref.Invite != "":
		// The invite hash is opaque; it is checked when Telegram sees it.
		return tgapi.ParseChannelRef("+" + ref.Invite)
	case ref.ChannelID > 0:
		return tgapi.ChannelRef{ID: ref.ChannelID}, nil
	default:
		return tgapi.ChannelRef{}, rpc.NewError(rpc.CodeInvalidInput,
			"A channel id or invite hash is required.", 0)
	}
}

// mapDiscoveryError turns adapter errors into wire codes.
//
// The log keeps the error Telegram actually sent. Without it an unclassified
// adapter error reaches the caller as "internal", which names nothing.
func (s *Service) mapDiscoveryError(err error) error {
	out := classifyDiscoveryError(err)
	code := "not_mapped"
	if coded, ok := out.(*rpc.Error); ok {
		code = coded.Code
	}
	s.log("worker: account=%s mapped error %v -> %s", s.logName(), err, code)
	return out
}

func classifyDiscoveryError(err error) error {
	switch {
	case errors.Is(err, tgapi.ErrInviteInvalid), errors.Is(err, tgapi.ErrInviteExpired):
		return rpc.NewError(rpc.CodeInviteInvalid, "The invite link is not valid or has expired.", 0)
	case errors.Is(err, tgapi.ErrMembershipLimit):
		return rpc.NewError(rpc.CodeMembershipLimit, "This account cannot join more channels.", 0)
	case errors.Is(err, tgapi.ErrNumericIDUnresolved):
		return rpc.NewError(rpc.CodePeerUnresolved,
			"This numeric channel id cannot be resolved without prior peer information.", 0)
	case errors.Is(err, tgapi.ErrNotAMember):
		return rpc.NewError(rpc.CodeChannelNotFound,
			"The account is not a member of this channel.", 0)
	case errors.Is(err, tgapi.ErrChannelNotFound):
		return rpc.NewError(rpc.CodeChannelNotFound, "The channel was not found.", 0)
	case errors.Is(err, tgapi.ErrJoinUnconfirmed):
		return rpc.NewError(rpc.CodeInternal,
			"Telegram accepted the join call but did not confirm membership; retry.", 0)
	case errors.Is(err, tgapi.ErrNotAuthorized):
		return rpc.ErrNeedsAuth
	case errors.Is(err, tgapi.ErrNotConnected):
		return rpc.ErrNotConnected
	}
	return err
}

// channelInfos converts a discovery result, never returning nil.
func channelInfos(infos []tgapi.ChannelInfo) []rpc.ChannelInfo {
	out := make([]rpc.ChannelInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, channelInfoDTO(info))
	}
	return out
}

// channelInfoDTO converts one channel to its wire shape.
func channelInfoDTO(info tgapi.ChannelInfo) rpc.ChannelInfo {
	out := rpc.ChannelInfo{
		ID:               channelIDString(info.ID),
		Type:             info.Kind,
		Title:            info.Title,
		Username:         info.Username,
		Description:      info.Description,
		AvatarAvailable:  info.HasPhoto,
		HasPhoto:         info.HasPhoto,
		AvatarVersion:    info.PhotoID,
		IsMember:         info.Membership == tgapi.MembershipMember,
		MembershipStatus: string(info.Membership),
		Permissions: rpc.Permissions{
			New:    info.Permissions.New,
			Edit:   info.Permissions.Edit,
			Delete: info.Permissions.Delete,
			Read:   info.Permissions.Read,
		},
		Verified:          info.Verified,
		Scam:              info.Scam,
		Fake:              info.Fake,
		ParticipantsCount: info.ParticipantsCount,
	}
	if out.Type == "" {
		out.Type = "channel"
	}
	if out.MembershipStatus == "" {
		out.MembershipStatus = string(tgapi.MembershipUnknown)
	}
	return out
}

// outcomeOf maps the adapter's outcome onto the wire vocabulary.
func outcomeOf(outcome tgapi.JoinOutcome) string {
	switch outcome {
	case tgapi.JoinAlreadyMember:
		return rpc.OutcomeAlreadyMember
	case tgapi.JoinPending:
		return rpc.OutcomePending
	default:
		return rpc.OutcomeJoined
	}
}

// channelIDString renders a channel id for JavaScript-safe JSON.
func channelIDString(id int64) string {
	if id == 0 {
		return ""
	}
	return tgapi.FormatChannelID(id)
}
