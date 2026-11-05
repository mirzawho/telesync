# Decisions

## D1 — Ignore `help.getConfig` addresses in the custom resolver

**Decision.** `routing.Resolver` rebuilds a one-entry `dcs.List` from
configuration on every dial and ignores the list gotd passes in.

**Why.** `Client.cfg` is replaced by the server's `tg.Config` after
`initConnection`, and `dcList()` then returns `cfg.DCOptions`. A resolver that
honoured its argument could be redirected off the backend after the first
handshake. Verified in `telegram/session.go:onSession` and
`telegram/conn_builder.go:dcList`.

**Rejected.** Passing a custom `dcs.List` via `Options.DCList` only — that fixes
the *first* dial and nothing after it. Wrapping `dcs.Plain` with a filtered list
— still trusts server data.

## D2 — `single-dc` is the default routing policy

**Decision.** Under `single-dc`, a request for any DC other than
`CUSTOM_MT_DC_ID` fails with `ErrForeignDC`. `map-all` is opt-in.

**Why.** MTProto auth keys are per-datacenter. Mapping DC 4 onto a backend that
only implements DC 2 yields undecryptable traffic or confusing server errors.
Failing loudly names the redirect and the fix.

**Rejected.** Silently mapping all IDs (masks real backend gaps); silently
falling back to `dcs.Prod()` (the exact failure mode this milestone exists to
prevent).

## D3 — CDN is refused, media reuses the configured endpoint

**Decision.** `Resolver.CDN` always errors; `MediaOnly` dials the configured
endpoint.

**Why.** A single-endpoint backend has no separate CDN network. Letting CDN
requests fall through would either fail obscurely or reach Telegram's CDN.

**Unresolved.** Whether the custom backend implements media-only semantics at
all; no backend is available to test against.

## D4 — Bundled keys are never a fallback

**Decision.** `keys.Load` returns typed errors (`ErrNotConfigured`, `ErrEmpty`,
`ErrInvalid`) and `profile.buildCustom` aborts. `telegram.PublicKeys` is always
set explicitly for the custom profile.

**Why.** `mtproto.Options.setDefaultPublicKeys` silently installs Telegram's
vendored keys when `PublicKeys` is empty. Falling back would make a
misconfigured custom profile "connect" to Telegram.

## D5 — Official profile leaves gotd option fields at zero

**Decision.** `buildOfficial` does not set `DC`, `DCList`, `Resolver` or
`PublicKeys`.

**Why.** gotd's `setDefaults` is the maintained definition of "official
Telegram". Re-specifying production IPs here would rot and would make the two
profiles look alike. Pinned by `TestOfficialUsesLibraryDefaults`.

## D6 — Separate CLI verbs, no command framework

**Decision.** Hand-rolled parsing in `internal/cli` with exactly `config check`,
`check`, `login`, `help`.

**Why.** Scope is a connectivity checker; Cobra/urfave would be pure overhead.
The `Deps` struct gives testable exit codes without a framework.

## D7 — Sessions are files, not a database

**Decision.** `sessionstore` writes `official.json` / `custom.json` atomically at
0600 in a 0700 directory.

**Why.** gotd's `session.Storage` is a byte slice API; a DB adds an operational
dependency and an extra place for credentials to leak. `FileStorage` upstream
does not create dirs or write atomically, hence the wrapper.

## D8 — Verification distinguishes connection from authorization

**Decision.** Separate `Connected` / `Authorized` / `Verified` flags, with
`users.getUsers(self)` as the probe.

**Why.** A successful TCP+MTProto session with a revoked key is common and must
not be reported as "working". The probe is the cheapest RPC that requires
authorization and returns identity.

## D9 — Interactive prompts live only in `cmd/telesync`

**Decision.** `gotdclient.Prompter` is an interface; the terminal implementation
(including the non-echoing 2FA prompt via `golang.org/x/term`) is in `main`.

**Why.** Keeps `internal/*` testable without TTYs, and keeps the one place that
handles secrets obviously located.

## Open questions

- Does the backend implement `help.getConfig` with its own `DCOptions`? The
  client tolerates any answer (resolver ignores it), but `VerifyConfig` warnings
  are only surfaced in future tooling, not in `check` output yet.
- Does the backend require Obfuscated2, or a specific transport? Configurable,
  untested against a real server.
- Does the backend accept arbitrary API ID/hash, or only its own? Configurable,
  untested.

## D10 — Always install a no-op update handler

**Decision.** `profile.Options` sets `UpdateHandler` to a function that discards
updates, for both profiles.

**Why.** gotd sets `NoUpdates=true` when no handler is installed and then wraps
every request in `invokeWithoutUpdates`. The custom backend rejects that wrapper
with `400 CONNECTION_NOT_INITED` (observed live 2026-10-23; a bare
`invokeWithLayer(initConnection(getConfig))` succeeds while the wrapped form
fails). The handler must stay a no-op: processing updates is messaging, which is
out of scope.

## D11 — `check` is time-bounded by default

**Decision.** `telesync check` applies a 60 s timeout per profile (`-timeout`),
and reports a never-ready connection as such.

**Why.** gotd reconnects with exponential backoff indefinitely, so without a bound
a misconfigured endpoint hangs forever and prints only "connecting/disconnected".
The timeout error names the likely causes instead of leaking a bare
`context deadline exceeded`.

## D12 — gotd's structured logs go to stderr

**Decision.** `profile.Options(state, logWriter)` installs a `slog`-backed gotd
logger when a writer is given; `check` always passes stderr.

**Why.** Every real diagnostic in this milestone (dial blocked,
`CONNECTION_NOT_INITED`, auth key generation timing) came from these logs. They
contain no secrets: no API hash, phone, code or session body is logged by gotd.

## D13 — One process per client, with a Unix-socket RPC boundary

**Decision.** `telesync daemon` supervises one `telesync worker --key <k>`
process per enabled client. Each worker owns exactly one Telegram client, one
session and one Unix socket; the gateway holds only `rpc.Client` handles.

**Why.** The specification requires process isolation and one long-lived client
per account. In-process goroutines would share a failure domain and a session
path mistake would be one map lookup away. A separate process makes "this worker
crash cannot take the others down" a property of the OS, and makes the socket
the only way to reach a client.

**Rejected.** One process with N goroutines (shared blast radius, no process
state to report); the gateway dialing Telegram itself (duplicate sessions, which
Telegram rejects or duplicates as `AUTH_KEY_DUPLICATED`).

## D14 — The RPC contract is a separate package, HTTP/JSON over a Unix socket

**Decision.** `internal/rpc` defines the DTOs, error codes, server handler and
client. The worker and the gateway both import it; neither imports the other.

**Why.** A shared wire contract written once prevents the two sides drifting, and
lets the gateway be tested against a fake worker without gotd. HTTP/JSON was
chosen over a custom framing because it is debuggable with `curl --unix-socket`
and needs no dependency.

**Security.** The socket is `0600` in a `0700` directory and is never a TCP
listener. There is no token on it: filesystem permissions are the boundary, and
the daemon is the only client.

## D15 — Restart on process failure only, never on a request failure

**Decision.** The supervisor restarts a worker when the process exits or never
answers its socket within `startup_timeout`. A failed Telegram RPC, a dropped
connection or an unauthorized session does not restart anything.

**Why.** gotd already reconnects in the background, so restarting on a dropped
connection would fight it and churn sessions. And a missing or revoked session
is a *state*, not a crash: restarting would loop forever and log the same error.
The worker instead stays up and reports `needs_auth` until
`telesync auth --key <k>` fixes it.

## D16 — Backoff is exponential with ±25% jitter, and a budget that resets

**Decision.** `Backoff(initial, max, attempt, jitter)` doubles per attempt up to
`restart_max_delay`; jitter 0.5 is the midpoint. A worker that stays ready for
twice the initial delay resets its budget to zero.

**Why.** Jitter stops several workers that fail together (e.g. the machine lost
its network) from retrying in lockstep. The reset means a worker that crashes
once a day is never given up on, while a worker that crashes every second is
left `failed` after `max_restart_attempts` instead of burning CPU forever.

## D17 — Configuration is JSON with secrets referenced by variable name

**Decision.** `appconfig` loads a JSON file; `app_hash_env` and `phone_env` name
environment variables holding the secrets. Unknown JSON fields are errors.

**Why.** A configuration file is likely to be committed or copied; it must
therefore never hold a hash. Naming the variable keeps the file shareable and
still lets the process resolve real values. `DisallowUnknownFields` turns a typo
like `session_dir` in a client block into a startup error instead of a silent
default.

**Consequence.** A disabled client still has to name a set variable: a broken
secret reference is a configuration bug whether or not the client runs today.

## D18 — Secrets never leave the process, including via reflection

**Decision.** Resolved secrets live in fields tagged `json:"-"`; `Describe()`
prints only variable names and shipped sizes. The gateway never returns hashes,
phones, session paths or tokens.

**Why.** The runtime has more surfaces than the CLI did: `/profile` and `/health`
are HTTP responses, and a `json.Marshal` of the config would otherwise print the
hash. Tagging the fields makes the redaction structural rather than a promise
that every future handler remembers it.

## D19 — Non-loopback binding requires a bearer token

**Decision.** `api.listen_addr` must be loopback unless `api.auth_token_env`
names a non-empty variable. The token is compared in constant time and never
logged or echoed.

**Why.** The API exposes account identity and channel titles. Making remote
exposure impossible-by-default and an explicit configuration choice is cheaper
than trusting every deployment to remember a firewall rule.

## D20 — Page numbers are an adapter, and the API says so

**Decision.** Channels are sliced from a 60-second per-worker dialog snapshot.
Messages page by message id: page 1 is the newest, and page N+1 continues below
the oldest id served, tracked in a 64-entry per-channel cursor cache. A deep page
with no cursor restarts from the newest page.

**Why.** Telegram has no numeric page index, and manufacturing one by walking
the whole history per request would be both slow and rate-limited. The cache
bounds memory and RPC cost. The limitation is documented in
`docs/api/pagination.md`
rather than disguised: `meta.page` echoes the request, and callers that need
exact resumption use message ids.

**Rejected.** Unbounded per-channel cursors (memory grows with account size);
re-walking from the newest message for every page (correct but O(page) RPCs).

## D21 — Health reports evidence, never inference

**Decision.** `authorized` is set only from a successful authorized probe, and
`last_success_at` is omitted until one has happened. `transport` is reported
separately from `state`.

**Why.** "The process answered" and "the connection is up" and "the account is
signed in" fail for different reasons and are routinely conflated. The adapter
already distinguished them; the DTO carries the distinction to HTTP. A connected
transport with no successful probe previously would have reported the current
time, which is exactly the kind of fabricated success the spec forbids — hence
`Manager.Wait`/`Status.LastSuccess` and the `LastSuccessAt == nil` test.

## D22 — The worker flushes its session before exiting

**Decision.** `RunWorker` cancels the client and waits (10 s) for
`Manager.Wait` before returning. `Manager.Wait` closes only after gotd's client
goroutine and its final session write have finished.

**Why.** gotd persists the session during shutdown. Exiting as soon as the
socket server returns could truncate that write. The same race made a test's
`TempDir` cleanup flaky; waiting fixed it there too.

## D23 — Liveness probing covers a wedged process, not a lost connection

**Decision.** While a worker runs, the supervisor probes its socket every
`LivenessInterval` (30 s in the daemon) and replaces the process after
`LivenessFailures` (3) consecutive failures, through the normal bounded restart
path.

**Why.** "The PID exists" is not "the worker is healthy": a worker whose RPC
loop is stuck cannot answer any request, and without this it would sit there
forever while the gateway answered 503. The probe targets the RPC socket, not
Telegram, so a dropped Telegram connection (which the worker still reports
correctly) never triggers a restart — that would violate D15.

## D24 — Delete the panel and the legacy checker outright

**Decision.** On 2026-10-24 the single-account admin panel (SQLite store, HTTP
API, browser frontend), the two-profile connectivity checker and their CLI verbs
were removed rather than left dormant.

**Why.** The user asked to build on the multi-client runtime only and stated the
panel and the old sources are not wanted. Keeping them would keep a second
config schema (`OFFICIAL_*` env vars), a second HTTP surface and a second
session naming convention alive, and every future reader would have to work out
which of the two halves is real. Dead code that still compiles is not free.

**Kept because the runtime needs it.** `keys` (PEM loading), `routing` (custom
endpoint isolation), `sessionstore` (0600/0700 storage), `login` (the auth
command's state machine), `tgapi` (library-independent DTOs), `profile`
(`FromClient` only) and `gotdclient` (Manager + a one-shot auth flow).

**Consequence.** `internal/tgapi` no longer defines a `Client` interface: with
one consumer there is nothing to abstract. The worker declares the narrow
`worker.Telegram` interface it needs, and the gateway declares its own `Worker`
interface. Interfaces live with their consumer.

## D25 — Create the config on first run, but only at the default path

**Decision.** With no `--config` and no `TELESYNC_CONFIG`, the first runtime
command writes a minimal starter to `$HOME/.telesync/config.json` (dir `0700`,
file `0600`, temp-file + rename), prints its path on stderr, and then reports the
fields still missing. A path named by a flag or variable is never created.

**Why.** A first run with no configuration used to be a dead end: the command
exited 2 and told the user to "point TELESYNC_CONFIG at the JSON configuration
file", with no file to point at. Generating a starter makes the first run
actionable. Restricting creation to the default path is the other half: if
someone types `--config ~/.telesync/confg.json`, creating that file would hide
the typo and produce a client that silently never runs.

**Never invented.** The starter has `app_id: 0`, one `official` client and no
secret values — only `*_env` variable names. Validation then fails with exactly
what to fill in, which is honest: telesync cannot know a real app_id.

**Never overwritten.** An existing file is left byte-for-byte untouched whatever
it contains, so a half-edited configuration is never replaced by the template.

## D26 — A client may hold its API hash literally or name a variable

**Decision.** Each client supplies its hash as either `app_hash` (the literal
value) or `app_hash_env` (the name of the variable holding it). Exactly one;
both is an error ("set either … not both"), neither is an error. The same pair
exists for the phone number.

**Why.** The environment-only rule protected a configuration file that might be
committed, but it made the common case — one user, one private `0600` file under
`$HOME/.telesync` — needlessly awkward: the file could never be self-contained.
Supporting both keeps the safe option available without forcing it.

**Still never leaked.** `app_hash` is `json:"-"` on the resolved struct,
`Describe()` prints `app_hash=(literal)` instead of the value, and marshalling
the config omits it. Tests pin both the acceptance and the absence from output
(`TestLiteralAppHashIsAccepted`, `TestLiteralHashNeverAppearsInOutput`).

**Ambiguity is rejected rather than resolved by precedence.** If both fields are
set, the user's intent is unclear, and silently preferring one would hide a
stale value.

## D27 — Media is described in the page and fetched by a separate URL

**Decision.** A message's `media` object carries `id`, `type`, `label`,
`mime_type`, `size` and `url`. `url` is the API path
`/api/v1/telegram/{key}/channels/{cid}/messages/{mid}/media`, which streams the
bytes. Listing a page never downloads anything.

**Why.** Downloading every attachment while listing history would be slow,
memory-hungry, and would spend Telegram's flood budget on images nobody asked
for. Two endpoints keep each cheap and let a browser or client fetch lazily.

**`url` is a path, not an absolute URL.** The response stays correct behind any
host, port or reverse proxy, and does not leak the bind address.

**Shape.** The channel is in the path and the message is the attachment (one
attachment per message in this API's scope), so `:id` is the message id; the
`media.id` field repeats it as `"<channel>:<message>"` for clients that want a
single opaque key.

**Bounded.** One download is capped at 32 MiB, so a single request cannot
exhaust worker memory. Photos download the largest `PhotoSize`; documents use
their own file reference and MIME type.

## D28 — Membership and local selection are separate facts

**Decision.** `is_member`/`membership_status` come from Telegram and are reported
fresh on every read; `is_managed` comes from the local database. `/channels` and
`/channels/find` never read or write the database's content — `find` only reads
the id set to fill `is_managed`.

**Why.** A user's Telegram account can be in a hundred channels while the
application manages three. Treating dialogs as managed would make the selection
meaningless, and treating the database as a channel cache would make every read
depend on a mirror that drifts from Telegram. The database records one thing
only: an explicit `join` instruction, which has to survive a restart.

**Consequence.** Nothing is ever recorded as a side effect of reading. Adding a
channel to the managed set requires calling `POST .../join`.

## D29 — The database and the avatar cache belong to the daemon

**Decision.** The SQLite store and the avatar cache are opened by the daemon
process and injected into the gateway. Workers never open the database; each
keeps only its own access-hash file under `runtime.peer_dir`.

**Why.** One writer means no competing copies per client and no cross-process
lock games on SQLite. The worker stays what it was: one Telegram client and a
socket. The gateway is already the only component that knows every `{key}`, so
it is the natural owner of keyed state.

**Partial failure is reported, not hidden.** A confirmed join whose local write
fails answers `500` with "membership is confirmed but the record was not saved;
retry to record it". The retry takes the already-a-member path and finishes the
write. Framing the two systems as one transaction would be a lie.

## D30 — One permission normalizer, conservative when unsure

**Decision.** `tgapi.NormalizePermissions` is the only implementation; the
adapter feeds it from Telegram's `channel` object (creator, admin rights,
banned rights, default rights, `left`, `broadcast`) and every endpoint renders
the same four booleans.

**Why.** Independent per-handler calculations inevitably disagree. The fields
must be booleans, so an unestablished value is `false`, never an optimistic
`true`: a non-member gets nothing, an unknown standing gets nothing, and a
restricted member loses posting. `edit`/`delete` describe the channel-level
right, and the documentation says plainly that a specific message may still be
refused by Telegram.

## D31 — Avatars: 24 h server-side cache, photo version as the validator

**Decision.** Bytes are cached on disk for 24 h under
`<cache_dir>/<client_key>/<channel_id>/`, written by temp file + rename with the
metadata sidecar last, one download per key in flight, and `ETag` from
Telegram's `photo_id`.

**Why.** A browser `Cache-Control` alone would re-download whenever a client
skips its cache, and file names from the remote cannot be trusted. Keying on
(client, channel) makes cross-client leakage structurally impossible, and the
photo version both invalidates a changed avatar and is a correct `ETag`.

**Stale policy.** An expired entry is refreshed; if that fails and the entry is
within 6 h of expiry it is served with `Warning: 110` and `max-age=0`. Beyond
that the answer is an error, not a months-old picture. A failed download is
never cached.

## D32 — Reconnection is a fixed interval, plus a client rebuild

**Decision.** `runtime.reconnect` is a whole number of seconds (default 10). The
worker passes it to gotd as the reconnection policy and also runs a watchdog: a
client that is still not connected one interval after a generation started is
destroyed and rebuilt.

**Why.** The reported problem is a filtered network: the source runs with no
route at all, a tunnel comes up later, and gotd (which re-resolves and redials on
its own) leaves the client stuck. A rebuild forces a fresh DNS answer and dial
from the operator's current route. A connected client is never rebuilt, and an
unauthorized account is a state, not a failure, so it is neither rebuilt
uselessly nor logged on every attempt.

**Logging.** One line per transition (connecting, connected, disconnected with
the time it stayed up, error, unauthorized), emitted from the state the worker
also reports — not from gotd's per-attempt callbacks — so a flapping link cannot
flood the log. Lines carry the client key and endpoint, never a credential.

**Config shape.** A bare number, because that is the unit the operator thinks in
("1" = every second); a duration string is rejected rather than silently parsed.

## D33 — Categories live in SQLite, outside the client key, unique ignoring case

**Decision.** Channel categories are a local table (`categories`) plus a
many-to-many `channel_categories (client_key, channel_id, category_id)`. The
HTTP surface is `/api/v1/local/categories`, not namespaced by a client key; the
assignment table carries the key.

**Why.** A category is an operator's label, not a Telegram folder: nothing is
written back to Telegram, so it cannot be scoped per account, while "this
channel, for this account, is in this category" must be. Names are unique with
`UNIQUE(name)` and `COLLATE NOCASE` — the rule lives in the schema, so a race
cannot create two `Technology` rows, and the match is ASCII-insensitive exactly
as SQLite's own `NOCASE` is (documented; non-ASCII case variants stay distinct).

**Why no foreign key to `managed_channels`.** A category may reference a channel
this application never joined (the gateway reports `channel_id` as a string, and
managed rows are not a precondition for filing one). Omitting the reference is
also what makes the deletion rule structural: `ON DELETE CASCADE` reaches only
assignment rows, so deleting a category cannot remove channel data.

**Enforcement.** `foreign_keys(1)` is in the DSN so every pooled connection gets
it, and `SetMaxOpenConns(1)` keeps one connection. Both are pinned by tests, and
the cascade is covered end to end (assign → delete category → assignment gone,
managed channel intact; deleting the FK's `ON DELETE CASCADE` makes the test
fail with a FK constraint error).

**Contract choices.** `PUT` replaces (both fields required); `POST` answers 201;
duplicate name is `409 conflict` with code `conflict`; deletes answer
`{"deleted": true, "id": n}`; unknown body fields, trailing JSON and non-JSON
content types are rejected rather than ignored.

## D34 — Permissions are per resource table, never polymorphic

**Decision.** Three tables: `user_permissions` (global names), and
`user_channels_permission` / `user_categories_permission`, each with a real
foreign key to the local resource table, `permission IN ('view','update','delete')`,
`effect IN ('allow','deny')`, `UNIQUE (user_id, resource, permission)` and
`ON DELETE CASCADE` on both sides. A new resource type gets its own table with
its own FK; `channelstore.Authorizer` is the one evaluator.

**Why.** A polymorphic `(resource_type, resource_id)` column cannot be checked
by SQLite, so it cannot cascade and cannot refuse a stale id. The split also
keeps each permission check a two-column index lookup, and the schema exposes
the rule "creation is global" because `create` is simply not an allowed action
in a resource table.

**Rejected.** Columns on `users` (a migration per resource); a single
`user_resources_permissions` table (no FK, and the cascade is the cleanup rule);
role names (the requirement is per-resource sets, which roles model badly).

## D35 — Resource ids are local ids; Telegram ids are resolved first

**Decision.** Every permission names `managed_channels.id` or `categories.id`.
A Telegram route resolves its `{channel_id}` to the local row
(`client_key, telegram_id`) and checks the permission against that row's id. A
channel with no local row has nowhere to store an assignment: only the global
`channels.view` admits it.

**Why.** The alternative — storing Telegram ids — would silently grant access
across client keys for the same channel, and would compare ids from two
namespaces. Resolving first keeps one id space for assignment and makes the
documented "local vs Telegram id" rule enforceable (a Telegram id in the
permissions API is `400`, because no local row has it).

**Rejected.** Matching by Telegram id with the client key as an extra filter
(still two namespaces in one column); auto-creating a local row on a read (it
would turn a read into a write, and the API promises reads never write).

## D36 — Authorization denies by default; global view is the only list grant

**Decision.** For a resource: explicit assignment decides, then a global
`<kind>.view` grants view only; otherwise deny. Update and delete are always
per resource. `create` is global only. Lists are narrowed to granted resources
when the caller lacks the global view, and `total` counts the narrowed set.

**Why.** The requirement enumerates exactly these rules, and the two
constraints fall out of them: a resource that does not exist cannot carry an
assignment, and "missing assignment is never a grant". Restating `total` after
filtering is what keeps pagination honest; `has_more` stays Telegram's, because
the next page is Telegram's to produce.

**Rejected.** A global update/delete permission (would make per-resource
`update: deny` pointless for an administrator); returning the unfiltered total
(leaks how many hidden resources exist).

## D37 — The fixed operator token is loopback-only and cannot use the user API

**Decision.** `api.auth_token_env` keeps working, but only when `listen_addr` is
loopback; on any other address the loader refuses it and requires
`api.jwt.secret_env`. The token is checked before the JWT, holds every
permission, and is refused by every `/api/v1/local/users*` route.

**Why.** Deployments and the existing tests rely on the token; removing it
would break them without a migration path. But one shared secret cannot be
revoked per person and should not travel on a network, and it has no identity
to own accounts, so the user API stays closed to it.

**Rejected.** Dropping the token outright (breaks existing deployments);
letting it manage users as a pseudo-account (an unrevocable superuser).

## D38 — Separate `hash-password` command; no bootstrap command yet

**Decision.** `telesync hash-password` prints an Argon2id PHC hash of the
password read from stdin. The first administrator is inserted with that hash in
one documented SQL statement (`docs/api/local/users.md`); there is no
`bootstrap-admin` command.

**Why.** The hash step needs nothing but the binary, so it is a real command.
Creating the account needs the database path and the schema, and every rule
about it (idempotence, refusing to escalate, minimum grants) would be new
surface for one use; the SQL is short, exact and cannot silently escalate. The
gap is stated in the docs rather than papered over.

**Rejected.** Documenting a command that does not exist (the first draft did;
the docs were corrected to match the code).

## D39 — `telesync admin` is the administrator command; D38's SQL is withdrawn

**Decision.** `telesync admin [--config <path>]` asks for username, full name
(optional, defaults to the username) and password twice, hashes the password in
process, and creates or promotes the account with **every** global permission.
It creates the database if missing. Re-running it for an existing username
reactivates the account, replaces the password and adds missing permissions;
nothing is ever removed and no duplicate is created. This supersedes the SQL
insert documented under D38 (kept only as history).

**Why.** The user asked for exactly this: a CLI-defined administrator with full
access that takes a username and password. It removes the hand-written SQL,
which is the step most likely to be mistyped, and makes password recovery a
normal command instead of a database edit.

**Rejected.** Asking only for `users.manage` + `users.view` (the request is
"full access"); a non-interactive `--password` flag (it would put the password
in the process list and shell history; stdin piping is the scriptable path).

## D40 — The JWT key may be a literal; login never trusts an interface nil

**Decision.** `api.jwt.secret` (the key written in the file, ≥ 32 bytes) is
accepted as the alternative to `api.jwt.secret_env`; setting both is refused and
`config validate` says which is in use without printing it. Separately,
`auth.Missing` treats a typed-nil signer (e.g. `(*auth.Tokens)(nil)` in the
interface field) as absent, and login answers `503` instead of issuing on nil.

**Why.** The operator asked for the literal form so a single-user deployment
needs no export. The panic behind the request was an interface holding a nil
pointer: `s.cfg.Tokens == nil` is false for it, so login dereferenced nil and
Go's http server dropped the connection. The literal form makes a key *in the
file* a supported choice; the documented cost is that the file must then be
treated as a secret (it is `0600`).

## D41 — Local channel routes address the managed_channels row, and only an account may leave

**Decision.** `/api/v1/local/channels/{id}` resolves `id` as the
`managed_channels` primary key and then follows the existing channel rule
against that local id: `channels.view` admits every record except an explicit
`view: deny`, otherwise only an explicit `view: allow` does. `leave` requires a
per-channel `delete` assignment. Messages and the avatar go to the worker named
by the row's `client_key` with the row's `channel_id`; the request never names
a Telegram id. `leave` deletes with `DeleteChannelRow` only — the row's
permission assignments go by `ON DELETE CASCADE`, and `channel_categories`
(client key + Telegram id, no FK to the row) is deliberately left alone.

**Why.** A local id and a Telegram id are different namespaces and must never
be interchangeable (the request says so, and the schema already keys
assignments by the local id). Reusing the row's identifiers is what keeps the
client that answers identical to the client that recorded the channel — no
fallback to another account. Leaving the category assignment alone follows D25:
a category may name a channel this application does not manage.

**Rejected.** A second table or a messages table (the record exists, and
Telegram is the source of truth for history). Accepting the operator token on
`leave`: it has no account and therefore no per-channel assignment to check, so
a deployment that must leave from a host without login does it as an account.

## D42 — `leave` is not atomic across Telegram and SQLite; it is ordered and reported

**Decision.** With `telegram: true` the order is: resolve the row, leave on
Telegram, then delete the row. A Telegram failure leaves the row untouched so
the request is retryable. A delete failure after a confirmed leave answers
`500 partial_failure` and names the retry (`{"telegram": false}`). Concurrent
leaves of one id are serialized in-process (`acquireLeaveLock`). The Telegram
call runs on a context detached from the request (`context.WithoutCancel` +
`request_timeout`), so a client that disconnects cannot abort a leave Telegram
has already recorded.

**Why.** There is no distributed transaction; pretending otherwise would either
delete a row whose Telegram leave did not happen or claim success for a row that
still exists. The ordering makes the failure modes retry-safe and the partial
failure truthful, which is what the API contract asks for.

**Rejected.** Deleting first and leaving after (loses the identifiers on
failure); wrapping both in one `sql.Tx` (the network call would hold the
single SQLite connection for the request).

## D43 — The admin panel's shell is public; the session is the browser's

The gateway mounts the panel as an optional `AdminSite` and serves `/admin` and
`/admin/*` before authentication, like `/docs`. The server never checks a
credential for the shell; the shell itself checks the stored token against
`GET /api/v1/auth/me` and shows the sign-in page when it is missing or rejected.

**Why.** The shell carries no account data — the three routes are placeholders —
and the API's own permission checks decide everything a signed-in caller can
read. Requiring a credential at the shell would duplicate the API's auth in a
second place, and a cookie session would need a server change this task was
explicitly not to make.

**Rejected.** A cookie/session layer in the gateway (new server state, CSRF
handling, a redirect flow); requiring `Authorization` on `/admin` (a browser
address-bar load cannot send one).

## D44 — The token lives in `localStorage` under `#telesync/token`

`POST /api/v1/auth/login` returns `access_token`; the panel stores it under the
key the operator asked for, sends it as `Authorization: Bearer`, and removes it
on sign-out. An expired or revoked token leads to the login page with a notice;
an unreachable server does not discard a token that has not been refused.

**Why.** The key is an explicit requirement. The distinction matters: a 401 is
"sign in again", a network failure is "try later", and treating the second as
the first would silently sign people out during an outage.

**Rejected.** `sessionStorage` (a new tab would sign in again); a cookie (the
server has no cookie session, and adding one is an authentication change).

## D45 — WordPress credentials are sealed with AES-256-GCM, never hashed

**Decision.** `internal/secrets` seals every registered WordPress application
password with AES-256-GCM (32-byte key, 12-byte random nonce, version tag `1`).
The box (version, nonce, ciphertext, sealed-at) is stored in `wordpress_sites`;
the key comes from `api.wordpress.encryption_key_env` (or a literal in the file)
and never from the database, and nothing generates it at startup. Without a key
`config validate` fails and the WordPress routes answer `503` — a credential is
never stored to fall back on.

**Why.** The password has to be presented to WordPress later, so a hash cannot
work. GCM is in the standard library, authenticated (a tampered row is refused
rather than opened) and versioned for a future rotation. A startup-generated
key would silently make every earlier credential unreadable.

**Rejected.** A new crypto dependency; per-row keys (no rotation story); a
`hash+encrypt` combination (the hash adds nothing once the ciphertext is
authenticated).

## D46 — Site registration is validated against the site, then written once

**Decision.** `POST` and `PATCH` normalize the URL, call the site's
`wp/v2/settings` with Basic auth through `internal/wordpress`, resolve
`site_icon` as an attachment id via `wp/v2/media/{id}`, and only then store.
`PATCH` builds the next record from the stored one (omitted fields and the
sealed credential are kept), probes with the new URL / username / password, and
writes once — so a failed rotation or domain change leaves the old record
exactly as it was. The title and icon are always taken from the answer.

**Why.** "Do not create a partial record" and "preserve the existing valid
record" are the same rule: validate first, write once. Reading the icon as an
id is what stops a numeric attachment id from being shown as an image URL.

**Rejected.** Writing first and rolling back on failure (two writes, a window
where a broken credential is live); resolving the icon lazily on read (the
`GET` route would then have to reach out to the site).

## D47 — Outbound site requests are https-only, public-only, redirect-free

**Decision.** `wordpress.NormalizeBaseURL` refuses anything but a public
hostname on `https` with an optional path prefix — no port, no userinfo, no
query, no fragment, no IP literal, no `.local`/`.internal`/metadata names. At
dial time the client resolves the name once, refuses the connection unless
every answer is public, and connects to exactly the addresses it checked; no
proxy, no redirect (`ErrUseLastResponse` is reported, not followed), 10 s total
timeout, 1 MiB body, JSON only.

**Why.** This is the one place an operator-supplied URL becomes a request. The
URL check stops the obvious cases; the dial check is what stops DNS rebinding,
because it is the connection itself that is validated.

**Rejected.** Following redirects and validating each hop (the WordPress routes
never redirect); using the environment's proxy (`HTTPS_PROXY` would decide the
destination); allowing `http` behind a configuration switch (the request
carries a credential).

## D48 — Sites are written with one global permission; the list is per-site

**Decision.** `GET /api/v1/local/wordpress` lists every site for a caller with
`wordpress.view`, and only the sites with an explicit per-site `view: allow`
otherwise. `POST`, `PATCH` and `DELETE` need the global `wordpress.create`
(`PermissionWordPressManage`). Per-site `user_wordpress_permission` rows exist
and are reported, but their update/delete effects grant nothing.

**Why.** A per-site update grant would let an account replace that site's
credential, which is a capability equivalent to managing it; a per-site view
grant is harmless and useful. The name `wordpress.create` keeps the
`<kind>.<action>` shape the permission model derives from a kind.

**Rejected.** A separate `wordpress.manage` constant outside the derived shape;
per-site write grants (see above).

## 2026-10-29 — Manage requires view; an empty permission picker means "all"

- 2026-10-29 — A storing permission needs its viewing permission
  (`users.manage`⇒`users.view`, `channels.create`⇒`channels.view`,
  `categories.create`⇒`categories.view`). **Why:** a manager who can act on
  users/channels/categories but not see them has no working screen; the panel
  enforces it by auto-checking and the gateway refuses an inconsistent set so
  other clients are held to the same rule. **How to apply:** keep this in
  `permissionSetOf`, not only in the dialog.
- 2026-10-29 — An empty channel/category picker stores no per-resource rows,
  which (with the global view permission) means every item; picks store
  `view: allow` rows and are authoritative, so clearing them removes earlier
  restrictions. **Why:** the API replaces the whole set, and the authorizer
  already treats "global view, no per-item rows" as "all". **How to apply:**
  do not add an "allow all" expansion; the absence of rows is the grant.

## D49 — A WordPress site publishes into the global categories; the link is its own table

**Decision.** Migration 5 adds `wordpress_site_categories (site_id, category_id)`
with `ON DELETE CASCADE` on both sides, so it reuses the `categories` table
channels are filed under rather than introducing a second kind. A site's views
carry `category_ids`; `POST` files the site, and `PATCH` takes exactly one of
`category_ids` (replace), `add_category_ids`, `remove_category_ids` (toggle one
without listing the rest). `GET /api/v1/local/wordpress/{id}` was added so a
site can be read on its own. A `PATCH` that only changes categories does not
contact the remote site; one that changes the record rewrites it and then
applies the category set, which is what its PATCH body already carries.

**Why.** The operator asked for the same global categories, so channels filed
under "News" and sites publishing "News" refer to one id. Toggling one category
through a whole-set replace would race with another reader of the list; the
two explicit forms say what the caller means.

**Rejected.** A separate `wordpress_categories` table (a second, parallel
classification the operator would have to keep in step).

## D50 — The gateway sees the link through an optional interface

**Decision.** `gateway.WordPressStore` stays the five site methods; the link is
`gateway.WordPressCategories` (ReplaceSiteCategories, SiteCategories,
CategoriesOfSites), found by type assertion. A store that does not implement it
reports `category_ids: []` and refuses a request that names categories.

**Why.** A test double that only stores sites keeps compiling. The cost is one
trap, recorded here: an interface holding a bare `*channelstore.Store` does not
satisfy the link interface, so the gateway's tests hold a small struct that
embeds the store (`siteStore`). The real store is passed by value in the daemon,
where it does satisfy it.

## D51 — The server status page is its own permission; the selector stays open

**Decision.** `GET /api/v1/telegram` (which also feeds the dashboard's "وضعیت
سرورهای تلگرام" card) lists every
enabled Telegram server with the selector's row plus `user`, the server's own
Telegram account (`id`, `first_name`, `last_name`, `username`; `null` when not
known). It needs the new global `servers.view` (`PermissionServersView`,
`channelstore.KindServer`), checked in the handler with
`actorAuth(r).Global(...)` so the rule sits beside the code that writes the
page. `/auth/me` reports it as `access.servers`. `GET /api/v1/telegram` keeps
needing only a token: the channel pages use it to pick a server and it names no
account's data.

**Why.** The connected/not-connected question ("وضعیت سرورها") is operator
infrastructure, and the operator asked for it to be grantable per administrator.
The identity is account data, so it belongs behind a right of its own rather
than in the selector every channel reader already calls. A failing profile
lookup leaves `user` null and never fails the page; `state` still says
`connected`, so a connection is never hidden by a missing identity.

**Rejected.** Filtering the existing listing by a permission (it would silently
break the channel pages for accounts without it); per-server assignments (the
servers come from configuration, not a local table, so there is no id to
grant).

## D52 — One visibility rule for every resource; a write needs the view

**Decision.** Every route that returns or names a channel, category or
WordPress site asks the same question, `visible(authr, kind, action, localID)`
(`internal/gateway/access.go`), which is `Authorizer.Allowed` over the id the
per-resource assignment names (`0` when there is no local record). The rules
are: an explicit per-resource deny overrides the global grant; without a global
grant only an explicit allow admits; a resource with no record is admitted by
the global view alone; denied by default. `Authorizer.Allowed` now also requires
the view for update and delete, so a `update:allow`/`delete:allow` (or a bare
global view without a per-record allow) never reveals a record, and an explicit
`view:deny` takes every write away with it.

**Why.** The reported bug: with a global `channels.view` and one
`view:allow` the local channel list fell into the "restricted" branch, which
listed only the explicit allows — `test` saw Channel A and not Channel B — but
the same branch was skipped for a global view carrying denies, the category's
channel list returned every member channel unfiltered, the channel's category
list could name categories the caller may not see, and WordPress
update/delete answered before reading the record, so the global manage right
edited any site. Two different definitions of "may see" had grown beside each
other; there is now one.

**How to apply.** New list routes: filter before paging and count
(`listLocalChannels`, `applyChannelFilter`); a global view narrowed by denies
uses `ChannelExcluder.ListChannelsExcept` so the denial reaches the query.
Detail and nested routes: authorize before the body is decoded and before the
worker is called (leave, WordPress PATCH/DELETE). Nested resources check the
parent and the child (`category /channels`, `channel /categories`, filing).
`ResourceIDs` counts a resource whose `delete` cannot be granted (WordPress) by
its `view:allow`, so a global list narrowed by denies keeps it.

## D53 — The git tag is the version, written into the panel's package.json

**Decision.** `make version v1.0.2` is the release: `web/scripts/version.mjs`
writes `1.0.2` into `web/apps/web/package.json` and creates the tag `v1.0.2`
at HEAD. The panel imports that file (`import { version } from
"../../package.json"`) and shows `نسخه v<version>` on the sign-in page and in
the sidebar header. `make build` never chooses a version: it builds the file as
it is, and only a file without a version falls back to the nearest git tag.
The placeholder `0.0.1` means unreleased and shows nothing. The image build is
handed the tag by the workflow, which stamps the file before building the
panel.

**Why.** The user asked for exactly this chain: one command that writes
package.json and tags, then the panel importing the file. The file wins over
the tag so a checkout without the tag (or without `.git`) still shows its
release; the tag is only a fallback. A build context has no `.git`, so the
container build must be told the version rather than read it.

**Rejected.** A `GET /api/v1/version` route and a `telesync version` command
(built first, then removed on the user's correction: they asked for the panel,
not a new API route); deriving the version from the panel bundle only at run
time; `git describe --dirty` (the build rewrites package.json, so every build
would be dirty).

## D54 — Views and reactions come from the message Telegram returned; never a method on `rpc.Message`

**Decision.** `views` and `reactions` are read from the `tg.Message` of the
request that returns it (`message.views`, `message.reactions.results`), carried
through `tgapi.Message` → `rpc.Message` → the public `messageView`, and never
stored. `views` is `*int` (null = Telegram did not report it) and `reactions`
a named slice rendered as `[]`; a reaction keeps `type`, `emoji`,
`document_id` and `chosen` so a custom emoji is never read as a standard one.

**Why.** The API is read-only and holds no message table, so there is nothing
to go stale; a stored copy would also need a migration and a refresh policy.

**Rejected.** `MarshalJSON` on `rpc.Message` to render nil as `[]`: Go
promotes a method of an embedded type onto the embedding struct, so
`gateway.messageView` would lose its `media.url` (observed: every field of the
message vanished). Normalization lives in `rpc.Client.messagePage` instead.

## D55 — The product is content management and distribution; "not implemented" is not "out of scope"

**Decision (owner, 2026-10-30).** telesync is a Telegram *content management and
distribution* system. Its intended workflow is acquire → manage → read → select
→ prepare → publish: a message from a **source** connection is prepared as an
outgoing post (hashtags, mentions and main text edited; the source message left
untouched) and published to a **destination** — a channel on a destination
connection, or a registered WordPress site. `PROJECT_BRIEF.md` is the canonical
product definition; the status of every capability is its §9 matrix.

**Supersedes, partially:** the "Out of scope (do not add): sending, forwarding,
copying, mirroring or publishing messages … any web UI, admin panel … SQLite or
any database" list of the original brief, and the matching paragraph in
`AGENTS.md`. The panel and SQLite have existed since 2026-10-29; publishing is
now intended product. The original brief is kept in history here, not as a
current instruction: its security and isolation rules stay in force, its
prohibitions on publication and web UI do not.

**Still true.** The content API is read-only today; reading never marks
messages read; messages are not stored; no publishing route, send call,
content model or WordPress post writer exists. This decision changes what the
product is for, not what the code does.

**How to apply.** Do not refuse a publishing/preparation request as "out of
scope". Do not claim it is implemented. Build it in the existing architecture
(rpc → worker → gotdclient; the gateway's authorization rule; generated docs)
when asked, and label reports VERIFIED IMPLEMENTATION FACT / EXPLICIT PRODUCT
REQUIREMENT / PROPOSED DESIGN / UNKNOWN.

**Open (not answered by this decision):** per-destination publish rights vs a
global `content.publish`; `clients[].role` in configuration; the media policy
for unsupported destinations; how mentions map to a destination channel; whether
publication history, scheduling or bulk publishing are requirements. See brief
§11.

## D56 — Source and destination are business roles, not connection types

**Decision.** A connection's *type* (`official`/`custom`, how it reaches
Telegram) and its *role* (source/destination) are independent. The owner's
"Cat" is the destination backend role; the sample configuration names it
`the-cat`. Source and destination share the gotd adapter, worker and RPC path
deliberately, but differ in data direction, in what must be validated (a read
capability is not a post capability) and in endpoint isolation.

**Why.** The earlier documentation treated the custom client as "just another
read source"; that hid the product's destination side and invited treating
"can read" or "is a member" as "can post", which Telegram does not guarantee.

**How to apply.** Never hardcode `telegram`, `cat` or `the-cat`: read
`clients[].key`. A future publish route validates the *destination account's*
posting capability from Telegram data at publication time. Do not add a `role`
field, a publisher service or a second adapter until the owner decides (brief
§11).

## D57 — The real frontend is `web/`; the root `panel/` is a stale scaffold

**Decision.** The panel is a React Router 8 / React 19 / Vite / TypeScript /
Tailwind application under `web/`, with `@workspace/ui` (Base UI) components,
Persian RTL; `web/scripts/build-panel.mjs` builds it into `internal/panel/files`
which the binary embeds at `/admin`. The repository-root `panel/` is an early
shadcn template that nothing builds and that is behind `web/`.

**Why.** Both trees exist and the scaffold has a root package also named `web`,
so a reader (or a prompt) can pick the wrong one. This project is not Next.js.

**How to apply.** Edit `web/` only; take components from `web/packages/ui`. Do
not delete `panel/` without the owner's word (it is untracked work).

**2026-10-31 (release-readiness audit).** The owner's audit request §3.2
delivered that word: remove a confirmed-obsolete `panel/` that contains no
owner work. Verified first: the tree was the untouched shadcn scaffold (one
route importing a component that does not exist there — `app-version`, so the
scaffold did not even typecheck), nothing built or imported it, and
AGENTS.md/PROJECT_BRIEF.md named it stale. All of it was template boilerplate;
the only omission from `web/` is a handful of scaffold component files no
product code uses. A tarball was written outside the repository before
deletion. Do not reintroduce it.

## D58 — One channel object for both channel detail routes; the local one reads the snapshot

**Decision.** `GET /api/v1/local/channels/{id}` answers the same `channelView`
object as `GET /api/v1/telegram/{key}/channels/{channel_id}` (one struct,
`internal/gateway/channels.go`, built by one function), differing only in its
values and in the `avatar_url` route. The local route builds it from the
`managed_channels` row (`localChannelDetail` in `local_channels.go`) and asks no
worker; `id` is Telegram's channel id as a string, and the local row id stays
in the path. The listing `GET /local/channels` keeps its record shape (local
`id`, `client_key`, numeric `channel_id`, timestamps), because it addresses
records. Migration 6 adds `verified`/`scam`/`fake`/`notices_known`,
`permission_new|edit|delete|read`/`permissions_known`, and
`avatar_available` (backfilled from `avatar_photo_id`). `rpc.ChannelInfo`
carries `has_photo`, so a record can say a photo exists without its version.
`Upsert` writes notices and permissions only when `NoticesKnown` /
`PermissionsKnown`; a write that did not report them keeps the stored values.

**Why.** The admin panel needed channel metadata the local API did not answer,
and the response to that must not be a panel-specific endpoint: one contract,
two legitimate sources. The snapshot is what the join recorded, so it is
honest, works while Telegram is down, and can be stale — documented as such.

**How to apply.** A new field on the channel object is added to `channelView`
and to migration-set columns that the join writes, never to one route only. The
local route never calls the worker; a missing value is `false`/`null`, and
`NoticesKnown`/`PermissionsKnown` say whether it was ever reported. The
frontend's `ChannelDetail` mirrors `channelView`; `channel.id` is never a local
id (the page URL carries the local one). The avatar detail route does not
pre-check the photo through the worker: the photo is discovered on the avatar
route itself.

## D59 — A publishing attempt is recorded so a retry cannot post twice

**Decision.** Migration 7 adds `publish_operations`, one row per attempt keyed
by `(user_id, idempotency_key)` with a `request_hash`, the destination, a
status (`in_flight` / `succeeded` / `failed` / `unknown`) and the stored
result. `Store.BeginPublishOperation` returns `created=true` only for a new key
or a retry of one that **failed**; a confirmed success is replayed from the row,
and an in-flight or unknown outcome is refused rather than re-run. A Telegram
send also carries a `random_id` derived from the same key, so Telegram itself
recognises a resend.

**Why.** Publishing is the one operation whose effect the other end cannot
undo. Without a record, a retried request or a double click creates a second
post; with one, "same key, same body" is safe and "same key, different body" is
a conflict rather than a silent overwrite.

**Rejected.** A message broker or job queue (the task forbids one); storing a
publication history (out of scope); treating `unknown` as `failed` and retrying
it (the post may already exist).

## D60 — `content.publish` is the publishing permission; the destination rule stays `visible()`

**Decision.** A new global permission `content.publish`
(`channelstore.PermissionContentPublish`) gates both publish routes. It is not
implied by any other right. Which destinations a caller may name is decided by
D52's single `visible()` rule — the same one every other route uses — so an
account can publish only where it can already look. No per-destination publish
table is added.

**Why.** `PROJECT_BRIEF.md` §10.2 records `content.publish` as the owner's
requirement. Reusing `visible()` keeps one definition of "may see" and avoids a
second permission model while brief §11 #1 is still an open owner decision.

**How to apply.** New publishing surfaces check `content.publish` first, then
`visible()` on the source and the destination. Do not repurpose
`channels.create` or `wordpress.create` for this.

## D61 — Media crosses the worker boundary as multipart, never as JSON

**Decision.** `POST /messages` on the worker socket takes
`multipart/form-data`: a `request` part carrying the JSON description and an
optional `media` part carrying the bytes, bounded by `MaxSendMediaBytes`
(32 MiB). The bytes are never inside the JSON document.

**Why.** The attachment can be 32 MiB; base64 JSON would be ~43 MiB of text in
a request body that the contract caps at 1 MiB. A bounded multipart part is
explicit about what it is carrying and cannot exhaust the worker by accident.

**How to apply.** Any future worker operation that moves bytes uses the same
shape. Do not raise the JSON body limit to accommodate media.

## D62 — The manage right writes inside the caller's own selection

**Decision.** The global manage permission of a kind (`channels.create`,
`categories.create`, `wordpress.create`) allows `update` and `delete` on that
kind, not only `create`. An explicit per-resource assignment of the action still
decides first, and a write still needs the view. The manage right is narrowed by
the caller's **selection**: while any per-resource assignment is stored it
covers only the resources carrying `view: allow`, and with none stored it covers
every resource of the kind. A selection is stored as a `view: allow` per picked
item plus an `update: deny` with a `delete: deny` per item left out, which is
what the administrators dialog writes; a lone deny is an exclusion over the
whole and does not make a selection.

**Why.** The panel labels `channels.create` "مدیریت کانال‌ها" (manage channels)
and showed a delete action for every row, but the model granted delete only
through a per-channel `delete` assignment the dialog never wrote — so no account
could ever remove a record (the owner's reported bug). WordPress's identically
labelled `wordpress.create` already covered every write, so the same label meant
two different things. Scoping the right keeps D36's "no global update/delete
that overrides a per-resource deny" and gives the dialog its documented "all
items, or the picked ones" choice.

**Why the selection is stored as write denies too.** Assignment rows cascade
with their resource (D34), so a selection recorded only as `view: allow` on the
picked items evaporates when the last one is deleted — the manager would fall
back to every item, which is a scoped manager escalating themselves by deleting
their own channel. The denies on the items left out outlive the picked records
and keep the selection in place. The residue is narrow: a manager scoped to
every item in the inventory, all of which are later deleted and replaced, can
then manage the replacements.

**How to apply.** `channelstore.Authorizer.allowedWrite` is the one rule;
`gateway.visible()` already routes every write through it. Do not grant a kind's
writes from `channels.view`-style globals, and do not let a per-resource deny be
overridden by a manage right. A new manageable kind maps its own global in
`managePermission` and stores its selection the same way. This amends D36's
"update and delete are always per resource" and D48's "per-site update and
delete grant nothing"; WordPress writing still also needs the global on the
route, so a per-site assignment alone never replaces a credential.

## D63 — Attachment bytes cross the worker boundary as a stream, never as JSON

Reading an attachment (`GET /api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}/media`)
returns the file's bytes as a raw response body. The worker's internal media
route answers `200` with `Content-Type`, `Content-Length`, and three headers
carrying what the URL cannot (`X-Telesync-Size`, `X-Telesync-Offset`,
`X-Telesync-Filename`); the gateway turns those into the public contract. The
previous shape — `rpc.MediaFile{Data []byte}` JSON-encoded over the socket — put
every attachment through a base64 buffer on both sides and was capped at 32 MiB,
so a 60 MB APK could not be downloaded at all.

**Why.** "A 60 MB APK must be downloadable without buffering the entire file in
RAM" cannot be met by any encoding that materialises the whole file: only a
stream does. `rpc.MediaStream` carries an `io.ReadCloser`, `gotdclient` pulls
512 KiB `upload.getFile` parts as they are read, and the gateway forwards with
`io.Copy` and flushes the headers first. Nothing on the path holds more than one
part.

**How to apply.** `rpc.Backend.Media` and `gateway.Worker.Media` take a
`rpc.MediaSlice{Offset, Limit, Tail}` and return `rpc.MediaStream`. `Tail` exists
because an HTTP suffix range cannot be resolved to an offset without the size,
and only the worker has it; the gateway keeps all Range parsing (`parseRange`,
`unsatisfiableRange`, `contentDisposition` in `internal/gateway/mediahttp.go`),
so HTTP meaning stays in the public layer. Callers that genuinely need one
buffer — preparing a send, uploading to WordPress — call `MediaStream.ReadAll`,
which is bounded by `MaxSendMediaBytes`; nothing else may. The `rpc.Client`'s
`stream` HTTP client has no total timeout (a large transfer is legitimate), only
`ResponseHeaderTimeout` on the shared transport and the caller's context.

A range is only honoured and only advertised (`Accept-Ranges: bytes`) when the
backend reported a size; with no size the route serves the whole attachment and
says `Accept-Ranges: none` rather than promising something it cannot deliver.
This amends D61's direction of travel only for reads: media *sent* to a worker
still arrives as multipart.

## D64 — A picked scope denies what it left out, for the view as well as the write

**Decision.** When the administrators dialog saves a resource scope, the items
the picker left out are stored with `view: deny` alongside the `update: deny`
and `delete: deny` D62 already puts there. The picked items keep `view: allow`,
and no rows at all still means the whole kind. This amends D62's stored shape
(`view: allow` on the picks, write denies on the rest) without touching its
rule: `marksASelection` still sees a selection, so the manage right is still
narrowed to the picks exactly as before.

**Why.** With D62's shape the *view* was never scoped: an unpicked row carried
no `view` assignment, so `allowedHere` fell through to the kind's global viewing
permission and the account saw every channel it had just been narrowed away
from. The picker's own text says "هر مورد انتخاب‌شده یعنی دسترسی فقط به همان
موارد", and the redesigned report tab has to state what access actually results
— under the old shape it would have had to write "دسترسی به ۲ کانال" while the
answer was "all of them". An explicit deny is the only stored form that outranks
the global grant, and denying is the safe direction.

**How to apply.** A scope is `view: allow` on the picks and
`view: deny, update: deny, delete: deny` on every other row of the same kind in
the same catalog; an empty picker still posts no rows (the widest access, D62's
"no selection"). Do not drop the write denies to shorten the rows: a lone
`view: deny` is an exclusion over the whole and would stop marking a selection,
which is exactly the escalation D62 guards against. Known residue, unchanged in
kind from D62: a record added to the inventory *after* the save has no row and
so is still visible under the global viewing permission (the global cannot be
omitted while a manage right is granted — `requireViewForManage` demands it),
but it can never be written. The report tab says "دسترسی به N" for the picked
count rather than pretending the scope covers records that do not exist yet.

## D65 — A media download is a transfer, not a call: the request budget must not end it

**Decision.** The media route streams on its own context: one that keeps the
request-scoped values, carries no deadline, and is cancelled when the handler
returns. `RequestTimeout` continues to bound every other Telegram route and the
media request's *setup* (the `ResponseHeaderTimeout` on the shared transport),
but it never bounds the body. The response's write deadline is cleared before
the first byte, exactly as `rpc.writeMediaStream` already does.

**Why.** D63 already recorded that "a large transfer is legitimate" and that the
stream client carries "only `ResponseHeaderTimeout` … **and the caller's
context**". The caller's context turned out to be a 30s request budget
(`serveTelegram`'s `context.WithTimeout`), and in Go a client request's context
governs reading the response **body**, not just its headers — so every download
slower than 30s was aborted mid-stream and, with `Content-Length` already on the
wire, reached `curl` as `transfer closed with N bytes remaining`. The daemon's
`WriteTimeout: 120s` cut the same transfer a different way. Both were reproduced
standalone before being fixed. A download that ends early is now reported through
`Config.Logf` with the delivered and promised byte counts, because once the
headers are out a short body cannot be turned into an error — and a short body
with no error at all (`mediaReader` calling a 0-byte part `io.EOF` short of the
reported size) is now a failure rather than a quiet, wrong success.

**How to apply.** Only the streaming answer gets this: `Server.media` derives
`context.WithCancel(context.WithoutCancel(ctx))` and cancels it on return. Do not
put it in `rpc.Client.Media` or in `serveTelegram` — the first would unbound the
publish reads that call `MediaStream.ReadAll`, the second every Telegram route.
`requireChannelView` stays on the bounded context: a permission lookup must never
become an unbounded call. Note `r.Context()` inside the handler is not the raw
request context (`serveTelegram` already replaced it), so it is not a shortcut
for the same thing. A transfer can still end early for reasons in the gotd
reconnect lifecycle — a part that finds the client detached, a watchdog that
cancels a generation, a file reference that expires twice — and those are
reported now rather than fixed here.
