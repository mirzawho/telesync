package workflows

import (
	"testing"

	"github.com/mirza/telesync/internal/automation"
)

func TestAlbumMembersShareWorkflowPublicationIdentity(t *testing.T) {
	groupID := int64(9001)
	first := automation.MessageEvent{
		NetworkKey: "custom@telegram.example:443", TelegramChannelID: -10055,
		MessageID: 41, GroupedID: &groupID,
	}
	second := first
	second.MessageID = 42
	publication := automation.WorkflowPublication{
		DestinationType: "telegram", ClientKey: "destination", ChannelID: -10077,
	}

	key := idempotencyKey(first, 17, publication)
	if got := idempotencyKey(second, 17, publication); got != key {
		t.Fatalf("album member key = %q, want shared key %q", got, key)
	}

	// The identity remains isolated by source network/channel, workflow, and
	// destination; a shared grouped_id alone is never globally meaningful.
	cases := []struct {
		name  string
		event automation.MessageEvent
		wfID  int64
		pub   automation.WorkflowPublication
	}{
		{"network", func() automation.MessageEvent { e := first; e.NetworkKey = "official"; return e }(), 17, publication},
		{"channel", func() automation.MessageEvent { e := first; e.TelegramChannelID++; return e }(), 17, publication},
		{"workflow", first, 18, publication},
		{"destination", first, 17, automation.WorkflowPublication{DestinationType: "telegram", ClientKey: "destination", ChannelID: -10078}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := idempotencyKey(tc.event, tc.wfID, tc.pub); got == key {
				t.Fatalf("identity collision: %q", got)
			}
		})
	}
}

func TestStandaloneWorkflowPublicationIdentityUsesMessageID(t *testing.T) {
	event := automation.MessageEvent{NetworkKey: "official", TelegramChannelID: 55, MessageID: 41}
	publication := automation.WorkflowPublication{DestinationType: "wordpress", SiteID: 7}
	first := idempotencyKey(event, 17, publication)
	event.MessageID++
	if got := idempotencyKey(event, 17, publication); got == first {
		t.Fatal("different standalone messages shared a workflow publication key")
	}
}
