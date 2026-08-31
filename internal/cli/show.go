package cli

import (
	"fmt"
	"io"
	"os"

	"loadout.dev/loadout/internal/vault"
)

const showUsage = "usage: loadout show <kind>/<name>"

// cmdShow prints one item's file, raw, exit 0. It exits 1 when the
// address does not parse or the item does not exist.
func cmdShow(out, errOut io.Writer, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, showUsage)
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	kind, name, err := vault.ParseAddress(args[0])
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	path, err := vault.ItemPath(v, kind, name)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	out.Write(data)
	return 0
}
