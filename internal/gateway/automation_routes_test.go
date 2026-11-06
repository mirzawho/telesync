package gateway_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
)

// The tests in this file prove the automation backend is genuinely reachable
// over HTTP: the real router, a real channelstore.Store and the real webhook and
// workflow services. Every one asserts the status, the envelope shape and the
// row the request left in SQLite.

const autoAPI = "/api/v1/local/automation"

// TestAutomationStatusRoute is the first route of the area.
func TestAutomationStatusRoute(t *testing.T) {
	e := newAutoEnv(t)
	rec := e.call(t, defaultCaller, http.MethodGet, autoAPI+"/status", "")
	autoOK(t, rec)

	var body autoStatusBody
	meta := autoDecode(t, rec, &body)
	if !body.Status.Enabled {
		t.Error("status.enabled = false, want true")
	}
	if body.Status.Workers != 2 {
		t.Errorf("status.workers = %d, want 2", body.Status.Workers)
	}
	if body.Status.RecentFailures == nil {
		t.Error("status.recent_failures is null, want an array")
	}
	if meta.RequestID == "" {
		t.Error("meta.request_id is empty")
	}
	// The bare collection path is the same route: a panel that drops the
	// trailing /status still lands somewhere real.
	autoOK(t, e.call(t, defaultCaller, http.MethodGet, autoAPI, ""))
}

// TestWebhookRoutesReachSQLite walks the whole webhook surface and checks the
// row behind every answer.
func TestWebhookRoutesReachSQLite(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	// GET the collection: empty, in the documented list shape.
	rec := e.call(t, defaultCaller, http.MethodGet, autoAPI+"/webhooks", "")
	autoOK(t, rec)
	var list autoWebhooksBody
	meta := autoDecode(t, rec, &list)
	if len(list.Webhooks) != 0 {
		t.Fatalf("webhooks = %d, want 0", len(list.Webhooks))
	}
	for name, want := range map[string]int{"page": 1, "per_page": 20, "total": 0, "total_pages": 0} {
		got := metaInt(t, meta, name)
		if got != want {
			t.Errorf("meta.%s = %d, want %d", name, got, want)
		}
	}
	if metaInt(t, meta, "per_page") == 0 {
		t.Error("meta.per_page is 0: total_pages would divide by it")
	}

	// POST creates.
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks",
		autoWebhookPOST("Receiver", source))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	id := created.Webhook.ID
	if id <= 0 {
		t.Fatalf("created webhook id = %d, want a positive id", id)
	}

	// The row is in SQLite with exactly what was sent.
	row := e.storeWebhook(t, id)
	if row.Name != "Receiver" {
		t.Errorf("stored name = %q, want %q", row.Name, "Receiver")
	}
	if row.URL != autoWebhookURL {
		t.Errorf("stored url = %q, want %q", row.URL, autoWebhookURL)
	}
	if !row.HasSecret {
		t.Error("stored webhook reports no secret")
	}
	if len(row.SourceChannelIDs) != 1 || row.SourceChannelIDs[0] != source {
		t.Errorf("stored source channels = %v, want [%d]", row.SourceChannelIDs, source)
	}

	// GET one.
	rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/webhooks/"+strconv.FormatInt(id, 10), "")
	autoOK(t, rec)
	var one autoWebhookBody
	autoDecode(t, rec, &one)
	if one.Webhook.ID != id || one.Webhook.Name != "Receiver" {
		t.Errorf("GET returned %+v, want id %d named Receiver", one.Webhook, id)
	}

	// PATCH persists.
	rec = e.call(t, defaultCaller, http.MethodPatch, autoAPI+"/webhooks/"+strconv.FormatInt(id, 10),
		autoJSON(t, map[string]any{"name": "Receiver (renamed)", "description": "edited", "timeout_ms": 7000}))
	autoOK(t, rec)
	var patched autoWebhookBody
	autoDecode(t, rec, &patched)
	if patched.Webhook.Name != "Receiver (renamed)" {
		t.Errorf("patched name = %q", patched.Webhook.Name)
	}
	if patched.Webhook.TimeoutMS != 7000 {
		t.Errorf("patched timeout_ms = %d, want 7000", patched.Webhook.TimeoutMS)
	}
	row = e.storeWebhook(t, id)
	if row.Name != "Receiver (renamed)" || row.Timeout.Milliseconds() != 7000 {
		t.Errorf("stored row after PATCH = %q / %dms", row.Name, row.Timeout.Milliseconds())
	}
	// A PATCH that says nothing about the secret leaves the stored one alone.
	if !row.HasSecret {
		t.Error("PATCH dropped the stored secret")
	}

	// POST /test performs a real exchange through the scripted receiver.
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks/"+strconv.FormatInt(id, 10)+"/test", "")
	autoOK(t, rec)
	var test autoDeliveryBody
	autoDecode(t, rec, &test)
	if test.Delivery.Status != "succeeded" {
		t.Errorf("test delivery status = %q, want succeeded", test.Delivery.Status)
	}
	if test.Delivery.AttemptCount != 1 {
		t.Errorf("test delivery attempts = %d, want 1", test.Delivery.AttemptCount)
	}
	if got := len(e.transport.seen()); got != 1 {
		t.Fatalf("receiver saw %d requests, want 1", got)
	}
	// A test is not a delivery: nothing is written to the history.
	rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/webhooks/"+strconv.FormatInt(id, 10)+"/deliveries", "")
	autoOK(t, rec)
	var deliveries autoDeliveriesBody
	autoDecode(t, rec, &deliveries)
	if len(deliveries.Deliveries) != 0 {
		t.Fatalf("deliveries after a test = %d, want 0", len(deliveries.Deliveries))
	}

	// DELETE.
	rec = e.call(t, defaultCaller, http.MethodDelete, autoAPI+"/webhooks/"+strconv.FormatInt(id, 10), "")
	autoOK(t, rec)
	var gone autoDeletedBody
	autoDecode(t, rec, &gone)
	if !gone.Deleted {
		t.Error("DELETE did not report deleted")
	}
	if _, found, err := e.store.GetWebhook(t.Context(), id); err != nil || found {
		t.Errorf("webhook %d still in the store (found=%v err=%v)", id, found, err)
	}
	// A second DELETE is a 404, not a 500.
	rec = e.call(t, defaultCaller, http.MethodDelete, autoAPI+"/webhooks/"+strconv.FormatInt(id, 10), "")
	autoFail(t, rec, http.StatusNotFound, gateway.CodeNotFound)
}

// TestWebhookHistoryRoutesReachSQLite drives one real delivery through the real
// webhook engine, then reads it back over HTTP.
func TestWebhookHistoryRoutesReachSQLite(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks",
		autoWebhookPOST("History", source))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	id := created.Webhook.ID

	event := e.seedEvent(t, source, 1001, 42)
	// The receiver refuses the post, so the delivery ends failed and can be
	// retried by hand — which is the state the retry route is for.
	e.transport.answer(http.StatusBadRequest, `{"error":"nope"}`)
	stored := e.deliver(t, id, event)
	if stored.ID <= 0 {
		t.Fatalf("delivery id = %d, want a positive id", stored.ID)
	}
	if stored.Status != "failed" {
		t.Fatalf("delivery status = %q, want failed", stored.Status)
	}

	// GET the collection.
	rec = e.call(t, defaultCaller, http.MethodGet,
		autoAPI+"/webhooks/"+strconv.FormatInt(id, 10)+"/deliveries", "")
	autoOK(t, rec)
	var list autoDeliveriesBody
	meta := autoDecode(t, rec, &list)
	if len(list.Deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(list.Deliveries))
	}
	if list.Deliveries[0].ID != stored.ID {
		t.Errorf("listed delivery id = %d, want %d", list.Deliveries[0].ID, stored.ID)
	}
	if metaInt(t, meta, "total") != 1 {
		t.Errorf("meta.total = %d, want 1", metaInt(t, meta, "total"))
	}

	// GET one, with the request that was transmitted and the attempt behind it.
	rec = e.call(t, defaultCaller, http.MethodGet,
		autoAPI+"/webhooks/"+strconv.FormatInt(id, 10)+"/deliveries/"+strconv.FormatInt(stored.ID, 10), "")
	autoOK(t, rec)
	var one autoDeliveryBody
	autoDecode(t, rec, &one)
	if one.Delivery.ID != stored.ID {
		t.Errorf("delivery id = %d, want %d", one.Delivery.ID, stored.ID)
	}
	if one.Delivery.Request == nil {
		t.Fatal("delivery carries no request record")
	}
	if one.Delivery.Request.Method != http.MethodPost {
		t.Errorf("request method = %q, want POST", one.Delivery.Request.Method)
	}
	if len(one.Delivery.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(one.Delivery.Attempts))
	}
	if one.Delivery.Attempts[0].ResponseStatus != http.StatusBadRequest {
		t.Errorf("attempt response status = %d, want 400", one.Delivery.Attempts[0].ResponseStatus)
	}
	if one.Delivery.Attempts[0].ErrorCode == "" {
		t.Error("the attempt records no error code for a refused delivery")
	}

	// POST /retry reschedules the same row; it never creates a second one.
	rec = e.call(t, defaultCaller, http.MethodPost,
		autoAPI+"/webhooks/"+strconv.FormatInt(id, 10)+"/deliveries/"+strconv.FormatInt(stored.ID, 10)+"/retry", "")
	autoOK(t, rec)
	var retried autoDeliveryBody
	autoDecode(t, rec, &retried)
	if retried.Delivery.ID != stored.ID {
		t.Errorf("retry returned delivery %d, want the same row %d", retried.Delivery.ID, stored.ID)
	}
	if retried.Delivery.Status != "pending" {
		t.Errorf("delivery status after retry = %q, want pending", retried.Delivery.Status)
	}
	page, err := e.store.ListWebhookDeliveries(t.Context(), id, automation.DeliveryListOptions{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Deliveries) != 1 {
		t.Fatalf("deliveries after a retry = %d, want 1: a retry extends the row", len(page.Deliveries))
	}

	// A delivery under the wrong webhook is not readable.
	other := e.channelWithID(t, 2002)
	e.grantChannelView(t, e.defaults, source, other)
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks",
		autoWebhookPOST("Other", other))
	autoCreated(t, rec)
	var second autoWebhookBody
	autoDecode(t, rec, &second)
	rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/webhooks/"+
		strconv.FormatInt(second.Webhook.ID, 10)+"/deliveries/"+strconv.FormatInt(stored.ID, 10), "")
	autoFail(t, rec, http.StatusNotFound, gateway.CodeNotFound)
}

// TestRetryRefusesADeliveryThatCannotBeRetried is the other half of the retry
// contract: a delivery that already succeeded is not rescheduled, and the
// refusal is the narrow conflict code.
func TestRetryRefusesADeliveryThatCannotBeRetried(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoWebhookPOST("Done", source))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	id := created.Webhook.ID

	stored := e.deliver(t, id, e.seedEvent(t, source, 1001, 7))
	if stored.Status != "succeeded" {
		t.Fatalf("delivery status = %q, want succeeded", stored.Status)
	}
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks/"+
		strconv.FormatInt(id, 10)+"/deliveries/"+strconv.FormatInt(stored.ID, 10)+"/retry", "")
	autoFail(t, rec, http.StatusConflict, gateway.CodeAutomationConflict)
}

// TestWorkflowRoutesReachSQLite walks the whole workflow surface.
func TestWorkflowRoutesReachSQLite(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	rec := e.call(t, defaultCaller, http.MethodGet, autoAPI+"/workflows", "")
	autoOK(t, rec)
	var empty autoWorkflowsBody
	autoDecode(t, rec, &empty)
	if len(empty.Workflows) != 0 {
		t.Fatalf("workflows = %d, want 0", len(empty.Workflows))
	}

	// POST creates.
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": source,
		"enabled":            false,
		"destinations":       []any{},
	}))
	autoCreated(t, rec)
	var created autoWorkflowBody
	autoDecode(t, rec, &created)
	id := created.Workflow.ID
	if id <= 0 {
		t.Fatalf("workflow id = %d, want a positive id", id)
	}
	if created.Workflow.ManagedChannelID != source {
		t.Errorf("managed_channel_id = %d, want %d", created.Workflow.ManagedChannelID, source)
	}
	if created.Workflow.ConfigRevision != 1 {
		t.Errorf("config_revision = %d, want 1", created.Workflow.ConfigRevision)
	}
	// The source channel is resolved onto the answer so a screen can name it.
	if created.Workflow.Channel == nil || created.Workflow.Channel.TelegramChannelID != 1001 {
		t.Errorf("resolved source channel = %+v, want telegram id 1001", created.Workflow.Channel)
	}

	row := e.storeWorkflow(t, id)
	if row.ManagedChannelID != source || row.Enabled {
		t.Errorf("stored workflow = channel %d enabled=%v", row.ManagedChannelID, row.Enabled)
	}

	// GET one.
	rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/workflows/"+strconv.FormatInt(id, 10), "")
	autoOK(t, rec)
	var one autoWorkflowBody
	autoDecode(t, rec, &one)
	if one.Workflow.ID != id {
		t.Errorf("workflow id = %d, want %d", one.Workflow.ID, id)
	}

	// PATCH with a destination, then enable.
	dest := e.channelWithID(t, 1002)
	e.grantChannelView(t, e.defaults, source, dest)
	rec = e.call(t, defaultCaller, http.MethodPatch, autoAPI+"/workflows/"+strconv.FormatInt(id, 10),
		autoJSON(t, map[string]any{
			"destinations": []any{map[string]any{
				"destination_type": "telegram",
				"client_key":       "official",
				"channel_id":       1002,
				"position":         0,
			}},
		}))
	autoOK(t, rec)
	var withDest autoWorkflowBody
	autoDecode(t, rec, &withDest)
	if len(withDest.Workflow.Destinations) != 1 {
		t.Fatalf("destinations = %d, want 1", len(withDest.Workflow.Destinations))
	}
	if withDest.Workflow.Destinations[0].DestinationType != "telegram" ||
		withDest.Workflow.Destinations[0].ChannelID != 1002 {
		t.Errorf("destination = %+v", withDest.Workflow.Destinations[0])
	}

	rec = e.call(t, defaultCaller, http.MethodPatch, autoAPI+"/workflows/"+strconv.FormatInt(id, 10),
		autoJSON(t, map[string]any{"enabled": true}))
	autoOK(t, rec)
	var enabled autoWorkflowBody
	autoDecode(t, rec, &enabled)
	if !enabled.Workflow.Enabled {
		t.Error("workflow is not enabled after PATCH")
	}
	if !e.storeWorkflow(t, id).Enabled {
		t.Error("the stored row is not enabled")
	}

	// GET runs: empty, but in the documented list shape.
	rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/workflows/"+strconv.FormatInt(id, 10)+"/runs", "")
	autoOK(t, rec)
	var runs autoRunsBody
	autoDecode(t, rec, &runs)
	if len(runs.Runs) != 0 {
		t.Fatalf("runs = %d, want 0", len(runs.Runs))
	}

	// DELETE.
	rec = e.call(t, defaultCaller, http.MethodDelete, autoAPI+"/workflows/"+strconv.FormatInt(id, 10), "")
	autoOK(t, rec)
	var gone autoDeletedBody
	autoDecode(t, rec, &gone)
	if !gone.Deleted {
		t.Error("DELETE did not report deleted")
	}
	if _, found, err := e.store.GetWorkflow(t.Context(), id); err != nil || found {
		t.Errorf("workflow %d still in the store (found=%v err=%v)", id, found, err)
	}
}

// TestWorkflowRunRouteReadsTheStoredRun covers the run detail route, which a
// probe cannot reach without a real execution behind it.
func TestWorkflowRunRouteReadsTheStoredRun(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": source,
		"enabled":            false,
		"destinations":       []any{},
	}))
	autoCreated(t, rec)
	var created autoWorkflowBody
	autoDecode(t, rec, &created)
	id := created.Workflow.ID

	run, isNew, err := e.store.BeginWorkflowRun(t.Context(), channelstore.BeginWorkflowRunRequest{
		WorkflowID:      id,
		EventID:         e.seedEvent(t, source, 1001, 42).ID,
		SourceMessageID: 42,
		OriginalContent: "Example message",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Fatal("the run was not created")
	}

	rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/workflows/"+strconv.FormatInt(id, 10)+"/runs", "")
	autoOK(t, rec)
	var list autoRunsBody
	autoDecode(t, rec, &list)
	if len(list.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(list.Runs))
	}

	rec = e.call(t, defaultCaller, http.MethodGet,
		autoAPI+"/workflows/"+strconv.FormatInt(id, 10)+"/runs/"+strconv.FormatInt(run.ID, 10), "")
	autoOK(t, rec)
	var one autoRunBody
	autoDecode(t, rec, &one)
	if one.Run.ID != run.ID {
		t.Errorf("run id = %d, want %d", one.Run.ID, run.ID)
	}
	if one.Run.WorkflowID != id {
		t.Errorf("run workflow_id = %d, want %d", one.Run.WorkflowID, id)
	}
	if one.Run.OriginalContent != "Example message" {
		t.Errorf("original_content = %q", one.Run.OriginalContent)
	}
	if one.Run.Publications == nil {
		t.Error("run.publications is null, want an array")
	}

	// A run of another workflow is not readable through this one.
	other := e.channelWithID(t, 1002)
	e.grantChannelView(t, e.defaults, source, other)
	rec = e.call(t, defaultCaller, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": other, "enabled": false, "destinations": []any{},
	}))
	autoCreated(t, rec)
	var second autoWorkflowBody
	autoDecode(t, rec, &second)
	rec = e.call(t, defaultCaller, http.MethodGet,
		autoAPI+"/workflows/"+strconv.FormatInt(second.Workflow.ID, 10)+
			"/runs/"+strconv.FormatInt(run.ID, 10), "")
	autoFail(t, rec, http.StatusNotFound, gateway.CodeNotFound)
}

// TestWebhookConfigurationSurvivesANewRequest is the persistence round trip the
// mission names: create, read, edit, read, then a request through a handler that
// shares no memory with the one that wrote it.
func TestWebhookConfigurationSurvivesANewRequest(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)
	path := autoAPI + "/webhooks"

	// Create → Save.
	rec := e.call(t, defaultCaller, http.MethodPost, path, autoJSON(t, map[string]any{
		"name":               "Round trip",
		"description":        "first",
		"enabled":            true,
		"url":                autoWebhookURL,
		"method":             "PATCH",
		"auth_mode":          "bearer",
		"secret":             autoSecret,
		"timeout_ms":         12345,
		"max_attempts":       9,
		"source_channel_ids": []int64{source},
	}))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	id := created.Webhook.ID
	at := path + "/" + strconv.FormatInt(id, 10)

	// GET.
	rec = e.call(t, defaultCaller, http.MethodGet, at, "")
	autoOK(t, rec)
	var first autoWebhookBody
	autoDecode(t, rec, &first)
	if first.Webhook.Name != "Round trip" || first.Webhook.Method != "PATCH" ||
		first.Webhook.AuthMode != "bearer" || first.Webhook.TimeoutMS != 12345 ||
		first.Webhook.MaxAttempts != 9 || !first.Webhook.Enabled {
		t.Fatalf("first read = %+v", first.Webhook)
	}

	// Edit → Save.
	rec = e.call(t, defaultCaller, http.MethodPatch, at, autoJSON(t, map[string]any{
		"name": "Round trip (edited)", "description": "second", "max_attempts": 3,
	}))
	autoOK(t, rec)
	var edited autoWebhookBody
	autoDecode(t, rec, &edited)
	if edited.Webhook.Name != "Round trip (edited)" {
		t.Fatalf("edited name = %q", edited.Webhook.Name)
	}

	// GET again.
	rec = e.call(t, defaultCaller, http.MethodGet, at, "")
	autoOK(t, rec)
	var second autoWebhookBody
	autoDecode(t, rec, &second)
	if second.Webhook.Description != "second" || second.Webhook.MaxAttempts != 3 {
		t.Fatalf("second read = %+v", second.Webhook)
	}

	// A new request: a fresh handler, fresh service instances, same database.
	rec = e.restart(t, http.MethodGet, defaultCaller, at, "")
	autoOK(t, rec)
	var fresh autoWebhookBody
	autoDecode(t, rec, &fresh)
	want := second.Webhook
	if fresh.Webhook.Name != want.Name || fresh.Webhook.Description != want.Description ||
		fresh.Webhook.URL != want.URL || fresh.Webhook.Method != want.Method ||
		fresh.Webhook.AuthMode != want.AuthMode || fresh.Webhook.AuthHeader != want.AuthHeader ||
		fresh.Webhook.TimeoutMS != want.TimeoutMS || fresh.Webhook.MaxAttempts != want.MaxAttempts ||
		fresh.Webhook.Enabled != want.Enabled || !fresh.Webhook.HasSecret ||
		len(fresh.Webhook.SourceChannels) != len(want.SourceChannels) {
		t.Fatalf("after a restart the webhook reads %+v, want %+v", fresh.Webhook, want)
	}
	for i := range want.SourceChannels {
		if fresh.Webhook.SourceChannels[i] != want.SourceChannels[i] {
			t.Fatalf("source channels after a restart = %v, want %v",
				fresh.Webhook.SourceChannels, want.SourceChannels)
		}
	}
	// And the row itself is still there.
	row := e.storeWebhook(t, id)
	if row.Name != "Round trip (edited)" || row.Description != "second" || row.MaxAttempts != 3 {
		t.Fatalf("stored row = %+v", row)
	}
}

// TestWorkflowEnablementSurvivesANewRequest is the second persistence round
// trip the mission names.
func TestWorkflowEnablementSurvivesANewRequest(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	dest := e.channelWithID(t, 1002)
	e.grantChannelView(t, e.defaults, source, dest)
	path := autoAPI + "/workflows"

	// Create → Save.
	rec := e.call(t, defaultCaller, http.MethodPost, path, autoJSON(t, map[string]any{
		"managed_channel_id": source,
		"enabled":            false,
		"ai_enabled":         false,
		"destinations": []any{map[string]any{
			"destination_type": "telegram", "client_key": "official", "channel_id": 1002,
		}},
	}))
	autoCreated(t, rec)
	var created autoWorkflowBody
	autoDecode(t, rec, &created)
	id := created.Workflow.ID
	at := path + "/" + strconv.FormatInt(id, 10)

	// GET.
	rec = e.call(t, defaultCaller, http.MethodGet, at, "")
	autoOK(t, rec)
	var before autoWorkflowBody
	autoDecode(t, rec, &before)
	if before.Workflow.Enabled {
		t.Fatal("the workflow was created enabled")
	}

	// Enable → Save.
	rec = e.call(t, defaultCaller, http.MethodPatch, at, autoJSON(t, map[string]any{"enabled": true}))
	autoOK(t, rec)
	var enabled autoWorkflowBody
	autoDecode(t, rec, &enabled)
	if !enabled.Workflow.Enabled {
		t.Fatal("PATCH did not enable the workflow")
	}

	// GET.
	rec = e.call(t, defaultCaller, http.MethodGet, at, "")
	autoOK(t, rec)
	var after autoWorkflowBody
	autoDecode(t, rec, &after)
	if !after.Workflow.Enabled {
		t.Fatal("the workflow is no longer enabled")
	}

	// A new request.
	rec = e.restart(t, http.MethodGet, defaultCaller, at, "")
	autoOK(t, rec)
	var fresh autoWorkflowBody
	autoDecode(t, rec, &fresh)
	if !fresh.Workflow.Enabled {
		t.Error("the workflow is not enabled after a restart")
	}
	if fresh.Workflow.ManagedChannelID != source {
		t.Errorf("managed_channel_id = %d, want %d", fresh.Workflow.ManagedChannelID, source)
	}
	if len(fresh.Workflow.Destinations) != 1 || fresh.Workflow.Destinations[0].ChannelID != 1002 {
		t.Errorf("destinations after a restart = %+v", fresh.Workflow.Destinations)
	}
	row := e.storeWorkflow(t, id)
	if !row.Enabled {
		t.Error("the stored row is not enabled")
	}
}

// TestAutomationRefusesAnUnsupportedMethod checks the method contract: a route
// answers 405 with its Allow header rather than doing something else.
func TestAutomationRefusesAnUnsupportedMethod(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)

	for _, tc := range []struct {
		method, path, allow string
	}{
		{http.MethodPost, autoAPI + "/status", http.MethodGet},
		{http.MethodPut, autoAPI + "/webhooks", "GET, POST"},
		{http.MethodPut, autoAPI + "/webhooks/1", "GET, PATCH, DELETE"},
		{http.MethodDelete, autoAPI + "/webhooks/1/test", http.MethodPost},
		{http.MethodPost, autoAPI + "/webhooks/1/deliveries", http.MethodGet},
		{http.MethodPut, autoAPI + "/workflows", "GET, POST"},
		{http.MethodPut, autoAPI + "/workflows/1", "GET, PATCH, DELETE"},
		{http.MethodPost, autoAPI + "/workflows/1/runs", http.MethodGet},
	} {
		rec := e.call(t, defaultCaller, tc.method, tc.path, "")
		autoFail(t, rec, http.StatusMethodNotAllowed, gateway.CodeMethodNotAllowed)
		if got := rec.Header().Get("Allow"); got != tc.allow {
			t.Errorf("%s %s: Allow = %q, want %q", tc.method, tc.path, got, tc.allow)
		}
	}

	// A path under the namespace that names no resource is a 404, and so is a
	// mistyped sub-resource: a typo must not read as a malformed id.
	for _, path := range []string{
		autoAPI + "/nope",
		autoAPI + "/webhooks/1/nope",
		autoAPI + "/webhooks/1/deliveries/2/nope",
		autoAPI + "/workflows/1/nope",
		autoAPI + "/workflows/1/runs/2/nope",
	} {
		rec := e.call(t, defaultCaller, http.MethodGet, path, "")
		autoFail(t, rec, http.StatusNotFound, gateway.CodeNotFound)
	}

	// A malformed id is refused as input, and before it is read as a resource.
	for _, path := range []string{
		autoAPI + "/webhooks/not-an-id",
		autoAPI + "/webhooks/1/deliveries/not-an-id",
		autoAPI + "/workflows/not-an-id",
		autoAPI + "/workflows/1/runs/not-an-id",
	} {
		rec := e.call(t, defaultCaller, http.MethodGet, path, "")
		autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
	}
}

// TestAutomationListValidation checks the one query contract the lists share.
func TestAutomationListValidation(t *testing.T) {
	e := newAutoEnv(t)
	for _, path := range []string{autoAPI + "/webhooks", autoAPI + "/workflows"} {
		for _, q := range []string{"?page=0", "?per_page=0", "?per_page=1000", "?per_page=x", "?enabled=maybe", "?nope=1"} {
			rec := e.call(t, defaultCaller, http.MethodGet, path+q, "")
			autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
		}
	}
	// A history list is only reached under a webhook that exists, and then it
	// takes status and nothing else.
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, e.defaults, source)
	rec := e.call(t, defaultCaller, http.MethodPost, autoAPI+"/webhooks", autoWebhookPOST("Query", source))
	autoCreated(t, rec)
	var created autoWebhookBody
	autoDecode(t, rec, &created)
	at := autoAPI + "/webhooks/" + strconv.FormatInt(created.Webhook.ID, 10)

	for _, q := range []string{"?status=", "?other=1", "?page=-1", "?per_page=99999"} {
		rec := e.call(t, defaultCaller, http.MethodGet, at+"/deliveries"+q, "")
		autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
	}
	rec = e.call(t, defaultCaller, http.MethodGet, at+"/deliveries?status=failed", "")
	autoOK(t, rec)
	// A large but legal page size is accepted; the bound is the RPC one.
	rec = e.call(t, defaultCaller, http.MethodGet, autoAPI+"/webhooks?per_page=100", "")
	autoOK(t, rec)
}

// TestAutomationIs503WhenNotWired is what a deployment without the subsystem
// sees: an honest 503 naming the code, not a panic and not an empty list.
func TestAutomationIs503WhenNotWired(t *testing.T) {
	e := newAutoEnv(t, func(c *gateway.Config) {
		c.AutomationWebhooks = nil
		c.AutomationWorkflows = nil
	})
	for _, path := range []string{
		autoAPI + "/webhooks", autoAPI + "/webhooks/1", autoAPI + "/workflows", autoAPI + "/workflows/1",
	} {
		rec := e.call(t, defaultCaller, http.MethodGet, path, "")
		autoFail(t, rec, http.StatusServiceUnavailable, gateway.CodeAutomationUnavailable)
	}
	// The status route reports the subsystem's own state and needs no service.
	autoOK(t, e.call(t, defaultCaller, http.MethodGet, autoAPI+"/status", ""))
}

// metaInt reads one integer out of the page metadata.
func metaInt(t *testing.T, meta autoMeta, name string) int {
	t.Helper()
	var v *int
	switch name {
	case "page":
		v = meta.Page
	case "per_page":
		v = meta.PerPage
	case "total":
		v = meta.Total
	case "total_pages":
		v = meta.TotalPages
	}
	if v == nil {
		t.Fatalf("meta.%s is absent", name)
	}
	return *v
}

// autoNoSecret reports whether a serialized answer mentions the webhook secret.
func autoNoSecret(t *testing.T, raw, label string) {
	t.Helper()
	if strings.Contains(raw, autoSecret) {
		t.Fatalf("%s carries the webhook secret: %s", label, raw)
	}
}
