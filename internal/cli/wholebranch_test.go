package cli_test

// Regression test for the 2026-09-01 Phase 8a whole-branch fix wave.
// See .superpowers/sdd/2026-09-01-phase-8a-device-roles/wholebranch-fixwave-report.md.
//
// THE CRITICAL FINDING (Attack 4): a no-secrets device could decrypt
// the CURRENT value of a secret, right after a concurrent, role-
// affecting merge — even though devices.toml already, correctly,
// called it no-secrets.
//
// Live reproduction, three full devices A, B, and C, all already
// synced on one secret:
//
//   - C rotates the secret to a new value and pushes. C's own roster
//     still calls B "full", so C's rotated ciphertext still names B a
//     recipient.
//   - A, before it ever pulls C's rotate, demotes B to no-secrets. A
//     re-encrypts its own local secrets, excluding B, using its own
//     (older) view of the secret's value, then syncs.
//   - A's sync pulls and merges C's already-pushed rotate. The merge
//     keeps A's own devices.toml (it changed since the shared base;
//     C's did not), so B correctly shows no-secrets. But the secret's
//     value.age changed on BOTH sides since that base (A's own
//     re-encrypt, and C's rotate), so the merge's own "both changed"
//     rule lets the INCOMING copy win — C's, still encrypted under
//     the stale, pre-demotion roster.
//
// Before the fix, nothing ever revisited value.age after a merge
// landed it: B could decrypt the CURRENT secret, despite devices.toml
// already and correctly calling it no-secrets. Fixed in
// internal/remote/sync.go's pullMergePush: a full device now
// re-encrypts every secret to the merged roster on every merge,
// before republishing.
//
// This test must never be deleted or weakened: it is the proof the
// fix holds. To see it fail against the pre-fix code, revert the
// added block in pullMergePush (the "role, err := vault.SelfRole(v)"
// block right after mergeInto) and re-run it: B's raw age.Decrypt
// then succeeds against the rotated value, and the test fails.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// TestNoSecretsDeviceCannotDecryptSecretRotatedDuringConcurrentDemote
// is the Critical finding's acceptance gate: it reproduces Attack 4
// end to end over ordinary CLI calls (three vaults, one httptest
// loadoutd, dummy secret values), and proves the demoted device can
// never read the CURRENT secret once every device has converged.
func TestNoSecretsDeviceCannotDecryptSecretRotatedDuringConcurrentDemote(t *testing.T) {
	ts, token := newRemoteTestServer(t)

	// A: the bootstrap, full device. It creates the secret and pushes.
	baseA := newDeviceEnv(t)
	useDeviceEnv(t, baseA)
	run(t, "init")
	writeDeviceName(t, baseA, "device-a")
	if _, errOut, code := run(t, "remote", "add", ts.URL, token); code != 0 {
		t.Fatalf("remote add on A failed: %s", errOut)
	}
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "rotating-key", "--service", "svc"); code != 0 {
		t.Fatalf("secret add on A failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on A failed: %s", errOut)
	}

	// B joins and is approved full: the eventual victim, currently a
	// legitimate, fully-trusted recipient of the secret.
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

	// C joins and is approved full.
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

	// A, B, and C are all now full and converged on the secret's
	// original value. Confirm B can read it right now — it is a
	// legitimate recipient before the attack, not by accident.
	useDeviceEnv(t, baseB)
	got, errOut, code := run(t, "secret", "show", "rotating-key", "--reveal")
	if code != 0 || got != dummySecretValue {
		t.Fatalf("test setup error: B must start able to read the secret, got %q err=%q code=%d", got, errOut, code)
	}

	const rotatedValue = "rotated-secret-value-456"

	// C rotates the secret to a NEW value and pushes. C's own roster
	// still lists B as full, so the rotated ciphertext still names B a
	// recipient.
	useDeviceEnv(t, baseC)
	if _, errOut, code := runWithStdin(t, rotatedValue, "secret", "rotate", "rotating-key"); code != 0 {
		t.Fatalf("secret rotate on C failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote on C (rotate push) failed: %s", errOut)
	}

	// A, WITHOUT ever having pulled C's rotate, demotes B to
	// no-secrets. Its own approve re-encrypts A's LOCAL secrets
	// excluding B (using A's own, not-yet-updated view of the value),
	// then syncs — which pulls and MERGES C's already-pushed rotate.
	useDeviceEnv(t, baseA)
	demoteOut, errOut, code := run(t, "devices", "approve", "device-b", "--no-secrets")
	if code != 0 {
		t.Fatalf("demote B failed: %s", errOut)
	}
	if demoteOut != "changed device-b's role to no-secrets.\n" {
		t.Fatalf("bad demote output: %q", demoteOut)
	}

	// Convergence: every device syncs once more, so the final, merged
	// state (devices.toml demoting B, and whatever the merge did to
	// the secret) reaches everyone.
	useDeviceEnv(t, baseC)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("convergence sync on C failed: %s", errOut)
	}
	useDeviceEnv(t, baseA)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("convergence sync on A failed: %s", errOut)
	}
	useDeviceEnv(t, baseB)
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("convergence sync on B failed: %s", errOut)
	}

	// THE ACCEPTANCE GATE: B, now demoted, must NOT be able to decrypt
	// the CURRENT (rotated) secret. Proven two ways.

	// Proof 1: "secret show --reveal" on B fails, and never leaks the
	// value in its error.
	_, secretErr, code := run(t, "secret", "show", "rotating-key", "--reveal")
	if code != 1 {
		t.Fatalf("ATTACK 4: a demoted device must fail to decrypt the current secret, got exit %d", code)
	}
	if strings.Contains(secretErr, rotatedValue) || strings.Contains(secretErr, dummySecretValue) {
		t.Fatal("the failed decrypt error must never leak the secret's value")
	}

	// Proof 2, the load-bearing one: a RAW age.Decrypt of B's own
	// synced value.age, using B's own private key directly (no CLI, no
	// vault package error handling in the way), must fail. This proves
	// the CIPHERTEXT ITSELF excludes B — not merely that loadout's own
	// code refuses to try.
	bIdentity := readDeviceIdentity(t, baseB)
	ciphertext, err := os.ReadFile(filepath.Join(baseB, "vault", "secrets", "rotating-key", "value.age"))
	if err != nil {
		t.Fatalf("B must still hold the secret's ciphertext on disk (the outer snapshot syncs to every device): %v", err)
	}
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), bIdentity); err == nil {
		t.Fatal("ATTACK 4: B's own raw age key must never decrypt the CURRENT (rotated) secret's value.age")
	}

	// The fix must not break legitimate access: A and C, still full,
	// can both still decrypt the current (rotated) value.
	useDeviceEnv(t, baseA)
	got, errOut, code = run(t, "secret", "show", "rotating-key", "--reveal")
	if code != 0 || got != rotatedValue {
		t.Fatalf("A must still decrypt the current secret, got %q err=%q code=%d", got, errOut, code)
	}
	useDeviceEnv(t, baseC)
	got, errOut, code = run(t, "secret", "show", "rotating-key", "--reveal")
	if code != 0 || got != rotatedValue {
		t.Fatalf("C must still decrypt the current secret, got %q err=%q code=%d", got, errOut, code)
	}
}
