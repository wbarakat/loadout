package adapter

import "loadout.dev/loadout/internal/vault"

// ClaudeCode projects the vault into Claude Code: skills as symlinks,
// memory as one import line in CLAUDE.md. It is a thin wrapper over
// the file-adapter kit, in memoryImport mode.
type ClaudeCode struct {
	Cfg vault.AdapterConfig
}

func (a ClaudeCode) Name() string { return "claude-code" }

func (a ClaudeCode) Apply(v *vault.Vault, dry bool) (Report, error) {
	return newFileAdapter(a.Name(), a.Cfg, memoryImport).Apply(v, dry)
}

func (a ClaudeCode) Check(v *vault.Vault) []Problem {
	return newFileAdapter(a.Name(), a.Cfg, memoryImport).Check(v)
}
