package worker

import (
	"context"
	"mime"
	"net/http"
	"strings"

	"github.com/mirza/telesync/internal/rpc"
)

const maxChannelAvatarBytes = 5 << 20

func (s *Service) CheckUsername(ctx context.Context, req rpc.UsernameAvailabilityRequest) (rpc.UsernameAvailability, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.UsernameAvailability{}, err
	}
	capability, ok := s.tg.(ChannelCreationTelegram)
	if !ok {
		return rpc.UsernameAvailability{}, rpc.NewError(rpc.CodeNotConnected, "Username lookup is unavailable for this connection.", 0)
	}
	result, err := capability.CheckUsername(ctx, req.Username)
	if err != nil {
		return rpc.UsernameAvailability{}, s.mapDiscoveryError(err)
	}
	return rpc.UsernameAvailability{Username: result.Username, Status: result.Status, Assignable: result.Assignable}, nil
}

func (s *Service) CreateChannel(ctx context.Context, req rpc.CreateChannelRequest) (rpc.CreateChannelResult, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.CreateChannelResult{}, err
	}
	if err := validateCreateChannel(req); err != nil {
		return rpc.CreateChannelResult{}, err
	}
	capability, ok := s.tg.(ChannelCreationTelegram)
	if !ok {
		return rpc.CreateChannelResult{}, rpc.NewError(rpc.CodeNotConnected, "Channel creation is unavailable for this connection.", 0)
	}

	info, err := capability.CreateBroadcastChannel(ctx, strings.TrimSpace(req.Title), strings.TrimSpace(req.Description))
	if err != nil {
		return rpc.CreateChannelResult{}, s.mapDiscoveryError(err)
	}
	result := rpc.CreateChannelResult{ChannelCreated: true}
	var setupErrors []string

	if req.Visibility == "public" {
		if err := capability.SetBroadcastChannelUsername(ctx, info.ID, req.Username); err != nil {
			setupErrors = append(setupErrors, "username_assignment_failed")
		} else {
			info.Username = req.Username
			result.UsernameAssigned = true
		}
	}
	if req.Avatar != nil {
		photoID, err := capability.SetBroadcastChannelAvatar(ctx, info.ID, req.Avatar.Name, req.Avatar.MimeType, req.Avatar.Data)
		if err != nil {
			setupErrors = append(setupErrors, "avatar_update_failed")
		} else {
			info.HasPhoto = true
			info.PhotoID = photoID
			result.AvatarUpdated = true
		}
	}
	if len(setupErrors) > 0 {
		result.ErrorCode = strings.Join(setupErrors, "+")
	}
	if len(setupErrors) == 1 {
		if result.ErrorCode == "username_assignment_failed" {
			result.ErrorMessage = "The channel was created, but Telegram did not confirm the public username."
		} else {
			result.ErrorMessage = "The channel was created, but Telegram did not confirm the avatar update."
		}
	} else if len(setupErrors) > 1 {
		result.ErrorMessage = "The channel was created, but Telegram did not confirm the public username or avatar update."
	}
	channel := channelInfoDTO(info)
	result.Channel = &rpc.ChannelResult{Channel: channel}
	return result, nil
}

func validateCreateChannel(req rpc.CreateChannelRequest) error {
	title := strings.TrimSpace(req.Title)
	if title == "" || len([]rune(title)) > 128 {
		return rpc.NewError(rpc.CodeInvalidInput, "title must contain between 1 and 128 characters", 0)
	}
	if len([]rune(req.Description)) > 255 {
		return rpc.NewError(rpc.CodeInvalidInput, "description must be at most 255 characters", 0)
	}
	if req.Visibility != "private" && req.Visibility != "public" {
		return rpc.NewError(rpc.CodeInvalidInput, "visibility must be private or public", 0)
	}
	if req.Visibility == "public" {
		if len(req.Username) < 5 || len(req.Username) > 32 {
			return rpc.NewError(rpc.CodeInvalidInput, "public username must contain between 5 and 32 characters", 0)
		}
		for i, r := range req.Username {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') || i == 0 && !(r >= 'a' && r <= 'z') {
				return rpc.NewError(rpc.CodeInvalidInput, "public username contains unsupported characters", 0)
			}
		}
	} else if req.Username != "" {
		return rpc.NewError(rpc.CodeInvalidInput, "private channels cannot have a public username", 0)
	}
	if req.Avatar != nil {
		if len(req.Avatar.Data) == 0 || len(req.Avatar.Data) > maxChannelAvatarBytes {
			return rpc.NewError(rpc.CodeMediaTooLarge, "avatar must be between 1 byte and 5 MiB", 0)
		}
		mediaType, _, err := mime.ParseMediaType(req.Avatar.MimeType)
		if err != nil || !(mediaType == "image/jpeg" || mediaType == "image/png" || mediaType == "image/webp" || mediaType == "image/gif") {
			return rpc.NewError(rpc.CodeInvalidInput, "avatar must be a supported image", 0)
		}
		detected, _, _ := mime.ParseMediaType(http.DetectContentType(req.Avatar.Data))
		if detected != mediaType {
			return rpc.NewError(rpc.CodeInvalidInput, "avatar content does not match its declared image type", 0)
		}
	}
	return nil
}
