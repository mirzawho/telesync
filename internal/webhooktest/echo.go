package webhooktest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"
)

// How a body is represented in a record.
const (
	// EncodingUTF8 means the body is valid UTF-8 and is reported as text.
	EncodingUTF8 = "utf-8"
	// EncodingBase64 means the body is not text and is reported encoded, so
	// nothing is corrupted by a conversion it never survived.
	EncodingBase64 = "base64"
)

// RequestRecord is one received request as it arrived.
//
// Every field is what the sender put on the wire. Nothing is normalized,
// rewritten or redacted: the whole point of this receiver is to see the
// request Telesync actually made, authentication headers included.
type RequestRecord struct {
	Method        string              `json:"method"`
	Path          string              `json:"path"`
	URI           string              `json:"uri"`
	Query         map[string][]string `json:"query"`
	Protocol      string              `json:"protocol"`
	Host          string              `json:"host"`
	RemoteAddr    string              `json:"remote_addr"`
	Headers       map[string][]string `json:"headers"`
	ContentType   string              `json:"content_type,omitempty"`
	ContentLength *int64              `json:"content_length,omitempty"`
	BodySize      int                 `json:"body_size"`
	// BodyTruncated says the body was larger than this receiver reads, so what
	// is reported is a count rather than content that would be incomplete.
	BodyTruncated bool   `json:"body_truncated,omitempty"`
	BodyEncoding  string `json:"body_encoding"`
	// Body is the parsed JSON value, when the body was valid JSON.
	Body json.RawMessage `json:"body,omitempty"`
	// BodyText is the body exactly as it arrived, when it is valid UTF-8.
	BodyText string `json:"body_text,omitempty"`
	// BodyBase64 is the body exactly as it arrived, when it is not text.
	BodyBase64 string `json:"body_base64,omitempty"`
}

// Describe renders one request and its body as a record. The moment it was
// received belongs to the response around it, not to the request itself.
func Describe(r *http.Request, body []byte, truncated bool) RequestRecord {
	rec := RequestRecord{
		Method:        r.Method,
		Path:          r.URL.Path,
		URI:           r.URL.RequestURI(),
		Query:         r.URL.Query(),
		Protocol:      r.Proto,
		Host:          r.Host,
		RemoteAddr:    r.RemoteAddr,
		Headers:       make(map[string][]string, len(r.Header)),
		ContentType:   mediaType(r.Header.Get("Content-Type")),
		BodySize:      len(body),
		BodyTruncated: truncated,
		BodyEncoding:  EncodingUTF8,
		BodyText:      "",
		BodyBase64:    "",
		Body:          nil,
		ContentLength: nil,
	}
	for name, values := range r.Header {
		rec.Headers[name] = append([]string(nil), values...)
	}
	if r.ContentLength >= 0 {
		length := r.ContentLength
		rec.ContentLength = &length
	}

	if truncated {
		return rec
	}

	// Text is reported as text and anything else encoded, so a binary payload
	// round-trips through the record unchanged instead of surviving a
	// conversion into UTF-8 that it never made.
	if !utf8.Valid(body) {
		rec.BodyEncoding = EncodingBase64
		rec.BodyBase64 = base64.StdEncoding.EncodeToString(body)
		return rec
	}
	rec.BodyText = string(body)
	if json.Valid(body) {
		rec.Body = json.RawMessage(append([]byte(nil), body...))
	}
	return rec
}

// mediaType is the type and subtype of a Content-Type, without its parameters.
// The header's own verbatim value is in the record's Headers; this is the
// answer to "what kind of body is this".
func mediaType(contentType string) string {
	name, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(name))
}
