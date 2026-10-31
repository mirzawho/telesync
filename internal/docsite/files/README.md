<!-- title: telesync HTTP API -->
# telesync HTTP API

The daemon serves one JSON HTTP API in front of every configured Telegram
account:

```
HTTP client → gateway (HTTP) → worker RPC (Unix socket) → MTProto
```

The panel, and every route it calls, is served under `/admin`: the panel's
page at `/admin`, its assets beside it, and the API at `/admin/api/v1/...`. The
same listener and the same credential policy, one address the browser uses.
`make build` builds the panel and folds it into the binary; a binary built
without it answers `/admin` with 503 and the command to build it.

Three groups of routes, one credential policy:

- **Telegram routes**, `/api/v1/telegram/{key}/...`, read live data from one
  configured client. `{key}` is the client's `key` in the configuration file.
- **Local routes**, `/api/v1/local/...`, read and write this application's own
  SQLite data — categories, operator accounts and local channel records. Users
  and categories never contact Telegram and keep answering while every worker is
  down; a local channel record names the client that reaches its channel, and
  the messages and avatar routes ask Telegram through that client's worker.
- **Identity routes**, `/healthz`, `/docs` and `/api/v1/auth/...`, answer for
  the HTTP server, the documentation and the caller.

## The pages

- [Telegram routes](generated/telegram.md): health, profile, channels, discovery, history,
  media, avatars and join.
- [Local routes](generated/local.md): channels, categories, users, WordPress
  site registrations and permissions. What a local channel is, and how its id
  differs from Telegram's, is in [the local channels paragraph](manual/components/channels.md);
  what registering a WordPress site does, and what it refuses, is in
  [the WordPress sites paragraph](manual/components/wordpress.md).
- [Examples](examples/README.md): every request the tests execute, and the ids
  they create.
- [Generated facts](generated/facts.json): the JSON every generated page is
  rendered from.
- [Unresolved items](generated/unresolved.md): what the generator could not
  establish, with the evidence it had.

## Generated and hand-written

The pages under `generated/`, and the tables and endpoint sections of this page
and the two group pages, are generated from the source by
`go run ./cmd/docsgen` and replaced on every run. The prose under `manual/` is
hand-written and never rewritten. The generator:

- reads the routes from its checked table (`internal/apidocs`), each handler's
  `writeJSON` value and status, the type each handler decodes a request body
  into, and the credential and permission conditions on the way to each handler;
- replays every route against the gateway with no credential, the operator
  token, a limited account and an administrator, and replays the examples in
  `examples/api.http`;
- reports everything else as unresolved rather than guessing.

`go run ./cmd/docsgen --check` fails when the committed pages are stale.

## Reading this over HTTP

`GET /docs` renders this documentation tree as HTML with a sidebar; the same
pages are at `/docs/<path under docs/api>`. It is public — no credential — and
the binary carries the tree, so it works with no directory to configure. See
the health and documentation paragraph for what is published and how paths are
checked.
