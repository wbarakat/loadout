package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

const addUsage = "usage: loadout add skill|memory <name> [--by <who>]"

func cmdAdd(out, errOut io.Writer, args []string) int {
	kind, name, by, ok := parseAddArgs(args)
	if !ok {
		fmt.Fprintln(errOut, addUsage)
		return 2
	}
	if err := validateBy(by); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
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
	var path string
	if kind == "skill" {
		path, err = vault.AddSkill(v, name, by)
	} else {
		path, err = vault.AddFact(v, name, by)
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := vault.Snapshot(v, "add "+kind+" "+name); err != nil {
		removeItemFile(kind, path)
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "created %s\n", path)
	return 0
}

// maxByLength caps a --by value, so a malformed value cannot bloat
// the vault frontmatter.
const maxByLength = 64

// validateBy rejects a --by value that could corrupt the frontmatter
// line it lands in: one with a newline or carriage return, one with
// leading or trailing whitespace, or one over maxByLength characters.
func validateBy(by string) error {
	if strings.ContainsAny(by, "\n\r") {
		return fmt.Errorf("%q: not a valid --by value. Fix: remove the newline or carriage return.", by)
	}
	if strings.TrimSpace(by) != by {
		return fmt.Errorf("%q: not a valid --by value. Fix: remove the leading or trailing whitespace.", by)
	}
	if len(by) > maxByLength {
		return fmt.Errorf("%q: not a valid --by value. Fix: use %d characters or fewer.", by, maxByLength)
	}
	return nil
}

// parseAddArgs reads "skill|memory <name> [--by <who>]" from args. by
// defaults to "human" when the flag is absent. ok is false when args
// does not match this shape, so the caller can print usage.
func parseAddArgs(args []string) (kind, name, by string, ok bool) {
	if len(args) < 2 {
		return "", "", "", false
	}
	kind, name = args[0], args[1]
	if kind != "skill" && kind != "memory" {
		return "", "", "", false
	}
	rest := args[2:]
	switch len(rest) {
	case 0:
		return kind, name, "human", true
	case 2:
		if rest[0] != "--by" || rest[1] == "" {
			return "", "", "", false
		}
		return kind, name, rest[1], true
	default:
		return "", "", "", false
	}
}

// removeItemFile deletes an item's file from disk. For a skill, path
// is the SKILL.md file; remove its whole directory. For a fact, path
// is the fact file itself. cmdAdd calls it to undo a scaffold after a
// failed snapshot; "loadout review drop" calls it to delete an item
// for good.
func removeItemFile(kind, path string) error {
	if kind == "skill" {
		return os.RemoveAll(filepath.Dir(path))
	}
	return os.Remove(path)
}
