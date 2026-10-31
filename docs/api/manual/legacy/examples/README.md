<!-- archived: the page this documentation replaced -->
# Examples

Every route as a `curl` command you can copy into a Bash-compatible shell.

`api.http` is the same list in the compact form that
`go test ./internal/apidocs` executes against the gateway, so an example here
cannot drift from what the code accepts.

## Before you start

```bash
export BASE_URL="http://127.0.0.1:8080"           # api.listen_addr, default
```

If the API requires a credential (`api.auth_token_env`, `api.jwt.secret_env`, or
a non-loopback listener), export a token. Either the operator token from your
environment, or a user token from login (see `POST /api/v1/auth/login` below):

```bash
export TELESYNC_TOKEN="${YOUR_OPERATOR_TOKEN_ENV}"
# or, with per-user login enabled, a token from POST /api/v1/auth/login below
```

`official` is the client key used below; replace it with one from your
configuration. `1671633563` and `1233` are example channel and message ids.

## Method / route index

| Method | Route | Captured id | Example |
| --- | --- | --- | --- |
| GET | `/healthz` | — | below |
| POST | `/api/v1/auth/login` | — | below |
| GET | `/api/v1/auth/me` | — | below |
| GET | `/docs` | — | below |
| GET | `/api/v1/telegram/{key}/health` | — | below |
| GET | `/api/v1/telegram/{key}/profile` | — | below |
| GET | `/api/v1/telegram/{key}/channels` | — | below |
| GET | `/api/v1/telegram/{key}/channels/find` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}` | — | below |
| POST | `/api/v1/telegram/{key}/channels/{channel_id}/join` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}/avatar` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}/messages` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}` | — | below |
| GET | `/api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}/media` | — | below |
| GET | `/api/v1/local/categories` | — | below |
| POST | `/api/v1/local/categories` | `category` | below |
| GET | `/api/v1/local/categories/{category_id}` | — | below |
| PUT | `/api/v1/local/categories/{category_id}` | — | below |
| DELETE | `/api/v1/local/categories/{category_id}` | — | below |
| GET | `/api/v1/local/users` | — | below |
| POST | `/api/v1/local/users` | `user` | below |
| GET | `/api/v1/local/users/{user_id}` | — | below |
| PUT | `/api/v1/local/users/{user_id}` | — | below |
| DELETE | `/api/v1/local/users/{user_id}` | — | below |
| GET | `/api/v1/local/users/{user_id}/permissions` | — | below |
| PUT | `/api/v1/local/users/{user_id}/permissions` | — | below |

`{channel_id}` and `{message_id}` are stand-ins for a channel and a message of
your own account; the curl commands below use `1671633563` and `1233`.

`{category_id}` and `{user_id}` are ids the API assigns, so they cannot be written
down in advance. The test that runs `api.http` reads them from the create
response and substitutes them in the later requests; when you follow along by
hand, use the ids you got back.

All flags are written with `--fail-with-body` (non-2xx exits non-zero and prints
the error body), `--silent --show-error` (no progress meter, errors still
shown), and `--header "Accept: application/json"`.

## Gateway and identity

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/healthz" \
  --header "Accept: application/json"
```

The credentials below are the ones the documentation test's probe accounts use.
On a real deployment, substitute your own account's, and prefer reading the
password from a file or an environment variable over putting it in a shell
history.

```bash
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/auth/login" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --data '{
    "username": "admin",
    "password": "example-doc-password"
  }'
```

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/auth/me" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

## Rendered documentation

The same Markdown, as HTML, at `/docs`. It needs a credential like every other
route: the pages describe permissions, so they are as private as the API.

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/docs" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

## Telegram client

### Health and profile

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/health" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/profile" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

### Channels

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels?page=1&per_page=5" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

A wrong page size is a useful error example — it answers `400` with
`"code":"invalid_input"`:

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels?per_page=500" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
# --data-urlencode puts an @ and spaces into q correctly
curl --fail-with-body --silent --show-error \
  --get "${BASE_URL}/api/v1/telegram/official/channels/find" \
  --data-urlencode "q=@telegram" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels/{channel_id}" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
# A join changes state on Telegram and writes a local record. It needs
# channels.create.
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/telegram/official/channels/{channel_id}/join" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
# Image bytes, not JSON: save them and look at the headers.
curl --fail-with-body --silent --show-error \
  --dump-header /dev/stderr \
  --output /tmp/channel-avatar.jpg \
  "${BASE_URL}/api/v1/telegram/official/channels/{channel_id}/avatar" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

### Messages and media

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels/{channel_id}/messages?page=1&per_page=10" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels/{channel_id}/messages/{message_id}" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
# Attachment bytes, not JSON.
curl --fail-with-body --silent --show-error \
  --dump-header /dev/stderr \
  --output /tmp/attachment.bin \
  "${BASE_URL}/api/v1/telegram/official/channels/{channel_id}/messages/{message_id}/media" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

## Local categories

```bash
# --data-urlencode also builds page/per_page/q safely
curl --fail-with-body --silent --show-error \
  --get "${BASE_URL}/api/v1/local/categories" \
  --data-urlencode "q=technology" \
  --data-urlencode "page=1" \
  --data-urlencode "per_page=10" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/local/categories" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "name": "Technology",
    "description": "Technology channels"
  }'
```

A second create with `"technology"` is the documented conflict (`409`):

```bash
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/local/categories" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "name": "technology"
  }'
```

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/local/categories/1" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
# PUT replaces: both fields, always.
curl --fail-with-body --silent --show-error \
  --request PUT \
  "${BASE_URL}/api/v1/local/categories/1" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "name": "Tech",
    "description": "All technology channels"
  }'
```

```bash
# Destructive: deletes the category and its channel assignments. Nothing on
# Telegram is touched.
curl --fail-with-body --silent --show-error \
  --request DELETE \
  "${BASE_URL}/api/v1/local/categories/1" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

## Local users and permissions

```bash
curl --fail-with-body --silent --show-error \
  --get "${BASE_URL}/api/v1/local/users" \
  --data-urlencode "q=example" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/local/users" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "fullname": "Example Operator",
    "username": "example-user",
    "password": "example password 123"
  }'
```

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/local/users/1" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
# Partial update: only the fields sent change.
curl --fail-with-body --silent --show-error \
  --request PUT \
  "${BASE_URL}/api/v1/local/users/2" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "fullname": "Example O."
  }'
```

```bash
# Destructive: removes the account; its tokens stop working on the next request.
curl --fail-with-body --silent --show-error \
  --request DELETE \
  "${BASE_URL}/api/v1/local/users/2" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/local/users/2/permissions" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

```bash
# Replaces the whole set; anything not listed is removed. channel_id and
# category_id are LOCAL ids: channel 3 is a managed_channels row created by
# POST .../join, not Telegram's channel id.
curl --fail-with-body --silent --show-error \
  --request PUT \
  "${BASE_URL}/api/v1/local/users/2/permissions" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "global_permissions": ["channels.view", "categories.view"],
    "channel_permissions": [
      {"channel_id": 3, "permissions": {"view": "allow"}}
    ],
    "category_permissions": []
  }'
```
