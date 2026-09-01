package vault

import (
	"sort"
	"strings"
)

// Item is one skill or fact, addressed as "<kind>/<name>" — "skill/x"
// or "memory/x" — with its hook text (the item's description). It is
// the shared shape "loadout list", "loadout recall", and the
// matching MCP tools all report.
type Item struct {
	Kind string
	Name string
	Hook string
}

// Address returns the item's "<kind>/<name>" address — the same
// string ParseAddress reads back.
func (it Item) Address() string {
	return it.Kind + "/" + it.Name
}

// HookOrDefault returns hook, or the fixed "(no description)"
// placeholder when hook is blank.
func HookOrDefault(hook string) string {
	if hook == "" {
		return "(no description)"
	}
	return hook
}

// HookLine renders "<name> — <hook>", with the "(no description)"
// placeholder for a blank hook.
func HookLine(name, hook string) string {
	return name + " — " + HookOrDefault(hook)
}

// AllItems returns every skill and fact as an Item: skills first,
// then facts, each in ListSkills/ListFacts order. Call SortItems on
// the result for the kind-then-name order "loadout list" and
// "loadout recall" both use.
func AllItems(v *Vault) ([]Item, error) {
	skills, err := ListSkills(v)
	if err != nil {
		return nil, err
	}
	facts, err := ListFacts(v)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(skills)+len(facts))
	for _, s := range skills {
		items = append(items, Item{Kind: "skill", Name: s.Name, Hook: s.Description})
	}
	for _, f := range facts {
		items = append(items, Item{Kind: "memory", Name: f.Name, Hook: f.Description})
	}
	return items, nil
}

// SortItems orders items kind then name — the order "loadout list"
// and "loadout recall" both use.
func SortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Name < items[j].Name
	})
}

// Recall returns every item that matches ALL terms, case-
// insensitively, sorted kind then name. A fact matches on its name,
// hook, and body; a skill matches on its name and hook only — Recall
// never reads a skill body. "loadout recall" and the MCP "recall"
// tool both use this.
func Recall(v *Vault, terms []string) ([]Item, error) {
	skills, err := ListSkills(v)
	if err != nil {
		return nil, err
	}
	facts, err := ListFacts(v)
	if err != nil {
		return nil, err
	}

	lower := make([]string, len(terms))
	for i, t := range terms {
		lower[i] = strings.ToLower(t)
	}

	var items []Item
	for _, s := range skills {
		haystack := strings.ToLower(s.Name + " " + s.Description)
		if matchesAllTerms(haystack, lower) {
			items = append(items, Item{Kind: "skill", Name: s.Name, Hook: s.Description})
		}
	}
	for _, f := range facts {
		haystack := strings.ToLower(f.Name + " " + f.Description + " " + f.Body)
		if matchesAllTerms(haystack, lower) {
			items = append(items, Item{Kind: "memory", Name: f.Name, Hook: f.Description})
		}
	}
	SortItems(items)
	return items, nil
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
