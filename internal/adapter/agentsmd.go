package adapter

import (
	"path/filepath"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// AgentsMD writes memory and a skills index into any AGENTS.md file
// the user lists as a target. It serves tools without a dedicated
// adapter.
type AgentsMD struct {
	Cfg vault.AdapterConfig
}

func (a AgentsMD) Name() string { return "agents-md" }

func renderAgentsMD(v *vault.Vault) (string, error) {
	facts, err := vault.ListFacts(v)
	if err != nil {
		return "", err
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(vault.RenderMemory(facts))
	b.WriteString("\n## Skills (synced by Loadout)\n\n")
	for _, s := range skills {
		b.WriteString("- " + s.Name + ": " + filepath.Join(s.Dir, "SKILL.md") + " — " + s.Description + "\n")
	}
	return b.String(), nil
}

func (a AgentsMD) Apply(v *vault.Vault) error {
	content, err := renderAgentsMD(v)
	if err != nil {
		return err
	}
	for _, target := range a.Cfg.Targets {
		if err := WriteManagedBlock(vault.ExpandPath(target), content); err != nil {
			return err
		}
	}
	return nil
}

func (a AgentsMD) Check(v *vault.Vault) []Problem {
	content, err := renderAgentsMD(v)
	if err != nil {
		return []Problem{{a.Name(), err.Error(), "repair the vault"}}
	}
	var ps []Problem
	for _, target := range a.Cfg.Targets {
		got, ok := ReadManagedBlock(vault.ExpandPath(target))
		if !ok || got != strings.TrimSpace(content) {
			ps = append(ps, Problem{a.Name(), "the block in " + target + " is missing or stale", "run: loadout sync"})
		}
	}
	return ps
}
