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

func (a ClaudeCode) Apply(v *vault.Vault, dry bool) (Report, error) {
	report := Report{Adapter: a.Name(), DryRun: dry}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return report, err
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return report, err
	}
	if err := scanForMarks(facts, skills); err != nil {
		return report, err
	}
	renderPath := filepath.Join(v.RenderDir(), "memory.md")
	memoryFile := vault.ExpandPath(a.Cfg.MemoryFile)
	if dry {
		if err := checkManagedBlockDamage(memoryFile); err != nil {
			return report, err
		}
		report.Applied = append(report.Applied, managedBlockDryMsg(memoryFile, a.memoryImport(v)))
	} else {
		if err := writeFileAtomic(renderPath, []byte(vault.RenderMemory(facts))); err != nil {
			return report, err
		}
		if err := WriteManagedBlock(memoryFile, a.memoryImport(v)); err != nil {
			return report, err
		}
		report.Applied = append(report.Applied, "memory: block written")
	}
	skillsDir := vault.ExpandPath(a.Cfg.SkillsDir)
	applied, pruned, blocked, err := LinkSkills(skills, v.SkillsDir(), skillsDir, dry)
	if err != nil {
		return report, err
	}
	report.Applied = append(report.Applied, applied...)
	report.Pruned = pruned
	report.Blocked = blocked
	return report, nil
}

func (a ClaudeCode) Check(v *vault.Vault) []Problem {
	var ps []Problem
	skills, err := vault.ListSkills(v)
	if err != nil {
		return []Problem{{a.Name(), err.Error(), "repair the vault skills directory"}}
	}
	ps = append(ps, checkLinks(a.Name(), skills, v.SkillsDir(), vault.ExpandPath(a.Cfg.SkillsDir))...)
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
