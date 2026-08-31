package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func cmdSync(out, errOut io.Writer) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	failed := false
	for _, a := range adapter.Enabled(v) {
		if err := a.Apply(v); err != nil {
			fmt.Fprintf(errOut, "%s: %v\n", a.Name(), err)
			failed = true
			continue
		}
		fmt.Fprintf(out, "synced %s\n", a.Name())
	}
	if err := vault.Snapshot(v, "sync"); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if failed {
		return 1
	}
	return 0
}
