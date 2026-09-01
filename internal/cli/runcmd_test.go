package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCapturingChildStdout is like run, but swaps os.Stdout for a pipe
// so the caller can read exactly what the CHILD process wrote there —
// proving injection reached the child — while still capturing
// loadout's own combined stdout/stderr through the normal run
// buffers. cmdRun wires cmd.Stdout to os.Stdout directly (transparent
// wrapper), so this is the only way a test can see the child's own
// output separately from whatever loadout might have added.
func runCapturingChildStdout(t *testing.T, args ...string) (childStdout, loadoutOut, loadoutErr string, code int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 4096)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()

	loadoutOut, loadoutErr, code = run(t, args...)

	os.Stdout = origStdout
	w.Close()
	childStdout = <-done
	r.Close()
	return
}

// TestRunInjectsSecretIntoChildEnvOnly proves the centerpiece
// invariant: a secret named on "loadout run --secret" reaches the
// CHILD process's environment (captured from the child's own stdout,
// which cmdRun wires directly to os.Stdout) while loadout's own
// output never carries the value at all.
func TestRunInjectsSecretIntoChildEnvOnly(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	childOut, loadoutOut, loadoutErr, code := runCapturingChildStdout(t,
		"run", "--secret", "test-key=FOO", "--", "sh", "-c", `printf %s "$FOO"`)
	if code != 0 {
		t.Fatalf("run failed: %s", loadoutErr)
	}
	if childOut != dummySecretValue {
		t.Fatalf("child stdout = %q, want %q (proves injection into the child)", childOut, dummySecretValue)
	}
	if strings.Contains(loadoutOut, dummySecretValue) {
		t.Fatalf("loadout's own stdout must never carry the value, got %q", loadoutOut)
	}
	if strings.Contains(loadoutErr, dummySecretValue) {
		t.Fatalf("loadout's own stderr must never carry the value, got %q", loadoutErr)
	}

	logData, err := os.ReadFile(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), dummySecretValue) {
		t.Fatalf("the access log must never carry the value, got %q", logData)
	}
	lines := strings.Split(strings.TrimRight(string(logData), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 access-log line, got %d: %q", len(lines), logData)
	}
	var entry struct {
		At     string `json:"at"`
		Verb   string `json:"verb"`
		Secret string `json:"secret"`
		Tool   string `json:"tool"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("access-log line did not parse: %v", err)
	}
	if entry.Verb != "run" || entry.Secret != "test-key" || entry.Tool != "sh" || entry.At == "" {
		t.Fatalf("bad access-log entry: %+v", entry)
	}
}

// TestRunSilentChildLeavesNoTraceInLoadoutOutput proves the negative
// case an echoing child cannot: when the child does NOT print the
// secret (here, "sh -c true" prints nothing at all), loadout's own
// stdout and stderr carry no trace of the value either — there is
// nowhere else the value could have leaked into.
func TestRunSilentChildLeavesNoTraceInLoadoutOutput(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	childOut, loadoutOut, loadoutErr, code := runCapturingChildStdout(t,
		"run", "--secret", "test-key=FOO", "--", "sh", "-c", "true")
	if code != 0 {
		t.Fatalf("run failed: %s", loadoutErr)
	}
	if childOut != "" {
		t.Fatalf("the silent child must print nothing, got %q", childOut)
	}
	if strings.Contains(loadoutOut, dummySecretValue) || loadoutOut != "" {
		t.Fatalf("loadout's own stdout must print nothing at all, got %q", loadoutOut)
	}
	if strings.Contains(loadoutErr, dummySecretValue) {
		t.Fatalf("loadout's own stderr must never carry the value, got %q", loadoutErr)
	}
}

// TestRunPropagatesChildExitCode proves loadout's own exit code is
// exactly the child's exit code, not a fixed success/failure value.
func TestRunPropagatesChildExitCode(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, _, code := run(t, "run", "--secret", "test-key=FOO", "--", "sh", "-c", "exit 7")
	if code != 7 {
		t.Fatalf("want exit 7 propagated from the child, got %d", code)
	}
}

// TestRunMissingSecretExitsOneAndNeverStartsChild proves a bad
// --secret name fails before the child is ever started: the child
// would create a sentinel file if it ran, and that file must not
// exist.
func TestRunMissingSecretExitsOneAndNeverStartsChild(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")

	sentinel := filepath.Join(base, "sentinel")
	_, errOut, code := run(t, "run", "--secret", "no-such-key=FOO", "--", "sh", "-c", "touch "+sentinel)
	if code != 1 {
		t.Fatalf("want exit 1, got %d (%s)", code, errOut)
	}
	if strings.Contains(errOut, dummySecretValue) {
		t.Fatalf("the error must never hold a value, got %q", errOut)
	}
	if !strings.Contains(errOut, "no such secret") {
		t.Fatalf("bad error: %q", errOut)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("the child must never run when a secret is missing")
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "access.log")); !os.IsNotExist(err) {
		t.Fatal("a run that never happened must never write the access log")
	}
}

// TestRunDefaultEnvNameDerivation proves the default env-var name
// derivation: "openai-key" becomes "OPENAI_KEY" when no "=ENVVAR" is
// given.
func TestRunDefaultEnvNameDerivation(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "svc")

	childOut, _, loadoutErr, code := runCapturingChildStdout(t,
		"run", "--secret", "openai-key", "--", "sh", "-c", `printf %s "$OPENAI_KEY"`)
	if code != 0 {
		t.Fatalf("run failed: %s", loadoutErr)
	}
	if childOut != dummySecretValue {
		t.Fatalf("child stdout = %q, want %q", childOut, dummySecretValue)
	}
}

// TestRunMissingSeparatorIsUsageError proves "--" is mandatory: with
// no "--" to mark where the child command starts, run must refuse
// with a usage error, exit 2, rather than guess.
func TestRunMissingSeparatorIsUsageError(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, errOut, code := run(t, "run", "--secret", "test-key=FOO", "sh", "-c", "true")
	if code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("missing -- must be a usage error, got %d %q", code, errOut)
	}
}

// TestRunNoCommandAfterSeparatorIsUsageError proves an empty command
// after "--" is also a usage error, not a silent no-op.
func TestRunNoCommandAfterSeparatorIsUsageError(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, errOut, code := run(t, "run", "--secret", "test-key=FOO", "--")
	if code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("no command after -- must be a usage error, got %d %q", code, errOut)
	}
}

// TestRunRequiresAtLeastOneSecret proves "loadout run" with no
// --secret at all is a usage error: run's whole purpose is injection.
func TestRunRequiresAtLeastOneSecret(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	_, errOut, code := run(t, "run", "--", "sh", "-c", "true")
	if code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("no --secret must be a usage error, got %d %q", code, errOut)
	}
}

// TestRunRejectsInvalidEnvVarName proves an explicit "=ENVVAR" half
// that is not a valid environment variable name is a usage error,
// caught before any secret is decrypted.
func TestRunRejectsInvalidEnvVarName(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, errOut, code := run(t, "run", "--secret", "test-key=not-valid", "--", "sh", "-c", "true")
	if code != 2 || !strings.Contains(errOut, "not a valid environment variable name") {
		t.Fatalf("an invalid ENVVAR must be a usage error naming the problem, got %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "access.log")); !os.IsNotExist(err) {
		t.Fatal("a usage error must never write the access log")
	}
}

// TestRunByFlagNamesTheToolInTheAccessLog proves --by overrides the
// default child-argv0-basename tool name in the access-log entry.
func TestRunByFlagNamesTheToolInTheAccessLog(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, errOut, code := run(t, "run", "--secret", "test-key=FOO", "--by", "claude-code", "--", "sh", "-c", "true")
	if code != 0 {
		t.Fatalf("run failed: %s", errOut)
	}
	logData, err := os.ReadFile(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), `"tool":"claude-code"`) {
		t.Fatalf("access log must record the --by tool, got %s", logData)
	}
}

// TestRunMultipleSecretsEachGetOwnAccessLogEntry proves several
// --secret flags each inject their own env var and each get their own
// access-log entry.
func TestRunMultipleSecretsEachGetOwnAccessLogEntry(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")
	runWithStdin(t, dummySecretValue+"-2", "secret", "add", "other-key", "--service", "svc")

	childOut, _, loadoutErr, code := runCapturingChildStdout(t,
		"run", "--secret", "test-key=FOO", "--secret", "other-key=BAR", "--",
		"sh", "-c", `printf %s,%s "$FOO" "$BAR"`)
	if code != 0 {
		t.Fatalf("run failed: %s", loadoutErr)
	}
	want := dummySecretValue + "," + dummySecretValue + "-2"
	if childOut != want {
		t.Fatalf("child stdout = %q, want %q", childOut, want)
	}

	logData, err := os.ReadFile(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(logData), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 access-log lines, one per secret, got %d: %q", len(lines), logData)
	}
}

// TestRunChildInheritsParentEnv proves the child's environment is the
// parent's environment plus the injected secrets, not a stripped-down
// environment.
func TestRunChildInheritsParentEnv(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")
	t.Setenv("LOADOUT_TEST_MARKER", "present")

	childOut, _, loadoutErr, code := runCapturingChildStdout(t,
		"run", "--secret", "test-key=FOO", "--", "sh", "-c", `printf %s "$LOADOUT_TEST_MARKER"`)
	if code != 0 {
		t.Fatalf("run failed: %s", loadoutErr)
	}
	if childOut != "present" {
		t.Fatalf("the child must inherit the parent's own env, got %q", childOut)
	}
}

// TestRunCommandNotFoundExitsOneWithFixedMessage proves a child that
// fails to START (as opposed to one that starts and exits nonzero)
// is reported as a grammar error, exit 1, distinct from exit-code
// passthrough.
func TestRunCommandNotFoundExitsOneWithFixedMessage(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, errOut, code := run(t, "run", "--secret", "test-key=FOO", "--", "/no/such/binary-at-all")
	if code != 1 {
		t.Fatalf("want exit 1, got %d (%s)", code, errOut)
	}
	if strings.Contains(errOut, dummySecretValue) {
		t.Fatalf("the error must never hold the value, got %q", errOut)
	}
	if !strings.Contains(errOut, "did not start") {
		t.Fatalf("bad error: %q", errOut)
	}
}

// TestRunRefusesPathTraversalSecretName proves "loadout run" closes
// the path-traversal BLOCKER: a "--secret" name carrying ".."
// components is refused as a usage error BEFORE the vault is even
// opened, the child is never started, and the directory the name
// would otherwise resolve to (outside the whole vault) survives
// untouched.
func TestRunRefusesPathTraversalSecretName(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	sentinel := plantSentinel(t, base)

	childSentinel := filepath.Join(base, "child-ran")
	_, errOut, code := run(t, "run", "--secret", hostileSecretName+"=FOO", "--", "sh", "-c", "touch "+childSentinel)
	if code != 2 {
		t.Fatalf("want exit 2, got %d (%s)", code, errOut)
	}
	if !strings.Contains(errOut, "not a valid secret name") {
		t.Fatalf("bad error: %q", errOut)
	}
	if _, err := os.Stat(childSentinel); !os.IsNotExist(err) {
		t.Fatal("the child must never run when the secret name is invalid")
	}
	assertSentinelSurvives(t, sentinel)
}

// TestRunSecretEqualsEmptyEnvVarIsUsageError proves an explicit but
// empty "--secret name=" is a usage error distinct from omitting "="
// entirely: the caller asked for a specific env var name and gave
// none, so run must not silently fall back to the derived default.
func TestRunSecretEqualsEmptyEnvVarIsUsageError(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, errOut, code := run(t, "run", "--secret", "test-key=", "--", "sh", "-c", "true")
	if code != 2 {
		t.Fatalf("want exit 2, got %d (%s)", code, errOut)
	}
	if !strings.Contains(errOut, "the env var name after = is empty") {
		t.Fatalf("bad error: %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "access.log")); !os.IsNotExist(err) {
		t.Fatal("a usage error must never write the access log")
	}
}

// TestRunJSONIsAPlainError pins the exact "run --json" refusal text.
func TestRunJSONIsAPlainError(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := run(t, "run", "--json", "--secret", "test-key=FOO", "--", "true")
	if code != 2 {
		t.Fatalf("run --json must exit 2, got %d", code)
	}
	if out != "" {
		t.Fatalf("run --json must print nothing to stdout, got %q", out)
	}
	want := "run has no json output. Fix: run the command without --json.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}

// TestRunJSONFlagIsRejected proves --json before "--" is refused, and
// TestRunJSONAfterSeparatorReachesTheChild proves the same token
// after "--" is left alone: it belongs to the child, not to loadout.
func TestRunJSONFlagIsRejected(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, errOut, code := run(t, "run", "--json", "--secret", "test-key=FOO", "--", "sh", "-c", "true")
	if code != 2 {
		t.Fatalf("want exit 2, got %d (%s)", code, errOut)
	}
}

func TestRunJSONAfterSeparatorReachesTheChild(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	childOut, _, loadoutErr, code := runCapturingChildStdout(t,
		"run", "--secret", "test-key=FOO", "--", "sh", "-c", `printf %s "$1"`, "_", "--json")
	if code != 0 {
		t.Fatalf("run failed: %s", loadoutErr)
	}
	if childOut != "--json" {
		t.Fatalf("a --json after -- must reach the child untouched, got %q", childOut)
	}
}
