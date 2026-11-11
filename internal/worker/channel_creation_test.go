package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/worker"
)

type creationTelegram struct {
	*fakeTelegram
	checkResult   gotdclient.UsernameCheck
	checkErr      error
	created       tgapi.ChannelInfo
	createErr     error
	usernameErr   error
	avatarErr     error
	createCalls   int
	usernameCalls int
	avatarCalls   int
}

func (f *creationTelegram) CheckUsername(context.Context, string) (gotdclient.UsernameCheck, error) {
	return f.checkResult, f.checkErr
}
func (f *creationTelegram) CreateBroadcastChannel(context.Context, string, string) (tgapi.ChannelInfo, error) {
	f.createCalls++
	return f.created, f.createErr
}
func (f *creationTelegram) SetBroadcastChannelUsername(context.Context, int64, string) error {
	f.usernameCalls++
	return f.usernameErr
}
func (f *creationTelegram) SetBroadcastChannelAvatar(context.Context, int64, string, string, []byte) (int64, error) {
	f.avatarCalls++
	return 0, f.avatarErr
}

func TestCreateChannelReturnsRemoteIdentityWhenPublicSetupFails(t *testing.T) {
	tg := &creationTelegram{fakeTelegram: &fakeTelegram{status: tgapi.Status{State: tgapi.StateReady}},
		created:     tgapi.ChannelInfo{ID: 99112233, Kind: "channel", Title: "News", Membership: tgapi.MembershipMember},
		usernameErr: errors.New("username rejected")}
	s := worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official}, tg, worker.Options{})
	result, err := s.CreateChannel(context.Background(), rpc.CreateChannelRequest{Title: "News", Visibility: "public", Username: "news_room"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ChannelCreated || result.UsernameAssigned || result.Channel == nil || result.Channel.Channel.ID != "99112233" || result.ErrorCode != "username_assignment_failed" {
		t.Fatalf("partial create result = %+v", result)
	}
	if tg.createCalls != 1 || tg.usernameCalls != 1 || tg.avatarCalls != 0 {
		t.Fatalf("stages create=%d username=%d avatar=%d", tg.createCalls, tg.usernameCalls, tg.avatarCalls)
	}
}

func TestCreateChannelAttemptsAvatarAfterUsernameAssignmentFailure(t *testing.T) {
	tg := &creationTelegram{fakeTelegram: &fakeTelegram{status: tgapi.Status{State: tgapi.StateReady}},
		created:     tgapi.ChannelInfo{ID: 778899, Kind: "channel", Title: "News", Membership: tgapi.MembershipMember},
		usernameErr: errors.New("username rejected"), avatarErr: errors.New("photo rejected")}
	s := worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official}, tg, worker.Options{})
	_, err := s.CreateChannel(context.Background(), rpc.CreateChannelRequest{Title: "News", Visibility: "public", Username: "news_room",
		Avatar: &rpc.SendMedia{Name: "logo.png", MimeType: "image/png", Data: []byte{137, 80, 78, 71, 13, 10, 26, 10}}})
	if err != nil {
		t.Fatal(err)
	}
	if tg.usernameCalls != 1 || tg.avatarCalls != 1 {
		t.Fatalf("setup calls username=%d avatar=%d, want both independent stages attempted", tg.usernameCalls, tg.avatarCalls)
	}
}

func TestCreateChannelValidatesBeforeRemoteCreate(t *testing.T) {
	tg := &creationTelegram{fakeTelegram: &fakeTelegram{status: tgapi.Status{State: tgapi.StateReady}}}
	s := worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official}, tg, worker.Options{})
	_, err := s.CreateChannel(context.Background(), rpc.CreateChannelRequest{Title: " ", Visibility: "private"})
	var wire *rpc.Error
	if !errors.As(err, &wire) || wire.Code != rpc.CodeInvalidInput {
		t.Fatalf("invalid title error=%v, want invalid_input", err)
	}
	if tg.createCalls != 0 {
		t.Fatalf("remote create calls=%d, want none", tg.createCalls)
	}
}
