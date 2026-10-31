package tgapi

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// QueryKind is how a discovery query has to be resolved.
type QueryKind string

const (
	// QueryUsername resolves a public @username or t.me/username link.
	QueryUsername QueryKind = "username"
	// QueryInvite inspects a private invite link (t.me/+hash).
	QueryInvite QueryKind = "invite"
	// QueryID looks a channel up by its numeric identifier.
	QueryID QueryKind = "id"
	// QueryText is a free-text search.
	QueryText QueryKind = "text"
)

// MaxQueryLength bounds the discovery query so malformed or absurdly broad
// input never reaches Telegram.
const MaxQueryLength = 256

// ChannelQuery is one parsed discovery query.
type ChannelQuery struct {
	Kind QueryKind
	// Raw is the query as the caller supplied it.
	Raw string
	// Username is set for QueryUsername, without the leading "@".
	Username string
	// Invite is set for QueryInvite, without the t.me/+ prefix.
	Invite string
	// ChannelID is set for QueryID, always positive.
	ChannelID int64
	// Text is set for QueryText.
	Text string
}

var (
	usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{2,31}$`)
	invitePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	numericPattern  = regexp.MustCompile(`^-?[0-9]{1,19}$`)
)

// linkHosts are the hosts a Telegram channel link may use.
var linkHosts = []string{"t.me/", "telegram.me/", "www.t.me/", "www.telegram.me/"}

// ParseChannelQuery classifies one query string.
//
// The forms it understands are a public username, a public t.me link, a private
// invite link, a numeric channel id, and free text. Anything else is text: it
// is Telegram that decides whether a text search finds something.
func ParseChannelQuery(raw string) (ChannelQuery, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return ChannelQuery{}, fmt.Errorf("q is required")
	}
	if len(q) > MaxQueryLength {
		return ChannelQuery{}, fmt.Errorf("q must be at most %d characters", MaxQueryLength)
	}
	if strings.ContainsAny(q, "\r\n\t") {
		return ChannelQuery{}, fmt.Errorf("q must not contain line breaks or tabs")
	}

	out := ChannelQuery{Raw: q}

	// A numeric id, in either the bare Telegram form (1671633563) or the
	// Bot-API supergroup form (-1001671633563).
	if numericPattern.MatchString(q) {
		id, err := ParseChannelID(q)
		if err != nil {
			return ChannelQuery{}, err
		}
		out.Kind = QueryID
		out.ChannelID = id
		return out, nil
	}

	// A public username with the @ prefix.
	if rest, ok := strings.CutPrefix(q, "@"); ok {
		if !usernamePattern.MatchString(rest) {
			return ChannelQuery{}, fmt.Errorf("q looks like a username but %q is not a valid one", rest)
		}
		out.Kind = QueryUsername
		out.Username = rest
		return out, nil
	}

	// A link, with or without a scheme.
	if rest, ok := cutLink(q); ok {
		if invite, ok := cutInvite(rest); ok {
			if !invitePattern.MatchString(invite) {
				return ChannelQuery{}, fmt.Errorf("q looks like an invite link but %q is not a valid invite hash", invite)
			}
			out.Kind = QueryInvite
			out.Invite = invite
			return out, nil
		}
		// t.me/s/<username> is the public web preview of a channel.
		rest = strings.TrimPrefix(rest, "s/")
		rest = strings.TrimSuffix(rest, "/")
		if usernamePattern.MatchString(rest) {
			out.Kind = QueryUsername
			out.Username = rest
			return out, nil
		}
		return ChannelQuery{}, fmt.Errorf("q looks like a link but %q is not a channel username or invite", q)
	}

	// A bare username without the @ prefix is ambiguous with free text, so it
	// stays text: Telegram's own search resolves it either way.
	out.Kind = QueryText
	out.Text = q
	return out, nil
}

// cutLink returns the path part of a Telegram link, if q is one. The scheme is
// optional: people paste "t.me/example" as often as the full URL.
func cutLink(q string) (string, bool) {
	lower := strings.ToLower(q)
	for _, host := range linkHosts {
		for _, prefix := range []string{"https://", "http://", ""} {
			if rest, ok := strings.CutPrefix(lower, prefix+host); ok {
				return q[len(q)-len(rest):], true
			}
		}
	}
	return "", false
}

// cutInvite extracts the hash from an invite path.
func cutInvite(rest string) (string, bool) {
	if hash, ok := strings.CutPrefix(rest, "+"); ok {
		return strings.TrimSuffix(hash, "/"), true
	}
	if hash, ok := strings.CutPrefix(rest, "joinchat/"); ok {
		return strings.TrimSuffix(hash, "/"), true
	}
	return "", false
}

// ParseChannelID normalizes a channel identifier to Telegram's own positive
// channel id.
//
// It accepts the bare form (1671633563) and the Bot-API supergroup form
// (-1001671633563), because both are common in links and user input.
func ParseChannelID(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("channel_id is required")
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("channel_id must be a decimal integer")
	}
	switch {
	case v > 0:
		return v, nil
	case v == 0:
		return 0, fmt.Errorf("channel_id must not be zero")
	}
	magnitude := -v
	// -100XXXXXXXXXX is the Bot API rendering of a supergroup/channel id.
	const botAPIPrefix = 1000000000000
	if magnitude > botAPIPrefix {
		if rest := magnitude - botAPIPrefix; rest > 0 {
			return rest, nil
		}
	}
	return magnitude, nil
}

// FormatChannelID renders a channel id for the public API.
func FormatChannelID(id int64) string { return strconv.FormatInt(id, 10) }

// ChannelRef addresses one channel for the join and avatar endpoints.
//
// A numeric reference needs peer information the worker may not have; an invite
// reference carries its own. Keeping both in one type is what lets the join and
// avatar routes accept either form.
type ChannelRef struct {
	// ID is the positive Telegram channel id, or zero for an invite reference.
	ID int64
	// Invite is the invite hash, without the t.me/+ prefix.
	Invite string
}

// IsInvite reports whether this reference is an invite link.
func (r ChannelRef) IsInvite() bool { return r.Invite != "" }

// String renders the reference the way it appears in a URL path segment.
func (r ChannelRef) String() string {
	if r.Invite != "" {
		return "+" + r.Invite
	}
	return FormatChannelID(r.ID)
}

// ParseChannelRef reads a channel reference from a URL path segment.
//
// Accepted forms are a decimal channel id (with or without the -100 prefix) and
// an invite hash written as "+hash" or "joinchat/hash".
func ParseChannelRef(raw string) (ChannelRef, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ChannelRef{}, fmt.Errorf("channel reference is required")
	}
	if len(s) > MaxQueryLength {
		return ChannelRef{}, fmt.Errorf("channel reference is too long")
	}
	if hash, ok := cutInvite(s); ok {
		if !invitePattern.MatchString(hash) {
			return ChannelRef{}, fmt.Errorf("invite hash is not valid")
		}
		return ChannelRef{Invite: hash}, nil
	}
	id, err := ParseChannelID(s)
	if err != nil {
		return ChannelRef{}, err
	}
	return ChannelRef{ID: id}, nil
}

// AvatarFile is a channel's profile photo.
type AvatarFile struct {
	// MimeType is what the backend reported, when it reported anything.
	MimeType string
	Data     []byte
	// PhotoID is Telegram's photo version; it changes when the avatar changes.
	// Zero means the backend did not report one.
	PhotoID int64
}

// ErrNoAvatar means the channel exists but has no profile photo.
var ErrNoAvatar = errors.New("tgapi: this channel has no avatar")

// Join-flow errors the adapter classifies so handlers never parse RPC strings.
var (
	// ErrInviteExpired means the invite link is no longer valid.
	ErrInviteExpired = errors.New("tgapi: the invite link has expired")
	// ErrInviteInvalid means the invite link is malformed or revoked.
	ErrInviteInvalid = errors.New("tgapi: the invite link is not valid")
	// ErrMembershipLimit means the account cannot join any more channels.
	ErrMembershipLimit = errors.New("tgapi: this account has reached a membership limit")
	// ErrNumericIDUnresolved means the worker has no peer information for a
	// numeric channel id. Telegram needs the access hash, which only comes from
	// a prior resolution; guessing one would be a fabricated peer.
	ErrNumericIDUnresolved = errors.New("tgapi: no peer information for this numeric channel id")
)
