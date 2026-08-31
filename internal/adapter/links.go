package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// isVaultOwned reports whether target is the vault skills directory,
// or lies inside it. Loadout only ever creates a link with a target
// like this; any other target belongs to the user.
func isVaultOwned(target, vaultSkillsDir string) bool {
	target = filepath.Clean(target)
	vaultSkillsDir = filepath.Clean(vaultSkillsDir)
	return target == vaultSkillsDir || strings.HasPrefix(target, vaultSkillsDir+string(filepath.Separator))
}

// LinkSkills creates one symlink per skill in dir, pointing into the
// vault skills directory. It repairs a Loadout-owned link that has
// the wrong target. It never replaces a real file, a real directory,
// or a symlink that points outside the vault skills directory — it
// reports those names as blocked. After linking, it removes every
// Loadout-owned link in dir that no longer matches a listed skill.
func LinkSkills(skills []vault.Skill, vaultSkillsDir, dir string) (blocked []string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	want := make(map[string]string, len(skills))
	for _, s := range skills {
		want[s.Name] = s.Dir
	}
	for _, s := range skills {
		linkPath := filepath.Join(dir, s.Name)
		fi, statErr := os.Lstat(linkPath)
		switch {
		case statErr == nil && fi.Mode()&os.ModeSymlink != 0:
			cur, readErr := os.Readlink(linkPath)
			if readErr != nil || !isVaultOwned(cur, vaultSkillsDir) {
				// A foreign link: it is the user's. Leave it.
				blocked = append(blocked, s.Name)
				continue
			}
			if cur == s.Dir {
				continue
			}
			if err := os.Remove(linkPath); err != nil {
				return blocked, err
			}
		case statErr == nil:
			// A real file or directory: it is the user's. Leave it.
			blocked = append(blocked, s.Name)
			continue
		}
		if err := os.Symlink(s.Dir, linkPath); err != nil {
			return blocked, err
		}
	}
	if err := pruneLinks(dir, vaultSkillsDir, want); err != nil {
		return blocked, err
	}
	return blocked, nil
}

// pruneLinks removes every Loadout-owned symlink in dir that does not
// match a wanted skill name with the correct target. It never removes
// a real file, a real directory, or a symlink that points outside the
// vault skills directory.
func pruneLinks(dir, vaultSkillsDir string, want map[string]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(dir, e.Name())
		cur, err := os.Readlink(path)
		if err != nil || !isVaultOwned(cur, vaultSkillsDir) {
			continue
		}
		if target, ok := want[e.Name()]; ok && target == cur {
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

// blockedSkillsError builds one error naming every blocked skill and
// the path that blocks it.
func blockedSkillsError(names []string, dir string) error {
	msgs := make([]string, len(names))
	for i, name := range names {
		path := filepath.Join(dir, name)
		msgs[i] = fmt.Sprintf("the skill %s is blocked: a real file or a foreign link occupies %s; move or remove it", name, path)
	}
	return errors.New(strings.Join(msgs, "\n"))
}

// checkLinks reports one problem for each skill that is not correctly
// linked in dir. A missing link, or a Loadout-owned link with the
// wrong target, is fixed by a sync. A path held by a real file, a
// real directory, or a foreign symlink is fixed by the user moving or
// removing it.
func checkLinks(name string, skills []vault.Skill, vaultSkillsDir, dir string) []Problem {
	var ps []Problem
	for _, s := range skills {
		path := filepath.Join(dir, s.Name)
		fi, statErr := os.Lstat(path)
		switch {
		case statErr != nil:
			ps = append(ps, Problem{name, "the skill " + s.Name + " is not linked", "run: loadout sync"})
		case fi.Mode()&os.ModeSymlink != 0:
			cur, readErr := os.Readlink(path)
			if readErr != nil || !isVaultOwned(cur, vaultSkillsDir) {
				ps = append(ps, Problem{name, "a real file or a foreign link occupies " + path, "move or remove " + path})
			} else if cur != s.Dir {
				ps = append(ps, Problem{name, "the skill " + s.Name + " is not linked", "run: loadout sync"})
			}
		default:
			ps = append(ps, Problem{name, "a real file or a foreign link occupies " + path, "move or remove " + path})
		}
	}
	return ps
}
