package cli

import (
	"fmt"
	"io"
	"strings"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

// syncResult is the JSON shape of "loadout sync".
type syncResult struct {
	Reports  []adapter.Report `json:"reports"`
	Snapshot bool             `json:"snapshot"`
	DryRun   bool             `json:"dry_run,omitempty"`
}

// extractDryRun removes the first "--dry-run" argument found at any
// position in args, and reports whether it found one. cmdSync calls
// this on its own arguments, the way Run strips "--json" before any
// verb sees its arguments.
func extractDryRun(args []string) ([]string, bool) {
	for i, a := range args {
		if a == "--dry-run" {
			out := make([]string, 0, len(args)-1)
			out = append(out, args[:i]...)
			out = append(out, args[i+1:]...)
			return out, true
		}
	}
	return args, false
}

func cmdSync(out, errOut io.Writer, args []string, m mode) int {
	rest, dry := extractDryRun(args)
	if len(rest) > 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	release, err := vault.Lock(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer release()
	problem := false
	reports := []adapter.Report{}
	for _, a := range adapter.Enabled(v) {
		report, err := a.Apply(v, dry)
		if err != nil {
			report.Error = err.Error()
			fmt.Fprintf(errOut, "%s: %v\n", a.Name(), err)
			problem = true
			reports = append(reports, report)
			continue
		}
		reports = append(reports, report)
		if m != modeJSON {
			if dry {
				fmt.Fprintf(out, "would sync %s (%d to link, %d to prune; memory: %s)\n", a.Name(), report.Linked, len(report.Pruned), memoryStatus(report.Applied))
			} else {
				fmt.Fprintf(out, "synced %s (%d linked, %d pruned)\n", a.Name(), report.Linked, len(report.Pruned))
			}
		}
		for _, b := range report.Blocked {
			fmt.Fprintf(errOut, "%s: %s\n", a.Name(), b)
		}
		// A blocked skill only fails a real sync. A dry run just
		// previews it: nothing was written, so nothing failed.
		if len(report.Blocked) > 0 && !dry {
			problem = true
		}
	}
	if !dry {
		if err := vault.Snapshot(v, "sync"); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	if m == modeJSON {
		printJSON(out, syncResult{Reports: reports, Snapshot: !dry, DryRun: dry})
	}
	if problem {
		return 1
	}
	return 0
}

// memoryStatus finds the one applied entry that reports the memory
// block's status ("up to date" or "block would change") and returns
// just that status word, for the dry summary line. Every adapter's
// Apply call appends exactly one such entry, so this always finds
// one in practice; it returns "" only if that ever stops holding.
func memoryStatus(applied []string) string {
	const prefix = "memory: "
	for _, a := range applied {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix)
		}
	}
	return ""
}
