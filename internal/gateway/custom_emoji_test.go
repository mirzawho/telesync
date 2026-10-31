package gateway_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
)

func TestCustomEmojiStreamsTheEmojiByDocumentID(t *testing.T) {
	official := readyWorker()
	official.customEmoji = rpc.MediaFile{
		MimeType: "application/x-tgsticker",
		Data:     []byte("emoji-bytes"),
	}
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/custom_emoji/5368324170671202286")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if string(body) != "emoji-bytes" {
		t.Fatalf("body = %q", body)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/x-tgsticker" {
		t.Fatalf("content type = %q", got)
	}
	if official.lastEmojiID != 5368324170671202286 {
		t.Fatalf("document id = %d", official.lastEmojiID)
	}
}

func TestCustomEmojiMissingIsNotFound(t *testing.T) {
	official := readyWorker()
	official.customEmojiE = rpc.NewError(rpc.CodeNotFound, "This custom emoji is not available.", 0)
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/custom_emoji/42")
	body := decode[errorEnvelope](t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if body.Error.Code != rpc.CodeNotFound {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if official.lastEmojiID != 42 {
		t.Fatalf("document id = %d", official.lastEmojiID)
	}
}

func TestCustomEmojiRejectsABadID(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})

	for _, path := range []string{
		"/api/v1/telegram/official/custom_emoji/0",
		"/api/v1/telegram/official/custom_emoji/-3",
		"/api/v1/telegram/official/custom_emoji/abc",
	} {
		resp := env.get(t, path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", path, resp.StatusCode)
		}
	}
	if official.callCount() != 0 {
		t.Fatal("a bad id must not reach the worker")
	}
}
