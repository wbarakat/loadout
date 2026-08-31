package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

func cmdInit(out, errOut io.Writer) int {
	v, err := vault.Init(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "created the vault at %s\n", v.Root)
	fmt.Fprintln(out, "next: loadout add skill <name>, then loadout sync")
	return 0
}
