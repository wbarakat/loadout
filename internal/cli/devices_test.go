package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

// writeDeviceName pins a vault's device.name before anything else
// touches device identity, so two vaults in the same test process
// never collide on the hostname-derived default (mirrors
// internal/remote's own test helper of the same purpose).
func writeDeviceName(t *testing.T, base, name string) {
	t.Helper()
	path := filepath.Join(base, "vault", "device.name")
	if err := os.WriteFile(path, []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEnrollmentFullFlow drives the complete Task 6 scenario across
// two vault homes and one httptest server: A is the bootstrap device
// (and must appear approved once it has synced, with no explicit
// self-approval), B joins and waits, a negative sync before approval
// fails cleanly, A approves B, and B's next sync decrypts and
// receives A's content.
func TestEnrollmentFullFlow(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	if _, errOut, code := run(t, "remote", "add", ts.URL, token); code != 0 {
		t.Fatalf("remote add on A failed: %s", errOut)
	}
	if _, errOut, code := run(t, "add", "memory", "stack"); code != 0 {
		t.Fatalf("add memory on A failed: %s", errOut)
	}
	aFact, err := os.ReadFile(filepath.Join(baseA, "vault", "memory", "stack.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}

	// A is the bootstrap device: it never called "devices approve" on
	// itself, but it must already appear approved, since PackSnapshot
	// has been encrypting to it alone all along.
	out, errOut, code := run(t, "devices")
	if code != 0 {
		t.Fatalf("devices on A failed: %s", errOut)
	}
	if !strings.Contains(out, "device-a — approved") {
		t.Fatalf("the bootstrap device must appear approved once it has synced, got %q", out)
	}

	// B: a fresh machine joins.
	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	joinOut, joinErr, code := run(t, "join", ts.URL, token)
	if code != 0 {
		t.Fatalf("join on B failed: %s", joinErr)
	}
	want := "this device is enrolled as device-b and waits for an approval.\n" +
		"next: on an already-approved device run: loadout devices approve device-b.\n" +
		"then run: loadout sync --remote here.\n"
	if joinOut != want {
		t.Fatalf("bad join output: got %q want %q", joinOut, want)
	}
	if strings.Contains(joinOut, token) || strings.Contains(joinErr, token) {
		t.Fatal("join must never print the token")
	}

	// Negative: B syncs before approval. It must fail cleanly with the
	// decrypt-refusal grammar, not crash, exit 1.
	_, errOut, code = run(t, "sync", "--remote")
	if code != 1 {
		t.Fatalf("sync --remote before approval must exit 1, got %d (%s)", code, errOut)
	}
	if !strings.Contains(errOut, "this device cannot decrypt the snapshot") {
		t.Fatalf("bad error for an unapproved device's sync: %q", errOut)
	}

	// From A's side, B must show as waiting.
	useDeviceEnv(t, baseA)
	out, errOut, code = run(t, "devices")
	if code != 0 {
		t.Fatalf("devices on A failed: %s", errOut)
	}
	if !strings.Contains(out, "device-b — waiting") {
		t.Fatalf("B must show as waiting before approval, got %q", out)
	}

	// A approves B.
	approveOut, approveErr, code := run(t, "devices", "approve", "device-b")
	if code != 0 {
		t.Fatalf("devices approve failed: %s", approveErr)
	}
	if approveOut != "approved device-b. Run loadout sync --remote on that device now.\n" {
		t.Fatalf("bad approve output: %q", approveOut)
	}
	if strings.Contains(approveOut, token) || strings.Contains(approveErr, token) {
		t.Fatal("devices approve must never print the token")
	}

	vA, err := vault.Open(filepath.Join(baseA, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	rosterA, err := vault.ReadRoster(vA)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rosterA["device-b"]; !ok {
		t.Fatalf("device-b must be in A's devices.toml after approval, got %+v", rosterA)
	}
	if _, ok := rosterA["device-a"]; !ok {
		t.Fatalf("A's own bootstrap approval must add itself to devices.toml too, got %+v", rosterA)
	}

	// devices now shows both approved from A's side.
	out, errOut, code = run(t, "devices")
	if code != 0 {
		t.Fatalf("devices on A failed: %s", errOut)
	}
	if !strings.Contains(out, "device-a — approved") || !strings.Contains(out, "device-b — approved") {
		t.Fatalf("both devices must show approved after the approval, got %q", out)
	}

	// Re-approving the same device is idempotent.
	out2, errOut2, code2 := run(t, "devices", "approve", "device-b")
	if code2 != 0 {
		t.Fatalf("re-approve failed: %s", errOut2)
	}
	if out2 != "device-b is already approved.\n" {
		t.Fatalf("bad re-approve output: %q", out2)
	}

	// Approving a name the remote has never seen is an error.
	_, errOut3, code3 := run(t, "devices", "approve", "device-nope")
	if code3 != 1 {
		t.Fatalf("approving an unknown device must exit 1, got %d", code3)
	}
	wantErr := "device-nope: no such device on the remote. Fix: run loadout devices to see who is waiting.\n"
	if errOut3 != wantErr {
		t.Fatalf("bad error: got %q want %q", errOut3, wantErr)
	}

	// B syncs and now decrypts and receives A's fact.
	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B failed: %s", errOut)
	}
	bFact, err := os.ReadFile(filepath.Join(baseB, "vault", "memory", "stack.md"))
	if err != nil {
		t.Fatalf("B must receive A's fact: %v", err)
	}
	if string(bFact) != string(aFact) {
		t.Fatalf("B's fact content = %q, want %q", bFact, aFact)
	}

	// B's own devices view, now that it holds the synced devices.toml,
	// must also show both devices approved.
	out, errOut, code = run(t, "devices")
	if code != 0 {
		t.Fatalf("devices on B failed: %s", errOut)
	}
	if !strings.Contains(out, "device-a — approved") || !strings.Contains(out, "device-b — approved") {
		t.Fatalf("both devices must show approved from B's side too, got %q", out)
	}
}

// TestDevicesJSON proves the JSON shape: {devices:[{name, recipient,
// approved}]}.
func TestDevicesJSON(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	writeDeviceName(t, base, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	out, errOut, code := run(t, "devices", "--json")
	if code != 0 {
		t.Fatalf("devices --json failed: %s", errOut)
	}
	var got struct {
		Devices []struct {
			Name      string `json:"name"`
			Recipient string `json:"recipient"`
			Approved  bool   `json:"approved"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("devices --json did not parse: %v\noutput: %s", err, out)
	}
	if len(got.Devices) != 1 {
		t.Fatalf("bad devices json: %+v", got)
	}
	d := got.Devices[0]
	if d.Name != "device-a" || !d.Approved || !strings.HasPrefix(d.Recipient, "age1") {
		t.Fatalf("bad device entry: %+v", d)
	}
	if strings.Contains(out, token) {
		t.Fatal("devices --json must never print the token")
	}
}

// TestDevicesApproveRequiresOneArg proves the usage error path for
// "devices approve" and an unknown "devices" subcommand.
func TestDevicesApproveRequiresOneArg(t *testing.T) {
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")

	for _, args := range [][]string{
		{"devices", "approve"},
		{"devices", "approve", "a", "b"},
		{"devices", "bogus"},
	} {
		_, errOut, code := run(t, args...)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Fatalf("%v must be a usage error, got %d %q", args, code, errOut)
		}
	}
}

// TestDevicesRequiresRemoteConfigured proves "loadout devices" gives
// the standard no-remote-configured error, rather than a confusing
// failure, when no remote is set up yet.
func TestDevicesRequiresRemoteConfigured(t *testing.T) {
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")

	_, errOut, code := run(t, "devices")
	if code != 1 {
		t.Fatalf("devices with no remote must exit 1, got %d", code)
	}
	want := "no remote configured. Fix: run loadout remote add <url> <token>.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}
