<!-- component: authentication -->
<!-- manual: authentication and authorization -->
# Authentication and authorization

Every request must carry

```
Authorization: Bearer <credential>
```

except the pages: `GET /healthz`, `GET /docs/...` and the admin panel under
`/admin` are read without one. Only `/api/v1/...` checks a credential. It is
either the **operator token** or a **user access
token**, which are resolved differently and grant different things.

## The two credentials

| | Operator token | User access token |
| --- | --- | --- |
| Source | `api.auth_token_env` names an environment variable; the daemon reads it at startup | `POST /api/v1/auth/login` |
| Valid on | a loopback `api.listen_addr` only | any listener with `api.jwt.secret_env` |
| Represents | nobody; the deployment's own credential | one row in `users` |
| Expires | never, until the variable changes and the daemon restarts | after `api.jwt.token_ttl`, default 1 h, at most 24 h |
| Permissions | every permission | the stored permission set |
| `/api/v1/auth/me`, `/api/v1/local/users` | refused: it has no account | yes |
| Revocable per person | no | yes, on the account's next request |

The configuration loader refuses a non-loopback listener that sets
`api.auth_token_env` ("remove it and set `api.jwt.secret_env`") and one that has
neither. The token is compared in constant time and never echoed.

An expired token is the only authentication failure whose message says why;
every other unusable credential is one `401 unauthorized`.

## Permissions

An account holds **global permissions** and **per-resource assignments** for
channels, categories and WordPress sites, each an `allow` or a `deny` for
`view`, `update` or `delete`. The global permissions are exactly:

| Permission | Grants |
| --- | --- |
| `users.view` | see every account in `GET /api/v1/local/users`, read any account and its permissions |
| `users.manage` | create, update and delete accounts; replace anyone's permissions; read any account |
| `channels.view` | list and read every channel (`/channels`, `find`, `/channels/{id}`, messages, media, avatars, `/profile`) |
| `channels.create` | `POST .../join`, which creates the local channel record |
| `categories.view` | list every category and read any category |
| `categories.create` | create a category |
| `wordpress.view` | list every registered WordPress site |
| `wordpress.create` | register, update and delete WordPress sites (see below) |

The decision, in order:

1. an explicit assignment for that resource (`allow` or `deny`) decides;
2. otherwise the global permission for that kind and action decides, which
   allows `view` and `create` only;
3. otherwise the answer is no. An unestablished permission is `false`, never an
   optimistic `true`.

`create` is only ever a global permission, because the resource does not exist
yet and cannot carry an assignment. Per-channel, per-category and per-site
assignments accept only `view`, `update` and `delete`.

A WordPress site's assignment can admit its `view` in the list; `update` and
`delete` assignments are accepted and reported but grant nothing, because
writing a site needs the global `wordpress.create`. A site's credential can be
replaced through an update, so writing is one global right rather than a
per-site one.

The operator token holds every permission but has no account row: the account
routes refuse it and it can never be given a set.

## The channel rule

A Telegram channel is authorized through its **local** `managed_channels` row,
because that is where a per-channel assignment points (`channel_id` in a
permission body is the local row id, not Telegram's id).

- With `channels.view`, every channel is visible, except one whose local row
  carries an explicit `view: deny`.
- Without `channels.view`, only a channel whose local row carries an explicit
  `view: allow` is visible. A channel with no local row cannot carry an
  assignment and stays hidden until it is joined.
- If the local lookup fails, the failure is logged and the global permission
  alone decides.

On `GET .../channels`, a caller with `channels.view` and no per-channel denies
sees the page untouched and `total` is Telegram's count. If channels are removed,
`total` is restated as the number on the page after filtering, while `has_more`
stays Telegram's answer. A page can therefore look shorter than `per_page` while
`has_more` is `true`.

An invite reference has no channel id yet, so it cannot be authorized per
channel: the invite routes require `channels.view` (read) or `channels.create`
(join) instead.

The same rule governs `/api/v1/local/channels/...`, with one addition: those
routes name the row directly, so the record is looked up first (`404` when it
does not exist) and then the rule above decides the view. Removing a record with
`POST /api/v1/local/channels/{id}/leave` needs a `delete` assignment on that row
(`403` otherwise); the global `channels.view` does not grant it. The operator
token holds no per-channel assignment, so it cannot remove a record.

## Rules that apply to everyone

- The last active account holding `users.manage` cannot be deleted,
  deactivated, or have that permission removed (`409 conflict`).
- Nobody can change their own permission set (`403 forbidden`).
- Nobody can deactivate or delete their own account (`400 invalid_input`).

## Bootstrap order

The gateway checks in a fixed order, which is observable:

1. `/healthz` — routed first; no credential, on every listener.
2. `/api/v1/auth/login` — no credential.
3. `/docs/...` — no credential. `/api/v1/auth/me`, `/api/v1/local/...` — the
   credential first.
   An anonymous request to an unserved path under `/api/v1/local/` is `401`, not
   `405`.
4. `/api/v1/telegram/{key}/...` — the key is resolved first (an unknown key is
   `404 unknown_client` before authentication), then the credential (`401`),
   then per-channel and per-permission authorization (`403`).
