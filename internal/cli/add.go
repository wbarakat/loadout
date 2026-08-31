package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

func cmdAdd(out, errOut io.Writer, args []string) int {
	if len(args) != 2 || (args[0] != "skill" && args[0] != "memory") {
		fmt.Fprintln(errOut, "usage: loadout add skill|memory <name>")
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	kind, name := args[0], args[1]
	var path string
	if kind == "skill" {
		path, err = vault.AddSkill(v, name)
	} else {
		path, err = vault.AddFact(v, name)
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := vault.Snapshot(v, "add "+kind+" "+name); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "created %s\n", path)
	return 0
}
