package adapter

import (
	"fmt"
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
	report := newReport(a.Name(), dry)
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
	content := renderProjection(facts)
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
	report.Linked = len(applied)
	report.Applied = append(report.Applied, applied...)
	report.Pruned = orEmpty(pruned)
	report.Blocked = orEmpty(blocked)
	return report, nil
}

func (a Pi) Check(v *vault.Vault) []Problem {
	var ps []Problem
	skills, err := vault.ListSkills(v)
	if err != nil {
		return []Problem{{a.Name(), err.Error(), "repair the vault skills directory"}}
	}
	skillsDir := vault.ExpandPath(a.Cfg.SkillsDir)
	ps = append(ps, checkLinks(a.Name(), skills, v.SkillsDir(), skillsDir)...)
	for _, p := range orphanLinks(skills, v.SkillsDir(), skillsDir) {
		p.Adapter = a.Name()
		ps = append(ps, p)
	}
	memoryFile := vault.ExpandPath(a.Cfg.MemoryFile)
	if err := checkManagedBlockDamage(memoryFile); err != nil {
		// Damaged marks need a repair, not a sync: a sync would refuse
		// to touch the file too, so telling the user to sync it is a
		// dead end.
		ps = append(ps, Problem{a.Name(), fmt.Sprintf("the loadout marks in %s are damaged", memoryFile), fmt.Sprintf("repair or remove the marks in %s.", memoryFile)})
		return ps
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return append(ps, Problem{a.Name(), err.Error(), "repair the vault memory directory"})
	}
	got, ok := ReadManagedBlock(memoryFile)
	if !ok || got != strings.TrimSpace(renderProjection(facts)) {
		ps = append(ps, Problem{a.Name(), "the memory block is missing or stale", "run: loadout sync"})
	}
	return ps
}
