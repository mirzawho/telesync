<!-- archived: the page this documentation replaced -->
# Schemas

Field-by-field reference for the objects the API exchanges.

- [conventions.md(conventions.md) — envelopes, request IDs, identifiers,
  timestamps, limits
- [telegram.md(telegram.md) — objects returned by the Telegram routes:
  health, profile, channel summary, channel, message, media, sender,
  permissions
- [local.md](local.md) — objects returned by the local routes: category, user,
  permissions

Each endpoint page links to the schema it returns; this directory is the
canonical definition, so a field is described once. "Required" below means
"always present in a successful response"; "omitted" means the JSON key is
**absent** (not `null`) when the value is unknown, which distinguishes "no value
reported" from `false` or `0`.
