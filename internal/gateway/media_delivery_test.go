package gateway_test

import (
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
)

// The media route is the one place the public API sends bytes rather than JSON.
// Everything a browser needs to play an attachment or to save one is decided
// here: the content type, the name the sender gave the file, and — for a
// request that asks for part of the file — exactly which bytes and which
// status.

func mediaEnv(t *testing.T, name string, mime string, data []byte) (*testEnv, *fakeWorker) {
	t.Helper()
	official := readyWorker()
	official.mediaBytes = data
	official.mediaName = name
	official.mediaType = mime
	official.mediaSize = int64(len(data))
	return newEnv(t, registry{"cat": official}), official
}

func TestMediaEndpointServesAByteRange(t *testing.T) {
	env, _ := mediaEnv(t, "track-01.mp3", "audio/mpeg", []byte("0123456789"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media", "Range", "bytes=2-5")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (body %s)", resp.StatusCode, body)
	}
	if body != "2345" {
		t.Fatalf("body = %q, want %q", body, "2345")
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("content-range = %q", got)
	}
	if got := resp.Header.Get("Content-Length"); got != "4" {
		t.Fatalf("content-length = %q", got)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("accept-ranges = %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("content-type = %q", got)
	}
}

func TestMediaEndpointServesAnOpenEndedRange(t *testing.T) {
	env, _ := mediaEnv(t, "track-01.mp3", "audio/mpeg", []byte("0123456789"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media", "Range", "bytes=6-")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if body != "6789" {
		t.Fatalf("body = %q, want %q", body, "6789")
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 6-9/10" {
		t.Fatalf("content-range = %q", got)
	}
}

func TestMediaEndpointServesASuffixRange(t *testing.T) {
	env, _ := mediaEnv(t, "track-01.mp3", "audio/mpeg", []byte("0123456789"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media", "Range", "bytes=-3")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (body %s)", resp.StatusCode, body)
	}
	if body != "789" {
		t.Fatalf("body = %q, want %q", body, "789")
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 7-9/10" {
		t.Fatalf("content-range = %q", got)
	}
}

func TestMediaEndpointClampsARangePastTheEnd(t *testing.T) {
	env, _ := mediaEnv(t, "track-01.mp3", "audio/mpeg", []byte("0123456789"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media", "Range", "bytes=8-99")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if body != "89" {
		t.Fatalf("body = %q, want %q", body, "89")
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 8-9/10" {
		t.Fatalf("content-range = %q", got)
	}
}

func TestMediaEndpointRejectsAnUnsatisfiableRange(t *testing.T) {
	env, _ := mediaEnv(t, "track-01.mp3", "audio/mpeg", []byte("0123456789"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media", "Range", "bytes=10-")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416 (body %s)", resp.StatusCode, body)
	}
	// The answer must say how long the file really is, or a client cannot
	// recover from asking past the end.
	if got := resp.Header.Get("Content-Range"); got != "bytes */10" {
		t.Fatalf("content-range = %q", got)
	}
}

func TestMediaEndpointIgnoresAnUnparseableRange(t *testing.T) {
	// RFC 9110: a Range header that is not a valid byte range is ignored, and
	// the whole representation is sent. It is not an error.
	env, _ := mediaEnv(t, "track-01.mp3", "audio/mpeg", []byte("0123456789"))

	for _, header := range []string{"bytes=abc", "items=0-1", "bytes=", "bytes=5-2", "bytes=0-1,4-5"} {
		resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/24/media", "Range", header)
		body := readAll(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%q: status = %d, want 200 (body %s)", header, resp.StatusCode, body)
		}
		if body != "0123456789" {
			t.Errorf("%q: body = %q, want the whole file", header, body)
		}
	}
}

func TestMediaEndpointNamesTheDownload(t *testing.T) {
	env, _ := mediaEnv(t, "original-app-name.apk",
		"application/vnd.android.package-archive", []byte("apk"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/36/media")
	readAll(t, resp)

	disposition := resp.Header.Get("Content-Disposition")
	if disposition == "" {
		t.Fatal("a download must name the file it is sending")
	}
	kind, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		t.Fatalf("content-disposition %q: %v", disposition, err)
	}
	if kind != "attachment" {
		t.Fatalf("disposition = %q, want attachment", kind)
	}
	if params["filename"] != "original-app-name.apk" {
		t.Fatalf("filename = %q, want %q (header %q)",
			params["filename"], "original-app-name.apk", disposition)
	}
}

func TestMediaEndpointEncodesANonASCIINameSafely(t *testing.T) {
	// A Persian name has no plain ASCII form. It must survive the header as an
	// RFC 5987 parameter and still come back byte-for-byte identical.
	const name = "گزارش سالانه.pdf"
	env, _ := mediaEnv(t, name, "application/pdf", []byte("pdf"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/36/media")
	readAll(t, resp)

	disposition := resp.Header.Get("Content-Disposition")
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		t.Fatalf("content-disposition %q: %v", disposition, err)
	}
	if params["filename"] != name {
		t.Fatalf("filename = %q, want %q (header %q)", params["filename"], name, disposition)
	}
	// The header itself must stay printable ASCII; raw UTF-8 would be mangled
	// by any hop that is not byte-transparent.
	for i := 0; i < len(disposition); i++ {
		if disposition[i] > 127 {
			t.Fatalf("content-disposition carries raw non-ASCII: %q", disposition)
		}
	}
}

func TestMediaEndpointWithoutANameSendsNoFilename(t *testing.T) {
	// Telegram never named this one. Naming it here would be inventing a
	// filename the sender never chose.
	env, official := mediaEnv(t, "", "image/jpeg", []byte("jpeg"))
	official.mediaName = ""

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/179/media")
	readAll(t, resp)

	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("content-disposition: %v", err)
	}
	if params["filename"] != "" {
		t.Fatalf("filename = %q, want none", params["filename"])
	}
}

func TestMediaEndpointAsksTheWorkerOnlyForTheRequestedSlice(t *testing.T) {
	// The whole point of a range is that the rest of the file is never
	// transferred. If the gateway asked the worker for everything and threw the
	// surplus away, a seek on a 60 MB document would cost 60 MB every time.
	env, official := mediaEnv(t, "app.apk", "application/octet-stream", []byte("0123456789"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/36/media", "Range", "bytes=4-5")
	readAll(t, resp)

	official.mu.Lock()
	got := official.lastMediaSlice
	official.mu.Unlock()
	if got.Offset != 4 || got.Limit != 2 {
		t.Fatalf("worker was asked for %+v, want offset 4 limit 2", got)
	}
}

func TestMediaEndpointStreamsWholeFiles(t *testing.T) {
	env, official := mediaEnv(t, "app.apk", "application/octet-stream", []byte("0123456789"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/36/media")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body != "0123456789" {
		t.Fatalf("body = %q", body)
	}
	if got := resp.Header.Get("Content-Length"); got != "10" {
		t.Fatalf("content-length = %q, want 10", got)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("accept-ranges = %q", got)
	}
	official.mu.Lock()
	got := official.lastMediaSlice
	official.mu.Unlock()
	if got.Offset != 0 || got.Limit != 0 {
		t.Fatalf("worker was asked for %+v, want the whole file", got)
	}
}

func TestMediaEndpointDoesNotAdvertiseRangesItCannotServe(t *testing.T) {
	// When the backend reports no size there is no length to slice against.
	// Advertising ranges anyway would be a promise the route cannot keep.
	env, official := mediaEnv(t, "", "image/jpeg", []byte("jpeg"))
	official.mediaSize = 0

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/179/media", "Range", "bytes=1-2")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body != "jpeg" {
		t.Fatalf("body = %q, want the whole attachment", body)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "none" {
		t.Fatalf("accept-ranges = %q, want none", got)
	}
	if got := resp.Header.Get("Content-Range"); got != "" {
		t.Fatalf("content-range = %q, want none", got)
	}
}

func TestMediaEndpointHeadersDoNotLeakInternals(t *testing.T) {
	env, _ := mediaEnv(t, "app.apk", "application/octet-stream", []byte("x"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/36/media")
	readAll(t, resp)

	for _, header := range []string{
		"X-Telesync-Size", "X-Telesync-Offset", "X-Telesync-Filename",
		"X-Worker", "X-Backend",
	} {
		if got := resp.Header.Get(header); got != "" {
			t.Errorf("internal header %s leaked to the public API: %q", header, got)
		}
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("x-content-type-options = %q, want nosniff", got)
	}
}

func TestMediaEndpointRejectsANegativeRange(t *testing.T) {
	env, _ := mediaEnv(t, "app.apk", "application/octet-stream", []byte("0123456789"))

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages/36/media", "Range", "bytes=-0")
	readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a zero-length suffix", resp.StatusCode)
	}
}

// TestMediaDescriptorReportsTheSizeOfEveryDownloadableAttachment pins that a
// client can decide whether a range is satisfiable before asking: without a
// size there is no way to answer "the last 500 bytes".
func TestMediaDescriptorReportsTheSizeOfEveryDownloadableAttachment(t *testing.T) {
	official := readyWorker()
	official.messages = rpc.MessagePage{
		Channel: rpc.Channel{ID: 6, Title: "cat"},
		Messages: []rpc.Message{
			{ID: 36, Media: &rpc.Media{ID: "6:36", Type: "document", Size: 59797754}},
		},
	}
	env := newEnv(t, registry{"cat": official})

	resp := env.get(t, "/api/v1/telegram/cat/channels/6/messages?page=1&per_page=20")
	var body struct {
		Data struct {
			Messages []struct {
				Media struct {
					Size int64 `json:"size"`
				} `json:"media"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.NewDecoder(strings.NewReader(readAll(t, resp))).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got := body.Data.Messages[0].Media.Size; got != 59797754 {
		t.Fatalf("size = %d, want 59797754", got)
	}
}
