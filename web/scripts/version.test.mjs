// Tests for version.mjs: `node --test web/scripts/version.test.mjs`.
//
// The rule under test: `make version v1.0.2` writes 1.0.2 into package.json
// and makes the tag v1.0.2 at HEAD; the panel then reads the file, whether or
// not this checkout still has the tag; and with no version in the file the git
// tag is the fallback.

import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { test } from "node:test"

import {
  currentVersion,
  packageVersion,
  releaseCommand,
  tagFromVersion,
  tagHead,
  tagVersion,
  versionFromTag,
  writeVersion,
} from "./version.mjs"

// repo makes a scratch repository with fixed identity, plus a package.json to
// release.
function repo(t) {
  const dir = mkdtempSync(join(tmpdir(), "telesync-version-"))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const git = (...args) =>
    execFileSync(
      "git",
      ["-C", dir, "-c", "user.email=t@example.test", "-c", "user.name=t", ...args],
      { stdio: "pipe" },
    )
  git("init", "-q", "-b", "main")
  const path = join(dir, "package.json")
  writeFileSync(path, '{\n  "name": "web",\n  "version": "0.0.1",\n  "private": true\n}\n')
  return {
    dir,
    path,
    git,
    commit(message) {
      git("commit", "-q", "--allow-empty", "-m", message)
    },
    tag(name) {
      git("tag", name)
    },
  }
}

test("the tag's leading v is the convention, not the version", () => {
  assert.equal(versionFromTag("v1.0.0"), "1.0.0")
  assert.equal(versionFromTag("1.2.3"), "1.2.3")
  assert.equal(versionFromTag("v1.0.0-rc.1"), "1.0.0-rc.1")
  assert.equal(versionFromTag("V2.1.0"), "V2.1.0")
})

test("a version is tagged with a leading v", () => {
  assert.equal(tagFromVersion("1.0.2"), "v1.0.2")
  assert.equal(tagFromVersion("v1.0.2"), "v1.0.2")
})

test("an unreleased package version is no version", (t) => {
  const r = repo(t)
  assert.equal(packageVersion(r.path), "")
  writeVersion("1.0.2", r.path)
  assert.equal(packageVersion(r.path), "1.0.2")
})

test("the file wins, so a missing tag does not hide the release", (t) => {
  const r = repo(t)
  r.commit("one")
  writeVersion("2.0.0", r.path)
  // No tag at all: the release is still what package.json says.
  assert.equal(currentVersion(r.dir, r.path), "2.0.0")
})

test("the tag does not override the version in the file", (t) => {
  const r = repo(t)
  r.commit("one")
  r.tag("v1.4.2")
  writeVersion("1.5.0", r.path)
  assert.equal(currentVersion(r.dir, r.path), "1.5.0")
})

test("with no version in the file, the git tag is the fallback", (t) => {
  const r = repo(t)
  r.commit("one")
  r.tag("v1.4.2")
  assert.equal(currentVersion(r.dir, r.path), "1.4.2")
})

test("past the tag, the fallback names the commit", (t) => {
  const r = repo(t)
  r.commit("one")
  r.tag("v1.0.0")
  r.commit("two")
  assert.match(tagVersion(r.dir), /^1\.0\.0-1-g[0-9a-f]+$/)
})

test("the newest tag on the history is the fallback", (t) => {
  const r = repo(t)
  r.commit("one")
  r.tag("v1.0.0")
  r.commit("two")
  r.tag("v1.1.0")
  assert.equal(tagVersion(r.dir), "1.1.0")
})

test("a tag that is not version-shaped is not a version", (t) => {
  const r = repo(t)
  r.commit("one")
  r.tag("latest")
  assert.equal(tagVersion(r.dir), "")
})

test("no repository and no tag are no version", (t) => {
  const empty = mkdtempSync(join(tmpdir(), "telesync-nogit-"))
  t.after(() => rmSync(empty, { recursive: true, force: true }))
  assert.equal(tagVersion(empty), "")
})

test("a checkout that is not a repository still gets the version, with a note", (t) => {
  const r = repo(t)
  rmSync(join(r.dir, ".git"), { recursive: true, force: true })
  assert.equal(tagHead("v1.0.2", r.dir), "no-repository")
})

test("a repository with no commit has no HEAD to tag", (t) => {
  const r = repo(t)
  assert.equal(tagHead("v1.0.2", r.dir), "no-commit")
})

test("a tag that exists is left alone", (t) => {
  const r = repo(t)
  r.commit("one")
  r.tag("v1.0.2")
  assert.equal(tagHead("v1.0.2", r.dir), "exists")
  r.commit("two")
  assert.equal(tagHead("v1.0.3", r.dir), "created")
})

test("writing a version rewrites only it and keeps the formatting", (t) => {
  const r = repo(t)
  assert.equal(writeVersion("1.2.3", r.path), true)
  assert.equal(
    readFileSync(r.path, "utf8"),
    '{\n  "name": "web",\n  "version": "1.2.3",\n  "private": true\n}\n',
  )
  assert.equal(writeVersion("1.2.3", r.path), false)
})

// The Docker build's stamp: write the version into package.json and never
// touch git. --no-tag is the same rule under its older name.
test("--stamp writes the version and never touches git", () => {
  assert.deepEqual(releaseCommand(["--stamp", "v1.2.3"]), {
    mode: "release",
    version: "1.2.3",
    writeOnly: true,
  })
  assert.deepEqual(releaseCommand(["1.2.3", "--stamp"]), {
    mode: "release",
    version: "1.2.3",
    writeOnly: true,
  })
  assert.deepEqual(releaseCommand(["1.2.3", "--no-tag"]), {
    mode: "release",
    version: "1.2.3",
    writeOnly: true,
  })
})

test("a plain release tags HEAD after writing the version", () => {
  assert.deepEqual(releaseCommand(["v1.2.3"]), {
    mode: "release",
    version: "1.2.3",
    writeOnly: false,
  })
})

test("query mode never writes and knows --json", () => {
  assert.deepEqual(releaseCommand([]), { mode: "query", asJSON: false })
  assert.deepEqual(releaseCommand(["--json"]), { mode: "query", asJSON: true })
  assert.deepEqual(releaseCommand(["--stamp"]), { mode: "query", asJSON: false })
})

test("write-only mode leaves a git tag out of the picture entirely", (t) => {
  const r = repo(t)
  r.commit("one")
  const plan = releaseCommand(["--stamp", "v1.2.3"])
  assert.equal(plan.writeOnly, true)
  assert.equal(writeVersion(plan.version, r.path), true)
  assert.equal(packageVersion(r.path), "1.2.3")
  const tags = execFileSync("git", ["-C", r.dir, "tag"], { encoding: "utf8" }).trim()
  assert.equal(tags, "", "no tag must exist after a stamp")
})
