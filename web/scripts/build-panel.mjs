// Builds the admin panel and folds it into the Go binary.
//
// It runs as part of `make build`: the panel's static client is built, and
// every file in apps/web/build/client is copied over the tree the Go compiler
// embeds (internal/panel/files) by the same atomic mirror the documentation
// generator uses. `go build` then carries the panel, and the daemon serves it
// at /admin with no Node and nothing on disk beside it.
//
// The copy is a mirror, not a merge: a file the build no longer produces is
// removed from the embedded tree, and only files under it are ever touched.
//
// Usage:
//   node scripts/build-panel.mjs                  build, then embed
//   node scripts/build-panel.mjs --skip-if-current  embed only if the built
//                                                 panel is newer (`make run`)

import { execFileSync } from "node:child_process"
import { existsSync, lstatSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs"
import { dirname, join, relative, resolve, sep } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const appDir = join(webRoot, "apps", "web")
const builtDir = join(appDir, "build", "client")
const repoRoot = resolve(webRoot, "..")
const embedDir = join(repoRoot, "internal", "panel", "files")
const indexPath = join(builtDir, "index.html")

const skipIfCurrent = process.argv.includes("--skip-if-current")

// --embed-only copies a page written by hand (no build), for checking the page
// without running Vite again.
const embedOnly = process.argv.includes("--embed-only")

if (embedOnly) {
  embed(copyTree)
  process.exit(0)
}

if (skipIfCurrent && existsSync(indexPath) && existsSync(join(embedDir, "index.html"))) {
  if (statSync(join(embedDir, "index.html")).mtimeMs >= statSync(indexPath).mtimeMs) {
    console.log("panel: the embedded copy is current")
    process.exit(0)
  }
}

console.log("panel: building with vite")
const vite = join(webRoot, "node_modules", "vite", "bin", "vite.js")
try {
  execFileSync(process.execPath, [vite, "build"], { cwd: appDir, stdio: "inherit" })
} catch (error) {
  // React Router ends its static build by rendering the page through a preview
  // server it starts itself. That step is left out of the Vite config here, so
  // the page is assembled from the client manifest (spa-prerender.ts) — and a
  // build that failed for any other reason still fails here.
  if (!ensurePage()) throw error
}
if (!existsSync(indexPath)) {
  ensurePage()
}
// ensurePage leaves a page from the build alone, and writes one when the
// build left none.
function ensurePage() {
  if (existsSync(indexPath)) return true
  if (!existsSync(join(builtDir, ".vite", "manifest.json"))) return false
  const page = pathToFileURL(join(appDir, "spa-prerender.ts")).href
  execFileSync(process.execPath, [
    "--experimental-transform-types",
    "-e",
    `const { writeSpaPage } = await import(${JSON.stringify(page)}); await writeSpaPage(${JSON.stringify(appDir)})`,
  ], { stdio: "inherit" })
  return existsSync(indexPath)
}

// walk lists every file under dir, relative and slash-separated, in a stable
// order.
function walk(dir, base = dir) {
  const out = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) {
      out.push(...walk(full, base))
    } else {
      out.push(relative(base, full).split(sep).join("/"))
    }
  }
  return out
}

embed(copyTree)

function embed(copy) { copy() }

function copyTree() {
mkdirSync(embedDir, { recursive: true })
// Only what is on disk is copied: the React Router build's server half also
// writes a manifest into build/client/.vite, whose entries name chunks of that
// half, which the browser never requests.
const files = walk(builtDir).filter((rel) => !rel.startsWith(".vite/"))
const produced = new Set(files)
for (const rel of [...produced].sort()) {
  const target = join(embedDir, rel)
  mkdirSync(dirname(target), { recursive: true })
  writeFileSync(target, readFileSync(join(builtDir, rel)))
}

// Remove what the build no longer has. The walk yields parents before their
// children, so it is reversed: a directory left empty by the sweep is removed
// only after its files. .keep is not a built file and stays, so the package
// still compiles before the first panel build.
for (const rel of walk(embedDir).reverse()) {
  if (produced.has(rel)) continue
  const target = join(embedDir, rel)
  if (lstatSync(target).isDirectory()) {
    if (readdirSync(target).length === 0) rmSync(target, { recursive: true, force: true })
    continue
  }
  rmSync(target, { force: true })
}

console.log(`panel: embedded ${produced.size} files at ${relative(repoRoot, embedDir)}`)
}
