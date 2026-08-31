package adapter

import (
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/vault"
)

// ClaudeCode projects the vault into Claude Code: skills as symlinks,
// memory as one import line in CLAUDE.md.
type ClaudeCode struct {
	Cfg vault.AdapterConfig
}

func (a ClaudeCode) Name() string { return "claude-code" }

func (a ClaudeCode) memoryImport(v *vault.Vault) string {
	return "@" + filepath.Join(v.RenderDir(), "memory.md")
}

func (a ClaudeCode) Apply(v *vault.Vault) error {
	skills, err := vault.ListSkills(v)
	if err != nil {
		return err
	}
	if _, err := LinkSkills(skills, vault.ExpandPath(a.Cfg.SkillsDir)); err != nil {
		return err
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return err
	}
	renderPath := filepath.Join(v.RenderDir(), "memory.md")
	if err := os.WriteFile(renderPath, []byte(vault.RenderMemory(facts)), 0o644); err != nil {
		return err
	}
	return WriteManagedBlock(vault.ExpandPath(a.Cfg.MemoryFile), a.memoryImport(v))
}

func (a ClaudeCode) Check(v *vault.Vault) []Problem {
	var ps []Problem
	skills, err := vault.ListSkills(v)
	if err != nil {
		return []Problem{{a.Name(), err.Error(), "repair the vault skills directory"}}
	}
	dir := vault.ExpandPath(a.Cfg.SkillsDir)
	for _, s := range skills {
		cur, err := os.Readlink(filepath.Join(dir, s.Name))
		if err != nil || cur != s.Dir {
			ps = append(ps, Problem{a.Name(), "the skill " + s.Name + " is not linked", "run: loadout sync"})
		}
	}
	got, ok := ReadManagedBlock(vault.ExpandPath(a.Cfg.MemoryFile))
	if !ok || got != a.memoryImport(v) {
		ps = append(ps, Problem{a.Name(), "the memory import block is missing or stale", "run: loadout sync"})
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return append(ps, Problem{a.Name(), err.Error(), "repair the vault memory directory"})
	}
	renderPath := filepath.Join(v.RenderDir(), "memory.md")
	data, err := os.ReadFile(renderPath)
	if err != nil || string(data) != vault.RenderMemory(facts) {
		ps = append(ps, Problem{a.Name(), "the rendered memory is missing or stale", "run: loadout sync"})
	}
	return ps
}
