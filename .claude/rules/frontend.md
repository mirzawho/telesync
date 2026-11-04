---
paths:
  - "web/**"
  - "internal/panel/**"
---

# Frontend boundary

Edit `web/`, not a root `panel/` scaffold. Keep React Router 8, React 19, Vite,
TypeScript, Tailwind, `@workspace/ui`/Base UI and Persian RTL conventions. The
panel is built from `web/` and embedded into `internal/panel`; run the relevant
web typecheck/build after frontend changes.
