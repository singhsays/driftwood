// Package version reports the driftwood build version.
//
// Version is injected at build time via:
//
//	-ldflags "-X driftwood/internal/version.Version=$(git describe --tags --always --dirty)"
//
// When not injected (bare `go build`, `go run`), falls back to embedded VCS build info.
package version

import (
	"fmt"
	"runtime/debug"
	"strings"
	"time"
)

// Version is the semver tag the binary was built from (e.g. "v0.1.0"),
// or a git-describe string like "v0.1.0-3-gabc1234-dirty".
var Version = defaultVersion

// Commit is the full or short commit hash, injected at build time.
var Commit = ""

// defaultVersion is the placeholder when no version was injected.
const defaultVersion = "dev"

// develVersion is what the toolchain reports for a non-module build.
const develVersion = "(devel)"

// shortCommitLen is the abbreviated commit hash length.
const shortCommitLen = 12

// Date is the build date (RFC3339), injected at build time.
var Date = ""

// String returns the version string, falling back to build info.
func String() string {
	v := Version
	if v != "" && v != defaultVersion {
		return v
	}

	// Fallback: Go toolchain VCS stamping (go >= 1.18, git checkout).
	var rev, modified, stampTime string
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != develVersion {
			v = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					modified = "-dirty"
				}
			case "vcs.time":
				stampTime = s.Value
			}
		}
	}
	if rev != "" {
		short := rev
		if len(short) > shortCommitLen {
			short = short[:shortCommitLen]
		}
		if Commit == "" {
			Commit = short
		}
		if v == "" || v == defaultVersion {
			v = short
		}
		// A module pseudo-version (go install from a tag) already encodes
		// the revision; don't append it twice.
		if !strings.Contains(v, short) {
			v += "-" + short + modified
		}
	}
	if Date == "" && stampTime != "" {
		if t, err := time.Parse(time.RFC3339, stampTime); err == nil {
			Date = t.UTC().Format("2006-01-02T15:04:05Z")
		}
	}
	if v == "" {
		v = defaultVersion
	}
	return v
}

// Full returns a human-readable version with commit and build date.
func Full() string {
	var parts []string
	if Commit != "" {
		parts = append(parts, "commit "+Commit)
	}
	if Date != "" {
		parts = append(parts, "built "+Date)
	}
	if len(parts) == 0 {
		return String()
	}
	return fmt.Sprintf("%s (%s)", String(), strings.Join(parts, ", "))
}
