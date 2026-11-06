package publishing

import (
	"errors"
	"net/http"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// Failure codes. They are the publishing route's documented codes, restated
// here because the producer must not import the HTTP gateway that renders them.
// The gateway's own constants carry the same strings; its tests pin what a
// client sees.
const (
	CodeInvalidInput                 = "invalid_input"
	CodeStoreError                   = "store_error"
	CodeRateLimited                  = "rate_limited"
	CodeWorkerUnavail                = "worker_unavailable"
	CodePublishForbidden             = "publish_forbidden"
	CodePublishSourceMissing         = "publish_source_unavailable"
	CodePublishSourceDenied          = "publish_source_forbidden"
	CodePublishDestMissing           = "publish_destination_unavailable"
	CodePublishDestDenied            = "publish_destination_forbidden"
	CodePublishDenied                = "publish_permission_denied"
	CodePublishUnsupported           = "publish_unsupported"
	CodePublishMediaTooLarge         = "publish_media_too_large"
	CodePublishConflict              = "publish_idempotency_conflict"
	CodePublishUnknown               = "publish_outcome_unknown"
	CodePublishFailed                = "publish_failed"
	CodePublishSourceMedia           = "publish_source_media_unavailable"
	CodePublishMediaUpload           = "publish_media_upload_failed"
	CodePublishWordPressAuth         = "publish_wordpress_auth_failed"
	CodePublishWordPressForbidden    = "publish_wordpress_forbidden"
	CodePublishWordPressTimeout      = "publish_wordpress_timeout"
	CodePublishWordPressUnreachable  = "publish_wordpress_unreachable"
	CodePublishWordPressIncompatible = "publish_wordpress_incompatible"
)

// Error is a failed publication together with the answer its caller owes.
//
// Status is the HTTP status the publishing route reports. A background run
// ignores it and reads Code and Message, so nothing outside the HTTP layer has
// to pretend to know about status codes.
//
// The field order is load-bearing: internal/gateway builds these with unkeyed
// literals through a type alias.
type Error struct {
	Status  int
	Code    string
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// CodeOf reports the failure code of an error, whatever shape it arrived in.
// An error this pipeline did not classify is a store failure, which is what an
// unclassified failure has always been reported as.
func CodeOf(err error) string {
	var perr *Error
	if errors.As(err, &perr) && perr.Code != "" {
		return perr.Code
	}
	return CodeStoreError
}

// sendFailureOf is the rpc code a failed send names, whatever shape it arrived
// in. A worker reached over a socket answers with a sentinel joined to its
// error; one in this process may answer with the coded error alone. Both mean
// the same thing to a caller, so both read as the code.
func sendFailureOf(err error) string {
	switch {
	case errors.Is(err, rpc.ErrSendForbidden):
		return rpc.CodeSendForbidden
	case errors.Is(err, rpc.ErrSendUnsupported):
		return rpc.CodeSendUnsupported
	case errors.Is(err, rpc.ErrMediaTooLarge):
		return rpc.CodeMediaTooLarge
	case errors.Is(err, rpc.ErrMessageTooLong):
		return rpc.CodeMessageTooLong
	}
	var coded *rpc.Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

// statusForRPCCode is the HTTP status one worker code is reported as.
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

// statusOf picks the HTTP status for a worker failure.
func statusOf(err error, code string) int {
	switch code {
	case CodePublishDenied, CodePublishForbidden:
		return http.StatusForbidden
	case CodePublishUnsupported:
		return http.StatusBadRequest
	case CodePublishSourceMissing, CodePublishDestMissing:
		return http.StatusNotFound
	case CodePublishSourceMedia, CodePublishMediaUpload:
		// The stage is named in the code; the answer is that the other side of
		// the transfer did not do its part.
		return http.StatusBadGateway
	case CodePublishMediaTooLarge:
		return http.StatusRequestEntityTooLarge
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

// messageFor is the caller-facing sentence for a worker failure.
//
// The stage the failure happened in is what a caller acts on, so a post that
// carried an attachment and was refused is described as the destination
// refusing the attachment — never as the source attachment being unreadable,
// which is a different failure with a different remedy.
func messageFor(err error, code string) string {
	switch sendFailureOf(err) {
	case rpc.CodeSendForbidden:
		return "The destination refused the message: this account may not post there."
	case rpc.CodeSendUnsupported:
		return "This content cannot be sent to that destination."
	case rpc.CodeMessageTooLong:
		return "The message text is too long to send."
	case rpc.CodeMediaTooLarge:
		return "The attachment is too large to send."
	}
	if code == CodePublishMediaUpload {
		return "The destination did not accept the attachment; nothing was published."
	}
	var coded *rpc.Error
	if errors.As(err, &coded) && coded.Message != "" {
		return coded.Message
	}
	return "The message could not be published."
}

// classifySend turns a failed send into both answers it has to give: the state
// the durable record is left in, and the error the caller is answered with.
//
// A failure Telegram (or a WordPress site) answered is a failure; one we never
// heard back from is unknown, and is never retried behind the caller's back.
func classifySend(sendErr error, mediaSent bool) (channelstore.PublishStatus, *Error) {
	status := channelstore.PublishFailed
	code := CodePublishFailed
	var flood interface{ FloodSeconds() int }
	// A worker answers with a sentinel or with a coded error, and both must
	// classify the same way: the caller's answer depends on the stage that
	// failed, not on whether the worker was reached over a socket.
	sent := sendFailureOf(sendErr)
	switch {
	case errors.As(sendErr, &flood):
		code = CodeRateLimited
	case sent == rpc.CodeSendForbidden:
		code = CodePublishDenied
	case sent == rpc.CodeSendUnsupported:
		// The destination cannot take this content at all. It is not a source
		// problem: the attachment was read and is in hand.
		code = CodePublishUnsupported
	case sent == rpc.CodeMediaTooLarge:
		code = CodePublishMediaTooLarge
	case errors.Is(sendErr, rpc.ErrTimeout), errors.Is(sendErr, rpc.ErrUnavailable):
		status, code = channelstore.PublishUnknown, CodePublishUnknown
	case mediaSent && sent == "":
		// The attachment was read successfully and the destination would not
		// take the post carrying it. That is a destination failure and is named
		// as one, never as an unreadable source. Only an error that names no
		// stage at all lands here: one that does name a stage is answered above
		// with the stage it names.
		code = CodePublishMediaUpload
	}
	return status, &Error{
		Status:  statusOf(sendErr, code),
		Code:    code,
		Message: messageFor(sendErr, code),
	}
}
