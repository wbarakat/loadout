package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"loadout.dev/loadout/internal/vault"
)

// runUsage is the usage line for "loadout run". "--" is mandatory: it
// is what separates loadout's own flags from the child command and
// its arguments.
const runUsage = `usage: loadout run --secret <name>[=ENVVAR] [--secret <name2>...] [--by <tool>] -- <cmd> [args...]`

// runJSONError is what "loadout run --json" prints: run is a
// transparent wrapper around a child process, so it has nothing of
// its own to marshal as JSON, matching cmdEdit's own refusal.
const runJSONError = "run has no json output. Fix: run run without --json."

// envVarPattern is what an explicit "--secret name=ENVVAR" form's
// ENVVAR half must match: a valid POSIX environment variable name.
var envVarPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// runSecretSpec is one parsed "--secret name[=ENVVAR]" flag. envVar
// is empty until resolveEnvVars fills it in with either the given
// name or one derived from the secret's own name.
type runSecretSpec struct {
	name   string
	envVar string
}

// runArgs is the parsed shape of "run --secret <name>[=ENVVAR]...
// [--by <tool>] -- <cmd> [args...]".
type runArgs struct {
	secrets []runSecretSpec
	by      string
	command []string
}

// parseRunArgs splits args on the mandatory "--" separator, then
// reads every --secret and the optional --by flag out of the part
// before it. ok is false when "--" is missing, no command follows it,
// no --secret is given, or a flag does not match the expected shape —
// the caller prints usage and exits 2 in every such case, so run
// never touches the vault on a malformed invocation.
func parseRunArgs(args []string) (runArgs, bool) {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep == -1 {
		return runArgs{}, false
	}
	command := args[sep+1:]
	if len(command) == 0 {
		return runArgs{}, false
	}

	var a runArgs
	flags := args[:sep]
	for i := 0; i < len(flags); i++ {
		if i+1 >= len(flags) {
			return runArgs{}, false
		}
		flag, value := flags[i], flags[i+1]
		i++
		switch flag {
		case "--secret":
			name, envVar, hasEnvVar := strings.Cut(value, "=")
			if name == "" {
				return runArgs{}, false
			}
			spec := runSecretSpec{name: name}
			if hasEnvVar {
				spec.envVar = envVar
			}
			a.secrets = append(a.secrets, spec)
		case "--by":
			a.by = value
		default:
			return runArgs{}, false
		}
	}
	if len(a.secrets) == 0 {
		return runArgs{}, false
	}
	a.command = command
	return a, true
}

// deriveEnvName turns a kebab-case secret name into an upper-snake
// environment variable name, for example "openai-key" becomes
// "OPENAI_KEY".
func deriveEnvName(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// resolveEnvVars fills in envVar on every spec: the explicit ENVVAR
// half of "name=ENVVAR" when given, checked against envVarPattern, or
// else deriveEnvName's own reading of the secret's name. It returns
// an error naming the bad ENVVAR when one fails that check, so the
// caller can report a usage error before any secret is decrypted.
func resolveEnvVars(secrets []runSecretSpec) error {
	for i, spec := range secrets {
		if spec.envVar == "" {
			secrets[i].envVar = deriveEnvName(spec.name)
			continue
		}
		if !envVarPattern.MatchString(spec.envVar) {
			return fmt.Errorf("%s: not a valid environment variable name. Fix: use a name matching ^[A-Z_][A-Z0-9_]*$.", spec.envVar)
		}
	}
	return nil
}

// cmdRun decrypts every named secret and injects it into a child
// process's environment, then execs the command with loadout's own
// stdio (transparent wrapper). loadout's own exit code is the
// child's exit code.
//
// INVARIANT 10: a decrypted value goes into the child's environment
// and nowhere else — never loadout's own stdout or stderr, never a
// temp file, never the access log, never an error message. Every
// secret is decrypted before any is used, so a bad --secret name
// fails before the child ever starts, and the child's own stdio is
// wired up only after every value is ready.
//
// Residual exposure, accepted and documented: once a value is copied
// into a "KEY=VALUE" string for exec.Cmd.Env, that string cannot be
// zeroed — Go strings are immutable — so it lives on in the child's
// environment for as long as the child runs, which is the entire
// point of loadout run. Only the []byte DecryptSecret returns is
// zeroed, immediately after cmdRun copies it into that string.
//
// Access-log entries: cmdRun appends one entry per secret, verb
// "run", only once every secret has decrypted successfully and
// loadout is about to exec — never one at a time as each secret
// decrypts. If any secret fails to decrypt, nothing is logged and the
// child never starts: the run never happened, so there is nothing to
// log. If every secret decrypts but the child then fails to START
// (for example, command not found), the entries are still written,
// since the secrets really were decrypted into this process's memory
// — that use already happened regardless of what the child did next.
func cmdRun(out, errOut io.Writer, args []string, m mode) int {
	if m == modeJSON {
		fmt.Fprintln(errOut, runJSONError)
		return 2
	}
	parsed, ok := parseRunArgs(args)
	if !ok {
		fmt.Fprintln(errOut, runUsage)
		return 2
	}
	if err := resolveEnvVars(parsed.secrets); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	by := ""
	if parsed.by != "" {
		validated, err := validateBy(parsed.by)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		by = validated
	}

	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	// Decrypt every secret before touching the child at all: one bad
	// name must fail the whole run, with the child never started and
	// nothing logged for the secrets that did decrypt.
	env := os.Environ()
	for _, spec := range parsed.secrets {
		value, err := vault.DecryptSecret(v, spec.name)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		env = append(env, spec.envVar+"="+string(value))
		for i := range value {
			value[i] = 0
		}
	}

	tool := by
	if tool == "" {
		tool = filepath.Base(parsed.command[0])
	}
	at := time.Now().UTC().Format(time.RFC3339)
	for _, spec := range parsed.secrets {
		if err := AppendAccessLog(v, AccessEntry{
			At:     at,
			Verb:   "run",
			Secret: spec.name,
			Tool:   tool,
		}); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}

	cmd := exec.Command(parsed.command[0], parsed.command[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	if runErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode()
	}
	fmt.Fprintf(errOut, "%s: the command did not start: %v\n", parsed.command[0], runErr)
	return 1
}
