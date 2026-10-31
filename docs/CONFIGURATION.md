# Multi-instance configuration

The runtime reads **one JSON file**. It describes every Telegram client the
daemon should run, plus the HTTP API and process-supervision settings.

### Which file, and when it is created

| precedence | source | created automatically? |
| --- | --- | --- |
| 1 | `--config <path>` | no |
| 2 | `TELESYNC_CONFIG` | no |
| 3 | `$HOME/.telesync/config.json` | **yes, on first run** |

When neither a flag nor the variable names a file, the first runtime command —
including a bare `telesync` with no arguments — writes a minimal starter at
`$HOME/.telesync/config.json` (directory `0700`, file `0600`) and prints its
path. `telesync --help` never writes anything. The starter has `app_id: 0` on purpose —
telesync never invents a credential — so the command then reports what is still
missing. Fill in `app_id`, export the variables it names, and run again.

A path you named yourself is **never** created: a typo in `--config` or
`TELESYNC_CONFIG` is reported as a missing file instead of silently producing a
configuration at the wrong place. An existing file is never overwritten, ever.

Each client names its API hash one of two ways:

- `app_hash` — the literal hash, stored in the file; or
- `app_hash_env` — the **name** of an environment variable holding it.

Exactly one of the two, per client: setting both is rejected as ambiguous, and
setting neither is rejected. Use `app_hash_env` when the file is shared (a repo,
a container image, a backup); use `app_hash` when the file is private to the
user (`0600`, as the generated default is). The same rule applies to the phone
number via `phone` / `phone_env`.

Either way the hash never appears in diagnostics, logs, API responses or JSON
marshalling.

See [`telesync.example.json`](telesync.example.json) for a complete sample.

## Schema

```json
{
  "api": {
    "listen_addr": "127.0.0.1:8080",
    "request_timeout": "30s",
    "docs_dir": "docs/api",
    "jwt": {
      "secret_env": "TELESYNC_JWT_SECRET",
      "token_ttl": "1h"
    },
    "wordpress": {
      "encryption_key_env": "TELESYNC_WORDPRESS_KEY"
    }
  },
  "runtime": {
    "run_dir": "./run",
    "session_dir": "./data/sessions",
    "data_dir": "./data/db",
    "cache_dir": "./data/cache/avatars",
    "peer_dir": "./data/peers",
    "avatar_ttl": "24h",
    "reconnect": 10,
    "restart_initial_delay": "1s",
    "restart_max_delay": "30s",
    "max_restart_attempts": 10,
    "startup_timeout": "60s",
    "shutdown_timeout": "15s"
  },
  "clients": [ ... ]
}
```

Relative paths resolve against the directory containing the configuration file,
so a configuration directory can be moved as a unit.

### `api`

| field | default | meaning |
| --- | --- | --- |
| `listen_addr` | `127.0.0.1:8080` | public HTTP listen address |
| `request_timeout` | `30s` | bound on one upstream worker call |
| `jwt.secret_env` | — | **name** of the environment variable holding the HS256 signing key (≥ 32 bytes) |
| `jwt.secret` | — | the HS256 signing key **written in the file** (≥ 32 bytes); convenient, but anyone who can read the file has it |
| `jwt.token_ttl` | `1h` | access token lifetime (at most `24h`) |
| `auth_token_env` | — | **name** of the variable holding a fixed operator token; **loopback only** |
| `docs_dir` | none: the copy embedded in the binary. A directory only overrides it (nearest `docs/api` found beside or above the configuration file, else the embedded copy); a named directory must exist and may not overlap another data directory | the Markdown tree `GET /admin/docs` renders |

A `listen_addr` that is not loopback (`127.0.0.1`, `::1`, `localhost`) is
**rejected** unless a key comes from `jwt.secret_env` (a non-empty variable of
at least 32 bytes) or from `jwt.secret` (a literal of at least 32 bytes).
Exposing the API is therefore always an explicit decision, and a public
listener is served by per-user logins rather than a shared token.

The key is read from **one** place; setting both is refused. Either:

```bash
# A. The key in an environment variable (recommended: the file holds no key).
#    48 random bytes, base64. Export it before starting the daemon.
export TELESYNC_JWT_SECRET="$(openssl rand -base64 48)"
```

```json
"api": { "jwt": { "secret_env": "TELESYNC_JWT_SECRET" } }
```

or:

```json
"api": { "jwt": { "secret": "paste-48-random-bytes-base64-here" } }
```

The literal form is simplest for a single-user machine: it works without an
export, across restarts and under a process manager. Its cost is that the key
is in a file — treat the file like a password (it is created `0600`, and it
already names your Telegram credentials). Generate it with
`openssl rand -base64 48`.

`auth_token_env` stays loopback-only, because one token shared by every caller
cannot be revoked per person.

`telesync config validate` prints `api.jwt=(set, N chars) via TELESYNC_JWT_SECRET
ttl=1h0m0s`, or `api.jwt=(set, N chars) from api.jwt.secret in the configuration
file ttl=1h0m0s`, and never the key.

### `api.wordpress`

| field | meaning |
| --- | --- |
| `encryption_key_path` | file holding the 32-byte AES-256 key (base64 or raw) that encrypts registered WordPress application passwords; a relative path resolves against the configuration file's directory |
| `encryption_key_env` | name of the variable holding the same key |
| `encryption_key` | the same key written in the configuration file |

Set exactly one of the three. The file is the form to prefer when the daemon
is started by something other than an interactive shell (the admin panel, a
process manager, the desktop app's preview): the daemon and a later
`telesync config validate` then read the same key, whatever environment each
was started in. Create it once, `0600`, and keep it.

Optional, and independent of Telegram: without it the WordPress site routes
answer `503` and the rest of the API is unaffected. A missing variable or a
value that is not 32 bytes is a startup error naming the variable, never the
value. `telesync config validate` prints `api.wordpress=key from <file>`, `api.wordpress=key via
TELESYNC_WORDPRESS_KEY`, `… from api.wordpress.encryption_key in the
configuration file`, or `api.wordpress=disabled (no encryption key; WordPress
site routes answer 503)`.

The key must be stable: it is not generated at startup, and a different key
cannot open credentials sealed with the old one (that site must be registered
again). Generate it once with `openssl rand -base64 32`. See
[docs/api/manual/components/wordpress.md](api/manual/components/wordpress.md).

### `runtime`

| field | default | meaning |
| --- | --- | --- |
| `run_dir` | `<config dir>/run` | parent of every worker socket |
| `session_dir` | `<config dir>/data/sessions` | default parent of session files (credentials) |
| `data_dir` | `<config dir>/data/db` | local SQLite database of managed channels |
| `cache_dir` | `<config dir>/data/cache/avatars` | cached channel avatars |
| `peer_dir` | `<config dir>/data/peers` | per-worker channel access-hash memory |
| `avatar_ttl` | `24h` | how long a cached avatar is served before a refresh |
| `reconnect` | `10` | seconds between attempts to restore a Telegram connection (whole number, 1–3600) |
| `restart_initial_delay` | `1s` | first backoff delay |
| `restart_max_delay` | `30s` | backoff ceiling; must be ≥ the initial delay |
| `max_restart_attempts` | `10` | consecutive restarts before a worker is left failed |
| `startup_timeout` | `60s` | how long a worker may take to answer its socket |
| `shutdown_timeout` | `15s` | SIGINT grace period before SIGKILL |

The budget resets after a worker has been ready for twice the initial delay, so
a worker that crashes once a day never exhausts it.

While a worker runs, the daemon probes its socket every 30 s. Three consecutive
failed probes mean the process is wedged: it is stopped and replaced through the
same bounded restart path. This is a process-liveness check — a worker whose
Telegram connection dropped still answers its socket and is never restarted for
that.

### `clients[]`

| field | required | meaning |
| --- | --- | --- |
| `key` | yes | route identifier; `^[a-z][a-z0-9-]{0,63}$`, unique |
| `type` | yes | `official` or `custom` |
| `enabled` | no (`true`) | a disabled client is not started and is not routable (404) |
| `app_id` | yes | positive integer |
| `app_hash` | one of | the literal API hash |
| `app_hash_env` | one of | **name** of the variable holding the API hash |
| `phone` | no | literal login phone number |
| `phone_env` | no | **name** of the variable holding the phone number |
| `session_path` | no | defaults to `<session_dir>/<key>.json` |
| `rpc_socket_path` | no | defaults to `<run_dir>/<key>.sock`; must be inside `run_dir` |
| `peer_cache_path` | no | defaults to `<peer_dir>/<key>.json`; this worker's channel access hashes |
| `server` | custom only | forbidden for `official` |

`official` clients connect to Telegram production using gotd's bundled
datacenter list, resolver and RSA keys (`DC`, `DCList`, `Resolver` and
`PublicKeys` are left zero on purpose).

`custom` clients require `server`:

| field | default | meaning |
| --- | --- | --- |
| `host` | — | bare hostname or IP (no scheme, no path) |
| `port` | `443` | 1–65535 |
| `dc_id` | `2` | 1–5 |
| `rsa_public_key_path` | — | PEM **public** key; must exist and be a regular file |
| `transport` | `intermediate` | `intermediate`, `abridged`, `padded-intermediate`, `full` |
| `routing` | `single-dc` | `single-dc` refuses foreign datacenters; `map-all` is opt-in |
| `obfuscated` | `false` | wrap the transport in Obfuscated2 |

## Validation rules

Startup fails, naming the file and the field, when:

- the file is missing, malformed, or contains unknown fields (typos are errors);
- `clients` is empty; a key is empty, unsafe, duplicated, or uses uppercase;
- `type` is not `official`/`custom`; an `official` client has a `server` block;
  a `custom` client has none;
- `app_id` ≤ 0, `app_hash_env` is empty, or the named variable is unset/empty
  (**also for disabled clients**, so a disabled entry never hides a broken
  secret reference);
- a port, datacenter id, duration, or enum is out of range;
- the RSA key file is missing or not a regular file;
- two clients share a session path or a socket path;
- `data_dir` or `cache_dir` is inside `run_dir`, or overlaps `session_dir`
  (sessions are credentials; the database and the cache are state);
- a socket is outside `run_dir`, a session is inside `run_dir`, or a session and
  socket path are equal;
- `listen_addr` is not loopback and no JWT secret is configured;
- `auth_token_env` is set on a non-loopback `listen_addr`;
- both `api.jwt.secret_env` and `api.jwt.secret` are set, the named variable is
  unset or shorter than 32 bytes, `api.jwt.secret` is empty or shorter than 32
  bytes, or `token_ttl` is not a positive duration of at most `24h`;
- more than one of `api.wordpress.encryption_key_env`,
  `api.wordpress.encryption_key_path` and `api.wordpress.encryption_key` is set;
  the variable is unset, or the key from any form is not 32 bytes;
  `encryption_key_path` names a missing path, a directory, or an empty file.

## How to add an instance

1. Add its credentials to the environment (or an env file your process manager
   loads), e.g. `SECOND_API_HASH` and `SECOND_PHONE`.
2. Append a `clients` entry with a fresh `key`, the right `type`, and the
   `*_env` names. Leave `session_path`/`rpc_socket_path` out to use the defaults.
3. `telesync config validate` — it must say `valid`.
4. `telesync auth --key <key>` — interactive, once.
5. Restart the daemon. No route, handler, or supervisor change is needed: the
   gateway registry is built from the file.

## Session storage

Each client owns one session file (default `<session_dir>/<key>.json`), written
atomically with mode `0600` inside a `0700` directory. Session files are
credentials:

- never commit them, never copy them between keys or machines;
- `official` and `custom` sessions are separate files and never interchangeable;
- a custom session saved for a different datacenter is rejected at startup;
- to sign out, run `telesync auth --key <key>` again (or remove the file and
  re-authenticate). The worker stays up in `needs_auth` until then — it is never
  restarted in a loop because of a missing session.

## Worker socket

The private RPC socket lives in `run_dir`, is mode `0600` inside a `0700`
directory, and speaks HTTP/JSON. There is no authentication on it: filesystem
permissions are the boundary, and it is never a TCP listener. Do not put
`run_dir` on a shared filesystem or inside a web-served directory.
