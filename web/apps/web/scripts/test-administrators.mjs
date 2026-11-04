// Bundles the administrators tests and runs them with node's built-in test
// runner, exactly like test-panel.mjs: the workspace has no test runner and none
// can be added offline, so esbuild (already part of Vite) resolves the app's
// aliases and JSX and node:test does the rest. The bundle is written inside the
// app so node finds the workspace's own react, and is removed afterwards.
import { execFileSync } from "node:child_process"
import { mkdirSync, readdirSync, rmSync } from "node:fs"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const appDir = resolve(here, "..")
const webDir = resolve(appDir, "../..")
const esbuild = resolve(webDir, "node_modules/.bin/esbuild")
// Its own output directory: a concurrent test run in this app writes to the
// same shape of path, and a shared one would make each run see the other's
// bundles.
const outDir = join(appDir, ".test-out-administrators")

const tests = [
  join(appDir, "app/lib/admin-permissions.test.ts"),
  join(appDir, "app/lib/admin-users.test.ts"),
  join(appDir, "app/components/administrators/administrators.test.tsx"),
]

try {
  mkdirSync(outDir, { recursive: true })
  execFileSync(
    esbuild,
    [
      ...tests,
      "--bundle",
      "--platform=node",
      // CommonJS: react-dom's server build requires node built-ins, and a
      // dynamic require is not available inside an ES module bundle.
      "--format=cjs",
      "--out-extension:.js=.cjs",
      "--jsx=automatic",
      "--target=node20",
      "--outdir=" + outDir,
      "--alias:@=" + join(appDir, "app"),
      "--alias:@workspace/ui=" + join(webDir, "packages/ui/src"),
      // Vite supplies these at build time; node has no import.meta.env.
      "--define:import.meta.env.BASE_URL=\"/\"",
      "--define:import.meta.env.DEV=false",
      "--define:import.meta.env.PROD=true",
      "--define:import.meta.env.MODE=\"test\"",
      "--log-level=warning",
    ],
    { stdio: "inherit" }
  )
  const bundles = readdirSync(outDir, { recursive: true })
    .filter((name) => String(name).endsWith(".test.cjs"))
    .map((name) => join(outDir, String(name)))
    .sort()
  execFileSync("node", ["--test", ...bundles], { stdio: "inherit" })
} finally {
  rmSync(outDir, { recursive: true, force: true })
}
