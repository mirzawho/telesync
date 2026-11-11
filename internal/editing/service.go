// Package editing accepts durable replacements of existing Telegram messages.
package editing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/sending"
	"github.com/mirza/telesync/internal/staging"
)

const (
	CodeEditQueueUnavailable = "edit_queue_unavailable"
	CodeEditMediaMissing     = "edit_attachment_missing"
	CodeEditUnsupported      = rpc.CodeMessageEditUnsupported
	StageEditingMessage      = "editing_message"
	StageUploadingMedia      = "uploading_media"
)

type AcceptInput struct {
	ClientKey            string
	DestinationChannelID int64
	TargetMessageID      int64
	IdempotencyKey       string
	Request              publishing.DirectSendRequest
	Attachment           *sending.Attachment
}

type Accepted struct {
	Operation channelstore.MessageEditOperation
	Created   bool
}

type Sender interface {
	ActorFor(context.Context, int64) (publishing.Actor, error)
}
type Editor interface {
	EditMessage(context.Context, string, rpc.EditRequest) (rpc.EditResult, error)
	Message(context.Context, string, int64, int) (rpc.MessagePage, error)
}
type Channels interface {
	Get(context.Context, string, int64) (channelstore.Channel, bool, error)
}
type Operations interface {
	BeginMessageEditOperation(context.Context, channelstore.MessageEditOperation) (channelstore.MessageEditOperation, bool, error)
	MessageEditOperation(context.Context, string) (channelstore.MessageEditOperation, bool, error)
	MessageEditOperationForTarget(context.Context, int64, string, string, int64, string) (channelstore.MessageEditOperation, bool, error)
	StartMessageEditOperation(context.Context, int64, int, string) error
	RetryMessageEditOperation(context.Context, int64, int, time.Time, string, string, string) error
	FinishMessageEditOperation(context.Context, int64, channelstore.MessageEditStatus, int64, string, string, string) error
	MessageEditOperationsHoldingMedia(context.Context) ([]channelstore.MessageEditOperation, error)
	ExpiredMessageEditOperations(context.Context, time.Time) ([]channelstore.MessageEditOperation, error)
	DeleteMessageEditOperation(context.Context, int64) error
}
type Staging interface {
	Limit() int64
	Write(context.Context, io.Reader, staging.Meta) (staging.File, error)
	Open(string) (io.ReadCloser, error)
	ReadAll(string) ([]byte, error)
	Remove(string) error
	Reconcile(func(string) bool) (int, error)
	SweepPartials(time.Duration) (int, error)
}
type Jobs interface {
	EnqueueMessageEdit(context.Context, string, int64) error
}

type Service struct {
	Sender           Sender
	Editor           Editor
	Channels         Channels
	Operations       Operations
	Staging          Staging
	Jobs             Jobs
	HistoryRetention time.Duration
	Logf             func(string, ...any)
}

func (s *Service) logf(format string, args ...any) {
	if s != nil && s.Logf != nil {
		s.Logf(format, args...)
	}
}
func (s *Service) StagingLimit() int64 {
	if s == nil || s.Staging == nil {
		return 0
	}
	return s.Staging.Limit()
}

// StageAttachment reuses direct-send streaming, size limits and content sniffing.
func (s *Service) StageAttachment(ctx context.Context, a *sending.Attachment) (*sending.Staged, error) {
	if s == nil || s.Staging == nil {
		return nil, errors.New("editing: staging is unavailable")
	}
	return (&sending.Service{Staging: s.Staging}).StageAttachment(ctx, a)
}

func (s *Service) DiscardStaged(a *sending.Staged) {
	if s == nil || s.Staging == nil || a == nil || a.File.Name == "" {
		return
	}
	if err := s.Staging.Remove(a.File.Name); err != nil {
		s.logf("editing: discard staged attachment: %v", err)
	}
}

func (s *Service) Accept(ctx context.Context, actor publishing.Actor, in AcceptInput) (Accepted, error) {
	if s == nil || s.Operations == nil || s.Staging == nil || s.Jobs == nil {
		return Accepted{}, fmt.Errorf("editing: service is unavailable")
	}
	if strings.TrimSpace(in.ClientKey) == "" || in.DestinationChannelID <= 0 || in.TargetMessageID <= 0 {
		return Accepted{}, &publishing.Error{Status: 400, Code: publishing.CodeInvalidInput, Message: "A client, destination and target message are required."}
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if len([]rune(key)) > 200 {
		return Accepted{}, &publishing.Error{Status: 400, Code: publishing.CodeInvalidInput, Message: "Idempotency-Key is too long."}
	}
	hasFile := in.Attachment != nil && (in.Attachment.Reader != nil || in.Attachment.Staged != nil)
	if err := publishing.ValidateDirect(in.Request, hasFile); err != nil {
		return Accepted{}, &publishing.Error{Status: 400, Code: publishing.CodeInvalidInput, Message: err.Error()}
	}
	var staged *sending.Staged
	if hasFile {
		var err error
		staged, err = s.StageAttachment(ctx, in.Attachment)
		if err != nil {
			return Accepted{}, err
		}
		if !editMediaCompatible(in.Request.Media.Kind, staged.ContentType) {
			s.DiscardStaged(staged)
			return Accepted{}, &publishing.Error{Status: 400, Code: "invalid_media_kind", Message: "The attachment type does not match the requested media kind."}
		}
	}
	keep := false
	defer func() {
		if !keep {
			s.DiscardStaged(staged)
		}
	}()
	name, size, digest, filename, mimeType := "", int64(0), "", "", ""
	if staged != nil {
		name, size, digest, filename, mimeType = staged.File.Name, staged.File.Size, staged.File.SHA256, staged.File.Original, staged.File.MIMEType
	}
	fingerprintInput := struct {
		Client           string
		Channel, Message int64
		Request          publishing.DirectSendRequest
		Digest           string
		Size             int64
	}{in.ClientKey, in.DestinationChannelID, in.TargetMessageID, in.Request, digest, size}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return Accepted{}, err
	}
	sum := sha256.Sum256(encoded)
	payload, err := json.Marshal(in.Request)
	if err != nil {
		return Accepted{}, err
	}
	op, created, err := s.Operations.BeginMessageEditOperation(ctx, channelstore.MessageEditOperation{
		UserID: actor.UserID, ClientKey: in.ClientKey, DestinationChannelID: strconv.FormatInt(in.DestinationChannelID, 10), TargetMessageID: in.TargetMessageID,
		RequestPayload: string(payload), MediaKind: mediaKind(in.Request), MediaStagingName: name, MediaFilename: filename, MediaMimeType: mimeType, MediaSize: size, MediaSHA256: digest,
		IdempotencyKey: key, RequestFingerprint: hex.EncodeToString(sum[:]), Status: channelstore.MessageEditQueued,
	})
	if err != nil {
		if errors.Is(err, channelstore.ErrMessageEditIdempotencyConflict) {
			return Accepted{}, &publishing.Error{Status: 409, Code: publishing.CodePublishConflict, Message: "This idempotency key was already used for a different edit."}
		}
		return Accepted{}, &publishing.Error{Status: 503, Code: CodeEditQueueUnavailable, Message: "The edit could not be recorded and has not been accepted."}
	}
	if !created {
		return Accepted{Operation: op}, nil
	}
	keep = true
	if err := s.Jobs.EnqueueMessageEdit(ctx, op.PublicID, targetQueueKey(in.ClientKey, in.DestinationChannelID, in.TargetMessageID)); err != nil {
		_ = s.Operations.DeleteMessageEditOperation(ctx, op.ID)
		keep = false
		return Accepted{}, &publishing.Error{Status: 503, Code: CodeEditQueueUnavailable, Message: "The edit could not be scheduled and has not been accepted."}
	}
	return Accepted{Operation: op, Created: true}, nil
}

func (s *Service) Status(ctx context.Context, actor publishing.Actor, clientKey, channelID string, messageID int64, operationID string) (channelstore.MessageEditOperation, error) {
	op, found, err := s.Operations.MessageEditOperationForTarget(ctx, actor.UserID, clientKey, channelID, messageID, operationID)
	if err != nil {
		return channelstore.MessageEditOperation{}, err
	}
	if !found {
		return channelstore.MessageEditOperation{}, &publishing.Error{Status: 404, Code: rpc.CodeNotFound, Message: "The edit operation was not found."}
	}
	return op, nil
}

func mediaKind(req publishing.DirectSendRequest) string {
	if req.Media == nil {
		return ""
	}
	return req.Media.Kind
}
func targetQueueKey(client string, channel, message int64) int64 {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", client, channel, message)))
	n := int64(0)
	for _, b := range sum[:8] {
		n = n<<8 | int64(b)
	}
	n &= 0x7fffffffffffffff
	if n == 0 {
		return 1
	}
	return n
}
func editMediaCompatible(kind, contentType string) bool {
	t := strings.ToLower(strings.Split(contentType, ";")[0])
	switch kind {
	case publishing.DirectMediaPhoto:
		return strings.HasPrefix(t, "image/")
	case publishing.DirectMediaVideo:
		return strings.HasPrefix(t, "video/")
	case publishing.DirectMediaAudio, publishing.DirectMediaVoice:
		return strings.HasPrefix(t, "audio/") || t == "application/ogg"
	case publishing.DirectMediaDocument:
		return t != ""
	}
	return false
}

// HeldMediaNames returns the attachments whose durable operations still need them.
func (s *Service) HeldMediaNames(ctx context.Context) ([]string, error) {
	ops, err := s.Operations.MessageEditOperationsHoldingMedia(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.MediaStagingName != "" {
			names = append(names, op.MediaStagingName)
		}
	}
	return names, nil
}

// Cleanup expires finished history and reports missing active attachments. Shared staging reconciliation is performed by runtime over the union of send and edit holdings.
func (s *Service) Cleanup(ctx context.Context) error {
	if s == nil || s.Operations == nil {
		return nil
	}
	active, err := s.Operations.MessageEditOperationsHoldingMedia(ctx)
	if err != nil {
		return err
	}
	for _, op := range active {
		if s.Staging != nil {
			rc, openErr := s.Staging.Open(op.MediaStagingName)
			if openErr == nil {
				_ = rc.Close()
				continue
			}
		}
		message := "The staged attachment is missing."
		if err := s.Operations.FinishMessageEditOperation(ctx, op.ID, channelstore.MessageEditFailed, 0, "", CodeEditMediaMissing, message); err != nil {
			return err
		}
	}
	if s.HistoryRetention <= 0 {
		return nil
	}
	expired, err := s.Operations.ExpiredMessageEditOperations(ctx, time.Now().UTC().Add(-s.HistoryRetention))
	if err != nil {
		return err
	}
	for _, op := range expired {
		if err := s.Operations.DeleteMessageEditOperation(ctx, op.ID); err != nil {
			return err
		}
	}
	return nil
}

// Execute revalidates app permissions, then performs a native edit and checks
// the resulting message. It never falls back to creating a new message.
func (s *Service) Execute(ctx context.Context, job automation.Job) error {
	op, found, err := s.Operations.MessageEditOperation(ctx, job.EventID)
	if err != nil {
		return &automation.JobError{Code: publishing.CodeStoreError, Message: err.Error(), Retryable: true}
	}
	if !found || op.Status == channelstore.MessageEditEdited || op.Status == channelstore.MessageEditFailed || op.Status == channelstore.MessageEditUnknown {
		return nil
	}
	stage := StageEditingMessage
	if op.MediaKind != "" {
		stage = StageUploadingMedia
	}
	if err := s.Operations.StartMessageEditOperation(ctx, op.ID, job.Attempt, stage); err != nil {
		return &automation.JobError{Code: publishing.CodeStoreError, Message: err.Error(), Retryable: true}
	}
	fail := func(code, msg string) error {
		_ = s.Operations.FinishMessageEditOperation(ctx, op.ID, channelstore.MessageEditFailed, 0, "", code, msg)
		return &automation.JobError{Code: code, Message: msg}
	}
	if s.Sender == nil || s.Channels == nil || s.Editor == nil {
		return fail(CodeEditUnsupported, "The edit worker is unavailable.")
	}
	actor, err := s.Sender.ActorFor(ctx, op.UserID)
	if err != nil {
		return fail("permission_denied", "The account no longer has permission to edit this channel.")
	}
	channelID, _ := strconv.ParseInt(op.DestinationChannelID, 10, 64)
	channel, ok, err := s.Channels.Get(ctx, op.ClientKey, channelID)
	if err != nil {
		return s.retry(ctx, op, job.Attempt, stage, publishing.CodeStoreError, "The managed channel could not be checked.", 10*time.Second)
	}
	if !ok || actor.Auth == nil || !actor.Auth.Allowed(channelstore.KindChannel, channelstore.ActionUpdate, channel.ID) {
		return fail("permission_denied", "The account no longer has permission to edit this channel.")
	}
	request := publishing.DirectSendRequest{}
	if err := json.Unmarshal([]byte(op.RequestPayload), &request); err != nil {
		return fail(publishing.CodeStoreError, "The stored edit request could not be read.")
	}
	readback, err := s.Editor.Message(ctx, op.ClientKey, channelID, int(op.TargetMessageID))
	if err != nil {
		delay := retryDelay(err)
		if delay <= 0 {
			delay = 10 * time.Second
		}
		return s.retry(ctx, op, job.Attempt, stage, rpc.CodeNotConnected, "The target message could not be checked before editing.", delay)
	}
	var before *rpc.Message
	for i := range readback.Messages {
		if readback.Messages[i].ID == int(op.TargetMessageID) {
			before = &readback.Messages[i]
			break
		}
	}
	if before == nil {
		return fail(rpc.CodeNotFound, "The target message no longer exists.")
	}
	if request.Media == nil && before.Media != nil && before.Media.Type != "webpage" {
		return fail(rpc.CodeEditMediaRemovalUnsupported, "Telegram media removal could not be verified safely.")
	}
	var media *rpc.SendMedia
	if request.Media != nil {
		data, err := s.Staging.ReadAll(op.MediaStagingName)
		if err != nil {
			return fail(CodeEditMediaMissing, "The staged attachment is missing.")
		}
		media = &rpc.SendMedia{Kind: request.Media.Kind, Name: op.MediaFilename, MimeType: op.MediaMimeType, Data: data}
		if request.Media.DurationSeconds != nil {
			media.Duration = *request.Media.DurationSeconds
		}
	}
	req := rpc.EditRequest{ChannelID: channelID, MessageID: int(op.TargetMessageID), Text: request.Text, Entities: request.Entities, Media: media, NoWebpage: request.Options.DisableWebPagePreview}
	result, editErr := s.Editor.EditMessage(ctx, op.ClientKey, req)
	// Read the same id after success and after an ambiguous transport error. It
	// is the only safe way to resolve a lost response without issuing another edit.
	afterPage, readErr := s.Editor.Message(ctx, op.ClientKey, channelID, int(op.TargetMessageID))
	var after *rpc.Message
	if readErr == nil {
		for i := range afterPage.Messages {
			if afterPage.Messages[i].ID == int(op.TargetMessageID) {
				after = &afterPage.Messages[i]
				break
			}
		}
	}
	if after == nil {
		code := rpc.CodeMessageEditOutcomeUnknown
		message := "Telegram's edit result could not be verified."
		_ = s.Operations.FinishMessageEditOperation(ctx, op.ID, channelstore.MessageEditUnknown, 0, "", code, message)
		return &automation.JobError{Code: code, Message: message}
	}
	if editErr != nil && retryDelay(editErr) > 0 {
		delay := retryDelay(editErr)
		code, message := errorCode(editErr), "Telegram is temporarily unavailable for this edit."
		return s.retry(ctx, op, job.Attempt, stage, code, message, delay)
	}
	// Telegram can return MESSAGE_NOT_MODIFIED after comparing only the media
	// kind and caption. A readback of those fields cannot prove the uploaded
	// bytes replaced the previous media, so preserve the adapter's ambiguity.
	if editErr != nil && request.Media != nil && errorCode(editErr) == rpc.CodeMessageEditOutcomeUnknown {
		code := rpc.CodeMessageEditOutcomeUnknown
		message := "Telegram could not confirm whether the requested attachment replaced the existing media."
		return s.unknown(ctx, op, code, message)
	}
	if after.Text != request.Text || !sameEntities(after.Entities, request.Entities) || !sameMediaKind(after.Media, request.Media, request.Options.DisableWebPagePreview) {
		if editErr != nil {
			if errorCode(editErr) == rpc.CodeMessageEditOutcomeUnknown {
				code := rpc.CodeMessageEditOutcomeUnknown
				message := "Telegram's edit result could not be confirmed."
				return s.unknown(ctx, op, code, message)
			}
			return fail(errorCode(editErr), "Telegram did not confirm the requested message replacement.")
		}
		return fail(rpc.CodeMessageEditUnsupported, "Telegram did not apply the complete requested message replacement.")
	}
	remoteID := int64(after.ID)
	resultID := int64(0)
	if result.Message != nil {
		resultID = int64(result.Message.ID)
	}
	if resultID != 0 && resultID != remoteID {
		return s.unknown(ctx, op, rpc.CodeMessageEditOutcomeUnknown, "Telegram returned a different message than the target.")
	}
	mediaResult := ""
	if after.Media != nil {
		mediaResult = after.Media.Type
	}
	if err := s.Operations.FinishMessageEditOperation(ctx, op.ID, channelstore.MessageEditEdited, remoteID, mediaResult, "", ""); err != nil {
		return &automation.JobError{Code: publishing.CodeStoreError, Message: err.Error(), Retryable: true}
	}
	return nil
}

func (s *Service) retry(ctx context.Context, op channelstore.MessageEditOperation, attempt int, stage, code, message string, delay time.Duration) error {
	if delay <= 0 {
		delay = 10 * time.Second
	}
	if err := s.Operations.RetryMessageEditOperation(ctx, op.ID, attempt, time.Now().UTC().Add(delay), stage, code, message); err != nil {
		return &automation.JobError{Code: publishing.CodeStoreError, Message: err.Error(), Retryable: true}
	}
	return &automation.JobError{Code: code, Message: message, Retryable: true, RetryAfter: delay}
}

func (s *Service) unknown(ctx context.Context, op channelstore.MessageEditOperation, code, message string) error {
	if err := s.Operations.FinishMessageEditOperation(ctx, op.ID, channelstore.MessageEditUnknown, 0, "", code, message); err != nil {
		return &automation.JobError{Code: publishing.CodeStoreError, Message: err.Error(), Retryable: true}
	}
	return &automation.JobError{Code: code, Message: message}
}

func sameEntities(a, b []rpc.Entity) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func sameMediaKind(actual *rpc.Media, want *publishing.DirectSendMedia, noWebpage bool) bool {
	if want == nil {
		return actual == nil || (!noWebpage && actual.Type == "webpage")
	}
	return actual != nil && actual.Type == want.Kind
}
func errorCode(err error) string {
	var re *rpc.Error
	if errors.As(err, &re) && re.Code != "" {
		return re.Code
	}
	return rpc.CodeMessageEditOutcomeUnknown
}

func retryDelay(err error) time.Duration {
	var coded *rpc.Error
	if errors.As(err, &coded) {
		if coded.Code == rpc.CodeRateLimited {
			if coded.RetryAfter > 0 {
				return time.Duration(coded.RetryAfter) * time.Second
			}
			return 10 * time.Second
		}
		if coded.Code == rpc.CodeNotConnected {
			return 10 * time.Second
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 10 * time.Second
	}
	return 0
}
