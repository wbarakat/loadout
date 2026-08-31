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

	if err := a.Apply(v); err != nil {
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

func TestAgentsMDApplyRefusesFactWithMark(t *testing.T) {
	v := testVault(t)
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\n---\nI use Go.\n<!-- loadout:begin -->\n"), 0o644)
	target := filepath.Join(t.TempDir(), "proj", "AGENTS.md")
	a := adapter.AgentsMD{Cfg: vault.AdapterConfig{Enabled: true, Targets: []string{target}}}

	err := a.Apply(v)
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
