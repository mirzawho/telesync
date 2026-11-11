package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

const channelAvatarLimit = 5 << 20

func (s *Server) channelUsernameAvailability(ctx context.Context, w http.ResponseWriter, r *http.Request, key string, worker Worker, requestID string) {
	if !actorAuth(r).Global(channelstore.PermissionChannelsCreate) {
		s.forbidden(w, requestID)
		return
	}
	username := r.URL.Query().Get("username")
	if strings.TrimSpace(username) == "" {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "username is required", requestID)
		return
	}
	creation, ok := worker.(channelCreationWorker)
	if !ok {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail, "Username availability is unavailable for this connection.", requestID)
		return
	}
	out, err := creation.CheckUsername(ctx, rpc.UsernameAvailabilityRequest{Username: username})
	if err != nil {
		s.upstreamError(w, key, "username_availability", err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: out, Meta: metaFor(requestID, s.cfg.Now())})
}

func (s *Server) createChannel(ctx context.Context, w http.ResponseWriter, r *http.Request, key string, worker Worker, requestID string) {
	if !actorAuth(r).Global(channelstore.PermissionChannelsCreate) {
		s.forbidden(w, requestID)
		return
	}
	req, err := decodeChannelCreation(w, r)
	if err != nil {
		code, message := "invalid_channel_request", err.Error()
		if strings.Contains(err.Error(), "title") {
			code = "invalid_channel_title"
		}
		s.fail(w, http.StatusBadRequest, code, message, requestID)
		return
	}
	if err := validateChannelCreation(req); err != nil {
		code := "invalid_channel_request"
		if strings.Contains(err.Error(), "title") {
			code = "invalid_channel_title"
		}
		s.fail(w, http.StatusBadRequest, code, err.Error(), requestID)
		return
	}
	if s.cfg.Channels == nil || s.cfg.ChannelCreations == nil {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail, "Local channel storage is not configured.", requestID)
		return
	}
	creation, ok := worker.(channelCreationWorker)
	if !ok {
		s.fail(w, http.StatusServiceUnavailable, CodeWorkerUnavail, "Channel creation is unavailable for this connection.", requestID)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		s.fail(w, http.StatusBadRequest, CodeInvalidInput, "a valid Idempotency-Key header is required", requestID)
		return
	}
	userID := actorOf(r).user.ID
	if userID < 0 {
		s.fail(w, http.StatusForbidden, CodeForbidden, "Channel creation requires an administrator account.", requestID)
		return
	}

	avatarDigest := ""
	if req.Avatar != nil {
		digest := sha256.Sum256(req.Avatar.Data)
		avatarDigest = hex.EncodeToString(digest[:])
	}
	fingerprintInput := struct{ Title, Description, Visibility, Username, AvatarSHA256 string }{req.Title, req.Description, req.Visibility, req.Username, avatarDigest}
	fingerprintBytes, _ := json.Marshal(fingerprintInput)
	fingerprintHash := sha256.Sum256(fingerprintBytes)
	fingerprint := hex.EncodeToString(fingerprintHash[:])
	payloadBytes, _ := json.Marshal(fingerprintInput)
	op, created, err := s.cfg.ChannelCreations.BeginChannelCreation(ctx, userID, key, idempotencyKey, fingerprint, string(payloadBytes))
	if err != nil {
		if errors.Is(err, channelstore.ErrChannelCreationIdempotencyConflict) {
			s.fail(w, http.StatusConflict, "idempotency_conflict", "This Idempotency-Key was already used for a different request.", requestID)
			return
		}
		s.fail(w, http.StatusInternalServerError, CodeWorkerError, "The channel-creation operation could not be recorded.", requestID)
		return
	}
	if !created {
		if op.RemoteChannelID == 0 {
			s.fail(w, http.StatusConflict, "channel_creation_outcome_unknown", "A prior create attempt has no confirmed Telegram result; it was not retried to avoid creating a duplicate.", requestID)
			return
		}
		channel, exists, getErr := s.cfg.Channels.Get(ctx, key, op.RemoteChannelID)
		if getErr != nil || !exists {
			s.fail(w, http.StatusConflict, "channel_creation_recovery_required", "The channel was created, but its local record is unavailable; the create was not repeated.", requestID)
			return
		}
		s.writeCreatedChannel(w, requestID, key, channel, op, true)
		return
	}

	result, err := creation.CreateChannel(ctx, req)
	if err != nil {
		op.Status = channelstore.ChannelCreationUnknown
		op.ErrorCode = "remote_outcome_unknown"
		op.ErrorMessage = "The Telegram result could not be confirmed."
		_ = s.cfg.ChannelCreations.UpdateChannelCreation(ctx, op)
		s.fail(w, http.StatusBadGateway, "channel_creation_outcome_unknown", "The Telegram result could not be confirmed; retry with the same key only after reconciliation.", requestID)
		return
	}
	if result.Channel == nil || result.Channel.Channel.ID == "" {
		op.Status = channelstore.ChannelCreationUnknown
		op.ErrorCode = "remote_identity_missing"
		_ = s.cfg.ChannelCreations.UpdateChannelCreation(ctx, op)
		s.fail(w, http.StatusBadGateway, "channel_creation_outcome_unknown", "Telegram did not return a channel identity; the create was not retried.", requestID)
		return
	}
	remoteID, parseErr := strconv.ParseInt(result.Channel.Channel.ID, 10, 64)
	if parseErr != nil || remoteID <= 0 {
		s.fail(w, http.StatusBadGateway, CodeWorkerError, "Telegram returned an invalid channel identity.", requestID)
		return
	}
	op.RemoteChannelID = remoteID
	op.ChannelCreated = result.ChannelCreated
	op.UsernameAssigned = result.UsernameAssigned
	op.AvatarUpdated = result.AvatarUpdated
	op.Status = channelstore.ChannelCreationComplete
	if result.ErrorCode != "" {
		op.Status = channelstore.ChannelCreationPartial
		op.ErrorCode = result.ErrorCode
		op.ErrorMessage = result.ErrorMessage
	}
	info := result.Channel.Channel
	saved, saveErr := s.cfg.Channels.Upsert(ctx, channelstore.Channel{
		ClientKey: key, ChannelID: remoteID, PeerType: peerTypeOf(info.Type), Title: info.Title,
		Username: info.Username, Description: info.Description, AvatarPhotoID: info.AvatarVersion,
		AvatarAvailable: info.AvatarAvailable, MembershipStatus: statusOf(info, false),
		Verified: info.Verified, Scam: info.Scam, Fake: info.Fake, NoticesKnown: true,
		Permissions: tgapiPermissionsOf(info.Permissions), PermissionsKnown: true,
		ParticipantsCount: info.ParticipantsCount,
	})
	if saveErr != nil {
		op.Status = channelstore.ChannelCreationPartial
		op.ErrorCode = combineChannelCreationErrors(op.ErrorCode, "local_registration_failed")
		op.ErrorMessage = strings.TrimSpace(op.ErrorMessage + " Telegram created the channel, but local registration failed.")
		_ = s.cfg.ChannelCreations.UpdateChannelCreation(ctx, op)
		writeJSON(w, http.StatusOK, envelope{Data: map[string]any{
			"channel": s.channelViewOf(key, info, false), "remote_channel_id": strconv.FormatInt(remoteID, 10),
			"local_channel_id": nil, "channel_created": op.ChannelCreated,
			"username_assigned": op.UsernameAssigned, "avatar_updated": op.AvatarUpdated,
			"locally_registered": false, "status": channelstore.ChannelCreationPartial,
			"error_code": op.ErrorCode, "error_message": op.ErrorMessage, "replayed": false,
		}, Meta: metaFor(requestID, s.cfg.Now())})
		return
	}
	op.LocalChannelID, op.LocallyRegistered = saved.ID, true
	if err := s.cfg.ChannelCreations.UpdateChannelCreation(ctx, op); err != nil {
		op.Status = channelstore.ChannelCreationPartial
		op.ErrorCode = combineChannelCreationErrors(op.ErrorCode, "operation_finalize_failed")
		op.ErrorMessage = strings.TrimSpace(op.ErrorMessage + " Telegram created the channel and it was locally registered, but the operation result could not be finalized.")
		s.writeCreatedChannel(w, requestID, key, saved, op, false)
		return
	}
	s.writeCreatedChannel(w, requestID, key, saved, op, false)
}

func combineChannelCreationErrors(existing, next string) string {
	if existing == "" {
		return next
	}
	if next == "" || strings.Contains("+"+existing+"+", "+"+next+"+") {
		return existing
	}
	return existing + "+" + next
}

func (s *Server) writeCreatedChannel(w http.ResponseWriter, requestID, key string, saved channelstore.Channel, op channelstore.ChannelCreationOperation, replay bool) {
	info := rpc.ChannelInfo{ID: strconv.FormatInt(saved.ChannelID, 10), Type: saved.PeerType, Title: saved.Title, Username: saved.Username,
		Description: saved.Description, AvatarAvailable: saved.AvatarAvailable, AvatarVersion: saved.AvatarPhotoID,
		IsMember: saved.MembershipStatus == channelstore.StatusMember, MembershipStatus: saved.MembershipStatus,
		Verified: saved.Verified, Scam: saved.Scam, Fake: saved.Fake, ParticipantsCount: saved.ParticipantsCount}
	if saved.PermissionsKnown {
		info.Permissions = rpc.Permissions{
			New: saved.Permissions.New, Edit: saved.Permissions.Edit,
			Delete: saved.Permissions.Delete, Read: saved.Permissions.Read,
		}
	}
	view := s.channelViewOf(key, info, true)
	status := op.Status
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{
		"channel": view, "remote_channel_id": strconv.FormatInt(saved.ChannelID, 10),
		"local_channel_id": saved.ID, "channel_created": op.ChannelCreated,
		"username_assigned": op.UsernameAssigned, "avatar_updated": op.AvatarUpdated,
		"locally_registered": op.LocallyRegistered, "status": status,
		"error_code": op.ErrorCode, "error_message": op.ErrorMessage, "replayed": replay,
	}, Meta: metaFor(requestID, s.cfg.Now())})
}

func tgapiPermissionsOf(p rpc.Permissions) tgapi.Permissions {
	return tgapi.Permissions{New: p.New, Edit: p.Edit, Delete: p.Delete, Read: p.Read}
}

func validateChannelCreation(req rpc.CreateChannelRequest) error {
	if strings.TrimSpace(req.Title) == "" || len([]rune(strings.TrimSpace(req.Title))) > 128 {
		return errors.New("title must contain between 1 and 128 characters")
	}
	if len([]rune(req.Description)) > 255 {
		return errors.New("description must be at most 255 characters")
	}
	if req.Visibility != "private" && req.Visibility != "public" {
		return errors.New("visibility must be private or public")
	}
	if req.Visibility == "public" {
		if len(req.Username) < 5 || len(req.Username) > 32 {
			return errors.New("public username must contain between 5 and 32 characters")
		}
		for i, r := range req.Username {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') || i == 0 && !(r >= 'a' && r <= 'z') {
				return errors.New("public username contains unsupported characters")
			}
		}
	} else if req.Username != "" {
		return errors.New("private channels cannot have a public username")
	}
	if req.Avatar != nil {
		if len(req.Avatar.Data) == 0 || len(req.Avatar.Data) > channelAvatarLimit {
			return errors.New("avatar must be between 1 byte and 5 MiB")
		}
		mediaType, _, err := mime.ParseMediaType(req.Avatar.MimeType)
		if err != nil || !(mediaType == "image/jpeg" || mediaType == "image/png" || mediaType == "image/webp" || mediaType == "image/gif") {
			return errors.New("avatar must be JPEG, PNG, WebP or GIF")
		}
		detected, _, _ := mime.ParseMediaType(http.DetectContentType(req.Avatar.Data))
		if detected != mediaType {
			return errors.New("avatar content does not match its declared image type")
		}
	}
	return nil
}

func decodeChannelCreation(w http.ResponseWriter, r *http.Request) (rpc.CreateChannelRequest, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return rpc.CreateChannelRequest{}, errors.New("invalid content type")
	}
	var req rpc.CreateChannelRequest
	if mediaType == "application/json" {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			return req, errors.New("invalid JSON request")
		}
		return req, nil
	}
	if mediaType != "multipart/form-data" || params["boundary"] == "" {
		return req, errors.New("request must be JSON or multipart/form-data")
	}
	r.Body = http.MaxBytesReader(w, r.Body, channelAvatarLimit+(64<<10))
	reader := multipart.NewReader(r.Body, params["boundary"])
	sawJSON, sawFile := false, false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return req, errors.New("invalid multipart request")
		}
		switch part.FormName() {
		case "request":
			if sawJSON {
				part.Close()
				return req, errors.New("duplicate request part")
			}
			sawJSON = true
			b, err := io.ReadAll(io.LimitReader(part, 64<<10+1))
			if err != nil || len(b) > 64<<10 || json.Unmarshal(b, &req) != nil {
				part.Close()
				return req, errors.New("invalid request part")
			}
		case "media":
			if sawFile {
				part.Close()
				return req, errors.New("duplicate avatar file")
			}
			sawFile = true
			data, err := io.ReadAll(io.LimitReader(part, channelAvatarLimit+1))
			if err != nil || len(data) == 0 || len(data) > channelAvatarLimit {
				part.Close()
				return req, errors.New("avatar must be at most 5 MiB")
			}
			if req.Avatar == nil {
				req.Avatar = &rpc.SendMedia{}
			}
			req.Avatar.Data = data
			req.Avatar.Name = filepath.Base(part.FileName())
			if req.Avatar.MimeType == "" {
				req.Avatar.MimeType = part.Header.Get("Content-Type")
			}
		default:
			part.Close()
			return req, errors.New("unexpected multipart part")
		}
		_ = part.Close()
	}
	if !sawJSON {
		return req, errors.New("request part is required")
	}
	if req.Avatar != nil && !sawFile {
		return req, errors.New("avatar metadata requires a file")
	}
	return req, nil
}
