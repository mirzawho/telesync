// Releases the panel: one command writes the version and makes the git tag.
//
//   make version v1.0.2
//
// That writes 1.0.2 into the panel's package.json (which the panel imports and
// shows) and creates the git tag v1.0.2 at HEAD, so there is no second `git
// tag` step. The tag's leading "v" is the convention and is not part of the
// version.
//
// `make build` does not choose a version: it builds whatever package.json
// holds, so a checkout whose git tag is missing still shows the release written
// there. When the file holds none, the nearest git tag is the fallback.
//
// Usage:
//   node scripts/version.mjs <tag>          write it into package.json and tag HEAD
//   node scripts/version.mjs <tag> --no-tag write it into package.json only
//   node scripts/version.mjs                the version in package.json, else the git tag
//                                           (empty when there is neither)
//   node scripts/version.mjs --json         the same, as JSON

import { execFileSync } from "node:child_process"
import { existsSync, readFileSync, writeFileSync } from "node:fs"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const repoRoot = resolve(webRoot, "..")

// The panel's package.json is where the version the panel displays is kept:
// the app imports it, so a build (or a test) reads exactly what was written.
// It is the web/ workspace's app, not the older panel/ copy: this script's
// repo root is that workspace.
export const panelPackagePath = join(webRoot, "apps", "web", "package.json")

// The version npm and `create-*` scaffolds put in a package that has never
// been released. It is not a version the project chose, so it reads as none.
export const UNRELEASED = "0.0.1"

// versionFromTag turns a git tag into the version: "v1.0.0" -> "1.0.0". Only
// the tag's single leading "v" is dropped, the way the panel writes it back
// ("v" + version); nothing else about the tag is rewritten.
export function versionFromTag(tag) {
  const t = tag.trim()
  return t.startsWith("v") ? t.slice(1) : t
}

// packageVersion reads the version package.json holds, or "" when it holds
// none.
export function packageVersion(path = panelPackagePath) {
  try {
    const version = JSON.parse(readFileSync(path, "utf8")).version
    return typeof version === "string" && version !== UNRELEASED ? version : ""
  } catch {
    return ""
  }
}

// tagFromVersion is the tag a version is released under: "1.0.2" -> "v1.0.2".
export function tagFromVersion(version) {
  return versionFromTag(version) === version ? "v" + version : version
}

// tagVersion asks git for the nearest version tag on HEAD. It never throws:
// no git, no repository or no tag is "".
export function tagVersion(cwd) {
  let out
  try {
    // --match keeps a tag used for something else ("latest", "nightly") out
    // of the version; --long names the commit distance explicitly.
    out = execFileSync(
      "git",
      ["-C", cwd, "describe", "--tags", "--long", "--match", "v[0-9]*"],
      { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] },
    ).trim()
  } catch {
    return ""
  }
  // "-0-g<sha>" means HEAD is the tag itself: the version is the tag's.
  if (/-0-g[0-9a-f]+$/.test(out)) {
    return versionFromTag(out.replace(/-0-g[0-9a-f]+$/, ""))
  }
  // Past the tag: the nearest tag, the distance and the commit, so a build
  // says which commit past v1.0.0 it is.
  return out.replace(/^v(?=\d)/, "")
}

// currentVersion is what the panel shows: the released version in
// package.json, and the git tag only when the file holds none — the fallback
// for a checkout that has been tagged but not released through `make version`.
export function currentVersion(cwd = repoRoot, path = panelPackagePath) {
  return packageVersion(path) || tagVersion(cwd)
}

// writeVersion puts version into package.json, keeping the file's formatting,
// and says whether it changed anything.
export function writeVersion(version, path = panelPackagePath) {
  const before = readFileSync(path, "utf8")
  const pkg = JSON.parse(before)
  if (pkg.version === version) return false
  pkg.version = version
  const indent = (before.match(/^\{\n([ \t]+)"/) || [null, "  "])[1]
  writeFileSync(path, JSON.stringify(pkg, null, indent) + "\n")
  return true
}

// tagHead creates tag at HEAD in cwd and answers what happened:
//
//	"created"        the tag is new
//	"exists"         the tag was already there, and is left as it is
//	"no-repository"  cwd is not a git repository, so there is nothing to tag on
//	"no-commit"      the repository has no commit yet, so there is no HEAD
//	"error: …"       git refused for another reason, with git's own words
//
// It never throws: the version is already written, and the caller says what
// became of the tag.
export function tagHead(tag, cwd) {
  if (!isRepository(cwd)) return "no-repository"
  if (!hasCommit(cwd)) return "no-commit"
  try {
    execFileSync("git", ["-C", cwd, "tag", tag], { stdio: ["ignore", "pipe", "pipe"] })
    return "created"
  } catch (error) {
    const message = (error.stderr || error.message || "").toString().trim()
    if (message.includes("already exists")) return "exists"
    return `error: ${message}`
  }
}

// hasCommit says whether the repository has a HEAD to tag.
function hasCommit(cwd) {
  try {
    execFileSync("git", ["-C", cwd, "rev-parse", "--verify", "HEAD"], {
      stdio: ["ignore", "pipe", "ignore"],
    })
    return true
  } catch {
    return false
  }
}

// isRepository says whether cwd is the top of a git work tree (or inside one).
function isRepository(cwd) {
  if (!existsSync(cwd)) return false
  try {
    return (
      execFileSync("git", ["-C", cwd, "rev-parse", "--is-inside-work-tree"], {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"],
      }).trim() === "true"
    )
  } catch {
    return false
  }
}

// releaseCommand turns CLI arguments into what the command should do. It is
// separate from main so the flag rules are tested without invoking git.
//
//	{ mode: "release", version, writeOnly }  write version; tag unless writeOnly
//	{ mode: "query", asJSON }                print the current version
//
// A named version with --stamp or --no-tag is write-only: it records the
// version and never touches git. That is what the Docker build uses (its build
// context has no .git), and what a release dry run wants.
export function releaseCommand(argv) {
  const flags = argv.filter((arg) => arg.startsWith("--"))
  const writeOnly = flags.includes("--stamp") || flags.includes("--no-tag")
  const named = argv.find((arg) => !arg.startsWith("--"))

  if (named !== undefined) {
    const version = versionFromTag(named)
    if (version === "") {
      throw new Error("a version is required, as in `make version v1.0.2`")
    }
    return { mode: "release", version, writeOnly }
  }
  return { mode: "query", asJSON: flags.includes("--json") }
}

function main(argv) {
  const plan = releaseCommand(argv)

  if (plan.mode === "release") {
    const changed = writeVersion(plan.version)
    process.stdout.write(
      `version: ${plan.version} written to web/apps/web/package.json${changed ? "" : " (unchanged)"}\n`,
    )
    if (plan.writeOnly) return
    const tag = tagFromVersion(plan.version)
    const outcome = tagHead(tag, repoRoot)
    switch (outcome) {
      case "created":
        process.stdout.write(`git tag: ${tag} created at HEAD\n`)
        break
      case "exists":
        process.stdout.write(`git tag: ${tag} already exists\n`)
        break
      case "no-repository":
        process.stdout.write(
          `git tag: not created, ${repoRoot} is not a git repository\n` +
            `         the version is written; run \`git init\` and commit, then \`git tag ${tag}\`\n`,
        )
        break
      case "no-commit":
        process.stdout.write(
          `git tag: not created, the repository has no commit yet to tag\n` +
            `         commit, then \`git tag ${tag}\`\n`,
        )
        break
      default:
        throw new Error(`git tag: ${tag} was not created: ${outcome.replace(/^error: /, "")}`)
    }
    return
  }

  const version = currentVersion()
  if (plan.asJSON) {
    process.stdout.write(JSON.stringify(version) + "\n")
    return
  }
  // A checkout with neither answers nothing: `make build` then builds a panel
  // that shows no version, as it did before there were releases.
  process.stdout.write(version + "\n")
}

// node runs this file as a script, not an import.
if (import.meta.url === pathToFileURL(process.argv[1] || "").href) {
  try {
    main(process.argv.slice(2))
  } catch (error) {
    process.stderr.write(`version: ${error.message}\n`)
    process.exit(1)
  }
}
