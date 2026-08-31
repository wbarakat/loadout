package adapter

import (
	"fmt"
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/vault"
)

// orphanLinks scans dir for a Loadout-owned symlink that no skill in
// skills explains — for example, a link left behind after its skill
// was deleted from the vault. A sync prunes these; between syncs,
// doctor must still report them. It never flags a foreign symlink or
// a real file: those belong to the user. A dir that does not exist
// yet is not an error; it holds nothing to flag.
func orphanLinks(skills []vault.Skill, vaultSkillsDir, dir string) []Problem {
	vaultSkillsDir = canonicalPath(vaultSkillsDir)
	want := make(map[string]bool, len(skills))
	for _, s := range skills {
		want[s.Name] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ps []Problem
	for _, e := range entries {
		if want[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(dir, e.Name())
		cur, err := os.Readlink(path)
		if err != nil || !isVaultOwned(cur, vaultSkillsDir) {
			continue
		}
		ps = append(ps, Problem{Detail: fmt.Sprintf("stale link %s", path), Fix: "run: loadout sync"})
	}
	return ps
}
