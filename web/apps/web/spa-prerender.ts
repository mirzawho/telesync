// The panel's one page, rendered at the end of the build.
//
// React Router's static build renders this page at the end of `vite build`,
// through a preview server it starts itself; where no server can start, this
// module renders it in-process from the same server bundle. The page it writes
// is the shell the router hydrates from: the module map, the context, and the
// root route, with no data — every screen loads its data from the API in the
// browser.
//
// It reads and writes only build/, and loads Vite itself so the server bundle
// runs with the same aliases and extensions the app was built with.

import { writeFileSync } from "node:fs"
import { join } from "node:path"
import { pathToFileURL } from "node:url"

export async function writeSpaPage(appDir: string) {
  const clientDir = join(appDir, "build", "client")
  const webRoot = join(appDir, "..", "..")
  const viteEntry = join(webRoot, "node_modules", "vite", "dist", "node", "index.js")
  const { createServer } = await import(pathToFileURL(viteEntry).href)
  const vite = await createServer({
    configFile: false,
    root: appDir,
    logLevel: "silent",
    server: { middlewareMode: true, hmr: false, watch: null },
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  try {
    const mod = await vite.ssrLoadModule("/build/server/index.js")
    const rr = await vite.ssrLoadModule("react-router")
        const build = { ...(mod.default ?? mod) }
    build.routes = typeof build.routes === "function" ? await build.routes() : build.routes
    const router = rr.createRequestHandler(build, "production")
    // The request is addressed where the router expects it: under its base.
    const base = build.basename ?? "/"
    const response = await router(
      new Request(`http://localhost${base}`, { headers: { Accept: "text/html" } }),
      new rr.RouterContextProvider()
    )
        if (response.status !== 200) {
      throw new Error(`the panel's page rendered ${response.status}`)
    }
    writeFileSync(join(clientDir, "index.html"), await response.text())
  } finally {
    await vite.close()
  }
  return join(clientDir, "index.html")
}
