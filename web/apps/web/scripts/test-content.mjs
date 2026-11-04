// Compiles the content parser and its tests, then runs them with node:test.
//
// There is no test runner in this workspace (no vitest, no jest), and one
// cannot be added offline. The parser is pure TypeScript with no dependencies,
// so tsc plus node's built-in runner is enough to prove its behaviour. The
// compile still typechecks, which is why this one runs tsc and not a bundler.
import { execFileSync } from "node:child_process"
import { mkdirSync, readdirSync, rmSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = fileURLToPath(new URL(".", import.meta.url))
const appDir = resolve(here, "..")
const tsc = resolve(here, "../../../node_modules/.bin/tsc")
// The output lives inside the app rather than in the system temp dir: tsc has
// to reach the workspace's own @types and vite/client, and it finds them by
// walking up from the config it is given.
const out = join(appDir, ".test-out")
mkdirSync(out, { recursive: true })

// The parser takes its Entity type from the app's own "@/lib/types", so the
// compile needs the path alias the app is built with. tsc reads `paths` from a
// config only, so a minimal one is written next to the output, with the same
// compiler options the app itself builds with. rootDir is the app's own source
// root: the alias reaches outside app/lib, so tsc may not pick a narrower one
// without failing.
writeFileSync(
  join(out, "tsconfig.json"),
  JSON.stringify(
    {
      compilerOptions: {
        rootDir: join(appDir, "app"),
        outDir: out,
        lib: ["DOM", "DOM.Iterable", "ES2022"],
        target: "ES2022",
        module: "ES2022",
        moduleResolution: "bundler",
        esModuleInterop: true,
        verbatimModuleSyntax: true,
        noEmit: false,
        resolveJsonModule: true,
        skipLibCheck: true,
        types: ["node", "vite/client"],
        paths: { "@/*": [join(appDir, "app/*")] },
      },
      files: [
        join(appDir, "app/lib/content.ts"),
        join(appDir, "app/lib/content.test.ts"),
      ],
    },
    null,
    2,
  ),
)

try {
  execFileSync(tsc, ["--project", join(out, "tsconfig.json")], { stdio: "inherit" })
  // The app is an ES module, so tsc emits one and node must read it as one.
  writeFileSync(join(out, "package.json"), '{ "type": "module" }\n')
  // tsc mirrors the source tree below outDir, so the bundle is found by name
  // rather than by a fixed path.
  const bundles = readdirSync(out, { recursive: true })
    .filter((name) => String(name).endsWith(".test.js"))
    .map((name) => join(out, String(name)))
    .sort()
  execFileSync("node", ["--test", ...bundles], { stdio: "inherit" })
} finally {
  rmSync(out, { recursive: true, force: true })
}
