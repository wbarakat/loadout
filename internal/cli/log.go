package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// logCount is the number of history entries "loadout log" prints.
const logCount = 20

// cmdLog prints the last logCount vault history entries, one per
// line, newest first: "<date>  <subject>".
func cmdLog(out, errOut io.Writer) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	entries, err := vault.History(v, logCount)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, e := range entries {
		fmt.Fprintf(out, "%s  %s\n", e.At, e.Subject)
	}
	return 0
}
