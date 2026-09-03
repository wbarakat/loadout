// Package cli implements the loadout commands.
package cli

import (
	"fmt"
	"io"
)

const usage = `usage: loadout <command>

commands:
  init [--yes] [--tools a,b,...] [--no-import]
       [--remote URL --token-file PATH] [--project-memory]
                             first-run wizard: detect installed agent tools,
                             create the vault (or keep an existing one),
                             enable+configure their adapters, offer to
                             import their skills/memory as drafts, and
                             optionally connect a loadoutd remote.
                             Prompts over stdin; every prompt has a
                             default, so it is also safe to run
                             unattended with empty stdin.
                             --yes skips every prompt for an unattended,
                             headless install (an agent or CI): it enables
                             adapters for --tools (or every detected tool
                             when --tools is absent), imports unless you
                             pass --no-import, and connects a remote only
                             when you pass both --remote URL and
                             --token-file PATH. The token comes from that
                             file, never from the command line, and never
                             appears in any output. Add --project-memory
                             to also pull per-project or per-profile
                             memory during the import.
  add skill NAME [--by WHO]  add a skill
  add memory NAME [--by WHO] add a memory fact
  show KIND/NAME             print an item's file
  list                       show every item
  edit KIND/NAME             open an item in $EDITOR
  recall TERM...             find items matching every term
  context                    show the compact picture of the vault
  device                     show this device's name and age recipient
  remote                     show the configured remote and last synced version
  remote add URL TOKEN       configure the remote to sync with
  join URL TOKEN             enroll this device with a remote, waiting for approval
  devices [--json]           show every device: approved, waiting, or re-keyed
  devices approve NAME       approve a waiting device
  devices approve NAME --rotate RECIPIENT  trust an out-of-band-verified new key
  sync [--dry-run] [--remote] project the vault into every enabled tool, and sync
  watch [--interval DUR]     sync in a loop until Ctrl-C (default 10s)
  status                     show the vault and the adapter state
  doctor                     find problems and show the fix for each one
  log                        show the vault history
  undo                       revert to the state before the last change
  import [SOURCE...] [--skills] [--memory] [--project DIR]
         [--project-memory] [--dry-run]
                             pull skills and memory from installed agent tools
                             (claude-code, codex, cursor, hermes, pi, gemini,
                             droid) into the vault as drafts.
                             Memory defaults to GLOBAL instruction files only.
                             Pass --project-memory to also pull per-project or
                             per-profile memory for --project DIR, or the
                             current directory.
                             Devin is hosted. Loadout cannot import it from
                             this device.
                             Cursor keeps global User Rules in an internal
                             database. Loadout cannot import them.
  review                     list draft items awaiting review
  review keep KIND/NAME      mark a draft item kept
  review drop KIND/NAME      delete a draft item
  secret add NAME --service SVC [--hook TEXT] [--rotate-after DUR] [--by WHO]
                             add a secret; the value is piped on stdin
  secret list [--json]       show every secret's metadata, never its value
  secret show NAME [--reveal] [--by WHO]
                             refuse by default; print the value only with --reveal
  secret rotate NAME [--by WHO]
                             replace a secret's value; the new value is piped on stdin
  secret rm NAME             remove a secret
  run --secret NAME[=ENVVAR] [--secret NAME2...] [--by WHO] -- CMD [args...]
                             decrypt secrets, inject them into a child process's
                             environment, exec it, and exit with its exit code
  mcp                        serve the vault over MCP as JSON-RPC on stdio
  version                    print the loadout version
  help                       show this message

flags:
  --json                     print the result as JSON instead of text
`

// Run dispatches one loadout command. It first strips a "--json"
// argument found at any position in args, so a verb sees only its own
// arguments plus a mode flag telling it whether to render JSON.
func Run(out, errOut io.Writer, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	args, wantJSON := extractJSON(args)
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		fmt.Fprint(out, usage)
		return 0
	}
	m := modeText
	if wantJSON {
		m = modeJSON
	}
	if args[0] == "version" || args[0] == "--version" {
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdVersion(out, m)
	}
	switch args[0] {
	case "init":
		return cmdInit(out, errOut, args[1:], m)
	case "add":
		return cmdAdd(out, errOut, args[1:], m)
	case "show":
		return cmdShow(out, errOut, args[1:], m)
	case "list":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdList(out, errOut, m)
	case "edit":
		return cmdEdit(out, errOut, args[1:], m)
	case "recall":
		return cmdRecall(out, errOut, args[1:], m)
	case "context":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdContext(out, errOut, m)
	case "device":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdDevice(out, errOut, m)
	case "remote":
		return cmdRemote(out, errOut, args[1:], m)
	case "join":
		return cmdJoin(out, errOut, args[1:], m)
	case "devices":
		return cmdDevices(out, errOut, args[1:], m)
	case "sync":
		return cmdSync(out, errOut, args[1:], m)
	case "watch":
		return cmdWatch(out, errOut, args[1:], m)
	case "status":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdStatus(out, errOut, m)
	case "doctor":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdDoctor(out, errOut, m)
	case "log":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdLog(out, errOut, m)
	case "undo":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdUndo(out, errOut, m)
	case "import":
		return cmdImport(out, errOut, args[1:], m)
	case "review":
		return cmdReview(out, errOut, args[1:], m)
	case "secret":
		return cmdSecret(out, errOut, args[1:], m)
	case "run":
		return cmdRun(out, errOut, args[1:], m)
	case "mcp":
		return cmdMCP(errOut, args[1:])
	default:
		fmt.Fprintf(errOut, "unknown command %q\n%s", args[0], usage)
		return 2
	}
}

// rejectExtraArgs prints the usage text to errOut and reports true
// when rest holds an argument. Run calls this for every verb that
// takes no positional arguments — sync (after its own "--dry-run"
// extraction), status, doctor, list, context, device, log, undo, and
// help — so an unknown argument never rides along on a mutating verb
// such as sync or undo. init has its own flags now, so it parses and
// rejects its own leftover arguments (parseInitArgs) instead of
// calling this.
func rejectExtraArgs(errOut io.Writer, rest []string) bool {
	if len(rest) == 0 {
		return false
	}
	fmt.Fprint(errOut, usage)
	return true
}
