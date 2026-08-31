// Package cli implements the loadout commands.
package cli

import (
	"fmt"
	"io"
)

const usage = `usage: loadout <command>

commands:
  init                       create the vault
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
  sync [--dry-run] [--remote] project the vault into every enabled tool, and sync
  status                     show the vault and the adapter state
  doctor                     find problems and show the fix for each one
  log                        show the vault history
  undo                       revert to the state before the last change
  review                     list draft items awaiting review
  review keep KIND/NAME      mark a draft item kept
  review drop KIND/NAME      delete a draft item
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
	switch args[0] {
	case "init":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdInit(out, errOut, m)
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
	case "sync":
		return cmdSync(out, errOut, args[1:], m)
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
	case "review":
		return cmdReview(out, errOut, args[1:], m)
	default:
		fmt.Fprintf(errOut, "unknown command %q\n%s", args[0], usage)
		return 2
	}
}

// rejectExtraArgs prints the usage text to errOut and reports true
// when rest holds an argument. Run calls this for every verb that
// takes no positional arguments — init, sync (after its own
// "--dry-run" extraction), status, doctor, list, context, device,
// log, undo, and help — so an unknown argument never rides along on
// a mutating verb such as sync or undo.
func rejectExtraArgs(errOut io.Writer, rest []string) bool {
	if len(rest) == 0 {
		return false
	}
	fmt.Fprint(errOut, usage)
	return true
}
