// Bundles the personal-account tests and runs them with node's built-in test
// runner.
//
// Same shape as scripts/test-panel.mjs: the workspace has no test runner and
// none can be added offline, so esbuild (already present through Vite) resolves
// the path aliases and JSX and node:test does the rest. The bundle is written
// inside the app so node can find the workspace's own react, and is removed
// afterwards.
import { execFileSync } from "node:child_process"
import { mkdirSync, readdirSync, rmSync } from "node:fs"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const appDir = resolve(here, "..")
const webDir = resolve(appDir, "../..")
const esbuild = resolve(webDir, "node_modules/.bin/esbuild")
// Its own output directory, so a concurrent run of another suite cannot see
// these bundles.
const outDir = join(appDir, ".test-out-accounts")

const tests = [
  join(appDir, "app/components/automation/source-selection.test.tsx"),
  join(appDir, "app/lib/telegram-account.test.ts"),
  join(appDir, "app/lib/account-errors.test.ts"),
  join(appDir, "app/lib/login-flow.test.ts"),
  join(appDir, "app/lib/account-channel-scope.test.ts"),
  join(appDir, "app/lib/account-filter.test.ts"),
  join(appDir, "app/lib/account-api.test.ts"),
  join(appDir, "app/components/accounts/accounts.test.tsx"),
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
