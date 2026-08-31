package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// printWarnings writes each manifest warning to errOut, one per
// line. Sync, status, and doctor call it once, right after they open
// the vault.
func printWarnings(errOut io.Writer, v *vault.Vault) {
	for _, w := range v.Warnings {
		fmt.Fprintln(errOut, w)
	}
}
