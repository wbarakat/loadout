package adapter

import "loadout.dev/loadout/internal/vault"

// Hermes projects the vault into Hermes: skills as symlinks only. A
// probe of ~/.hermes found SOUL.md, a persona file loadout must
// never target, plus an auto-managed memories/MEMORY.md and
// memories/USER.md pair that hermes itself locks and writes during
// normal use — not a stable, user-owned instructions file like
// AGENTS.md. No such file exists at the top level of ~/.hermes (see
// the task-5 report for the full probe evidence), so Hermes carries
// no memory_file and runs in memoryNone mode: skills-only, no memory
// file ever written. It is a thin wrapper over the file-adapter kit.
type Hermes struct {
	Cfg vault.AdapterConfig
}

func (a Hermes) Name() string { return "hermes" }

func (a Hermes) Apply(v *vault.Vault, dry bool) (Report, error) {
	return newFileAdapter(a.Name(), a.Cfg, memoryNone).Apply(v, dry)
}

func (a Hermes) Check(v *vault.Vault) []Problem {
	return newFileAdapter(a.Name(), a.Cfg, memoryNone).Check(v)
}
