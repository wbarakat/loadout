package cli

import (
	"fmt"
	"io"
	"os"

	"loadout.dev/loadout/internal/mcp"
	"loadout.dev/loadout/internal/vault"
)

// cmdMCP opens the vault and serves the Model Context Protocol on
// os.Stdin and os.Stdout: an agent tool can then query the vault, and
// later (Tasks 2 and 3) use secrets through a broker.
//
// It takes no positional arguments and ignores the "--json" flag:
// Run strips "--json" before dispatch, but the whole reply is already
// JSON-RPC, so the flag would mean nothing here even if it arrived.
//
// Any diagnostic message goes to errOut (stderr), never to stdout:
// stdout is the JSON-RPC channel, and a stray print there would
// corrupt a message.
func cmdMCP(errOut io.Writer, args []string) int {
	if rejectExtraArgs(errOut, args) {
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	if err := mcp.Serve(v, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
