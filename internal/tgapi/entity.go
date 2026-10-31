package tgapi

// Entity is one formatting span of a message text.
//
// Offset and Length are in UTF-16 code units, exactly as Telegram reports and
// accepts them. A Go string's byte index or rune index is never interchangeable
// with them: "🙂" is one rune and two code units. Use UTF16Len and UTF16Slice
// to move between the two.
type Entity struct {
	// Type is one of the Entity* constants below.
	Type string
	// Offset is the span's start, in UTF-16 code units from the start of the text.
	Offset int
	// Length is the span's length, in UTF-16 code units.
	Length int
	// URL is the target of a text_url entity; empty for every other type.
	URL string
	// Language is the fenced language of a pre entity; empty otherwise.
	Language string
	// UserID is the referenced user of a mention_name entity; zero otherwise.
	UserID int64
	// DocumentID is the custom emoji's document id; zero otherwise.
	DocumentID int64
}

// The entity types Telegram defines. They are the wire contract between the
// worker and the API: the worker writes one of these strings and every reader
// switches on it.
const (
	EntityMention     = "mention"
	EntityHashtag     = "hashtag"
	EntityBotCommand  = "bot_command"
	EntityURL         = "url"
	EntityEmail       = "email"
	EntityBold        = "bold"
	EntityItalic      = "italic"
	EntityCode        = "code"
	EntityPre         = "pre"
	EntityTextURL     = "text_url"
	EntityMentionName = "mention_name"
	EntityCashtag     = "cashtag"
	EntityPhone       = "phone"
	EntityUnderline   = "underline"
	EntityStrike      = "strike"
	EntityBankCard    = "bank_card"
	EntitySpoiler     = "spoiler"
	EntityBlockquote  = "blockquote"
	EntityCustomEmoji = "custom_emoji"
	// EntityUnknown is a kind this application does not model. It is reported
	// rather than dropped so no formatting silently disappears.
	EntityUnknown = "unknown"
)

// ExtractableToken reports whether an entity names a token the editor lifts
// out of the text into its own control (a hashtag or a mention).
//
// url, email, phone and the rest are ordinary text: they stay where they are.
func ExtractableToken(kind string) bool {
	switch kind {
	case EntityHashtag, EntityMention, EntityMentionName:
		return true
	}
	return false
}

// Rebuildable reports whether an entity of this type can be written back to a
// Telegram message. An unknown type is kept on read so nothing disappears
// silently, but there is no constructor to send it again.
func Rebuildable(kind string) bool {
	switch kind {
	case EntityMention, EntityHashtag, EntityBotCommand, EntityURL, EntityEmail,
		EntityBold, EntityItalic, EntityCode, EntityPre, EntityTextURL,
		EntityMentionName, EntityCashtag, EntityPhone, EntityUnderline,
		EntityStrike, EntityBankCard, EntitySpoiler, EntityBlockquote,
		EntityCustomEmoji:
		return true
	}
	return false
}

// UTF16Len returns the length of s in UTF-16 code units.
//
// A rune above the Basic Multilingual Plane is one rune and two code units;
// everything else is one unit per rune.
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
			continue
		}
		n++
	}
	return n
}

// utf16ToByte returns the byte index in s at UTF-16 code-unit position u.
// ok is false when u is outside the string or falls inside a surrogate pair.
func utf16ToByte(s string, u int) (int, bool) {
	if u < 0 {
		return 0, false
	}
	unit := 0
	for i, r := range s {
		if unit == u {
			return i, true
		}
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if u < unit+w {
			return 0, false
		}
		unit += w
	}
	if unit == u {
		return len(s), true
	}
	return 0, false
}

// UTF16Slice returns the substring of s covering the UTF-16 span
// [offset, offset+length). ok is false when the span leaves the string or
// splits a surrogate pair.
func UTF16Slice(s string, offset, length int) (string, bool) {
	if offset < 0 || length < 0 {
		return "", false
	}
	end := offset + length
	if end < offset {
		return "", false
	}
	startByte, ok := utf16ToByte(s, offset)
	if !ok {
		return "", false
	}
	endByte, ok := utf16ToByte(s, end)
	if !ok {
		return "", false
	}
	return s[startByte:endByte], true
}

// ValidEntity reports whether the entity's span lies inside text and does not
// cut a code point in half.
func ValidEntity(text string, e Entity) bool {
	if e.Offset < 0 || e.Length < 0 {
		return false
	}
	_, ok := UTF16Slice(text, e.Offset, e.Length)
	return ok
}
