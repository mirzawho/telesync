package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// The publishing pipeline itself lives in internal/automation/publishing, so a
// background workflow publishes exactly the way this route does. What stays
// here is the HTTP part: decoding the body, shaping the answer, and mapping a
// failure onto the status and code the API documents. Nothing in this file
// sends to Telegram or to WordPress.

// maxPublishBody bounds a publishing request. The text is the longest field;
// the media bytes travel beside this body in the worker's own transfer.
const maxPublishBody = 256 << 10

// PublishStore is the durable record of publishing attempts. It is what makes
// a retry safe: the same key never posts twice.
type PublishStore interface {
	BeginPublishOperation(ctx context.Context, op channelstore.PublishOperation) (channelstore.PublishOperation, bool, error)
	FinishPublishOperation(ctx context.Context, id int64, status channelstore.PublishStatus, result, errorCode string) error
}

// publishError is an internal failure with the answer the route owes the
// caller. The publishing pipeline reports its own error; it is mapped onto
// this one so the route's answer is shaped here and nowhere else.
type publishError struct {
	status  int
	code    string
	message string
}

func (e *publishError) Error() string { return e.message }

// publishErrorOf maps a publishing failure onto the route's own.
func publishErrorOf(err error) error {
	if err == nil {
		return nil
	}
	var perr *publishing.Error
	if errors.As(err, &perr) {
		return &publishError{status: perr.Status, code: perr.Code, message: perr.Message}
	}
	return err
}

// publisher is the publishing pipeline behind this server's routes and any
// background work this process runs.
//
// It is built from the same configuration the routes already use: no store,
// worker or credential is reachable here that the manual path could not reach.
func (s *Server) publisher() *publishing.Service {
	return &publishing.Service{
		Workers:    publishingRegistry{s.cfg.Workers},
		Channels:   localChannelsOf(s.cfg.Channels),
		Publishes:  s.cfg.Publishes,
		Sites:      s.cfg.WordPress,
		SiteClient: s.cfg.WordPressClient,
		Secrets:    s.cfg.Secrets,
		Logf:       s.logf,
	}
}

// actorOfPublish is the authorization context a publication runs under: one
// account, and the permissions it holds as of this request.
func actorOfPublish(a *actor) publishing.Actor {
	return publishing.Actor{UserID: a.user.ID, Auth: a.auth}
}

// publishingRegistry adapts the client registry to the publisher's own worker
// contract. A gateway worker answers more than the publisher asks for, so the
// conversion is a widening one; the key is still never substituted.
type publishingRegistry struct {
	inner Registry
}

func (r publishingRegistry) Worker(key string) (publishing.Worker, bool) {
	if r.inner == nil {
		return nil, false
	}
	worker, ok := r.inner.Worker(key)
	if !ok {
		return nil, false
	}
	return worker, true
}

// localChannelsOf narrows the channel store to what publishing resolves
// through: a source is addressed by its local row id, so a store without that
// lookup cannot serve a publish and is reported as unavailable.
func localChannelsOf(store ManagedStore) publishing.ManagedChannels {
	if store == nil {
		return nil
	}
	local, ok := store.(LocalChannelStore)
	if !ok {
		return nil
	}
	return local
}

// copyTelegramMessage serves POST /api/v1/telegram/{client_key}/messages/copy.
//
// It copies a message that already exists on a source channel to a
// destination: the request names that message, and the body carries no file.
// A send of free text is a different route, and one this handler does not serve.
func (s *Server) copyTelegramMessage(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key string, worker Worker, requestID string) {

	a := actorOf(r)
	if !a.auth.Global(channelstore.PermissionContentPublish) {
		s.fail(w, http.StatusForbidden, CodePublishForbidden,
			"Publishing a message needs the content.publish permission.", requestID)
		return
	}
	var req publishing.Request
	if err := decodeJSONBodyLimited(w, r, &req, maxPublishBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if err := publishing.Validate(req); err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	req.IdempotencyKey = r.Header.Get("Idempotency-Key")

	result, err := s.publisher().PublishTelegram(ctx, actorOfPublish(a), key, req)
	if err != nil {
		s.publishFailure(w, publishErrorOf(err), requestID)
		return
	}
	if result.Replayed {
		// A confirmed success is replayed, never repeated: nothing was sent now.
		w.Header().Set("Idempotent-Replay", "true")
	}
	answer := envelope{Data: json.RawMessage(result.Payload), Meta: metaFor(requestID, s.cfg.Now())}
	writeJSON(w, http.StatusOK, answer)
}

// publishWordPress serves POST /api/v1/local/wordpress/{id}/posts.
func (s *Server) publishWordPress(w http.ResponseWriter, r *http.Request, siteID int64, requestID string) {
	ctx := r.Context()
	a := actorOf(r)
	if !a.auth.Global(channelstore.PermissionContentPublish) {
		s.fail(w, http.StatusForbidden, CodePublishForbidden,
			"Publishing a message needs the content.publish permission.", requestID)
		return
	}
	var req publishing.WordPressPost
	if err := decodeJSONBodyLimited(w, r, &req, maxPublishBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if err := publishing.ValidateWordPress(req); err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}
	req.IdempotencyKey = r.Header.Get("Idempotency-Key")

	result, err := s.publisher().PublishWordPress(ctx, actorOfPublish(a), siteID, req)
	if err != nil {
		s.publishFailure(w, publishErrorOf(err), requestID)
		return
	}
	if result.Replayed {
		w.Header().Set("Idempotent-Replay", "true")
	}
	answer := envelope{Data: json.RawMessage(result.Payload), Meta: metaFor(requestID, s.cfg.Now())}
	writeJSON(w, http.StatusOK, answer)
}

// publishFailure answers a publishing failure with the status and code the
// failure names. An error the pipeline did not classify is a store failure.
func (s *Server) publishFailure(w http.ResponseWriter, err error, requestID string) {
	var perr *publishError
	if errors.As(err, &perr) {
		s.fail(w, perr.status, perr.code, perr.message, requestID)
		return
	}
	s.fail(w, http.StatusInternalServerError, CodeStoreError, err.Error(), requestID)
}

// credentialFor resolves one registered site and its decrypted password.
//
// The password is opened here and never returned to a caller; a route that
// needs it presents it to the site and nothing else.
func (s *Server) credentialFor(ctx context.Context, a *actor, siteID int64) (channelstore.WordPressSite, string, error) {
	site, password, err := s.publisher().CredentialFor(ctx, actorOfPublish(a), siteID)
	return site, password, publishErrorOf(err)
}

// applyCapabilityFilter narrows a channel listing to what the caller asked
// for. capability=send keeps only channels Telegram says this account may
// post to: an unknown permission is never treated as permission granted.
//
// It is a convenience for the destination picker. The publish route
// revalidates the destination with Telegram itself before anything is sent.
func applyCapabilityFilter(q url.Values, in []rpc.Channel) []rpc.Channel {
	capability := q.Get("capability")
	if capability == "" {
		return in
	}
	out := make([]rpc.Channel, 0, len(in))
	for _, ch := range in {
		switch capability {
		case "send":
			if ch.Permissions.New {
				out = append(out, ch)
			}
		default:
			return in
		}
	}
	return out
}
