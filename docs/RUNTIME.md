# Running the multi-instance runtime

The runtime is one binary in two process modes:

- `telesync daemon` — supervises worker processes and serves the public HTTP API.
- `telesync worker --key <client>` — one process, one Telegram client, one
  session, one Unix socket.

Plus two one-shot commands: `telesync auth --key <client>` (interactive, once per
client) and `telesync config validate`.

## Build

```bash
go build -o telesync ./cmd/telesync
```

## Configure

Copy [`telesync.example.json`](telesync.example.json), edit it, and export the
variables it names:

```bash
export TELESYNC_CONFIG="$PWD/telesync.json"
export OFFICIAL_API_HASH=...          # the value for app_hash_env
export OFFICIAL_PHONE=+15551234567    # optional; auth prompts when unset
export THE_CAT_API_HASH=...
```

The example is written for the container: its paths under `/data` and
`/run/secrets` exist only in the image. On a host, point `runtime.*` at local
directories and replace `encryption_key_path` with
`"encryption_key_env": "TELESYNC_WORDPRESS_KEY"` (or drop the `wordpress`
block to leave WordPress routes answering 503), or validation will report the
missing files.

Never put the hash itself in the JSON file. Validate:

```bash
telesync config validate
telesync config validate --json    # {"clients":["official","the-cat"],"valid":true}
```

Exit codes: `0` valid, `1` invalid file, `2` usage error (e.g. no config path).

## Authenticate each client, once

```bash
telesync auth --key official
telesync auth --key the-cat
```

The command validates that client's configuration, connects using **that
client's** credentials and datacenter settings, prompts for the phone number,
login code and two-step password (no echo), saves the session to that client's
`session_path`, then re-checks the session. It reports success only if the
account really is signed in. Session data, codes and passwords are never
printed.

The whole attempt is bounded by `--timeout` (default `5m`) so an unreachable
endpoint fails with "authentication timed out … before the connection became
ready" instead of retrying forever. Auth-key generation can take 10–15 s against
a slow endpoint, so do not set it far below a minute.

## Run

```bash
telesync daemon
```

Logs go to stderr and name the client on every line:

```
supervisor: official: started pid=4242
worker: client=official pid=4242 socket=/…/run/official.sock started
supervisor: official: ready
daemon: listening on 127.0.0.1:8080
```

Workers are separate processes (`ps` shows one `telesync worker --key …` each).
The daemon:

- starts one worker per enabled client, never two for the same key;
- waits for the worker's socket to answer `/health` before calling it ready;
- probes each running worker's socket every 30 s while it runs, and replaces a
  process that stops answering three consecutive probes (a wedged process is
  not healthy just because it exists); a worker that is merely disconnected from
  Telegram still answers its socket, so this never restarts it for that;
- restarts on an unexpected exit with exponential backoff (+25% jitter), from
  `restart_initial_delay` up to `restart_max_delay`;
- gives up after `max_restart_attempts` consecutive failures and leaves that
  worker `failed` while the others keep running;
- resets the budget once a worker has stayed ready for twice the initial delay;
- removes a stale `.sock` file, but never deletes a non-socket file at that path;
- on SIGINT/SIGTERM: stops accepting HTTP, sends SIGINT to every worker, waits
  `shutdown_timeout`, then kills anything still running.

A worker writes its session on the way down, and the worker process only exits
after the client goroutine has finished, so a shutdown never truncates a session
file.

### What does *not* trigger a restart

- A single failed Telegram API request — that is a request error, returned to the
  caller.
- A dropped connection — gotd reconnects in the background; the worker stays up
  and reports `disconnected`/`starting`.
- A missing or revoked session — the worker stays up in `needs_auth`. It is not
  restarted in a loop; run `telesync auth --key <client>`.

A restart happens only when the **process** exits or never becomes ready.

## Inspect

```bash
curl -s localhost:8080/admin/healthz
curl -s localhost:8080/admin/api/v1/telegram/official/health | jq
curl -s localhost:8080/admin/api/v1/telegram/the-cat/profile | jq
curl -s 'localhost:8080/admin/api/v1/telegram/official/channels?page=1&per_page=20' | jq
curl -s 'localhost:8080/admin/api/v1/telegram/official/channels/1234567890/messages?page=1&per_page=20' | jq
```

Both routes need a bearer token. Log in once and keep the token:

```bash
TOKEN=$(curl -s -X POST localhost:8080/admin/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"…"}' | jq -r .access_token)
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/admin/api/v1/auth/me | jq
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/admin/api/v1/telegram/official/health | jq
```

The token lasts `api.jwt.token_ttl` (one hour by default); log in again when it
expires — `401` with `WWW-Authenticate: Bearer … error="invalid_token"`. Create
the first account with `telesync admin` (it asks for the username and password);
see [API_V1.md](API_V1.md#initial-administrator) and
[api/local/users.md](api/local/users.md#the-first-account-created-with-the-cli).

Worker sockets answer the same operations privately:

```bash
curl --unix-socket run/official.sock http://worker/health | jq
```

## Stop

`Ctrl-C`, or `kill -INT <daemon pid>`. The daemon stops HTTP, then interrupts the
workers, then waits. Do not `kill -9` the daemon as a first resort: workers would
be orphaned. If the daemon is already gone, `kill -INT` the `telesync worker`
processes; a leftover socket is cleaned up on the next start.

## Files and permissions

| path | mode | contents |
| --- | --- | --- |
| `run_dir` | `0700` | worker sockets only |
| `run_dir/<key>.sock` | `0600` | private RPC |
| `session_dir` | `0700` | session files |
| `session_dir/<key>.json` | `0600` | MTProto credentials |

Back up session files encrypted; a copied session grants account access. Do not
place `run_dir` or `session_dir` inside a web-served directory.

## Limits worth knowing

- Numeric page numbers are an adapter over Telegram's offset/message-id
  pagination. Channel pages come from a 60-second dialog snapshot; message pages
  use a bounded (64-entry) per-channel cursor cache. Deep pages restart from the
  newest page when the worker has no cursor. Details in
  [api/pagination.md](api/pagination.md).
- Custom-server support rests only on what was verified against the real backend
  (see `.agents/memory/GOTD_NOTES.md`): `invokeWithLayer`, a no-op update
  handler, one datacenter, intermediate transport. No other behaviour is
  assumed.
- Configuration changes need a restart. There is no hot reload.

## Automatic reconnection

A client that cannot reach Telegram keeps trying on its own, with no restart and
no manual step:

- `runtime.reconnect` sets the retry interval in **whole seconds**
  (`"reconnect": 1` = every second, `"reconnect": 60` = every minute). The
  default is `10`. This matters on a filtered network: the link can be absent
  for minutes and come back the moment a tunnel is up.
- The worker logs one line per transition, on the daemon's stderr:

  ```
  telegram: client official: connecting to Telegram production
  telegram: client official: connected to Telegram production
  telegram: client official: disconnected from Telegram production (connection lost after 12m3s); reconnecting
  telegram: client official: still not connected after 10s; reconnecting with a fresh client
  ```

  Lines carry the client key and the endpoint only — never a hash, phone or
  session.
- gotd retries on its own; on top of that, if a client is still not connected
  after one interval, the whole client is rebuilt (a fresh DNS lookup and dial),
  because a long outage can leave it retrying an address that no longer works.
  A connected client is never rebuilt.
- An account that needs authorization is reported once
  (`... the account is not authorized; run 'auth --key <client>'`) and is not
  treated as a failed connection.
