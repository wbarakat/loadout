package adapter

import "loadout.dev/loadout/internal/vault"

// Cursor projects the vault into Cursor: skills as symlinks only. A
// probe of ~/.cursor found no verified global-instructions file (no
// rules file, no AGENTS.md at the top level — see the task-4 report
// for the evidence), so Cursor carries no memory_file and runs in
// memoryNone mode: skills-only, no memory file ever written. It is a
// thin wrapper over the file-adapter kit.
type Cursor struct {
	Cfg vault.AdapterConfig
}

func (a Cursor) Name() string { return "cursor" }

func (a Cursor) Apply(v *vault.Vault, dry bool) (Report, error) {
	return newFileAdapter(a.Name(), a.Cfg, memoryNone).Apply(v, dry)
}

func (a Cursor) Check(v *vault.Vault) []Problem {
	return newFileAdapter(a.Name(), a.Cfg, memoryNone).Check(v)
}
