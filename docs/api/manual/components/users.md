<!-- component: users -->
<!-- manual: users -->
# Users and permissions

Operator accounts for this API, stored in the local SQLite database. They have
nothing to do with Telegram accounts: a Telegram account is
`GET /api/v1/telegram/{key}/profile`.

## The first account

Every write here needs `users.manage`, which only an existing account can hold,
and there is no HTTP registration route. `telesync admin` creates or promotes
one interactively, storing only a hash and granting every global permission;
running it again for an existing username resets its password. An account can
also be inserted by hand with `telesync hash-password` and SQL; either way it is
the only account that needs anything but the API.

## Objects and rules

A user is `{"id", "fullname", "username", "is_active", "created_at",
"updated_at", "permissions"}`. `fullname` is 1–200 characters, `username` is
1–64 of ASCII letters, digits, `.`, `_`, `-` and unique ignoring case, and the
password is 8–1024 characters. `password_hash` is never returned and never
accepted as a field.

- `POST` needs `users.manage`; `PUT` is partial (only the fields sent change,
  at least one is required); `DELETE` needs `users.manage`.
- You cannot deactivate or delete your own account (`400`), and the last active
  account holding `users.manage` cannot be deleted, deactivated or demoted
  (`409 conflict`).
- Listing: with `users.view` every account; without it, the caller and (with
  `users.manage`) the accounts holding a permission.
- Permissions are replaced whole by `PUT .../permissions`: anything not listed
  is removed. `global_permissions` are the listed global permissions;
  `channel_permissions` and `category_permissions` name a **local** row id and
  `view`/`update`/`delete` effects of `allow` or `deny`. `create` is not a
  per-resource action. Every id must exist; a set naming one that does not
  changes nothing. A managing permission needs its viewing permission:
  `users.manage` needs `users.view`, `channels.create` needs `channels.view`,
  `categories.create` needs `categories.view` (`400` naming the missing one).
  With `<kind>.view` and no per-item rows the caller sees every resource of
  that kind; per-item `allow` rows restrict them to those items.
- `servers.view` is a global permission of its own: it allows `GET
  /api/v1/telegram`, the server listing behind the dashboard's card
  (described in the Telegram routes paragraph), and nothing else. No other
  global permission implies it.
- Nobody can change their own permission set (`403 forbidden`).

The permission model itself is in the authentication paragraph.
