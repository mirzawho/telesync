// Bundles the webhook screens' tests and runs them with node's built-in test
// runner, exactly like test-automation.mjs: the workspace has no test runner and
// none can be added offline, so esbuild (already part of Vite) resolves the
// app's aliases and JSX and node:test does the rest. The bundle is written
// inside the app so node finds the workspace's own react, and is removed
// afterwards.
//
// Its own output directory: a concurrent test run in this app writes to the
// same shape of path, and a shared one would make each run see the other's
// bundles.
//
// Usage:
//   node scripts/test-webhook.mjs                    # every file listed below
//   node scripts/test-webhook.mjs path/to/a.test.tsx # only the named files
//
// Arguments are resolved against the app directory (apps/web).
import { execFileSync } from "node:child_process"
import { mkdirSync, readdirSync, rmSync } from "node:fs"
import { dirname, isAbsolute, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const appDir = resolve(here, "..")
const webDir = resolve(appDir, "../..")
const esbuild = resolve(webDir, "node_modules/.bin/esbuild")
const outDir = join(appDir, ".test-out-webhook")

const tests = [
  join(appDir, "app/components/automation/webhook-errors.test.tsx"),
  join(appDir, "app/components/automation/webhook-pages.test.tsx"),
]

const chosen =
  process.argv.length > 2
    ? process.argv
        .slice(2)
        .map((arg) => (isAbsolute(arg) ? arg : join(appDir, arg)))
    : tests

try {
  mkdirSync(outDir, { recursive: true })
  execFileSync(
    esbuild,
    [
      ...chosen,
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
  // esbuild keeps the entry points' own directories below the output, so the
  // bundles are found by name rather than by a fixed path.
  const bundles = readdirSync(outDir, { recursive: true })
    .filter((name) => String(name).endsWith(".test.cjs"))
    .map((name) => join(outDir, String(name)))
    .sort()
  execFileSync("node", ["--test", ...bundles], { stdio: "inherit" })
} finally {
  rmSync(outDir, { recursive: true, force: true })
}
