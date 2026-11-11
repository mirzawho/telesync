package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// Query kinds understood by Find.
const (
	FindByUsername = "username"
	FindByInvite   = "invite"
	FindByID       = "id"
	FindByText     = "text"
)

// FindRequest asks a worker to resolve one discovery query.
//
// The public query string is parsed by the gateway (it is public input) and the
// worker receives the structured result: it decides how to talk to Telegram,
// never how to interpret the URL.
type FindRequest struct {
	Kind      string `json:"kind"`
	Username  string `json:"username,omitempty"`
	Invite    string `json:"invite,omitempty"`
	ChannelID int64  `json:"channel_id,omitempty"`
	Text      string `json:"text,omitempty"`
}

// ChannelRef addresses one channel. Exactly one field is set.
type ChannelRef struct {
	ChannelID int64  `json:"channel_id,omitempty"`
	Invite    string `json:"invite,omitempty"`
}

// Permissions is the channel-level capability set. All four fields are plain
// booleans: the public API contract never renders a permission as null.
type Permissions struct {
	New    bool `json:"new"`
	Edit   bool `json:"edit"`
	Delete bool `json:"delete"`
	Read   bool `json:"read"`
}

// ChannelInfo is one channel as the worker resolved it from Telegram.
type ChannelInfo struct {
	// ID is the decimal channel id as a string: JavaScript loses precision on
	// the raw int64.
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Username    string `json:"username,omitempty"`
	Description string `json:"description,omitempty"`
	// AvatarAvailable says whether the channel has a profile photo to fetch.
	AvatarAvailable bool `json:"avatar_available"`
	// HasPhoto is whether Telegram reported a profile photo at all. It is the
	// question AvatarAvailable answers when the backend does not know the
	// photo's version: a channel can have a photo with a zero AvatarVersion.
	HasPhoto bool `json:"has_photo"`
	// AvatarVersion changes when the channel changes its photo, so a cache can
	// tell a stale image from a valid one. Zero means "not reported".
	AvatarVersion int64 `json:"avatar_version,omitempty"`
	// IsMember is the account's actual Telegram membership.
	IsMember bool `json:"is_member"`
	// MembershipStatus is one of member, not_member, join_pending, unknown.
	MembershipStatus string      `json:"membership_status"`
	Permissions      Permissions `json:"permissions"`
	Verified         bool        `json:"verified"`
	Scam             bool        `json:"scam"`
	Fake             bool        `json:"fake"`
	// ParticipantsCount is null when Telegram did not report one.
	ParticipantsCount *int `json:"participants_count"`
}

// FindResult is the answer to FindRequest.
type FindResult struct {
	Channels []ChannelInfo `json:"channels"`
}

// Join outcomes reported on the wire.
const (
	OutcomeJoined        = "joined"
	OutcomeAlreadyMember = "already_member"
	OutcomePending       = "join_pending"
)

// JoinResult reports what a join attempt actually achieved.
type JoinResult struct {
	Channel ChannelInfo `json:"channel"`
	// Outcome is joined, already_member or join_pending. Only the first two
	// mean Telegram confirmed membership.
	Outcome string `json:"outcome"`
}

// ChannelResult is one resolved channel.
type ChannelResult struct {
	Channel ChannelInfo `json:"channel"`
}

// LeaveResult is what a leave attempt achieved.
type LeaveResult struct {
	// AlreadyAbsent is true when the account was not a member when the call
	// was made: Telegram had nothing to undo. It is not an error.
	AlreadyAbsent bool `json:"already_absent"`
}

// AvatarFile is a channel's profile photo bytes.
type AvatarFile struct {
	MimeType string `json:"mime_type"`
	// PhotoID is Telegram's photo version, when the backend reported one.
	PhotoID int64  `json:"photo_id,omitempty"`
	Data    []byte `json:"data"`
}

// JobTimeout bounds one avatar download on the wire.
const JobTimeout = 60 * time.Second

// --- wire plumbing ---

func (c *Client) post(ctx context.Context, path string, body, dst any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode worker request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://worker"+path,
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, dst)
}

// Find resolves one discovery query.
func (c *Client) Find(ctx context.Context, req FindRequest) (FindResult, error) {
	var out FindResult
	err := c.post(ctx, "/find", req, &out)
	return out, err
}

// Channel resolves one channel reference.
func (c *Client) Channel(ctx context.Context, ref ChannelRef) (ChannelResult, error) {
	var out ChannelResult
	err := c.post(ctx, "/channel", ref, &out)
	return out, err
}

// Join joins the channel if needed and reports what happened.
func (c *Client) Join(ctx context.Context, ref ChannelRef) (JoinResult, error) {
	var out JoinResult
	err := c.post(ctx, "/join", ref, &out)
	return out, err
}

// Avatar downloads the channel's profile photo.
func (c *Client) Avatar(ctx context.Context, ref ChannelRef) (AvatarFile, error) {
	var out AvatarFile
	err := c.post(ctx, "/avatar", ref, &out)
	return out, err
}

// Leave makes the account leave the channel.
func (c *Client) Leave(ctx context.Context, ref ChannelRef) (LeaveResult, error) {
	var out LeaveResult
	err := c.post(ctx, "/leave", ref, &out)
	return out, err
}

// SendMessage sends one prepared post through this worker.
//
// The request and its attachment travel as a multipart body: the JSON half
// describes the message, the file half carries the bytes. A large attachment
// never rides inside a JSON document.
func (c *Client) SendMessage(ctx context.Context, req SendRequest) (SendResult, error) {
	var out SendResult
	err := c.postMultipart(ctx, "/messages", req, req.Media, &out)
	return out, err
}

// EditMessage asks this worker to replace one existing channel message. The
// request and its new attachment travel in the same bounded multipart form as
// direct send, but the target message ID remains part of the JSON request.
func (c *Client) EditMessage(ctx context.Context, req EditRequest) (EditResult, error) {
	var out EditResult
	err := c.postMultipart(ctx, "/edit_message", req, req.Media, &out)
	return out, err
}

// DeleteMessage synchronously asks this worker to delete one channel message.
func (c *Client) DeleteMessage(ctx context.Context, req DeleteMessageRequest) (DeleteMessageResult, error) {
	var out DeleteMessageResult
	err := c.post(ctx, "/delete_message", req, &out)
	return out, err
}

// postMultipart sends one request as multipart/form-data: a "request" part
// holding the JSON body and, when present, a "media" part holding the bytes.

func (c *Client) postMultipart(ctx context.Context, path string, body any, media *SendMedia, dst any) error {
	if media != nil && len(media.Data) > MaxSendMediaBytes {
		return fmt.Errorf("%w: %d bytes exceeds the %d byte limit",
			ErrMediaTooLarge, len(media.Data), MaxSendMediaBytes)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	jsonPart, err := w.CreateFormField("request")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode worker request: %w", err)
	}
	if _, err := jsonPart.Write(payload); err != nil {
		return err
	}
	if media != nil {
		part, err := w.CreateFormFile("media", media.Name)
		if err != nil {
			return err
		}
		if _, err := part.Write(media.Data); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://worker"+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(req, dst)
}

// do performs one request and decodes the answer.
func (c *Client) do(req *http.Request, dst any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return requestError(req.Context(), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return decodeErrorBody(resp.StatusCode, body)
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decode worker response: %w", err)
	}
	return nil
}

// requestError maps a failed HTTP round trip onto the worker's error contract.
func requestError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ErrTimeout, ctxErr)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// decodeErrorBody turns a non-OK worker answer into the error the contract
// names, without ever echoing the body to a caller that must not see it.
func decodeErrorBody(status int, body []byte) error {
	var envelope ErrorBody
	if jsonErr := json.Unmarshal(body, &envelope); jsonErr == nil && envelope.Error != nil {
		return envelope.Error.wire()
	}
	return NewError(CodeInternal,
		fmt.Sprintf("the worker answered with an unreadable error (status %d)", status), 0)
}
