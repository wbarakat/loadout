// Package cli implements the loadout commands.
package cli

import (
	"fmt"
	"io"
)

const usage = `usage: loadout <command>

commands:
  init            create the vault
  add skill NAME  add a skill
  add memory NAME add a memory fact
  sync            project the vault into every enabled tool
  status          show the vault and the adapter state
  doctor          find problems and show the fix for each one
`

func Run(out, errOut io.Writer, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	switch args[0] {
	case "init":
		return cmdInit(out, errOut)
	case "add":
		return cmdAdd(out, errOut, args[1:])
	default:
		fmt.Fprintf(errOut, "unknown command %q\n%s", args[0], usage)
		return 2
	}
}
