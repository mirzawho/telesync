// Package sending performs a message a caller composed and wants in a Telegram
// channel: text they wrote, an attachment they uploaded, and no source message
// behind any of it.
//
// It is asynchronous by construction. Accepting a send and performing one are
// different moments in different requests — often different processes, and
// always after a restart is possible — so the whole of the work is described by
// a durable record and a staged file, and nothing is held in the memory of the
// request that asked for it.
//
// What it does not own is the outgoing path. The destination, its permissions,
// how a message is built and what a failure means all belong to the publishing
// pipeline and are shared with a source-based publish, so the two cannot drift
// into sending different things to the same channel.
package sending

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/staging"
)

// Stages a send reports while it is being performed. They are for a caller
// wondering whether a send is stuck, and say nothing about this deployment.
const (
	StagePreparing      = "preparing"
	StageUploadingMedia = "uploading_media"
	StageSendingMessage = "sending_message"
)

// Error codes this package produces. They are the API's documented codes,
// restated here because the producer must not import the HTTP gateway that
// renders them.
const (
	// CodeInvalidMediaKind means the request named a media kind that does not
	// exist, or named one the uploaded file cannot keep.
	CodeInvalidMediaKind = "invalid_media_kind"
	// CodeSendQueueUnavailable means the send could not be made durable. It is
	// the one failure that answers with something other than an operation: if
	// the work is not safely recorded there is nothing to report about it.
	CodeSendQueueUnavailable = "send_queue_unavailable"
	// CodeSendAttachmentMissing means the staged attachment is gone. It is
	// permanent: the bytes are not coming back and a retry would only fail
	// again.
	CodeSendAttachmentMissing = "send_attachment_missing"
)

// Sender is the shared outgoing path.
//
// Both ways of producing an outgoing message go through it: one that copies a
// message somebody already wrote, and one that sends what this caller wrote.
type Sender interface {
	// ActorFor resolves the account a background send runs under, reading its
	// permissions again rather than trusting anything recorded earlier.
	ActorFor(ctx context.Context, userID int64) (publishing.Actor, error)
	// SendDirect sends one prepared post to one destination.
	SendDirect(ctx context.Context, a publishing.Actor, clientKey string, destID int64, send rpc.SendRequest) (publishing.DirectResult, error)
}

// Operations is the durable record of accepted sends.
type Operations interface {
	BeginSendOperation(ctx context.Context, op channelstore.SendOperation) (channelstore.SendOperation, bool, error)
	SendOperation(ctx context.Context, publicID string) (channelstore.SendOperation, bool, error)
	StartSendOperation(ctx context.Context, id int64, attempt int, stage string) error
	RetrySendOperation(ctx context.Context, id int64, attempt int, next time.Time, stage, code, message string) error
	FinishSendOperation(ctx context.Context, id int64, status channelstore.SendStatus, remoteMessageID int64, stage, code, message string) error
	SendOperationsHoldingMedia(ctx context.Context) ([]channelstore.SendOperation, error)
	ExpiredSendOperations(ctx context.Context, before time.Time) ([]channelstore.SendOperation, error)
	DeleteSendOperation(ctx context.Context, id int64) error
}

// Staging holds an uploaded attachment between acceptance and delivery.
type Staging interface {
	// Limit is the largest attachment it will hold. It is the one number the
	// upload limit is read from, so the answer a caller gets and the limit that
	// is applied cannot disagree.
	Limit() int64
	Write(ctx context.Context, r io.Reader, meta staging.Meta) (staging.File, error)
	Open(name string) (io.ReadCloser, error)
	OpenFor(ctx context.Context, ownerUserID int64, accountID, name string) (io.ReadCloser, error)
	ReadAll(name string) ([]byte, error)
	ReadFor(ctx context.Context, ownerUserID int64, accountID, name string) ([]byte, error)
	Remove(name string) error
	Reconcile(keep func(name string) bool) (int, error)
	ReconcileOwnedKeeping(ctx context.Context, keep []string, owners []staging.OwnerFilter) (int, error)
	SweepPartials(olderThan time.Duration) (int, error)
}

// Jobs schedules background work on the durable queue.
type Jobs interface {
	EnqueueMessageSend(ctx context.Context, operationID string, channelID int64, owner automation.Owner) error
}

// Service accepts sends and performs them later.
//
// Every field is required: a send that cannot be recorded, staged or queued is
// refused rather than accepted on the understanding that it will probably be
// fine.
type Service struct {
	Sender     Sender
	Operations Operations
	Staging    Staging
	Jobs       Jobs
	// HistoryRetention is how long a finished operation is kept. Zero keeps
	// them until the next sweep says otherwise.
	HistoryRetention time.Duration
	// Logf receives one line per failure worth recording. Nil discards.
	Logf func(format string, args ...any)
}

func (s *Service) logf(format string, args ...any) {
	if s != nil && s.Logf != nil {
		s.Logf(format, args...)
	}
}

// StagingLimit is the largest attachment this deployment accepts.
//
// It is read back out so the request body can be bounded by the same number the
// attachment is: an upload that cannot possibly fit is refused before it is
// written anywhere, rather than after it has been streamed part of the way.
func (s *Service) StagingLimit() int64 {
	if s == nil || s.Staging == nil {
		return 0
	}
	return s.Staging.Limit()
}

// Accepted is what a caller is told when a send has been made durable.
type Accepted struct {
	Operation channelstore.SendOperation
	// Created is false when an earlier request with the same idempotency key
	// already made this exact send: nothing was staged, queued or sent now.
	Created bool
}

// Accept records one send and schedules it for background delivery.
//
// It is the whole of the HTTP request's work. Nothing here talks to Telegram:
// the attachment is streamed to the staging area, the operation is written, and
// the job is queued. Only when all three have succeeded does the caller get an
// operation to poll — and by then the work survives this process ending.
//
// A repeated request with the same idempotency key is answered with the
// operation that already exists and enqueues nothing. A file it had already
// staged for that answer is discarded, so a retry costs a temporary upload and
// never a second message.
func (s *Service) Accept(ctx context.Context, a publishing.Actor, in AcceptInput) (Accepted, error) {
	// A key is the caller's own token for this request and is stored so a
	// repeat can be recognised. Its length is bounded because it is stored and
	// indexed, and a limit is a cheaper answer to an unbounded header than
	// whatever happens without one.
	if key := strings.TrimSpace(in.IdempotencyKey); len([]rune(key)) > MaxIdempotencyKey {
		return Accepted{}, &publishing.Error{Status: 400, Code: publishing.CodeInvalidInput,
			Message: fmt.Sprintf("Idempotency-Key is %d characters; the limit is %d.",
				len([]rune(key)), MaxIdempotencyKey)}
	}

	hasAttachment := in.Attachment != nil && (in.Attachment.Reader != nil || in.Attachment.Staged != nil)

	// Whatever is written down is finished with here. It is either adopted by
	// the operation recorded below or removed again on the way out — and that
	// covers a file that arrived before the request describing it, which is
	// already on disk before this function is even called.
	var staged stagedFile
	if in.Attachment != nil && in.Attachment.Staged != nil {
		staged.File = in.Attachment.Staged.File
	}
	keep := false
	defer func() {
		if !keep {
			s.discard(staged.Name)
		}
	}()

	if err := publishing.ValidateDirect(in.Request, hasAttachment); err != nil {
		return Accepted{}, &publishing.Error{Status: 400, Code: publishing.CodeInvalidInput, Message: err.Error()}
	}

	if hasAttachment {
		kept, err := s.stageAttachment(ctx, a, in.Attachment)
		if err != nil {
			return Accepted{}, err
		}
		if !acceptableMedia(in.Request.Media.Kind, kept.ContentType) {
			return Accepted{}, &publishing.Error{Status: 400, Code: CodeInvalidMediaKind,
				Message: fmt.Sprintf("A %s cannot be sent as %q.",
					baseType(kept.ContentType), in.Request.Media.Kind)}
		}
		staged.File = kept.File
	}

	sum, err := fingerprint(in, staged.SHA256, staged.Size)
	if err != nil {
		return Accepted{}, &publishing.Error{Status: 500, Code: publishing.CodeStoreError, Message: err.Error()}
	}
	staged.Fingerprint = sum

	// The id the send is performed under is issued once, here, and is written
	// down before any attempt: it is what makes a resend the same message.
	randomID, err := newRandomID()
	if err != nil {
		return Accepted{}, &publishing.Error{Status: 500, Code: publishing.CodeStoreError, Message: err.Error()}
	}

	operation, created, err := s.Operations.BeginSendOperation(ctx, channelstore.SendOperation{
		UserID:               a.UserID,
		AccountID:            a.AccountID,
		ClientKey:            in.ClientKey,
		DestinationChannelID: formatChannelID(in.DestinationChannelID),
		RequestPayload:       payloadOf(in.Request),
		MediaKind:            mediaKindOf(in.Request),
		MediaStagingName:     staged.Name,
		MediaFilename:        staged.Original,
		MediaMimeType:        staged.MIMEType,
		MediaSize:            staged.Size,
		MediaSHA256:          staged.SHA256,
		IdempotencyKey:       strings.TrimSpace(in.IdempotencyKey),
		RequestFingerprint:   staged.Fingerprint,
		RandomID:             randomID,
	})
	if err != nil {
		if errors.Is(err, channelstore.ErrSendIdempotencyConflict) {
			return Accepted{}, &publishing.Error{Status: 409, Code: publishing.CodePublishConflict,
				Message: "This idempotency key was already used for a different request."}
		}
		return Accepted{}, &publishing.Error{Status: 503, Code: CodeSendQueueUnavailable,
			Message: "The send could not be recorded durably and has not been accepted."}
	}
	if !created {
		// The answer is the operation that already exists. The file staged a
		// moment ago is a second copy of an attachment that is already there,
		// and the deferred cleanup removes it.
		return Accepted{Operation: operation, Created: false}, nil
	}

	if err := s.Jobs.EnqueueMessageSend(ctx, operation.PublicID, in.DestinationChannelID, a.Owner()); err != nil {
		// The request is not accepted: an operation no job will ever run is a
		// message that was promised and will never be sent. Both halves are
		// undone so a retry of the same request starts clean.
		_ = s.Operations.DeleteSendOperation(ctx, operation.ID)
		s.logf("sending: queue send %s: %v", operation.PublicID, err)
		return Accepted{}, &publishing.Error{Status: 503, Code: CodeSendQueueUnavailable,
			Message: "The send could not be scheduled and has not been accepted."}
	}
	keep = true
	return Accepted{Operation: operation, Created: true}, nil
}

// DiscardStaged removes an attachment that was written down for a request that
// will not be accepted after all.
//
// A multipart body can carry its file before the JSON that describes it, so a
// caller can find itself holding a staged file and an unusable request. This is
// how it gives the file back.
func (s *Service) DiscardStaged(staged *Staged) {
	if staged == nil {
		return
	}
	s.discard(staged.File.Name)
}

// stagedFile is an attachment as it was written down, with the identity of the
// request it belongs to.
type stagedFile struct {
	staging.File
	Fingerprint string
}

// StageAttachment writes one uploaded attachment down before the request that
// describes it has been read.
//
// It exists for a multipart body that carries its file before its JSON: a part
// is spent by the time the next one is read, so the file has to be taken while
// it is there and cannot be held in memory waiting for the rest. The answer is
// the same either way — an attachment in the staging area, and what its bytes
// turned out to be.
func (s *Service) StageAttachment(ctx context.Context, owner publishing.Actor, a *Attachment) (*Staged, error) {
	return s.stageAttachment(ctx, owner, a)
}

// stageAttachment is the one place an attachment is written down.
//
// The bytes go straight to the staging area in chunks and are hashed as they
// pass. What they are is decided from their first bytes rather than from the
// content type the client declared, which is a claim and is treated as one.
func (s *Service) stageAttachment(ctx context.Context, owner publishing.Actor, a *Attachment) (*Staged, error) {
	if a == nil || (a.Reader == nil && a.Staged == nil) {
		return nil, &publishing.Error{Status: 400, Code: publishing.CodeInvalidInput,
			Message: "There is no attachment."}
	}
	if a.Staged != nil {
		return a.Staged, nil
	}

	head, body, err := sniffingReader(a.Reader)
	if err != nil {
		return nil, &publishing.Error{Status: 400, Code: publishing.CodeInvalidInput,
			Message: "The attachment could not be read."}
	}
	contentType := effectiveContentType(a.MimeType, head)

	// The upload is bound to the account and user it belongs to. Staged bytes
	// are readable only by the owner named here, so a guessed storage name is
	// not a way to fetch somebody else's attachment.
	file, err := s.Staging.Write(ctx, body, staging.Meta{
		OriginalName: a.Name,
		MIMEType:     contentType,
		OwnerUserID:  owner.UserID,
		AccountID:    owner.AccountID,
	})
	if err != nil {
		if errors.Is(err, staging.ErrTooLarge) {
			return nil, &publishing.Error{Status: 413, Code: publishing.CodePublishMediaTooLarge,
				Message: fmt.Sprintf("The attachment is larger than the %d byte limit.", s.Staging.Limit())}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, &publishing.Error{Status: 400, Code: publishing.CodeInvalidInput,
				Message: "The upload did not finish."}
		}
		return nil, &publishing.Error{Status: 503, Code: CodeSendQueueUnavailable,
			Message: "The attachment could not be staged and the send has not been accepted."}
	}
	return &Staged{File: file, ContentType: contentType}, nil
}

func (s *Service) attachmentThereFor(ctx context.Context, op channelstore.SendOperation) bool {
	if op.MediaStagingName == "" || op.UserID <= 0 || strings.TrimSpace(op.AccountID) == "" {
		return false
	}
	rc, err := s.Staging.OpenFor(ctx, op.UserID, op.AccountID, op.MediaStagingName)
	if err != nil {
		return false
	}
	_ = rc.Close()
	return true
}

// discard removes a staged file that is no longer wanted. A failure here is
// logged and not returned: the caller's answer is about the request, and a file
// the cleanup sweep will collect is not a reason to misreport one.
func (s *Service) discard(name string) {
	if name == "" {
		return
	}
	if err := s.Staging.Remove(name); err != nil {
		s.logf("sending: discard staged attachment %s: %v", name, err)
	}
}

// Execute performs one accepted send.
//
// It is the durable queue's handler and is called with a job this process has
// claimed. Everything it needs is in the record: the request, the staged
// attachment, whose it is, and the client message id every attempt must reuse.
// Nothing is taken from the request that accepted it, which is long gone.
//
// A nil return means the work is done — including the case where the operation
// is already terminal and there was nothing left to do. A *automation.JobError
// says whether another attempt is wanted; anything else is left to the queue's
// own conservative policy.
func (s *Service) Execute(ctx context.Context, job automation.Job) error {
	op, found, err := s.Operations.SendOperation(ctx, job.EventID)
	if err != nil {
		return &automation.JobError{Code: publishing.CodeStoreError, Message: err.Error(), Retryable: true}
	}
	if !found {
		// The operation is gone — retention ran, or it was never written. There
		// is nothing to send and nothing to retry.
		s.logf("sending: job %d names no operation (%s)", job.ID, job.EventID)
		return nil
	}
	if !job.Owner.Bound() || job.Owner.OwnerUserID != op.UserID || job.Owner.AccountID != op.AccountID {
		return s.failPermanently(ctx, op, StageSendingMessage,
			&publishing.Error{Status: 409, Code: publishing.CodeInvalidInput,
				Message: "The queued send does not match its recorded owner."})
	}
	if op.Status == channelstore.SendSent || op.Status == channelstore.SendUnknown {
		// Already answered, and never to be answered twice. A second worker
		// picking this up is a lease that expired, not new work.
		return nil
	}
	stage := StageSendingMessage
	if op.MediaKind != "" {
		stage = StageUploadingMedia
	}
	if err := s.Operations.StartSendOperation(ctx, op.ID, job.Attempt, stage); err != nil {
		if errors.Is(err, channelstore.ErrSendOperationNotFound) {
			return nil
		}
		return &automation.JobError{Code: publishing.CodeStoreError, Message: err.Error(), Retryable: true}
	}

	// A send runs under an account, and the account's permissions are read
	// again now rather than believed from when the request arrived. A grant that
	// has been withdrawn stops this send; so does an account that is gone.
	actor, err := s.Sender.ActorFor(ctx, op.UserID)
	if err != nil {
		return s.failPermanently(ctx, op, stage, err)
	}

	send, err := s.prepare(ctx, op)
	if err != nil {
		return s.failPermanently(ctx, op, stage, err)
	}

	result, sendErr := s.Sender.SendDirect(ctx, actor, op.ClientKey, channelIDOf(op.DestinationChannelID), send)
	if sendErr != nil {
		return s.recordFailure(ctx, op, job, stage, sendErr, result)
	}

	messageID := int64(0)
	if result.Message != nil {
		messageID = int64(result.Message.ID)
	}
	if err := s.Operations.FinishSendOperation(ctx, op.ID, channelstore.SendSent, messageID, StageSendingMessage, "", ""); err != nil {
		return &automation.JobError{Code: publishing.CodeStoreError, Message: err.Error(), Retryable: true}
	}
	s.discard(op.MediaStagingName)
	if result.UnsendableEntities > 0 {
		s.logf("sending: %s sent with %d formatting span(s) the destination could not take",
			op.PublicID, result.UnsendableEntities)
	}
	return nil
}

// prepare turns a recorded operation back into the post it describes.
//
// The attachment is read whole here because the worker's send takes one buffer;
// it is the one place that is allowed to, and it is bounded by the same limit
// the upload was.
func (s *Service) prepare(ctx context.Context, op channelstore.SendOperation) (rpc.SendRequest, error) {
	req, err := decodePayload(op.RequestPayload)
	if err != nil {
		return rpc.SendRequest{}, &publishing.Error{Status: 500, Code: publishing.CodeStoreError,
			Message: "The recorded request could not be read."}
	}
	send := rpc.SendRequest{
		Text:      req.Text,
		Entities:  req.Entities,
		Silent:    req.Options.Silent,
		NoWebpage: req.Options.DisableWebPagePreview,
		// The same client message id on every attempt is what makes a resend
		// after a lost confirmation the same message rather than a second one.
		// It is written before the first attempt and is never regenerated.
		RandomID: op.RandomID,
	}
	if op.MediaKind == "" {
		return send, nil
	}
	if op.MediaStagingName == "" {
		return rpc.SendRequest{}, &publishing.Error{Status: 500, Code: CodeSendAttachmentMissing,
			Message: "The staged attachment is not named on this operation."}
	}
	data, err := s.Staging.ReadFor(ctx, op.UserID, op.AccountID, op.MediaStagingName)
	if err != nil {
		// Permanent. The bytes are gone and no amount of waiting brings them
		// back, so the send stops here and says so rather than burning the
		// attempt budget on the same missing file.
		s.logf("sending: %s has no staged attachment: %v", op.PublicID, err)
		return rpc.SendRequest{}, &publishing.Error{Status: 410, Code: CodeSendAttachmentMissing,
			Message: "The uploaded attachment is no longer available."}
	}
	// A recording goes out unnamed. Telegram's own voice messages carry no
	// documentAttributeFilename, and a name is what turns one into a file a
	// client offers to download instead of play. The client's upload filename is
	// the name of a file on their disk, not a property of the recording, so it
	// is kept as the operation's metadata and never put on the wire.
	name := op.MediaFilename
	if op.MediaKind == publishing.DirectMediaVoice {
		name = ""
	}
	send.Media = &rpc.SendMedia{
		Kind:     op.MediaKind,
		Name:     name,
		MimeType: op.MediaMimeType,
		Data:     data,
		Duration: requestDuration(req),
	}
	return send, nil
}

// recordFailure decides what a failed attempt means and writes it down.
//
// This is where the difference between "certainly not sent" and "we never heard
// back" is acted on. An outcome that is unknown is terminal and is never
// retried: the message may already be in the channel, and trying again is the
// one thing guaranteed to make a duplicate.
func (s *Service) recordFailure(ctx context.Context, op channelstore.SendOperation, job automation.Job,
	stage string, sendErr error, result publishing.DirectResult) error {

	code := publishing.CodeOf(sendErr)
	message := messageOf(sendErr)

	if result.Outcome == channelstore.PublishUnknown {
		_ = s.Operations.FinishSendOperation(ctx, op.ID, channelstore.SendUnknown, 0, stage,
			publishing.CodePublishUnknown, "The destination never confirmed whether the message was posted.")
		// Not retryable, and deliberately not a success either: the queue
		// records the job as finished with this reason, and nobody runs it again.
		return &automation.JobError{Code: publishing.CodePublishUnknown,
			Message: "The destination never confirmed this send; it is not repeated.", Retryable: false}
	}

	if permanentSend(code) {
		return s.failPermanently(ctx, op, stage, sendErr)
	}

	// Only a delay the destination itself named is written down here. Any other
	// number would be a second guess at a scheduling policy the queue already
	// owns and already applies.
	if err := s.Operations.RetrySendOperation(ctx, op.ID, job.Attempt, retryTimeOf(result.RetryAfter), stage, code, message); err != nil {
		s.logf("sending: record retry for %s: %v", op.PublicID, err)
	}
	return &automation.JobError{Code: code, Message: message, Retryable: true, RetryAfter: result.RetryAfter}
}

// failPermanently records a send that will not be attempted again.
func (s *Service) failPermanently(ctx context.Context, op channelstore.SendOperation,
	stage string, sendErr error) error {

	code := publishing.CodeOf(sendErr)
	message := messageOf(sendErr)
	if err := s.Operations.FinishSendOperation(ctx, op.ID, channelstore.SendFailed, 0, stage, code, message); err != nil {
		s.logf("sending: record failure for %s: %v", op.PublicID, err)
	}
	// The attachment is kept: an operator looking at a failure should still be
	// able to see what was sent, and the retention sweep removes it later.
	return &automation.JobError{Code: code, Message: message, Retryable: false}
}

// permanentSend reports whether a failure code means the send will never
// succeed however often it is tried.
//
// Everything not named here is retried, bounded by the queue's attempt budget.
// That is deliberate: an unclassified failure is not proof the remote side
// refused, and guessing "permanent" wrong means a message the caller was told
// about is never sent at all.
func permanentSend(code string) bool {
	switch code {
	case publishing.CodePublishForbidden,
		publishing.CodePublishDenied,
		publishing.CodePublishDestDenied,
		publishing.CodePublishUnsupported,
		publishing.CodePublishMediaTooLarge,
		publishing.CodeInvalidInput,
		publishing.CodePublishConflict,
		CodeSendAttachmentMissing,
		rpc.CodeMessageTooLong,
		rpc.CodeSendUnsupported:
		return true
	}
	return false
}

// Status reads one accepted send.
//
// Reading is reading: nothing about this changes the operation, and polling a
// send never causes one. The caller must own the operation and must be naming
// the destination the route says, so an id that has leaked is still not a way
// to read somebody else's message.
func (s *Service) Status(ctx context.Context, a publishing.Actor, clientKey, channelID string, publicID string) (channelstore.SendOperation, error) {
	op, found, err := s.Operations.SendOperation(ctx, publicID)
	if err != nil {
		return channelstore.SendOperation{}, &publishing.Error{Status: 500, Code: publishing.CodeStoreError, Message: err.Error()}
	}
	if !found {
		return channelstore.SendOperation{}, &publishing.Error{Status: 404, Code: publishing.CodeInvalidInput,
			Message: "No send operation has that id."}
	}
	if op.UserID != a.UserID {
		// Not "forbidden": a caller who does not own an operation is told it is
		// not there at all. Knowing an id must not confirm that it exists.
		return channelstore.SendOperation{}, &publishing.Error{Status: 404, Code: publishing.CodeInvalidInput,
			Message: "No send operation has that id."}
	}
	if op.ClientKey != clientKey || op.DestinationChannelID != channelID {
		return channelstore.SendOperation{}, &publishing.Error{Status: 404, Code: publishing.CodeInvalidInput,
			Message: "No send operation has that id on this channel."}
	}
	return op, nil
}

// Cleanup removes what is no longer needed and reports what it did.
//
// It is the answer to a crash between the bytes being written and the operation
// that owns them being recorded, and to a finished send whose attachment is
// still on disk. It is careful in one direction only: an attachment is never
// removed while any operation still names it, because a queued or retrying job
// is about to need those bytes.
type CleanupReport struct {
	// ExpiredOperations is how many finished operations passed their retention
	// window and were deleted.
	ExpiredOperations int
	// AbandonedAttachments is how many staged files nothing claimed were
	// removed.
	AbandonedAttachments int
	// IncompleteOperations is how many operations were stopped because the
	// attachment they need is gone.
	IncompleteOperations int
	// PartialUploads is how many half-written files a crash left behind.
	PartialUploads int
}

// Cleanup sweeps the staging area and the operation history.
//
// The order is load-bearing: operations go first, so an attachment whose owner
// has just been deleted is then unclaimed and the sweep below picks it up in
// the same pass.
func (s *Service) Cleanup(ctx context.Context) (CleanupReport, error) {
	return s.CleanupKeeping(ctx, nil)
}

// CleanupKeeping reconciles this service's operation history and staged files,
// while preserving any media names held by other operation types that share
// the staging area. The caller must settle those operations first and pass
// their remaining live media names here. Partial uploads are swept once, at
// startup, by the caller that owns the shared staging lifecycle.
func (s *Service) CleanupKeeping(ctx context.Context, additionalHeldMediaNames []string) (CleanupReport, error) {
	var report CleanupReport
	// Capture all send ownership scopes before retention deletes finished
	// operation rows. A sweep may delete abandoned media only for owners this
	// operation store can positively identify.
	allOps, err := s.Operations.SendOperationsHoldingMedia(ctx)
	if err != nil {
		return report, err
	}
	owners := make([]staging.OwnerFilter, 0, len(allOps))
	seenOwners := make(map[staging.OwnerFilter]bool, len(allOps))
	for _, op := range allOps {
		filter := staging.OwnerFilter{OwnerUserID: op.UserID, AccountID: op.AccountID}
		if filter.OwnerUserID > 0 && filter.AccountID != "" && !seenOwners[filter] {
			owners = append(owners, filter)
			seenOwners[filter] = true
		}
	}

	if s.HistoryRetention > 0 {
		before := time.Now().UTC().Add(-s.HistoryRetention)
		expired, err := s.Operations.ExpiredSendOperations(ctx, before)
		if err != nil {
			return report, err
		}
		for _, op := range expired {
			s.discard(op.MediaStagingName)
			if err := s.Operations.DeleteSendOperation(ctx, op.ID); err != nil {
				s.logf("sending: delete expired operation %s: %v", op.PublicID, err)
				continue
			}
			report.ExpiredOperations++
		}
	}

	// An operation whose attachment is gone cannot be performed. It is stopped
	// here rather than left queued to fail on every attempt, and never reported
	// as delivered: a missing attachment is not a successful send.
	//
	// Everything else keeps its file for exactly as long as it might still need
	// it. A finished send does not: the bytes have already gone up, or they
	// never will, and both cases are done with them.
	held, err := s.Operations.SendOperationsHoldingMedia(ctx)
	if err != nil {
		return report, err
	}
	keep := make(map[string]bool, len(held)+len(additionalHeldMediaNames))
	for _, op := range held {
		switch op.Status {
		case channelstore.SendSent, channelstore.SendFailed, channelstore.SendUnknown:
			continue
		}
		if !s.attachmentThereFor(ctx, op) {
			if err := s.Operations.FinishSendOperation(ctx, op.ID, channelstore.SendFailed, 0, StagePreparing,
				CodeSendAttachmentMissing, "The uploaded attachment is no longer available."); err != nil {
				s.logf("sending: stop operation %s: %v", op.PublicID, err)
				continue
			}
			report.IncompleteOperations++
			continue
		}
		keep[op.MediaStagingName] = true
	}
	for _, name := range additionalHeldMediaNames {
		if name != "" {
			keep[name] = true
		}
	}

	keepNames := make([]string, 0, len(keep))
	for name := range keep {
		keepNames = append(keepNames, name)
	}
	removed, err := s.Staging.ReconcileOwnedKeeping(ctx, keepNames, owners)
	if err != nil {
		return report, err
	}
	report.AbandonedAttachments += removed

	// A half-written file is only ever created by a write this process is
	// performing, so sweeping them at start-up — when none can be in flight —
	// is safe and is the only time it is.
	partials, err := s.Staging.SweepPartials(0)
	if err != nil {
		return report, err
	}
	report.PartialUploads = partials
	return report, nil
}
