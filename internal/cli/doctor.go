package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

// doctorProblem is one entry in the JSON shape of "loadout doctor".
// Source names where the problem comes from: "vault" for a vault-
// level check, or the adapter's name for an adapter check.
type doctorProblem struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// doctorResult is the JSON shape of "loadout doctor".
type doctorResult struct {
	Problems []doctorProblem `json:"problems"`
	Count    int             `json:"count"`
}

func cmdDoctor(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	problems := []doctorProblem{}
	if _, err := os.Stat(filepath.Join(v.Root, ".git")); err != nil {
		problems = append(problems, doctorProblem{
			Source: "vault",
			Detail: "the vault history is missing",
			Fix:    "restore the .git directory from a backup, or re-create the vault.",
		})
	}
	repos, err := vault.EmbeddedSkillRepos(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, d := range repos {
		problems = append(problems, doctorProblem{
			Source: "vault",
			Detail: fmt.Sprintf("the skill folder %s is a git repository", d),
			Fix:    "remove its .git directory; the vault keeps history for you.",
		})
	}
	bad, err := vault.InvalidSkillDirs(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, d := range bad {
		problems = append(problems, doctorProblem{
			Source: "vault",
			Detail: fmt.Sprintf("the skill directory %s has no SKILL.md file", d),
			Fix:    "add a SKILL.md file, or remove the directory",
		})
	}
	for _, a := range adapter.Enabled(v) {
		for _, p := range a.Check(v) {
			problems = append(problems, doctorProblem{Source: p.Adapter, Detail: p.Detail, Fix: p.Fix})
		}
	}
	remoteStatus, hasRemote, err := remote.LoadStatus(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if hasRemote && remoteStatus.State != "in sync" {
		problems = append(problems, doctorRemoteProblem(remoteStatus))
	}
	count := len(problems)
	if m == modeJSON {
		printJSON(out, doctorResult{Problems: problems, Count: count})
		if count == 0 {
			return 0
		}
		return 1
	}
	for _, p := range problems {
		fmt.Fprintf(out, "%s: %s\n  fix: %s\n", p.Source, p.Detail, p.Fix)
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
