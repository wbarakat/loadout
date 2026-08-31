package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/vault"
)

const addUsage = "usage: loadout add skill|memory <name> [--by <who>]"

func cmdAdd(out, errOut io.Writer, args []string) int {
	kind, name, by, ok := parseAddArgs(args)
	if !ok {
		fmt.Fprintln(errOut, addUsage)
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
		removeScaffold(kind, path)
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "created %s\n", path)
	return 0
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

// removeScaffold undoes AddSkill or AddFact after a failed snapshot,
// so a retry starts from a clean vault. For a skill, path is the
// SKILL.md file; remove its whole directory. For a fact, path is the
// fact file itself.
func removeScaffold(kind, path string) {
	if kind == "skill" {
		os.RemoveAll(filepath.Dir(path))
		return
	}
	os.Remove(path)
}
