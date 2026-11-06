package ai

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/automation"
)

func TestImageMediaTypeAcceptsOnlyRealImages(t *testing.T) {
	accepted := []struct{ in, want string }{
		{in: "image/jpeg", want: "image/jpeg"},
		{in: "image/jpg", want: "image/jpeg"},
		{in: "image/png", want: "image/png"},
		{in: "image/webp", want: "image/webp"},
		{in: "image/gif", want: "image/gif"},
		{in: "IMAGE/PNG", want: "image/png"},
		{in: "image/png; charset=utf-8", want: "image/png"},
		{in: "  image/jpeg  ", want: "image/jpeg"},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.in, func(t *testing.T) {
			got, ok := imageMediaType(tc.in)
			if !ok {
				t.Fatalf("imageMediaType(%q) reported false, want true", tc.in)
			}
			if got != tc.want {
				t.Fatalf("imageMediaType(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	rejected := []string{
		"",
		"application/pdf",
		"application/octet-stream",
		"video/mp4",
		"audio/mpeg",
		"image/svg+xml",
		"image/tiff",
		"image/bmp",
		"text/plain",
		"image/",
	}
	for _, in := range rejected {
		t.Run("rejects "+in, func(t *testing.T) {
			if got, ok := imageMediaType(in); ok {
				t.Fatalf("imageMediaType(%q) = %q, true; want a rejection", in, got)
			}
		})
	}
}

func TestSelectImagesDropsEverythingThatIsNotAnImage(t *testing.T) {
	images := []automation.ImageInput{
		{MimeType: "video/mp4", Filename: "clip.mp4", Data: []byte("video bytes")},
		{MimeType: "application/pdf", Filename: "doc.pdf", Data: []byte("pdf bytes")},
		{MimeType: "audio/mpeg", Filename: "song.mp3", Data: []byte("audio bytes")},
		{MimeType: "image/png", Filename: "photo.png", Data: []byte("png bytes")},
	}

	got := selectImages(images, 10, 1<<20)

	if len(got) != 1 {
		t.Fatalf("selectImages kept %d entries, want 1: %#v", len(got), got)
	}
	if got[0].Filename != "photo.png" {
		t.Fatalf("kept %q, want photo.png", got[0].Filename)
	}
}

func TestSelectImagesBoundsTheCount(t *testing.T) {
	var images []automation.ImageInput
	for i := 0; i < 6; i++ {
		images = append(images, automation.ImageInput{MimeType: "image/png", Data: []byte("x")})
	}

	got := selectImages(images, 2, 1<<20)
	if len(got) != 2 {
		t.Fatalf("selectImages kept %d images, want 2", len(got))
	}
}

func TestSelectImagesBoundsTheTotalBytes(t *testing.T) {
	images := []automation.ImageInput{
		{MimeType: "image/png", Data: []byte(strings.Repeat("a", 40))},
		{MimeType: "image/png", Data: []byte(strings.Repeat("b", 40))},
		{MimeType: "image/png", Data: []byte(strings.Repeat("c", 40))},
	}

	got := selectImages(images, 10, 64)
	if len(got) != 1 {
		t.Fatalf("selectImages kept %d images, want 1 under a 64-byte budget", len(got))
	}
	if len(got[0].Data) != 40 {
		t.Fatalf("kept %d bytes, want the first 40", len(got[0].Data))
	}
}

func TestSelectImagesNeverMutatesTheCallerBytes(t *testing.T) {
	data := []byte("original image bytes")
	images := []automation.ImageInput{{MimeType: "image/jpeg", Data: data}}

	got := selectImages(images, 1, 1<<20)
	if len(got) != 1 {
		t.Fatalf("want one image, got %d", len(got))
	}
	if string(data) != "original image bytes" {
		t.Fatalf("the caller's bytes were changed to %q", data)
	}
	if len(got[0].Data) != len(data) {
		t.Fatalf("the kept image was truncated to %d bytes", len(got[0].Data))
	}
}

func TestSelectImagesWithNoBudgetSendsNothing(t *testing.T) {
	images := []automation.ImageInput{{MimeType: "image/png", Data: []byte("x")}}

	if got := selectImages(images, 0, 1<<20); len(got) != 0 {
		t.Fatalf("with maxCount 0 nothing may be sent, got %d", len(got))
	}
	if got := selectImages(images, 10, 0); len(got) != 0 {
		t.Fatalf("with maxBytes 0 nothing may be sent, got %d", len(got))
	}
}

func TestImageContentPartUsesADataURL(t *testing.T) {
	img := automation.ImageInput{MimeType: "image/jpg", Data: []byte("bytes")}
	part := imageContentPart(img)

	kind, _ := part["type"].(string)
	if kind != "image_url" {
		t.Fatalf("type = %q, want image_url", kind)
	}
	urlField, _ := part["image_url"].(map[string]any)
	if urlField == nil {
		t.Fatalf("image_url = %#v, want an object", part["image_url"])
	}
	url, _ := urlField["url"].(string)
	want := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img.Data)
	if url != want {
		t.Fatalf("url = %q, want %q", url, want)
	}
	if strings.Contains(url, "filename") {
		t.Fatal("the content part must not carry the source filename")
	}
}
