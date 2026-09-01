package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// dummySecretValue is the only secret value any test in this file
// ever uses. It is not a real credential.
const dummySecretValue = "test-secret-value-123"

// runWithStdin runs the CLI with os.Stdin replaced by a pipe carrying
// stdin, restoring the real os.Stdin once the call returns. cmdSecret
// reads a secret's value only off piped stdin (never a flag or
// argument), so every "secret add" test needs this.
func runWithStdin(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	go func() {
		io.WriteString(w, stdin)
		w.Close()
	}()
	return run(t, args...)
}

// TestSecretAddViaStdinPipeStoresValue proves the value arrives only
// through piped stdin, and lands on disk only as value.age ciphertext
// — never in meta.md, byte for byte.
func TestSecretAddViaStdinPipeStoresValue(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")

	out, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc", "--hook", "a hook")
	if code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}
	if !strings.Contains(out, "added secret/test-key") {
		t.Fatalf("bad output: %q", out)
	}

	dir := filepath.Join(base, "vault", "secrets", "test-key")
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.md"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metaData, []byte(dummySecretValue)) {
		t.Fatalf("meta.md must never contain the value, got:\n%s", metaData)
	}
	if !strings.Contains(string(metaData), "service: svc") || !strings.Contains(string(metaData), "hook: a hook") {
		t.Fatalf("bad metadata: %s", metaData)
	}

	valueData, err := os.ReadFile(filepath.Join(dir, "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(valueData, []byte(dummySecretValue)) {
		t.Fatal("value.age must hold ciphertext only, never the plaintext")
	}
}

// TestSecretAddTrimsSingleTrailingNewline proves a value piped with a
// trailing newline (as "echo" would send) is stored without that one
// newline, and round-trips exactly through show --reveal.
func TestSecretAddTrimsSingleTrailingNewline(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue+"\n", "secret", "add", "test-key", "--service", "svc")

	out, errOut, code := run(t, "secret", "show", "test-key", "--reveal")
	if code != 0 {
		t.Fatalf("secret show --reveal failed: %s", errOut)
	}
	if out != dummySecretValue {
		t.Fatalf("stdout = %q, want %q", out, dummySecretValue)
	}
}

// TestSecretAddRequiresService proves --service is mandatory: a usage
// error, not a silently empty service field.
func TestSecretAddRequiresService(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "test-key")
	if code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("want a usage error, got %d %q", code, errOut)
	}
}

// TestSecretAddRejectsNewlineInServiceOrHook pins the deferred
// Task-1 frontmatter-injection minor: a --service or --hook value
// holding a newline or carriage return must be refused, not written.
func TestSecretAddRejectsNewlineInServiceOrHook(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	_, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc\nname: injected")
	if code != 2 {
		t.Fatalf("want exit 2, got %d (%s)", code, errOut)
	}
	if !strings.Contains(errOut, "must be a single line") {
		t.Fatalf("bad error: %q", errOut)
	}

	_, errOut, code = runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc", "--hook", "line1\rline2")
	if code != 2 {
		t.Fatalf("want exit 2, got %d (%s)", code, errOut)
	}
	if !strings.Contains(errOut, "must be a single line") {
		t.Fatalf("bad error: %q", errOut)
	}
}

// TestSecretAddRejectsBadRotateAfter proves --rotate-after must parse
// as a Go duration.
func TestSecretAddRejectsBadRotateAfter(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc", "--rotate-after", "not-a-duration")
	if code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "not a valid duration") {
		t.Fatalf("bad error: %q", errOut)
	}
}

// TestSecretAddRotateAfterAndByStored proves both --rotate-after and
// --by land in meta.md, matching "add memory --by".
func TestSecretAddRotateAfterAndByStored(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	_, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc", "--rotate-after", "720h", "--by", "claude-code")
	if code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}
	data, err := os.ReadFile(filepath.Join(base, "vault", "secrets", "test-key", "meta.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "rotate_after: 720h") {
		t.Fatalf("bad rotate_after: %s", text)
	}
	if !strings.Contains(text, "by: claude-code") {
		t.Fatalf("bad by: %s", text)
	}
}

// TestSecretShowWithoutRevealPrintsNothingAndDoesNotLog proves the
// default, safe path: no --reveal means no stdout output and no
// access-log entry, ever (INVARIANT 10's "never by default" rule).
func TestSecretShowWithoutRevealPrintsNothingAndDoesNotLog(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	out, errOut, code := run(t, "secret", "show", "test-key")
	if code != 1 {
		t.Fatalf("want exit 1, got %d (%s)", code, errOut)
	}
	if out != "" {
		t.Fatalf("must print nothing to stdout, got %q", out)
	}
	want := "refusing to reveal a secret without --reveal. Fix: run loadout secret show <name> --reveal, or use loadout run to inject it.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "access.log")); !os.IsNotExist(err) {
		t.Fatalf("a non-revealing show must never write the access log, got err=%v", err)
	}
}

// TestSecretShowRevealPrintsExactValueAndLogsOnce proves --reveal
// prints exactly the value with no label or trailing newline it did
// not already have, and appends exactly one access-log entry that
// names the secret and verb but never the value.
func TestSecretShowRevealPrintsExactValueAndLogsOnce(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	out, errOut, code := run(t, "secret", "show", "test-key", "--reveal")
	if code != 0 {
		t.Fatalf("secret show --reveal failed: %s", errOut)
	}
	if out != dummySecretValue {
		t.Fatalf("stdout = %q, want exactly %q", out, dummySecretValue)
	}

	logData, err := os.ReadFile(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(logData), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 access-log line, got %d: %q", len(lines), logData)
	}
	if strings.Contains(lines[0], dummySecretValue) {
		t.Fatalf("the access-log line must never hold the value, got %q", lines[0])
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
	if entry.Verb != "show" || entry.Secret != "test-key" || entry.Tool != "human" || entry.At == "" {
		t.Fatalf("bad access-log entry: %+v", entry)
	}
}

// TestSecretShowRevealAcceptsByFlag proves --by names the tool in the
// access-log entry, matching "add memory --by".
func TestSecretShowRevealAcceptsByFlag(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	_, errOut, code := run(t, "secret", "show", "test-key", "--reveal", "--by", "pi")
	if code != 0 {
		t.Fatalf("secret show --reveal failed: %s", errOut)
	}
	logData, err := os.ReadFile(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), `"tool":"pi"`) {
		t.Fatalf("access log must record the --by tool, got %s", logData)
	}
}

// TestSecretShowRevealJSONErrors proves --reveal never combines with
// --json: a reveal is a raw-stdout operation, and JSON output must
// never carry the value.
func TestSecretShowRevealJSONErrors(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	out, errOut, code := run(t, "secret", "show", "test-key", "--reveal", "--json")
	if code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
	if out != "" {
		t.Fatalf("must print nothing, got %q", out)
	}
	if !strings.Contains(errOut, "cannot combine") {
		t.Fatalf("bad error: %q", errOut)
	}
}

// TestSecretListJSONHasNoValueField proves the JSON shape of "secret
// list" carries metadata only: no field ever holds the value, and the
// value never appears anywhere in the raw output.
func TestSecretListJSONHasNoValueField(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc", "--hook", "a hook")

	out, errOut, code := run(t, "secret", "list", "--json")
	if code != 0 {
		t.Fatalf("secret list --json failed: %s", errOut)
	}
	if strings.Contains(out, dummySecretValue) {
		t.Fatalf("secret list --json must never hold the value, got %q", out)
	}
	var got struct {
		Secrets []map[string]any `json:"secrets"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(got.Secrets) != 1 {
		t.Fatalf("want 1 secret, got %+v", got)
	}
	entry := got.Secrets[0]
	if _, has := entry["value"]; has {
		t.Fatalf("a value field must never be present, got %+v", entry)
	}
	if entry["name"] != "test-key" || entry["service"] != "svc" || entry["hook"] != "a hook" {
		t.Fatalf("bad entry: %+v", entry)
	}
}

// TestSecretListTextNeverHoldsValue is the text-mode counterpart of
// the JSON test above.
func TestSecretListTextNeverHoldsValue(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	out, errOut, code := run(t, "secret", "list")
	if code != 0 {
		t.Fatalf("secret list failed: %s", errOut)
	}
	if strings.Contains(out, dummySecretValue) {
		t.Fatalf("secret list must never hold the value, got %q", out)
	}
	if !strings.Contains(out, "secret/test-key") {
		t.Fatalf("bad output: %q", out)
	}
}

// TestSecretShowErrorsNeverContainTheValue proves INVARIANT 10 for
// both failure modes DecryptSecret defines: a name that does not
// exist, and a value.age this device cannot decrypt (a secret
// encrypted only to some other device's identity).
func TestSecretShowErrorsNeverContainTheValue(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")

	_, errOut, code := run(t, "secret", "show", "no-such-key", "--reveal")
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if strings.Contains(errOut, dummySecretValue) {
		t.Fatalf("a missing-secret error must never hold the value, got %q", errOut)
	}
	if !strings.Contains(errOut, "no such secret") {
		t.Fatalf("bad error: %q", errOut)
	}

	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, other.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	valuePath := filepath.Join(base, "vault", "secrets", "test-key", "value.age")
	if err := os.WriteFile(valuePath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errOut, code = run(t, "secret", "show", "test-key", "--reveal")
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if strings.Contains(errOut, dummySecretValue) {
		t.Fatalf("an undecryptable-secret error must never hold the value, got %q", errOut)
	}
	if !strings.Contains(errOut, "this device cannot read the secret") {
		t.Fatalf("bad error: %q", errOut)
	}
}

// hostileSecretName is a path-traversal name three levels deep,
// matching the depth of the proven-live exploit: "secret rm" on this
// name used to delete a directory OUTSIDE the whole vault, not just
// outside secrets/.
const hostileSecretName = "../../../outside-vault-target"

// plantSentinel creates a directory at exactly the path
// filepath.Join(<vault>/secrets, hostileSecretName) resolves to once
// its ".." components are cleaned — the path a hostile secret name
// would destroy, read, or overwrite if the CLI layer forwarded it to
// the vault unvalidated. The directory carries a meta.md AND a
// value.age, the shape vault.SecretExists treats as a real secret —
// the exact condition that would let an unvalidated name past the "no
// such secret" guard and on to the destructive call — plus a third
// sentinel file with no special meaning to any secret verb, so its
// survival proves the directory was never touched. It returns the
// sentinel file's path so the caller can assert that.
func plantSentinel(t *testing.T, base string) string {
	t.Helper()
	dir := filepath.Join(base, "vault", "secrets", hostileSecretName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.md"), []byte("---\nname: outside-vault-target\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "value.age"), []byte("bogus-ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "sentinel.txt")
	if err := os.WriteFile(file, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// assertSentinelSurvives re-reads the sentinel file, and its meta.md
// and value.age siblings, and fails the test unless every one of them
// is exactly as plantSentinel left it.
func assertSentinelSurvives(t *testing.T, file string) {
	t.Helper()
	dir := filepath.Dir(file)
	for _, f := range []string{"meta.md", "value.age", "sentinel.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s outside secrets/ must survive, got err=%v", f, err)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the sentinel outside secrets/ must survive, got err=%v", err)
	}
	if string(data) != "do not delete" {
		t.Fatal("the sentinel's content must be untouched")
	}
}

// TestSecretRmRefusesPathTraversalName proves the CLI layer closes the
// path-traversal BLOCKER too: "secret rm" on a hostile name is
// refused, and the directory that name would otherwise resolve to,
// outside the whole vault, survives untouched.
func TestSecretRmRefusesPathTraversalName(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	sentinel := plantSentinel(t, base)

	_, errOut, code := run(t, "secret", "rm", hostileSecretName)
	if code == 0 {
		t.Fatalf("secret rm must refuse a path-traversal name, got exit 0 (%s)", errOut)
	}
	if !strings.Contains(errOut, "not a valid secret name") {
		t.Fatalf("bad error: %q", errOut)
	}
	assertSentinelSurvives(t, sentinel)
}

// TestSecretShowRevealRefusesPathTraversalName proves "secret show
// --reveal" on a hostile name is refused before any read is even
// attempted at the resolved path, and never prints anything.
func TestSecretShowRevealRefusesPathTraversalName(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	sentinel := plantSentinel(t, base)

	out, errOut, code := run(t, "secret", "show", hostileSecretName, "--reveal")
	if code == 0 {
		t.Fatalf("secret show --reveal must refuse a path-traversal name, got exit 0")
	}
	if out != "" {
		t.Fatalf("must print nothing to stdout, got %q", out)
	}
	if !strings.Contains(errOut, "not a valid secret name") {
		t.Fatalf("bad error: %q", errOut)
	}
	assertSentinelSurvives(t, sentinel)
}

// TestSecretRotateRefusesPathTraversalName proves "secret rotate" on a
// hostile name is refused before the piped stdin value is ever used
// to overwrite anything at the resolved path.
func TestSecretRotateRefusesPathTraversalName(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	sentinel := plantSentinel(t, base)

	_, errOut, code := runWithStdin(t, dummySecretValue, "secret", "rotate", hostileSecretName)
	if code == 0 {
		t.Fatalf("secret rotate must refuse a path-traversal name, got exit 0")
	}
	if !strings.Contains(errOut, "not a valid secret name") {
		t.Fatalf("bad error: %q", errOut)
	}
	assertSentinelSurvives(t, sentinel)
}

// TestSecretRmRemovesSecret proves "secret rm" deletes the whole
// secret directory and reports it in a message that never holds the
// value (there is nothing to hold: rm never decrypts).
func TestSecretRmRemovesSecret(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	out, errOut, code := run(t, "secret", "rm", "test-key")
	if code != 0 {
		t.Fatalf("secret rm failed: %s", errOut)
	}
	if !strings.Contains(out, "removed secret/test-key") {
		t.Fatalf("bad output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "secrets", "test-key")); !os.IsNotExist(err) {
		t.Fatal("the secret directory must be removed")
	}
}

// TestSecretRmMissingRefused proves rm refuses a name that does not
// exist, rather than silently succeeding.
func TestSecretRmMissingRefused(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := run(t, "secret", "rm", "no-such-key")
	if code != 1 {
		t.Fatalf("want exit 1, got %d (%s)", code, errOut)
	}
}

// TestSecretRotateReplacesValueAndUpdatesAt proves the headline
// mechanism: rotate replaces a secret's value (round-tripping through
// show --reveal), keeps the metadata the secret was added with, and
// prints only a fixed confirmation line, never the value.
func TestSecretRotateReplacesValueAndUpdatesAt(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc", "--hook", "a hook", "--rotate-after", "24h")

	const newValue = "rotated-secret-value-999"
	out, errOut, code := runWithStdin(t, newValue, "secret", "rotate", "test-key")
	if code != 0 {
		t.Fatalf("secret rotate failed: %s", errOut)
	}
	if out != "rotated secret/test-key\n" {
		t.Fatalf("bad output: %q", out)
	}
	if strings.Contains(out, newValue) || strings.Contains(errOut, newValue) {
		t.Fatal("secret rotate must never echo the value")
	}

	showOut, showErr, showCode := run(t, "secret", "show", "test-key", "--reveal")
	if showCode != 0 {
		t.Fatalf("secret show --reveal failed: %s", showErr)
	}
	if showOut != newValue {
		t.Fatalf("stdout = %q, want the rotated value %q", showOut, newValue)
	}

	metaData, err := os.ReadFile(filepath.Join(base, "vault", "secrets", "test-key", "meta.md"))
	if err != nil {
		t.Fatal(err)
	}
	meta := string(metaData)
	for _, want := range []string{"service: svc", "hook: a hook", "rotate_after: 24h"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("rotate must keep %q, got:\n%s", want, meta)
		}
	}
	if bytes.Contains(metaData, []byte(newValue)) {
		t.Fatalf("meta.md must never contain the value, got:\n%s", metaData)
	}

	valueData, err := os.ReadFile(filepath.Join(base, "vault", "secrets", "test-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(valueData, []byte(newValue)) || bytes.Contains(valueData, []byte(dummySecretValue)) {
		t.Fatal("value.age must hold ciphertext only, never a plaintext value")
	}
}

// TestSecretRotateRefusesNonexistentSecret proves rotate replaces a
// value, it does not create one: a name that was never added is
// refused with the standard grammar, and the pipe is never even
// consumed into a new secret.
func TestSecretRotateRefusesNonexistentSecret(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")

	out, errOut, code := runWithStdin(t, dummySecretValue, "secret", "rotate", "no-such-key")
	if code != 1 {
		t.Fatalf("want exit 1, got %d (%s)", code, errOut)
	}
	if out != "" {
		t.Fatalf("must print nothing to stdout, got %q", out)
	}
	if !strings.Contains(errOut, "no such secret") {
		t.Fatalf("bad error: %q", errOut)
	}
	if strings.Contains(errOut, dummySecretValue) {
		t.Fatalf("the error must never hold a value, got %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "secrets", "no-such-key")); !os.IsNotExist(err) {
		t.Fatal("rotate must never create a secret")
	}
}

// TestSecretRotateAppendsAccessLogEntryNoValue proves rotate appends
// exactly one access-log entry, verb "rotate", naming the secret but
// never its value.
func TestSecretRotateAppendsAccessLogEntryNoValue(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	const newValue = "rotated-secret-value-321"
	_, errOut, code := runWithStdin(t, newValue, "secret", "rotate", "test-key", "--by", "pi")
	if code != 0 {
		t.Fatalf("secret rotate failed: %s", errOut)
	}

	logData, err := os.ReadFile(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), newValue) || strings.Contains(string(logData), dummySecretValue) {
		t.Fatalf("the access log must never hold a value, got %q", logData)
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
	if entry.Verb != "rotate" || entry.Secret != "test-key" || entry.Tool != "pi" || entry.At == "" {
		t.Fatalf("bad access-log entry: %+v", entry)
	}
}

// TestSecretRotateJSON proves rotate's JSON shape carries the name
// only, never a value field.
func TestSecretRotateJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	go func() {
		io.WriteString(w, "rotated-secret-value-json")
		w.Close()
	}()
	out, errOut, code := run(t, "secret", "rotate", "test-key", "--json")
	os.Stdin = origStdin
	r.Close()
	if code != 0 {
		t.Fatalf("secret rotate --json failed: %s", errOut)
	}
	if strings.Contains(out, "rotated-secret-value-json") {
		t.Fatalf("secret rotate --json must never hold the value, got %q", out)
	}
	var got struct {
		Name    string `json:"name"`
		Rotated bool   `json:"rotated"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got.Name != "test-key" || !got.Rotated {
		t.Fatalf("bad json: %+v", got)
	}
}

// TestSecretAccessLogGitignoredSnapshotTracksNothing proves the
// device-local access log a "show --reveal" writes never enters
// history: a later snapshot-causing command tracks nothing new for
// it.
func TestSecretAccessLogGitignoredSnapshotTracksNothing(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	runWithStdin(t, dummySecretValue, "secret", "add", "test-key", "--service", "svc")
	run(t, "secret", "show", "test-key", "--reveal")

	// A later snapshot-causing command must never pick up access.log.
	run(t, "add", "memory", "y")

	out, err := exec.Command("git", "-C", filepath.Join(base, "vault"), "ls-files", "access.log").CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-files failed: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("access.log must never be tracked, got %q", out)
	}
}
