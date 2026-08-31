package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

// statusAdapter is one adapter's entry in the JSON shape of "loadout
// status".
type statusAdapter struct {
	Name     string `json:"name"`
	Problems int    `json:"problems"`
}

// statusResult is the JSON shape of "loadout status".
type statusResult struct {
	Vault    string          `json:"vault"`
	Skills   int             `json:"skills"`
	Facts    int             `json:"facts"`
	Adapters []statusAdapter `json:"adapters"`
}

func cmdStatus(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
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
	if m == modeJSON {
		adapters := []statusAdapter{}
		for _, a := range adapter.Enabled(v) {
			adapters = append(adapters, statusAdapter{Name: a.Name(), Problems: len(a.Check(v))})
		}
		printJSON(out, statusResult{Vault: v.Root, Skills: len(skills), Facts: len(facts), Adapters: adapters})
		return 0
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
