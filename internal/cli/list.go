package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// jsonItem is one entry in the JSON shape of "loadout list" and
// "loadout recall": {items: [{address, hook}]}.
type jsonItem struct {
	Address string `json:"address"`
	Hook    string `json:"hook"`
}

// itemsResult is the JSON shape of "loadout list" and "loadout
// recall".
type itemsResult struct {
	Items []jsonItem `json:"items"`
}

// toJSONItems turns vault items into their JSON shape. hook is the
// raw hook string, which may be empty — unlike the text form, it
// does not carry the "(no description)" placeholder.
func toJSONItems(items []vault.Item) []jsonItem {
	out := make([]jsonItem, 0, len(items))
	for _, it := range items {
		out = append(out, jsonItem{Address: it.Address(), Hook: it.Hook})
	}
	return out
}

// cmdList prints every item in the vault, kind then name order, one
// line each: "<kind>/<name> — <hook>".
func cmdList(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	items, err := vault.AllItems(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	vault.SortItems(items)
	if m == modeJSON {
		printJSON(out, itemsResult{Items: toJSONItems(items)})
		return 0
	}
	printItems(out, items)
	return 0
}

// printItems writes one line per item: "<kind>/<name> — <hook>". A
// blank hook becomes "(no description)". Both "loadout list" and
// "loadout recall" use this, so their output lines match exactly.
func printItems(out io.Writer, items []vault.Item) {
	for _, it := range items {
		fmt.Fprintf(out, "%s — %s\n", it.Address(), vault.HookOrDefault(it.Hook))
	}
}
