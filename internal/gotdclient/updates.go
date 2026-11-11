package gotdclient

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/rpc"
)

// This file connects the MTProto update stream to the automation pipeline.
//
// gotd delivers every incoming update to the manager's handler. Telesync is not
// a messaging client, so until now that handler discarded them. Automation is
// the one reason to look: a new channel message is exactly the event the durable
// pipeline consumes. The handler normalizes each one to bounded metadata and
// hands it to a sink, which carries it over the private RPC to the daemon, which
// owns the database and does everything downstream.
//
// The handler must never block on delivery. Storing one event is bounded work
// and happens here so a crash cannot skip it; firing a webhook, calling an AI
// provider or publishing is the daemon's queue, and the update callback returns
// without waiting for any of it.

// IngestSink is the daemon-facing destination for one observed channel message.
//
// It is satisfied by the automation bridge client. Ingest returns once the
// daemon has durably stored the event (or recognized it as a duplicate or a
// suppression); it never waits for a webhook, an AI call or a publication.
type IngestSink interface {
	Ingest(ctx context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error)
}

// IngestSinkFunc adapts a function to IngestSink.
type IngestSinkFunc func(ctx context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error)

// Ingest implements IngestSink.
func (f IngestSinkFunc) Ingest(ctx context.Context, msg rpc.IngestMessage) (rpc.IngestOutcome, error) {
	return f(ctx, msg)
}

// updateHandler is the automation half of the update stream: it turns new
// channel messages into bounded observations and hands each to the sink.
//
// It is installed as the gotd update manager's Config.Handler, so it sees
// updates already put in order and with gaps resolved. It runs in the worker and
// holds no database; the sink carries each observation to the daemon.
type updateHandler struct {
	clientKey  string
	networkKey string
	sink       IngestSink
	now        func() time.Time
	logf       func(format string, args ...any)
}

// newUpdateHandler builds the handler. sink may be nil, in which case updates
// are normalized and dropped: automation is optional and a worker without it
// must not fail to start.
func newUpdateHandler(clientKey, networkKey string, sink IngestSink,
	now func() time.Time, logf func(format string, args ...any)) *updateHandler {
	if now == nil {
		now = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &updateHandler{
		clientKey: clientKey, networkKey: networkKey,
		sink: sink, now: now, logf: logf,
	}
}

// Handle normalizes one update batch and hands each channel message to the sink.
//
// It returns as soon as the messages are stored. It performs no network call, no
// media transfer and no downstream work: the only thing it waits for is the
// daemon's durable store, one bounded call per message. Anything the automations
// do with the message happens later, on the daemon's queue.
//
// Ordering is the point of waiting here. gotd advances pts/qts/seq by calling
// Storage.Set* only after this returns, so blocking on the store is what makes
// the event durable before the counter moves. A failure is returned, not
// swallowed: gotd's channel path then leaves the persisted counter where it is,
// so a crash or a later getDifference replays the message instead of skipping
// it. Every message is still handed over even if an earlier one failed, and the
// replayed ones are deduplicated by the daemon.
func (h *updateHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	if h.sink == nil {
		return nil
	}
	received := h.now().UTC()
	msgs := normalizeIngest(h.clientKey, h.networkKey, u, received)
	var failures []string
	for _, msg := range msgs {
		if _, err := h.sink.Ingest(ctx, msg); err != nil {
			failures = append(failures, fmt.Sprintf("message %d of channel %d: %v",
				msg.Message.ID, msg.TelegramChannelID, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("gotdclient: %d message(s) could not be stored: %s",
			len(failures), strings.Join(failures, "; "))
	}
	return nil
}

// normalizeIngest extracts the new channel messages of one update batch into
// bounded observations.
//
// Only new channel messages are observations: that is the single event type the
// automation pipeline consumes today. Everything else — edits, reactions,
// read markers, service notices — is deliberately not turned into an event.
// Media is metadata only: the bytes are never copied here and travel later
// through the authorized media pipeline.
func normalizeIngest(clientKey, networkKey string, u tg.UpdatesClass, received time.Time) []rpc.IngestMessage {
	var out []rpc.IngestMessage
	forEachUpdate(u, func(msg *tg.Message, channelID int64, users map[int64]*tg.User) {
		out = append(out, rpc.IngestMessage{
			ClientKey:         clientKey,
			NetworkKey:        networkKey,
			TelegramChannelID: channelID,
			Message:           rpcMessageFromTG(msg, users, channelID),
			GroupedID:         msg.GroupedID,
			ReceivedAt:        received,
		})
	})
	return out
}

// forEachUpdate walks the new channel messages of an update batch in order,
// pairing each with its channel id and the batch's user table (for sender names).
func forEachUpdate(u tg.UpdatesClass, f func(msg *tg.Message, channelID int64, users map[int64]*tg.User)) {
	var updates []tg.UpdateClass
	var users []tg.UserClass
	switch v := u.(type) {
	case *tg.Updates:
		updates, users = v.Updates, v.Users
	case *tg.UpdatesCombined:
		updates, users = v.Updates, v.Users
	default:
		return
	}
	userByID := make(map[int64]*tg.User, len(users))
	for _, uc := range users {
		if usr, ok := uc.(*tg.User); ok {
			userByID[usr.ID] = usr
		}
	}
	for _, up := range updates {
		newMsg, ok := up.(*tg.UpdateNewChannelMessage)
		if !ok {
			continue
		}
		msg, ok := newMsg.Message.(*tg.Message)
		if !ok {
			continue
		}
		channelID := channelIDOf(msg)
		if channelID == 0 {
			continue
		}
		f(msg, channelID, userByID)
	}
}

// channelIDOf returns the channel a message was posted in, or zero when it was
// not a channel message. Only a channel message is an automation source.
func channelIDOf(msg *tg.Message) int64 {
	if peer, ok := msg.PeerID.(*tg.PeerChannel); ok {
		return peer.ChannelID
	}
	return 0
}

// rpcMessageFromTG builds the bounded, media-metadata-only message an
// observation carries.
//
// It mirrors the worker's own message normalization (normalizeMessage +
// messageDTO) so a message read through the API and a message seen on the update
// stream describe the same facts. The attachment is named but never fetched: the
// bytes stay on Telegram until an authorized reader asks for them.
func rpcMessageFromTG(msg *tg.Message, users map[int64]*tg.User, channelID int64) rpc.Message {
	base := normalizeMessage(msg, users)
	out := rpc.Message{
		ID:               base.ID,
		Date:             base.Date,
		Text:             base.Text,
		GroupedID:        base.GroupedID,
		ReplyToMessageID: base.ReplyToMessageID,
		Views:            base.Views,
		Reactions:        make([]rpc.Reaction, 0, len(base.Reactions)),
		Entities:         make([]rpc.Entity, 0, len(base.Entities)),
	}
	for _, r := range base.Reactions {
		out.Reactions = append(out.Reactions, rpc.Reaction{
			Type:       r.Type,
			Emoji:      r.Emoji,
			DocumentID: r.DocumentID,
			Count:      r.Count,
			Chosen:     r.Chosen,
		})
	}
	for _, e := range base.Entities {
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
	if base.Sender != nil {
		out.Sender = &rpc.Sender{
			ID:       base.Sender.ID,
			Name:     base.Sender.Name,
			Username: base.Sender.Username,
		}
	}
	if base.Media != nil {
		out.Media = &rpc.Media{
			ID:       fmt.Sprintf("%d:%d", channelID, base.ID),
			Type:     base.Media.Type,
			Label:    base.Media.Label,
			Filename: base.Media.Filename,
			MimeType: base.Media.MimeType,
			Size:     base.Media.Size,
			Duration: base.Media.Duration,
			Waveform: base.Media.Waveform,
		}
	}
	return out
}
