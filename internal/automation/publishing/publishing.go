// Package publishing is the publishing pipeline: one prepared post, one
// destination, one attempt.
//
// It is the v0.0.2 implementation that used to live inside the HTTP gateway,
// exposed so a background workflow publishes exactly the way an interactive
// caller does. Nothing here builds an HTTP request or an HTTP answer: the
// gateway decodes its body, renders its envelope and maps the failures, and the
// workflow engine asks for the same operation with an automation.PublishRequest.
// Both go through the one implementation below.
//
// Two rules are the reason this package exists rather than a copy of the old
// code:
//
//   - A publication runs under a real account. The actor is resolved again at
//     execution time and its current grants decide, so a stale grant never
//     publishes and a background run has no more power than the account it
//     belongs to.
//   - A send is recorded against the idempotency key, so a retry — of an HTTP
//     request or of a workflow run — reuses the one attempt rather than posting
//     twice.
package publishing

import (
	"context"
	"net/http"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/wordpress"
)

// Actor is the authorization context one publication runs under.
//
// It is not a session: it is one account's identity and the permissions that
// account holds right now. The gateway builds it from the authenticated
// request; a background run builds it from the workflow's author by reading the
// account and its grants again at execution time.
type Actor struct {
	UserID int64
	Auth   *channelstore.Authorizer
}

// Worker is the upstream operation set one publication needs from a client. It
// is the slice of the gateway's worker contract this pipeline uses; a worker is
// never substituted for another one.
type Worker interface {
	Message(ctx context.Context, channelID int64, messageID int) (rpc.MessagePage, error)
	Media(ctx context.Context, channelID int64, messageID, photoIndex int, slice rpc.MediaSlice) (rpc.MediaStream, error)
	Channel(ctx context.Context, ref rpc.ChannelRef) (rpc.ChannelResult, error)
	SendMessage(ctx context.Context, req rpc.SendRequest) (rpc.SendResult, error)
}

// Registry maps a configured client key to its worker. An unknown key is an
// error, never a fallback to another client.
type Registry interface {
	Worker(key string) (Worker, bool)
}

// RegistryFunc adapts a lookup function to Registry, so a deployment can hand
// over the registry it already has — one whose workers answer more than this
// package asks for — without a wrapper type.
type RegistryFunc func(key string) (Worker, bool)

// Worker implements Registry.
func (f RegistryFunc) Worker(key string) (Worker, bool) { return f(key) }

// ManagedChannels is the local record of managed channels. A publication
// resolves the source through it and maps a destination Telegram id onto the
// local row the permission rule is asked about.
type ManagedChannels interface {
	Get(ctx context.Context, clientKey string, channelID int64) (channelstore.Channel, bool, error)
	GetByID(ctx context.Context, id int64) (channelstore.Channel, bool, error)
}

// PublishRecords is the durable record of publishing attempts: what makes a
// retry safe.
type PublishRecords interface {
	BeginPublishOperation(ctx context.Context, op channelstore.PublishOperation) (channelstore.PublishOperation, bool, error)
	FinishPublishOperation(ctx context.Context, id int64, status channelstore.PublishStatus, result, errorCode string) error
}

// Sites is the local registry of WordPress destinations.
type Sites interface {
	GetWordPressSite(ctx context.Context, id int64) (channelstore.WordPressSite, bool, error)
}

// SiteClient is the remote side of a WordPress publication. ListTerms reads
// the site's own taxonomy, which is how a configured term id is confirmed to
// belong to the site before a workflow may name it.
type SiteClient interface {
	CreatePost(ctx context.Context, baseURL, username, password string, in wordpress.PostInput) (wordpress.PostResult, error)
	UploadMedia(ctx context.Context, baseURL, username, password, filename, mimeType string, data []byte) (wordpress.MediaRef, error)
	FindTag(ctx context.Context, baseURL, username, password, name string) (wordpress.TagRef, bool, error)
	CreateTag(ctx context.Context, baseURL, username, password, name string) (wordpress.TagRef, error)
	ListTerms(ctx context.Context, baseURL, username, password, taxonomy, search string, page, perPage int) (wordpress.TermPage, error)
}

// SecretBox opens a stored site credential. The password is used to talk to the
// site and is never returned to a caller.
type SecretBox interface {
	Open(box secrets.Box) (string, error)
}

// ActorResolver reads one account's current authorization state.
//
// It is called at execution time and never cached: a permission change takes
// effect on the next publication, and an account that is gone or inactive stops
// publishing immediately.
type ActorResolver interface {
	GetUser(ctx context.Context, id int64) (channelstore.User, bool, error)
	AuthorizerFor(ctx context.Context, id int64) (*channelstore.Authorizer, error)
}

// Service is the publishing pipeline.
//
// Every field is required unless the comment says otherwise: a publication that
// cannot be made because the deployment is not wired for it is reported as a
// failure, never guessed at.
type Service struct {
	Workers    Registry
	Channels   ManagedChannels
	Publishes  PublishRecords
	Sites      Sites
	SiteClient SiteClient
	Secrets    SecretBox
	// Actors resolves the account a background publication runs under. The
	// interactive route passes its own actor and does not need it.
	Actors ActorResolver
	// Logf receives one line per upstream failure. Nil discards.
	Logf func(format string, args ...any)
}

func (s *Service) logf(format string, args ...any) {
	if s != nil && s.Logf != nil {
		s.Logf(format, args...)
	}
}

// actorFor resolves the account a background publication runs under, reading
// its permissions again rather than trusting anything carried in a request.
//
// An account that does not exist, is inactive or cannot be read stops the
// operation: a background job never runs with more authority than the account
// that configured it, and never with a grant that has since been withdrawn.
func (s *Service) actorFor(ctx context.Context, userID int64) (Actor, error) {
	if userID <= 0 {
		// A publication is recorded against the account it runs under: the
		// idempotency record names one, and a retry with the same key is what
		// keeps a post from being sent twice. Work with no owning account — a
		// workflow made through the operator token — therefore cannot publish,
		// and is refused rather than run with the deployment's own authority.
		//
		// An owned workflow never takes this path: its account is read again
		// here, on every publication, and a grant that has been withdrawn stops
		// the next one.
		return Actor{}, &Error{Status: http.StatusForbidden, Code: CodePublishForbidden,
			Message: "This publication has no owning account. A workflow must be created from an account, " +
				"not with the operator token, because a publishing attempt is recorded against its author."}
	}
	if s.Actors == nil {
		return Actor{}, &Error{Status: http.StatusServiceUnavailable, Code: CodePublishForbidden,
			Message: "This deployment cannot resolve the account a background publication runs under."}
	}
	user, found, err := s.Actors.GetUser(ctx, userID)
	if err != nil {
		return Actor{}, &Error{Status: http.StatusInternalServerError, Code: CodeStoreError, Message: err.Error()}
	}
	if !found || !user.IsActive {
		return Actor{}, &Error{Status: http.StatusForbidden, Code: CodePublishForbidden,
			Message: "The account this workflow publishes under no longer exists or is inactive."}
	}
	authr, err := s.Actors.AuthorizerFor(ctx, userID)
	if err != nil {
		return Actor{}, &Error{Status: http.StatusInternalServerError, Code: CodeStoreError, Message: err.Error()}
	}
	if authr == nil {
		return Actor{}, &Error{Status: http.StatusForbidden, Code: CodePublishForbidden,
			Message: "The account this workflow publishes under has no readable permissions."}
	}
	return Actor{UserID: user.ID, Auth: authr}, nil
}
