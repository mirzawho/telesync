package gotdclient_test

import (
	"encoding/json"
	"testing"

	"github.com/gotd/td/tg"
)

func TestCheckUsernameUsesSelectedConnectionAndReportsConservativeStatus(t *testing.T) {
	tests := []struct {
		name  string
		chats []tg.ChatClass
		want  string
	}{
		{name: "channel is occupied", chats: []tg.ChatClass{memberChannel()}, want: "occupied"},
		{name: "empty success is non-authoritative", want: "unknown"},
		{name: "trailing underscore is accepted", chats: []tg.ChatClass{memberChannel()}, want: "occupied"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotUsername string
			backend := &discoveryBackend{resolveUsername: func(req *tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
				gotUsername = req.Username
				return tc.chats, nil
			}}
			m := managerWithPeers(t, backend)
			ctx, cancel := ctxTimeout(t)
			defer cancel()
			username := " @Example_Channel "
			if tc.name == "trailing underscore is accepted" {
				username = " @Example_ "
			}
			got, err := m.CheckUsername(ctx, username)
			if err != nil {
				t.Fatalf("CheckUsername: %v", err)
			}
			wantUsername := "example_channel"
			if tc.name == "trailing underscore is accepted" {
				wantUsername = "example_"
			}
			if got.Status != tc.want || got.Username != wantUsername {
				t.Fatalf("result = %+v, want status %q and canonical username", got, tc.want)
			}
			if gotUsername != wantUsername {
				t.Fatalf("resolve username = %q", gotUsername)
			}
			if got.Assignable != nil {
				t.Fatalf("assignable = %v, want nil", *got.Assignable)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			var wire map[string]any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			if value, exists := wire["assignable"]; !exists || value != nil {
				t.Fatalf("JSON assignable = %#v, want explicit null in %s", value, encoded)
			}
		})
	}
}

func TestCheckUsernameRejectsInvalidCanonicalNameWithoutCallingTelegram(t *testing.T) {
	called := false
	m := managerWithPeers(t, &discoveryBackend{resolveUsername: func(*tg.ContactsResolveUsernameRequest) ([]tg.ChatClass, error) {
		called = true
		return nil, nil
	}})
	ctx, cancel := ctxTimeout(t)
	defer cancel()
	got, err := m.CheckUsername(ctx, "ab")
	if err != nil {
		t.Fatalf("CheckUsername: %v", err)
	}
	if got.Status != "invalid" || called {
		t.Fatalf("result = %+v, resolve called = %v", got, called)
	}
}

func TestCheckUsernameTreatsTelegramUnoccupiedAsAdvisory(t *testing.T) {
	m := managerWithPeers(t, &discoveryBackend{})
	ctx, cancel := ctxTimeout(t)
	defer cancel()
	got, err := m.CheckUsername(ctx, "available_name")
	if err != nil {
		t.Fatalf("CheckUsername: %v", err)
	}
	if got.Status != "unclaimed" || got.Assignable != nil {
		t.Fatalf("result = %+v, want advisory unclaimed", got)
	}
}
