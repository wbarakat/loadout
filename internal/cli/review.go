package cli

import (
	"fmt"
	"io"
	"sort"

	"loadout.dev/loadout/internal/vault"
)

const reviewUsage = "usage: loadout review [keep|drop <kind>/<name>]"

// noDraftsMessage is what "loadout review" prints when every item is
// already reviewed.
const noDraftsMessage = "no drafts. Every item is kept."

// cmdReview dispatches the three review forms: the bare list, keep,
// and drop.
func cmdReview(out, errOut io.Writer, args []string, m mode) int {
	if len(args) == 0 {
		return cmdReviewList(out, errOut, m)
	}
	switch args[0] {
	case "keep":
		return cmdReviewKeep(out, errOut, args[1:], m)
	case "drop":
		return cmdReviewDrop(out, errOut, args[1:], m)
	default:
		fmt.Fprintln(errOut, reviewUsage)
		return 2
	}
}

// draftLine is one row of "loadout review": a list-format line plus
// the provenance that review needs.
type draftLine struct {
	kind, name, hook, by, at string
}

// reviewDraft is one entry in the JSON shape of "loadout review".
type reviewDraft struct {
	Address string `json:"address"`
	Hook    string `json:"hook"`
	By      string `json:"by"`
	At      string `json:"at"`
}

// reviewListResult is the JSON shape of "loadout review".
type reviewListResult struct {
	Drafts []reviewDraft `json:"drafts"`
}

// cmdReviewList prints every draft item, list format plus by and at.
func cmdReviewList(out, errOut io.Writer, m mode) int {
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

	var drafts []draftLine
	for _, s := range skills {
		if s.Review == "draft" {
			drafts = append(drafts, draftLine{kind: "skill", name: s.Name, hook: s.Description, by: s.By, at: s.At})
		}
	}
	for _, f := range facts {
		if f.Review == "draft" {
			drafts = append(drafts, draftLine{kind: "memory", name: f.Name, hook: f.Description, by: f.By, at: f.At})
		}
	}
	sort.Slice(drafts, func(i, j int) bool {
		if drafts[i].kind != drafts[j].kind {
			return drafts[i].kind < drafts[j].kind
		}
		return drafts[i].name < drafts[j].name
	})
	if m == modeJSON {
		jsonDrafts := make([]reviewDraft, 0, len(drafts))
		for _, d := range drafts {
			jsonDrafts = append(jsonDrafts, reviewDraft{Address: d.kind + "/" + d.name, Hook: d.hook, By: d.by, At: d.at})
		}
		printJSON(out, reviewListResult{Drafts: jsonDrafts})
		return 0
	}
	if len(drafts) == 0 {
		fmt.Fprintln(out, noDraftsMessage)
		return 0
	}
	for _, d := range drafts {
		hook := d.hook
		if hook == "" {
			hook = "(no description)"
		}
		fmt.Fprintf(out, "%s/%s — %s (by %s, at %s)\n", d.kind, d.name, hook, d.by, d.at)
	}
	return 0
}

// reviewKeepResult is the JSON shape of "loadout review keep".
type reviewKeepResult struct {
	Address string `json:"address"`
	Review  string `json:"review"`
}

// cmdReviewKeep sets an item's review field to kept, under the vault
// lock, then snapshots the change.
func cmdReviewKeep(out, errOut io.Writer, args []string, m mode) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, reviewUsage)
		return 2
	}
	addr := args[0]
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	kind, name, err := vault.ParseAddress(addr)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	path, err := vault.ItemPath(v, kind, name)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	release, err := vault.Lock(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer release()
	if err := vault.SetReviewKept(path); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := vault.Snapshot(v, "review keep "+addr); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		printJSON(out, reviewKeepResult{Address: addr, Review: "kept"})
		return 0
	}
	fmt.Fprintf(out, "kept %s\n", addr)
	return 0
}

// reviewDropResult is the JSON shape of "loadout review drop".
type reviewDropResult struct {
	Address string `json:"address"`
	Dropped bool   `json:"dropped"`
}

// cmdReviewDrop deletes an item, under the vault lock, then snapshots
// the change.
func cmdReviewDrop(out, errOut io.Writer, args []string, m mode) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, reviewUsage)
		return 2
	}
	addr := args[0]
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	kind, name, err := vault.ParseAddress(addr)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	path, err := vault.ItemPath(v, kind, name)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	release, err := vault.Lock(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer release()
	if err := removeItemFile(kind, path); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := vault.Snapshot(v, "review drop "+addr); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		printJSON(out, reviewDropResult{Address: addr, Dropped: true})
		return 0
	}
	fmt.Fprintf(out, "dropped %s\n", addr)
	fmt.Fprintln(out, "next: run loadout sync")
	return 0
}
