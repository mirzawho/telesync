package gateway

import (
	"fmt"
	"mime"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/rpc"
)

// This file holds the HTTP meaning of the media route: what a Range header asks
// for and what a download is called. Both belong to the public contract, so
// both live here rather than in the worker, which only ever deals in offsets.

// rangeKind is what a Range header asks for once it has been read.
type rangeKind int

const (
	// rangeWhole means no usable range was named: the whole attachment is sent.
	rangeWhole rangeKind = iota
	// rangeSlice means the caller wants one run of bytes of the attachment.
	rangeSlice
)

// byteRange is one requested run of bytes of an attachment.
type byteRange struct {
	// start is the attachment offset of the first byte wanted.
	start int64
	// limit is the most bytes wanted, zero for "everything from start on".
	limit int64
	// tail is the number of bytes wanted from the attachment's end, for a
	// suffix range. Greater than zero means start and limit are not used.
	tail int64
}

// parseRange reads one Range header and reports what it asks for.
//
// It follows RFC 9110 §14.1: only the "bytes" unit is understood, only one
// range is served at a time, and a header that is not a valid byte range is
// ignored rather than treated as an error. Whether the named bytes actually
// exist is a separate question, answered once the attachment's real size is
// known — see unsatisfiableRange.
func parseRange(header string) (byteRange, rangeKind) {
	var none byteRange
	unit, rest, ok := strings.Cut(strings.TrimSpace(header), "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return none, rangeWhole
	}
	if strings.Contains(rest, ",") {
		// Several ranges would need a multipart answer. Rather than send one
		// part of a multipart body, the whole attachment is sent: that is what
		// a server that ignores the header does.
		return none, rangeWhole
	}

	spec := strings.TrimSpace(rest)
	if strings.HasPrefix(spec, "-") {
		length, err := strconv.ParseInt(spec[1:], 10, 64)
		if err != nil || length <= 0 {
			return none, rangeWhole
		}
		return byteRange{tail: length}, rangeSlice
	}

	firstText, lastText, hasLast := strings.Cut(spec, "-")
	first, err := strconv.ParseInt(strings.TrimSpace(firstText), 10, 64)
	if err != nil || first < 0 {
		return none, rangeWhole
	}
	lastText = strings.TrimSpace(lastText)
	if !hasLast || lastText == "" {
		// "bytes=5-" is the run from byte 5 to the end of the attachment.
		return byteRange{start: first}, rangeSlice
	}
	last, err := strconv.ParseInt(lastText, 10, 64)
	if err != nil || last < 0 {
		return none, rangeWhole
	}
	if last < first {
		// An empty run is not a valid byte range spec; the header is ignored.
		return none, rangeWhole
	}
	return byteRange{start: first, limit: last - first + 1}, rangeSlice
}

// unsatisfiableRange reports whether a requested slice has no bytes to serve.
//
// It is deliberately separate from parseRange: satisfiability is a fact about
// the attachment's real length, which only the worker knows. length is what the
// worker actually returned for the request.
func unsatisfiableRange(size, length int64) bool {
	if size <= 0 {
		// Without a size there is nothing to be unsatisfiable against, and
		// nothing to report in Content-Range either.
		return false
	}
	if length > 0 {
		return false
	}
	// No bytes came back for a requested slice. For a suffix that can only mean
	// there was nothing to give, and a range with nothing to give is
	// unsatisfiable like any other.
	return true
}

// contentDisposition renders the header that names a download.
//
// The sender's name is carried twice: a plain ASCII parameter for old clients,
// and an RFC 5987 parameter that carries the name byte-for-byte whatever its
// script. A name with no ASCII form at all leaves the plain parameter out
// rather than being mangled into a lookalike. With no name at all the header
// still says "attachment", so a browser saves the bytes instead of trying to
// render them.
func contentDisposition(filename string) string {
	if filename == "" {
		return "attachment"
	}
	params := map[string]string{}
	if isASCII(filename) {
		params["filename"] = filename
	}
	return mime.FormatMediaType("attachment", params) + rfc5987("filename", filename)
}

// rfc5987 renders "; filename*=UTF-8”…" carrying the name verbatim.
func rfc5987(param, value string) string {
	var b strings.Builder
	b.WriteString("; ")
	b.WriteString(param)
	b.WriteString("*=UTF-8''")
	for i := 0; i < len(value); i++ {
		c := value[i]
		// attr-char from RFC 5987: everything else is percent-encoded.
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '!' || c == '#' || c == '$' || c == '&' || c == '+' || c == '-' ||
			c == '.' || c == '^' || c == '_' || c == '`' || c == '|' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// isASCII reports whether a value can be written into a header as-is.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// mediaSliceOf turns a parsed range into the slice the worker fetches.
func mediaSliceOf(r byteRange) rpc.MediaSlice {
	return rpc.MediaSlice{Offset: r.start, Limit: r.limit, Tail: r.tail}
}
