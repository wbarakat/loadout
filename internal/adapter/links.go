package adapter

import (
	"bytes"
	"fmt"
	"io/fs"
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

// adoptedLinkMsg names a skill whose foreign link Loadout replaced
// with its own link.
func adoptedLinkMsg(name string) string {
	return fmt.Sprintf("skill/%s: adopted a foreign link", name)
}

// adoptedFolderMsg names a skill whose real source folder Loadout
// replaced with a link into the vault, after proving the vault already
// holds every byte of that folder.
func adoptedFolderMsg(name string) string {
	return fmt.Sprintf("skill/%s: adopted the source folder", name)
}

// skillBody returns raw with a leading frontmatter block removed, and
// the rest trimmed. Loadout rewrites a skill's frontmatter when it
// imports one: it keeps the original keys and adds by:, at:, and
// review:. Two copies of the same skill therefore differ in their
// frontmatter while their bodies stay byte-identical, so the body is
// what tells us whether the vault already holds this file's content.
func skillBody(raw []byte) []byte {
	const fence = "---\n"
	text := strings.TrimPrefix(string(raw), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, fence) {
		return []byte(strings.TrimSpace(text))
	}
	rest := text[len(fence):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return []byte(strings.TrimSpace(text))
	}
	return []byte(strings.TrimSpace(strings.TrimPrefix(rest[end+len("\n---"):], "\n")))
}

// vaultCapturesFolder reports whether the vault's own copy of a skill
// at vaultDir already holds every byte of the real folder at srcDir.
//
// This is the safety proof for replacing a real source folder with a
// link into the vault. A true answer means removing srcDir loses
// nothing, because every one of its files is already in the vault.
// Every regular file under srcDir must exist at the same relative path
// under vaultDir with identical bytes. SKILL.md is the one exception:
// it is compared by body, since Loadout rewrites frontmatter on import.
//
// srcDir must itself hold a SKILL.md whose body matches the vault's.
// That match is what identifies srcDir as the source the vault's copy
// came from, rather than some unrelated directory that merely shares a
// name. Without this requirement an EMPTY directory would qualify
// vacuously, since a walk of it finds no file that fails to match.
//
// It answers false, never an error, for anything it cannot prove: a
// file the vault does not hold, a file whose bytes differ, an
// unreadable file, or an entry that is not a regular file (a nested
// symlink, for example). An import deliberately leaves some things
// behind, such as a .git directory, a node_modules tree, or an
// oversized file, and every one of those makes this false. A false
// answer keeps the old, safe behavior: leave the user's folder alone.
func vaultCapturesFolder(srcDir, vaultDir string) bool {
	captured := true
	sawSkillMD := false
	stop := func() error {
		captured = false
		return filepath.SkipAll
	}
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return stop()
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			// Not something an import copies byte for byte, so it
			// cannot be shown to be safe to delete.
			return stop()
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return stop()
		}
		srcData, readErr := os.ReadFile(path)
		if readErr != nil {
			return stop()
		}
		vaultData, readErr := os.ReadFile(filepath.Join(vaultDir, rel))
		if readErr != nil {
			return stop()
		}
		if rel == "SKILL.md" {
			if !bytes.Equal(skillBody(srcData), skillBody(vaultData)) {
				return stop()
			}
			sawSkillMD = true
			return nil
		}
		if !bytes.Equal(srcData, vaultData) {
			return stop()
		}
		return nil
	})
	if err != nil {
		return false
	}
	return captured && sawSkillMD
}

// atomicSymlink creates a symlink to oldname at newname, replacing
// whatever symlink currently sits at newname, without ever leaving
// newname missing. It builds the new link under a temporary name in
// newname's own directory, then renames it over newname; a rename
// within one directory is atomic on every platform Loadout supports.
//
// Call atomicSymlink only when newname is already known (by a prior
// Lstat) to be a symlink, not a real file or a real directory —
// atomicSymlink itself does not check, and a rename over a real file
// or directory would destroy it.
func atomicSymlink(oldname, newname string) error {
	tmp, err := os.CreateTemp(filepath.Dir(newname), ".loadout-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	if err := os.Symlink(oldname, tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, newname); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// LinkSkills creates one symlink per skill in dir, pointing into the
// vault skills directory. It repairs a Loadout-owned link that has
// the wrong target. When a symlink already occupies the path but
// points outside the vault skills directory — a foreign link, made
// by the user or another tool before Loadout ever ran — LinkSkills
// adopts it: it replaces the foreign link with its own, since the
// vault owns a skill with this exact name.
//
// It also adopts a real DIRECTORY, but only when vaultCapturesFolder
// proves the vault already holds every byte of it — the normal case
// right after an import, since the vault's copy came from that very
// folder. Without this, the tool a skill was imported from is the one
// tool that never receives it back. Any other real file or directory
// is the user's: it is reported as blocked, not replaced, and never as
// an error. After linking, it removes every Loadout-owned link in dir
// that no longer matches a listed skill.
//
// If dry is true, LinkSkills changes nothing on disk. It still walks
// the same decisions and returns the same applied, adopted, pruned,
// and blocked lists it would return for a real run.
func LinkSkills(skills []vault.Skill, vaultSkillsDir, dir string, dry bool) (applied, adopted, pruned, blocked []string, err error) {
	if !dry {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, nil, nil, err
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
			switch {
			case readErr != nil:
				// The link cannot be read: leave it, it is the
				// user's.
				blocked = append(blocked, blockedLinkMsg(s.Name, linkPath))
				continue
			case !isVaultOwned(cur, vaultSkillsDir):
				// A foreign link, but the vault owns a skill with
				// this exact name: adopt the link rather than
				// refuse it.
				if !dry {
					if err := atomicSymlink(s.Dir, linkPath); err != nil {
						return applied, adopted, pruned, blocked, err
					}
				}
				adopted = append(adopted, adoptedLinkMsg(s.Name))
				continue
			case canonicalPath(cur) == canonicalPath(s.Dir):
				continue
			default:
				if !dry {
					if err := os.Remove(linkPath); err != nil {
						return applied, adopted, pruned, blocked, err
					}
				}
			}
		case statErr == nil:
			// A real file or directory sits where the link belongs.
			// When it is a directory whose every byte the vault already
			// holds, replace it with the link: nothing is lost. This is
			// the normal case right after an import, since the vault's
			// copy came from this very folder — without it, the tool a
			// skill was imported FROM is the one tool that never
			// receives it back. Anything the vault does not fully hold
			// stays the user's, and is left alone.
			if fi.IsDir() && vaultCapturesFolder(linkPath, s.Dir) {
				if !dry {
					if err := os.RemoveAll(linkPath); err != nil {
						return applied, adopted, pruned, blocked, err
					}
					if err := os.Symlink(s.Dir, linkPath); err != nil {
						return applied, adopted, pruned, blocked, err
					}
				}
				adopted = append(adopted, adoptedFolderMsg(s.Name))
				continue
			}
			blocked = append(blocked, blockedLinkMsg(s.Name, linkPath))
			continue
		}
		if !dry {
			if err := os.Symlink(s.Dir, linkPath); err != nil {
				return applied, adopted, pruned, blocked, err
			}
		}
		applied = append(applied, fmt.Sprintf("skill/%s: linked", s.Name))
	}
	removed, err := pruneLinks(dir, vaultSkillsDir, want, dry)
	if err != nil {
		return applied, adopted, pruned, blocked, err
	}
	for _, name := range removed {
		pruned = append(pruned, fmt.Sprintf("skill/%s: stale link removed", name))
	}
	return applied, adopted, pruned, blocked, nil
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
			ps = append(ps, Problem{name, "the skill " + s.Name + " is not linked", FixBySync})
		case fi.Mode()&os.ModeSymlink != 0:
			cur, readErr := os.Readlink(path)
			switch {
			case readErr != nil:
				ps = append(ps, Problem{name, "a real file or a foreign link occupies " + path, "move or remove " + path})
			case !isVaultOwned(cur, vaultSkillsDir):
				// A foreign link, but the vault owns a skill of this
				// name: a sync adopts it, so this is pending work, not
				// something the user has to clear by hand.
				ps = append(ps, Problem{name, "the skill " + s.Name + " is not linked", FixBySync})
			case canonicalPath(cur) != canonicalPath(s.Dir):
				ps = append(ps, Problem{name, "the skill " + s.Name + " is not linked", FixBySync})
			}
		case fi.IsDir() && vaultCapturesFolder(path, s.Dir):
			// The vault already holds every byte of this folder, so a
			// sync replaces it with the link. Pending work, not a
			// conflict for the user to resolve.
			ps = append(ps, Problem{name, "the skill " + s.Name + " is not linked", FixBySync})
		default:
			ps = append(ps, Problem{name, "a real file or a foreign link occupies " + path, "move or remove " + path})
		}
	}
	return ps
}
