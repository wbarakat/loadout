package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

// TestHermesApplyAndCheck proves Hermes links skills only: no
// memory_file is set, and Apply and Check must never touch one.
func TestHermesApplyAndCheck(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: filepath.Join(home, ".hermes", "skills"),
	}
	a := adapter.Hermes{Cfg: cfg}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(cfg.SkillsDir, "deploy-checks")); err != nil {
		t.Fatal("skill link is missing")
	}
	if _, err := os.Stat(filepath.Join(home, ".hermes")); err != nil {
		t.Fatal(".hermes dir must exist for the skills link")
	}
	// memoryNone must never create a memory file anywhere under home.
	entries, err := os.ReadDir(filepath.Join(home, ".hermes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "skills" {
			t.Fatalf("no file other than skills must exist under .hermes, found %q", e.Name())
		}
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
	}
}

// TestHermesApplyDryRunWritesNothing proves a dry run creates no
// skill link and reports DryRun true.
func TestHermesApplyDryRunWritesNothing(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: filepath.Join(home, ".hermes", "skills"),
	}
	a := adapter.Hermes{Cfg: cfg}

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

// TestHermesApplyReportsBlockedSkill proves a real directory occupying
// a skill's link path is reported in Report.Blocked, and Apply does
// not error over it.
func TestHermesApplyReportsBlockedSkill(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: filepath.Join(home, ".hermes", "skills"),
	}
	a := adapter.Hermes{Cfg: cfg}
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

// TestHermesEnabledInRegistry proves the default manifest ships
// Hermes disabled, with the plain skills dir and no memory file, and
// that Enabled() picks it up once turned on.
func TestHermesEnabledInRegistry(t *testing.T) {
	m := vault.DefaultManifest()
	cfg, ok := m.Adapters["hermes"]
	if !ok {
		t.Fatal("hermes must be in the default manifest")
	}
	if cfg.Enabled {
		t.Fatal("hermes must be disabled by default")
	}
	if cfg.SkillsDir != "~/.hermes/skills" {
		t.Fatalf("bad skills dir: %q", cfg.SkillsDir)
	}
	if cfg.MemoryFile != "" {
		t.Fatalf("hermes must ship with no memory file, got %q", cfg.MemoryFile)
	}

	v := testVault(t)
	v.Manifest.Adapters["hermes"] = vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: "~/.hermes/skills",
	}
	got := adapter.Enabled(v)
	names := []string{}
	for _, a := range got {
		names = append(names, a.Name())
	}
	if len(names) != 3 || names[0] != "claude-code" || names[1] != "pi" || names[2] != "hermes" {
		t.Fatalf("bad registry order: %v", names)
	}
}
