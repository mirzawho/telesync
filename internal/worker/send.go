package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// SendMessage implements rpc.Backend.
//
// It sends one prepared post through this worker's own connection. The source
// message the post was composed from is never touched: this is a new message
// to a channel the account can post in.
func (s *Service) SendMessage(ctx context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	if err := s.requireAuthorized(ctx); err != nil {
		return rpc.SendResult{}, err
	}
	if runes := len([]rune(req.Text)); runes > tgapi.MaxMessageRunes {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeMessageTooLong,
			fmt.Sprintf("The message text is %d characters; the limit is %d.", runes, tgapi.MaxMessageRunes), 0)
	}
	out, err := s.tg.SendMessage(ctx, tgapi.ChannelRef{ID: req.ChannelID}, tgapi.SendRequest{
		Text:      req.Text,
		Entities:  entitiesFromWire(req.Entities),
		Media:     sendMediaFromWire(req.Media),
		Silent:    req.Silent,
		NoWebpage: req.NoWebpage,
		RandomID:  req.RandomID,
	})
	if err != nil {
		return rpc.SendResult{}, s.mapSendError(err)
	}
	result := rpc.SendResult{
		ChannelID:          strconv.FormatInt(out.ChannelID, 10),
		UnsendableEntities: out.UnsendableEntities,
	}
	if out.Message != nil {
		msg := s.messageDTO(out.ChannelID, *out.Message)
		result.Message = &msg
	}
	return result, nil
}

func sendMediaFromWire(m *rpc.SendMedia) *tgapi.SendMedia {
	if m == nil {
		return nil
	}
	return &tgapi.SendMedia{
		Kind:     m.Kind,
		Name:     m.Name,
		MimeType: m.MimeType,
		Data:     m.Data,
	}
}

func entitiesFromWire(in []rpc.Entity) []tgapi.Entity {
	out := make([]tgapi.Entity, 0, len(in))
	for _, e := range in {
		out = append(out, tgapi.Entity{
			Type:       e.Type,
			Offset:     e.Offset,
			Length:     e.Length,
			URL:        e.URL,
			Language:   e.Language,
			UserID:     e.UserID,
			DocumentID: e.DocumentID,
		})
	}
	return out
}

// mapSendError classifies a send failure into a coded wire error. Anything the
// adapter already classified keeps its meaning; a refusal becomes a code the
// gateway can report without parsing RPC strings.
func (s *Service) mapSendError(err error) error {
	switch {
	case errors.Is(err, tgapi.ErrSendForbidden):
		return rpc.NewError(rpc.CodeSendForbidden,
			"The destination refused the message: this account may not post there.", 0)
	case errors.Is(err, tgapi.ErrSendUnsupported):
		return rpc.NewError(rpc.CodeSendUnsupported, "This content cannot be sent to that destination.", 0)
	case errors.Is(err, tgapi.ErrMessageTooLong):
		return rpc.NewError(rpc.CodeMessageTooLong, "The message text is too long to send.", 0)
	}
	var flood interface{ FloodSeconds() int }
	if errors.As(err, &flood) {
		return rpc.NewError(rpc.CodeRateLimited, "Telegram is rate limiting this client.", flood.FloodSeconds())
	}
	return s.mapDiscoveryError(err)
}
