package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// recentHistoryCount is how many history subjects the "recent"
// section of "loadout context" shows.
const recentHistoryCount = 3

// nextLine is the final line of "loadout context": a pointer to the
// two commands that dig into what context shows.
const nextLine = "next: loadout show <kind/name> reads one item; loadout recall <terms> searches."

// cmdContext prints the compact picture of the vault: the counts,
// every fact hook, every skill hook, and the last few history
// subjects.
func cmdContext(out, errOut io.Writer) int {
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
	subjects, err := vault.RecentSubjects(v, recentHistoryCount)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	fmt.Fprintf(out, "vault: %s (%d skills, %d facts)\n", v.Root, len(skills), len(facts))

	fmt.Fprintln(out, "memory:")
	for _, f := range facts {
		fmt.Fprintf(out, "  %s\n", hookLine(f.Name, f.Description))
	}

	fmt.Fprintln(out, "skills:")
	for _, s := range skills {
		fmt.Fprintf(out, "  %s\n", hookLine(s.Name, s.Description))
	}

	fmt.Fprintln(out, "recent:")
	for _, subject := range subjects {
		fmt.Fprintf(out, "  %s\n", subject)
	}

	fmt.Fprintln(out, nextLine)
	return 0
}

// hookLine renders "<name> — <hook>", with the same "(no
// description)" placeholder "loadout list" uses for a blank hook.
func hookLine(name, hook string) string {
	if hook == "" {
		hook = "(no description)"
	}
	return name + " — " + hook
}
