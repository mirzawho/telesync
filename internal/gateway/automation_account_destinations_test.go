package gateway_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
)

func workflowDestinationEnv(t *testing.T) (*autoEnv, identity, string, int64, int64) {
	t.Helper()
	e := newAutoEnv(t)
	owner := e.account(t, "workflow-destination-owner", channelstore.GlobalPermissions()...)
	accountID := e.sessionAccount(t, owner, "official", channelstore.NetworkTelegram, readyWorker())
	source := e.ownedChannelRow(t, accountID, "official", 1001)
	destination := e.ownedChannelRow(t, accountID, "official", 1002)
	e.grantChannelView(t, owner, source.ID, destination.ID)
	return e, owner, accountID, source.ID, destination.ChannelID
}

func TestWorkflowTelegramDestinationRequiresAnExplicitOwnedAccount(t *testing.T) {
	e, owner, accountID, sourceID, destinationID := workflowDestinationEnv(t)

	rec := e.call(t, owner.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": sourceID,
		"enabled":            false,
		"destinations": []any{map[string]any{
			"destination_type": "telegram",
			"client_key":       "official",
			"channel_id":       destinationID,
		}},
	}))
	autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
	if !strings.Contains(rec.Body.String(), "account_id") {
		t.Fatalf("missing account response = %s, want an account_id validation error", rec.Body.String())
	}

	rec = e.call(t, owner.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": sourceID,
		"enabled":            false,
		"destinations": []any{map[string]any{
			"destination_type": "telegram",
			"account_id":       accountID,
			"channel_id":       destinationID,
		}},
	}))
	autoCreated(t, rec)
	var created struct {
		Workflow struct {
			Destinations []struct {
				AccountID string `json:"account_id"`
				ClientKey string `json:"client_key"`
				ChannelID int64  `json:"channel_id"`
			} `json:"destinations"`
		} `json:"workflow"`
	}
	autoDecode(t, rec, &created)
	if len(created.Workflow.Destinations) != 1 {
		t.Fatalf("destinations = %+v", created.Workflow.Destinations)
	}
	destination := created.Workflow.Destinations[0]
	if destination.AccountID != accountID ||
		destination.ClientKey != "official" || destination.ChannelID != destinationID {
		t.Fatalf("destination = %+v, want its account_id and informational server key", destination)
	}
}

func TestWorkflowTelegramDestinationCannotResolveAnotherOwnersAccountOrRegistration(t *testing.T) {
	e, owner, _, sourceID, _ := workflowDestinationEnv(t)
	foreign := e.account(t, "workflow-destination-foreign", channelstore.GlobalPermissions()...)
	foreignAccountID := e.sessionAccount(t, foreign, "official", channelstore.NetworkTelegram, readyWorker())
	foreignChannel := e.ownedChannelRow(t, foreignAccountID, "official", 1002)

	rec := e.call(t, owner.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": sourceID,
		"enabled":            false,
		"destinations": []any{map[string]any{
			"destination_type": "telegram",
			"account_id":       foreignAccountID,
			"channel_id":       foreignChannel.ChannelID,
		}},
	}))
	autoFail(t, rec, http.StatusNotFound, gateway.CodeNotFound)

	rec = e.call(t, owner.token, http.MethodGet, autoAPI+"/workflows", "")
	autoOK(t, rec)
	var workflows struct {
		Workflows []json.RawMessage `json:"workflows"`
	}
	autoDecode(t, rec, &workflows)
	if len(workflows.Workflows) != 0 {
		t.Fatalf("foreign destination request persisted %d workflow(s)", len(workflows.Workflows))
	}
}

func TestWorkflowTelegramDestinationUpdateRequiresAccountSelection(t *testing.T) {
	e, owner, _, sourceID, destinationID := workflowDestinationEnv(t)
	// A legacy workflow with no destination account remains readable; it is not
	// assigned to the source account or any other account by inference.
	rec := e.call(t, owner.token, http.MethodPost, autoAPI+"/workflows", autoJSON(t, map[string]any{
		"managed_channel_id": sourceID,
		"enabled":            false,
		"destinations":       []any{},
	}))
	autoCreated(t, rec)
	var created autoWorkflowBody
	autoDecode(t, rec, &created)

	rec = e.call(t, owner.token, http.MethodPatch,
		autoAPI+"/workflows/"+strconv.FormatInt(created.Workflow.ID, 10), autoJSON(t, map[string]any{
			"destinations": []any{map[string]any{
				"destination_type": "telegram",
				"client_key":       "official",
				"channel_id":       destinationID,
			}},
		}))
	autoFail(t, rec, http.StatusBadRequest, gateway.CodeInvalidInput)
	if !strings.Contains(rec.Body.String(), "account_id") {
		t.Fatalf("missing account response = %s, want an account_id validation error", rec.Body.String())
	}
}
