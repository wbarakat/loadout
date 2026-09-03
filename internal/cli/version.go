package cli

import (
	"fmt"
	"io"
	"runtime/debug"
)

// Version is the release version, stamped at build time by the release
// workflow with:
//
//	-ldflags "-X loadout.dev/loadout/internal/cli.Version=v0.1.1"
//
// It stays empty for a build that does not stamp it, and versionString
// then falls back to Go's own build information.
var Version = ""

// versionString reports the version to print. It prefers the stamped
// Version. Failing that it reads Go's build information, which carries
// a real version for a binary installed with "go install pkg@version"
// and "(devel)" for one built from a working tree. When a working-tree
// build records a commit, that commit is shown instead, since "(devel)"
// alone tells a bug report nothing.
func versionString() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := info.Main.Version
	if v != "" && v != "(devel)" {
		return v
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			rev := s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
			return "devel-" + rev
		}
	}
	return "devel"
}

// cmdVersion prints the version on one line, so a bug report or a
// script can quote it.
func cmdVersion(out io.Writer, m mode) int {
	if m == modeJSON {
		printJSON(out, struct {
			Version string `json:"version"`
		}{versionString()})
		return 0
	}
	fmt.Fprintf(out, "loadout %s\n", versionString())
	return 0
}
