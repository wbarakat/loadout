package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"loadout.dev/loadout/internal/vault"
)

const editUsage = "usage: loadout edit <kind>/<name>"

// cmdEdit opens one item's file in $EDITOR, then prints the next
// step. It falls back to vi when $EDITOR is unset. It exits 1 before
// it spawns an editor when the address does not parse or the item
// does not exist, so a missing item never opens an editor.
func cmdEdit(out, errOut io.Writer, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, editUsage)
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
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, "next: run loadout sync")
	return 0
}
