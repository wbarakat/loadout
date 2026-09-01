package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"loadout.dev/loadout/internal/vault"
)

// TestDoctorReportsUnreadableSecretButNotAReadableOne proves the
// durable readability-probe check: a secret this device cannot
// decrypt (a hand-crafted orphan here, standing in for one some other
// device added) is reported by name with the approve-then-sync fix, a
// normal, decryptable secret added alongside it is never reported,
// and doctor's own probing never appends an access-log entry — a
// probe is not a use.
func TestDoctorReportsUnreadableSecretButNotAReadableOne(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")

	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	deviceName, _, err := vault.DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}

	const orphanValue = "orphan-secret-do-not-leak"
	stranger, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	writeOrphanSecret(t, v, "orphan-key", orphanValue, stranger)

	out, errOut, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must report a problem for an unreadable secret, got %d (%s)", code, errOut)
	}
	wantDetail := "secret/orphan-key: this device cannot read it"
	wantFix := fmt.Sprintf("run loadout devices approve %s from a device that can read it, then sync.", deviceName)
	if !strings.Contains(out, wantDetail) {
		t.Fatalf("doctor must name the unreadable secret, got %q", out)
	}
	if !strings.Contains(out, wantFix) {
		t.Fatalf("doctor must carry the approve-then-sync fix, got %q", out)
	}
	if strings.Contains(out, "secret/openai-key: this device cannot read it") {
		t.Fatalf("doctor must never report a secret this device CAN read, got %q", out)
	}
	if strings.Contains(out, orphanValue) || strings.Contains(errOut, orphanValue) {
		t.Fatal("doctor must never print a secret's value")
	}

	// A readability probe is not a use: doctor must never write an
	// access-log entry for either secret.
	logPath := filepath.Join(base, "vault", "access.log")
	if data, err := os.ReadFile(logPath); err == nil {
		t.Fatalf("doctor must never append to the access log, but access.log holds: %q", data)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// TestDoctorSilentWhenEveryReadableSecretDecrypts proves a vault
// where every secret decrypts fine reports nothing about secrets at
// all.
func TestDoctorSilentWhenEveryReadableSecretDecrypts(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	// A local sync projects memory into the enabled adapters, so
	// doctor's own adapter checks (unrelated to this test) stay quiet
	// too — mirrors TestDoctorSilentWhenRemoteInSync's own setup.
	run(t, "sync")
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}

	out, errOut, code := run(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor must stay quiet when every secret is readable, got %d: out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "all good") {
		t.Fatalf("doctor must report all good, got %q", out)
	}

	logPath := filepath.Join(base, "vault", "access.log")
	if data, err := os.ReadFile(logPath); err == nil {
		t.Fatalf("doctor must never append to the access log, but access.log holds: %q", data)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// TestDoctorFlagsSecretDueForRotation proves the rotation reminder: a
// secret added with a rotate_after of 1ns is due for rotation almost
// as soon as it lands (any real clock tick since "at" is well past
// 1ns), and doctor reports it by name with the exact fix text, never
// the value, and without writing an access-log entry — a rotation
// check reads only metadata, it is not a use of the secret either.
func TestDoctorFlagsSecretDueForRotation(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai", "--rotate-after", "1ns"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}
	// meta.md's "at" carries only second resolution; give SecretDue's
	// 1ns window a real clock tick to clear.
	time.Sleep(1100 * time.Millisecond)

	out, errOut, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must report a problem for a due secret, got %d (%s)", code, errOut)
	}
	if !strings.Contains(out, "secret/openai-key: is due for rotation (added ") {
		t.Fatalf("doctor must name the due secret, got %q", out)
	}
	if !strings.Contains(out, ", rotate after 1ns)") {
		t.Fatalf("doctor must carry the rotate_after value, got %q", out)
	}
	if !strings.Contains(out, "fix: rotate the key at openai, then run loadout secret rotate openai-key to replace it.") {
		t.Fatalf("doctor must carry the exact rotation fix, got %q", out)
	}
	if strings.Contains(out, dummySecretValue) || strings.Contains(errOut, dummySecretValue) {
		t.Fatal("doctor must never print a secret's value")
	}

	logPath := filepath.Join(base, "vault", "access.log")
	if data, err := os.ReadFile(logPath); err == nil {
		t.Fatalf("a rotation check must never append to the access log, but access.log holds: %q", data)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// TestDoctorSilentForFreshSecretWithRotateAfter proves the negative
// case: a secret just added with a long rotate_after is not flagged —
// only one that has actually elapsed past its window is.
func TestDoctorSilentForFreshSecretWithRotateAfter(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "sync")
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai", "--rotate-after", "720h"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}

	out, errOut, code := run(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor must stay quiet for a fresh secret, got %d: out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "all good") {
		t.Fatalf("doctor must report all good, got %q", out)
	}
}

// TestDoctorSilentForSecretWithNoRotateAfter proves a secret added
// with no rotate_after at all is never flagged, no matter its age.
func TestDoctorSilentForSecretWithNoRotateAfter(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "sync")
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}

	out, errOut, code := run(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor must stay quiet, got %d: out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "all good") {
		t.Fatalf("doctor must report all good, got %q", out)
	}
}
