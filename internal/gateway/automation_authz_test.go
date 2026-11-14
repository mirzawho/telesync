package gateway_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
)

// The permission model of the automation area:
//
//	automation.view   admits the area and every read
//	webhooks.manage   writes to webhooks, sends tests, retries deliveries
//	workflows.manage  writes to workflows
//
// Beyond the global right a row is only readable when the caller can see its
// source channel, and a write may name only channels and sites the caller can
// already see. A resource the caller cannot see answers 404, never 403, so the
// API cannot be used to probe for another account's configuration.

// autoAccounts builds one account per permission shape the matrix needs.
type autoAccounts struct {
	admin    identity // every global right
	viewer   identity // automation.view only
	outsider identity // nothing at all
	manager  identity // the three automation rights, scoped to one channel
}

func (e *autoEnv) accounts(t *testing.T) autoAccounts {
	t.Helper()
	return autoAccounts{
		admin:    e.account(t, "auto-admin", channelstore.GlobalPermissions()...),
		viewer:   e.account(t, "auto-viewer", channelstore.PermissionAutomationView),
		outsider: e.account(t, "auto-outsider"),
		manager: e.account(t, "auto-manager",
			channelstore.PermissionAutomationView,
			channelstore.PermissionWebhooksManage,
			channelstore.PermissionWorkflowsManage,
		),
	}
}

// autoEveryRoute is one request to every automation route, with a resource id
// that does not exist: what matters is the refusal in front of it.
type autoProbe struct {
	method, path string
}

func autoEveryRoute(id string) []autoProbe {
	base := autoAPI
	return []autoProbe{
		{http.MethodGet, base + "/status"},
		{http.MethodGet, base + "/webhooks"},
		{http.MethodPost, base + "/webhooks"},
		{http.MethodGet, base + "/webhooks/" + id},
		{http.MethodPatch, base + "/webhooks/" + id},
		{http.MethodDelete, base + "/webhooks/" + id},
		{http.MethodPost, base + "/webhooks/" + id + "/test"},
		{http.MethodGet, base + "/webhooks/" + id + "/deliveries"},
		{http.MethodGet, base + "/webhooks/" + id + "/deliveries/" + id},
		{http.MethodPost, base + "/webhooks/" + id + "/deliveries/" + id + "/retry"},
		{http.MethodGet, base + "/workflows"},
		{http.MethodPost, base + "/workflows"},
		{http.MethodGet, base + "/workflows/" + id},
		{http.MethodPatch, base + "/workflows/" + id},
		{http.MethodDelete, base + "/workflows/" + id},
		{http.MethodGet, base + "/workflows/" + id + "/runs"},
		{http.MethodGet, base + "/workflows/" + id + "/runs/" + id},
	}
}

// TestAutomationAreaIsClosedWithoutAutomationView is the outermost gate: with no
// automation.view nothing under the namespace is reachable, and the answer is
// the one 403 rather than a 404 that would say whether the row exists.
func TestAutomationAreaIsClosedWithoutAutomationView(t *testing.T) {
	e := newAutoEnv(t)
	a := e.accounts(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, a.outsider, source)

	for _, who := range []identity{a.outsider} {
		for _, p := range autoEveryRoute("1") {
			rec := e.call(t, who.token, p.method, p.path, "")
			autoFail(t, rec, http.StatusForbidden, gateway.CodeForbidden)
		}
	}
	// An anonymous caller is refused as unauthorized before anything else.
	for _, p := range autoEveryRoute("1") {
		rec := e.call(t, "", p.method, p.path, "")
		autoFail(t, rec, http.StatusUnauthorized, gateway.CodeUnauthorized)
	}
}

// TestAutomationViewReadsButDoesNotWrite is the middle case: automation.view is
// enough for every read and for nothing else.
func TestAutomationViewReadsButDoesNotWrite(t *testing.T) {
	e := newAutoEnv(t)
	a := e.accounts(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, a.admin, source)
	e.grantChannelView(t, a.viewer, source)

	// The administrator sets up a readable webhook and workflow.
	rec := e.call(t, a.admin.token, http.MethodPost, autoAPI+"/webhooks", autoWebhookPOST("Read only", source))
	autoCreated(t, rec)
	var hook autoWebhookBody
	autoDecode(t, rec, &hook)
	rec = e.call(t, a.admin.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": source, "enabled": false, "destinations": []any{},
	}))
	autoCreated(t, rec)
	var flow autoWorkflowBody
	autoDecode(t, rec, &flow)

	hookAt := autoAPI + "/webhooks/" + strconv.FormatInt(hook.Webhook.ID, 10)
	flowAt := autoAPI + "/workflows/" + strconv.FormatInt(flow.Workflow.ID, 10)

	// Reads.
	for _, path := range []string{
		autoAPI + "/status", autoAPI + "/webhooks", hookAt,
		hookAt + "/deliveries", autoAPI + "/workflows", flowAt, flowAt + "/runs",
	} {
		rec := e.call(t, a.viewer.token, http.MethodGet, path, "")
		autoOK(t, rec)
	}

	// Writes, each refused with the one 403.
	for _, p := range []autoProbe{
		{http.MethodPost, autoAPI + "/webhooks"},
		{http.MethodPatch, hookAt},
		{http.MethodDelete, hookAt},
		{http.MethodPost, hookAt + "/test"},
		{http.MethodPost, autoAPI + "/workflows"},
		{http.MethodPatch, flowAt},
		{http.MethodDelete, flowAt},
	} {
		body := ""
		if p.method == http.MethodPost || p.method == http.MethodPatch {
			body = "{}"
		}
		rec := e.call(t, a.viewer.token, p.method, p.path, body)
		autoFail(t, rec, http.StatusForbidden, gateway.CodeForbidden)
	}

	// A retry is a write to the delivery history and needs webhooks.manage.
	event := e.seedEvent(t, source, 1001, 42)
	stored := e.deliver(t, hook.Webhook.ID, event)
	rec = e.call(t, a.viewer.token, http.MethodPost,
		hookAt+"/deliveries/"+strconv.FormatInt(stored.ID, 10)+"/retry", "")
	autoFail(t, rec, http.StatusForbidden, gateway.CodeForbidden)

	// The refused writes changed nothing.
	if e.storeWebhook(t, hook.Webhook.ID).Name != "Read only" {
		t.Error("a refused PATCH changed the stored webhook")
	}
	if e.storeWorkflow(t, flow.Workflow.ID).Enabled {
		t.Error("a refused PATCH enabled the stored workflow")
	}
}

// TestAutomationResourceTheCallerCannotSeeIs404 is the probe-resistance rule: a
// caller who holds the managing right but cannot see the channel behind a row
// is told the row does not exist.
func TestAutomationResourceTheCallerCannotSeeIs404(t *testing.T) {
	e := newAutoEnv(t)
	a := e.accounts(t)
	mine := e.channelWithID(t, 1001)
	theirs := e.channelWithID(t, 1002)
	e.grantChannelView(t, a.admin, mine, theirs)
	// The manager sees only its own channel.
	e.grantChannelView(t, a.manager, mine)

	// An administrator makes a webhook and a workflow on the other account's
	// channel.
	rec := e.call(t, a.admin.token, http.MethodPost, autoAPI+"/webhooks", autoWebhookPOST("Theirs", theirs))
	autoCreated(t, rec)
	var hook autoWebhookBody
	autoDecode(t, rec, &hook)
	rec = e.call(t, a.admin.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": theirs, "enabled": false, "destinations": []any{},
	}))
	autoCreated(t, rec)
	var flow autoWorkflowBody
	autoDecode(t, rec, &flow)

	event := e.seedEvent(t, theirs, 1002, 42)
	stored := e.deliver(t, hook.Webhook.ID, event)

	hookAt := autoAPI + "/webhooks/" + strconv.FormatInt(hook.Webhook.ID, 10)
	flowAt := autoAPI + "/workflows/" + strconv.FormatInt(flow.Workflow.ID, 10)
	deliveryAt := hookAt + "/deliveries/" + strconv.FormatInt(stored.ID, 10)
	runAt := flowAt + "/runs/1"

	for _, p := range []autoProbe{
		{http.MethodGet, hookAt},
		{http.MethodPatch, hookAt},
		{http.MethodDelete, hookAt},
		{http.MethodPost, hookAt + "/test"},
		{http.MethodGet, hookAt + "/deliveries"},
		{http.MethodGet, deliveryAt},
		{http.MethodPost, deliveryAt + "/retry"},
		{http.MethodGet, flowAt},
		{http.MethodPatch, flowAt},
		{http.MethodDelete, flowAt},
		{http.MethodGet, flowAt + "/runs"},
		{http.MethodGet, runAt},
	} {
		body := ""
		if p.method == http.MethodPatch {
			body = "{}"
		}
		rec := e.call(t, a.manager.token, p.method, p.path, body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404 so the route cannot be used to probe (%s)",
				p.method, p.path, rec.Code, rec.Body.String())
		}
	}

	// Nothing was written by the refused retries and edits.
	if e.storeDelivery(t, stored.ID).Status == "pending" {
		t.Error("a refused retry rescheduled a delivery the caller cannot see")
	}

	// The lists do not name it either.
	rec = e.call(t, a.manager.token, http.MethodGet, autoAPI+"/webhooks", "")
	autoOK(t, rec)
	var hooks autoWebhooksBody
	autoDecode(t, rec, &hooks)
	if len(hooks.Webhooks) != 0 {
		t.Errorf("the list named %d webhooks the caller cannot see", len(hooks.Webhooks))
	}
	rec = e.call(t, a.manager.token, http.MethodGet, autoAPI+"/workflows", "")
	autoOK(t, rec)
	var flows autoWorkflowsBody
	autoDecode(t, rec, &flows)
	if len(flows.Workflows) != 0 {
		t.Errorf("the list named %d workflows the caller cannot see", len(flows.Workflows))
	}

	// And the very same routes answer for the administrator.
	autoOK(t, e.call(t, a.admin.token, http.MethodGet, hookAt, ""))
}

// TestAutomationExplicitDenyBeatsTheGlobalView is the same rule from the other
// side: an account that may see every channel except one is still told nothing
// about that one.
func TestAutomationExplicitDenyBeatsTheGlobalView(t *testing.T) {
	e := newAutoEnv(t)
	a := e.accounts(t)
	denied := e.channelWithID(t, 1001)
	e.grantChannelView(t, a.admin, denied)

	rec := e.call(t, a.admin.token, http.MethodPost, autoAPI+"/webhooks", autoWebhookPOST("Denied", denied))
	autoCreated(t, rec)
	var hook autoWebhookBody
	autoDecode(t, rec, &hook)
	rec = e.call(t, a.admin.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": denied, "enabled": false, "destinations": []any{},
	}))
	autoCreated(t, rec)
	var flow autoWorkflowBody
	autoDecode(t, rec, &flow)

	// The administrator holds every global right; the deny is per resource.
	e.denyChannelView(t, a.admin, denied)
	hookAt := autoAPI + "/webhooks/" + strconv.FormatInt(hook.Webhook.ID, 10)
	flowAt := autoAPI + "/workflows/" + strconv.FormatInt(flow.Workflow.ID, 10)
	for _, p := range []autoProbe{
		{http.MethodGet, hookAt}, {http.MethodPatch, hookAt}, {http.MethodDelete, hookAt},
		{http.MethodGet, hookAt + "/deliveries"},
		{http.MethodGet, flowAt}, {http.MethodPatch, flowAt}, {http.MethodDelete, flowAt},
		{http.MethodGet, flowAt + "/runs"},
	} {
		body := ""
		if p.method == http.MethodPatch {
			body = "{}"
		}
		rec := e.call(t, a.admin.token, p.method, p.path, body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404 (%s)", p.method, p.path, rec.Code, rec.Body.String())
		}
	}
}

// TestAutomationRefusesAnUnreachableSourceOrDestination is the write-side rule:
// naming a channel or a site the caller cannot see is refused with its own code,
// so the panel can say which field is wrong.
func TestAutomationRefusesAnUnreachableSourceOrDestination(t *testing.T) {
	e := newAutoEnv(t)
	a := e.accounts(t)
	mine := e.channelWithID(t, 1001)
	theirs := e.channelWithID(t, 1002)
	e.grantChannelView(t, a.admin, mine, theirs)
	e.grantChannelView(t, a.manager, mine)

	// A source channel the caller cannot see.
	rec := e.call(t, a.manager.token, http.MethodPost, autoAPI+"/webhooks", autoJSON(t, map[string]any{
		"name": "Not yours", "url": autoWebhookURL, "method": "POST",
		"auth_mode": "none", "source_channel_ids": []int64{theirs},
	}))
	autoFail(t, rec, http.StatusForbidden, gateway.CodeAutomationSourceDenied)

	rec = e.call(t, a.manager.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": theirs, "enabled": false, "destinations": []any{},
	}))
	autoFail(t, rec, http.StatusForbidden, gateway.CodeAutomationSourceDenied)

	// A Telegram destination the account owner is explicitly denied from
	// seeing. The destination account itself is owned by this principal.
	ownerToken := e.workflowOwnerToken(t)
	owner := identity{user: channelstore.User{ID: e.ownerUser}, token: ownerToken}
	e.denyChannelView(t, owner, theirs)
	rec = e.call(t, ownerToken, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": mine, "enabled": false,
		"destinations": []any{map[string]any{
			"destination_type": "telegram", "account_id": e.accountID,
			"client_key": "official", "channel_id": 1002,
		}},
	}))
	autoFail(t, rec, http.StatusForbidden, gateway.CodeAutomationDestDenied)

	// The same refusal on an edit, and on a WordPress destination.
	site := e.site(t)
	rec = e.call(t, a.manager.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": mine, "enabled": false,
		"destinations": []any{map[string]any{
			"destination_type": "wordpress", "site_id": site,
		}},
	}))
	autoFail(t, rec, http.StatusForbidden, gateway.CodeAutomationDestDenied)

	// Nothing was created.
	rec = e.call(t, a.manager.token, http.MethodGet, autoAPI+"/workflows", "")
	autoOK(t, rec)
	var flows autoWorkflowsBody
	autoDecode(t, rec, &flows)
	if len(flows.Workflows) != 0 {
		t.Errorf("a refused create left %d workflows behind", len(flows.Workflows))
	}

	// The owning principal naming the same targets is not refused.
	ownerToken = e.workflowOwnerToken(t) // restore global channel visibility
	rec = e.call(t, ownerToken, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": theirs, "enabled": false,
		"destinations": []any{map[string]any{
			"destination_type": "telegram", "account_id": e.accountID,
			"client_key": "official", "channel_id": 1002,
		}},
	}))
	autoCreated(t, rec)
}

// TestAutomationPermissionsDoNotBreakOtherRoutes: a refusal in this area says
// nothing about the rest of the API.
func TestAutomationPermissionsDoNotBreakOtherRoutes(t *testing.T) {
	e := newAutoEnv(t)
	a := e.accounts(t)
	source := e.channelWithID(t, 1001)
	e.grantChannelView(t, a.outsider, source)

	// One refusal first, so the test is about what happens after it.
	rec := e.call(t, a.outsider.token, http.MethodPost, autoAPI+"/webhooks", "{}")
	autoFail(t, rec, http.StatusForbidden, gateway.CodeForbidden)

	for _, p := range []autoProbe{
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/local/channels"},
		{http.MethodGet, "/api/v1/telegram/official/health"},
	} {
		rec := e.call(t, a.outsider.token, p.method, p.path, "")
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d after an automation refusal (%s)",
				p.method, p.path, rec.Code, rec.Body.String())
		}
	}
}

// TestAuthMeReportsAutomationAccess is what the panel reads to decide whether to
// show the automation area at all.
func TestAuthMeReportsAutomationAccess(t *testing.T) {
	e := newAutoEnv(t)
	a := e.accounts(t)

	for _, tc := range []struct {
		name string
		who  identity
		want bool
	}{
		{"administrator", a.admin, true},
		{"viewer", a.viewer, true},
		{"outsider", a.outsider, false},
	} {
		rec := e.call(t, tc.who.token, http.MethodGet, "/api/v1/auth/me", "")
		autoOK(t, rec)
		var body struct {
			Access *struct {
				Automation bool `json:"automation"`
				Channels   bool `json:"channels"`
				Content    bool `json:"content"`
			} `json:"access"`
		}
		autoDecode(t, rec, &body)
		if body.Access == nil {
			t.Fatalf("%s: /auth/me carries no access summary", tc.name)
		}
		if body.Access.Automation != tc.want {
			t.Errorf("%s: access.automation = %v, want %v", tc.name, body.Access.Automation, tc.want)
		}
	}
}

// TestOperatorTokenHoldsTheAutomationRights is the deployment credential: it may
// configure automation, and it has no account behind it.
func TestOperatorTokenHoldsTheAutomationRights(t *testing.T) {
	e := newAutoEnv(t)
	source := e.channelWithID(t, 1001)

	rec := e.call(t, operatorToken, http.MethodPost, autoAPI+"/webhooks", autoWebhookPOST("Operator", source))
	autoCreated(t, rec)
	var hook autoWebhookBody
	autoDecode(t, rec, &hook)
	autoOK(t, e.call(t, operatorToken, http.MethodGet,
		autoAPI+"/webhooks/"+strconv.FormatInt(hook.Webhook.ID, 10), ""))

	// A workflow may be created through it for inspection, but not switched on:
	// a workflow publishes under the account that created it, and the operator
	// token is not one.
	rec = e.call(t, operatorToken, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": source, "enabled": true,
		"destinations": []any{map[string]any{
			"destination_type": "telegram", "account_id": e.accountID,
			"client_key": "official", "channel_id": 1001,
		}},
	}))
	autoFail(t, rec, http.StatusNotFound, gateway.CodeNotFound)
}

// site registers one WordPress site and returns its local id, so a WordPress
// destination has a real row behind it.
func (e *autoEnv) site(t *testing.T) int64 {
	t.Helper()
	box, err := e.encrypter.Seal("application-password")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := e.store.CreateWordPressSite(t.Context(), channelstore.WordPressSite{
		URL: "https://blog.example.com", Title: "Example site",
		Username: "editor", Credential: box,
	})
	if err != nil {
		t.Fatalf("create a WordPress site: %v", err)
	}
	return rec.ID
}
