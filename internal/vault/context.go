package vault

import (
	"fmt"
	"strings"
)

// contextRecentCount is how many history subjects RenderContext's
// "recent" section shows.
const contextRecentCount = 3

// contextNextLine is RenderContext's final line: a pointer to the
// two operations that dig into what context shows.
const contextNextLine = "next: loadout show <kind/name> reads one item; loadout recall <terms> searches."

// RenderContext builds the compact situational picture of the vault:
// its counts, every fact hook, every skill hook, and the last few
// history subjects. "loadout context" (text mode) and the MCP
// "context" tool both use this.
func RenderContext(v *Vault) (string, error) {
	skills, err := ListSkills(v)
	if err != nil {
		return "", err
	}
	facts, err := ListFacts(v)
	if err != nil {
		return "", err
	}
	subjects, err := RecentSubjects(v, contextRecentCount)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "vault: %s (%d skills, %d facts)\n", v.Root, len(skills), len(facts))

	b.WriteString("memory:\n")
	for _, f := range facts {
		fmt.Fprintf(&b, "  %s\n", HookLine(f.Name, f.Description))
	}

	b.WriteString("skills:\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "  %s\n", HookLine(s.Name, s.Description))
	}

	b.WriteString("recent:\n")
	for _, subject := range subjects {
		fmt.Fprintf(&b, "  %s\n", subject)
	}

	b.WriteString(contextNextLine + "\n")
	return b.String(), nil
}
