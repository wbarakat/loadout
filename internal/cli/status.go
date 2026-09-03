package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

// statusAdapter is one adapter's entry in the JSON shape of "loadout
// status".
type statusAdapter struct {
	Name     string `json:"name"`
	Problems int    `json:"problems"`
}

// statusRemote is the remote's entry in the JSON shape of "loadout
// status". It never carries the token.
type statusRemote struct {
	URL    string `json:"url"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// statusResult is the JSON shape of "loadout status".
type statusResult struct {
	Vault    string          `json:"vault"`
	Skills   int             `json:"skills"`
	Facts    int             `json:"facts"`
	Adapters []statusAdapter `json:"adapters"`
	Remote   *statusRemote   `json:"remote,omitempty"`
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
	remoteStatus, hasRemote, remoteErr := remote.LoadStatus(v)
	if remoteErr != nil {
		fmt.Fprintln(errOut, remoteErr)
		return 1
	}

	if m == modeJSON {
		adapters := []statusAdapter{}
		for _, a := range adapter.Enabled(v) {
			adapters = append(adapters, statusAdapter{Name: a.Name(), Problems: len(a.Check(v))})
		}
		result := statusResult{Vault: v.Root, Skills: len(skills), Facts: len(facts), Adapters: adapters}
		if hasRemote {
			result.Remote = &statusRemote{URL: remoteStatus.URL, State: remoteStatus.State, Detail: remoteStatus.Detail}
		}
		printJSON(out, result)
		return 0
	}
	fmt.Fprintf(out, "vault: %s\nskills: %d\nmemory facts: %d\n", v.Root, len(skills), len(facts))
	for _, a := range adapter.Enabled(v) {
		ps := a.Check(v)
		switch {
		case len(ps) == 0:
			fmt.Fprintf(out, "%s: in sync\n", a.Name())
		case adapter.PendingSyncOnly(ps):
			// Nothing is wrong: this adapter simply has not been
			// projected yet. A fresh install is entirely in this state,
			// and calling it "problems" alarms a new user for no reason.
			fmt.Fprintf(out, "%s: %s pending (run: loadout sync)\n",
				a.Name(), countNoun(len(ps), "change", "changes"))
		default:
			fmt.Fprintf(out, "%s: %s (run: loadout doctor)\n",
				a.Name(), countNoun(len(ps), "problem", "problems"))
		}
	}
	if hasRemote {
		fmt.Fprintln(out, remoteStatusLine(remoteStatus))
	}
	return 0
}
