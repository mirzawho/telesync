<!-- archived: the page this documentation replaced -->
# Profile

## `GET /api/v1/telegram/{key}/profile`

The Telegram account signed in for one client.

- **Authentication:** a valid credential **and** `channels.view` or any
  per-channel assignment; otherwise `403 forbidden`.
- **Method:** only `GET`.
- **Path parameter:** `key` — a configured client key, case-sensitive.

### Success — `200` (authorized)

```json
{
  "data": {
    "key": "the-cat",
    "type": "custom",
    "authorized": true,
    "authorization_state": "authorized",
    "this_dc": 2,
    "user": {
      "id": 123456789,
      "first_name": "Example",
      "last_name": "",
      "username": "example"
    }
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

| Field | Type | Present | Meaning |
| --- | --- | --- | --- |
| `key` | string | always | client key |
| `type` | string | always | `official` or `custom` |
| `authorized` | boolean | always | the account is signed in |
| `authorization_state` | string | always | `authorized`, `needs_auth`, or `unknown` |
| `this_dc` | number | omitted when 0 | current datacenter |
| `user` | object \| `null` | always | **`null`** unless the account is authorized |

`user` fields (`Account`):

| Field | Type | Notes |
| --- | --- | --- |
| `id` | number | Telegram user id |
| `first_name` | string | may be `""` |
| `last_name` | string | may be `""` |
| `username` | string | may be `""` |

### Success — `200` (not authorized)

```json
{
  "data": {
    "key": "official",
    "type": "official",
    "authorized": false,
    "authorization_state": "needs_auth",
    "user": null
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

The route still answers `200` with an explicit state; nothing is invented. An
unauthorized account is a state, not an error, and the worker stays up
reporting it until `telesync auth --key <client>` runs.

### Never returned

API hashes, phone numbers, session content, access hashes, RSA material, bearer
tokens, and the custom backend's host/port. `type` and `this_dc` are the only
facts about the connection that are public.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 401 | `unauthorized` | missing/invalid credential |
| 403 | `forbidden` | no `channels.view` and no per-channel assignment |
| 404 | `unknown_client` | key not configured/enabled |
| 503 | `not_connected` | worker up, transport down |
| 503 | `worker_unavailable` | worker socket does not answer |
| 504 | `worker_timeout` | no answer in time |

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/profile" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

Related: [Health(health.md), [`GET /api/v1/auth/me`](../auth.md#get-apiv1authme)
(this application's own account, a different thing).
