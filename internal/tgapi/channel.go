package tgapi

import "errors"

// Membership is the connected account's standing in a channel.
//
// It is deliberately separate from "is this channel selected locally": an
// account can be a member of a channel this application does not manage, and a
// channel can be selected locally while the account is only waiting for a join
// request to be approved.
type Membership string

const (
	MembershipMember      Membership = "member"
	MembershipNotMember   Membership = "not_member"
	MembershipJoinPending Membership = "join_pending"
	MembershipUnknown     Membership = "unknown"
)

// Permissions is the channel-level capability set the public API reports.
//
// Every field is a plain bool: the API contract requires booleans, so a
// permission that cannot be established is false rather than null.
type Permissions struct {
	New    bool `json:"new"`
	Edit   bool `json:"edit"`
	Delete bool `json:"delete"`
	Read   bool `json:"read"`
}

// ChannelAccess is everything Telegram reported about the account's standing
// and rights in one channel.
//
// It is the single input to NormalizePermissions so that every endpoint derives
// permissions the same way. Fields the backend could not establish stay false,
// and Known says whether the standing itself is trustworthy.
type ChannelAccess struct {
	// Known is false when the standing could not be established at all.
	Known bool
	// Member is whether the account belongs to the channel.
	Member bool
	// Left is Telegram's explicit "this account left" flag.
	Left bool
	// Creator is whether the account owns the channel.
	Creator bool
	// Admin is whether the account is an administrator.
	Admin bool
	// Broadcast is whether this is a broadcast channel rather than a
	// supergroup. Subscribers of a broadcast channel cannot post.
	Broadcast bool
	// PostMessages, EditMessages and DeleteMessages are the administrative
	// rights the account holds; they only apply when Admin or Creator is set.
	PostMessages   bool
	EditMessages   bool
	DeleteMessages bool
	// ViewDenied and SendDenied are the account's own restrictions: true
	// means Telegram forbids the action.
	ViewDenied bool
	SendDenied bool
	// DefaultSendDenied is the supergroup's default restriction for ordinary
	// members.
	DefaultSendDenied bool
}

// NormalizePermissions turns Telegram's rights into the four public booleans.
//
// Semantics, and their limits:
//
//   - read: the account can fetch channel history through this client. False
//     for a non-member, because MTProto returns CHANNEL_PRIVATE to an account
//     that has not joined, even for a public channel.
//   - new: the account can generally publish a new post. A broadcast channel
//     only allows this for admins with post messages rights (or the creator);
//     a supergroup also allows ordinary members unless they are restricted.
//   - edit / delete: the account holds the channel-level right to edit or
//     delete posts. A subscriber is false even though Telegram lets anyone
//     edit their own message; the field describes the channel-level
//     capability, not one particular message. Telegram may still refuse a
//     specific message because of ownership or age.
//
// Anything that could not be established is false. An unknown standing yields
// no permissions at all rather than an optimistic guess.
func NormalizePermissions(a ChannelAccess) Permissions {
	if !a.Known || !a.Member || a.Left {
		return Permissions{}
	}
	if a.ViewDenied {
		return Permissions{}
	}

	p := Permissions{Read: true}
	canPost := !a.SendDenied && !a.DefaultSendDenied

	switch {
	case a.Creator:
		p.New, p.Edit, p.Delete = true, true, true
	case a.Admin:
		p.New = a.PostMessages || (!a.Broadcast && canPost)
		p.Edit = a.EditMessages
		p.Delete = a.DeleteMessages
	default:
		// An ordinary subscriber of a broadcast channel cannot publish; in a
		// supergroup they can unless restricted.
		p.New = !a.Broadcast && canPost
	}
	return p
}

// ChannelInfo is one channel as the worker knows it from Telegram.
//
// AccessHash is carried here because the worker needs it to address the peer
// again; it must never reach a public response.
type ChannelInfo struct {
	ID          int64
	AccessHash  int64
	Title       string
	Username    string
	Description string
	// Kind is "channel" or "group".
	Kind              string
	HasPhoto          bool
	PhotoID           int64
	Verified          bool
	Scam              bool
	Fake              bool
	ParticipantsCount *int
	// Membership is MembershipUnknown when Telegram did not say.
	Membership  Membership
	Permissions Permissions
	// UnreadCount and LastMessageID are only filled in for dialogs.
	UnreadCount   *int
	LastMessageID int
}

// JoinOutcome is what a join attempt actually achieved.
type JoinOutcome string

const (
	// JoinJoined means Telegram confirmed the account is now a member.
	JoinJoined JoinOutcome = "joined"
	// JoinAlreadyMember means the account was a member before the call.
	JoinAlreadyMember JoinOutcome = "already_member"
	// JoinPending means Telegram accepted a request that an administrator
	// must approve; membership is NOT confirmed.
	JoinPending JoinOutcome = "join_pending"
)

// JoinResult is the neutral outcome of a join call.
type JoinResult struct {
	Channel ChannelInfo
	Outcome JoinOutcome
}

// Join-flow errors the adapter classifies. Handlers branch on these rather
// than on Telegram's message strings.
var (
	// ErrJoinPending means Telegram accepted a join request that needs
	// administrator approval. Membership is not confirmed.
	ErrJoinPending = errors.New("tgapi: the join request is pending approval")
	// ErrAlreadyParticipant means Telegram reported the account is already in
	// the channel. It is resolved against the channel's real state before it
	// is reported as success.
	ErrAlreadyParticipant = errors.New("tgapi: the account is already a participant")
	// ErrJoinUnconfirmed means the join call succeeded but Telegram returned
	// nothing that proves membership.
	ErrJoinUnconfirmed = errors.New("tgapi: Telegram did not confirm the join")
	// ErrNotAMember means the account has no access to the channel yet, so it
	// cannot be addressed by id.
	ErrNotAMember = errors.New("tgapi: the account is not a member of this channel")
)

// DedupeChannels drops repeated channels, keeping the first occurrence.
//
// A result without an id (an invite Telegram did not resolve) is kept: it is
// not a duplicate of anything, and dropping it would hide a real channel.
func DedupeChannels(in []ChannelInfo) []ChannelInfo {
	if len(in) < 2 {
		if in == nil {
			return []ChannelInfo{}
		}
		return in
	}
	seen := make(map[int64]bool, len(in))
	out := make([]ChannelInfo, 0, len(in))
	for _, ch := range in {
		if ch.ID != 0 && seen[ch.ID] {
			continue
		}
		if ch.ID != 0 {
			seen[ch.ID] = true
		}
		out = append(out, ch)
	}
	return out
}
