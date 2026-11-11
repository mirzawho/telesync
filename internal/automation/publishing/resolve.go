package publishing

import (
	"context"
	"net/http"
	"sort"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// visible reports whether the actor may see one resource of kind whose local id
// is localID, for a resource that is recorded locally (localID > 0) or not
// (localID == 0: it carries no assignment and only the global view admits it).
//
// It is the same rule the HTTP gateway applies to every route
// (internal/gateway/access.go): deny by default, an explicit deny wins, and a
// write needs the view. It is restated here because a background publication
// has no HTTP request to borrow the answer from, and the rule must not drift
// into something weaker.
func visible(authr *channelstore.Authorizer, kind channelstore.ResourceKind, action channelstore.Action, localID int64) bool {
	if authr == nil || localID < 0 {
		return false
	}
	if action == channelstore.ActionView && localID == 0 {
		// No local record: no assignment can exist, so the global view alone
		// decides. Asked as the global permission, never as resource 0, which no
		// assignment can name.
		return authr.CanList(kind)
	}
	return authr.Allowed(kind, action, localID)
}

// sourceMessage is the source message read back for one publishing request.
//
// worker is the connection that recorded it. The source's attachment is read
// through this and nothing else: the destination is a different account on a
// different session, whose peer store has never heard of the source channel and
// whose Telegram view of the world does not contain the source's file
// references. Handing the source's channel id or media id to the destination
// produces a not-found at best and someone else's message at worst.
type sourceMessage struct {
	row     channelstore.Channel
	message rpc.Message
	members []rpc.Message
	worker  Worker
}

// resolvePublishSource finds the source record and the message on it, and
// checks that the actor may read both.
//
// The actor's permissions are the ones it carries now: a background run is
// refused here the moment the account behind it loses the source, exactly as an
// interactive caller would be.
func (s *Service) resolvePublishSource(ctx context.Context, a Actor, req Request) (sourceMessage, error) {
	if s.Channels == nil {
		return sourceMessage{}, &Error{http.StatusServiceUnavailable, CodePublishSourceMissing,
			"The local channel store is unavailable."}
	}
	row, found, err := s.Channels.GetByID(ctx, req.Source.LocalChannelID)
	if err != nil {
		return sourceMessage{}, &Error{http.StatusInternalServerError, CodeStoreError, err.Error()}
	}
	if !found {
		return sourceMessage{}, &Error{http.StatusNotFound, CodePublishSourceMissing,
			"No local channel has that id."}
	}
	if !visible(a.Auth, channelstore.KindChannel, channelstore.ActionView, row.ID) {
		return sourceMessage{}, &Error{http.StatusForbidden, CodePublishSourceDenied,
			"The source channel may not be read by this account."}
	}
	worker, ok := s.publishWorker(row.ClientKey)
	if !ok {
		return sourceMessage{}, &Error{http.StatusServiceUnavailable, CodeWorkerUnavail,
			"The Telegram client the source belongs to is unavailable."}
	}
	page, err := worker.Message(ctx, row.ChannelID, req.Source.MessageID)
	if err != nil {
		return sourceMessage{}, &Error{http.StatusNotFound, CodePublishSourceMissing,
			"The source message is not available."}
	}
	if len(page.Messages) != 1 {
		return sourceMessage{}, &Error{http.StatusNotFound, CodePublishSourceMissing,
			"The source message is not available."}
	}
	message := page.Messages[0]
	source := sourceMessage{row: row, message: message, worker: worker}
	if len(req.Source.MessageIDs) > 0 {
		if message.GroupedID == "" {
			return sourceMessage{}, &Error{http.StatusBadRequest, CodeInvalidInput,
				"source.message_id does not belong to an album."}
		}
		for _, id := range req.Source.MessageIDs {
			member := message
			if id != message.ID {
				memberPage, fetchErr := worker.Message(ctx, row.ChannelID, id)
				if fetchErr != nil || len(memberPage.Messages) != 1 {
					return sourceMessage{}, &Error{http.StatusBadRequest, CodeInvalidInput,
						"Every source.message_ids entry must name an available member of the same album."}
				}
				member = memberPage.Messages[0]
			}
			if member.GroupedID != message.GroupedID {
				return sourceMessage{}, &Error{http.StatusBadRequest, CodeInvalidInput,
					"Every source.message_ids entry must belong to the selected album."}
			}
			source.members = append(source.members, member)
		}
		sort.Slice(source.members, func(i, j int) bool { return source.members[i].ID < source.members[j].ID })
	}
	return source, nil
}

// resolvePublishDestination checks the destination is one this actor may name
// and this connection can actually post to, and returns the connection the post
// goes out on.
//
// The Telegram peer id is resolved to its local record first, so the permission
// rule is asked about the record and never about a Telegram number that no
// assignment can name.
//
// The worker it answers with is the destination's and never the source's: the
// source is read through its own connection and the post is sent through this
// one. Handing a send to the source connection would post the message into the
// channel it came from.
func (s *Service) resolvePublishDestination(ctx context.Context, a Actor, key string, destID int64) (Worker, error) {
	localID := int64(0)
	if s.Channels != nil {
		row, ok, err := s.Channels.Get(ctx, key, destID)
		if err != nil {
			return nil, &Error{http.StatusInternalServerError, CodeStoreError, err.Error()}
		}
		if ok {
			localID = row.ID
		}
	}
	if !visible(a.Auth, channelstore.KindChannel, channelstore.ActionView, localID) {
		return nil, &Error{http.StatusForbidden, CodePublishDestDenied,
			"The destination channel may not be named by this account."}
	}
	worker, ok := s.publishWorker(key)
	if !ok {
		return nil, &Error{http.StatusServiceUnavailable, CodeWorkerUnavail,
			"The Telegram client this channel belongs to is unavailable."}
	}
	// Revalidate with Telegram's own answer: reading a channel is not proof of
	// being able to post to it, and a client-supplied boolean is worth nothing.
	channel, err := worker.Channel(ctx, rpc.ChannelRef{ChannelID: destID})
	if err != nil {
		return nil, &Error{http.StatusNotFound, CodePublishDestMissing,
			"The destination channel is not available to this connection."}
	}
	if !channel.Channel.Permissions.New {
		return nil, &Error{http.StatusForbidden, CodePublishDenied,
			"This account may not post to the destination channel."}
	}
	return worker, nil
}

// credentialFor resolves one registered site and its decrypted password.
//
// The password is opened here and never returned to a caller; a route that
// needs it presents it to the site and nothing else.
func (s *Service) credentialFor(ctx context.Context, a Actor, siteID int64) (channelstore.WordPressSite, string, error) {
	if s.Sites == nil || s.Secrets == nil {
		return channelstore.WordPressSite{}, "", &Error{http.StatusServiceUnavailable, CodePublishDestMissing,
			"WordPress site storage is not configured."}
	}
	if !visible(a.Auth, channelstore.KindWordPress, channelstore.ActionView, siteID) {
		return channelstore.WordPressSite{}, "", &Error{http.StatusForbidden, CodePublishDestDenied,
			"The WordPress site may not be used by this account."}
	}
	return s.siteCredential(ctx, siteID)
}

// siteCredential resolves one registered site and its decrypted password
// without asking who is asking.
//
// It is for the checks that are not themselves a publication — verifying that a
// configured term id still belongs to a site — where the caller's right to name
// the site has already been decided. Every publish goes through credentialFor.
func (s *Service) siteCredential(ctx context.Context, siteID int64) (channelstore.WordPressSite, string, error) {
	if s.Sites == nil || s.Secrets == nil {
		return channelstore.WordPressSite{}, "", &Error{http.StatusServiceUnavailable, CodePublishDestMissing,
			"WordPress site storage is not configured."}
	}
	site, ok, err := s.Sites.GetWordPressSite(ctx, siteID)
	if err != nil {
		return channelstore.WordPressSite{}, "", &Error{http.StatusInternalServerError, CodeStoreError, err.Error()}
	}
	if !ok {
		return channelstore.WordPressSite{}, "", &Error{http.StatusNotFound, CodePublishDestMissing,
			"No registered WordPress site has that id."}
	}
	password, err := s.Secrets.Open(site.Credential)
	if err != nil {
		return channelstore.WordPressSite{}, "", &Error{http.StatusServiceUnavailable, CodePublishDestMissing,
			"The stored credential could not be opened."}
	}
	return site, password, nil
}

// CredentialFor resolves one registered site and its decrypted password for a
// caller that needs to read from it. The password is usable only inside this
// process: the gateway never returns it.
func (s *Service) CredentialFor(ctx context.Context, a Actor, siteID int64) (channelstore.WordPressSite, string, error) {
	return s.credentialFor(ctx, a, siteID)
}

// publishWorker resolves one client's worker for a publish, answering false
// when the key is not configured. A worker is never substituted for another
// one: an unknown key is an error, not a fallback.
func (s *Service) publishWorker(key string) (Worker, bool) {
	if s.Workers == nil {
		return nil, false
	}
	return s.Workers.Worker(key)
}

// requirePublishPermission is the one gate every publication passes, whether it
// was asked for over HTTP or queued by a workflow.
//
// A background run is not a privileged path: it holds the publishing right of
// the account it runs under, or it does not publish at all.
func (s *Service) requirePublishPermission(a Actor) error {
	if !a.Auth.Global(channelstore.PermissionContentPublish) {
		return &Error{http.StatusForbidden, CodePublishForbidden,
			"Publishing a message needs the content.publish permission."}
	}
	return nil
}
