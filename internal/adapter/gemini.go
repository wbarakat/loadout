package adapter

import "loadout.dev/loadout/internal/vault"

// Gemini projects the vault into Gemini: skills as symlinks, memory as
// a managed block with the full rendered content. It is a thin wrapper
// over the file-adapter kit, in memoryBlock mode.
type Gemini struct {
	Cfg vault.AdapterConfig
}

func (a Gemini) Name() string { return "gemini" }

func (a Gemini) Apply(v *vault.Vault, dry bool) (Report, error) {
	return newFileAdapter(a.Name(), a.Cfg, memoryBlock).Apply(v, dry)
}

func (a Gemini) Check(v *vault.Vault) []Problem {
	return newFileAdapter(a.Name(), a.Cfg, memoryBlock).Check(v)
}
