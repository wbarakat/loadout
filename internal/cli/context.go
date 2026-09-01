package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// recentHistoryCount is how many history subjects the JSON shape of
// "loadout context" reports under "recent". The text shape uses the
// same count through vault.RenderContext.
const recentHistoryCount = 3

// contextItem is one fact or skill entry in the JSON shape of
// "loadout context". Address is "<kind>/<name>", the same shape
// "loadout list" and "loadout recall" use, so a caller can pass it
// straight to "loadout show" or "loadout edit" without guessing kind.
type contextItem struct {
	Address string `json:"address"`
	Hook    string `json:"hook"`
}

// contextResult is the JSON shape of "loadout context".
type contextResult struct {
	Vault      string        `json:"vault"`
	Skills     int           `json:"skills"`
	Facts      int           `json:"facts"`
	Memory     []contextItem `json:"memory"`
	SkillsList []contextItem `json:"skills_list"`
	Recent     []string      `json:"recent"`
}

// cmdContext prints the compact picture of the vault: the counts,
// every fact hook, every skill hook, and the last few history
// subjects.
func cmdContext(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)

	if m == modeJSON {
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
		subjects, err := vault.RecentSubjects(v, recentHistoryCount)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		memory := make([]contextItem, 0, len(facts))
		for _, f := range facts {
			memory = append(memory, contextItem{Address: "memory/" + f.Name, Hook: f.Description})
		}
		skillsList := make([]contextItem, 0, len(skills))
		for _, s := range skills {
			skillsList = append(skillsList, contextItem{Address: "skill/" + s.Name, Hook: s.Description})
		}
		recent := make([]string, 0, len(subjects))
		recent = append(recent, subjects...)
		printJSON(out, contextResult{
			Vault:      v.Root,
			Skills:     len(skills),
			Facts:      len(facts),
			Memory:     memory,
			SkillsList: skillsList,
			Recent:     recent,
		})
		return 0
	}

	text, err := vault.RenderContext(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprint(out, text)
	return 0
}
