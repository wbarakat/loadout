package adapter_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestLinkSkills(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := filepath.Join(t.TempDir(), "tool", "skills")
	skillDir := filepath.Join(vaultSkillsDir, "deploy-checks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := []vault.Skill{{Name: "deploy-checks", Dir: skillDir}}

	applied, adopted, pruned, blocked, err := adapter.LinkSkills(skills, vaultSkillsDir, dst, false)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("err=%v blocked=%v", err, blocked)
	}
	if len(applied) != 1 || applied[0] != "skill/deploy-checks: linked" {
		t.Fatalf("applied must report the new link, got %v", applied)
	}
	if len(pruned) != 0 {
		t.Fatalf("nothing to prune yet, got %v", pruned)
	}
	if len(adopted) != 0 {
		t.Fatalf("a brand-new link is not an adoption, got %v", adopted)
	}
	got, err := os.Readlink(filepath.Join(dst, "deploy-checks"))
	if err != nil || got != skillDir {
		t.Fatalf("bad link: %q err=%v", got, err)
	}
	// A second run must not fail (idempotent), and must report nothing
	// new to apply since the link is already correct.
	applied, adopted, _, _, err = adapter.LinkSkills(skills, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("an already-correct link must not be reported again, got %v", applied)
	}
	if len(adopted) != 0 {
		t.Fatalf("an already-correct link must not be reported as adopted, got %v", adopted)
	}
}

func TestLinkSkillsRepairsWrongLink(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(vaultSkillsDir, "a")
	os.MkdirAll(skillDir, 0o755)
	// A stale Loadout-owned link: it points inside the vault skills
	// directory, but at the wrong skill.
	oldDir := filepath.Join(vaultSkillsDir, "old-a")
	os.MkdirAll(oldDir, 0o755)
	os.Symlink(oldDir, filepath.Join(dst, "a"))
	if _, _, _, _, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, vaultSkillsDir, dst, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.Readlink(filepath.Join(dst, "a"))
	if got != skillDir {
		t.Fatalf("link was not repaired: %q", got)
	}
}

func TestLinkSkillsRefusesRealDir(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(vaultSkillsDir, "a")
	os.MkdirAll(skillDir, 0o755)
	blockedPath := filepath.Join(dst, "a")
	os.MkdirAll(blockedPath, 0o755) // a real dir owned by the user
	_, adopted, _, blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("skill/a: a real file or a foreign link occupies %s. Fix: move or remove %s.", blockedPath, blockedPath)
	if len(blocked) != 1 || blocked[0] != want {
		t.Fatalf("blocked=%v want=%q", blocked, want)
	}
	if len(adopted) != 0 {
		t.Fatalf("a real directory must never be adopted, got %v", adopted)
	}
	fi, err := os.Lstat(blockedPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("must not replace a real directory")
	}
	if !fi.IsDir() {
		t.Fatal("the real directory must survive untouched")
	}
}

func TestLinkSkillsRefusesRealFile(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(vaultSkillsDir, "a")
	os.MkdirAll(skillDir, 0o755)
	blockedPath := filepath.Join(dst, "a")
	content := []byte("the user's own file, not Loadout's")
	if err := os.WriteFile(blockedPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	_, adopted, _, blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("skill/a: a real file or a foreign link occupies %s. Fix: move or remove %s.", blockedPath, blockedPath)
	if len(blocked) != 1 || blocked[0] != want {
		t.Fatalf("blocked=%v want=%q", blocked, want)
	}
	if len(adopted) != 0 {
		t.Fatalf("a real file must never be adopted, got %v", adopted)
	}
	fi, err := os.Lstat(blockedPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("must not replace a real file with a link")
	}
	got, err := os.ReadFile(blockedPath)
	if err != nil || string(got) != string(content) {
		t.Fatalf("the real file's content must survive unchanged: got %q err=%v", got, err)
	}
}

func TestLinkSkillsAdoptsForeignSymlink(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(vaultSkillsDir, "archify")
	os.MkdirAll(skillDir, 0o755)
	// A symlink the user (or another tool) made before Loadout ever
	// ran, pointing at some other skills store entirely.
	foreignStore := filepath.Join(t.TempDir(), "agents-skills")
	foreignTarget := filepath.Join(foreignStore, "archify")
	os.MkdirAll(foreignTarget, 0o755)
	linkPath := filepath.Join(dst, "archify")
	if err := os.Symlink(foreignTarget, linkPath); err != nil {
		t.Fatal(err)
	}

	applied, adopted, _, blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "archify", Dir: skillDir}}, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 0 {
		t.Fatalf("the vault owns this skill name, so it must adopt rather than block, got blocked=%v", blocked)
	}
	if len(applied) != 0 {
		t.Fatalf("an adoption is not a plain apply, got applied=%v", applied)
	}
	want := "skill/archify: adopted a foreign link"
	if len(adopted) != 1 || adopted[0] != want {
		t.Fatalf("adopted=%v want=%q", adopted, want)
	}
	got, err := os.Readlink(linkPath)
	if err != nil || got != skillDir {
		t.Fatalf("the link must now point into the vault: got %q err=%v", got, err)
	}
	resolved, err := filepath.EvalSymlinks(linkPath)
	if err != nil || resolved != mustEvalSymlinks(t, skillDir) {
		t.Fatalf("the link must resolve into the vault skills dir: got %q err=%v", resolved, err)
	}

	// Adoption is idempotent: a second run must not re-report it, and
	// the link stays exactly where the first run put it.
	_, adopted, _, _, err = adapter.LinkSkills([]vault.Skill{{Name: "archify", Dir: skillDir}}, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 0 {
		t.Fatalf("an already-adopted link must not be reported again, got %v", adopted)
	}
}

func TestLinkSkillsDryRunAdoptsNothingOnDisk(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(vaultSkillsDir, "archify")
	os.MkdirAll(skillDir, 0o755)
	foreignTarget := filepath.Join(t.TempDir(), "archify")
	os.MkdirAll(foreignTarget, 0o755)
	linkPath := filepath.Join(dst, "archify")
	if err := os.Symlink(foreignTarget, linkPath); err != nil {
		t.Fatal(err)
	}

	_, adopted, _, blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "archify", Dir: skillDir}}, vaultSkillsDir, dst, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 0 {
		t.Fatalf("blocked=%v", blocked)
	}
	want := "skill/archify: adopted a foreign link"
	if len(adopted) != 1 || adopted[0] != want {
		t.Fatalf("a dry run must still report the pending adoption, got %v", adopted)
	}
	got, err := os.Readlink(linkPath)
	if err != nil || got != foreignTarget {
		t.Fatalf("a dry run must change nothing on disk: got %q err=%v", got, err)
	}
}

func TestLinkSkillsLeavesForeignSymlinkForUnownedName(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	aDir := filepath.Join(vaultSkillsDir, "a")
	os.MkdirAll(aDir, 0o755)
	// A foreign symlink under a name the vault does not own at all.
	foreignTarget := filepath.Join(t.TempDir(), "user-owned")
	os.MkdirAll(foreignTarget, 0o755)
	unownedPath := filepath.Join(dst, "unowned-skill")
	if err := os.Symlink(foreignTarget, unownedPath); err != nil {
		t.Fatal(err)
	}

	_, adopted, pruned, blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: aDir}}, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 0 {
		t.Fatalf("only a vault-owned name may be adopted, got %v", adopted)
	}
	if len(blocked) != 0 {
		t.Fatalf("a name outside the sync set is not this adapter's business, got %v", blocked)
	}
	if len(pruned) != 0 {
		t.Fatalf("a foreign link must never be pruned, got %v", pruned)
	}
	got, err := os.Readlink(unownedPath)
	if err != nil || got != foreignTarget {
		t.Fatalf("the unrelated foreign symlink must survive untouched: got %q err=%v", got, err)
	}
}

func TestLinkSkillsPrunesStaleLinks(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	aDir := filepath.Join(vaultSkillsDir, "a")
	bDir := filepath.Join(vaultSkillsDir, "b")
	os.MkdirAll(aDir, 0o755)
	os.MkdirAll(bDir, 0o755)
	skills := []vault.Skill{{Name: "a", Dir: aDir}, {Name: "b", Dir: bDir}}
	if _, _, _, _, err := adapter.LinkSkills(skills, vaultSkillsDir, dst, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(dst, "b")); err != nil {
		t.Fatal("b must be linked before the prune step")
	}

	// Sync again with only "a" listed: the "b" link must go away.
	_, _, pruned, _, err := adapter.LinkSkills(skills[:1], vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != "skill/b: stale link removed" {
		t.Fatalf("pruned must report the removed skill, got %v", pruned)
	}
	if _, err := os.Lstat(filepath.Join(dst, "b")); !os.IsNotExist(err) {
		t.Fatalf("the stale link b must be pruned, err=%v", err)
	}
	if _, err := os.Readlink(filepath.Join(dst, "a")); err != nil {
		t.Fatal("a must stay linked")
	}
}

// mustEvalSymlinks resolves path or fails the test. It exists so
// TestLinkSkillsAdoptsForeignSymlink can compare against the vault
// skill dir's own canonical spelling, the same way LinkSkills does
// internally, rather than a raw string.
func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
