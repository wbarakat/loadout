package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// cmdUndo reverts the vault to the state before its last history
// entry, under the vault lock, then prints the next step.
func cmdUndo(out, errOut io.Writer) int {
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
	if err := vault.Undo(v); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, "restored the previous vault state")
	fmt.Fprintln(out, "next: run loadout sync to project it")
	return 0
}
