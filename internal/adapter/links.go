package adapter

import (
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/vault"
)

// LinkSkills creates one symlink per skill in dir. It repairs a wrong
// link. It never replaces a real file or directory; it returns those
// names as blocked.
func LinkSkills(skills []vault.Skill, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var blocked []string
	for _, s := range skills {
		linkPath := filepath.Join(dir, s.Name)
		fi, err := os.Lstat(linkPath)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			if cur, _ := os.Readlink(linkPath); cur == s.Dir {
				continue
			}
			if err := os.Remove(linkPath); err != nil {
				return blocked, err
			}
		case err == nil:
			blocked = append(blocked, s.Name)
			continue
		}
		if err := os.Symlink(s.Dir, linkPath); err != nil {
			return blocked, err
		}
	}
	return blocked, nil
}
