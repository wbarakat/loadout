package adapter

import "loadout.dev/loadout/internal/vault"

// ProtocolFooter closes every rendered memory projection. It tells the
// reading agent how to use the memory: search first, read one item,
// save a new fact, and find every command. It holds no loadout marks,
// so it is always safe to fold into a managed block.
const ProtocolFooter = `

## How to use this memory (for agents)

This content syncs from the Loadout vault. Do not edit it here; edit the vault.
- Search first: loadout recall <terms>
- Read one item: loadout show <kind/name>
- Save a fact you learned: loadout add memory <name> --by <your-tool>, write the file it names, then run: loadout sync
- See every command: loadout help`

// renderProjection renders facts as memory, then appends the protocol
// footer. Every path that writes or compares a projected memory block
// must call this function and no other renderer. A write and a check
// that use different renderers fall out of lockstep, and doctor
// reports drift right after a clean sync.
func renderProjection(facts []vault.Fact) string {
	return vault.RenderMemory(facts) + ProtocolFooter
}
