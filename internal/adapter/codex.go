package adapter

import "loadout.dev/loadout/internal/vault"

// Codex projects the vault into Codex: skills as symlinks, memory as
// a managed block with the full rendered content. It is a thin wrapper
// over the file-adapter kit, in memoryBlock mode.
type Codex struct {
	Cfg vault.AdapterConfig
}

func (a Codex) Name() string { return "codex" }

func (a Codex) Apply(v *vault.Vault, dry bool) (Report, error) {
	return newFileAdapter(a.Name(), a.Cfg, memoryBlock).Apply(v, dry)
}

func (a Codex) Check(v *vault.Vault) []Problem {
	return newFileAdapter(a.Name(), a.Cfg, memoryBlock).Check(v)
}
