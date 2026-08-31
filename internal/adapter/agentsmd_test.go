package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestAgentsMDApply(t *testing.T) {
	v := testVault(t)
	target := filepath.Join(t.TempDir(), "proj", "AGENTS.md")
	a := adapter.AgentsMD{Cfg: vault.AdapterConfig{Enabled: true, Targets: []string{target}}}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	block, ok := adapter.ReadManagedBlock(target)
	if !ok {
		t.Fatal("no block written")
	}
	if !strings.Contains(block, "I use Go.") {
		t.Fatal("memory is missing from the block")
	}
	want := filepath.Join(v.SkillsDir(), "deploy-checks", "SKILL.md")
	if !strings.Contains(block, want) {
		t.Fatalf("the block must hold the absolute skill path %s", want)
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
	}
}

func TestAgentsMDApplyDryRunWritesNothingAndReportsStatus(t *testing.T) {
	v := testVault(t)
	target := filepath.Join(t.TempDir(), "proj", "AGENTS.md")
	a := adapter.AgentsMD{Cfg: vault.AdapterConfig{Enabled: true, Targets: []string{target}}}

	report, err := a.Apply(v, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun {
		t.Fatal("the report must carry DryRun true")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create the target file")
	}
	applied := strings.Join(report.Applied, "|")
	if !strings.Contains(applied, "memory: block would change") {
		t.Fatalf("a not-yet-synced target must report the block would change, got %v", report.Applied)
	}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	report, err = a.Apply(v, true)
	if err != nil {
		t.Fatal(err)
	}
	applied = strings.Join(report.Applied, "|")
	if !strings.Contains(applied, "memory: up to date") {
		t.Fatalf("a synced target must report up to date, got %v", report.Applied)
	}
}

func TestAgentsMDApplyRefusesFactWithMark(t *testing.T) {
	v := testVault(t)
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\n---\nI use Go.\n<!-- loadout:begin -->\n"), 0o644)
	target := filepath.Join(t.TempDir(), "proj", "AGENTS.md")
	a := adapter.AgentsMD{Cfg: vault.AdapterConfig{Enabled: true, Targets: []string{target}}}

	_, err := a.Apply(v, false)
	if err == nil || !strings.Contains(err.Error(), "memory/stack") {
		t.Fatalf("Apply must name the offending fact, got %v", err)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("the target file must not be created")
	}
}

func TestEnabledRegistry(t *testing.T) {
	v := testVault(t)
	got := adapter.Enabled(v)
	if len(got) != 2 || got[0].Name() != "claude-code" || got[1].Name() != "pi" {
		names := []string{}
		for _, a := range got {
			names = append(names, a.Name())
		}
		t.Fatalf("bad registry: %v", names)
	}
}
