package gotdclient_test

import (
	"testing"

	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/gotdclient"
)

func TestDocumentMediaClassificationUsesStickerAttributesBeforeContainerHints(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mime       string
		attributes []tg.DocumentAttributeClass
		want       string
	}{
		{
			name: "static webp sticker",
			mime: "image/webp",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeSticker{Alt: "🙂", Stickerset: &tg.InputStickerSetEmpty{}},
				&tg.DocumentAttributeImageSize{W: 512, H: 512},
			},
			want: "sticker",
		},
		{
			name: "animated tgs sticker",
			mime: "application/x-tgsticker",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeSticker{Alt: "✨", Stickerset: &tg.InputStickerSetEmpty{}},
				&tg.DocumentAttributeAnimated{},
			},
			want: "sticker",
		},
		{
			name: "video webm sticker",
			mime: "video/webm",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeSticker{Alt: "🔥", Stickerset: &tg.InputStickerSetEmpty{}},
				&tg.DocumentAttributeVideo{Duration: 2, W: 512, H: 512},
			},
			want: "sticker",
		},
		{
			name: "generic webp remains document",
			mime: "image/webp",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: "image.webp"},
			},
			want: "document",
		},
		{
			name: "generic mp4 remains document",
			mime: "video/mp4",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: "clip.mp4"},
			},
			want: "document",
		},
		{
			name: "ordinary video attribute",
			mime: "video/mp4",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeVideo{Duration: 4, W: 1280, H: 720},
			},
			want: "video",
		},
		{
			name: "custom emoji is not a sticker",
			mime: "application/x-tgsticker",
			attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeCustomEmoji{Alt: "💠", Stickerset: &tg.InputStickerSetEmpty{}},
			},
			want: "custom_emoji",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			media := &tg.MessageMediaDocument{Document: &tg.Document{
				MimeType: tc.mime, Attributes: tc.attributes,
			}}
			if got := gotdclient.MediaType(media); got != tc.want {
				t.Fatalf("MediaType = %q, want %q", got, tc.want)
			}
			if tc.want == "sticker" {
				for _, attr := range tc.attributes {
					if sticker, ok := attr.(*tg.DocumentAttributeSticker); ok {
						if got := gotdclient.StickerEmoji(media); got != sticker.Alt {
							t.Fatalf("StickerEmoji = %q, want %q", got, sticker.Alt)
						}
						break
					}
				}
			}
		})
	}
}

func TestStickerSubtypesComeOnlyFromStickerDocumentAttributes(t *testing.T) {
	tests := []struct {
		name       string
		mime       string
		attributes []tg.DocumentAttributeClass
		animated   bool
		video      bool
		mask       bool
	}{
		{"static sticker", "image/webp", []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{Mask: true}}, false, false, true},
		{"animated sticker", "application/x-tgsticker", []tg.DocumentAttributeClass{
			&tg.DocumentAttributeSticker{}, &tg.DocumentAttributeAnimated{},
		}, true, false, false},
		{"video sticker", "video/webm", []tg.DocumentAttributeClass{
			&tg.DocumentAttributeSticker{}, &tg.DocumentAttributeVideo{W: 512, H: 512},
		}, false, true, false},
		{"ordinary animated gif", "image/gif", []tg.DocumentAttributeClass{&tg.DocumentAttributeAnimated{}}, false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			media := &tg.MessageMediaDocument{Document: &tg.Document{
				MimeType: tc.mime, Attributes: tc.attributes,
			}}
			if got := gotdclient.StickerAnimated(media); got != tc.animated {
				t.Fatalf("StickerAnimated = %v, want %v", got, tc.animated)
			}
			if got := gotdclient.StickerVideo(media); got != tc.video {
				t.Fatalf("StickerVideo = %v, want %v", got, tc.video)
			}
			if got := gotdclient.StickerMask(media); got != tc.mask {
				t.Fatalf("StickerMask = %v, want %v", got, tc.mask)
			}
		})
	}
}

func TestStickerMaskPlacementIsPreserved(t *testing.T) {
	attr := &tg.DocumentAttributeSticker{Mask: true}
	attr.SetMaskCoords(tg.MaskCoords{N: 2, X: 0.5, Y: -1.25, Zoom: 1.4})
	media := &tg.MessageMediaDocument{Document: &tg.Document{
		MimeType: "image/webp", Attributes: []tg.DocumentAttributeClass{attr},
	}}
	got := gotdclient.StickerMaskCoords(media)
	if got == nil || got.N != 2 || got.X != 0.5 || got.Y != -1.25 || got.Zoom != 1.4 {
		t.Fatalf("StickerMaskCoords = %+v", got)
	}
}
