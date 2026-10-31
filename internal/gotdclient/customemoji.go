package gotdclient

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/tgapi"
)

// emojiSetTTL is how long one sticker-set lookup is reused. Reaction emoji
// change rarely and a history page can carry hundreds of the same document,
// so the set is asked for once, not once per document.
const emojiSetTTL = time.Hour

// errEmojiSetInvalid and errEmojiSetUnavailable separate "Telegram will not
// serve this set to this account" from a transient failure.
var (
	errEmojiSetInvalid     = errors.New("the reaction sticker set is not available to this account")
	errEmojiSetUnavailable = errors.New("the reaction sticker set could not be read")
)

// DownloadCustomEmoji fetches one custom emoji's bytes by document id.
//
// Reaction custom emoji live in Telegram's reaction sticker set, which
// messages.getStickerSet serves under a fixed input. Documents in it are
// either in the general list or in the keyword-indexed one (Telegram moves
// emoji into "keywords" as their number grows), so both are searched. The
// answer is cached for emojiSetTTL, with file references shared between
// documents — a document that carries its own reference is used as-is.
func (m *Manager) DownloadCustomEmoji(ctx context.Context, documentID int64) (tgapi.EmojiFile, error) {
	if documentID <= 0 {
		return tgapi.EmojiFile{}, tgapi.ErrCustomEmojiNotFound
	}
	client, _, err := m.apiClient(ctx)
	if err != nil {
		return tgapi.EmojiFile{}, err
	}

	doc, err := m.emojiDocument(ctx, client.API(), documentID)
	if err != nil {
		return tgapi.EmojiFile{}, err
	}

	loc := &tg.InputDocumentFileLocation{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
	}
	var buf bytes.Buffer
	if _, err := downloader.NewDownloader().Download(client.API(), loc).
		Stream(ctx, &limitedWriter{w: &buf, limit: maxMediaBytes}); err != nil {
		return tgapi.EmojiFile{}, ClassifyError(err)
	}
	mime := doc.MimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	return tgapi.EmojiFile{Data: buf.Bytes(), MimeType: mime}, nil
}

// emojiDocument finds the document in the reaction set, loading the set when
// it is not cached.
func (m *Manager) emojiDocument(ctx context.Context, api *tg.Client, documentID int64) (*tg.Document, error) {
	docs, err := m.reactionEmoji(ctx, api)
	if err != nil {
		return nil, err
	}
	doc, ok := docs[documentID]
	if !ok {
		return nil, tgapi.ErrCustomEmojiNotFound
	}
	return doc, nil
}

// reactionEmoji returns the reaction sticker set's documents by id, from the
// cache or from Telegram.
//
// The newer request that asks by keyword is not available in the layer this
// build speaks, so the set is fetched whole. When the general list is cut
// down by Telegram (stickerSet.documents may carry only a sample, with the
// rest indexed under "keywords"), a document in the keyword index that is not
// in the list is reported as not found rather than guessed at.
func (m *Manager) reactionEmoji(ctx context.Context, api *tg.Client) (map[int64]*tg.Document, error) {
	m.emojiMu.Lock()
	defer m.emojiMu.Unlock()
	now := m.clock()
	// A lookup that found the document absent is cached too: Telegram
	// recycles document ids, and a page of reactions must not ask again for
	// every one of them. Only a failed request leaves the cache cold.
	if m.emojiDocs != nil && now.Before(m.emojiExpires) {
		return m.emojiDocs, nil
	}

	res, err := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{
		Stickerset: &tg.InputStickerSetEmojiGenericAnimations{},
		Hash:       0,
	})
	if err != nil {
		return nil, classifyEmojiError(err)
	}
	set, ok := res.(*tg.MessagesStickerSet)
	if !ok {
		return nil, errEmojiSetUnavailable
	}

	// Every document in the set shares one file reference, so the ones the
	// answer lists without a reference of their own take it from any sibling.
	shared := sharedFileReference(set.Documents)
	docs := make(map[int64]*tg.Document, len(set.Documents))
	for _, c := range set.Documents {
		doc, ok := c.(*tg.Document)
		if !ok {
			continue
		}
		docs[doc.ID] = withFileReference(doc, shared)
	}

	m.emojiDocs = docs
	m.emojiExpires = now.Add(emojiSetTTL)
	return docs, nil
}

// classifyEmojiError maps a sticker-set failure: a set this account may not
// read is its own error, anything else is a transient failure that is not
// cached.
func classifyEmojiError(err error) error {
	if tgerr.Is(err, "STICKERSET_INVALID", "STICKERSET_NOT_MODIFIED") {
		return errors.Join(errEmojiSetInvalid, err)
	}
	return errors.Join(errEmojiSetUnavailable, ClassifyError(err))
}

// sharedFileReference returns the first non-empty reference in docs.
func sharedFileReference(docs []tg.DocumentClass) []byte {
	for _, c := range docs {
		if doc, ok := c.(*tg.Document); ok && len(doc.FileReference) > 0 {
			return doc.FileReference
		}
	}
	return nil
}

// withFileReference gives doc the set's shared reference when it has none of
// its own. The original is not modified.
func withFileReference(doc *tg.Document, shared []byte) *tg.Document {
	if len(doc.FileReference) > 0 || len(shared) == 0 {
		return doc
	}
	clone := *doc
	clone.FileReference = shared
	return &clone
}

// clock is the manager's time source.
func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// cachedEmojiDocument returns a cached document, or nil.
func (m *Manager) cachedEmojiDocument(now time.Time, id int64) *tg.Document {
	m.emojiMu.Lock()
	defer m.emojiMu.Unlock()
	if m.emojiDocs == nil || !now.Before(m.emojiExpires) {
		return nil
	}
	return m.emojiDocs[id]
}
