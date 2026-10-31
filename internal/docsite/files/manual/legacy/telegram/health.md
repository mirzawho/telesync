<!-- archived: the page this documentation replaced -->
# Health

## `GET /api/v1/telegram/{key}/health`

The worker process's self-report for one client. It answers three separate
questions and never blends them:

1. **Is the process answering?** — `rpc_ready` (true whenever this body is
   returned), `pid`, `started_at`, `uptime_seconds`.
2. **Is the MTProto transport up?** — `transport`.
3. **Is the account signed in?** — `authorized`, `state`, `last_success_at`.

- **Authentication:** any valid credential. No permission beyond that.
- **Method:** only `GET`.
- **Path parameter:** `key` — a configured client key, case-sensitive.

### Success — `200`

```json
{
  "data": {
    "key": "official",
    "type": "official",
    "state": "connected",
    "authorized": true,
    "transport": "ready",
    "rpc_ready": true,
    "pid": 4242,
    "started_at": "2026-10-25T09:00:00Z",
    "uptime_seconds": 3600,
    "this_dc": 2,
    "last_success_at": "2026-10-25T09:59:30Z"
  },
  "meta": {"request_id": "…", "generated_at": "2026-10-25T10:00:00Z"}
}
```

| Field | Type | Present | Meaning |
| --- | --- | --- | --- |
| `key` | string | always | the configured client key |
| `type` | string | always | `official` or `custom` |
| `state` | string | always | worker lifecycle: `starting`, `connected`, `disconnected`, `needs_auth`, `error`, `stopping` |
| `authorized` | boolean | always | a real authorized RPC probe (`users.getUsers(self)`) succeeded — **not** implied by a connection |
| `transport` | string | always | MTProto transport: `ready`, `connecting`, `disconnected`, `error` |
| `rpc_ready` | boolean | always | the worker answered this request |
| `pid` | number | always | worker process id |
| `started_at` | string \| null | always (null if the worker reported none) | when this worker process started |
| `uptime_seconds` | number | always | whole seconds since `started_at` |
| `this_dc` | number | **omitted** when unknown (0) | the datacenter the client is using |
| `last_success_at` | string | **omitted** until an authorized probe has succeeded | when the last authorized probe succeeded |
| `last_error` | string | **omitted** when empty | sanitized diagnostic |

`last_success_at` is omitted rather than fabricated: a client that connected but
never completed an authorized probe does not claim one.

### States you will actually see

| `state` | `authorized` | `transport` | Meaning |
| --- | --- | --- | --- |
| `connected` | `true` | `ready` | healthy |
| `disconnected` | `false` | `disconnected`/`connecting` | network down; the manager reconnects every `runtime.reconnect` seconds |
| `needs_auth` | `false` | `ready` | session missing, revoked or signed out; run `telesync auth --key <key>` |
| `starting` | `false` | `connecting` | process started, not connected yet |
| `error` | `false` | `error` | a failure the worker could not recover from alone |

`state: connected` with `authorized: false` does not occur; an unauthenticated
session reports `needs_auth`.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 401 | `unauthorized` | missing/invalid credential |
| 404 | `unknown_client` | `key` is not configured/enabled |
| 503 | `worker_unavailable` | the worker socket does not answer |
| 504 | `worker_timeout` | the worker did not answer in time |

Note `health` does **not** answer `409 needs_auth`: an unauthorized account is
reported in the body with `state: needs_auth` and `200`.

### Behaviour

- The worker never restarts because a request failed or the connection dropped;
  it reports the state. The supervisor restarts a worker only when its
  **process** exits or stops answering its socket (liveness probe, every 30 s,
  three consecutive failures).
- Transition logging is one line per change (connecting, connected,
  disconnected with uptime, error, unauthorized), carrying the client key and
  endpoint, never a credential.

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/health" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

Related: [`GET /api/auth/me`](../auth.md#get-apiv1authme), [`GET /healthz`](../auth.md#get-healthz) (gateway only),
[Profile](profile.md).
