package worker

import (
	"encoding/json"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

func TestStickerSemanticsSurviveMessageAndSendRPCDescriptors(t *testing.T) {
	input := tgapi.Media{
		Type: "sticker", MimeType: "video/webm", Width: 512, Height: 512,
		StickerEmoji: "🔥", StickerVideo: true, StickerMask: true,
		StickerMaskCoords: &tgapi.StickerMaskCoords{N: 1, X: 0.3, Y: -0.4, Zoom: 1.2},
	}
	message := (&Service{}).messageDTO(154, tgapi.Message{ID: 55, Media: &input})
	if message.Media == nil || message.Media.Type != "sticker" ||
		message.Media.StickerEmoji != "🔥" || !message.Media.StickerVideo ||
		!message.Media.StickerMask || message.Media.StickerMaskCoords == nil ||
		message.Media.StickerMaskCoords.N != 1 || message.Media.Width != 512 || message.Media.Height != 512 {
		t.Fatalf("message media = %+v", message.Media)
	}
	messageJSON, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal message: %v", err)
	}
	var decodedMessage rpc.Message
	if err := json.Unmarshal(messageJSON, &decodedMessage); err != nil {
		t.Fatalf("Unmarshal message: %v", err)
	}
	if decodedMessage.Media == nil || decodedMessage.Media.Type != "sticker" ||
		decodedMessage.Media.StickerEmoji != "🔥" || !decodedMessage.Media.StickerVideo ||
		!decodedMessage.Media.StickerMask || decodedMessage.Media.StickerMaskCoords == nil ||
		decodedMessage.Media.StickerMaskCoords.N != 1 {
		t.Fatalf("decoded message media = %+v", decodedMessage.Media)
	}

	wire := &rpc.SendMedia{
		Kind: "sticker", MimeType: "video/webm", Data: []byte("media"),
		StickerEmoji: "🔥", StickerVideo: true, StickerMask: true,
		StickerMaskCoords: &rpc.StickerMaskCoords{N: 1, X: 0.3, Y: -0.4, Zoom: 1.2},
		Width:             512, Height: 512,
	}
	got := sendMediaFromWire(wire)
	if got == nil || got.Kind != "sticker" || got.StickerEmoji != "🔥" || !got.StickerVideo ||
		!got.StickerMask || got.StickerMaskCoords == nil || got.StickerMaskCoords.N != 1 ||
		got.Width != 512 || got.Height != 512 {
		t.Fatalf("tgapi send media = %+v", got)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded rpc.SendMedia
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Kind != "sticker" || decoded.StickerEmoji != "🔥" || !decoded.StickerVideo ||
		!decoded.StickerMask || decoded.StickerMaskCoords == nil ||
		decoded.StickerMaskCoords.N != 1 || decoded.Width != 512 || decoded.Height != 512 {
		t.Fatalf("decoded send media = %+v", decoded)
	}
}
