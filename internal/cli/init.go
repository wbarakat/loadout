package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// initResult is the JSON shape of "loadout init".
type initResult struct {
	Vault string `json:"vault"`
}

func cmdInit(out, errOut io.Writer, m mode) int {
	v, err := vault.Init(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		printJSON(out, initResult{Vault: v.Root})
		return 0
	}
	fmt.Fprintf(out, "created the vault at %s\n", v.Root)
	fmt.Fprintln(out, "next: loadout add skill <name>, then loadout sync")
	return 0
}
