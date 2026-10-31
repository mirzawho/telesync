// Compiles the content parser and its tests, then runs them with node:test.
//
// There is no test runner in this workspace (no vitest, no jest), and one
// cannot be added offline. The parser is pure TypeScript with no dependencies,
// so tsc plus node's built-in runner is enough to prove its behaviour.
import { execFileSync } from "node:child_process"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = fileURLToPath(new URL(".", import.meta.url))
const appDir = resolve(here, "..")
const tsc = resolve(here, "../../../node_modules/.bin/tsc")
const out = mkdtempSync(join(tmpdir(), "telesync-content-"))

try {
  execFileSync(
    tsc,
    [
      join(appDir, "app/lib/content.ts"),
      join(appDir, "app/lib/content.test.ts"),
      "--ignoreConfig",
      "--outDir", out,
      "--target", "es2022",
      "--module", "nodenext",
      "--moduleResolution", "nodenext",
      "--noEmit", "false",
      "--skipLibCheck",
      "--types", "node",
    ],
    { stdio: "inherit" },
  )
  execFileSync("node", ["--test", join(out, "content.test.js")], { stdio: "inherit" })
} finally {
  rmSync(out, { recursive: true, force: true })
}
