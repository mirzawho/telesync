<!-- archived: the page this documentation replaced -->
# Authentication and authorization

Every request except `POST /api/v1/auth/login` (and `GET /healthz`, on a
loopback listener) must carry:

```
Authorization: Bearer <credential>
```

The credential is either the **operator token** or a **user access token**. They
are resolved differently and grant different things; the sections below keep
them apart.

## The two credentials

| | Operator token | User access token |
| --- | --- | --- |
| Where it comes from | `api.auth_token_env` names an environment variable holding it; the daemon reads it at startup | `POST /api/v1/auth/login` returns it |
| Where it is valid | only when `api.listen_addr` is loopback | any listener with `api.jwt.secret_env` or `api.jwt.secret` configured |
| Who it represents | nobody; it is the deployment's own credential | one row in `users` |
| Expires | never (until the environment variable changes and the daemon restarts) | after `api.jwt.token_ttl`, default 1 h, at most 24 h |
| Permissions | every permission | exactly the stored permission set |
| Can call `/api/v1/auth/me` or `/api/v1/local/users` | no — those routes need an account | yes |
| Revocable individually | no | yes: delete or deactivate the account, or replace its permissions; each takes effect on the next request |

The configuration loader refuses a non-loopback listener that sets
`api.auth_token_env` ("remove it and set `api.jwt.secret_env` for a
non-loopback API") and refuses one that has neither. The operator token is
compared in constant time and never echoed in a response or a log.

The signing key for user logins comes from one of two places, never both:
`api.jwt.secret_env` (the **name** of an environment variable, recommended) or
`api.jwt.secret` (the key itself, written in the file, convenient on a
single-user machine). Either must be at least 32 bytes; `telesync config
validate` tells you which one is in use and never prints it.

Token failures are reported as described in [the error reference(errors.md#status-and-code-reference).
Every reason a credential is unusable is one `401`; only an expired token says
so in the message, because the caller can then log in again.

## Bootstrap order in the gateway

The checks run in a fixed order, which is observable:

1. `/healthz` — routed first; the token is required only when `RequireAuth` is
   set (non-loopback listener).
2. `/api/v1/auth/login` — routed next, with no credential.
3. `/api/v1/auth/me`, `/api/v1/local/...` — the credential is checked **before**
   the path's shape. An anonymous request to an unserved path under
   `/api/v1/local/` gets `401`, not `405`.
4. `/api/v1/telegram/{key}/...` — the **key is resolved first**:
   - a key that is not a configured, enabled client is `404 unknown_client`
     before the credential is checked, so an anonymous caller learns which keys
     are configured;
   - then the credential is checked: missing or invalid is `401`;
   - then per-channel and per-permission authorization, which can answer `403`.

## Permissions

An account holds:

- **global permissions** (stored in `user_permissions`), and
- **per-resource assignments** for channels and categories (stored in
  `user_channels_permission` and `user_categories_permission`), each carrying an
  `allow` or `deny` for `view`, `update` or `delete`.

The global permissions are exactly:

| Permission | Grants |
| --- | --- |
| `users.view` | see every account in `GET /api/v1/local/users` and read any account and its permissions |
| `users.manage` | create, update and delete accounts; replace anyone's permission set; read any account |
| `channels.view` | list and read every channel (`/channels`, `find`, `/channels/{id}`, messages, media, avatars, `/profile`) |
| `channels.create` | call `POST .../join` (joining creates the local channel record) |
| `categories.view` | list every category and read any category |
| `categories.create` | create a category |

### The decision rule

`channelstore.Authorizer` answers one question at a time, in this order:

1. An explicit assignment for that exact resource (allow or deny) decides.
2. Otherwise the global permission for that kind and action decides; it allows
   `view` on that kind and nothing else per resource.
3. Otherwise the answer is no; an unestablished permission is `false`, never an
   optimistic `true`.

`create` is only ever decided by a global permission, because the resource does
not exist yet and cannot carry an assignment. Per-channel and per-category
assignments accept only `view`, `update` and `delete`; a `create` inside
`channel_permissions` is rejected with `400`.

The operator token is treated as holding every permission, but it has no account
row: `users.*` route handling refuses it, and it can never be given a permission
set.

### What each route requires

| Route | Requirement | 403 cases |
| --- | --- | --- |
| `GET /healthz` | nothing on loopback; a credential otherwise | — |
| `POST /api/v1/auth/login` | nothing | — |
| `GET /api/v1/auth/me` | any valid credential that names an account | operator token → `401` |
| `GET .../health` | a valid credential; no permission needed beyond that | — |
| `GET .../profile` | `channels.view`, **or** any per-channel assignment | neither held → `403` |
| `GET .../channels` | `channels.view`, **or** any per-channel assignment; each listed channel is then filtered by the channel rule | neither held → `403` |
| `GET .../channels/find` | same as `/channels`; results are filtered the same way | neither held → `403` |
| `GET .../channels/{id}` | the channel rule for that channel | not allowed → `403` |
| `GET .../channels/{id}/messages`, `/messages/{mid}`, `/messages/{mid}/media`, `/avatar` | the channel rule for that channel | not allowed → `403` |
| `POST .../channels/{id}/join` | `channels.create` | absent → `403` |
| `GET /api/v1/local/categories` | a valid credential; without `categories.view` the page is drawn from the categories the caller may view | — |
| `POST /api/v1/local/categories` | `categories.create` | absent → `403` |
| `GET /api/v1/local/categories/{id}` | category `view` (assignment, else `categories.view`) | not allowed → `403` |
| `PUT /api/v1/local/categories/{id}` | category `update` assignment | absent → `403` |
| `DELETE /api/v1/local/categories/{id}` | category `delete` assignment | absent → `403` |
| `GET /api/v1/local/users` | a valid credential; without `users.view` the list is narrowed (see [users](local/users.md#list-users)) | — |
| `POST /api/v1/local/users` | `users.manage` | absent → `403` |
| `GET /api/v1/local/users/{id}` | `users.view`, **or** `users.manage`, **or** the caller's own id | otherwise → `403` |
| `PUT /api/v1/local/users/{id}`, `DELETE /api/v1/local/users/{id}` | `users.manage` | absent → `403` |
| `GET /api/v1/local/users/{id}/permissions` | own id, or `users.view`, or `users.manage` | otherwise → `403` |
| `PUT /api/v1/local/users/{id}/permissions` | `users.manage`, **and** the target is not the caller | own id → `403`; absent → `403` |

### The channel rule

A Telegram channel is authorized through its **local** `managed_channels` row,
because that is where a per-channel assignment can point (`channel_id` in a
permission body is the local row id, not Telegram's id).

- With `channels.view`: every channel is visible, **except** one whose local row
  carries an explicit `view: deny`.
- Without `channels.view`: only a channel whose local row carries an explicit
  `view: allow` is visible. A channel with **no local row** cannot store such an
  assignment and stays hidden. Join it first (`POST .../join`) if it should be
  assignable.
- If the local lookup fails, the failure is logged and the global permission
  alone decides.

On `GET .../channels`, a caller with `channels.view` and no per-channel denies
sees the page untouched, and `total` is Telegram's count. If channels are
removed, `meta.total` is restated as the number of channels **on the page**
after filtering, while `has_more` stays Telegram's answer, because paging
continues upstream. A page can therefore look shorter than `per_page` while
`has_more` is `true`.

An unresolved invite reference has no channel id, so it cannot be authorized per
channel; `GET .../channels/+<invite>` and `POST .../+<invite>/join` require a
global permission (`channels.view` / `channels.create`). A `find` result that is
an unjoined invite (id `""`) is kept only for a caller with `channels.view`.

### Category and user rules that apply to everyone

- `409 conflict` protects the last administrator: deleting or deactivating the
  last active account holding `users.manage`, or replacing its permission set
  without `users.manage`, is refused.
- Nobody can change their own permission set (`PUT /local/users/{id}/permissions`
  with `id` = the caller's own id is `403`), so an administrator cannot widen
  their own access.
- Nobody can deactivate or delete their own account (`400 invalid_input`).
