<!-- component: health and docs -->
<!-- manual: health and documentation -->
# Health and documentation

## `GET /healthz`

Anonymous liveness for the daemon process: `200` while the HTTP server is
serving. It answers without a credential, on every listener — it reveals
nothing about the workers or the account — and says nothing about any client —
a Telegram client's state is
`GET /api/v1/telegram/{key}/health`, which reports `state`, `transport`,
`authorized`, `last_success_at` — and omits `last_success_at` until an
authorized probe has actually succeeded.

## `GET /docs` and `GET /docs/{path}`

The API documentation rendered as HTML, with a sidebar built from the pages.

- **Authentication:** none. `/docs` is public like `/healthz`, as the
  documentation pages the API rather than the API itself: the pages
  describe the API, never an account's data, and are meant to be read without a
  credential.
- **Where the pages come from:** the binary carries them. `make build` runs the
  generator and embeds `docs/api` in the executable, so `GET /docs` works with
  no directory to configure and nothing beside the configuration file. The
  embedded copy is the one that matched the source at build time; regenerating
  it takes a rebuild (and a daemon restart).
- **Overriding it:** `api.docs_dir` names a directory to serve instead of the
  embedded copy — a tree installed beside a deployment, for instance. It is
  resolved against the configuration file's directory, must exist, and is read
  once at startup. `telesync config validate` prints which one is in use
  (`api.docs_dir=(embedded)` or the path).
- **What is published:** only `.md` files that declare a writer in their first
  lines — generated pages (`<!-- generated …`), hand-written sources
  (`<!-- manual …`), composed pages (`<!-- compose:…`), and archived pages
  (`<!-- archived:…`). A stray file in the tree is a `404`.
- **Routes:** `/docs` is the landing page (`README.md`); a directory without
  its own README is its listing, served with a trailing slash (`GET
  /docs/<dir>` redirects `301` to `/docs/<dir>/`, so the links beside the page
  resolve from that directory); `/docs/<dir>/<page>.md` renders a page.
  Anything else, including a missing document, is `404` as HTML.
- **Safety:** paths are checked against the allow-list built at startup, and
  the filesystem refuses a path that leaves the root, so nothing outside the
  tree is read. Raw HTML in a document is dropped, a link with a `javascript:`
  target loses its destination, and the page is served with a `default-src
  'none'` content security policy.
- `HEAD` is served like `GET` without the body; any other method is `405` with
  `Allow: GET, HEAD`.
