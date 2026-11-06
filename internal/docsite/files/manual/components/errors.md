<!-- component: errors -->
<!-- manual: errors -->
# Errors

## The body

Every failed JSON response has one shape:

```json
{"error": {"code": "unknown_client", "message": "No configured Telegram client has that key.", "request_id": "6f1c…"}}
```

| Field | Type | Notes |
| --- | --- | --- |
| `code` | string | stable identifier; branch on this |
| `message` | string | human-readable; may change, do not parse it |
| `request_id` | string | the id the request was given, also in `X-Request-Id` |

`/healthz` and login success are **not** enveloped: `/healthz` answers
`{"status":"ok","time":…}` and login answers
`{"access_token":…,"token_type":…,"expires_in":…}`.

## Status and code reference

| HTTP | `code` | When | Retry? |
| --- | --- | --- | --- |
| 400 | `invalid_input` | malformed path id, `page`/`per_page`, unknown query parameter, malformed `q`, invalid body or field, self-deactivation/deletion | no |
| 400 | `invite_invalid` | the invite hash is malformed, revoked or expired | no |
| 400 | `peer_unresolved` | a numeric channel id this client has never resolved | after a `find` by username or link |
| 401 | `unauthorized` | missing, malformed, wrongly signed or expired token; the operator token on an account route | again after a fresh login (expired), else fix the credential |
| 401 | `invalid_credentials` | login with a wrong username or password | no |
| 403 | `forbidden` | a valid credential without the permission; changing your own permissions | after an administrator changes the set |
| 403 | `user_inactive` | login for an inactive account with the right password | no |
| 404 | `unknown_client` | `{key}` is not a configured, enabled client, or a local channel record names a client the configuration no longer has | no |
| 404 | `channel_not_found` | the account cannot see the channel | no |
| 404 | `not_found` | no such message, no attachment, no such category/user/local channel, no such route | usually no |
| 405 | `method_not_allowed` | the path exists but not for this method; `Allow` lists the methods | no |
| 409 | `needs_auth` | the client is configured but its account is not authorized | after `telesync auth --key <client>` |
| 409 | `membership_limit` | the account cannot join more channels | no |
| 409 | `conflict` | duplicate category name or username (ignoring case), or a change that would leave no active administrator | no |
| 415 | `invalid_input` | a write body without `Content-Type: application/json` | no |
| 429 | `rate_limited` | Telegram asked this client to wait; `Retry-After` gives the seconds | after `Retry-After` |
| 500 | `store_error` | the local SQLite store failed (details only in the daemon log) | maybe |
| 500 | `partial_failure` | `POST /api/v1/local/channels/{id}/leave` with `telegram` true: Telegram left the channel but the local record could not be deleted | **yes, with `{"telegram": false}`** |
| 500 | `worker_error` | `join` confirmed membership but the local record was not written | **yes; the retry completes it** |
| 502 | `worker_error` | the worker failed with no more specific code (details in the log) | inspect the log |
| 503 | `not_connected` | the worker is up, its Telegram connection is down | yes, with backoff |
| 503 | `worker_unavailable` | the worker's socket does not answer, or a store a route needs is not configured | yes, with backoff |
| 504 | `worker_timeout` | the worker did not answer within `api.request_timeout` | yes, with backoff |

A `405` always sets `Allow`. On `/api/v1/local/...` the credential is checked
before routing and before the method, and under `/api/v1/telegram/{key}/` an
unsupported method answers `405` with `Allow: GET` even for a path that is not a
route. A `429` is the only response that sets `Retry-After`, from Telegram's own
flood wait.

Publishing has its own codes, named after the stage that failed rather than the
status it answered with; they are listed in [publishing](publishing.md#failure-stages).

## Details never returned

Error messages carry no API hash, phone number, login code, 2FA password,
session content, access hash, bearer token, RSA material or custom-backend
host/port. Failures are logged once per request with the client key, operation
and request id.
