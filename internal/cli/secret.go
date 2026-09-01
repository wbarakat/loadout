package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"loadout.dev/loadout/internal/vault"
)

const secretUsage = `usage: loadout secret add <name> --service <svc> [--hook <text>] [--rotate-after <dur>] [--by <who>]
       loadout secret list [--json]
       loadout secret show <name> [--reveal] [--by <who>]
       loadout secret rm <name>`

// stdinIsTTY reports whether os.Stdin is a terminal rather than a
// pipe or a redirected file. cmdSecretAdd refuses to run when this is
// true, since it has no safe way to read a value without echoing it.
// It is a package variable, not a plain function, so a test can
// substitute it and exercise that refusal without a real terminal.
var stdinIsTTY = func() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// cmdSecret dispatches the four secret forms: add, list, show, rm.
func cmdSecret(out, errOut io.Writer, args []string, m mode) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, secretUsage)
		return 2
	}
	switch args[0] {
	case "add":
		return cmdSecretAdd(out, errOut, args[1:], m)
	case "list":
		if rejectExtraArgs(errOut, args[1:]) {
			return 2
		}
		return cmdSecretList(out, errOut, m)
	case "show":
		return cmdSecretShow(out, errOut, args[1:], m)
	case "rm":
		return cmdSecretRemove(out, errOut, args[1:], m)
	default:
		fmt.Fprintln(errOut, secretUsage)
		return 2
	}
}

// secretAddArgs is the parsed shape of "secret add <name> --service
// <svc> [--hook <text>] [--rotate-after <dur>] [--by <who>]".
type secretAddArgs struct {
	name, service, hook, rotateAfter, by string
}

// parseSecretAddArgs reads secretAddArgs out of args. by defaults to
// "human" when --by is absent, matching "add memory --by". ok is
// false when args does not match the expected shape, or --service is
// missing, so the caller can print usage.
func parseSecretAddArgs(args []string) (secretAddArgs, bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return secretAddArgs{}, false
	}
	a := secretAddArgs{name: args[0], by: "human"}
	rest := args[1:]
	haveService := false
	for i := 0; i < len(rest); i++ {
		if i+1 >= len(rest) {
			return secretAddArgs{}, false
		}
		flag, value := rest[i], rest[i+1]
		switch flag {
		case "--service":
			a.service = value
			haveService = true
		case "--hook":
			a.hook = value
		case "--rotate-after":
			a.rotateAfter = value
		case "--by":
			a.by = value
		default:
			return secretAddArgs{}, false
		}
		i++
	}
	if !haveService {
		return secretAddArgs{}, false
	}
	return a, true
}

// trimTrailingNewline drops exactly one trailing newline from b — a
// lone "\n" or a "\r\n" pair — and leaves everything else untouched.
// cmdSecretAdd calls this once on the whole of stdin, so a value
// piped with "printf %s" (no trailing newline) is stored byte-exact,
// and one piped with "echo" (one trailing newline) has that one
// newline removed rather than kept as part of the secret.
func trimTrailingNewline(b []byte) []byte {
	if bytes.HasSuffix(b, []byte("\r\n")) {
		return b[:len(b)-2]
	}
	if bytes.HasSuffix(b, []byte("\n")) {
		return b[:len(b)-1]
	}
	return b
}

// pipeStdinMessage is what cmdSecretAdd prints when stdin is a
// terminal: there is no safe way to read a secret value without
// echoing it, so the value must always arrive piped.
const pipeStdinMessage = `pipe the value on stdin, for example: printf %s "$VALUE" | loadout secret add <name> --service <svc>`

// cmdSecretAdd reads a secret's value from piped stdin and stores it,
// under the vault lock. It never accepts the value as a flag or
// argument, and never echoes it back.
func cmdSecretAdd(out, errOut io.Writer, args []string, m mode) int {
	parsed, ok := parseSecretAddArgs(args)
	if !ok {
		fmt.Fprintln(errOut, secretUsage)
		return 2
	}
	by, err := validateBy(parsed.by)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if strings.ContainsAny(parsed.service, "\n\r") || strings.ContainsAny(parsed.hook, "\n\r") {
		fmt.Fprintln(errOut, "--service/--hook: must be a single line. Fix: remove the newline.")
		return 2
	}
	if parsed.rotateAfter != "" {
		if _, err := time.ParseDuration(parsed.rotateAfter); err != nil {
			fmt.Fprintln(errOut, "--rotate-after: not a valid duration. Fix: use a duration like 720h.")
			return 2
		}
	}
	if stdinIsTTY() {
		io.WriteString(errOut, pipeStdinMessage+"\n")
		return 1
	}
	value, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	value = trimTrailingNewline(value)

	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	release, err := vault.Lock(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer release()
	if err := vault.AddSecret(v, parsed.name, parsed.service, parsed.hook, parsed.rotateAfter, by, value); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := vault.Snapshot(v, "add secret "+parsed.name); err != nil {
		vault.RemoveSecret(v, parsed.name)
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		printJSON(out, secretAddResult{Address: "secret/" + parsed.name})
		return 0
	}
	fmt.Fprintf(out, "added secret/%s\n", parsed.name)
	return 0
}

// secretAddResult is the JSON shape of "loadout secret add" — the
// address only, never a value field.
type secretAddResult struct {
	Address string `json:"address"`
}

// secretListItem is one entry in the JSON shape of "loadout secret
// list": metadata only, exactly what Secret carries — never a value
// field, since a Secret never holds one (INVARIANT 10).
type secretListItem struct {
	Name        string `json:"name"`
	Service     string `json:"service"`
	Hook        string `json:"hook"`
	RotateAfter string `json:"rotate_after"`
	By          string `json:"by"`
	At          string `json:"at"`
}

// secretListResult is the JSON shape of "loadout secret list".
type secretListResult struct {
	Secrets []secretListItem `json:"secrets"`
}

// cmdSecretList prints every secret's metadata, name order, never a
// value.
func cmdSecretList(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	secrets, err := vault.ListSecrets(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		items := make([]secretListItem, 0, len(secrets))
		for _, s := range secrets {
			items = append(items, secretListItem{
				Name:        s.Name,
				Service:     s.Service,
				Hook:        s.Hook,
				RotateAfter: s.RotateAfter,
				By:          s.By,
				At:          s.At,
			})
		}
		printJSON(out, secretListResult{Secrets: items})
		return 0
	}
	for _, s := range secrets {
		fmt.Fprintf(out, "secret/%s — %s (by %s, at %s)\n", s.Name, s.Service, s.By, s.At)
	}
	return 0
}

// refuseRevealMessage is what "loadout secret show <name>" prints,
// and the exit-1 error it returns, when --reveal is absent. INVARIANT
// 10 requires a secret value to appear only under an explicit
// --reveal: this is the default, safe path that prints nothing.
const refuseRevealMessage = "refusing to reveal a secret without --reveal. Fix: run loadout secret show <name> --reveal, or use loadout run to inject it."

// secretShowArgs is the parsed shape of "secret show <name> [--reveal]
// [--by <who>]".
type secretShowArgs struct {
	name   string
	reveal bool
	by     string
}

// parseSecretShowArgs reads secretShowArgs out of args. by defaults to
// "human" when --by is absent. ok is false when args does not match
// the expected shape.
func parseSecretShowArgs(args []string) (secretShowArgs, bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return secretShowArgs{}, false
	}
	a := secretShowArgs{name: args[0], by: "human"}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--reveal":
			a.reveal = true
		case "--by":
			if i+1 >= len(rest) {
				return secretShowArgs{}, false
			}
			a.by = rest[i+1]
			i++
		default:
			return secretShowArgs{}, false
		}
	}
	return a, true
}

// cmdSecretShow prints a secret's value, but ONLY under an explicit
// --reveal (INVARIANT 10). By default it prints nothing, writes no
// access-log entry, and exits 1. With --reveal, it prints exactly the
// value bytes to stdout, zeroes the plaintext, then appends one
// access-log entry. --reveal never combines with --json: a reveal is
// a raw-stdout operation, and JSON output must never carry the value.
func cmdSecretShow(out, errOut io.Writer, args []string, m mode) int {
	parsed, ok := parseSecretShowArgs(args)
	if !ok {
		fmt.Fprintln(errOut, secretUsage)
		return 2
	}
	if parsed.reveal && m == modeJSON {
		fmt.Fprintln(errOut, "--reveal cannot combine with --json. Fix: use one or the other.")
		return 2
	}
	if !parsed.reveal {
		fmt.Fprintln(errOut, refuseRevealMessage)
		return 1
	}
	by, err := validateBy(parsed.by)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	value, err := vault.DecryptSecret(v, parsed.name)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	out.Write(value)
	for i := range value {
		value[i] = 0
	}
	if err := AppendAccessLog(v, AccessEntry{
		At:     time.Now().UTC().Format(time.RFC3339),
		Verb:   "show",
		Secret: parsed.name,
		Tool:   by,
	}); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

// secretRemoveResult is the JSON shape of "loadout secret rm".
type secretRemoveResult struct {
	Name    string `json:"name"`
	Removed bool   `json:"removed"`
}

// cmdSecretRemove deletes a secret, under the vault lock, then
// snapshots the change.
func cmdSecretRemove(out, errOut io.Writer, args []string, m mode) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, secretUsage)
		return 2
	}
	name := args[0]
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	release, err := vault.Lock(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer release()
	if err := vault.RemoveSecret(v, name); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := vault.Snapshot(v, "remove secret "+name); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		printJSON(out, secretRemoveResult{Name: name, Removed: true})
		return 0
	}
	fmt.Fprintf(out, "removed secret/%s\n", name)
	return 0
}
