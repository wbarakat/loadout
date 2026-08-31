package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

// TestCursorApplyAndCheck proves Cursor links skills only: no
// memory_file is set, and Apply and Check must never touch one.
func TestCursorApplyAndCheck(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: filepath.Join(home, ".cursor", "skills"),
	}
	a := adapter.Cursor{Cfg: cfg}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(cfg.SkillsDir, "deploy-checks")); err != nil {
		t.Fatal("skill link is missing")
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); err != nil {
		t.Fatal(".cursor dir must exist for the skills link")
	}
	// memoryNone must never create a memory file anywhere under home.
	entries, err := os.ReadDir(filepath.Join(home, ".cursor"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "skills" {
			t.Fatalf("no file other than skills must exist under .cursor, found %q", e.Name())
		}
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
	}
}

// TestCursorApplyDryRunWritesNothing proves a dry run creates no
// skill link and reports DryRun true.
func TestCursorApplyDryRunWritesNothing(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: filepath.Join(home, ".cursor", "skills"),
	}
	a := adapter.Cursor{Cfg: cfg}

	report, err := a.Apply(v, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun {
		t.Fatal("the report must carry DryRun true")
	}
	if _, err := os.Lstat(filepath.Join(cfg.SkillsDir, "deploy-checks")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create the skill link")
	}
	applied := strings.Join(report.Applied, "|")
	if strings.Contains(applied, "memory") {
		t.Fatalf("memoryNone must never report a memory entry, got %v", report.Applied)
	}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after a real apply: %+v", ps)
	}
}

// TestCursorApplyReportsBlockedSkill proves a real directory occupying
// a skill's link path is reported in Report.Blocked, and Apply does
// not error over it.
func TestCursorApplyReportsBlockedSkill(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: filepath.Join(home, ".cursor", "skills"),
	}
	a := adapter.Cursor{Cfg: cfg}
	blockedPath := filepath.Join(cfg.SkillsDir, "deploy-checks")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := a.Apply(v, false)
	if err != nil {
		t.Fatalf("Apply must not error on a blocked skill, got %v", err)
	}
	if len(report.Blocked) == 0 {
		t.Fatal("Apply must report the blocked skill")
	}
}

// TestCursorCheckReportsMemoryFileIgnored proves Check flags a
// memory_file set on cursor. cursor runs in memoryNone mode and never
// touches the file; without this, a user who sets memory_file would
// get no feedback that loadout ignores it.
func TestCursorCheckReportsMemoryFileIgnored(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".cursor", "skills"),
		MemoryFile: filepath.Join(home, ".cursor", "MEMORY.md"),
	}
	a := adapter.Cursor{Cfg: cfg}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}

	ps := a.Check(v)
	if len(ps) != 1 {
		t.Fatalf("Check must report exactly the ignored memory_file problem, got %+v", ps)
	}
	want := adapter.Problem{
		Adapter: "cursor",
		Detail:  "the adapter cursor takes no memory_file; loadout ignores it.",
		Fix:     "remove adapters.cursor.memory_file, or use the agents-md adapter for extra instruction files.",
	}
	if ps[0] != want {
		t.Fatalf("Check = %+v, want %+v", ps[0], want)
	}
}

// TestCursorEnabledInRegistry proves the default manifest ships
// Cursor disabled, with the plain skills dir and no memory file, and
// that Enabled() picks it up once turned on.
func TestCursorEnabledInRegistry(t *testing.T) {
	m := vault.DefaultManifest()
	cfg, ok := m.Adapters["cursor"]
	if !ok {
		t.Fatal("cursor must be in the default manifest")
	}
	if cfg.Enabled {
		t.Fatal("cursor must be disabled by default")
	}
	if cfg.SkillsDir != "~/.cursor/skills" {
		t.Fatalf("bad skills dir: %q", cfg.SkillsDir)
	}
	if cfg.MemoryFile != "" {
		t.Fatalf("cursor must ship with no memory file, got %q", cfg.MemoryFile)
	}

	v := testVault(t)
	v.Manifest.Adapters["cursor"] = vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: "~/.cursor/skills",
	}
	got := adapter.Enabled(v)
	names := []string{}
	for _, a := range got {
		names = append(names, a.Name())
	}
	if len(names) != 3 || names[0] != "claude-code" || names[1] != "pi" || names[2] != "cursor" {
		t.Fatalf("bad registry order: %v", names)
	}
}
