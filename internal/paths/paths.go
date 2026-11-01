// Package paths contains path helpers shared across driftwood packages.
package paths

import (
	"os"
	"strings"
)

// ExpandHome resolves a leading ~ or ~/ to the current user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}
