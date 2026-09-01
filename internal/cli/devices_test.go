package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/server"
	"loadout.dev/loadout/internal/vault"
)

// findDevice finds name's recipient among devices, the remote's
// bootstrap roster — a test-local twin of the cli package's own
// unexported findDeviceRecipient, since this file lives in the
// external cli_test package.
func findDevice(devices []remote.Device, name string) (recipient string, ok bool) {
	for _, d := range devices {
		if d.Name == name {
			return d.Recipient, true
		}
	}
	return "", false
}

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
// two vault homes and one httptest server: A is the bootstrap device,
// B joins and waits, a negative sync before approval fails cleanly, A
// approves B (which also lands A's own identity in devices.toml, the
// self-lockout fix), and B's next sync decrypts and receives A's
// content.
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

	// Before any approval, devices.toml — the real decrypt allowlist —
	// is still empty, so A shows as waiting too, exactly like a fresh
	// joiner: there is no bootstrap-self-approval heuristic. The
	// review that added this behavior found the earlier heuristic made
	// a fresh joiner look approved from its own point of view; see
	// TestDevicesShowsFreshJoinerWaitingAndOwnerApproved for the
	// corrected steady-state picture, once someone has actually been
	// approved.
	out, errOut, code := run(t, "devices")
	if code != 0 {
		t.Fatalf("devices on A failed: %s", errOut)
	}
	if !strings.Contains(out, "device-a — waiting") {
		t.Fatalf("before any approval, A must show waiting too (devices.toml is still empty), got %q", out)
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

	vB, err := vault.Open(filepath.Join(baseB, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	_, bRecipient, err := vault.DeviceIdentity(vB)
	if err != nil {
		t.Fatal(err)
	}

	// A approves B. The approval message shows B's recipient in full,
	// so an admin can verify it out-of-band before trusting it.
	approveOut, approveErr, code := run(t, "devices", "approve", "device-b")
	if code != 0 {
		t.Fatalf("devices approve failed: %s", approveErr)
	}
	wantApprove := fmt.Sprintf("approved device-b (%s). Run loadout sync --remote on that device now.\n", bRecipient)
	if approveOut != wantApprove {
		t.Fatalf("bad approve output: got %q want %q", approveOut, wantApprove)
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
// approved, state}]}.
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

	// This test is about the JSON shape, not the approval flow: put
	// device-a in its own devices.toml directly, the way the
	// self-lockout fix would once it approved anyone.
	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	_, recipient, err := vault.DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "device-a", recipient, vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "approve device device-a"); err != nil {
		t.Fatal(err)
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
			State     string `json:"state"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("devices --json did not parse: %v\noutput: %s", err, out)
	}
	if len(got.Devices) != 1 {
		t.Fatalf("bad devices json: %+v", got)
	}
	d := got.Devices[0]
	if d.Name != "device-a" || !d.Approved || d.State != "approved" || !strings.HasPrefix(d.Recipient, "age1") {
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

// newRemoteTestServerWithStore is newRemoteTestServer, plus the
// underlying *server.Store: some tests here need to reach past the
// HTTP API (which now validates recipients) to seed the roster
// directly, proving devices approve's own validation layer catches a
// bad recipient independently of the server's.
func newRemoteTestServerWithStore(t *testing.T) (*httptest.Server, string, *server.Store) {
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
	return ts, token, store
}

// TestDevicesApproveRefusesInvalidRecipientAtBothLayers reproduces the
// live-repro review's DoS: a token holder registers a garbage
// recipient for a name, and a routine "devices approve" of that name
// must never commit it to devices.toml — doing so would brick
// PackSnapshot for every device on every future sync. It proves both
// ruled layers: the server's own HTTP API refuses the bad recipient
// outright (a raw POST, exactly the reviewer's curl reproduction),
// and — as defense in depth, seeding the roster directly, bypassing
// the now-validating HTTP layer — devices approve's own validation
// refuses it too, with no local mutation, leaving the vault syncing
// normally afterward.
func TestDevicesApproveRefusesInvalidRecipientAtBothLayers(t *testing.T) {
	ts, token, store := newRemoteTestServerWithStore(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	writeDeviceName(t, base, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "add", "memory", "stack"); code != 0 {
		t.Fatalf("add memory failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	// Layer 2: the server's raw HTTP API refuses a garbage recipient,
	// reproducing the reviewer's curl attack directly — no loadout
	// client involved at all.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/devices",
		strings.NewReader(`{"name":"evil","recipient":"not-an-age-recipient"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("the server must refuse an invalid recipient with 400, got %d", resp.StatusCode)
	}

	// Layer 1, defense in depth: seed the roster directly (bypassing
	// the HTTP layer above, simulating a bad entry reaching the roster
	// by any other means) and prove devices approve still refuses it,
	// with no local mutation.
	if _, err := store.UpsertDevice("evil-bypass", "not-an-age-recipient"); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := run(t, "devices", "approve", "evil-bypass")
	if code != 1 {
		t.Fatalf("approving an invalid recipient must exit 1, got %d", code)
	}
	want := "evil-bypass: the remote gave an invalid recipient key. Fix: that device must run loadout join again; do not approve it until it registers a valid key.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	roster, err := vault.ReadRoster(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := roster["evil-bypass"]; ok {
		t.Fatal("an invalid recipient must never enter devices.toml")
	}

	// The vault must still sync normally afterward: the refused
	// approval left nothing broken for every device.
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote must still work after the refused approval, got %d: %s", code, errOut)
	}
}

// TestDevicesApproveMismatchRequiresRotate proves a re-approve whose
// server recipient differs from the stored one is refused by default
// (a silent overwrite here would be a spoofing vector), shows both
// recipients in full for out-of-band verification, and --rotate
// writes exactly the admin's own explicit argument — never the
// server's own live value, even when that live value is a third,
// different recipient at the moment of rotation.
func TestDevicesApproveMismatchRequiresRotate(t *testing.T) {
	ts, token, store := newRemoteTestServerWithStore(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	writeDeviceName(t, base, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join failed: %s", errOut)
	}
	vB, err := vault.Open(filepath.Join(baseB, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	_, originalRecipient, err := vault.DeviceIdentity(vB)
	if err != nil {
		t.Fatal(err)
	}

	useDeviceEnv(t, base)
	if _, errOut, code := run(t, "devices", "approve", "device-b"); code != 0 {
		t.Fatalf("approve failed: %s", errOut)
	}

	// The server now reports a different recipient for "device-b" —
	// an attacker, or some unrelated re-registration; either way, a
	// plain re-approve must never adopt it automatically.
	serverIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	serverRecipient := serverIdentity.Recipient().String()
	if _, err := store.UpsertDevice("device-b", serverRecipient); err != nil {
		t.Fatal(err)
	}

	// A plain re-approve must refuse, showing both recipients in full.
	_, errOut, code := run(t, "devices", "approve", "device-b")
	if code != 1 {
		t.Fatalf("a mismatched re-approve without --rotate must exit 1, got %d", code)
	}
	want := fmt.Sprintf(
		"device-b is already approved with a different key. This is a re-keyed device or an imposter.\nstored:  %s\noffered: %s\nFix: verify the correct recipient out-of-band (run loadout device on that machine), then run loadout devices approve device-b --rotate <recipient> with the value you trust — never the remote's own live value.\n",
		originalRecipient, serverRecipient)
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	roster, err := vault.ReadRoster(v)
	if err != nil {
		t.Fatal(err)
	}
	if roster["device-b"] != originalRecipient {
		t.Fatal("a mismatched re-approve without --rotate must not overwrite the roster")
	}

	// devices shows the re-keyed state.
	out, errOut, code := run(t, "devices")
	if code != 0 {
		t.Fatalf("devices failed: %s", errOut)
	}
	if !strings.Contains(out, "device-b — re-keyed (verify the new key out-of-band, then run: loadout devices approve device-b --rotate <recipient>)") {
		t.Fatalf("a mismatched device must show the re-keyed state, got %q", out)
	}

	// The admin verifies a THIRD recipient out-of-band — neither the
	// original nor the server's current (unreliable) report — and
	// rotates to exactly that value.
	trustedIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	trustedRecipient := trustedIdentity.Recipient().String()
	out2, errOut2, code2 := run(t, "devices", "approve", "device-b", "--rotate", trustedRecipient)
	if code2 != 0 {
		t.Fatalf("devices approve --rotate failed: %s", errOut2)
	}
	wantRotate := fmt.Sprintf("rotated device-b to %s.\n", trustedRecipient)
	if out2 != wantRotate {
		t.Fatalf("bad rotate output: got %q want %q", out2, wantRotate)
	}
	roster, err = vault.ReadRoster(v)
	if err != nil {
		t.Fatal(err)
	}
	if roster["device-b"] != trustedRecipient {
		t.Fatalf("--rotate must write exactly the admin's explicit argument, got %+v", roster)
	}
	if roster["device-b"] == serverRecipient {
		t.Fatal("--rotate must never adopt the remote's own live recipient")
	}
}

// TestDevicesApproveRotateRequiresRecipientArg proves --rotate is
// never a bare boolean flag: it must always carry the exact recipient
// an admin has verified out-of-band, so a missing or empty argument,
// or the flag out of position, is a usage error — never an implicit
// "trust whatever the remote currently reports".
func TestDevicesApproveRotateRequiresRecipientArg(t *testing.T) {
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")

	for _, args := range [][]string{
		{"devices", "approve", "device-b", "--rotate"},
		{"devices", "approve", "--rotate", "device-b"},
		{"devices", "approve", "device-b", "--rotate", ""},
	} {
		_, errOut, code := run(t, args...)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Fatalf("%v must be a usage error, got %d %q", args, code, errOut)
		}
	}
}

// TestDevicesRotateClosesEvictedDeviceReplayAttack reproduces the
// live-repro review's full-circle attack end to end: remote.Sync used
// to register this device's own identity at the top of every sync,
// including one that then fails to decrypt. That let an evicted
// device — one whose old key was rotated out of trust, but which
// still holds a valid bearer token and network access — silently
// flip the remote's own bootstrap-roster recipient for its name back
// to its old key just by attempting (and failing) to sync, since a
// plain re-approve trusted that live value. This proves the fix
// closes it: the evicted device's failed sync changes nothing on the
// remote, and a replayed rotation still lands on the admin's own
// explicit, out-of-band-verified value.
func TestDevicesRotateClosesEvictedDeviceReplayAttack(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "add", "memory", "stack"); code != 0 {
		t.Fatalf("add memory failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}

	// B joins and is approved with its original key.
	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join failed: %s", errOut)
	}
	useDeviceEnv(t, baseA)
	if _, errOut, code := run(t, "devices", "approve", "device-b"); code != 0 {
		t.Fatalf("approve failed: %s", errOut)
	}

	// B, still on its original key, syncs successfully: it genuinely
	// holds current trust right now, and its own local devices.toml
	// now (accurately, at this moment) lists itself under this key.
	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B failed: %s", errOut)
	}

	// B is reinstalled (or otherwise loses its identity) and rejoins
	// with a brand-new key, under the same device name: a fresh,
	// legitimate "loadout join" always registers unconditionally, so
	// the remote's own bootstrap roster now reports the NEW key.
	baseBReinstalled := newDeviceEnv(t)
	useDeviceEnv(t, baseBReinstalled)
	run(t, "init")
	writeDeviceName(t, baseBReinstalled, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("rejoin failed: %s", errOut)
	}
	vBNew, err := vault.Open(filepath.Join(baseBReinstalled, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	_, newRecipient, err := vault.DeviceIdentity(vBNew)
	if err != nil {
		t.Fatal(err)
	}

	client := remote.NewClient(ts.URL, token)
	registered, err := client.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := findDevice(registered, "device-b"); got != newRecipient {
		t.Fatalf("the rejoin must register the new key with the remote, got %q want %q", got, newRecipient)
	}

	// The admin verifies newRecipient out-of-band (this is exactly
	// what a real admin reads from "loadout device" on the
	// reinstalled machine) and rotates to it, evicting the old key.
	useDeviceEnv(t, baseA)
	out, errOut, code := run(t, "devices", "approve", "device-b", "--rotate", newRecipient)
	if code != 0 {
		t.Fatalf("rotate failed: %s", errOut)
	}
	if out != fmt.Sprintf("rotated device-b to %s.\n", newRecipient) {
		t.Fatalf("bad rotate output: %q", out)
	}

	// THE ATTACK: the evicted, old-key copy of B — its own local
	// devices.toml still (stalely) lists itself under its own,
	// now-evicted recipient, from its last successful sync above —
	// runs sync --remote. It must fail to decrypt, and it must NOT
	// re-assert its old recipient onto the remote's bootstrap roster:
	// without the fix, this call's own RegisterDevice would silently
	// overwrite the remote's roster entry for "device-b" from
	// newRecipient back to the old, evicted one.
	useDeviceEnv(t, baseB)
	_, errOut, code = run(t, "sync", "--remote")
	if code != 1 {
		t.Fatalf("the evicted device's sync must fail, got %d", code)
	}
	if !strings.Contains(errOut, "this device cannot decrypt the snapshot") {
		t.Fatalf("bad error for the evicted device's sync: %q", errOut)
	}

	afterAttack, err := client.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := findDevice(afterAttack, "device-b"); got != newRecipient {
		t.Fatalf("the evicted device's failed sync must never change the remote's registered recipient: got %q, want it to remain %q", got, newRecipient)
	}

	// Full-circle replay: even if an admin, worried by the attempt,
	// re-runs the exact same rotation again, it still lands on the
	// admin's own explicit value — the old key is never re-admitted —
	// and the evicted device still cannot decrypt.
	useDeviceEnv(t, baseA)
	out, errOut, code = run(t, "devices", "approve", "device-b", "--rotate", newRecipient)
	if code != 0 {
		t.Fatalf("the replayed rotate failed: %s", errOut)
	}
	if out != fmt.Sprintf("rotated device-b to %s.\n", newRecipient) {
		t.Fatalf("bad replayed rotate output: %q", out)
	}
	vA, err := vault.Open(filepath.Join(baseA, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	rosterA, err := vault.ReadRoster(vA)
	if err != nil {
		t.Fatal(err)
	}
	if rosterA["device-b"] != newRecipient {
		t.Fatalf("the roster must still hold exactly the admin's chosen key after the replay, got %+v", rosterA)
	}

	useDeviceEnv(t, baseB)
	_, errOut, code = run(t, "sync", "--remote")
	if code != 1 {
		t.Fatalf("the evicted device must still be unable to decrypt after the replay, got %d", code)
	}
	if !strings.Contains(errOut, "this device cannot decrypt the snapshot") {
		t.Fatalf("bad error: %q", errOut)
	}

	// The reinstalled (now-trusted) device can decrypt normally.
	useDeviceEnv(t, baseBReinstalled)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("the newly-rotated-in device must be able to sync, got %d: %s", code, errOut)
	}
}

// onceOnLatestGET wraps a real server handler, running trigger exactly
// once — synchronously, before the request is forwarded to inner — the
// first time it sees a GET /v1/snapshots/latest request after arm is
// called. It lets a test land a competing server-side change at the
// exact instant a device's remote.Sync asks the remote for its latest
// version, deterministically, with no goroutines or races: the
// request's own goroutine runs trigger and blocks on it before the
// real handler (which reads the store fresh) ever runs.
type onceOnLatestGET struct {
	inner http.Handler
	mu    sync.Mutex
	armed bool
	fired bool
	fn    func()
}

func (h *onceOnLatestGET) arm(fn func()) {
	h.mu.Lock()
	h.armed = true
	h.fired = false
	h.fn = fn
	h.mu.Unlock()
}

func (h *onceOnLatestGET) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	fire := h.armed && !h.fired && r.Method == http.MethodGet && r.URL.Path == "/v1/snapshots/latest"
	if fire {
		h.fired = true
	}
	trigger := h.fn
	h.mu.Unlock()
	if fire && trigger != nil {
		trigger()
	}
	h.inner.ServeHTTP(w, r)
}

// pushCompetingRoster seeds the store directly with a snapshot holding
// exactly roster as devices.toml, built on top of parent — bypassing
// any device's own vault entirely, the way a concurrent admin action
// on a totally different waiting device would land a competing
// devices.toml on the remote. It returns the new version.
func pushCompetingRoster(t *testing.T, store *server.Store, parent string, roster map[string]string) string {
	t.Helper()
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, recipient := range roster {
		if err := vault.AddToRoster(v, name, recipient, vault.RoleFull); err != nil {
			t.Fatal(err)
		}
	}
	if err := vault.Snapshot(v, "competing roster"); err != nil {
		t.Fatal(err)
	}
	blob, _, err := vault.PackSnapshot(v)
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.PutSnapshot(parent, blob)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

// TestDevicesApproveReportsOverrideWhenConcurrentSyncDropsIt proves
// Important 2: devices.toml is one file, merged whole under
// last-write-wins. If a concurrent sync's incoming devices.toml
// differs from this approval's own local one (both changed since
// their shared base), the merge picks the incoming file entirely,
// silently dropping this approval — even though remote.Sync itself
// reports no error (a real, confirmed merge, just not the outcome the
// admin asked for). "devices approve" must catch this by re-reading
// the roster after its own remote.Sync, and refuse to report success
// when the just-approved name did not survive.
func TestDevicesApproveReportsOverrideWhenConcurrentSyncDropsIt(t *testing.T) {
	store, err := server.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(store, token, log.New(io.Discard, "", 0))
	handler := &onceOnLatestGET{inner: srv.Handler()}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("bootstrap sync on A failed: %s", errOut)
	}
	vA, err := vault.Open(filepath.Join(baseA, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	aName, aRecipient, err := vault.DeviceIdentity(vA)
	if err != nil {
		t.Fatal(err)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join B failed: %s", errOut)
	}

	competingIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	competingRecipient := competingIdentity.Recipient().String()

	// Arm the handler: the instant A's approve-triggered sync asks the
	// remote for its latest version, a competing admin action lands
	// first — a devices.toml that approves a totally DIFFERENT device,
	// never mentioning device-b at all.
	handler.arm(func() {
		info, err := store.Latest()
		if err != nil {
			t.Fatal(err)
		}
		pushCompetingRoster(t, store, info.Version, map[string]string{
			aName:      aRecipient,
			"device-x": competingRecipient,
		})
	})

	useDeviceEnv(t, baseA)
	out, errOut, code := run(t, "devices", "approve", "device-b")
	if code != 1 {
		t.Fatalf("an approval overridden by a concurrent sync must exit 1, not report false success, got %d (out=%q err=%q)", code, out, errOut)
	}
	want := "the approval of device-b was overridden by a concurrent sync. Fix: run loadout devices approve device-b again.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
	if strings.Contains(out, "approved device-b") {
		t.Fatal("must never print success for an approval a concurrent sync silently dropped")
	}

	// The roster on disk really does miss device-b: this is not a
	// false alarm.
	roster, err := vault.ReadRoster(vA)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := roster["device-b"]; ok {
		t.Fatal("test setup error: device-b's approval should have been overridden by the competing roster")
	}
	if _, ok := roster["device-x"]; !ok {
		t.Fatal("test setup error: the competing roster should have won the merge")
	}
}

// TestDevicesShowsFreshJoinerWaitingAndOwnerApproved proves the
// corrected steady-state picture once someone has actually been
// approved: the owner (already a real devices.toml member) shows
// approved, and a brand-new joiner nobody has approved yet shows
// waiting, never approved.
func TestDevicesShowsFreshJoinerWaitingAndOwnerApproved(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	writeDeviceName(t, base, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	// Establish A as a real devices.toml member by approving another
	// device: the self-lockout fix lands A's own identity too.
	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join B failed: %s", errOut)
	}
	useDeviceEnv(t, base)
	if _, errOut, code := run(t, "devices", "approve", "device-b"); code != 0 {
		t.Fatalf("approve B failed: %s", errOut)
	}

	// A fresh third device joins; nobody has approved it.
	baseC := newDeviceEnv(t)
	useDeviceEnv(t, baseC)
	run(t, "init")
	writeDeviceName(t, baseC, "device-c")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join C failed: %s", errOut)
	}

	useDeviceEnv(t, base)
	out, errOut, code := run(t, "devices")
	if code != 0 {
		t.Fatalf("devices failed: %s", errOut)
	}
	if !strings.Contains(out, "device-a — approved") {
		t.Fatalf("the owner must show approved, got %q", out)
	}
	if !strings.Contains(out, "device-c — waiting") {
		t.Fatalf("a fresh joiner must show waiting, got %q", out)
	}
	if strings.Contains(out, "device-c — approved") {
		t.Fatal("a fresh joiner must never show approved")
	}
}

// TestDevicesUnionIncludesLocalOnlyEntry proves a devices.toml entry
// with no matching row on the server's bootstrap roster still lists
// as approved: devices.toml is the real decrypt allowlist, and the
// server roster is only ever a hint about who is waiting.
func TestDevicesUnionIncludesLocalOnlyEntry(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	writeDeviceName(t, base, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "offline-device", identity.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add offline-device to the roster by hand"); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "devices")
	if code != 0 {
		t.Fatalf("devices failed: %s", errOut)
	}
	if !strings.Contains(out, "offline-device — approved") {
		t.Fatalf("a devices.toml-only entry must still list as approved, got %q", out)
	}
}

// flakyHandler wraps a real handler, refusing every request except
// GET /v1/devices while broken is true: it simulates a remote that
// stays reachable for a lookup but fails a push, without needing to
// break the local vault's git history to do it.
type flakyHandler struct {
	inner  http.Handler
	broken bool
}

func (h *flakyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.broken && !(r.Method == http.MethodGet && r.URL.Path == "/v1/devices") {
		http.Error(w, "simulated network failure", http.StatusServiceUnavailable)
		return
	}
	h.inner.ServeHTTP(w, r)
}

// TestDevicesApproveIdempotentRetriesAFailedSync proves Important 3:
// when the roster already lists a name with a matching recipient (the
// idempotent branch) but the last remote.Sync failed, re-running
// approve must still call remote.Sync — otherwise a newcomer whose
// first push failed would be told "already approved" forever, with no
// way for the CLI to ever actually deliver the updated roster.
func TestDevicesApproveIdempotentRetriesAFailedSync(t *testing.T) {
	store, err := server.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(store, token, log.New(io.Discard, "", 0))
	flaky := &flakyHandler{inner: srv.Handler()}
	ts := httptest.NewServer(flaky)
	t.Cleanup(ts.Close)

	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	writeDeviceName(t, base, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "add", "memory", "stack"); code != 0 {
		t.Fatalf("add memory failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join failed: %s", errOut)
	}

	useDeviceEnv(t, base)
	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}

	// Break the remote's write paths: the roster lookup still works,
	// but the follow-up sync's push fails.
	flaky.broken = true
	if _, errOut, code := run(t, "devices", "approve", "device-b"); code != 1 {
		t.Fatalf("approve must fail when the follow-up sync fails, got %d: %s", code, errOut)
	}
	roster, err := vault.ReadRoster(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := roster["device-b"]; !ok {
		t.Fatal("the roster write must land locally even when the follow-up sync fails")
	}

	// Restore the remote, then re-run the same approve. It hits the
	// idempotent same-recipient branch, but must still call
	// remote.Sync to retry the push that failed above.
	flaky.broken = false
	out, errOut, code := run(t, "devices", "approve", "device-b")
	if code != 0 {
		t.Fatalf("the retried approve must succeed: %s", errOut)
	}
	if out != "device-b is already approved.\n" {
		t.Fatalf("bad output: %q", out)
	}

	// B can now actually decrypt, proving the retried sync really
	// pushed the updated roster this time.
	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B must now succeed, proving the retried sync really pushed: %s", errOut)
	}
}

// --- Task 4: secrets sync + re-encrypt on approval ---

// writeOrphanSecret hand-crafts a secret directly on v's filesystem,
// its value.age encrypted ONLY to stranger — never to v's own device
// key, and never to anyone in v's roster. It reproduces the shape a
// secret some other, long-gone device added would take: one the
// device now running "devices approve" genuinely cannot decrypt.
// value never appears anywhere outside this function's own age
// encryption call.
func writeOrphanSecret(t *testing.T, v *vault.Vault, name, value string, stranger *age.X25519Identity) {
	t.Helper()
	dir := filepath.Join(v.SecretsDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := "---\nname: " + name + "\nservice: orphan\nhook: \nrotate_after: \nby: human\nat: 2024-01-01T00:00:00Z\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.md"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, stranger.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "value.age"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "hand-craft an orphan secret"); err != nil {
		t.Fatal(err)
	}
}

// TestSecretSyncsToAlreadyApprovedDevice proves the base case Task 4
// builds on: secrets/ ciphertext syncs through the ordinary Phase 4
// snapshot exactly like skills and memory. B is already approved
// BEFORE A ever creates the secret, so no re-encrypt is even needed —
// B is already a recipient at AddSecret time — and B's sync still
// decrypts it correctly.
func TestSecretSyncsToAlreadyApprovedDevice(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("bootstrap sync on A failed: %s", errOut)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join B failed: %s", errOut)
	}

	useDeviceEnv(t, baseA)
	if _, errOut, code := run(t, "devices", "approve", "device-b"); code != 0 {
		t.Fatalf("approve B failed: %s", errOut)
	}

	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B failed: %s", errOut)
	}

	// B is now fully approved and already caught up. A creates a
	// brand-new secret only after that.
	useDeviceEnv(t, baseA)
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add on A failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}

	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B failed: %s", errOut)
	}
	out, errOut, code := run(t, "secret", "show", "openai-key", "--reveal")
	if code != 0 {
		t.Fatalf("secret show --reveal on B failed: %s", errOut)
	}
	if out != dummySecretValue {
		t.Fatalf("B's decrypted secret = %q, want %q", out, dummySecretValue)
	}
}

// TestNewlyApprovedDeviceDecryptsPreExistingSecret is the headline
// Task 4 test: device C is approved AFTER A already created a secret.
// Before approval, C cannot even decrypt the snapshot at all (it is
// not yet a recipient of anything). Approving C must re-encrypt the
// PRE-EXISTING secret to the new roster — not just carry it forward
// unchanged — so that once C syncs, it can decrypt a secret it never
// witnessed being created.
func TestNewlyApprovedDeviceDecryptsPreExistingSecret(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	run(t, "remote", "add", ts.URL, token)

	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add on A failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}

	baseC := newDeviceEnv(t)
	useDeviceEnv(t, baseC)
	run(t, "init")
	writeDeviceName(t, baseC, "device-c")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join C failed: %s", errOut)
	}

	// Before approval: C is not a recipient of anything A has pushed,
	// secrets included, so its sync fails cleanly.
	_, errOut, code := run(t, "sync", "--remote")
	if code != 1 {
		t.Fatalf("sync on C before approval must fail, got %d", code)
	}
	if !strings.Contains(errOut, "this device cannot decrypt the snapshot") {
		t.Fatalf("bad error for C's pre-approval sync: %q", errOut)
	}

	// A approves C: this must re-encrypt the pre-existing secret to
	// the roster as it now stands (A and C) before A's own sync
	// pushes it.
	useDeviceEnv(t, baseA)
	if _, errOut, code := run(t, "devices", "approve", "device-c"); code != 0 {
		t.Fatalf("approve C failed: %s", errOut)
	}

	// C syncs and must now decrypt the secret it never saw created —
	// proof ReEncryptSecrets actually ran during the approval.
	useDeviceEnv(t, baseC)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on C failed: %s", errOut)
	}
	out, errOut, code := run(t, "secret", "show", "openai-key", "--reveal")
	if code != 0 {
		t.Fatalf("secret show --reveal on C failed: %s", errOut)
	}
	if out != dummySecretValue {
		t.Fatalf("C's decrypted secret = %q, want %q", out, dummySecretValue)
	}
}

// TestSecretBearingSnapshotNeverHoldsPlaintextOnServer proves
// INVARIANT 8 end to end for secrets: the raw bytes loadoutd stores
// for a secret-bearing snapshot never contain the dummy secret value
// — it exists only doubly-wrapped, inside value.age's own age layer,
// itself inside the outer snapshot's age layer.
func TestSecretBearingSnapshotNeverHoldsPlaintextOnServer(t *testing.T) {
	ts, token, store := newRemoteTestServerWithStore(t)

	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	writeDeviceName(t, base, "device-a")
	run(t, "remote", "add", ts.URL, token)

	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	info, err := store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if info.Version == "" {
		t.Fatal("the server must hold a snapshot version after the sync")
	}
	blob, err := os.ReadFile(filepath.Join(store.Root, "blobs", info.Version))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte(dummySecretValue)) {
		t.Fatal("INVARIANT 8: the server-stored snapshot blob must never contain the plaintext secret value")
	}
}

// TestDevicesApproveWarnsOnUndecryptableSecretButStillSyncs proves the
// partial-success contract: a secret the approving device itself
// cannot decrypt (a hand-crafted orphan here, standing in for one some
// other, now-gone device added) is skipped with a warning that names
// it, and the approval still succeeds and still syncs — a decryptable
// secret added alongside it still reaches the new device normally.
// The orphan's own value never appears in any output.
func TestDevicesApproveWarnsOnUndecryptableSecretButStillSyncs(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")
	writeDeviceName(t, base, "device-a")
	run(t, "remote", "add", ts.URL, token)

	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	const orphanValue = "orphan-secret-do-not-leak"
	stranger, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	writeOrphanSecret(t, v, "orphan-key", orphanValue, stranger)

	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	baseC := newDeviceEnv(t)
	useDeviceEnv(t, baseC)
	run(t, "init")
	writeDeviceName(t, baseC, "device-c")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join C failed: %s", errOut)
	}

	useDeviceEnv(t, base)
	out, errOut, code := run(t, "devices", "approve", "device-c")
	if code != 0 {
		t.Fatalf("approve must still succeed despite one undecryptable secret: %s", errOut)
	}
	wantWarning := "warning: could not re-encrypt these secrets for the new device (this device cannot read them): orphan-key. Fix: re-run approve from a device that can read them.\n"
	if !strings.Contains(errOut, wantWarning) {
		t.Fatalf("bad warning: got %q want it to contain %q", errOut, wantWarning)
	}
	if strings.Contains(out, orphanValue) || strings.Contains(errOut, orphanValue) {
		t.Fatal("the warning must never leak the secret's value")
	}

	// The OTHER, decryptable secret still reaches C normally.
	useDeviceEnv(t, baseC)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on C failed: %s", errOut)
	}
	got, errOut, code := run(t, "secret", "show", "openai-key", "--reveal")
	if code != 0 {
		t.Fatalf("secret show on C failed: %s", errOut)
	}
	if got != dummySecretValue {
		t.Fatalf("C's decrypted secret = %q, want %q", got, dummySecretValue)
	}
}

// TestDevicesApproveAlreadyMatchesRetriesReEncryption reproduces the
// exact interrupted-approval state a crash between AddToRoster and
// ReEncryptSecrets would leave behind: devices.toml already lists the
// name (hand-written here, standing in for a prior approve that
// persisted the roster write but never got to re-encrypt), while
// value.age is still encrypted only to the original device. A plain
// re-approve must hit the approveAlreadyMatches path AND still
// complete the re-encryption — not silently report "already approved"
// forever while the newcomer stays unable to read the secret.
func TestDevicesApproveAlreadyMatchesRetriesReEncryption(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	run(t, "remote", "add", ts.URL, token)

	// The secret is created while the roster is still empty, so it is
	// encrypted only to A's own identity — exactly the ciphertext a
	// real interrupted approval would have left B unable to read.
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add on A failed: %s", errOut)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join B failed: %s", errOut)
	}
	vB, err := vault.Open(filepath.Join(baseB, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	_, bRecipient, err := vault.DeviceIdentity(vB)
	if err != nil {
		t.Fatal(err)
	}
	bKeyData, err := os.ReadFile(filepath.Join(vB.Root, "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	bIdentity, err := age.ParseX25519Identity(strings.TrimSpace(string(bKeyData)))
	if err != nil {
		t.Fatal(err)
	}

	// Hand-write devices.toml directly — bypassing "devices approve"
	// entirely — to simulate a prior approval that got exactly this
	// far: the roster write landed and was committed, but
	// ReEncryptSecrets never ran (or failed) before the process died.
	vA, err := vault.Open(filepath.Join(baseA, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	_, aRecipient, err := vault.DeviceIdentity(vA)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(vA, "device-a", aRecipient, vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(vA, "device-b", bRecipient, vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(vA, "simulate a roster write with no re-encrypt yet"); err != nil {
		t.Fatal(err)
	}

	// Precondition: B genuinely cannot decrypt the secret yet.
	ciphertext, err := os.ReadFile(filepath.Join(vA.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), bIdentity); err == nil {
		t.Fatal("test setup error: B must not be able to decrypt the secret before the retried approve runs")
	}

	// The retry: a plain re-approve of a name the roster already
	// lists under the exact same recipient.
	useDeviceEnv(t, baseA)
	out, errOut, code := run(t, "devices", "approve", "device-b")
	if code != 0 {
		t.Fatalf("retried approve failed: %s", errOut)
	}
	if out != "device-b is already approved.\n" {
		t.Fatalf("bad retry output: %q", out)
	}

	// B syncs and must now decrypt the secret: the retry actually
	// completed the interrupted re-encryption.
	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B failed: %s", errOut)
	}
	got, errOut, code := run(t, "secret", "show", "openai-key", "--reveal")
	if code != 0 {
		t.Fatalf("secret show on B failed: %s", errOut)
	}
	if got != dummySecretValue {
		t.Fatalf("B's decrypted secret = %q, want %q", got, dummySecretValue)
	}
}

// TestDevicesApproveRotateReEncryptsSecretsAndRevokesOldKey proves the
// --rotate call site's effect on secrets end to end: a secret that
// existed before a device's key is rotated is re-encrypted to the NEW
// key, and the OLD, now-revoked key can no longer decrypt it — true
// revocation (the old recipient is dropped, not merely joined by a
// new one), not just an added recipient.
func TestDevicesApproveRotateReEncryptsSecretsAndRevokesOldKey(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("bootstrap sync on A failed: %s", errOut)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join B failed: %s", errOut)
	}
	// Capture B's ORIGINAL identity before it is ever rotated out.
	oldKeyData, err := os.ReadFile(filepath.Join(baseB, "vault", "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	oldIdentity, err := age.ParseX25519Identity(strings.TrimSpace(string(oldKeyData)))
	if err != nil {
		t.Fatal(err)
	}

	useDeviceEnv(t, baseA)
	if _, errOut, code := run(t, "devices", "approve", "device-b"); code != 0 {
		t.Fatalf("approve B failed: %s", errOut)
	}

	// A creates a secret while B, under its ORIGINAL key, is a fully
	// approved recipient.
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}

	ciphertext, err := os.ReadFile(filepath.Join(baseA, "vault", "secrets", "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), oldIdentity); err != nil {
		t.Fatalf("test setup error: B's original key must decrypt the secret before rotation: %v", err)
	}

	// The admin verifies a brand-new key out-of-band (standing in for
	// a reinstalled or re-keyed device-b) and rotates to it.
	newIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	newRecipient := newIdentity.Recipient().String()
	out, errOut, code := run(t, "devices", "approve", "device-b", "--rotate", newRecipient)
	if code != 0 {
		t.Fatalf("rotate failed: %s", errOut)
	}
	if out != fmt.Sprintf("rotated device-b to %s.\n", newRecipient) {
		t.Fatalf("bad rotate output: %q", out)
	}

	ciphertext, err = os.ReadFile(filepath.Join(baseA, "vault", "secrets", "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), newIdentity)
	if err != nil {
		t.Fatalf("the NEW key must decrypt the pre-existing secret after rotation: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != dummySecretValue {
		t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
	}

	if _, err := age.Decrypt(bytes.NewReader(ciphertext), oldIdentity); err == nil {
		t.Fatal("the OLD, rotated-out key must no longer decrypt the secret: revocation must actually drop it as a recipient")
	}
}

// --- Task 3: approve --no-secrets, re-encrypt, and the no-secrets proof ---

// readDeviceIdentity reads and parses base's own device.key, so a test
// can attempt a raw age.Decrypt with that device's private key
// directly — no CLI, no vault package call — the strongest possible
// proof that a device really cannot read a secret's value.
func readDeviceIdentity(t *testing.T, base string) *age.X25519Identity {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(base, "vault", "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// TestNoSecretsDeviceSyncsVaultButCannotDecryptSecret is the headline
// Phase 8a proof. Full device A creates a skill, a memory fact, and a
// dummy secret. A approves device B as --no-secrets. B syncs and
// reads the skill and the fact — the whole vault still reaches it —
// but can never read the secret's value, proven two ways:
// vault.DecryptSecret itself refuses (through "secret show
// --reveal"), and B's own raw age identity cannot decrypt the raw
// value.age ciphertext bytes it actually holds on disk (the outer
// snapshot syncs to every device regardless of role; only the
// secret's own inner encryption excludes a no-secrets device). A
// third device, C, approved normally (full), CAN decrypt the same
// secret. Finally, A re-approves B as full — a role change on
// re-approve, the same recipient, a different role — and after the
// resulting re-encrypt and sync, B can now decrypt too: proof the
// role-change path really re-encrypts, not just relabels.
func TestNoSecretsDeviceSyncsVaultButCannotDecryptSecret(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	// A: the bootstrap, full device. It creates a skill, a memory
	// fact, and a dummy secret, then pushes all three.
	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	if _, errOut, code := run(t, "remote", "add", ts.URL, token); code != 0 {
		t.Fatalf("remote add on A failed: %s", errOut)
	}
	if _, errOut, code := run(t, "add", "skill", "deploy-checks"); code != 0 {
		t.Fatalf("add skill on A failed: %s", errOut)
	}
	if _, errOut, code := run(t, "add", "memory", "stack"); code != 0 {
		t.Fatalf("add memory on A failed: %s", errOut)
	}
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "dummy-secret", "--service", "dummy"); code != 0 {
		t.Fatalf("secret add on A failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}

	// B joins and waits.
	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join B failed: %s", errOut)
	}

	// A approves B as no-secrets.
	useDeviceEnv(t, baseA)
	approveOut, approveErr, code := run(t, "devices", "approve", "device-b", "--no-secrets")
	if code != 0 {
		t.Fatalf("approve B --no-secrets failed: %s", approveErr)
	}
	if !strings.Contains(approveOut, "as no-secrets") {
		t.Fatalf("approve output must say the device is no-secrets, got %q", approveOut)
	}

	// devices list shows each device's role.
	out, errOut, code := run(t, "devices")
	if code != 0 {
		t.Fatalf("devices on A failed: %s", errOut)
	}
	if !strings.Contains(out, "device-b — approved (no-secrets)") {
		t.Fatalf("devices list must show B's role as no-secrets, got %q", out)
	}
	if !strings.Contains(out, "device-a — approved (full)") {
		t.Fatalf("devices list must show A's own role as full, got %q", out)
	}

	// B syncs. It must receive the whole vault — the skill and the
	// fact both — but must never be able to decrypt the secret.
	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B failed: %s", errOut)
	}
	skillData, err := os.ReadFile(filepath.Join(baseB, "vault", "skills", "deploy-checks", "SKILL.md"))
	if err != nil {
		t.Fatalf("a no-secrets device must still receive the skill: %v", err)
	}
	if !strings.Contains(string(skillData), "deploy-checks") {
		t.Fatalf("B's skill content looks wrong: %q", skillData)
	}
	factData, err := os.ReadFile(filepath.Join(baseB, "vault", "memory", "stack.md"))
	if err != nil {
		t.Fatalf("a no-secrets device must still receive the memory fact: %v", err)
	}
	if !strings.Contains(string(factData), "stack") {
		t.Fatalf("B's fact content looks wrong: %q", factData)
	}

	// Proof 1: vault.DecryptSecret (through "secret show --reveal")
	// refuses.
	_, secretErr, code := run(t, "secret", "show", "dummy-secret", "--reveal")
	if code != 1 {
		t.Fatalf("a no-secrets device must fail to decrypt the secret, got exit %d", code)
	}
	if !strings.Contains(secretErr, "this device cannot read the secret") {
		t.Fatalf("bad error for a no-secrets device's decrypt attempt: %q", secretErr)
	}
	if strings.Contains(secretErr, dummySecretValue) {
		t.Fatal("the failed decrypt error must never leak the secret's value")
	}

	// Proof 2: B's own raw age identity cannot decrypt the raw
	// value.age bytes it actually holds on disk. The outer snapshot
	// synced the ciphertext file to B (every device gets it,
	// regardless of role); only the secret's own inner encryption
	// excludes B — this is the load-bearing proof, independent of
	// vault.DecryptSecret's own error handling.
	bIdentity := readDeviceIdentity(t, baseB)
	ciphertext, err := os.ReadFile(filepath.Join(baseB, "vault", "secrets", "dummy-secret", "value.age"))
	if err != nil {
		t.Fatalf("B must still hold the secret's ciphertext on disk (the outer snapshot syncs to every device): %v", err)
	}
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), bIdentity); err == nil {
		t.Fatal("B's own raw age key must never decrypt the secret's value.age")
	}

	// C: a second new device, approved normally (full).
	baseC := newDeviceEnv(t)
	useDeviceEnv(t, baseC)
	run(t, "init")
	writeDeviceName(t, baseC, "device-c")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join C failed: %s", errOut)
	}
	useDeviceEnv(t, baseA)
	if _, errOut, code := run(t, "devices", "approve", "device-c"); code != 0 {
		t.Fatalf("approve C failed: %s", errOut)
	}
	useDeviceEnv(t, baseC)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on C failed: %s", errOut)
	}
	got, errOut, code := run(t, "secret", "show", "dummy-secret", "--reveal")
	if code != 0 {
		t.Fatalf("a full device must decrypt the secret: %s", errOut)
	}
	if got != dummySecretValue {
		t.Fatalf("C's decrypted secret = %q, want %q", got, dummySecretValue)
	}

	// The role change: A re-approves B as full (no --no-secrets). B's
	// recipient is unchanged, only its role flips — the
	// approveRoleChanged path.
	useDeviceEnv(t, baseA)
	roleChangeOut, errOut, code := run(t, "devices", "approve", "device-b")
	if code != 0 {
		t.Fatalf("re-approving B as full failed: %s", errOut)
	}
	if roleChangeOut != "changed device-b's role to full.\n" {
		t.Fatalf("bad role-change output: %q", roleChangeOut)
	}

	// devices list now shows B as full.
	out, errOut, code = run(t, "devices")
	if code != 0 {
		t.Fatalf("devices on A failed: %s", errOut)
	}
	if !strings.Contains(out, "device-b — approved (full)") {
		t.Fatalf("devices list must show B's new role as full, got %q", out)
	}

	// After the role change's own re-encrypt and sync (both already
	// run inside "devices approve"), B syncs and can now decrypt the
	// secret it could not read a moment ago.
	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on B failed after the role change: %s", errOut)
	}
	got, errOut, code = run(t, "secret", "show", "dummy-secret", "--reveal")
	if code != 0 {
		t.Fatalf("B must now decrypt the secret after being promoted to full: %s", errOut)
	}
	if got != dummySecretValue {
		t.Fatalf("B's decrypted secret = %q, want %q", got, dummySecretValue)
	}
}

// TestDevicesApproveRotateKeepsExistingRoleUnlessOverridden proves
// --rotate's role handling: a plain rotate (no role flag) keeps the
// device's existing role, --full promotes it, and --no-secrets
// demotes it — a rotate can always change a key, and only changes a
// role when the admin explicitly asks for that too.
func TestDevicesApproveRotateKeepsExistingRoleUnlessOverridden(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join B failed: %s", errOut)
	}

	useDeviceEnv(t, baseA)
	if _, errOut, code := run(t, "devices", "approve", "device-b", "--no-secrets"); code != 0 {
		t.Fatalf("approve B --no-secrets failed: %s", errOut)
	}

	v, err := vault.Open(filepath.Join(baseA, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := vault.ReadRosterEntries(v)
	if err != nil {
		t.Fatal(err)
	}
	if entries["device-b"].Role != vault.RoleNoSecrets {
		t.Fatalf("B must start no-secrets, got %+v", entries["device-b"])
	}

	// A plain rotate (no role flag) must KEEP the existing role.
	newIdentity1, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run(t, "devices", "approve", "device-b", "--rotate", newIdentity1.Recipient().String()); code != 0 {
		t.Fatalf("plain rotate failed: %s", errOut)
	}
	entries, err = vault.ReadRosterEntries(v)
	if err != nil {
		t.Fatal(err)
	}
	if entries["device-b"].Role != vault.RoleNoSecrets {
		t.Fatalf("a plain rotate must keep the existing role, got %+v", entries["device-b"])
	}
	if entries["device-b"].Recipient != newIdentity1.Recipient().String() {
		t.Fatalf("the rotate must still write the new recipient, got %+v", entries["device-b"])
	}

	// --rotate ... --full promotes it.
	newIdentity2, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "devices", "approve", "device-b", "--rotate", newIdentity2.Recipient().String(), "--full")
	if code != 0 {
		t.Fatalf("rotate --full failed: %s", errOut)
	}
	wantFull := fmt.Sprintf("rotated device-b to %s.\n", newIdentity2.Recipient().String())
	if out != wantFull {
		t.Fatalf("bad rotate --full output: got %q want %q", out, wantFull)
	}
	entries, err = vault.ReadRosterEntries(v)
	if err != nil {
		t.Fatal(err)
	}
	if entries["device-b"].Role != vault.RoleFull {
		t.Fatalf("--full must promote the device, got %+v", entries["device-b"])
	}

	// --rotate ... --no-secrets demotes it again.
	newIdentity3, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code = run(t, "devices", "approve", "device-b", "--rotate", newIdentity3.Recipient().String(), "--no-secrets")
	if code != 0 {
		t.Fatalf("rotate --no-secrets failed: %s", errOut)
	}
	wantNoSecrets := fmt.Sprintf("rotated device-b to %s (no-secrets).\n", newIdentity3.Recipient().String())
	if out != wantNoSecrets {
		t.Fatalf("bad rotate --no-secrets output: got %q want %q", out, wantNoSecrets)
	}
	entries, err = vault.ReadRosterEntries(v)
	if err != nil {
		t.Fatal(err)
	}
	if entries["device-b"].Role != vault.RoleNoSecrets {
		t.Fatalf("--no-secrets must demote the device, got %+v", entries["device-b"])
	}
}

// TestDevicesApproveConflictingRoleFlagsIsUsageError proves
// "--no-secrets" and "--full" together, in either order, on a plain
// approve or a --rotate, is a usage error: a device cannot ask for
// both roles in the same call.
func TestDevicesApproveConflictingRoleFlagsIsUsageError(t *testing.T) {
	base := newDeviceEnv(t)
	useDeviceEnv(t, base)
	run(t, "init")

	for _, args := range [][]string{
		{"devices", "approve", "device-b", "--no-secrets", "--full"},
		{"devices", "approve", "device-b", "--full", "--no-secrets"},
		{"devices", "approve", "device-b", "--rotate", "some-recipient", "--no-secrets", "--full"},
	} {
		_, errOut, code := run(t, args...)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Fatalf("%v must be a usage error, got %d %q", args, code, errOut)
		}
	}
}

// TestDevicesJSONIncludesRole proves the JSON shape carries role
// information on both "devices" and "devices approve": the list's
// per-device "role" field, and the approve result's "role" and
// "role_changed" fields.
func TestDevicesJSONIncludesRole(t *testing.T) {
	ts, token := newRemoteTestServer(t)
	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	run(t, "remote", "add", ts.URL, token)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}

	baseB := newDeviceEnv(t)
	useDeviceEnv(t, baseB)
	run(t, "init")
	writeDeviceName(t, baseB, "device-b")
	if _, errOut, code := run(t, "join", ts.URL, token); code != 0 {
		t.Fatalf("join B failed: %s", errOut)
	}

	useDeviceEnv(t, baseA)
	approveOut, errOut, code := run(t, "devices", "approve", "device-b", "--no-secrets", "--json")
	if code != 0 {
		t.Fatalf("approve --json failed: %s", errOut)
	}
	var approveGot struct {
		Role        string `json:"role"`
		RoleChanged bool   `json:"role_changed"`
	}
	if err := json.Unmarshal([]byte(approveOut), &approveGot); err != nil {
		t.Fatalf("approve --json did not parse: %v\noutput: %s", err, approveOut)
	}
	if approveGot.Role != "no-secrets" || approveGot.RoleChanged {
		t.Fatalf("bad approve json role: %+v", approveGot)
	}

	out, errOut, code := run(t, "devices", "--json")
	if code != 0 {
		t.Fatalf("devices --json failed: %s", errOut)
	}
	var got struct {
		Devices []struct {
			Name  string `json:"name"`
			Role  string `json:"role"`
			State string `json:"state"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("devices --json did not parse: %v\noutput: %s", err, out)
	}
	roles := make(map[string]string, len(got.Devices))
	for _, d := range got.Devices {
		roles[d.Name] = d.Role
	}
	if roles["device-a"] != "full" {
		t.Fatalf("device-a must show role full, got %+v", roles)
	}
	if roles["device-b"] != "no-secrets" {
		t.Fatalf("device-b must show role no-secrets, got %+v", roles)
	}
}
