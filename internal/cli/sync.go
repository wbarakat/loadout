package cli

import (
	"fmt"
	"io"
	"strings"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

// syncRemoteResult is the JSON shape of "loadout sync --remote"'s
// remote outcome.
type syncRemoteResult struct {
	Version string `json:"version"`
	Pushed  bool   `json:"pushed"`
	Merged  bool   `json:"merged"`
}

// syncResult is the JSON shape of "loadout sync".
type syncResult struct {
	Reports  []adapter.Report  `json:"reports"`
	Snapshot bool              `json:"snapshot"`
	DryRun   bool              `json:"dry_run,omitempty"`
	Remote   *syncRemoteResult `json:"remote,omitempty"`
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

// extractRemoteFlag removes the first "--remote" argument found at
// any position in args, and reports whether it found one.
func extractRemoteFlag(args []string) ([]string, bool) {
	for i, a := range args {
		if a == "--remote" {
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
	rest, wantRemote := extractRemoteFlag(rest)
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

	reports, problem, err := syncLocal(v, out, errOut, dry, m)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	var remoteResult *syncRemoteResult
	switch {
	// A dry run stays a preview: --remote never pushes, pulls, or
	// merges anything while --dry-run is also set.
	case wantRemote && !dry:
		res, err := remote.Sync(v)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		remoteResult = &syncRemoteResult{Version: res.Version, Pushed: res.Pushed, Merged: res.Merged}
		if m != modeJSON {
			fmt.Fprintf(out, "synced with the remote (version %s)\n", res.Version)
		}
	case wantRemote && dry:
		// Still name the remote a real sync would reach, without ever
		// making a network call: --dry-run must stay a true preview,
		// but silently dropping the remote half of the preview left a
		// user with no idea --remote even did anything.
		cfg, err := remote.Load(v)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if m != modeJSON {
			fmt.Fprintf(out, "would sync with the remote at %s\n", cfg.URL)
		}
	}

	if m == modeJSON {
		printJSON(out, syncResult{Reports: reports, Snapshot: !dry, DryRun: dry, Remote: remoteResult})
	}
	if problem {
		return 1
	}
	return 0
}

// syncLocal projects the vault into every enabled adapter, under the
// vault lock, releasing that lock before it returns. remote.Sync
// takes the same lock itself, so cmdSync must never still hold this
// one when it calls remote.Sync: two flock acquisitions from the same
// process on the same file never share ownership, and the second
// would simply wait out its own first holder until it times out.
func syncLocal(v *vault.Vault, out, errOut io.Writer, dry bool, m mode) ([]adapter.Report, bool, error) {
	release, err := vault.Lock(v)
	if err != nil {
		return nil, false, err
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
				line := fmt.Sprintf("would sync %s (%d to link, %d to adopt, %d to prune", a.Name(), report.Linked, len(report.Adopted), len(report.Pruned))
				if status := memoryStatus(report.Applied); status != "" {
					line += "; memory: " + status
				}
				fmt.Fprintln(out, line+")")
			} else {
				fmt.Fprintf(out, "synced %s (%d linked, %d adopted, %d pruned)\n", a.Name(), report.Linked, len(report.Adopted), len(report.Pruned))
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
			return reports, problem, err
		}
	}
	return reports, problem, nil
}

// memoryStatus finds the one applied entry that reports the memory
// block's status ("up to date" or "block would change") and returns
// just that status word, for the dry summary line. A memoryBlock or
// memoryImport adapter's Apply call appends exactly one such entry. A
// memoryNone adapter appends none, so this returns "" for it, and the
// dry summary line then carries no memory clause at all.
func memoryStatus(applied []string) string {
	const prefix = "memory: "
	for _, a := range applied {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix)
		}
	}
	return ""
}
