package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// logCount is the number of history entries "loadout log" prints.
const logCount = 20

// logEntry is one entry in the JSON shape of "loadout log".
type logEntry struct {
	At      string `json:"at"`
	Subject string `json:"subject"`
}

// logResult is the JSON shape of "loadout log".
type logResult struct {
	Entries []logEntry `json:"entries"`
}

// cmdLog prints the last logCount vault history entries, one per
// line, newest first: "<date>  <subject>".
func cmdLog(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	entries, err := vault.History(v, logCount)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		jsonEntries := make([]logEntry, 0, len(entries))
		for _, e := range entries {
			jsonEntries = append(jsonEntries, logEntry{At: e.At, Subject: e.Subject})
		}
		printJSON(out, logResult{Entries: jsonEntries})
		return 0
	}
	for _, e := range entries {
		fmt.Fprintf(out, "%s  %s\n", e.At, e.Subject)
	}
	return 0
}
