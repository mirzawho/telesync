package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/wordpress"
)

// maxPublishBody bounds a publishing request. The text is the longest field;
// the media bytes travel beside this body in the worker's own transfer.
const maxPublishBody = 256 << 10

// PublishStore is the durable record of publishing attempts. It is what makes
// a retry safe: the same key never posts twice.
type PublishStore interface {
	BeginPublishOperation(ctx context.Context, op channelstore.PublishOperation) (channelstore.PublishOperation, bool, error)
	FinishPublishOperation(ctx context.Context, id int64, status channelstore.PublishStatus, result, errorCode string) error
}

// Media modes a publishing request may name.
const (
	mediaPreserveSource = "preserve_source"
	mediaOmit           = "omit"
)

// publishRequest is the prepared outgoing post a client submits.
//
// source names the message the post was composed from, so the backend can read
// it and its attachment through the connection that recorded it. The source
// itself is never written to: this request creates a new message elsewhere.
type publishRequest struct {
	Source struct {
		// LocalChannelID is the managed_channels row, never a Telegram id.
		LocalChannelID int64 `json:"local_channel_id"`
		MessageID      int   `json:"message_id"`
	} `json:"source"`
	Destination struct {
		// ChannelID is the Telegram channel id on the connection named in the
		// path. It must come from that connection's own channel data.
		ChannelID string `json:"channel_id"`
	} `json:"destination"`
	Content struct {
		// Text is the outgoing body exactly as the caller composed it.
		Text string `json:"text"`
		// Entities are its formatting spans, in UTF-16 code units.
		Entities []rpc.Entity `json:"entities"`
	} `json:"content"`
	Media struct {
		// Mode is "preserve_source" to send the source attachment along, or
		// "omit" for text only.
		Mode string `json:"mode"`
	} `json:"media"`
	Options struct {
		Silent                bool `json:"silent"`
		DisableWebPagePreview bool `json:"disable_web_page_preview"`
	} `json:"options"`
}

// publishTelegram serves POST /api/v1/telegram/{client_key}/messages.
func (s *Server) publishTelegram(ctx context.Context, w http.ResponseWriter, r *http.Request,
	key string, worker Worker, requestID string) {

	a := actorOf(r)
	if !a.auth.Global(channelstore.PermissionContentPublish) {
		s.fail(w, http.StatusForbidden, CodePublishForbidden,
			"Publishing a message needs the content.publish permission.", requestID)
		return
	}
	var req publishRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxPublishBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if err := validatePublishRequest(req); err != nil {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, err.Error(), requestID)
		return
	}

	source, err := s.resolvePublishSource(ctx, a, req)
	if err != nil {
		s.publishFailure(w, err, requestID)
		return
	}
	destID, err := strconv.ParseInt(req.Destination.ChannelID, 10, 64)
	if err != nil || destID <= 0 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"destination.channel_id must be a positive Telegram channel id.", requestID)
		return
	}
	if err := s.resolvePublishDestination(ctx, a, key, destID); err != nil {
		s.publishFailure(w, err, requestID)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	operation, err := s.beginPublish(ctx, a, idempotencyKey, key, req)
	if err != nil {
		s.publishFailure(w, err, requestID)
		return
	}
	if operation != nil && operation.Status == channelstore.PublishSucceeded {
		// A confirmed success is replayed, never repeated.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Idempotent-Replay", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(operation.Result))
		return
	}

	send, err := s.buildSend(ctx, source, key, worker, req)
	if err != nil {
		s.finishPublish(ctx, operation, channelstore.PublishFailed, "", CodePublishSourceMissing)
		s.publishFailure(w, err, requestID)
		return
	}
	result, sendErr := worker.SendMessage(ctx, send)
	if sendErr != nil {
		// A failure Telegram answered is a failure; one we never heard back
		// from is unknown, and is never retried behind the caller's back.
		status := channelstore.PublishFailed
		code := CodePublishFailed
		var flood interface{ FloodSeconds() int }
		switch {
		case errors.As(sendErr, &flood):
			code = CodeRateLimited
		case errors.Is(sendErr, rpc.ErrSendForbidden):
			code = CodePublishDenied
		case errors.Is(sendErr, rpc.ErrSendUnsupported):
			code = CodePublishUnsupported
		case errors.Is(sendErr, rpc.ErrTimeout), errors.Is(sendErr, rpc.ErrUnavailable):
			status, code = channelstore.PublishUnknown, CodePublishUnknown
		}
		s.finishPublish(ctx, operation, status, "", code)
		s.publishFailure(w, &publishError{status: publishStatusOf(sendErr, code), code: code, message: publishMessage(sendErr)}, requestID)
		return
	}

	body := map[string]any{
		"message":     result.Message,
		"destination": map[string]any{"client_key": key, "channel_id": req.Destination.ChannelID},
	}
	if result.UnsendableEntities > 0 {
		body["unsendable_entities"] = result.UnsendableEntities
	}
	answer := envelope{Data: body, Meta: metaFor(requestID, s.cfg.Now())}
	payload, err := json.Marshal(answer)
	if err != nil {
		s.finishPublish(ctx, operation, channelstore.PublishSucceeded, "", "")
		s.fail(w, http.StatusInternalServerError, CodeStoreError, "The result could not be encoded.", requestID)
		return
	}
	s.finishPublish(ctx, operation, channelstore.PublishSucceeded, string(payload), "")
	writeJSON(w, http.StatusOK, answer)
}

// publishError is an internal failure with the answer the route owes the caller.
type publishError struct {
	status  int
	code    string
	message string
}

func (e *publishError) Error() string { return e.message }

func (s *Server) publishFailure(w http.ResponseWriter, err error, requestID string) {
	var perr *publishError
	if errors.As(err, &perr) {
		s.fail(w, perr.status, perr.code, perr.message, requestID)
		return
	}
	s.fail(w, http.StatusInternalServerError, CodeStoreError, err.Error(), requestID)
}

// validatePublishRequest checks the shape of the request before anything is
// resolved or sent.
func validatePublishRequest(req publishRequest) error {
	if req.Source.LocalChannelID <= 0 {
		return errors.New("source.local_channel_id must be a positive local channel id")
	}
	if req.Source.MessageID <= 0 {
		return errors.New("source.message_id must be a positive message id")
	}
	if req.Media.Mode != mediaPreserveSource && req.Media.Mode != mediaOmit {
		return fmt.Errorf("media.mode must be %q or %q", mediaPreserveSource, mediaOmit)
	}
	if len(req.Content.Text) == 0 && req.Media.Mode == mediaOmit {
		return errors.New("content.text must not be empty when no media is sent")
	}
	for _, e := range req.Content.Entities {
		if !rpcEntityValid(req.Content.Text, e) {
			return errors.New("an entity span falls outside content.text")
		}
	}
	return nil
}

// rpcEntityValid reports whether a span lies inside the text it describes,
// measured in Telegram's UTF-16 code units.
func rpcEntityValid(text string, e rpc.Entity) bool {
	if e.Offset < 0 || e.Length < 0 {
		return false
	}
	end := e.Offset + e.Length
	return end >= e.Offset && end <= utf16Len(text)
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
			continue
		}
		n++
	}
	return n
}

// sourceMessage is the source message read back for one publishing request.
type sourceMessage struct {
	row     channelstore.Channel
	message rpc.Message
}

// resolvePublishSource finds the source record and the message on it, and
// checks that the caller may read both.
func (s *Server) resolvePublishSource(ctx context.Context, a *actor, req publishRequest) (sourceMessage, error) {
	store, ok := s.cfg.Channels.(LocalChannelStore)
	if s.cfg.Channels == nil || !ok {
		return sourceMessage{}, &publishError{http.StatusServiceUnavailable, CodePublishSourceMissing,
			"The local channel store is unavailable."}
	}
	row, found, err := store.GetByID(ctx, req.Source.LocalChannelID)
	if err != nil {
		return sourceMessage{}, &publishError{http.StatusInternalServerError, CodeStoreError, err.Error()}
	}
	if !found {
		return sourceMessage{}, &publishError{http.StatusNotFound, CodePublishSourceMissing,
			"No local channel has that id."}
	}
	if !visible(a.auth, channelstore.KindChannel, channelstore.ActionView, row.ID) {
		return sourceMessage{}, &publishError{http.StatusForbidden, CodePublishSourceDenied,
			"The source channel may not be read by this account."}
	}
	worker, ok := s.publishWorker(row.ClientKey)
	if !ok {
		return sourceMessage{}, &publishError{http.StatusServiceUnavailable, CodeWorkerUnavail,
			"The Telegram client the source belongs to is unavailable."}
	}
	page, err := worker.Message(ctx, row.ChannelID, req.Source.MessageID)
	if err != nil {
		return sourceMessage{}, &publishError{http.StatusNotFound, CodePublishSourceMissing,
			"The source message is not available."}
	}
	if len(page.Messages) != 1 {
		return sourceMessage{}, &publishError{http.StatusNotFound, CodePublishSourceMissing,
			"The source message is not available."}
	}
	return sourceMessage{row: row, message: page.Messages[0]}, nil
}

// resolvePublishDestination checks the destination is one this caller may name
// and this connection can actually post to.
func (s *Server) resolvePublishDestination(ctx context.Context, a *actor, key string, destID int64) error {
	localID := int64(0)
	if s.cfg.Channels != nil {
		row, ok, err := s.cfg.Channels.Get(ctx, key, destID)
		if err != nil {
			return &publishError{http.StatusInternalServerError, CodeStoreError, err.Error()}
		}
		if ok {
			localID = row.ID
		}
	}
	if !visible(a.auth, channelstore.KindChannel, channelstore.ActionView, localID) {
		return &publishError{http.StatusForbidden, CodePublishDestDenied,
			"The destination channel may not be named by this account."}
	}
	worker, ok := s.publishWorker(key)
	if !ok {
		return &publishError{http.StatusServiceUnavailable, CodeWorkerUnavail,
			"The Telegram client this channel belongs to is unavailable."}
	}
	// Revalidate with Telegram's own answer: reading a channel is not proof of
	// being able to post to it, and a client-supplied boolean is worth nothing.
	channel, err := worker.Channel(ctx, rpc.ChannelRef{ChannelID: destID})
	if err != nil {
		return &publishError{http.StatusNotFound, CodePublishDestMissing,
			"The destination channel is not available to this connection."}
	}
	if !channel.Channel.Permissions.New {
		return &publishError{http.StatusForbidden, CodePublishDenied,
			"This account may not post to the destination channel."}
	}
	return nil
}

// publishWorker resolves one client's worker for a publish, answering false
// when the key is not configured. A worker is never substituted for another
// one: an unknown key is an error, not a fallback.
func (s *Server) publishWorker(key string) (Worker, bool) {
	if s.cfg.Workers == nil {
		return nil, false
	}
	return s.cfg.Workers.Worker(key)
}

// buildSend assembles the outgoing request from the source message and the
// caller's edits.
func (s *Server) buildSend(ctx context.Context, source sourceMessage, key string, worker Worker,
	req publishRequest) (rpc.SendRequest, error) {

	send := rpc.SendRequest{
		ChannelID: mustChannelID(req.Destination.ChannelID),
		Text:      req.Content.Text,
		Entities:  req.Content.Entities,
		Silent:    req.Options.Silent,
		NoWebpage: req.Options.DisableWebPagePreview,
		RandomID:  randomIDFor(req),
	}
	if req.Media.Mode != mediaPreserveSource {
		return send, nil
	}
	if source.message.Media == nil || !mediaHasBytes(source.message.Media.Type) {
		// Nothing to preserve: a poll, a location or a message with no
		// attachment is sent as the text it is.
		return send, nil
	}
	file, err := worker.Media(ctx, source.row.ChannelID, source.message.ID, 0)
	if err != nil {
		return rpc.SendRequest{}, &publishError{http.StatusInternalServerError, CodePublishPartial,
			"The source attachment could not be read; publish without media or retry."}
	}
	kind := "document"
	if source.message.Media.Type == "photo" {
		kind = "photo"
	}
	send.Media = &rpc.SendMedia{
		Kind:     kind,
		Name:     sendFileName(source.message.Media),
		MimeType: file.MimeType,
		Data:     file.Data,
	}
	return send, nil
}

func sendFileName(m *rpc.Media) string {
	if m == nil {
		return "attachment"
	}
	if m.Label != "" {
		return sanitiseFileName(m.Label)
	}
	return "attachment"
}

// sanitiseFileName keeps a file name to something a document can carry.
func sanitiseFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "attachment"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// randomIDFor gives the send the client message id Telegram uses to recognise
// a resend. A request that carries an idempotency key gets a stable id, so a
// retry cannot post twice even if the operation record is lost.
func randomIDFor(req publishRequest) int64 {
	key := publishRequestHash(req)
	sum := sha256.Sum256([]byte(key))
	id := int64(binary.LittleEndian.Uint64(sum[:8]))
	if id == 0 {
		return 1
	}
	return id
}

// publishRequestHash is the fingerprint a stored operation is checked against.
func publishRequestHash(req publishRequest) string {
	raw, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:16])
}

func mustChannelID(raw string) int64 {
	id, _ := strconv.ParseInt(raw, 10, 64)
	return id
}

// beginPublish records the attempt, or reports the state of an earlier one.
//
// A nil operation with a nil error means "no idempotency key, run it now".
func (s *Server) beginPublish(ctx context.Context, a *actor, key, clientKey string, req publishRequest) (*channelstore.PublishOperation, error) {
	if key == "" {
		return nil, nil
	}
	if s.cfg.Publishes == nil {
		return nil, &publishError{http.StatusServiceUnavailable, CodeStoreError,
			"The publishing record store is unavailable."}
	}
	hash := publishRequestHash(req)
	destType := "telegram"
	if strings.HasPrefix(clientKey, "wp:") {
		destType = "wordpress"
	}
	op, created, err := s.cfg.Publishes.BeginPublishOperation(ctx, channelstore.PublishOperation{
		UserID:          a.user.ID,
		IdempotencyKey:  key,
		RequestHash:     hash,
		DestinationType: destType,
		DestinationID:   clientKey + ":" + req.Destination.ChannelID,
	})
	if errors.Is(err, channelstore.ErrIdempotencyConflict) {
		return nil, &publishError{http.StatusConflict, CodePublishConflict,
			"This idempotency key was already used for a different request."}
	}
	if err != nil {
		return nil, &publishError{http.StatusInternalServerError, CodeStoreError, err.Error()}
	}
	if created {
		return &op, nil
	}
	switch op.Status {
	case channelstore.PublishSucceeded:
		return &op, nil
	case channelstore.PublishUnknown:
		return nil, &publishError{http.StatusConflict, CodePublishUnknown,
			"The earlier attempt with this key has no confirmed outcome and is not retried."}
	default:
		return nil, &publishError{http.StatusConflict, CodePublishConflict,
			"The earlier attempt with this key is still in flight."}
	}
}

// finishPublish records what the attempt achieved. A missing operation is not
// an error: the request had no idempotency key.
func (s *Server) finishPublish(ctx context.Context, op *channelstore.PublishOperation,
	status channelstore.PublishStatus, result, errorCode string) {

	if op == nil || s.cfg.Publishes == nil {
		return
	}
	_ = s.cfg.Publishes.FinishPublishOperation(ctx, op.ID, status, result, errorCode)
}

// statusOf picks the HTTP status for a worker failure.
func publishStatusOf(err error, code string) int {
	switch code {
	case CodePublishDenied, CodePublishForbidden:
		return http.StatusForbidden
	case CodePublishUnsupported:
		return http.StatusBadRequest
	case CodePublishSourceMissing, CodePublishDestMissing:
		return http.StatusNotFound
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodePublishUnknown:
		return http.StatusConflict
	}
	var coded *rpc.Error
	if errors.As(err, &coded) {
		return statusForRPCCode(coded.Code)
	}
	return http.StatusInternalServerError
}

func statusForRPCCode(code string) int {
	switch code {
	case rpc.CodeInvalidInput, rpc.CodeMessageTooLong, rpc.CodeSendUnsupported:
		return http.StatusBadRequest
	case rpc.CodeSendForbidden:
		return http.StatusForbidden
	case rpc.CodeMediaTooLarge:
		return http.StatusRequestEntityTooLarge
	case rpc.CodeChannelNotFound, rpc.CodeNotFound:
		return http.StatusNotFound
	case rpc.CodeRateLimited:
		return http.StatusTooManyRequests
	case rpc.CodeNeedsAuth, rpc.CodeMembershipLimit:
		return http.StatusConflict
	case rpc.CodeNotConnected, CodeWorkerUnavail:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// publishMessage is the caller-facing sentence for a worker failure.
func publishMessage(err error) string {
	switch {
	case errors.Is(err, rpc.ErrSendForbidden):
		return "The destination refused the message: this account may not post there."
	case errors.Is(err, rpc.ErrSendUnsupported):
		return "This content cannot be sent to that destination."
	case errors.Is(err, rpc.ErrMessageTooLong):
		return "The message text is too long to send."
	case errors.Is(err, rpc.ErrMediaTooLarge):
		return "The attachment is too large to send."
	}
	var coded *rpc.Error
	if errors.As(err, &coded) && coded.Message != "" {
		return coded.Message
	}
	return "The message could not be published."
}

// wpPostRequest is a prepared WordPress post a client submits.
type wpPostRequest struct {
	Source struct {
		LocalChannelID int64 `json:"local_channel_id"`
		MessageID      int   `json:"message_id"`
	} `json:"source"`
	Post struct {
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Status  string   `json:"status"`
		Tags    []string `json:"tags"`
	} `json:"post"`
	Media struct {
		Mode string `json:"mode"`
	} `json:"media"`
	Options struct {
		CommentStatus string `json:"comment_status"`
	} `json:"options"`
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
	var req wpPostRequest
	if err := decodeJSONBodyLimited(w, r, &req, maxPublishBody); err != nil {
		s.fail(w, statusOfBodyError(err), CodeInvalidInput, err.Error(), requestID)
		return
	}
	if strings.TrimSpace(req.Post.Title) == "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			"post.title must not be empty.", requestID)
		return
	}
	if req.Media.Mode != mediaPreserveSource && req.Media.Mode != mediaOmit {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput,
			fmt.Sprintf("media.mode must be %q or %q.", mediaPreserveSource, mediaOmit), requestID)
		return
	}

	site, password, err := s.credentialFor(ctx, a, siteID)
	if err != nil {
		s.publishFailure(w, err, requestID)
		return
	}
	source, err := s.resolvePublishSource(ctx, a, publishRequestForSource(req.Source.LocalChannelID, req.Source.MessageID))
	if err != nil {
		s.publishFailure(w, err, requestID)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	operation, err := s.beginPublish(ctx, a, idempotencyKey, fmt.Sprintf("wp:%d", siteID), publishRequestForSource(req.Source.LocalChannelID, req.Source.MessageID))
	if err != nil {
		s.publishFailure(w, err, requestID)
		return
	}
	if operation != nil && operation.Status == channelstore.PublishSucceeded {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Idempotent-Replay", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(operation.Result))
		return
	}

	result, err := s.runWordPressPublish(ctx, a, source, site, password, req)
	if err != nil {
		s.finishPublish(ctx, operation, channelstore.PublishFailed, "", CodePublishFailed)
		s.publishFailure(w, err, requestID)
		return
	}
	answer := envelope{Data: result, Meta: metaFor(requestID, s.cfg.Now())}
	payload, err := json.Marshal(answer)
	if err == nil {
		s.finishPublish(ctx, operation, channelstore.PublishSucceeded, string(payload), "")
	}
	writeJSON(w, http.StatusOK, answer)
}

// publishRequestForSource builds a publishRequest-like source reference for the
// source resolver, which is shared with the Telegram route.
func publishRequestForSource(localChannelID int64, messageID int) publishRequest {
	var req publishRequest
	req.Source.LocalChannelID = localChannelID
	req.Source.MessageID = messageID
	req.Media.Mode = mediaPreserveSource
	return req
}

// credentialFor resolves one registered site and its decrypted password.
//
// The password is opened here and never returned to a caller; a route that
// needs it presents it to the site and nothing else.
func (s *Server) credentialFor(ctx context.Context, a *actor, siteID int64) (channelstore.WordPressSite, string, error) {
	if s.cfg.WordPress == nil || s.cfg.Secrets == nil {
		return channelstore.WordPressSite{}, "", &publishError{http.StatusServiceUnavailable, CodePublishDestMissing,
			"WordPress site storage is not configured."}
	}
	if !visible(a.auth, channelstore.KindWordPress, channelstore.ActionView, siteID) {
		return channelstore.WordPressSite{}, "", &publishError{http.StatusForbidden, CodePublishDestDenied,
			"The WordPress site may not be used by this account."}
	}
	site, ok, err := s.cfg.WordPress.GetWordPressSite(ctx, siteID)
	if err != nil {
		return channelstore.WordPressSite{}, "", &publishError{http.StatusInternalServerError, CodeStoreError, err.Error()}
	}
	if !ok {
		return channelstore.WordPressSite{}, "", &publishError{http.StatusNotFound, CodePublishDestMissing,
			"No registered WordPress site has that id."}
	}
	password, err := s.cfg.Secrets.Open(site.Credential)
	if err != nil {
		return channelstore.WordPressSite{}, "", &publishError{http.StatusServiceUnavailable, CodePublishDestMissing,
			"The stored credential could not be opened."}
	}
	return site, password, nil
}

// runWordPressPublish turns a prepared post into a WordPress post, resolving
// its tags, uploading its attachment and reporting what the site confirmed.
func (s *Server) runWordPressPublish(ctx context.Context, a *actor, source sourceMessage,
	site channelstore.WordPressSite, password string, req wpPostRequest) (map[string]any, error) {

	client := s.cfg.WordPressClient
	tagIDs := make([]int, 0, len(req.Post.Tags))
	for _, name := range req.Post.Tags {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if existing, found, err := client.FindTag(ctx, site.URL, site.Username, password, name); err != nil {
			return nil, &publishError{http.StatusInternalServerError, CodePublishFailed,
				fmt.Sprintf("The WordPress tag %q could not be looked up.", name)}
		} else if found {
			tagIDs = append(tagIDs, existing.ID)
			continue
		}
		created, err := client.CreateTag(ctx, site.URL, site.Username, password, name)
		if err != nil {
			// A tag that cannot be created is not quietly dropped: the caller
			// is told, and no post is made pretending it was assigned.
			return nil, &publishError{http.StatusForbidden, CodePublishFailed,
				fmt.Sprintf("The WordPress tag %q could not be created; the post was not published.", name)}
		}
		tagIDs = append(tagIDs, created.ID)
	}

	featured := 0
	if req.Media.Mode == mediaPreserveSource && source.message.Media != nil && mediaHasBytes(source.message.Media.Type) {
		file, err := s.publishWorkerFor(source.row.ClientKey).Media(ctx, source.row.ChannelID, source.message.ID, 0)
		if err != nil {
			return nil, &publishError{http.StatusInternalServerError, CodePublishPartial,
				"The source attachment could not be read; publish without media or retry."}
		}
		uploaded, err := client.UploadMedia(ctx, site.URL, site.Username, password,
			sendFileName(source.message.Media), file.MimeType, file.Data)
		if err != nil {
			// An upload that failed is not reported as published media.
			return nil, &publishError{http.StatusBadGateway, CodePublishFailed,
				"The attachment could not be uploaded to the WordPress site; the post was not published."}
		}
		if uploaded.ID > 0 {
			featured = uploaded.ID
		}
	}

	post, err := client.CreatePost(ctx, site.URL, site.Username, password, wordpress.PostInput{
		Title:         req.Post.Title,
		Content:       req.Post.Content,
		Status:        req.Post.Status,
		TagIDs:        tagIDs,
		FeaturedMedia: featured,
		CommentStatus: req.Options.CommentStatus,
	})
	if err != nil {
		return nil, mapWordPressPublishError(err)
	}
	result := map[string]any{
		"post": map[string]any{
			"id":     post.ID,
			"title":  post.Title,
			"link":   post.Link,
			"status": post.Status,
			"tags":   post.TagIDs,
		},
		"site": map[string]any{"id": site.ID, "url": site.URL},
	}
	if featured > 0 {
		result["media"] = map[string]any{"attachment_id": featured}
	}
	return result, nil
}

// publishWorkerFor resolves the worker of one client key for a media read.
func (s *Server) publishWorkerFor(key string) Worker {
	worker, _ := s.publishWorker(key)
	return worker
}

// mapWordPressPublishError turns a client failure into the route's answer.
func mapWordPressPublishError(err error) *publishError {
	switch {
	case errors.Is(err, wordpress.ErrUnauthorized):
		return &publishError{http.StatusUnauthorized, CodePublishWordPressAuth,
			"The WordPress credentials were refused."}
	case errors.Is(err, wordpress.ErrForbidden):
		return &publishError{http.StatusForbidden, CodePublishWordPressForbidden,
			"The WordPress user may not publish this content."}
	case errors.Is(err, wordpress.ErrTimeout):
		return &publishError{http.StatusGatewayTimeout, CodePublishWordPressTimeout,
			"The WordPress site did not answer in time. The outcome is unknown and is not retried."}
	case errors.Is(err, wordpress.ErrUnreachable), errors.Is(err, wordpress.ErrBlockedAddress):
		return &publishError{http.StatusBadGateway, CodePublishWordPressUnreachable,
			"The WordPress site could not be reached."}
	case errors.Is(err, wordpress.ErrIncompatible):
		return &publishError{http.StatusBadGateway, CodePublishWordPressIncompatible,
			"The WordPress site did not answer as a REST API."}
	}
	return &publishError{http.StatusBadGateway, CodePublishFailed,
		"The WordPress site refused the post."}
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
