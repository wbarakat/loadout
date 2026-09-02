package cli_test

import (
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/server"
	"loadout.dev/loadout/internal/vault"
)

// newRemoteTestServer builds an httptest.Server backed by a fresh
// Store, the same shape internal/server and internal/remote's own
// suites use, so the CLI's --remote path exercises the real Task 4
// API end to end.
func newRemoteTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	store, err := server.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(store, token, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, token
}

// newDeviceEnv creates a fresh, unselected device environment (its
// own home and vault directories), without pointing the process env
// at it yet. useDeviceEnv switches to it.
func newDeviceEnv(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	return base
}

// useDeviceEnv points HOME and LOADOUT_HOME at base, so every run()
// call after this acts on that device's vault. Tests that simulate
// two devices call this to switch between them; run() itself is never
// concurrent, so this sequential switch is safe.
func useDeviceEnv(t *testing.T, base string) {
	t.Helper()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("LOADOUT_HOME", filepath.Join(base, "vault"))
}

// enrollDevicesMutually makes the vaults at rootA and rootB each able
// to decrypt the other's snapshots, exactly as internal/remote's own
// tests do: Task 6 will automate this via an approval flow, but Task
// 5's job is the sync protocol and the merge, not enrollment.
func enrollDevicesMutually(t *testing.T, rootA, rootB string) {
	t.Helper()
	va, err := vault.Open(rootA)
	if err != nil {
		t.Fatal(err)
	}
	vb, err := vault.Open(rootB)
	if err != nil {
		t.Fatal(err)
	}
	aName, aRecipient, err := vault.DeviceIdentity(va)
	if err != nil {
		t.Fatal(err)
	}
	bName, bRecipient, err := vault.DeviceIdentity(vb)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []*vault.Vault{va, vb} {
		if err := vault.AddToRoster(v, aName, aRecipient, vault.RoleFull); err != nil {
			t.Fatal(err)
		}
		if err := vault.AddToRoster(v, bName, bRecipient, vault.RoleFull); err != nil {
			t.Fatal(err)
		}
		if err := vault.Snapshot(v, "enroll devices"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteAddWritesConfigAndNeverPrintsToken(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")

	out, errOut, code := run(t, "remote", "add", "http://127.0.0.1:7777", "super-secret-token")
	if code != 0 {
		t.Fatalf("remote add failed: %s", errOut)
	}
	if out != "remote added: http://127.0.0.1:7777\n" {
		t.Fatalf("bad output: %q", out)
	}
	if strings.Contains(out, "super-secret-token") || strings.Contains(errOut, "super-secret-token") {
		t.Fatalf("remote add must never print the token, got out=%q err=%q", out, errOut)
	}

	data, err := os.ReadFile(filepath.Join(base, "vault", "remote.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "super-secret-token") {
		t.Fatal("remote.toml must still hold the token on disk")
	}
	fi, err := os.Stat(filepath.Join(base, "vault", "remote.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("remote.toml must be mode 0600, got %o", fi.Mode().Perm())
	}
}

func TestRemoteAddJSONNeverPrintsToken(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := run(t, "remote", "add", "http://127.0.0.1:7777", "super-secret-token", "--json")
	if code != 0 {
		t.Fatalf("remote add --json failed: %s", errOut)
	}
	if strings.Contains(out, "super-secret-token") {
		t.Fatalf("remote add --json must never print the token, got %q", out)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("remote add --json did not parse: %v\noutput: %s", err, out)
	}
	if got.URL != "http://127.0.0.1:7777" {
		t.Fatalf("bad url in json: %+v", got)
	}
}

func TestRemoteAddRequiresBothArgs(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	for _, args := range [][]string{
		{"remote", "add"},
		{"remote", "add", "http://x"},
		{"remote", "add", "http://x", "tok", "extra"},
	} {
		_, errOut, code := run(t, args...)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Fatalf("%v must be a usage error, got %d %q", args, code, errOut)
		}
	}
}

func TestRemoteAddRejectsBadURL(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := run(t, "remote", "add", "not-a-url", "tok")
	if code != 2 {
		t.Fatalf("a bad url must be a usage-style error, got %d %q", code, errOut)
	}
	if !strings.Contains(errOut, "Fix:") {
		t.Fatalf("bad url error must use the standard grammar, got %q", errOut)
	}
}

func TestRemoteShowWithoutConfigGivesFixedError(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := run(t, "remote")
	if code != 1 {
		t.Fatalf("remote with no config must exit 1, got %d", code)
	}
	want := "no remote configured. Fix: run loadout remote add <url> <token>.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}

func TestRemoteShowDisplaysURLAndLastVersionNeverToken(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "remote", "add", "http://127.0.0.1:7777", "super-secret-token")

	out, errOut, code := run(t, "remote")
	if code != 0 {
		t.Fatalf("remote failed: %s", errOut)
	}
	if !strings.Contains(out, "http://127.0.0.1:7777") {
		t.Fatalf("bad output: %q", out)
	}
	if !strings.Contains(out, "(none yet)") {
		t.Fatalf("a fresh remote must show no last synced version yet, got %q", out)
	}
	if strings.Contains(out, "super-secret-token") {
		t.Fatalf("remote must never print the token, got %q", out)
	}
}

func TestRemoteShowJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "remote", "add", "http://127.0.0.1:7777", "super-secret-token")

	out, errOut, code := run(t, "remote", "--json")
	if code != 0 {
		t.Fatalf("remote --json failed: %s", errOut)
	}
	if strings.Contains(out, "super-secret-token") {
		t.Fatalf("remote --json must never print the token, got %q", out)
	}
	var got struct {
		URL         string `json:"url"`
		LastVersion string `json:"last_version"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("remote --json did not parse: %v\noutput: %s", err, out)
	}
	if got.URL != "http://127.0.0.1:7777" || got.LastVersion != "" {
		t.Fatalf("bad json: %+v", got)
	}
}

// TestSyncRemoteRunsLocalProjectionAndSyncsRemotely proves "loadout
// sync --remote" does both halves: the local projection (a skill
// links into the adapter's directory) and the remote sync (a second
// device receives the same skill after it syncs too).
func TestSyncRemoteRunsLocalProjectionAndSyncsRemotely(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	initClaudeAndPi(t, baseA)
	run(t, "add", "skill", "deploy-checks")
	if _, errOut, code := run(t, "remote", "add", ts.URL, token); code != 0 {
		t.Fatalf("remote add on A failed: %s", errOut)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	initClaudeAndPi(t, baseB)
	if _, errOut, code := run(t, "remote", "add", ts.URL, token); code != 0 {
		t.Fatalf("remote add on B failed: %s", errOut)
	}

	enrollDevicesMutually(t, filepath.Join(baseA, "vault"), filepath.Join(baseB, "vault"))

	useDeviceEnv(t, baseA)
	out, errOut, code := run(t, "sync", "--remote")
	if code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}
	if !strings.Contains(out, "synced claude-code") {
		t.Fatalf("sync --remote must still run the local projection, got %q", out)
	}
	if !strings.Contains(out, "synced with the remote") {
		t.Fatalf("sync --remote must report the remote sync too, got %q", out)
	}
	if _, err := os.Readlink(filepath.Join(baseA, "home", ".claude", "skills", "deploy-checks")); err != nil {
		t.Fatal("sync --remote must still link the skill locally")
	}

	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B failed: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(baseB, "vault", "skills", "deploy-checks", "SKILL.md")); err != nil {
		t.Fatal("B must receive the skill through the remote sync")
	}
}

// TestSyncRemoteWithDryRunNeverTouchesTheRemote proves --dry-run
// still wins over --remote: a preview must never push, pull, or
// register a device.
func TestSyncRemoteWithDryRunNeverTouchesTheRemote(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "remote", "add", ts.URL, token)

	out, errOut, code := run(t, "sync", "--remote", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --remote --dry-run failed: %s", errOut)
	}
	if strings.Contains(out, "synced with the remote") {
		t.Fatalf("a dry run must never report a remote sync, got %q", out)
	}

	rc, errOut2, code2 := run(t, "remote")
	if code2 != 0 {
		t.Fatalf("remote failed: %s", errOut2)
	}
	if !strings.Contains(rc, "(none yet)") {
		t.Fatalf("a dry run must never advance the last synced version, got %q", rc)
	}
}

// TestSyncDryRunRemotePrintsWouldSyncLineWithoutNetworkCall proves
// Minor 10: "sync --dry-run --remote" must never silently drop the
// remote half in text mode. It must print one line naming the
// remote's url, and it must never touch the network to do it: the
// remote here points at an address that refuses connections, and the
// command must still succeed.
func TestSyncDryRunRemotePrintsWouldSyncLineWithoutNetworkCall(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "remote", "add", "http://127.0.0.1:1", "some-token")

	out, errOut, code := run(t, "sync", "--remote", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --remote --dry-run must succeed even with an unreachable remote (no network call), got %d: %s", code, errOut)
	}
	if !strings.Contains(out, "would sync with the remote at http://127.0.0.1:1") {
		t.Fatalf("bad output: %q", out)
	}

	rc, errOut2, code2 := run(t, "remote")
	if code2 != 0 {
		t.Fatalf("remote failed: %s", errOut2)
	}
	if !strings.Contains(rc, "(none yet)") {
		t.Fatalf("a dry run must never advance the last synced version, got %q", rc)
	}
}

func TestStatusShowsRemoteLineWhenConfigured(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	setupEnv(t)
	run(t, "init")
	run(t, "remote", "add", ts.URL, token)

	out, errOut, code := run(t, "status")
	if code != 0 {
		t.Fatalf("status failed: %s", errOut)
	}
	if !strings.Contains(out, "remote: "+ts.URL+" — in sync") {
		t.Fatalf("status must show the remote line, got %q", out)
	}
}

func TestStatusOmitsRemoteLineWhenNotConfigured(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := run(t, "status")
	if code != 0 {
		t.Fatalf("status failed: %s", errOut)
	}
	if strings.Contains(out, "remote:") {
		t.Fatalf("status must not mention a remote when none is configured, got %q", out)
	}
}

// TestDoctorReportsUnreachableRemote proves doctor surfaces a remote
// that cannot be reached as a problem, with the fixed grammar's Fix
// text split out into doctor's own Fix field.
func TestDoctorReportsUnreachableRemote(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "remote", "add", "http://127.0.0.1:1", "some-token")

	out, _, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must report a problem for an unreachable remote, got %d", code)
	}
	if !strings.Contains(out, "http://127.0.0.1:1") || !strings.Contains(out, "not reachable") {
		t.Fatalf("doctor must name the unreachable remote, got %q", out)
	}
	if !strings.Contains(out, "check the url and that loadoutd runs.") {
		t.Fatalf("doctor must carry the fix, got %q", out)
	}
}

func TestDoctorSilentWhenRemoteInSync(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	setupEnv(t)
	run(t, "init")
	run(t, "sync")
	run(t, "remote", "add", ts.URL, token)

	out, _, code := run(t, "doctor")
	if code != 0 || !strings.Contains(out, "all good") {
		t.Fatalf("doctor must stay quiet about an in-sync remote, got code=%d out=%q", code, out)
	}
}
