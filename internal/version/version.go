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
var Version = "dev"

// Commit is the full or short commit hash, injected at build time.
var Commit = ""

// Date is the build date (RFC3339), injected at build time.
var Date = ""

// String returns the version string, falling back to build info.
func String() string {
	v := Version
	if v != "" && v != "dev" {
		return v
	}

	// Fallback: Go toolchain VCS stamping (go >= 1.18, git checkout).
	var rev, modified, stampTime string
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
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
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if Commit == "" {
			Commit = rev
		}
		if v == "" || v == "dev" {
			v = rev
		}
		v += "-" + rev + modified
	}
	if Date == "" && stampTime != "" {
		if t, err := time.Parse(time.RFC3339, stampTime); err == nil {
			Date = t.UTC().Format("2006-01-02T15:04:05Z")
		}
	}
	if v == "" {
		v = "dev"
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
