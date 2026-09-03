package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

const reviewUsage = "usage: loadout review\n" +
	"       loadout review keep|drop <kind>/<name>...\n" +
	"       loadout review keep|drop --all [--by SOURCE] [--kind skill|memory] [--dry-run]"

// reviewSelection is the parsed shape of a "review keep" or
// "review drop" invocation: either an explicit list of addresses, or
// --all with optional filters.
type reviewSelection struct {
	addresses []string
	all       bool
	// by filters --all to items whose provenance equals this value, for
	// example "import:codex".
	by string
	// kind filters --all to "skill" or "memory".
	kind   string
	dryRun bool
}

// parseReviewSelection reads a keep/drop argument list. ok is false
// when the arguments do not make sense together, so the caller prints
// usage. Filters only mean something alongside --all: with an explicit
// address list, the addresses already say exactly what to act on.
func parseReviewSelection(args []string) (reviewSelection, bool) {
	var sel reviewSelection
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--all":
			sel.all = true
		case "--dry-run":
			sel.dryRun = true
		case "--by", "--kind":
			if i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "--") {
				return reviewSelection{}, false
			}
			if args[i] == "--by" {
				sel.by = args[i+1]
			} else {
				sel.kind = args[i+1]
			}
			i++
		default:
			if strings.HasPrefix(args[i], "--") {
				return reviewSelection{}, false
			}
			sel.addresses = append(sel.addresses, args[i])
		}
	}
	if sel.all == (len(sel.addresses) > 0) {
		// Neither given, or both given: ambiguous either way.
		return reviewSelection{}, false
	}
	if !sel.all && (sel.by != "" || sel.kind != "") {
		return reviewSelection{}, false
	}
	if sel.kind != "" && sel.kind != "skill" && sel.kind != "memory" {
		return reviewSelection{}, false
	}
	return sel, true
}

// collectDrafts returns every draft item in the vault, sorted by kind
// then name, the same order "loadout review" prints.
func collectDrafts(v *vault.Vault) ([]draftLine, error) {
	skills, err := vault.ListSkills(v)
	if err != nil {
		return nil, err
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return nil, err
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
	return drafts, nil
}

// resolveReviewTargets turns a selection into the exact addresses to
// act on. For --all it reads the vault's drafts and applies the
// filters; for an explicit list it returns the addresses unchanged.
func resolveReviewTargets(v *vault.Vault, sel reviewSelection) ([]string, error) {
	if !sel.all {
		return sel.addresses, nil
	}
	drafts, err := collectDrafts(v)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range drafts {
		if sel.by != "" && d.by != sel.by {
			continue
		}
		if sel.kind != "" && d.kind != sel.kind {
			continue
		}
		out = append(out, d.kind+"/"+d.name)
	}
	return out, nil
}

// reviewBulkResult is the JSON shape of a keep or drop that acts on
// more than one item, or on --all. The single-address forms keep their
// original shapes (reviewKeepResult, reviewDropResult) so existing
// scripts continue to parse them.
type reviewBulkResult struct {
	Action    string   `json:"action"`
	DryRun    bool     `json:"dry_run,omitempty"`
	Count     int      `json:"count"`
	Addresses []string `json:"addresses"`
}

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
	drafts, err := collectDrafts(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
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
	return runReview(out, errOut, args, m, "keep")
}

// runReview carries out a keep or drop over one or many items.
//
// It resolves every target and validates all of them BEFORE it changes
// anything, so a bad address in a batch of fifty cannot leave the vault
// half done. It then takes the vault lock once and writes ONE snapshot
// for the whole batch, which is what lets a single "loadout undo"
// reverse a fifty-item decision.
//
// Dropping is destructive, so it refuses to delete an item that is not
// a draft, exactly as the single-item form always has. --dry-run
// reports the same resolved list and touches nothing.
func runReview(out, errOut io.Writer, args []string, m mode, action string) int {
	sel, ok := parseReviewSelection(args)
	if !ok {
		fmt.Fprintln(errOut, reviewUsage)
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	addresses, err := resolveReviewTargets(v, sel)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if len(addresses) == 0 {
		fmt.Fprintln(out, "nothing matched. No item changed.")
		return 0
	}

	// Validate every target first: parse the address, find the file,
	// and for a drop confirm the item really is a draft.
	paths := make([]string, 0, len(addresses))
	kinds := make([]string, 0, len(addresses))
	for _, addr := range addresses {
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
		if action == "drop" {
			draft, err := itemIsDraft(v, kind, name)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			if !draft {
				fmt.Fprintf(errOut, "%s: not a draft. Fix: remove the item file directly, or run loadout review to see the drafts.\n", addr)
				return 1
			}
		}
		paths = append(paths, path)
		kinds = append(kinds, kind)
	}

	if sel.dryRun {
		if m == modeJSON {
			printJSON(out, reviewBulkResult{Action: action, DryRun: true, Count: len(addresses), Addresses: addresses})
			return 0
		}
		fmt.Fprintf(out, "would %s %s:\n", action, countNoun(len(addresses), "item", "items"))
		for _, addr := range addresses {
			fmt.Fprintln(out, "  "+addr)
		}
		fmt.Fprintln(out, "nothing changed. Run the same command without --dry-run to apply it.")
		return 0
	}

	release, err := vault.Lock(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer release()

	for i, path := range paths {
		if action == "drop" {
			err = removeItemFile(kinds[i], path)
		} else {
			err = vault.SetReviewKept(path)
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}

	subject := fmt.Sprintf("review %s %s", action, addresses[0])
	if len(addresses) > 1 {
		subject = fmt.Sprintf("review %s %s", action, countNoun(len(addresses), "item", "items"))
	}
	if err := vault.Snapshot(v, subject); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	// One item through the classic form keeps its original output, so
	// existing scripts and the documented shapes still hold.
	single := len(addresses) == 1 && !sel.all
	if m == modeJSON {
		switch {
		case single && action == "keep":
			printJSON(out, reviewKeepResult{Address: addresses[0], Review: "kept"})
		case single && action == "drop":
			printJSON(out, reviewDropResult{Address: addresses[0], Dropped: true})
		default:
			printJSON(out, reviewBulkResult{Action: action, Count: len(addresses), Addresses: addresses})
		}
		return 0
	}
	if single {
		if action == "drop" {
			fmt.Fprintf(out, "dropped %s\n", addresses[0])
			fmt.Fprintln(out, "next: run loadout sync")
		} else {
			fmt.Fprintf(out, "kept %s\n", addresses[0])
		}
		return 0
	}
	past := "kept"
	if action == "drop" {
		past = "dropped"
	}
	fmt.Fprintf(out, "%s %s:\n", past, countNoun(len(addresses), "item", "items"))
	for _, addr := range addresses {
		fmt.Fprintln(out, "  "+addr)
	}
	fmt.Fprintln(out, "undo all of it with: loadout undo")
	if action == "drop" {
		fmt.Fprintln(out, "next: run loadout sync")
	}
	return 0
}

// reviewDropResult is the JSON shape of "loadout review drop".
type reviewDropResult struct {
	Address string `json:"address"`
	Dropped bool   `json:"dropped"`
}

// cmdReviewDrop deletes one or many draft items, under a single vault
// lock, then snapshots the whole change once.
func cmdReviewDrop(out, errOut io.Writer, args []string, m mode) int {
	return runReview(out, errOut, args, m, "drop")
}

// itemIsDraft reports whether the item named kind/name currently
// holds review: draft. cmdReviewDrop calls this before it deletes
// anything, so a human's kept item can never be destroyed by a
// mistaken or malicious drop.
func itemIsDraft(v *vault.Vault, kind, name string) (bool, error) {
	switch kind {
	case "skill":
		skills, err := vault.ListSkills(v)
		if err != nil {
			return false, err
		}
		for _, s := range skills {
			if s.Name == name {
				return s.Review == "draft", nil
			}
		}
	case "memory":
		facts, err := vault.ListFacts(v)
		if err != nil {
			return false, err
		}
		for _, f := range facts {
			if f.Name == name {
				return f.Review == "draft", nil
			}
		}
	}
	return false, nil
}
