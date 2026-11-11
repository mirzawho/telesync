package main

import (
	"context"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// The Telegram side of the probe: one fixed, healthy client named doc-client,
// answering every call with a small documented shape. The generator never
// asserts on these values; they exist so a route that reads Telegram answers
// 200 with a body instead of failing.

type workerRegistry struct{}

func (workerRegistry) Worker(key string) (gateway.Worker, bool) {
	switch key {
	case "doc-client", "official":
		return documentedWorker{}, true
	}
	return nil, false
}

type documentedWorker struct{}

func (documentedWorker) Health(context.Context) (rpc.Health, error) {
	return rpc.Health{Key: "doc-client", Type: "official", State: rpc.StateConnected,
		Authorized: true, Transport: "ready", RPCReady: true, PID: 1, ThisDC: 2}, nil
}

func (documentedWorker) Profile(context.Context) (rpc.Profile, error) {
	return rpc.Profile{Key: "doc-client", Type: "official", Authorized: true,
		AuthorizationState: rpc.AuthAuthorized, User: &rpc.Account{ID: 1}, ThisDC: 2}, nil
}

func (documentedWorker) Channels(context.Context, int, int) (rpc.ChannelPage, error) {
	return rpc.ChannelPage{
		Channels: []rpc.Channel{{ID: 1, Title: "Documented channel", Kind: "channel",
			LastMessageID: 1, Permissions: rpc.Permissions{Read: true}}},
		Page: rpc.PageInfo{Page: 1, PerPage: 20, Total: 1},
	}, nil
}

func (documentedWorker) Messages(context.Context, int64, int, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{Messages: []rpc.Message{documentedMessage()},
		Page: rpc.PageInfo{Page: 1, PerPage: 20}}, nil
}

func (documentedWorker) Message(context.Context, int64, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{Messages: []rpc.Message{documentedMessage()}}, nil
}

func documentedMessage() rpc.Message {
	return rpc.Message{
		ID: 1, Date: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC), Text: "Documented post",
		Sender: &rpc.Sender{ID: 1, Name: "Documented", Username: "documented"},
		Media:  &rpc.Media{ID: "1:1", Type: "photo", Label: "photo", MimeType: "image/jpeg", Size: 5},
	}
}

func (documentedWorker) Media(_ context.Context, channelID int64, messageID, _ int, slice rpc.MediaSlice) (rpc.MediaStream, error) {
	return rpc.MediaStream{
		MimeType: "text/plain",
		Filename: "documented.txt",
		Size:     5,
		Offset:   slice.Offset,
		Length:   5,
		Body:     io.NopCloser(strings.NewReader("bytes")),
	}, nil
}

func (documentedWorker) Find(context.Context, rpc.FindRequest) (rpc.FindResult, error) {
	return rpc.FindResult{Channels: []rpc.ChannelInfo{documentedChannel()}}, nil
}

func (documentedWorker) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	return rpc.ChannelResult{Channel: documentedChannel()}, nil
}

func (documentedWorker) Join(context.Context, rpc.ChannelRef) (rpc.JoinResult, error) {
	return rpc.JoinResult{Channel: documentedChannel(), Outcome: rpc.OutcomeJoined}, nil
}

func (documentedWorker) CustomEmoji(context.Context, int64) (rpc.MediaFile, error) {
	return rpc.MediaFile{MimeType: "application/x-tgsticker", Data: []byte("emoji-bytes")}, nil
}

func (documentedWorker) Avatar(context.Context, rpc.ChannelRef) (rpc.AvatarFile, error) {
	return rpc.AvatarFile{MimeType: "image/jpeg", PhotoID: 9, Data: []byte("jpeg-bytes")}, nil
}

func (documentedWorker) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	return rpc.SendResult{
		Message:   &rpc.Message{ID: 12345, Text: req.Text, Date: time.Now().UTC()},
		ChannelID: strconv.FormatInt(req.ChannelID, 10),
	}, nil
}

func (documentedWorker) EditMessage(_ context.Context, req rpc.EditRequest) (rpc.EditResult, error) {
	return rpc.EditResult{Message: &rpc.Message{ID: req.MessageID, Text: req.Text}}, nil
}

func (documentedWorker) DeleteMessage(_ context.Context, req rpc.DeleteMessageRequest) (rpc.DeleteMessageResult, error) {
	return rpc.DeleteMessageResult{Deleted: true}, nil
}

func (documentedWorker) Leave(context.Context, rpc.ChannelRef) (rpc.LeaveResult, error) {
	return rpc.LeaveResult{}, nil
}

func documentedChannel() rpc.ChannelInfo {
	return rpc.ChannelInfo{
		ID: "1", Type: "channel", Title: "Documented channel", IsMember: true,
		MembershipStatus: "member", AvatarAvailable: true, AvatarVersion: 9,
		Permissions: rpc.Permissions{Read: true},
	}
}

// managedStore is the local channel record: the join route writes to it, and
// the local channel routes list and remove from it. It holds one documented
// record (local id 1, Telegram id 1, client doc-client) so the local channel
// pages have a page to observe.
type managedStore struct{}

// documentedRecord is the record the local channel detail documents: what the
// join route writes for a channel the worker reports as a member the account
// may read.
func documentedRecord() channelstore.Channel {
	return channelstore.Channel{
		ID: 1, ClientKey: "doc-client", ChannelID: 1, PeerType: channelstore.PeerChannel,
		Title: "Documented channel", MembershipStatus: channelstore.StatusMember,
		AvatarPhotoID: 9, AvatarAvailable: true, NoticesKnown: true,
		Permissions: tgapi.Permissions{Read: true}, PermissionsKnown: true,
	}
}

func (managedStore) Get(_ context.Context, clientKey string, telegramID int64) (channelstore.Channel, bool, error) {
	if clientKey == "official" && telegramID == 1671633563 {
		return channelstore.Channel{ID: 2, ClientKey: clientKey, ChannelID: telegramID, PeerType: channelstore.PeerChannel,
			Title: "Documented channel", MembershipStatus: channelstore.StatusMember}, true, nil
	}
	return channelstore.Channel{}, false, nil
}

func (managedStore) GetByID(_ context.Context, id int64) (channelstore.Channel, bool, error) {
	record := documentedRecord()
	return record, id == record.ID, nil
}

func (managedStore) ListChannels(context.Context, channelstore.ChannelListQuery) ([]channelstore.Channel, int, error) {
	return []channelstore.Channel{documentedRecord()}, 1, nil
}

func (managedStore) ListChannelsByIDs(_ context.Context, ids []int64) ([]channelstore.Channel, error) {
	record := documentedRecord()
	for _, id := range ids {
		if id == record.ID {
			return []channelstore.Channel{record}, nil
		}
	}
	return []channelstore.Channel{}, nil
}

func (managedStore) DeleteChannelRow(context.Context, int64) error { return nil }

func (managedStore) ManagedIDs(context.Context, string) (map[int64]bool, error) {
	return map[int64]bool{1: true}, nil
}

func (managedStore) Upsert(_ context.Context, ch channelstore.Channel) (channelstore.Channel, error) {
	return ch, nil
}

// documentedServers is what GET /api/v1/telegram lists in the probe: the
// client the examples use, besides the one this deployment does not run.
func documentedServers() []gateway.ServerInfo {
	return []gateway.ServerInfo{
		{Key: "official", Type: "official", Enabled: true, Registered: true},
		{Key: "doc-client", Type: "official", Enabled: true, Registered: true},
	}
}
