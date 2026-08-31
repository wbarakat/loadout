package adapter_test

import (
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
	if !strings.Contains(block, "deploy-checks") || !strings.Contains(block, "SKILL.md") {
		t.Fatal("the skills index is missing from the block")
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
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
