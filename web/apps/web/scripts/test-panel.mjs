// Bundles the channel-explorer tests and runs them with node's built-in test
// runner.
//
// The workspace has no test runner (no vitest, no jest) and none can be added
// offline. esbuild is already present as part of Vite, and it is what resolves
// the app's path aliases and JSX, so a bundle plus node:test is enough to prove
// what the explorer computes and what it renders. The bundle is written inside
// the app so node can find the workspace's own react, and is removed afterwards.
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
const outDir = join(appDir, ".test-out-panel")

const tests = [
  join(appDir, "app/lib/message-entities.test.ts"),
  join(appDir, "app/lib/channel-filter.test.ts"),
  join(appDir, "app/lib/channel-server-filter.test.ts"),
  join(appDir, "app/lib/message-timeline.test.ts"),
  join(appDir, "app/lib/message-albums.test.ts"),
  join(appDir, "app/lib/channel-message-api.test.ts"),
  join(appDir, "app/lib/channel-message-permissions.test.ts"),
  join(appDir, "app/lib/scroll-anchor.test.ts"),
  join(appDir, "app/components/channel-explorer.test.tsx"),
  join(appDir, "app/components/channel-explorer/create-channel-form.test.tsx"),
  join(appDir, "app/routes/_panel/channel-create-legacy.test.ts"),
  join(appDir, "app/routes/_panel/channel-messages.test.tsx"),
  join(appDir, "app/components/channel-explorer/channel-information-dialog.test.tsx"),
  join(appDir, "app/components/channel-explorer/channel-message-composer.test.tsx"),
  join(appDir, "app/components/channel-explorer/channel-message-edit-composer.test.tsx"),
  join(appDir, "app/components/channel-explorer/chat-layout.test.tsx"),
  join(appDir, "app/components/channel-explorer/channel-message-pending.test.tsx"),
  join(appDir, "app/components/channel-explorer/message-actions.test.tsx"),
  join(appDir, "app/components/tabs.test.tsx"),
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
