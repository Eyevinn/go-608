// Package internal holds build-time version stamping and shared command
// helpers that are deliberately kept out of go-608's public API.
package internal

import (
	"fmt"
	"strconv"
	"time"
)

// Build-time version information. The defaults apply to an un-stamped
// `go run` / `go test`; the Makefile overrides both via -ldflags -X:
//
//	-X github.com/Eyevinn/go-608/internal.commitVersion=$(git describe --tags --always HEAD)
//	-X github.com/Eyevinn/go-608/internal.commitDate=$(git log -1 --format=%ct)
//
// The pair moves twice per cycle, and the two states say different things:
//
//   - Between releases (now): commitVersion carries a -dev suffix for the release being
//     worked towards, and commitDate is empty. An un-stamped dev build therefore reports
//     no date, which is the honest answer — it was not cut from a release, so there is no
//     release date to give.
//   - At a release: chore(release) drops the -dev suffix and sets commitDate to that
//     commit's own timestamp, so an un-stamped build of the release reports both. Since
//     GetVersion renders only the date, later commits on that release do not move it.
//
// A stamped build always wins over either.
var (
	commitVersion = "v0.10.0-dev"
	commitDate    = "" // commit date in Unix epoch seconds; set at release
)

// GetVersion returns the build version, appending the commit date when it is
// present and parseable (e.g. "v0.1.0, date: 2026-07-17").
func GetVersion() string {
	if commitDate != "" {
		if seconds, err := strconv.Atoi(commitDate); err == nil {
			t := time.Unix(int64(seconds), 0).UTC()
			return fmt.Sprintf("%s, date: %s", commitVersion, t.Format("2006-01-02"))
		}
	}
	return commitVersion
}
