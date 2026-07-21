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
var (
	commitVersion = "v0.5.0"
	commitDate    = "" // commit date in Unix epoch seconds
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
