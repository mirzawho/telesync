// Bundles the publishing tests and runs them with node's built-in test runner.
//
// There is no test runner in this workspace (no vitest, no jest) and none can
// be added offline. esbuild is already present as part of Vite, and it is what
// resolves the app's path aliases and JSX, so a bundle plus node:test is enough
// to prove what the publishing model computes and what the workspace renders.
// The bundle is written inside the app so node can find the workspace's own
// react, and is removed afterwards.
import { execFileSync } from "node:child_process"
import { mkdirSync, readdirSync, rmSync } from "node:fs"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const appDir = resolve(here, "..")
const webDir = resolve(appDir, "../..")
const esbuild = resolve(webDir, "node_modules/.bin/esbuild")
const outDir = join(appDir, ".test-out")

const tests = [
  join(appDir, "app/lib/publish-draft.test.ts"),
  join(appDir, "app/lib/publish-errors.test.ts"),
  join(appDir, "app/lib/media-blob-cache.test.ts"),
  join(appDir, "app/components/publish/publish-workspace.test.tsx"),
  join(appDir, "app/components/publish/source-attachment-media.test.tsx"),
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
