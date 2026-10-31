<!-- archived: the page this documentation replaced -->
# Errors

## The error body

Every failed JSON response uses one shape:

```json
{
  "error": {
    "code": "unknown_client",
    "message": "No configured Telegram client has that key.",
    "request_id": "6f1c2a4b8d0e9a71"
  }
}
```

| Field | Type | Notes |
| --- | --- | --- |
| `code` | string | stable identifier; branch on this |
| `message` | string | human-readable; may change between versions, do not parse it |
| `request_id` | string | the id this request was given (see [Conventions](schemas/conventions.md#request-ids)); also sent in the `X-Request-Id` response header |

Two responses are deliberately **not** in this shape, because they are not JSON:

- `/healthz` success is `{"status":"ok","time":"…"}` (no envelope).
- `POST /api/v1/auth/login` success is `{"access_token":…,"token_type":…,"expires_in":…}`
  (no envelope).
- Media and avatar bytes are image/binary content, documented per route.

Everything else — Telegram routes and local routes — uses
`{"data":…,"meta":…}` on success.

Error responses also carry `X-Request-Id` and `X-Content-Type-Options: nosniff`.

## Status and code reference

| HTTP | `code` | When | Retry? |
| --- | --- | --- | --- |
| 400 | `invalid_input` | a malformed path id, `page`/`per_page`, unknown or repeated query parameter, malformed `q`, invalid body or field value, self-deactivation/deletion | no — fix the request |
| 400 | `invite_invalid` | the invite hash is malformed, revoked or expired | no |
| 400 | `peer_unresolved` | a numeric channel id this client has never resolved (no access hash) | only after a `find` by username or link |
| 401 | `unauthorized` | missing, malformed, wrongly signed, wrong-issuer or expired token; operator token used on an account route | after logging in again (expired); otherwise fix the credential |
| 401 | `invalid_credentials` | `POST /api/v1/auth/login` with a wrong username or password | no |
| 403 | `forbidden` | a valid credential without the required permission; an operator token without an account; changing your own permissions | after an administrator changes the permission set |
| 403 | `user_inactive` | login for an inactive account with the correct password | no — the account needs reactivating |
| 404 | `unknown_client` | `{key}` is not a configured, enabled client | no |
| 404 | `channel_not_found` | the worker/Telegram cannot see the channel for this account | no |
| 404 | `not_found` | no such message, no attachment on the message, no category/user with that id, or no such route | usually no |
| 405 | `method_not_allowed` | the path exists but not for this method; `Allow` header lists the methods | no |
| 409 | `needs_auth` | the client is configured but its account is not authorized | after `telesync auth --key <client>` |
| 409 | `membership_limit` | the account cannot join more channels | no |
| 409 | `conflict` | duplicate category name (ignoring case), duplicate username (ignoring case), or a change that would leave no active administrator | no — change the value / promote another administrator |
| 415 | `invalid_input` | a write body without `Content-Type: application/json` | no |
| 429 | `rate_limited` | Telegram asked this client to wait; the `Retry-After` header gives the seconds | yes, after `Retry-After` |
| 500 | `store_error` | the local SQLite store failed (details only in the daemon log) | maybe — an operator problem |
| 500 | `worker_error` | `join` confirmed membership but the local record could not be written | **yes; the retry completes the local write** |
| 502 | `worker_error` | the worker failed in a way that has no specific code (details only in the log) | usually not without inspecting the log |
| 503 | `not_connected` | the worker is up but its Telegram connection is down | yes, with backoff |
| 503 | `worker_unavailable` | the worker's socket does not answer, or the local store a route needs is not configured | yes, with backoff |
| 504 | `worker_timeout` | the worker did not answer within `api.request_timeout` | yes, with backoff |

`429` responses are the only ones that set `Retry-After`; its value comes from
Telegram's own flood wait.

A `405` always sets `Allow`:

| Path group | `Allow` examples |
| --- | --- |
| `/healthz` | `GET` |
| `/api/v1/auth/login` | `POST` |
| `/api/v1/auth/me` | `GET` |
| `/api/v1/telegram/{key}/channels/{id}/join` | `POST` |
| other `/api/v1/telegram/...` reads | `GET`, or `GET, POST` under `/channels` |
| `/api/v1/local/categories` | `GET, POST` |
| `/api/v1/local/categories/{id}` | `GET, PUT, DELETE` |
| `/api/v1/local/users` | `GET, POST` |
| `/api/v1/local/users/{id}` | `GET, PUT, DELETE` |
| `/api/v1/local/users/{id}/permissions` | `GET, PUT` |

On `/api/v1/local/...` the credential is checked before routing
(an anonymous call to an unserved local path is `401`); on the Telegram routes
an unsupported method anywhere under `/api/v1/telegram/{key}/` answers `405`
with `Allow: GET` even if the path shape is wrong.

## Deciding what to do

- **`404 unknown_client` is never a fallback signal.** The requested client does
  not exist or is disabled in the configuration; no other client will answer.
- **`409 needs_auth` is a state, not a crash.** The worker stays up and keeps
  reporting `needs_auth` until the account is authorized; polling is safe.
- **`503`/`504` are transient by design.** The supervisor restarts a wedged
  worker and gotd reconnects on its own; back off rather than retrying tightly.
- **`join` partial failure (`500 worker_error` with a message naming
  membership).** Telegram's side completed; calling `join` again is safe and
  finishes the local write.
- **`403 forbidden` on a channel you expect to see** usually means the account
  lacks `channels.view` and the channel has no local row carrying `view: allow`.
  Joining it is what creates the row.
- **`400 peer_unresolved`** means no access hash is cached for that numeric id.
  Resolve it once with `GET .../channels/find?q=@username` (or a link), then
  retry; the worker remembers the hash in `runtime.peer_dir`.

## Details that are never returned

Error messages carry no API hash, phone number, login code, 2FA password,
session content, access hash, bearer token, RSA material or custom-backend
host/port. Worker and store failures are logged once per failing request with
the client key, operation and request id; the response says only that it failed.
