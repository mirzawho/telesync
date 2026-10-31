# The admin panel

A React Router single-page app served by the telesync binary at `/admin`. The
Go gateway serves its page and assets from the tree embedded in the binary
(`internal/panel/files`) and answers its API under the same mount
(`/admin/api/v1/...`), so the panel needs no server of its own.

## Building

```bash
make build        # at the repository root: builds the panel, then the binary
make panel        # the panel alone, into internal/panel/files
cd web && npm ci  # once, before the first panel build
```

A machine without Node keeps the panel already embedded in the tree.

## Developing

```bash
cd web/apps/web && npm run dev
```

The dev server serves the panel at the same path the binary does,
http://localhost:5173/admin. It calls the API from the browser, which then
calls the daemon's own origin — so either run the daemon behind the same
origin, or set `TELESYNC_DEV_API` to an API root on another port (for example
`http://127.0.0.1:8080/admin`) and vite proxies `/admin/api` there.

## Adding components

```bash
cd web && npx shadcn@latest add button -c apps/web
```

Components land in `packages/ui/src/components` and are imported as
`@workspace/ui/components/...`.
