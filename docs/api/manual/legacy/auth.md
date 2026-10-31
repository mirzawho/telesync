<!-- archived: the page this documentation replaced -->
# Identity API

Routes that concern the caller rather than any Telegram client. See
[Authentication](authentication.md) for the two credential types and the
permission model.

---

## `POST /api/v1/auth/login`

Exchanges a username and password for an access token.

- **Authentication:** none. This is where a user token comes from.
- **Required configuration:** `api.jwt.secret_env`. Without it the route answers
  `503 worker_unavailable` ("User login is not configured.").
- **Method:** only `POST`; anything else is `405` with `Allow: POST`.

### Request

`Content-Type: application/json`, one JSON object (unknown fields rejected,
trailing content rejected, body limited to 64 KiB):

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `username` | string | yes | matched ignoring case; surrounding whitespace trimmed |
| `password` | string | yes | verified against the stored Argon2id hash |

```json
{
  "username": "alice",
  "password": "correct horse battery staple"
}
```

### Success — `200`

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.…",
  "token_type": "Bearer",
  "expires_in": 3600
}
```

| Field | Meaning |
| --- | --- |
| `access_token` | HS256 JWT: header, payload (`sub` = local user id, `iss` `telesync`, `aud` `telesync-api`, `iat`, `exp`), signature |
| `token_type` | always `"Bearer"` |
| `expires_in` | seconds until `exp`; `api.jwt.token_ttl`, default 3600, at most 86400 |

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | malformed JSON, unknown field, empty `username` or `password` is **not** this — an empty value answers 401 |
| 401 | `invalid_credentials` | unknown username, wrong password, or an unreadable stored hash |
| 403 | `user_inactive` | the password is correct but the account is inactive |
| 415 | `invalid_input` | `Content-Type` is not `application/json` |
| 503 | `worker_unavailable` | login is not configured (`api.jwt.secret_env` unset) |

An unknown username and a wrong password answer identically, and an unknown
username still costs one password verification, so neither the answer nor its
timing reveals whether an account exists.

### Behaviour

- The user is re-read from the database on **every** protected request, so a
  deleted or deactivated account or a changed permission set takes effect on the
  next request, not when the token expires.
- The token names the user id (`sub`) only; that id is what the permission set
  is read for.

### Example

```bash
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/auth/login" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --data '{
    "username": "alice",
    "password": "correct horse battery staple"
  }'
```

Store the token and use it for everything else:

```bash
export TELESYNC_TOKEN="$(curl --silent --fail --show-error \
  --request POST "${BASE_URL}/api/v1/auth/login" \
  --header "Content-Type: application/json" \
  --data '{"username":"alice","password":"correct horse battery staple"}' |
  sed -e 's/.*"access_token":"//' -e 's/".*//')"
```

(The `sed` expression assumes the field order the API emits. Use `jq -r
.access_token` if jq is installed.)

Related: [`GET /api/v1/auth/me`](#get-apiv1authme), [Errors](errors.md).

---

## `GET /api/v1/auth/me`

The caller's own profile and stored permission set, read for this request.

- **Authentication:** any valid credential that names an account. The operator
  token has no account and is refused with `401 unauthorized`.
- **Method:** only `GET`; anything else is `405` with `Allow: GET`.

### Success — `200`

```json
{
  "data": {
    "id": 1,
    "fullname": "Alice Operator",
    "username": "alice",
    "is_active": true,
    "created_at": "2026-10-25T09:00:00Z",
    "updated_at": "2026-10-25T09:00:00Z",
    "permissions": {
      "global": ["users.view", "channels.view"],
      "channels": [
        {"channel_id": 3, "view": "allow", "update": "deny"}
      ],
      "categories": []
    }
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

`data` is the [User(schemas/local.md#user-object) object. Field meanings:

- `permissions.global` — the stored global permissions, as listed in
  [Authentication(authentication.md#permissions).
- `permissions.channels` / `permissions.categories` — only **explicit**
  assignments. `channel_id` and `category_id` are **local** row ids, not Telegram
  channel ids. An action that is absent from an entry has no override: the
  global permission, or the absence of one, decides.
- `view`, `update`, `delete` — each `"allow"` or `"deny"`.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 401 | `unauthorized` | missing/invalid token, or the operator token |

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/auth/me" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

Related: [Users(local/users.md), [Authentication(authentication.md).

---

## `GET /healthz`

Gateway liveness. It says nothing about any Telegram client.

- **Authentication:** none when `api.listen_addr` is loopback. On a non-loopback
  listener the daemon requires a credential, and a missing or invalid one is
  `401` with `WWW-Authenticate: Bearer realm="telesync"`.
- **Method:** only `GET`; anything else is `405` with `Allow: GET`.
- Replies with `X-Request-Id` like every route.

### Success — `200`

```json
{"status": "ok", "time": "2026-10-25T10:00:00Z"}
```

`time` is RFC 3339 UTC from the daemon's clock. This is **not** an envelope.

It stays `200` while every worker is down: it exists to tell an orchestrator
that the HTTP server is alive. For a client's state use
[`GET /api/v1/telegram/{key}/health`](telegram/health.md).

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/healthz" \
  --header "Accept: application/json"
```
