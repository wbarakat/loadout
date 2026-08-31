package cli_test

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"loadout.dev/loadout/internal/server"
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
	if err := vault.AddToRoster(v, "device-a", recipient); err != nil {
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
// (a silent overwrite here would be a spoofing vector), shows up as
// the "re-keyed" state, and only actually rotates the key when
// --rotate is passed.
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

	useDeviceEnv(t, base)
	if _, errOut, code := run(t, "devices", "approve", "device-b"); code != 0 {
		t.Fatalf("approve failed: %s", errOut)
	}

	// Simulate a re-key (or an imposter): the server now holds a
	// different, still-valid recipient for the same name.
	newIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDevice("device-b", newIdentity.Recipient().String()); err != nil {
		t.Fatal(err)
	}

	// A plain re-approve must refuse, not silently overwrite.
	_, errOut, code := run(t, "devices", "approve", "device-b")
	if code != 1 {
		t.Fatalf("a mismatched re-approve without --rotate must exit 1, got %d", code)
	}
	want := "device-b is already approved with a different key. This is a re-keyed device or an imposter. Fix: run loadout devices approve device-b --rotate only if you trust the new key.\n"
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
	if roster["device-b"] == newIdentity.Recipient().String() {
		t.Fatal("a mismatched re-approve without --rotate must not overwrite the roster")
	}

	// devices shows the re-keyed state.
	out, errOut, code := run(t, "devices")
	if code != 0 {
		t.Fatalf("devices failed: %s", errOut)
	}
	if !strings.Contains(out, "device-b — re-keyed (run: loadout devices approve device-b --rotate)") {
		t.Fatalf("a mismatched device must show the re-keyed state, got %q", out)
	}

	// --rotate performs the replacement deliberately.
	out2, errOut2, code2 := run(t, "devices", "approve", "device-b", "--rotate")
	if code2 != 0 {
		t.Fatalf("devices approve --rotate failed: %s", errOut2)
	}
	if out2 != "rotated device-b to a new key.\n" {
		t.Fatalf("bad rotate output: %q", out2)
	}
	roster, err = vault.ReadRoster(v)
	if err != nil {
		t.Fatal(err)
	}
	if roster["device-b"] != newIdentity.Recipient().String() {
		t.Fatalf("--rotate must overwrite the roster with the new recipient, got %+v", roster)
	}

	// devices now shows device-b cleanly approved again, not re-keyed.
	out, errOut, code = run(t, "devices")
	if code != 0 {
		t.Fatalf("devices failed: %s", errOut)
	}
	if !strings.Contains(out, "device-b — approved") {
		t.Fatalf("after rotation device-b must show approved, not re-keyed, got %q", out)
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
	if err := vault.AddToRoster(v, "offline-device", identity.Recipient().String()); err != nil {
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
