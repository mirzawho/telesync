import { reactRouter } from "@react-router/dev/vite"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

// The panel is a single-page app served by the Go binary at /admin: one page
// whose links are absolute under that base, and a browser router for every
// route after it. `npm run dev` serves it the same way at
// http://localhost:5173/admin.

export default defineConfig({
  base: "/admin/",
  resolve: { tsconfigPaths: true },
  plugins: [
    tailwindcss(),
    // The prerender step cannot run here (it needs its own server); the page is
    // written from the client manifest after the build, in build-panel.mjs.
    ...reactRouter().filter((plugin) => plugin.name !== "prerender"),
  ],
  build: { manifest: true },
  server: {
    port: 5173,
    strictPort: true,
    // TELESYNC_DEV_API sends the API calls to an existing deployment (a
    // running daemon on another port, say) instead of a vite process; unset,
    // this changes nothing.
    ...(process.env.TELESYNC_DEV_API
      ? {
          proxy: {
            "/admin/api": {
              target: process.env.TELESYNC_DEV_API,
              rewrite: (path) => path.replace(/^\/admin/, ""),
            },
          },
        }
      : {}),
  },
})
