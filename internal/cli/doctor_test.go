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

// demoteSelfToNoSecrets makes v's own device the roster's no-secrets
// entry, adding a distinct full device (otherIdentity's) alongside it
// so the vault keeps a full recipient to protect its secrets, then
// re-encrypts and snapshots — the on-disk shape a real
// "devices approve <self> --no-secrets" from another device would
// leave behind, built directly since these tests run a single vault
// with no remote.
func demoteSelfToNoSecrets(t *testing.T, v *vault.Vault) {
	t.Helper()
	name, recipient, err := vault.DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}
	otherIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "other-device", otherIdentity.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, name, recipient, vault.RoleNoSecrets); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.ReEncryptSecrets(v); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "demote self to no-secrets"); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorSkipsSecretReadabilityForNoSecretsDevice proves the
// foot-gun fix: on a no-secrets device, doctor must never report a
// secret as "this device cannot read it" with a fix that says to
// approve itself — following that fix would PROMOTE the device to
// full, the opposite of what a no-secrets device wants. A no-secrets
// device being unable to read a secret is expected, not a problem.
func TestDoctorSkipsSecretReadabilityForNoSecretsDevice(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	// A local sync projects memory into the enabled adapters, so
	// doctor's own adapter checks (unrelated to this test) stay quiet
	// too — mirrors TestDoctorSilentWhenEveryReadableSecretDecrypts.
	run(t, "sync")
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	demoteSelfToNoSecrets(t, v)

	// Precondition: this device genuinely cannot decrypt the secret
	// any more.
	if _, err := vault.DecryptSecret(v, "openai-key"); err == nil {
		t.Fatal("test setup error: a no-secrets device must not be able to decrypt the secret")
	}

	out, errOut, code := run(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor on a no-secrets device with nothing else wrong must report all good, got %d: out=%q err=%q", code, out, errOut)
	}
	if strings.Contains(out, "cannot read it") {
		t.Fatalf("doctor must never flag a no-secrets device's own inability to read a secret, got %q", out)
	}
	if strings.Contains(out, "devices approve") {
		t.Fatalf("doctor must never suggest promoting a no-secrets device via devices approve, got %q", out)
	}
	if !strings.Contains(out, "all good") {
		t.Fatalf("doctor must report all good, got %q", out)
	}
}

// TestDoctorNoSecretsDeviceStillReportsOtherProblems proves the skip
// is scoped to secret readability only: a no-secrets device with a
// genuine, unrelated problem (a stale skill link here) must still see
// it, and doctor must still exit 1.
func TestDoctorNoSecretsDeviceStillReportsOtherProblems(t *testing.T) {
	base := setupEnv(t)
	initClaudeAndPi(t, base)
	run(t, "add", "skill", "deploy-checks")
	run(t, "sync")
	if _, errOut, code := runWithStdin(t, dummySecretValue, "secret", "add", "openai-key", "--service", "openai"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	demoteSelfToNoSecrets(t, v)

	if err := os.RemoveAll(filepath.Join(base, "vault", "skills", "deploy-checks")); err != nil {
		t.Fatal(err)
	}

	out, _, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must still report the stale link, got %d", code)
	}
	if !strings.Contains(out, "stale link") {
		t.Fatalf("doctor must still report the stale link, got %q", out)
	}
	if strings.Contains(out, "cannot read it") || strings.Contains(out, "openai-key") {
		t.Fatalf("doctor must not mention the secret at all for a no-secrets device, got %q", out)
	}
}

// TestDoctorFlagsUnrecognizedRole proves the promised warning: a
// devices.toml entry whose role is neither "full" nor "no-secrets"
// (nor absent) is flagged by doctor, even though normalizeRole
// silently treats it as no-secrets under the hood.
func TestDoctorFlagsUnrecognizedRole(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	// devices.toml is hand-written here, not through AddToRoster:
	// AddToRoster's own validateRoleForWrite refuses "admin" outright,
	// so this reproduces a HAND-EDITED manifest — exactly the case
	// this check exists to catch.
	path := filepath.Join(base, "vault", "devices.toml")
	content := fmt.Sprintf("[devices.weird-device]\n  recipient = %q\n  role = \"admin\"\n", identity.Recipient().String())
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "hand-edit devices.toml with an unknown role"); err != nil {
		t.Fatal(err)
	}

	out, _, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must flag the unknown role, got %d", code)
	}
	want := "device weird-device has an unknown role admin in the manifest; loadout treats it as no-secrets"
	if !strings.Contains(out, want) {
		t.Fatalf("doctor must flag the unknown role, got %q", out)
	}
	if !strings.Contains(out, "fix: set the role to full or no-secrets.") {
		t.Fatalf("doctor must carry the fix, got %q", out)
	}
}

// TestDoctorSilentOnRecognizedRoles proves the negative case: a
// devices.toml with only "full", "no-secrets", and absent roles never
// trips the unrecognized-role check.
func TestDoctorSilentOnRecognizedRoles(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "sync")

	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	name, recipient, err := vault.DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, name, recipient, vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "other-device", other.Recipient().String(), vault.RoleNoSecrets); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add a full and a no-secrets device"); err != nil {
		t.Fatal(err)
	}

	out, _, code := run(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor must stay quiet for only recognized roles, got %d: %q", code, out)
	}
	if !strings.Contains(out, "all good") {
		t.Fatalf("doctor must report all good, got %q", out)
	}
}
