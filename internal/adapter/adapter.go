package adapter

import "loadout.dev/loadout/internal/vault"

// Problem is one finding from a check, with a fix the user can run.
type Problem struct {
	Adapter string
	Detail  string
	Fix     string
}

// Adapter projects the vault into one tool.
type Adapter interface {
	Name() string
	Apply(v *vault.Vault) error
	Check(v *vault.Vault) []Problem
}
