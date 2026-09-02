package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"loadout.dev/loadout/internal/adapter"
)

// StripLoadoutBlock removes Loadout's own managed block from content
// and returns what a native, non-Loadout file would hold. damaged is
// true when loadout marks are present but malformed — two begins, an
// orphan end — so the caller can skip the file and warn instead of
// guessing at a partial strip. It delegates to the adapter package's
// own marker parsing, so an importer never hardcodes the loadout mark
// text a second time.
func StripLoadoutBlock(content string) (native string, damaged bool) {
	return adapter.StripManagedBlock(content)
}

// IsVaultOwnedSkill reports whether entryPath is a skill Loadout
// itself projected: a symlink whose real target resolves inside the
// vault's own skills directory, vaultSkillsDir. It resolves both
// sides with filepath.EvalSymlinks — never a raw string prefix check
// on the paths as given, since a path component itself can be a
// symlink (macOS aliases /tmp to /private/tmp, so a naive prefix
// check under-matches). A path that is not a symlink is never
// vault-owned (Loadout only ever projects skills as symlinks), and
// reports false with no error. A dangling symlink is an error; the
// caller must turn it into a skip and a warning rather than guess.
func IsVaultOwnedSkill(entryPath, vaultSkillsDir string) (bool, error) {
	info, err := os.Lstat(entryPath)
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	target, err := filepath.EvalSymlinks(entryPath)
	if err != nil {
		return false, fmt.Errorf("the skill link %s does not resolve. Fix: remove the dangling link, or point it at a real skill.", entryPath)
	}
	resolvedVaultDir, err := filepath.EvalSymlinks(vaultSkillsDir)
	if err != nil {
		return false, err
	}
	return isWithinDir(resolvedVaultDir, target), nil
}

// isWithinDir reports whether target is dir itself, or a path under
// dir. Both dir and target must already be resolved (no symlink
// components left).
func isWithinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
