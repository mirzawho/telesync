// Package worker runs exactly one Telegram client and serves the private RPC
// interface the gateway talks to.
//
// A worker owns its client, its session, its connection lifecycle and its
// socket. It never creates a client per request: the client is started once and
// kept alive for the process lifetime.
package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// MaxCachedChannels bounds the per-worker pagination caches. A worker handling
// many channels re-reads the dialog snapshot rather than growing without limit.
const MaxCachedChannels = 64

// Options configures a worker service.
type Options struct {
	// PID is the worker's process id, reported in health.
	PID int
	// StartedAt is when the process started. Zero means now.
	StartedAt time.Time
	// Now overrides the clock (tests).
	Now func() time.Time
	// CacheTTL bounds how long a dialog snapshot is reused. Zero means 60s.
	CacheTTL time.Duration
}

// Option adjusts a service after construction.
type Option func(*Service)

// WithLogf sends the service's log lines to logf.
//
// The log is where an adapter error keeps its real text: the RPC answer carries
// a code, and an unclassified error becomes "internal", which names nothing.
func WithLogf(logf func(format string, args ...any)) Option {
	return func(s *Service) { s.logf = logf }
}

func (s *Service) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

// Telegram is the slice of a Telegram client a worker needs.
//
// It never marks anything read and never logs anyone in. Joining, leaving and
// sending a prepared post are the only writes it performs, each reached from
// its own documented endpoint and never as a side effect of a read. The
// concrete implementation is the gotd adapter.
type Telegram interface {
	Status(ctx context.Context) tgapi.Status
	Dialogs(ctx context.Context) ([]tgapi.Channel, error)
	History(ctx context.Context, channel tgapi.Channel, beforeID, limit int) (tgapi.HistoryPage, error)
	DownloadMedia(ctx context.Context, channel tgapi.Channel, messageID int) (tgapi.MediaFile, error)
	Message(ctx context.Context, channel tgapi.Channel, messageID int) (tgapi.Message, error)

	// FindChannels resolves a discovery query against Telegram.
	FindChannels(ctx context.Context, q tgapi.ChannelQuery) ([]tgapi.ChannelInfo, error)
	// ResolveChannel returns one channel's current Telegram state.
	ResolveChannel(ctx context.Context, ref tgapi.ChannelRef) (tgapi.ChannelInfo, error)
	// JoinChannel joins when needed and reports what Telegram confirmed.
	JoinChannel(ctx context.Context, ref tgapi.ChannelRef) (tgapi.JoinResult, error)
	// DownloadChannelAvatar fetches the channel's profile photo.
	DownloadChannelAvatar(ctx context.Context, ref tgapi.ChannelRef) (tgapi.AvatarFile, error)
	// DownloadCustomEmoji fetches a reaction custom emoji's bytes by document
	// id; tgapi.ErrCustomEmojiNotFound when Telegram does not serve it.
	DownloadCustomEmoji(ctx context.Context, documentID int64) (tgapi.EmojiFile, error)
	// LeaveChannel makes the account leave the channel. It is the only other
	// write this interface performs, and only for the documented leave route.
	// An account that is not in the channel is tgapi.ErrNotAMember.
	LeaveChannel(ctx context.Context, ref tgapi.ChannelRef) error
	// SendMessage sends one prepared post to a channel this account can reach.
	// It is the only write to Telegram's content this interface performs, and
	// it is reached only from an explicit publishing request.
	SendMessage(ctx context.Context, ref tgapi.ChannelRef, req tgapi.SendRequest) (tgapi.SendResult, error)
}

// Service implements rpc.Backend on top of one Telegram client.
type Service struct {
	client appconfig.Client
	tg     Telegram
	opt    Options
	logf   func(format string, args ...any)

	mu sync.Mutex
	// snapshot is the last dialog list, reused for a short window so paging
	// through it stays consistent.
	snapshot    []tgapi.Channel
	snapshotAt  time.Time
	snapshotErr error

	// cursors holds the message-id cursor for the next page of each channel.
	cursors map[int64]int
	// order records insertion order so the cache can evict the oldest entry.
	order []int64
}

// NewService builds a worker service.
func NewService(cfg appconfig.Client, tg Telegram, opt Options, adjust ...Option) *Service {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.StartedAt.IsZero() {
		opt.StartedAt = opt.Now()
	}
	if opt.CacheTTL <= 0 {
		opt.CacheTTL = time.Minute
	}
	s := &Service{
		client:  cfg,
		tg:      tg,
		opt:     opt,
		cursors: map[int64]int{},
	}
	for _, fn := range adjust {
		fn(s)
	}
	return s
}

// Health implements rpc.Backend.
//
// The three questions are answered separately: does the process answer, is the
// transport up, and is the account authorized.
func (s *Service) Health(ctx context.Context) rpc.Health {
	st := s.tg.Status(ctx)
	now := s.opt.Now()

	h := rpc.Health{
		Key:        s.client.Key,
		Type:       string(s.client.Type),
		PID:        s.opt.PID,
		StartedAt:  s.opt.StartedAt.UTC(),
		ThisDC:     st.ThisDC,
		RPCReady:   true,
		Transport:  transportState(st.State),
		State:      workerState(st.State),
		Authorized: st.State == tgapi.StateReady && st.User != nil,
		LastError:  st.Err,
	}
	if uptime := now.Sub(s.opt.StartedAt); uptime > 0 {
		h.UptimeSeconds = int64(uptime.Seconds())
	}
	if s.client.SessionPath != "" {
		// Stat, not Inspect: health is polled and must not read the
		// credentials file's body just to report that it exists.
		if _, err := os.Stat(s.client.SessionPath); err == nil {
			h.SessionPresent = true
		}
	}
	if !st.LastSuccess.IsZero() {
		success := st.LastSuccess.UTC()
		h.LastSuccessAt = &success
	}
	return h
}

// workerState maps the adapter's state onto the worker lifecycle states.
func workerState(state tgapi.State) rpc.WorkerState {
	switch state {
	case tgapi.StateReady:
		return rpc.StateConnected
	case tgapi.StateUnauthorized:
		return rpc.StateNeedsAuth
	case tgapi.StateError:
		return rpc.StateError
	case tgapi.StateConnecting:
		return rpc.StateStarting
	default:
		return rpc.StateDisconnected
	}
}

// transportState describes the MTProto transport independently of the session.
func transportState(state tgapi.State) string {
	switch state {
	case tgapi.StateReady, tgapi.StateUnauthorized:
		return "ready"
	case tgapi.StateConnecting:
		return "connecting"
	case tgapi.StateError:
		return "error"
	default:
		return "disconnected"
	}
}

// Profile implements rpc.Backend.
func (s *Service) Profile(ctx context.Context) (rpc.Profile, error) {
	st := s.tg.Status(ctx)
	p := rpc.Profile{
		Key:                s.client.Key,
		Type:               string(s.client.Type),
		AuthorizationState: rpc.AuthUnknown,
		ThisDC:             st.ThisDC,
	}
	if s.client.Server != nil {
		p.Endpoint = s.client.Server.Endpoint()
	}
	switch {
	case st.State == tgapi.StateReady && st.User != nil:
		p.Authorized = true
		p.AuthorizationState = rpc.AuthAuthorized
		p.User = &rpc.Account{
			ID:        st.User.ID,
			FirstName: st.User.FirstName,
			LastName:  st.User.LastName,
			Username:  st.User.Username,
		}
	case st.State == tgapi.StateUnauthorized:
		p.AuthorizationState = rpc.AuthNeedsAuth
	}
	return p, nil
}

// Channels implements rpc.Backend. Each page is sliced from a dialog snapshot
// that is reused briefly, so a client walking pages does not re-download the
// whole dialog list for every request.
func (s *Service) Channels(ctx context.Context, page, perPage int) (rpc.ChannelPage, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.ChannelPage{}, err
	}
	page, perPage = rpc.NormalizePage(page, perPage)

	dialogs, err := s.dialogSnapshot(ctx)
	if err != nil {
		return rpc.ChannelPage{}, err
	}

	total := len(dialogs)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	channels := make([]rpc.Channel, 0, end-start)
	for _, d := range dialogs[start:end] {
		channels = append(channels, channelDTO(d))
	}

	info := rpc.PageInfo{
		Page:    page,
		PerPage: perPage,
		Total:   total,
		HasMore: end < total,
	}
	if info.HasMore {
		info.NextPage = page + 1
	}
	return rpc.ChannelPage{Channels: channels, Page: info}, nil
}

func channelDTO(c tgapi.Channel) rpc.Channel {
	out := rpc.Channel{
		ID:            c.ID,
		Title:         c.Title,
		Username:      c.Username,
		Kind:          c.Kind,
		HasAvatar:     c.HasPhoto,
		LastMessageID: c.LastMessageID,
		Permissions: rpc.Permissions{
			New:    c.Permissions.New,
			Edit:   c.Permissions.Edit,
			Delete: c.Permissions.Delete,
			Read:   c.Permissions.Read,
		},
	}
	if c.UnreadCount != nil {
		// Copy: the value belongs to the caller, not the adapter.
		v := *c.UnreadCount
		out.UnreadCount = &v
	}
	return out
}

// dialogSnapshot returns the cached dialog list, refreshing it when stale.
func (s *Service) dialogSnapshot(ctx context.Context) ([]tgapi.Channel, error) {
	s.mu.Lock()
	fresh := s.snapshot != nil && s.opt.Now().Sub(s.snapshotAt) < s.opt.CacheTTL
	snapshot := s.snapshot
	err := s.snapshotErr
	s.mu.Unlock()
	if fresh {
		return snapshot, err
	}

	dialogs, fetchErr := s.tg.Dialogs(ctx)
	s.mu.Lock()
	s.snapshot = dialogs
	s.snapshotAt = s.opt.Now()
	s.snapshotErr = fetchErr
	s.mu.Unlock()
	if fetchErr != nil {
		return nil, fetchErr
	}
	return dialogs, nil
}

// invalidateSnapshot drops the cached dialog list, so the next page is read
// from Telegram. A membership change makes the snapshot wrong, not stale.
func (s *Service) invalidateSnapshot() {
	s.mu.Lock()
	s.snapshot = nil
	s.snapshotAt = time.Time{}
	s.snapshotErr = nil
	s.mu.Unlock()
}

// Messages implements rpc.Backend. Reading history never marks it read: the
// adapter's History only calls messages.getHistory.
func (s *Service) Messages(ctx context.Context, channelID int64, page, perPage int) (rpc.MessagePage, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.MessagePage{}, err
	}
	page, perPage = rpc.NormalizePage(page, perPage)

	channel, ok := s.channelByID(ctx, channelID)
	if !ok {
		return rpc.MessagePage{}, rpc.ErrChannelNotFound
	}

	beforeID := s.cursorFor(channelID, page)
	hist, err := s.tg.History(ctx, channel, beforeID, perPage)
	if err != nil {
		return rpc.MessagePage{}, err
	}

	messages := make([]rpc.Message, 0, len(hist.Messages))
	oldest := 0
	for _, m := range hist.Messages {
		messages = append(messages, s.messageDTO(channelID, m))
		if oldest == 0 || m.ID < oldest {
			oldest = m.ID
		}
	}
	if oldest > 0 {
		s.setCursor(channelID, oldest)
	}

	info := rpc.PageInfo{
		Page:    page,
		PerPage: perPage,
		HasMore: hist.HasMore,
	}
	if hist.HasMore {
		info.NextPage = page + 1
	}
	return rpc.MessagePage{Channel: channelDTO(channel), Messages: messages, Page: info}, nil
}

// Message implements rpc.Backend: exactly one post, not a page.
func (s *Service) Message(ctx context.Context, channelID int64, messageID int) (rpc.MessagePage, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.MessagePage{}, err
	}
	if messageID <= 0 {
		return rpc.MessagePage{}, rpc.NewError(rpc.CodeInvalidInput, "message_id must be positive", 0)
	}
	channel, ok := s.channelByID(ctx, channelID)
	if !ok {
		return rpc.MessagePage{}, rpc.ErrChannelNotFound
	}

	msg, err := s.tg.Message(ctx, channel, messageID)
	if err != nil {
		if errors.Is(err, tgapi.ErrMessageNotFound) {
			return rpc.MessagePage{}, rpc.NewError(rpc.CodeNotFound,
				"No message with that id exists in this channel.", 0)
		}
		return rpc.MessagePage{}, err
	}

	return rpc.MessagePage{
		Channel:  channelDTO(channel),
		Messages: []rpc.Message{s.messageDTO(channelID, msg)},
		Page:     rpc.PageInfo{Page: 1, PerPage: 1, Total: 1},
	}, nil
}

// messageDTO normalizes one message, including its attachment descriptor.
func (s *Service) messageDTO(channelID int64, m tgapi.Message) rpc.Message {
	out := rpc.Message{
		ID:               m.ID,
		Date:             m.Date.UTC(),
		Text:             m.Text,
		ReplyToMessageID: m.ReplyToMessageID,
		Views:            m.Views,
		Reactions:        make([]rpc.Reaction, 0, len(m.Reactions)),
		Entities:         make([]rpc.Entity, 0, len(m.Entities)),
	}
	for _, r := range m.Reactions {
		out.Reactions = append(out.Reactions, rpc.Reaction{
			Type:       r.Type,
			Emoji:      r.Emoji,
			DocumentID: r.DocumentID,
			Count:      r.Count,
			Chosen:     r.Chosen,
		})
	}
	for _, e := range m.Entities {
		out.Entities = append(out.Entities, rpc.Entity{
			Type:       e.Type,
			Offset:     e.Offset,
			Length:     e.Length,
			URL:        e.URL,
			Language:   e.Language,
			UserID:     e.UserID,
			DocumentID: e.DocumentID,
		})
	}
	if m.Sender != nil {
		out.Sender = &rpc.Sender{
			ID:       m.Sender.ID,
			Name:     m.Sender.Name,
			Username: m.Sender.Username,
		}
	}
	if m.Media != nil {
		out.Media = &rpc.Media{
			ID:       fmt.Sprintf("%d:%d", channelID, m.ID),
			Type:     m.Media.Type,
			Label:    m.Media.Label,
			MimeType: m.Media.MimeType,
			Size:     m.Media.Size,
		}
	}
	return out
}

// Media implements rpc.Backend.
//
// It fetches the attachment of one message. A message without one is a
// not-found, not an internal error: the caller asked for something that does
// not exist.
func (s *Service) Media(ctx context.Context, channelID int64, messageID, _ int) (rpc.MediaFile, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.MediaFile{}, err
	}
	if messageID <= 0 {
		return rpc.MediaFile{}, rpc.NewError(rpc.CodeInvalidInput, "message_id must be positive", 0)
	}
	channel, ok := s.channelByID(ctx, channelID)
	if !ok {
		return rpc.MediaFile{}, rpc.ErrChannelNotFound
	}
	file, err := s.tg.DownloadMedia(ctx, channel, messageID)
	if err != nil {
		if errors.Is(err, tgapi.ErrMediaNotFound) {
			return rpc.MediaFile{}, rpc.NewError(rpc.CodeNotFound,
				"This message has no downloadable media.", 0)
		}
		return rpc.MediaFile{}, err
	}
	return rpc.MediaFile{
		MediaID:  fmt.Sprintf("%d:%d", channelID, messageID),
		MimeType: file.MimeType,
		Data:     file.Data,
	}, nil
}

// CustomEmoji implements rpc.Backend: the bytes of one custom emoji.
//
// Custom emoji are not tied to a channel, so this route needs no channel
// lookup; it is read-only like every other GET.
func (s *Service) CustomEmoji(ctx context.Context, documentID int64) (rpc.MediaFile, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.MediaFile{}, err
	}
	if documentID <= 0 {
		return rpc.MediaFile{}, rpc.NewError(rpc.CodeInvalidInput, "document_id must be positive", 0)
	}
	file, err := s.tg.DownloadCustomEmoji(ctx, documentID)
	if err != nil {
		if errors.Is(err, tgapi.ErrCustomEmojiNotFound) {
			return rpc.MediaFile{}, rpc.NewError(rpc.CodeNotFound,
				"This custom emoji is not available.", 0)
		}
		return rpc.MediaFile{}, err
	}
	return rpc.MediaFile{MediaID: fmt.Sprintf("emoji:%d", documentID), MimeType: file.MimeType, Data: file.Data}, nil
}

// cursorFor returns the message id to page below for the given page number.
//
// Telegram pages by message id, not by page number. The first page is always
// "the newest", and every later page continues below the oldest message the
// worker already served. A page the cache has never seen yet restarts from the
// newest page rather than inventing an offset.
func (s *Service) cursorFor(channelID int64, page int) int {
	if page <= 1 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursors[channelID]
}

func (s *Service) setCursor(channelID int64, oldest int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.cursors[channelID]; !seen {
		s.order = append(s.order, channelID)
	}
	s.cursors[channelID] = oldest
	for len(s.order) > MaxCachedChannels {
		evict := s.order[0]
		s.order = s.order[1:]
		delete(s.cursors, evict)
	}
}

func (s *Service) channelByID(ctx context.Context, channelID int64) (tgapi.Channel, bool) {
	dialogs, err := s.dialogSnapshot(ctx)
	if err != nil || len(dialogs) == 0 {
		// No snapshot to check against (fresh process, or Telegram failed).
		// Proceed with the identifier alone: Telegram itself rejects a channel
		// the account cannot read, which is a truthful answer.
		return tgapi.Channel{ID: channelID}, true
	}
	for _, d := range dialogs {
		if d.ID == channelID {
			return d, true
		}
	}
	return tgapi.Channel{}, false
}

// requireAuthorized rejects work that cannot succeed before touching Telegram.
func (s *Service) requireAuthorized(ctx context.Context) error {
	switch st := s.tg.Status(ctx); st.State {
	case tgapi.StateReady:
		return nil
	case tgapi.StateUnauthorized:
		return rpc.ErrNeedsAuth
	default:
		return rpc.ErrNotConnected
	}
}

// CachedChannels reports how many channels currently hold a pagination cursor.
func (s *Service) CachedChannels() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cursors)
}
