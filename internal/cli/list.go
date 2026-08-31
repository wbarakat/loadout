package cli

import (
	"fmt"
	"io"
	"sort"

	"loadout.dev/loadout/internal/vault"
)

// listItem is one line of "loadout list" output.
type listItem struct {
	kind, name, hook string
}

// cmdList prints every item in the vault, kind then name order, one
// line each: "<kind>/<name> — <hook>".
func cmdList(out, errOut io.Writer) int {
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
	items := make([]listItem, 0, len(skills)+len(facts))
	for _, s := range skills {
		items = append(items, listItem{kind: "skill", name: s.Name, hook: s.Description})
	}
	for _, f := range facts {
		items = append(items, listItem{kind: "memory", name: f.Name, hook: f.Description})
	}
	sortItems(items)
	printItems(out, items)
	return 0
}

// sortItems orders items kind then name, the order "loadout list" and
// "loadout recall" both use.
func sortItems(items []listItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].kind != items[j].kind {
			return items[i].kind < items[j].kind
		}
		return items[i].name < items[j].name
	})
}

// printItems writes one line per item: "<kind>/<name> — <hook>". A
// blank hook becomes "(no description)". Both "loadout list" and
// "loadout recall" use this, so their output lines match exactly.
func printItems(out io.Writer, items []listItem) {
	for _, it := range items {
		hook := it.hook
		if hook == "" {
			hook = "(no description)"
		}
		fmt.Fprintf(out, "%s/%s — %s\n", it.kind, it.name, hook)
	}
}
