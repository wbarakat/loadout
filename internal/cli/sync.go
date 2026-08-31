package cli

import (
	"fmt"
	"io"
	"strings"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func cmdSync(out, errOut io.Writer) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	problem := false
	for _, a := range adapter.Enabled(v) {
		report, err := a.Apply(v, false)
		if err != nil {
			fmt.Fprintf(errOut, "%s: %v\n", a.Name(), err)
			problem = true
			continue
		}
		fmt.Fprintf(out, "synced %s (%d linked, %d pruned)\n", a.Name(), countSkillLinks(report.Applied), len(report.Pruned))
		for _, b := range report.Blocked {
			fmt.Fprintf(errOut, "%s: %s\n", a.Name(), b)
		}
		if len(report.Blocked) > 0 {
			problem = true
		}
	}
	if err := vault.Snapshot(v, "sync"); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if problem {
		return 1
	}
	return 0
}

// countSkillLinks counts the entries in an applied list that name a
// linked skill, not a memory write. Only these entries count as
// "linked" in the sync summary line.
func countSkillLinks(applied []string) int {
	n := 0
	for _, a := range applied {
		if strings.HasPrefix(a, "skill/") {
			n++
		}
	}
	return n
}
