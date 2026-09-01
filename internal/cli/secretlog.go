package cli

import "loadout.dev/loadout/internal/vault"

// AccessEntry and AppendAccessLog are aliases for vault's own access
// log types, kept here so every existing caller in this package
// (secret.go, runcmd.go) and every existing test needs no change.
// The real implementation moved to internal/vault/accesslog.go so
// internal/mcp's broker (Task 3) can append to the same log without
// an import cycle: internal/cli already imports internal/mcp to
// serve "loadout mcp" (see mcp.go), so internal/mcp cannot import
// internal/cli back.
type AccessEntry = vault.AccessEntry

// AppendAccessLog appends one JSON line to the vault's access log.
// See vault.AppendAccessLog for the full documentation.
func AppendAccessLog(v *vault.Vault, entry AccessEntry) error {
	return vault.AppendAccessLog(v, entry)
}
