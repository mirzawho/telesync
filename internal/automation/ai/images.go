package ai

import (
	"encoding/base64"
	"strings"

	"github.com/mirza/telesync/internal/automation"
)

// imageTypes are the media types this package will ever put in front of a
// model. It is a closed list on purpose: a document, a video, an audio file, a
// vector image or an unknown binary is never sent, whatever the caller passes
// in, because the workflow asks to send images and nothing else.
var imageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

// imageMediaType normalizes an image media type and reports whether it is one
// of the kinds this package will send to a model.
//
// Normalization is limited to what is unambiguous: parameters after a semicolon
// are dropped, the type is lower-cased, and the common image/jpg alias is
// folded into image/jpeg. Everything else is refused rather than guessed at.
func imageMediaType(mime string) (string, bool) {
	base := mime
	if i := strings.IndexByte(base, ';'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToLower(strings.TrimSpace(base))
	if base == "image/jpg" {
		base = "image/jpeg"
	}
	if base == "" || !imageTypes[base] {
		return "", false
	}
	return base, true
}

// selectImages applies the images-only rule and both bounds.
//
// It keeps whole images in the order given and stops at the first one that
// would push the total past maxBytes, so an album is never reordered or
// partially sent. Anything that is not one of the four supported image types is
// skipped without being counted, and an empty or zero budget sends nothing at
// all.
//
// Truncating here is safe in a way truncating text would not be: images are
// auxiliary context for the model and the structured output contract has no
// image field, so nothing that reaches a destination depends on how many images
// the model saw. The bytes themselves are never copied or written anywhere.
func selectImages(images []automation.ImageInput, maxCount int, maxBytes int64) []automation.ImageInput {
	if maxCount <= 0 || maxBytes <= 0 {
		return nil
	}
	var out []automation.ImageInput
	var total int64
	for _, img := range images {
		if len(out) >= maxCount {
			break
		}
		mime, ok := imageMediaType(img.MimeType)
		if !ok {
			continue
		}
		size := int64(len(img.Data))
		if size == 0 {
			continue
		}
		if total+size > maxBytes {
			break
		}
		total += size
		out = append(out, automation.ImageInput{MimeType: mime, Filename: img.Filename, Data: img.Data})
	}
	return out
}

// imageContentPart builds one OpenAI-style image content part.
//
// The bytes travel as a data URI inside the request body and are never written
// to disk, cached, or referenced by a path. The filename is deliberately not
// carried: it is metadata of the source message and has no meaning to a model.
//
// It returns nil for anything imageMediaType refuses, which cannot happen for
// output of selectImages; the caller must treat nil as "do not send".
func imageContentPart(img automation.ImageInput) map[string]any {
	mime, ok := imageMediaType(img.MimeType)
	if !ok {
		return nil
	}
	return map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
		},
	}
}
