// The panel's version, read from its own package.json at build time.
//
// `make version v1.0.2` writes the version there (and makes the git tag), the
// panel imports the file, so the bundle carries exactly what that release
// wrote. Vite picks up a new version without restarting its dev server.
//
// Only the version is imported, by name: a JSON module imports every field it
// is asked for, and the package's scripts and dependencies have no business
// in a browser bundle.

import { version } from "../../package.json"

// appVersion is exactly what package.json holds (`1.0.2`, shown as v1.0.2).
// Whatever version is written there is shown; no version is not hidden.
export const appVersion: string = version
