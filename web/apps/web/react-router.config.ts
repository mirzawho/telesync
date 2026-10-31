import type { Config } from "@react-router/dev/config"

// A single-page app: the Go binary serves the built files and answers every
// panel route of the app itself. Loaders and actions run on the browser side
// and call the mounted API (/admin/api/v1/...) with the token the login
// issued, so the panel needs no server of its own.
export default {
  ssr: false,
  // Absolute asset links under the mount: the page is served for routes like
  // /admin/categories, where a relative link would resolve one level deeper.
  basename: "/admin/",
} satisfies Config
