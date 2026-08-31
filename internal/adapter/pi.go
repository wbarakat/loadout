package adapter

import "loadout.dev/loadout/internal/vault"

// Pi projects the vault into pi: skills as symlinks, memory as a
// managed block with the full rendered content. It is a thin wrapper
// over the file-adapter kit, in memoryBlock mode.
type Pi struct {
	Cfg vault.AdapterConfig
}

func (a Pi) Name() string { return "pi" }

func (a Pi) Apply(v *vault.Vault, dry bool) (Report, error) {
	return newFileAdapter(a.Name(), a.Cfg, memoryBlock).Apply(v, dry)
}

func (a Pi) Check(v *vault.Vault) []Problem {
	return newFileAdapter(a.Name(), a.Cfg, memoryBlock).Check(v)
}
