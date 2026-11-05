package gateway_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
)

// Source media belongs to the connection that recorded the source message, not
// to whichever connection the caller chose as the destination.
//
// A publish reads the source message's attachment through the source client and
// sends the bytes through the destination client. The two are different
// accounts with different sessions, different peer stores and different
// Telegram view of the world: a channel id from one means nothing to the other,
// and a file reference from one is not a capability on the other. These tests
// pin that split, because the one thing that must never happen is the
// destination being asked to read the source.

// The media modes a publish request names. They are the wire values, written
// out here rather than imported: this suite drives the HTTP contract, and a
// renamed constant in the gateway must not silently rewrite what a client sends.
const (
	mediaModePreserve = "preserve_source"
	mediaModeOmit     = "omit"
)

// publishCrossEnv wires two distinct connections: "official" owns the source
// channel and its photo, "cat" is a separate destination the caller publishes
// to. The source worker refuses nothing; the destination serves no media at
// all, so any media read that reaches it is visible as a failure.
func publishCrossEnv(t *testing.T) (env *accountEnv, source, dest *fakeWorker, row channelstore.Channel) {
	t.Helper()
	source = readyWorker()
	source.health.Key = "official"
	source.message = rpc.MessagePage{
		Channel:  rpc.Channel{ID: 555, Title: "Source"},
		Messages: []rpc.Message{{ID: 9, Text: "hello"}},
		Page:     rpc.PageInfo{Page: 1, PerPage: 1, Total: 1},
	}
	source.mediaBytes = []byte{0xff, 0xd8, 0xff, 0xe0}
	source.mediaType = "image/jpeg"
	source.mediaName = "photo.jpg"
	source.mediaSize = int64(len(source.mediaBytes))

	dest = readyWorker()
	dest.health.Key = "cat"
	dest.health.Type = "custom"
	dest.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "999", Type: "channel", Title: "کت فیلم",
		IsMember: true, Permissions: rpc.Permissions{New: true, Read: true},
	}}
	dest.send = rpc.SendResult{
		Message:   &rpc.Message{ID: 12345, Text: "outgoing"},
		ChannelID: "999",
	}
	// The destination holds no media for anyone: if it is ever asked to read a
	// source attachment the request fails, which is what makes the split
	// visible rather than merely intended.
	dest.mediaE = rpc.ErrChannelNotFound

	env = newAccountEnv(t, registry{"official": source, "cat": dest})
	env.signedIn = true
	row = env.channelRow(t, "official", 555)
	return env, source, dest, row
}

// publishCrossBody publishes the source message's photo to a channel on "cat".
func publishCrossBody(localID int64, mode, attachmentID string) string {
	media := fmt.Sprintf(`"mode": %q`, mode)
	if attachmentID != "" {
		media += fmt.Sprintf(`, "attachment_id": %q`, attachmentID)
	}
	return fmt.Sprintf(`{
		"source": {"local_channel_id": %d, "message_id": 9},
		"destination": {"channel_id": "999"},
		"content": {"text": "hello", "entities": []},
		"media": {%s},
		"options": {"silent": false, "disable_web_page_preview": false}
	}`, localID, media)
}

// withSourcePhoto gives the source message an attachment identified the way the
// worker writes one: "channel:message", opaque and only ever compared. The
// filename is what Telegram reported from the sender, which is the name the
// outgoing attachment must carry.
func withSourcePhoto(source *fakeWorker) {
	source.message.Messages = []rpc.Message{{
		ID: 9, Text: "hello",
		Media: &rpc.Media{ID: "555:9", Type: "photo", Label: "عکس", Filename: "photo.jpg",
			MimeType: "image/jpeg", Size: int64(len(source.mediaBytes))},
	}}
}

// The whole point: the attachment is read where it lives and sent where it is
// going. The destination must never be asked for the source's bytes.
func TestPublishReadsSourceMediaThroughTheSourceConnectionNotTheDestination(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(source)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, "555:9"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}

	if source.mediaCalls != 1 {
		t.Fatalf("source media reads = %d, want 1", source.mediaCalls)
	}
	if source.lastMediaChannel != 555 || source.lastMediaMessage != 9 {
		t.Fatalf("source read channel:message = %d:%d, want 555:9",
			source.lastMediaChannel, source.lastMediaMessage)
	}
	if dest.mediaCalls != 0 {
		t.Fatalf("destination media reads = %d, want 0 — the source attachment is not the destination's to read", dest.mediaCalls)
	}
	if source.sendCalls != 0 {
		t.Fatalf("source sends = %d, want 0 — a send belongs to the destination connection", source.sendCalls)
	}
	if dest.sendCalls != 1 {
		t.Fatalf("destination sends = %d, want 1", dest.sendCalls)
	}
	if dest.sentReq.ChannelID != 999 {
		t.Fatalf("sent to channel = %d, want 999", dest.sentReq.ChannelID)
	}
	if dest.sentReq.Media == nil {
		t.Fatal("the source attachment must go out with the message")
	}
	if got := string(dest.sentReq.Media.Data); got != string(source.mediaBytes) {
		t.Fatalf("sent bytes = %q, want the source's %q", got, source.mediaBytes)
	}
	if dest.sentReq.Media.Kind != "photo" {
		t.Fatalf("sent kind = %q, want photo", dest.sentReq.Media.Kind)
	}
	if dest.sentReq.Media.MimeType != "image/jpeg" {
		t.Fatalf("sent mime = %q, want image/jpeg", dest.sentReq.Media.MimeType)
	}
	if dest.sentReq.Media.Name != "photo.jpg" {
		t.Fatalf("sent name = %q, want the sender's photo.jpg", dest.sentReq.Media.Name)
	}
}

// Choosing another destination must not change whose attachment this is or how
// it is read. The source connection is named by the source record alone.
func TestPublishReadsTheSameSourceMediaWhoeverTheDestinationIs(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(source)
	// "official" is a valid destination too, so the same source is published
	// once to another connection and once to its own.
	source.channel = dest.channel
	source.send = dest.send

	for _, path := range []string{"/api/v1/telegram/cat/messages", "/api/v1/telegram/official/messages"} {
		before := source.mediaCalls
		resp := env.send(t, defaultCaller, http.MethodPost, path,
			publishCrossBody(row.ID, mediaModePreserve, "555:9"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", path, resp.StatusCode, readBody(t, resp))
		}
		if source.mediaCalls != before+1 {
			t.Fatalf("%s: source media reads = %d, want %d", path, source.mediaCalls, before+1)
		}
		if source.lastMediaChannel != 555 || source.lastMediaMessage != 9 {
			t.Fatalf("%s: source read channel:message = %d:%d, want 555:9",
				path, source.lastMediaChannel, source.lastMediaMessage)
		}
		if dest.mediaCalls != 0 {
			t.Fatalf("%s: the destination read source media %d time(s)", path, dest.mediaCalls)
		}
	}
}

// A source attachment that cannot be read is a source failure and nothing else:
// no message is sent, certainly not a text-only one that drops the photo.
func TestPublishRefusesToSendTextAloneWhenTheSourceAttachmentCannotBeRead(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(source)
	source.mediaE = rpc.NewError(rpc.CodeNotFound, "gone", 0)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, "555:9"))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if code := errorCode(t, resp); code != "publish_source_media_unavailable" {
		t.Fatalf("code = %q, want publish_source_media_unavailable", code)
	}
	if dest.sendCalls != 0 {
		t.Fatalf("destination sends = %d, want 0 — a draft that needs its attachment is not published without it", dest.sendCalls)
	}
	if source.sendCalls != 0 {
		t.Fatalf("source sends = %d, want 0", source.sendCalls)
	}
}

// The connection that owns the source is named by the source record. When it is
// not configured there is no substitute, and the answer says so rather than
// reaching for the destination.
func TestPublishRefusesWhenTheSourceConnectionIsUnavailable(t *testing.T) {
	env, _, dest, _ := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	// The source record names a client the configuration no longer has.
	row := env.channelRow(t, "gone-client", 555)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, "555:9"))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if dest.sendCalls != 0 {
		t.Fatalf("destination sends = %d, want 0", dest.sendCalls)
	}
	if dest.mediaCalls != 0 {
		t.Fatalf("destination media reads = %d, want 0 — an unknown source connection is never stood in for", dest.mediaCalls)
	}
}

// A destination that refuses the message is a destination failure. It must not
// be reported as the source attachment being unreadable.
func TestPublishReportsADestinationFailureSeparatelyFromASourceReadFailure(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(source)
	dest.sendE = rpc.NewError(rpc.CodeSendForbidden, "not a channel admin", 0)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, "555:9"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", resp.StatusCode, readBody(t, resp))
	}
	code := errorCode(t, resp)
	if code == "publish_source_media_unavailable" {
		t.Fatal("a destination refusal was reported as an unreadable source attachment")
	}
	if code != "publish_permission_denied" {
		t.Fatalf("code = %q, want publish_permission_denied", code)
	}
	// The source attachment was read successfully before the send was refused.
	if source.mediaCalls != 1 {
		t.Fatalf("source media reads = %d, want 1", source.mediaCalls)
	}
}

// An attachment the source message does not carry is never sent as one, and a// request that names another message's attachment is refused before any read.
func TestPublishOmitsMediaOnlyWhenTheSourceMessageHasNone(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	// No media on the source message at all.
	dest.sendCalls = 0

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if source.mediaCalls != 0 {
		t.Fatalf("source media reads = %d, want 0 — a message with no attachment has nothing to read", source.mediaCalls)
	}
	if dest.sentReq.Media != nil {
		t.Fatalf("sent media = %+v, want none", dest.sentReq.Media)
	}
}

// An explicit "omit" is the caller's own decision: nothing is read and the text
// goes out alone. That is the only way a post leaves without its attachment.
func TestPublishHonoursAnExplicitDetachWithoutReadingTheSourceMedia(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(source)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModeOmit, ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if source.mediaCalls != 0 {
		t.Fatalf("source media reads = %d, want 0 for an explicit omit", source.mediaCalls)
	}
	if dest.sentReq.Media != nil {
		t.Fatalf("sent media = %+v, want none", dest.sentReq.Media)
	}
	if dest.sentReq.Text != "hello" {
		t.Fatalf("sent text = %q", dest.sentReq.Text)
	}
}

// A worker answers a failed send in two shapes: the sentinel an in-process
// backend returns, and the coded error one reached over the socket arrives as.
// Both name the same stage and must read the same way to a caller.
func TestPublishClassifiesASendFailureTheSameHoweverTheWorkerReportedIt(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
	}{
		{"sentinel", rpc.ErrSendForbidden},
		{"coded", rpc.NewError(rpc.CodeSendForbidden, "not a channel admin", 0)},
	} {
		env, source, dest, row := publishCrossEnv(t)
		grantPublish(t, env, env.defaults)
		withSourcePhoto(source)
		dest.sendE = failure.err

		resp := env.send(t, defaultCaller, http.MethodPost,
			"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, "555:9"))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403, body = %s", failure.name, resp.StatusCode, readBody(t, resp))
		}
		if code := errorCode(t, resp); code != "publish_permission_denied" {
			t.Fatalf("%s: code = %q, want publish_permission_denied", failure.name, code)
		}
	}
}

// A post carrying an attachment that the destination will not take is a
// destination failure. The source read already succeeded, so the two stages are
// never interchangeable — and an error with no code of its own is not left as
// an anonymous "publish failed" when it plainly died delivering media.
func TestPublishNamesAFailedMediaSendAsADestinationFailure(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(source)
	dest.sendE = errors.New("the upload died halfway")

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, "555:9"))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", resp.StatusCode, readBody(t, resp))
	}
	code := errorCode(t, resp)
	if code != "publish_media_upload_failed" {
		t.Fatalf("code = %q, want publish_media_upload_failed", code)
	}
	if code == "publish_source_media_unavailable" {
		t.Fatal("a failed destination send was reported as an unreadable source attachment")
	}
	// The attachment was read before the send; that much worked and must not be
	// undone or retried behind the caller's back.
	if source.mediaCalls != 1 {
		t.Fatalf("source media reads = %d, want 1", source.mediaCalls)
	}
}

// --- WordPress ---

// The WordPress route reads the source attachment the same way: through the
// connection that owns it. Its destination is a site, not a Telegram client, so
// there is no second Telegram connection to confuse it with — but the worker
// still has to be the source's own.
func TestWordPressPublishReadsSourceMediaThroughTheSourceConnection(t *testing.T) {
	env, client, row, worker := wpPublishEnvWorker(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(worker)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/local/wordpress/1/posts", wpRequest(row.ID, 9, "Title"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if worker.mediaCalls != 1 {
		t.Fatalf("source media reads = %d, want 1", worker.mediaCalls)
	}
	if worker.lastMediaChannel != 555 || worker.lastMediaMessage != 9 {
		t.Fatalf("source read channel:message = %d:%d, want 555:9",
			worker.lastMediaChannel, worker.lastMediaMessage)
	}
	if worker.lastMediaIndex != 0 {
		t.Fatalf("photo size = %d, want 0 (the largest)", worker.lastMediaIndex)
	}
	if client.uploadsCreated != 1 {
		t.Fatalf("site uploads = %d, want 1", client.uploadsCreated)
	}
	if client.gotUploadName != "photo.jpg" {
		t.Fatalf("uploaded name = %q, want the sender's photo.jpg", client.gotUploadName)
	}
}

// An unreadable source attachment stops the WordPress post too: a post whose
// featured image could not be read is not published without one.
func TestWordPressPublishRefusesWhenTheSourceAttachmentCannotBeRead(t *testing.T) {
	env, client, row, worker := wpPublishEnvWorker(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(worker)
	worker.mediaE = rpc.NewError(rpc.CodeNotFound, "gone", 0)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/local/wordpress/1/posts", wpRequest(row.ID, 9, "Title"))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if code := errorCode(t, resp); code != "publish_source_media_unavailable" {
		t.Fatalf("code = %q, want publish_source_media_unavailable", code)
	}
	if client.postsCreated != 0 {
		t.Fatalf("posts created = %d, want 0 — the post waits for its attachment", client.postsCreated)
	}
	if client.uploadsCreated != 0 {
		t.Fatalf("uploads = %d, want 0", client.uploadsCreated)
	}
}

// The WordPress route resolves its source worker the same way the Telegram one
// does: the connection named by the source record, and no other. A source
// record naming a client the configuration no longer has is an answer — 503
// with nothing written to the site — and never a nil worker or a panic.
func TestWordPressPublishReportsAnUnavailableSourceConnectionWithoutPanicking(t *testing.T) {
	env, client, _, worker := wpPublishEnvWorker(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(worker)
	row := env.channelRow(t, "gone-client", 555)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/local/wordpress/1/posts", wpRequest(row.ID, 9, "Title"))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if client.postsCreated != 0 {
		t.Fatalf("posts created = %d, want 0", client.postsCreated)
	}
	if client.uploadsCreated != 0 {
		t.Fatalf("uploads = %d, want 0", client.uploadsCreated)
	}
}

// The media id is opaque. A publish compares it with the source's own id; it
// never reads a channel or a message out of it, and it never hands it to the
// destination as a location.
func TestPublishNeverUsesTheDestinationConnectionToResolveAMediaID(t *testing.T) {
	env, source, dest, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(source)

	// The id names a different message entirely. It is refused on identity,
	// and the destination is not consulted to find out what it is.
	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, "555:77"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", resp.StatusCode, readBody(t, resp))
	}
	if dest.mediaCalls != 0 {
		t.Fatalf("destination media reads = %d, want 0", dest.mediaCalls)
	}
	if source.mediaCalls != 0 {
		t.Fatalf("source media reads = %d, want 0 — a refused attachment is never fetched", source.mediaCalls)
	}
	if dest.sendCalls != 0 {
		t.Fatalf("destination sends = %d, want 0", dest.sendCalls)
	}
}

// A source media failure is logged against the connection that owns it, so the
// request id in the answer can be followed to the failing stage.
func TestPublishLogsTheSourceConnectionWhenTheAttachmentCannotBeRead(t *testing.T) {
	env, source, _, row := publishCrossEnv(t)
	grantPublish(t, env, env.defaults)
	withSourcePhoto(source)
	source.mediaE = rpc.NewError(rpc.CodeNotFound, "gone", 0)

	var logged strings.Builder
	cfg := env.handlerConfig()
	cfg.Logf = func(format string, args ...any) {
		fmt.Fprintf(&logged, format+"\n", args...)
	}
	env.useConfig(cfg)

	resp := env.send(t, defaultCaller, http.MethodPost,
		"/api/v1/telegram/cat/messages", publishCrossBody(row.ID, mediaModePreserve, "555:9"))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	line := logged.String()
	if !strings.Contains(line, "client=official") {
		t.Fatalf("log names no source connection:\n%s", line)
	}
	if !strings.Contains(line, "message=9") {
		t.Fatalf("log names no source message:\n%s", line)
	}
	if strings.Contains(line, "client=cat") {
		t.Fatalf("log blames the destination for a source read:\n%s", line)
	}
}
