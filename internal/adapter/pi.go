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

func (a Pi) Apply(v *vault.Vault) error {
	facts, err := vault.ListFacts(v)
	if err != nil {
		return err
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return err
	}
	if err := scanForMarks(facts, skills); err != nil {
		return err
	}
	if err := WriteManagedBlock(vault.ExpandPath(a.Cfg.MemoryFile), vault.RenderMemory(facts)); err != nil {
		return err
	}
	skillsDir := vault.ExpandPath(a.Cfg.SkillsDir)
	blocked, err := LinkSkills(skills, v.SkillsDir(), skillsDir)
	if err != nil {
		return err
	}
	if len(blocked) > 0 {
		return blockedSkillsError(blocked, skillsDir)
	}
	return nil
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
