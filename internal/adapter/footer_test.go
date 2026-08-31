package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

// TestProtocolFooterHoldsNoLoadoutMarks proves the footer text is safe
// to fold into a managed block: it must never itself trip the mark
// scan or the mark-in-content guard.
func TestProtocolFooterHoldsNoLoadoutMarks(t *testing.T) {
	if strings.Contains(adapter.ProtocolFooter, "<!-- loadout:begin -->") ||
		strings.Contains(adapter.ProtocolFooter, "<!-- loadout:end -->") {
		t.Fatalf("the protocol footer must hold no loadout marks, got %q", adapter.ProtocolFooter)
	}
}

func TestPiBlockEndsWithProtocolFooter(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".pi", "agent", "skills"),
		MemoryFile: filepath.Join(home, ".pi", "AGENTS.md"),
	}
	a := adapter.Pi{Cfg: cfg}
	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	block, ok := adapter.ReadManagedBlock(cfg.MemoryFile)
	if !ok {
		t.Fatal("no block written")
	}
	if !strings.HasSuffix(block, strings.TrimSpace(adapter.ProtocolFooter)) {
		t.Fatalf("the pi block must end with the protocol footer, got %q", block)
	}
}

func TestRenderedMemoryEndsWithProtocolFooter(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		MemoryFile: filepath.Join(home, ".claude", "CLAUDE.md"),
	}
	a := adapter.ClaudeCode{Cfg: cfg}
	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(v.RenderDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), strings.TrimSpace(adapter.ProtocolFooter)) {
		t.Fatalf("render/memory.md must end with the protocol footer, got %q", string(data))
	}
}

func TestAgentsMDBlockHoldsFooterAfterSkillsIndex(t *testing.T) {
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
	skillsIdx := strings.Index(block, "## Skills (synced by Loadout)")
	footerIdx := strings.Index(block, "## How to use this memory (for agents)")
	if skillsIdx < 0 || footerIdx < 0 || skillsIdx > footerIdx {
		t.Fatalf("the skills index must sit between the memory and the footer, got %q", block)
	}
	if !strings.HasSuffix(block, strings.TrimSpace(adapter.ProtocolFooter)) {
		t.Fatalf("the agents-md block must end with the protocol footer, got %q", block)
	}
}

// TestCheckStaysCleanAfterApplyAcrossAdapters is the lockstep proof:
// every adapter's Apply and Check must render the same projection, or
// doctor would report drift right after a clean sync.
func TestCheckStaysCleanAfterApplyAcrossAdapters(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	claude := adapter.ClaudeCode{Cfg: vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		MemoryFile: filepath.Join(home, ".claude", "CLAUDE.md"),
	}}
	pi := adapter.Pi{Cfg: vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".pi", "agent", "skills"),
		MemoryFile: filepath.Join(home, ".pi", "AGENTS.md"),
	}}
	agentsmd := adapter.AgentsMD{Cfg: vault.AdapterConfig{
		Enabled: true,
		Targets: []string{filepath.Join(home, "proj", "AGENTS.md")},
	}}

	for _, a := range []adapter.Adapter{claude, pi, agentsmd} {
		if _, err := a.Apply(v, false); err != nil {
			t.Fatalf("%s: apply: %v", a.Name(), err)
		}
		if ps := a.Check(v); len(ps) != 0 {
			t.Fatalf("%s: check must be clean right after apply: %+v", a.Name(), ps)
		}
		// And a dry-run sync must report up to date, not drift.
		report, err := a.Apply(v, true)
		if err != nil {
			t.Fatalf("%s: dry apply: %v", a.Name(), err)
		}
		applied := strings.Join(report.Applied, "|")
		if !strings.Contains(applied, "memory: up to date") {
			t.Fatalf("%s: dry run must report up to date right after a real sync, got %v", a.Name(), report.Applied)
		}
	}
}
