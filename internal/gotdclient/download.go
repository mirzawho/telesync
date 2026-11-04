package gotdclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/tgapi"
)

// mediaPart is how many bytes one upload.getFile request asks for.
//
// Telegram caps a request at 1 MiB and wants 4 KiB multiples unless the precise
// flag is set. 512 KiB is the part size gotd's own downloader uses, and it is
// small enough that a large attachment never accumulates in memory.
const mediaPart = 512 << 10

// maxSmallFileBytes bounds the downloads this adapter still buffers whole:
// custom emoji and channel avatars. Both are small images, and callers want the
// bytes in hand rather than a stream. Message attachments are not among them —
// those stream, see mediaReader.
const maxSmallFileBytes = 32 << 20

// limitedWriter fails once more than limit bytes have been written.
type limitedWriter struct {
	w     *bytes.Buffer
	limit int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.w.Len()+len(p) > l.limit {
		return 0, fmt.Errorf("attachment exceeds the %d byte limit", l.limit)
	}
	return l.w.Write(p)
}

// DownloadMedia streams one attachment, or one slice of it.
//
// The bytes are pulled from Telegram a part at a time as the caller reads, so a
// 60 MB document never lands in this process as a single buffer. It is
// read-only: fetching a photo or a document has no effect on the account's read
// state. A message without an attachment is ErrMediaNotFound.
func (m *Manager) DownloadMedia(ctx context.Context, channel tgapi.Channel, messageID int, slice tgapi.MediaSlice) (tgapi.MediaStream, error) {
	msg, err := m.channelMessage(ctx, channel, messageID)
	if err != nil {
		if errors.Is(err, tgapi.ErrMessageNotFound) {
			return tgapi.MediaStream{}, tgapi.ErrMediaNotFound
		}
		return tgapi.MediaStream{}, err
	}
	loc, mimeType, err := mediaLocation(msg.Media)
	if err != nil {
		return tgapi.MediaStream{}, err
	}

	out := tgapi.MediaStream{
		MimeType: mimeType,
		Filename: MediaFileName(msg.Media),
		Size:     MediaSize(msg.Media),
	}

	offset, limit := slice.Offset, slice.Limit
	// A suffix is only meaningful against a size. When the backend reported
	// none, the only slice this adapter can honestly return is the whole
	// attachment: saying "the last N bytes" would be a guess.
	if slice.Tail > 0 {
		if out.Size == 0 {
			offset, limit = 0, 0
		} else {
			tail := slice.Tail
			if tail > out.Size {
				tail = out.Size
			}
			offset, limit = out.Size-tail, tail
		}
	}
	if offset < 0 {
		offset = 0
	}
	// Without a size there is no length to resolve a slice against. The only
	// honest answer is the whole attachment: handing over a fragment and
	// calling it the file would silently truncate it.
	if out.Size == 0 {
		offset, limit = 0, 0
	}

	if out.Size > 0 && offset >= out.Size {
		// The caller asked past the end. Nothing is fetched from Telegram: the
		// empty body is how the caller learns the range is unsatisfiable.
		out.Offset = out.Size
		out.Body = io.NopCloser(strings.NewReader(""))
		return out, nil
	}

	// end is the offset to stop at, exclusive, or negative for "to the end of
	// the attachment" when no size is known.
	end := int64(-1)
	switch {
	case limit > 0:
		end = offset + limit
		if out.Size > 0 && end > out.Size {
			end = out.Size
		}
	case out.Size > 0:
		end = out.Size
	}
	if end >= 0 {
		out.Length = end - offset
	}
	out.Offset = offset

	fetcher := &mediaFetcher{manager: m, ctx: ctx, channel: channel, messageID: messageID, loc: loc}
	out.Body = &mediaReader{ctx: ctx, fetch: fetcher.part, offset: offset, end: end}
	return out, nil
}

// mediaReader hands out attachment bytes, fetching one part at a time as they
// are read. It never holds more than a single part.
type mediaReader struct {
	ctx   context.Context
	fetch func(ctx context.Context, offset int64, limit int) ([]byte, error)
	// offset is the attachment offset of the next byte to hand out.
	offset int64
	// end is the offset to stop at, exclusive. Negative means "read on until
	// Telegram has no more bytes".
	end     int64
	pending []byte
}

func (r *mediaReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	for len(r.pending) == 0 {
		if r.end >= 0 && r.offset >= r.end {
			return 0, io.EOF
		}
		want := mediaPart
		if r.end >= 0 {
			if left := r.end - r.offset; left < int64(want) {
				want = int(left)
			}
		}
		part, err := r.fetch(r.ctx, r.offset, want)
		if err != nil {
			return 0, err
		}
		if len(part) == 0 {
			return 0, io.EOF
		}
		r.pending = part
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	r.offset += int64(n)
	return n, nil
}

// Close drops the part still in hand. The caller closes the stream even when it
// stops early, so a partial read leaves nothing behind.
func (r *mediaReader) Close() error {
	r.pending = nil
	return nil
}

// mediaFetcher reads parts of one attachment, re-reading the message once when
// Telegram says the file reference it was given has gone stale.
type mediaFetcher struct {
	manager   *Manager
	ctx       context.Context
	channel   tgapi.Channel
	messageID int
	loc       tg.InputFileLocationClass
	refreshed bool
}

// part reads limit bytes of the attachment starting at offset.
func (f *mediaFetcher) part(ctx context.Context, offset int64, limit int) ([]byte, error) {
	for {
		_, api, err := f.manager.apiClient(ctx)
		if err != nil {
			return nil, err
		}
		req := &tg.UploadGetFileRequest{
			Location: f.loc,
			Offset:   offset,
			Limit:    limit,
		}
		// precise lets the offset and limit be any size, which is what an
		// arbitrary byte range needs.
		req.SetPrecise(true)
		// A CDN redirect would send this client to infrastructure the profile
		// did not configure: the official and custom backends must never reach
		// an endpoint, DC or key that was not set up for them.
		req.SetCDNSupported(false)

		res, err := api.UploadGetFile(ctx, req)
		if err == nil {
			file, ok := res.(*tg.UploadFile)
			if !ok {
				return nil, fmt.Errorf("gotdclient: unexpected getFile result %T", res)
			}
			return file.Bytes, nil
		}
		// A file reference is a capability Telegram hands out per read and
		// expires on its own schedule; the message still carries a fresh one, so
		// reading the message again is the recovery. It is retried once: a
		// reference that goes stale again on the same read is not bad luck.
		if !f.refreshed && isFileReferenceExpired(err) {
			f.refreshed = true
			loc, refreshErr := f.manager.mediaFileLocation(f.ctx, f.channel, f.messageID)
			if refreshErr != nil {
				return nil, refreshErr
			}
			f.loc = loc
			continue
		}
		return nil, ClassifyError(err)
	}
}

// isFileReferenceExpired reports whether Telegram refused the location because
// its file reference is no longer valid rather than because the file is gone.
func isFileReferenceExpired(err error) bool {
	return tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID")
}

// mediaFileLocation re-reads one message for the current download location of
// its attachment.
func (m *Manager) mediaFileLocation(ctx context.Context, channel tgapi.Channel, messageID int) (tg.InputFileLocationClass, error) {
	msg, err := m.channelMessage(ctx, channel, messageID)
	if err != nil {
		if errors.Is(err, tgapi.ErrMessageNotFound) {
			return nil, tgapi.ErrMediaNotFound
		}
		return nil, err
	}
	loc, _, err := mediaLocation(msg.Media)
	return loc, err
}
