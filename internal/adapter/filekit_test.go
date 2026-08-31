package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

// fileKitTestVault builds a vault with one skill and one fact, for
// tests that exercise the fileAdapter kit directly.
func fileKitTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(v.SkillsDir(), "deploy-checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: deploy-checks\ndescription: run checks\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\ntype: user\n---\nI use Go.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestFileAdapterMemoryNoneSkipsMemory proves memoryNone skips memory
// entirely: it writes no managed block and no render file, even when
// MemoryFile is set, and it still links skills. Check still surfaces
// the set-but-ignored MemoryFile as a Problem, so the user learns
// loadout never acts on it.
func TestFileAdapterMemoryNoneSkipsMemory(t *testing.T) {
	v := fileKitTestVault(t)
	home := t.TempDir()
	memoryFile := filepath.Join(home, "MEMORY.md")
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, "skills"),
		MemoryFile: memoryFile, // memoryNone must ignore this
	}
	a := newFileAdapter("skills-only", cfg, memoryNone)

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(cfg.SkillsDir, "deploy-checks")); err != nil {
		t.Fatal("skill link is missing")
	}
	if _, err := os.Stat(memoryFile); !os.IsNotExist(err) {
		t.Fatal("memoryNone must not write a memory block")
	}
	if _, err := os.Stat(filepath.Join(v.RenderDir(), "memory.md")); !os.IsNotExist(err) {
		t.Fatal("memoryNone must not write a render file")
	}
	ps := a.Check(v)
	if len(ps) != 1 {
		t.Fatalf("Check must report the ignored MemoryFile and nothing else, got %+v", ps)
	}
	want := Problem{
		Adapter: "skills-only",
		Detail:  "the adapter skills-only takes no memory_file; loadout ignores it.",
		Fix:     "remove adapters.skills-only.memory_file, or use the agents-md adapter for extra instruction files.",
	}
	if ps[0] != want {
		t.Fatalf("Check = %+v, want %+v", ps[0], want)
	}
}

// TestFileAdapterEmptyMemoryFileIsConfigError proves an empty
// MemoryFile is a config error, not a write attempt, for both memory
// modes and from both Apply and Check.
func TestFileAdapterEmptyMemoryFileIsConfigError(t *testing.T) {
	v := fileKitTestVault(t)
	home := t.TempDir()
	wantErr := "the adapter no-memory-file has no memory_file in the manifest. " +
		"Fix: set adapters.no-memory-file.memory_file, or disable the adapter."

	for _, mode := range []memoryMode{memoryBlock, memoryImport} {
		cfg := vault.AdapterConfig{
			Enabled:   true,
			SkillsDir: filepath.Join(home, "skills"),
			// MemoryFile left empty on purpose.
		}
		a := newFileAdapter("no-memory-file", cfg, mode)

		_, err := a.Apply(v, false)
		if err == nil || err.Error() != wantErr {
			t.Fatalf("mode %d: Apply error = %v, want %q", mode, err, wantErr)
		}

		ps := a.Check(v)
		found := false
		for _, p := range ps {
			if p.Detail+". Fix: "+p.Fix == wantErr {
				found = true
			}
		}
		if !found {
			t.Fatalf("mode %d: Check must report the config error, got %+v", mode, ps)
		}
	}
}

// TestFileAdapterEmptySkillsDirSkipsSkills proves an empty SkillsDir
// skips the skills projection entirely, for a future
// instructions-only adapter that has no skills directory of its own.
func TestFileAdapterEmptySkillsDirSkipsSkills(t *testing.T) {
	v := fileKitTestVault(t)
	home := t.TempDir()
	memoryFile := filepath.Join(home, "MEMORY.md")
	cfg := vault.AdapterConfig{
		Enabled:    true,
		MemoryFile: memoryFile,
		// SkillsDir left empty on purpose.
	}
	a := newFileAdapter("memory-only", cfg, memoryBlock)

	report, err := a.Apply(v, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Linked != 0 || len(report.Applied) != 1 || len(report.Pruned) != 0 || len(report.Blocked) != 0 {
		t.Fatalf("an empty SkillsDir must skip the skills projection, got %+v", report)
	}
	if _, ok := ReadManagedBlock(memoryFile); !ok {
		t.Fatal("the memory block must still be written")
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("Check must be clean when skills are skipped and memory matches, got %+v", ps)
	}
}

// TestCheckReportsOnlyDamageWhenMarksAndLinksBothBroken pins a ruling:
// damage is a stop-first condition. When the memory file's marks are
// damaged AND a listed skill has no link, Check must report the
// damage alone. The damage problem suppresses the others until the
// user repairs the file — the same way Apply refuses to touch a
// damaged file at all, so Apply and Check stay symmetric.
func TestCheckReportsOnlyDamageWhenMarksAndLinksBothBroken(t *testing.T) {
	v := fileKitTestVault(t)
	home := t.TempDir()
	memoryFile := filepath.Join(home, "MEMORY.md")
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, "skills"), // deploy-checks never linked here
		MemoryFile: memoryFile,
	}
	a := newFileAdapter("both-broken", cfg, memoryBlock)

	// Damaged marks: two begin marks before the one end mark.
	corrupted := "<!-- loadout:begin -->\na\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	if err := os.WriteFile(memoryFile, []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}

	ps := a.Check(v)
	if len(ps) != 1 {
		t.Fatalf("the damage problem must suppress the missing-link problem, got %+v", ps)
	}
	if !strings.Contains(ps[0].Detail, "damaged") || !strings.Contains(ps[0].Detail, memoryFile) {
		t.Fatalf("the sole problem must name the damaged file, got %+v", ps[0])
	}
	if !strings.Contains(ps[0].Fix, "repair or remove the marks") {
		t.Fatalf("the sole problem's fix must point at repairing the marks, got %+v", ps[0])
	}
}
