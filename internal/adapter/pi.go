package adapter

import (
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// Pi projects the vault into pi: skills as symlinks, memory as a
// managed block with the full rendered content.
type Pi struct {
	Cfg vault.AdapterConfig
}

func (a Pi) Name() string { return "pi" }

func (a Pi) Apply(v *vault.Vault, dry bool) (Report, error) {
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
	memoryFile := vault.ExpandPath(a.Cfg.MemoryFile)
	content := vault.RenderMemory(facts)
	if dry {
		if err := checkManagedBlockDamage(memoryFile); err != nil {
			return report, err
		}
		report.Applied = append(report.Applied, managedBlockDryMsg(memoryFile, strings.TrimSpace(content)))
	} else {
		if err := WriteManagedBlock(memoryFile, content); err != nil {
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

func (a Pi) Check(v *vault.Vault) []Problem {
	var ps []Problem
	skills, err := vault.ListSkills(v)
	if err != nil {
		return []Problem{{a.Name(), err.Error(), "repair the vault skills directory"}}
	}
	ps = append(ps, checkLinks(a.Name(), skills, v.SkillsDir(), vault.ExpandPath(a.Cfg.SkillsDir))...)
	facts, err := vault.ListFacts(v)
	if err != nil {
		return append(ps, Problem{a.Name(), err.Error(), "repair the vault memory directory"})
	}
	got, ok := ReadManagedBlock(vault.ExpandPath(a.Cfg.MemoryFile))
	if !ok || got != strings.TrimSpace(vault.RenderMemory(facts)) {
		ps = append(ps, Problem{a.Name(), "the memory block is missing or stale", "run: loadout sync"})
	}
	return ps
}
