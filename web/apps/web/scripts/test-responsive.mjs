// Bundles responsive-UI tests and runs them with node's built-in test runner.
//
// Same shape as test-panel.mjs: the workspace has no test runner, so esbuild
// (already present through Vite) resolves the app's path aliases and JSX, and
// node:test runs the bundle.
//
// Usage:
//   node scripts/test-responsive.mjs                    # every *.responsive.test.*
//   node scripts/test-responsive.mjs path/to/a.test.tsx # only the named files
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
const outDir = join(appDir, ".test-out-responsive")

const isTest = (name) => String(name).endsWith(".responsive.test.tsx")

// Tests may live beside the code they prove, in the app or in the shared UI
// package; both roots are scanned.
const roots = [join(appDir, "app"), join(webDir, "packages/ui/src")]

const tests =
  process.argv.length > 2
    ? process.argv.slice(2).map((arg) => (isAbsolute(arg) ? arg : join(appDir, arg)))
    : roots
        .flatMap((root) =>
          readdirSync(root, { recursive: true })
            .filter(isTest)
            .map((name) => join(root, String(name)))
        )
        .sort()

if (tests.length === 0) {
  console.error("test-responsive: no *.responsive.test.tsx files found")
  process.exit(1)
}

try {
  mkdirSync(outDir, { recursive: true })
  execFileSync(
    esbuild,
    [
      ...tests,
      "--bundle",
      "--platform=node",
      "--format=cjs",
      "--out-extension:.js=.cjs",
      "--jsx=automatic",
      "--target=node20",
      "--outdir=" + outDir,
      "--alias:@=" + join(appDir, "app"),
      "--alias:@workspace/ui=" + join(webDir, "packages/ui/src"),
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
