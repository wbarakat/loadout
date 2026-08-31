package cli

import (
	"fmt"
	"io"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

const recallUsage = "usage: loadout recall <term>..."

// noMatchMessage is the fixed message recall prints when no item
// matches every term.
const noMatchMessage = "no items match. Fix: run loadout list to see every item."

// cmdRecall searches every item for terms that all match, case-
// insensitively, and prints the matches in list format. A fact
// matches on its name, hook, and body; a skill matches on its name
// and hook only — recall never reads a skill body.
func cmdRecall(out, errOut io.Writer, args []string, m mode) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, recallUsage)
		return 2
	}
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

	terms := make([]string, len(args))
	for i, a := range args {
		terms[i] = strings.ToLower(a)
	}

	var items []listItem
	for _, s := range skills {
		haystack := strings.ToLower(s.Name + " " + s.Description)
		if matchesAllTerms(haystack, terms) {
			items = append(items, listItem{kind: "skill", name: s.Name, hook: s.Description})
		}
	}
	for _, f := range facts {
		haystack := strings.ToLower(f.Name + " " + f.Description + " " + f.Body)
		if matchesAllTerms(haystack, terms) {
			items = append(items, listItem{kind: "memory", name: f.Name, hook: f.Description})
		}
	}

	sortItems(items)
	if m == modeJSON {
		printJSON(out, itemsResult{Items: toJSONItems(items)})
		return 0
	}
	if len(items) == 0 {
		fmt.Fprintln(out, noMatchMessage)
		return 0
	}
	printItems(out, items)
	return 0
}

// matchesAllTerms reports whether haystack contains every term.
func matchesAllTerms(haystack string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(haystack, t) {
			return false
		}
	}
	return true
}
