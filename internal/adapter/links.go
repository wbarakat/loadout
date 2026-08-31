package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// canonicalPath resolves the symlinks in path, so two different
// spellings of the same location compare equal — for example /tmp
// and /private/tmp on macOS. If path does not exist (a dangling link
// target), it walks up to the deepest existing ancestor, resolves
// that, and rejoins the missing remainder — so a dangling target
// under a symlink-indirected directory still canonicalizes. If no
// ancestor resolves, it falls back to a cleaned version of the raw
// value.
func canonicalPath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved
	}
	dir := filepath.Dir(clean)
	remainder := filepath.Base(clean)
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, remainder)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return clean
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
		dir = parent
	}
}

// isVaultOwned reports whether target is the vault skills directory,
// or lies inside it. Loadout only ever creates a link with a target
// like this; any other target belongs to the user. vaultSkillsDir
// must already be canonical (see canonicalPath).
func isVaultOwned(target, vaultSkillsDir string) bool {
	target = canonicalPath(target)
	return target == vaultSkillsDir || strings.HasPrefix(target, vaultSkillsDir+string(filepath.Separator))
}

// blockedLinkMsg names the skill and the path that blocks its link,
// with the fix the user must run by hand.
func blockedLinkMsg(name, path string) string {
	return fmt.Sprintf("skill/%s: a real file or a foreign link occupies %s. Fix: move or remove %s.", name, path, path)
}

// LinkSkills creates one symlink per skill in dir, pointing into the
// vault skills directory. It repairs a Loadout-owned link that has
// the wrong target. It never replaces a real file, a real directory,
// or a symlink that points outside the vault skills directory — it
// reports those names as blocked, not as an error. After linking, it
// removes every Loadout-owned link in dir that no longer matches a
// listed skill.
//
// If dry is true, LinkSkills changes nothing on disk. It still walks
// the same decisions and returns the same applied, pruned, and
// blocked lists it would return for a real run.
func LinkSkills(skills []vault.Skill, vaultSkillsDir, dir string, dry bool) (applied, pruned, blocked []string, err error) {
	if !dry {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, nil, err
		}
	}
	vaultSkillsDir = canonicalPath(vaultSkillsDir)
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
				blocked = append(blocked, blockedLinkMsg(s.Name, linkPath))
				continue
			}
			if canonicalPath(cur) == canonicalPath(s.Dir) {
				continue
			}
			if !dry {
				if err := os.Remove(linkPath); err != nil {
					return applied, pruned, blocked, err
				}
			}
		case statErr == nil:
			// A real file or directory: it is the user's. Leave it.
			blocked = append(blocked, blockedLinkMsg(s.Name, linkPath))
			continue
		}
		if !dry {
			if err := os.Symlink(s.Dir, linkPath); err != nil {
				return applied, pruned, blocked, err
			}
		}
		applied = append(applied, fmt.Sprintf("skill/%s: linked", s.Name))
	}
	removed, err := pruneLinks(dir, vaultSkillsDir, want, dry)
	if err != nil {
		return applied, pruned, blocked, err
	}
	for _, name := range removed {
		pruned = append(pruned, fmt.Sprintf("skill/%s: stale link removed", name))
	}
	return applied, pruned, blocked, nil
}

// pruneLinks removes every Loadout-owned symlink in dir that does not
// match a wanted skill name with the correct target, and returns the
// name of each skill it removed. It never removes a real file, a
// real directory, or a symlink that points outside the vault skills
// directory. If dry is true, it removes nothing and only reports what
// it would remove.
func pruneLinks(dir, vaultSkillsDir string, want map[string]string, dry bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if dry && os.IsNotExist(err) {
			// A dry run against a directory not yet created has
			// nothing to prune.
			return nil, nil
		}
		return nil, err
	}
	var pruned []string
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
		if target, ok := want[e.Name()]; ok && canonicalPath(target) == canonicalPath(cur) {
			continue
		}
		if !dry {
			if err := os.Remove(path); err != nil {
				return pruned, err
			}
		}
		pruned = append(pruned, e.Name())
	}
	return pruned, nil
}

// checkLinks reports one problem for each skill that is not correctly
// linked in dir. A missing link, or a Loadout-owned link with the
// wrong target, is fixed by a sync. A path held by a real file, a
// real directory, or a foreign symlink is fixed by the user moving or
// removing it.
func checkLinks(name string, skills []vault.Skill, vaultSkillsDir, dir string) []Problem {
	var ps []Problem
	vaultSkillsDir = canonicalPath(vaultSkillsDir)
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
			} else if canonicalPath(cur) != canonicalPath(s.Dir) {
				ps = append(ps, Problem{name, "the skill " + s.Name + " is not linked", "run: loadout sync"})
			}
		default:
			ps = append(ps, Problem{name, "a real file or a foreign link occupies " + path, "move or remove " + path})
		}
	}
	return ps
}
