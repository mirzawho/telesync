package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
)

// publishAll publishes one run to each of its destinations and records what
// each one achieved.
//
// A run's destinations are fixed the first time it runs: one result row per
// destination is recorded then, and every later attempt works from those rows.
// A destination that already published is never published again, and one whose
// outcome is not confirmed is retried under the same idempotency key, so the
// publisher refuses to post it twice.
func (h *Handler) publishAll(ctx context.Context, workflow automation.Workflow, run automation.WorkflowRun,
	event automation.MessageEvent, p prepared) error {

	publications, err := h.destinationsFor(ctx, workflow, run)
	if err != nil {
		return err
	}
	if len(publications) == 0 {
		return h.finishRun(ctx, run, channelstore.WorkflowRunFailed, "no_destinations",
			"The workflow has no enabled destination to publish to.", false, 0, 0)
	}

	authorID, found, err := h.Workflows.WorkflowAuthor(ctx, workflow.ID)
	if err != nil {
		return err
	}
	if !found {
		return h.finishRun(ctx, run, channelstore.WorkflowRunFailed, "workflow_author_missing",
			"The account this workflow publishes under no longer exists.", false, 0, 0)
	}
	if authorID <= 0 {
		// A workflow made through the operator token has no owning account, and
		// a publishing attempt is recorded against one so a retry cannot post
		// twice. It is refused with its reason rather than run with authority
		// nobody's account holds.
		return h.finishRun(ctx, run, channelstore.WorkflowRunFailed, "workflow_author_missing",
			"This workflow has no owning account: it was created with the operator token. "+
				"Create the workflow from an account so its publications can be recorded against an author.",
			false, 0, 0)
	}

	published, retried := 0, 0
	lastCode, lastMessage := "", ""
	retryable := false
	for _, pub := range publications {
		if pub.Status == channelstore.WorkflowPublicationPublished {
			// Already done, and never done twice.
			published++
			continue
		}
		res := h.publishOne(ctx, workflow, event, p, pub, authorID, run.ID)
		switch res.Outcome {
		case automation.PublishSucceeded:
			published++
		case automation.PublishUnknown:
			// The post may or may not exist. It is retried under the same key,
			// where the publisher refuses to send it again and reports the
			// outcome as still unconfirmed.
			retried++
			retryable = true
		default:
			retried++
			if publishRetryable(res.ErrorCode) {
				retryable = true
			}
		}
		if res.ErrorCode != "" {
			lastCode, lastMessage = res.ErrorCode, res.ErrorMessage
		}
	}

	switch {
	case published == len(publications):
		status := channelstore.WorkflowRunPublished
		if p.fallback {
			status = channelstore.WorkflowRunPublishedWithFallback
		}
		return h.finishRun(ctx, run, status, "", "", false, published, len(publications))
	case published > 0:
		// Some destinations took it and some did not. That is never reported as
		// a success, and the ones that did are not published again.
		return h.finishRun(ctx, run, channelstore.WorkflowRunPartiallyPublished, lastCode, lastMessage,
			retryable, published, len(publications))
	default:
		if lastCode == "" {
			lastCode = "publish_failed"
			lastMessage = "No destination accepted the message."
		}
		return h.finishRun(ctx, run, channelstore.WorkflowRunFailed, lastCode, lastMessage,
			retryable, published, len(publications))
	}
}

// finishRun closes a run with what happened and tells the queue whether there
// is anything left worth trying.
func (h *Handler) finishRun(ctx context.Context, run automation.WorkflowRun, status, code, message string,
	retryable bool, published, total int) error {

	_ = h.Runs.CompleteWorkflowRun(ctx, run.ID, status, code, message)
	if status == channelstore.WorkflowRunPublished || status == channelstore.WorkflowRunPublishedWithFallback {
		return nil
	}
	if code == "" {
		code = "publish_failed"
	}
	if message == "" {
		message = "Not every destination accepted the message."
	}
	h.logf("workflows: run=%d status=%s published=%d/%d code=%s", run.ID, status, published, total, code)
	return &automation.JobError{Code: code, Message: message, Retryable: retryable}
}

// destinationsFor is the run's destination list: the ones recorded on an earlier
// attempt, or the workflow's enabled destinations the first time it runs.
func (h *Handler) destinationsFor(ctx context.Context, workflow automation.Workflow,
	run automation.WorkflowRun) ([]automation.WorkflowPublication, error) {

	recorded, err := h.Runs.WorkflowPublications(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	out := make([]automation.WorkflowPublication, 0, len(recorded))
	if len(recorded) > 0 {
		for _, pub := range recorded {
			ensured, _, err := h.Runs.EnsureWorkflowPublication(ctx, run.ID, pub)
			if err != nil {
				return nil, err
			}
			out = append(out, ensured)
		}
		return out, nil
	}
	for _, dest := range workflow.Destinations {
		if !dest.Enabled {
			continue
		}
		ensured, _, err := h.Runs.EnsureWorkflowPublication(ctx, run.ID, automation.WorkflowPublication{
			DestinationType: dest.DestinationType,
			ClientKey:       dest.ClientKey,
			ChannelID:       dest.ChannelID,
			SiteID:          dest.SiteID,
			AccountID:       dest.AccountID,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, ensured)
	}
	return out, nil
}

// publishOne publishes to one destination and records what it confirmed.
func (h *Handler) publishOne(ctx context.Context, workflow automation.Workflow, event automation.MessageEvent,
	p prepared, pub automation.WorkflowPublication, authorID, runID int64) automation.PublishResult {

	options := optionsFor(workflow, pub)
	if p.aiTitle != "" {
		if _, named := options[publishing.OptionTitle]; !named {
			// The model wrote a title and the destination does not fix one.
			options[publishing.OptionTitle] = p.aiTitle
		}
	}

	request := automation.PublishRequest{
		Source: automation.SourceRef{
			ManagedChannelID:  event.ManagedChannelID,
			ClientKey:         event.SourceClientKey,
			TelegramChannelID: event.TelegramChannelID,
			MessageID:         event.MessageID,
		},
		Destination: automation.Destination{
			Type:      automation.DestinationType(pub.DestinationType),
			ClientKey: pub.ClientKey,
			ChannelID: pub.ChannelID,
			SiteID:    pub.SiteID,
			AccountID: pub.AccountID,
			Options:   options,
		},
		Text:     p.content,
		Entities: p.entities,
		// MediaMode is left to the destination's own policy, which preserves
		// the source attachment unless the destination says otherwise. Images
		// the AI never saw are still published.
		Options:        automation.PublishOptions{},
		IdempotencyKey: idempotencyKey(event, workflow.ID, pub),
		UserID:         authorID,
	}
	var res automation.PublishResult
	var err error
	if pub.DestinationType == string(automation.DestinationTelegram) &&
		strings.TrimSpace(pub.AccountID) == "" {
		// A pre-binding publication is historical data, not permission to choose
		// an account from its server key or current registration. Keep the result
		// row and stop this run until an explicit account selection is made.
		res = automation.PublishResult{
			Outcome: automation.PublishFailed, ErrorCode: "account_selection_required",
			ErrorMessage: "The Telegram destination has no selected account and remains paused.",
		}
	} else {
		res, err = h.Publisher.Publish(ctx, request)
	}
	if err != nil && res.ErrorCode == "" {
		res.ErrorCode = "publish_failed"
		res.ErrorMessage = errMessage(err)
	}
	if res.Outcome == "" {
		res.Outcome = automation.PublishFailed
	}

	status := channelstore.WorkflowPublicationFailed
	switch res.Outcome {
	case automation.PublishSucceeded:
		status = channelstore.WorkflowPublicationPublished
	case automation.PublishUnknown:
		status = channelstore.WorkflowPublicationUnknown
	}
	if err := h.Runs.FinishWorkflowPublication(ctx, pub.ID, status,
		res.RemoteMessageID, res.RemotePostID, res.RemoteURL, res.ErrorCode, res.ErrorMessage); err != nil {
		h.logf("workflows: run=%d could not record the result for %s: %v", runID, pub.DestinationType, err)
	}

	if pub.DestinationType == string(automation.DestinationTelegram) &&
		res.Outcome == automation.PublishSucceeded {
		h.recordOutbound(ctx, pub, res.RemoteMessageID, runID)
	}
	return res
}

// idempotencyKey names the logical source item, workflow and destination.
// Album members share GroupedID, so their independently queued jobs arrive at
// the publisher under the same key. Standalone messages retain message-level
// identity. Hashing keeps the key bounded and avoids putting source metadata in
// the publisher's idempotency record.
func idempotencyKey(event automation.MessageEvent, workflowID int64, pub automation.WorkflowPublication) string {
	sourceItem := "message:" + strconv.FormatInt(event.MessageID, 10)
	if event.GroupedID != nil && *event.GroupedID != 0 {
		sourceItem = "group:" + strconv.FormatInt(*event.GroupedID, 10)
	}
	identity := strings.Join([]string{
		"workflow-publication-v3", event.NetworkKey,
		strconv.FormatInt(event.TelegramChannelID, 10), sourceItem,
		strconv.FormatInt(workflowID, 10), pub.DestinationType, pub.AccountID, pub.ClientKey,
		strconv.FormatInt(pub.ChannelID, 10), strconv.FormatInt(pub.SiteID, 10),
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return "wf-pub-v2-" + hex.EncodeToString(sum[:])
}

// optionsFor is the destination's own publishing options.
//
// A retry works from the destination rows the run recorded, so an edit made
// after the run was queued does not change what it publishes. A destination
// removed since then has no options left and publishes with the defaults.
func optionsFor(workflow automation.Workflow, pub automation.WorkflowPublication) map[string]any {
	for _, dest := range workflow.Destinations {
		if dest.DestinationType == pub.DestinationType && dest.AccountID == pub.AccountID &&
			dest.ClientKey == pub.ClientKey &&
			dest.ChannelID == pub.ChannelID && dest.SiteID == pub.SiteID {
			out := make(map[string]any, len(dest.Options)+1)
			for k, v := range dest.Options {
				out[k] = v
			}
			return out
		}
	}
	return map[string]any{}
}

// recordOutbound writes the loop-prevention record for one published message.
//
// It is recorded under the destination's network and the destination's own
// address, with the message id the send confirmed, so ingestion recognises the
// message when it comes back and does not run the workflow a second time.
//
// Identity limits, for a deployment running more than one server: the record
// names a network, a connection, a channel and a message id. Two accounts on
// one network observing the same channel see one message and it is one record,
// which is what stops a second account from re-triggering the workflow. What it
// cannot do is carry across networks — the same channel and message ids on
// another network are a different message and are recorded as one — and it
// cannot name a message the destination never described: a send that came back
// without a message id leaves no record, and is logged instead.
func (h *Handler) recordOutbound(ctx context.Context, pub automation.WorkflowPublication,
	remoteMessageID, runID int64) {

	if h.Outbound == nil || remoteMessageID <= 0 {
		if remoteMessageID <= 0 {
			h.logf("workflows: run=%d published to %s/%d but the destination reported no message id, "+
				"so the loop-prevention record was not written", runID, pub.ClientKey, pub.ChannelID)
		}
		return
	}
	network := ""
	if h.Networks != nil {
		network, _ = h.Networks.NetworkKey(pub.ClientKey)
	}
	if network == "" {
		h.logf("workflows: run=%d published to %s/%d but the destination's network is unknown, "+
			"so the loop-prevention record was not written", runID, pub.ClientKey, pub.ChannelID)
		return
	}
	err := h.Outbound.MarkPublished(ctx, automation.OutboundMessage{
		NetworkKey:      network,
		ClientKey:       pub.ClientKey,
		ChannelID:       pub.ChannelID,
		RemoteMessageID: remoteMessageID,
		Origin:          "workflow",
		OriginID:        runID,
	})
	if err != nil {
		// The message is published either way; only the record failed. It is
		// logged loudly rather than turned into a publication failure.
		h.logf("workflows: run=%d published to %s/%d but the loop-prevention record failed: %v",
			runID, pub.ClientKey, pub.ChannelID, err)
	}
}

// publishRetryable reports whether a destination failure is worth another
// attempt. A refusal is permanent; a transport that did not answer is not proof
// that the destination rejected the post.
func publishRetryable(code string) bool {
	switch code {
	case publishing.CodePublishForbidden, publishing.CodePublishDenied,
		publishing.CodePublishSourceDenied, publishing.CodePublishDestDenied,
		publishing.CodePublishUnsupported, publishing.CodePublishMediaTooLarge,
		publishing.CodePublishConflict, publishing.CodeInvalidInput,
		publishing.CodePublishWordPressAuth, publishing.CodePublishWordPressForbidden,
		publishing.CodePublishWordPressIncompatible, "account_selection_required":
		return false
	default:
		return true
	}
}
