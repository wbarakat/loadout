package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func cmdStatus(out, errOut io.Writer) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "vault: %s\nskills: %d\nmemory facts: %d\n", v.Root, len(skills), len(facts))
	for _, a := range adapter.Enabled(v) {
		if ps := a.Check(v); len(ps) == 0 {
			fmt.Fprintf(out, "%s: in sync\n", a.Name())
		} else {
			fmt.Fprintf(out, "%s: %d problems (run: loadout doctor)\n", a.Name(), len(ps))
		}
	}
	return 0
}
