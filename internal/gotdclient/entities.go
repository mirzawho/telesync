package gotdclient

import (
	"github.com/gotd/td/tg"

	"github.com/mirza/telesync/internal/tgapi"
)

// Entities converts the formatting spans of one Telegram message into the
// library-independent shape. The result is never nil: a message without
// formatting reports an empty list, so no reader has to branch.
//
// The offsets pass through untouched. Telegram measures them in UTF-16 code
// units and so does tgapi.Entity; nothing between here and the API converts
// them.
func Entities(msg *tg.Message) []tgapi.Entity {
	out := []tgapi.Entity{}
	if msg == nil {
		return out
	}
	spans, ok := msg.GetEntities()
	if !ok {
		return out
	}
	for _, span := range spans {
		out = append(out, entityOf(span))
	}
	return out
}

// entityOf converts one span. A constructor this adapter does not model keeps
// its position and reports the type as unknown, so nothing silently vanishes
// from a message the user is reading.
func entityOf(span tg.MessageEntityClass) tgapi.Entity {
	switch e := span.(type) {
	case *tg.MessageEntityMention:
		return tgapi.Entity{Type: tgapi.EntityMention, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityHashtag:
		return tgapi.Entity{Type: tgapi.EntityHashtag, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityBotCommand:
		return tgapi.Entity{Type: tgapi.EntityBotCommand, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityURL:
		return tgapi.Entity{Type: tgapi.EntityURL, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityEmail:
		return tgapi.Entity{Type: tgapi.EntityEmail, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityBold:
		return tgapi.Entity{Type: tgapi.EntityBold, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityItalic:
		return tgapi.Entity{Type: tgapi.EntityItalic, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityCode:
		return tgapi.Entity{Type: tgapi.EntityCode, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityPre:
		return tgapi.Entity{Type: tgapi.EntityPre, Offset: e.Offset, Length: e.Length, Language: e.Language}
	case *tg.MessageEntityTextURL:
		return tgapi.Entity{Type: tgapi.EntityTextURL, Offset: e.Offset, Length: e.Length, URL: e.URL}
	case *tg.MessageEntityMentionName:
		return tgapi.Entity{Type: tgapi.EntityMentionName, Offset: e.Offset, Length: e.Length, UserID: e.UserID}
	case *tg.MessageEntityCashtag:
		return tgapi.Entity{Type: tgapi.EntityCashtag, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityPhone:
		return tgapi.Entity{Type: tgapi.EntityPhone, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityUnderline:
		return tgapi.Entity{Type: tgapi.EntityUnderline, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityStrike:
		return tgapi.Entity{Type: tgapi.EntityStrike, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityBankCard:
		return tgapi.Entity{Type: tgapi.EntityBankCard, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntitySpoiler:
		return tgapi.Entity{Type: tgapi.EntitySpoiler, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityBlockquote:
		return tgapi.Entity{Type: tgapi.EntityBlockquote, Offset: e.Offset, Length: e.Length}
	case *tg.MessageEntityCustomEmoji:
		return tgapi.Entity{Type: tgapi.EntityCustomEmoji, Offset: e.Offset, Length: e.Length, DocumentID: e.DocumentID}
	default:
		offset, length := 0, 0
		if e, ok := span.(interface{ GetOffset() int }); ok {
			offset = e.GetOffset()
		}
		if e, ok := span.(interface{ GetLength() int }); ok {
			length = e.GetLength()
		}
		return tgapi.Entity{Type: tgapi.EntityUnknown, Offset: offset, Length: length}
	}
}

// InputEntities rebuilds the spans for a message that is about to be sent.
//
// dropped counts the entities of a kind this adapter has no constructor for:
// they are left out rather than written as a wrong span, and the caller
// reports the loss instead of claiming the formatting survived.
func InputEntities(in []tgapi.Entity) (out []tg.MessageEntityClass, dropped int) {
	out = []tg.MessageEntityClass{}
	for _, e := range in {
		span := inputEntity(e)
		if span == nil {
			dropped++
			continue
		}
		out = append(out, span)
	}
	return out, dropped
}

// inputEntity returns the Telegram constructor for one span, or nil for a kind
// it cannot rebuild.
func inputEntity(e tgapi.Entity) tg.MessageEntityClass {
	switch e.Type {
	case tgapi.EntityMention:
		return &tg.MessageEntityMention{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityHashtag:
		return &tg.MessageEntityHashtag{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityBotCommand:
		return &tg.MessageEntityBotCommand{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityURL:
		return &tg.MessageEntityURL{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityEmail:
		return &tg.MessageEntityEmail{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityBold:
		return &tg.MessageEntityBold{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityItalic:
		return &tg.MessageEntityItalic{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityCode:
		return &tg.MessageEntityCode{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityPre:
		return &tg.MessageEntityPre{Offset: e.Offset, Length: e.Length, Language: e.Language}
	case tgapi.EntityTextURL:
		return &tg.MessageEntityTextURL{Offset: e.Offset, Length: e.Length, URL: e.URL}
	case tgapi.EntityMentionName:
		return &tg.MessageEntityMentionName{Offset: e.Offset, Length: e.Length, UserID: e.UserID}
	case tgapi.EntityCashtag:
		return &tg.MessageEntityCashtag{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityPhone:
		return &tg.MessageEntityPhone{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityUnderline:
		return &tg.MessageEntityUnderline{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityStrike:
		return &tg.MessageEntityStrike{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityBankCard:
		return &tg.MessageEntityBankCard{Offset: e.Offset, Length: e.Length}
	case tgapi.EntitySpoiler:
		return &tg.MessageEntitySpoiler{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityBlockquote:
		return &tg.MessageEntityBlockquote{Offset: e.Offset, Length: e.Length}
	case tgapi.EntityCustomEmoji:
		return &tg.MessageEntityCustomEmoji{Offset: e.Offset, Length: e.Length, DocumentID: e.DocumentID}
	}
	return nil
}
