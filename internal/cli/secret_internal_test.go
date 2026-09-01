package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestParseSecretAddArgsRequiresService(t *testing.T) {
	if _, ok := parseSecretAddArgs([]string{"my-key"}); ok {
		t.Fatal("must require --service")
	}
	if _, ok := parseSecretAddArgs(nil); ok {
		t.Fatal("must require a name")
	}
}

func TestParseSecretAddArgsFullShape(t *testing.T) {
	a, ok := parseSecretAddArgs([]string{"my-key", "--service", "svc", "--hook", "a hook", "--rotate-after", "24h", "--by", "pi"})
	if !ok {
		t.Fatal("must parse")
	}
	want := secretAddArgs{name: "my-key", service: "svc", hook: "a hook", rotateAfter: "24h", by: "pi"}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("got %+v, want %+v", a, want)
	}
}

// TestParseSecretAddArgsAllowedHosts proves --allowed-hosts splits on
// comma, trims spaces, and drops empty entries; absent, it parses as
// nil — no host permitted, the fail-closed default the broker relies
// on.
func TestParseSecretAddArgsAllowedHosts(t *testing.T) {
	a, ok := parseSecretAddArgs([]string{"my-key", "--service", "svc", "--allowed-hosts", "api.example.com, other.example.com:8443,,"})
	if !ok {
		t.Fatal("must parse")
	}
	want := []string{"api.example.com", "other.example.com:8443"}
	if !reflect.DeepEqual(a.allowedHosts, want) {
		t.Fatalf("allowedHosts = %v, want %v", a.allowedHosts, want)
	}

	a2, ok := parseSecretAddArgs([]string{"my-key", "--service", "svc"})
	if !ok {
		t.Fatal("must parse")
	}
	if a2.allowedHosts != nil {
		t.Fatalf("allowedHosts absent must be nil, got %v", a2.allowedHosts)
	}
}

func TestParseSecretAddArgsByDefaultsToHuman(t *testing.T) {
	a, ok := parseSecretAddArgs([]string{"my-key", "--service", "svc"})
	if !ok || a.by != "human" {
		t.Fatalf("by must default to human, got %+v (ok=%v)", a, ok)
	}
}

func TestParseSecretAddArgsRejectsDanglingFlag(t *testing.T) {
	if _, ok := parseSecretAddArgs([]string{"my-key", "--service"}); ok {
		t.Fatal("a flag with no value must be rejected")
	}
	if _, ok := parseSecretAddArgs([]string{"my-key", "--service", "svc", "--bogus"}); ok {
		t.Fatal("an unknown flag must be rejected")
	}
}

func TestParseSecretShowArgsDefaults(t *testing.T) {
	a, ok := parseSecretShowArgs([]string{"my-key"})
	if !ok || a.reveal || a.by != "human" {
		t.Fatalf("bad defaults: %+v (ok=%v)", a, ok)
	}
}

func TestParseSecretShowArgsFullShape(t *testing.T) {
	a, ok := parseSecretShowArgs([]string{"my-key", "--reveal", "--by", "pi"})
	if !ok || !a.reveal || a.by != "pi" || a.name != "my-key" {
		t.Fatalf("bad parse: %+v (ok=%v)", a, ok)
	}
}

func TestTrimTrailingNewline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"value", "value"},
		{"value\n", "value"},
		{"value\r\n", "value"},
		{"value\n\n", "value\n"},
		{"", ""},
	}
	for _, c := range cases {
		got := string(trimTrailingNewline([]byte(c.in)))
		if got != c.want {
			t.Fatalf("trimTrailingNewline(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSecretAddRefusesTTYStdin proves cmdSecretAdd refuses to run
// when stdin is a terminal, with the documented pipe-only error, and
// never touches the vault at all — a TTY refusal must fail before any
// I/O that could look like progress.
func TestSecretAddRefusesTTYStdin(t *testing.T) {
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	defer func() { stdinIsTTY = orig }()

	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("LOADOUT_HOME", filepath.Join(base, "vault"))
	if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Init(filepath.Join(base, "vault")); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := cmdSecretAdd(&out, &errOut, []string{"my-key", "--service", "svc"}, modeText)
	if code != 1 {
		t.Fatalf("want exit 1, got %d (%s)", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("must print nothing to stdout, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "pipe the value on stdin") {
		t.Fatalf("bad error: %q", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "secrets", "my-key")); !os.IsNotExist(err) {
		t.Fatal("a TTY refusal must never create a secret")
	}
}

// captureReadStdin substitutes readStdin with one that still reads
// normally but also stores the exact slice it returned into captured,
// so a test can inspect that slice after the command under test
// returns — the only way to see a byte slice that never leaves
// cmdSecretAdd/cmdSecretRotate any other way. It restores readStdin
// via t.Cleanup.
func captureReadStdin(t *testing.T) (captured *[]byte) {
	t.Helper()
	var buf []byte
	orig := readStdin
	readStdin = func(r io.Reader) ([]byte, error) {
		b, err := orig(r)
		buf = b
		return b, err
	}
	t.Cleanup(func() { readStdin = orig })
	return &buf
}

// pipeValue replaces os.Stdin with a pipe carrying value, restoring
// the real os.Stdin via t.Cleanup.
func pipeValue(t *testing.T, value string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; r.Close() })
	go func() {
		io.WriteString(w, value)
		w.Close()
	}()
}

// TestSecretAddZeroesStdinOnEarlyVaultOpenFailure proves the Commit-2
// hardening: cmdSecretAdd zeroes the stdin plaintext even when
// vault.Open fails AFTER it was read — before AddSecret ever runs to
// zero its own copy. LOADOUT_HOME here points at a directory nothing
// has run "loadout init" on, so vault.Open fails immediately, with no
// lock-retry delay.
func TestSecretAddZeroesStdinOnEarlyVaultOpenFailure(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("LOADOUT_HOME", filepath.Join(base, "vault")) // never initialized
	if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
		t.Fatal(err)
	}

	captured := captureReadStdin(t)
	pipeValue(t, "do-not-leak-this-value")

	var out, errOut bytes.Buffer
	code := cmdSecretAdd(&out, &errOut, []string{"my-key", "--service", "svc"}, modeText)
	if code != 1 {
		t.Fatalf("want exit 1 (vault.Open must fail against an uninitialized vault), got %d (%s)", code, errOut.String())
	}
	if *captured == nil {
		t.Fatal("the test hook never captured the stdin read")
	}
	for i, b := range *captured {
		if b != 0 {
			t.Fatalf("captured[%d] = %d, want 0: cmdSecretAdd must zero the stdin plaintext even when vault.Open fails", i, b)
		}
	}
}

// TestSecretRotateZeroesStdinOnEarlyVaultOpenFailure is
// TestSecretAddZeroesStdinOnEarlyVaultOpenFailure's counterpart for
// cmdSecretRotate.
func TestSecretRotateZeroesStdinOnEarlyVaultOpenFailure(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("LOADOUT_HOME", filepath.Join(base, "vault")) // never initialized
	if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
		t.Fatal(err)
	}

	captured := captureReadStdin(t)
	pipeValue(t, "do-not-leak-this-rotated-value")

	var out, errOut bytes.Buffer
	code := cmdSecretRotate(&out, &errOut, []string{"my-key"}, modeText)
	if code != 1 {
		t.Fatalf("want exit 1 (vault.Open must fail against an uninitialized vault), got %d (%s)", code, errOut.String())
	}
	if *captured == nil {
		t.Fatal("the test hook never captured the stdin read")
	}
	for i, b := range *captured {
		if b != 0 {
			t.Fatalf("captured[%d] = %d, want 0: cmdSecretRotate must zero the stdin plaintext even when vault.Open fails", i, b)
		}
	}
}

func TestParseSecretRotateArgsDefaults(t *testing.T) {
	a, ok := parseSecretRotateArgs([]string{"my-key"})
	if !ok || a.by != "human" || a.name != "my-key" {
		t.Fatalf("bad defaults: %+v (ok=%v)", a, ok)
	}
}

func TestParseSecretRotateArgsFullShape(t *testing.T) {
	a, ok := parseSecretRotateArgs([]string{"my-key", "--by", "pi"})
	if !ok || a.by != "pi" || a.name != "my-key" {
		t.Fatalf("bad parse: %+v (ok=%v)", a, ok)
	}
}

func TestParseSecretRotateArgsRejectsBadShape(t *testing.T) {
	if _, ok := parseSecretRotateArgs(nil); ok {
		t.Fatal("must require a name")
	}
	if _, ok := parseSecretRotateArgs([]string{"my-key", "--by"}); ok {
		t.Fatal("a --by flag with no value must be rejected")
	}
	if _, ok := parseSecretRotateArgs([]string{"my-key", "--bogus", "x"}); ok {
		t.Fatal("an unknown flag must be rejected")
	}
}

// TestSecretRotateRefusesTTYStdin proves cmdSecretRotate refuses to
// run when stdin is a terminal, the same pipe-only rule as
// cmdSecretAdd, and never touches the vault at all — the refusal must
// fail before any I/O that could look like progress, including
// reading the secret's own metadata to check it exists.
func TestSecretRotateRefusesTTYStdin(t *testing.T) {
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	defer func() { stdinIsTTY = orig }()

	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("LOADOUT_HOME", filepath.Join(base, "vault"))
	if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Init(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddSecret(v, "my-key", "svc", "", "", "human", nil, []byte("original-value")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(v.SecretsDir(), "my-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := cmdSecretRotate(&out, &errOut, []string{"my-key"}, modeText)
	if code != 1 {
		t.Fatalf("want exit 1, got %d (%s)", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("must print nothing to stdout, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "pipe the value on stdin") {
		t.Fatalf("bad error: %q", errOut.String())
	}

	after, err := os.ReadFile(filepath.Join(v.SecretsDir(), "my-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a TTY refusal must never touch the existing secret's value")
	}
}
