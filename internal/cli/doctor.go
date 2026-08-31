package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func cmdDoctor(out, errOut io.Writer) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	count := 0
	bad, err := vault.InvalidSkillDirs(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, d := range bad {
		count++
		fmt.Fprintf(out, "vault: the skill directory %s has no SKILL.md file\n  fix: add a SKILL.md file, or remove the directory\n", d)
	}
	for _, a := range adapter.Enabled(v) {
		for _, p := range a.Check(v) {
			count++
			fmt.Fprintf(out, "%s: %s\n  fix: %s\n", p.Adapter, p.Detail, p.Fix)
		}
	}
	if count == 0 {
		fmt.Fprintln(out, "all good")
		return 0
	}
	if count == 1 {
		fmt.Fprintln(out, "1 problem")
	} else {
		fmt.Fprintf(out, "%d problems\n", count)
	}
	return 1
}
