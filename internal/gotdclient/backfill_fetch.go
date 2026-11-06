package gotdclient

import (
	"context"
	"sort"
	"time"

	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// This file is the Telegram side of the bounded gap backfill: the reader that
// lists the messages a gap left behind, and the acceptor that turns each into
// the same durable event the live update path produces.
//
// Neither is exercised against a live account here — this checkout has no
// Telegram credentials — so the guarantees they are held to are the bounded ones
// the runner already enforces: at most one batch per pass, oldest first, and the
// checkpoint only advances past what was accepted.

// backfillRead lists up to limit message ids in a channel that are newer than
// afterID, oldest first.
//
// It pages forward from afterID: the messages immediately after the checkpoint,
// not the newest ones, so repeated passes drain a gap oldest-first instead of
// jumping to the present and leaving a hole behind. The bound is honored on both
// ends — Telegram is asked for at most limit, and at most limit are returned.
func (m *Manager) backfillRead(ctx context.Context, channelID, afterID int64, limit int) ([]int64, error) {
	_, api, err := m.apiClient(ctx)
	if err != nil {
		return nil, err
	}
	hash, _ := m.peerHash(channelID)
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:      &tg.InputPeerChannel{ChannelID: channelID, AccessHash: hash},
		OffsetID:  int(afterID),
		AddOffset: -limit,
		Limit:     limit,
		MinID:     int(afterID),
	})
	if err != nil {
		return nil, ClassifyError(err)
	}

	ids := make([]int64, 0, limit)
	for _, mc := range historyMessages(res) {
		if msg, ok := mc.(*tg.Message); ok && int64(msg.ID) > afterID {
			ids = append(ids, int64(msg.ID))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

// backfillAccept turns one recovered message into the same durable event the
// live update path stores, by handing it to the sink. It fetches the message's
// metadata only — media bytes stay on Telegram and travel later through the
// authorized media pipeline.
func (m *Manager) backfillAccept(ctx context.Context, channelID, messageID int64) error {
	if m.automation == nil || m.automation.Sink == nil {
		return nil
	}
	hash, _ := m.peerHash(channelID)
	msg, err := m.channelMessage(ctx, tgapi.Channel{ID: channelID, AccessHash: hash}, int(messageID))
	if err != nil {
		return err
	}
	ingest := rpc.IngestMessage{
		ClientKey:         m.spec.Key,
		NetworkKey:        m.automation.NetworkKey,
		TelegramChannelID: channelID,
		Message:           rpcMessageFromTG(msg, nil, channelID),
		GroupedID:         msg.GroupedID,
		ReceivedAt:        time.Now().UTC(),
	}
	_, err = m.automation.Sink.Ingest(ctx, ingest)
	return err
}

// historyMessages lists the messages of a getHistory result.
func historyMessages(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return v.Messages
	case *tg.MessagesMessagesSlice:
		return v.Messages
	case *tg.MessagesChannelMessages:
		return v.Messages
	default:
		return nil
	}
}
