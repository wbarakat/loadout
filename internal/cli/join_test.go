package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

// TestJoinOnFreshMachineInitsVaultWritesRemoteAndRegisters proves
// "loadout join" works on a machine with no vault at all: it creates
// one, writes remote.toml, and registers this device with the
// remote's bootstrap roster, without ever syncing.
func TestJoinOnFreshMachineInitsVaultWritesRemoteAndRegisters(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)

	out, errOut, code := run(t, "join", ts.URL, token)
	if code != 0 {
		t.Fatalf("join failed: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "loadout.toml")); err != nil {
		t.Fatal("join must create the vault when none exists")
	}
	data, err := os.ReadFile(filepath.Join(base, "vault", "remote.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ts.URL) {
		t.Fatalf("remote.toml must hold the url, got %q", data)
	}

	if !strings.Contains(out, "and waits for an approval.") {
		t.Fatalf("bad join output: %q", out)
	}
	if !strings.Contains(out, "loadout devices approve ") {
		t.Fatalf("join must name the next step, got %q", out)
	}
	if !strings.Contains(out, "loadout sync --remote here.") {
		t.Fatalf("join must name the final step, got %q", out)
	}

	c := remote.NewClient(ts.URL, token)
	devices, err := c.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("join must register the device with the remote, got %+v", devices)
	}
}

// TestJoinDoesNotSync proves join never pulls or pushes anything: a
// freshly joined device cannot decrypt the vault yet, so it must not
// even try.
func TestJoinDoesNotSync(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)

	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join failed: %s", errOut)
	}
	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := remote.Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LastVersion != "" {
		t.Fatalf("join must never advance last_version, got %q", cfg.LastVersion)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", ".sync-state.json")); !os.IsNotExist(err) {
		t.Fatal("join must never write sync state: it never syncs")
	}
}

// TestJoinNeverPrintsToken proves join's text and JSON output never
// carry the token, in either stream.
func TestJoinNeverPrintsToken(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)

	out, errOut, code := run(t, "join", ts.URL, token)
	if code != 0 {
		t.Fatalf("join failed: %s", errOut)
	}
	if strings.Contains(out, token) || strings.Contains(errOut, token) {
		t.Fatalf("join must never print the token, got out=%q err=%q", out, errOut)
	}

	base2 := newDeviceEnv(t)
	useDeviceEnv(t, base2)
	out, errOut, code = run(t, "join", ts.URL, token, "--json")
	if code != 0 {
		t.Fatalf("join --json failed: %s", errOut)
	}
	if strings.Contains(out, token) || strings.Contains(errOut, token) {
		t.Fatalf("join --json must never print the token, got out=%q err=%q", out, errOut)
	}
	var got struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("join --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Name == "" || got.URL != ts.URL {
		t.Fatalf("bad join json: %+v", got)
	}
}

func TestJoinRequiresBothArgs(t *testing.T) {
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	for _, args := range [][]string{
		{"join"},
		{"join", "http://x"},
		{"join", "http://x", "tok", "extra"},
	} {
		_, errOut, code := run(t, args...)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Fatalf("%v must be a usage error, got %d %q", args, code, errOut)
		}
	}
}

func TestJoinRejectsBadURL(t *testing.T) {
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	_, errOut, code := run(t, "join", "not-a-url", "tok")
	if code != 2 {
		t.Fatalf("a bad url must be a usage-style error, got %d %q", code, errOut)
	}
	if !strings.Contains(errOut, "Fix:") {
		t.Fatalf("bad url error must use the standard grammar, got %q", errOut)
	}
}

// TestJoinOnAnAlreadyInitializedVaultStillWorks proves join can also
// run on a machine that already has a vault (it just reuses it,
// rather than requiring a bare machine).
func TestJoinOnAnAlreadyInitializedVaultStillWorks(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	run(t, "add", "memory", "my-stack")

	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join on an existing vault failed: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "memory", "my-stack.md")); err != nil {
		t.Fatal("join must not disturb existing vault content")
	}
}
