package gotdclient_test

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/mirza/telesync/internal/tgapi"
)

func TestStatusRecordsTheLastSuccessfulProbe(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	st := m.Status(context.Background())
	if st.LastSuccess.IsZero() {
		t.Fatal("a ready client must report when its last successful probe happened")
	}
	if age := time.Since(st.LastSuccess); age > time.Minute {
		t.Fatalf("last success is %s old", age)
	}
}

func TestUnauthorizedClientHasNoLastSuccess(t *testing.T) {
	l, key := startPanelServer(t, nil, &panelServer{})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateUnauthorized)

	st := m.Status(context.Background())
	if !st.LastSuccess.IsZero() {
		t.Fatalf("an unauthorized client reported a successful probe at %s", st.LastSuccess)
	}
}
