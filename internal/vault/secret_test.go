package vault_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"loadout.dev/loadout/internal/vault"
)

// dummySecretValue is the only secret value any test in this file
// ever uses. It is not a real credential.
const dummySecretValue = "test-secret-value-123"

// deviceIdentityFor reads v's own device.key straight off disk and
// parses it as an age identity, the same way a production decrypt
// path will (Task 2), so this test can prove the round trip without
// that path existing yet.
func deviceIdentityFor(t *testing.T, v *vault.Vault) *age.X25519Identity {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(v.Root, "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestAddSecretWritesBothFiles(t *testing.T) {
	v := newVault(t)
	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "deploy hook", "", "human", nil, value); err != nil {
		t.Fatal(err)
	}

	metaPath := filepath.Join(v.SecretsDir(), "openai-key", "meta.md")
	valuePath := filepath.Join(v.SecretsDir(), "openai-key", "value.age")

	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("meta.md must exist: %v", err)
	}
	if _, err := os.Stat(valuePath); err != nil {
		t.Fatalf("value.age must exist: %v", err)
	}

	meta := string(metaData)
	for _, want := range []string{"name: openai-key", "service: openai", "hook: deploy hook", "rotate_after:", "by: human", "at:"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("meta.md missing %q, got:\n%s", want, meta)
		}
	}
}

// TestAddSecretValueAbsentFromDisk proves INVARIANT 10 at the byte
// level: the dummy value appears nowhere in meta.md, and value.age
// holds only ciphertext, never the plaintext bytes.
func TestAddSecretValueAbsentFromDisk(t *testing.T) {
	v := newVault(t)
	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, value); err != nil {
		t.Fatal(err)
	}

	metaData, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "meta.md"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metaData, []byte(dummySecretValue)) {
		t.Fatalf("meta.md must never contain the secret value, got:\n%s", metaData)
	}

	valueData, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(valueData, []byte(dummySecretValue)) {
		t.Fatal("value.age must never contain the plaintext value: it must be ciphertext only")
	}
}

// TestAddSecretZeroesCallerBuffer proves the caller's own byte slice
// is zeroed once AddSecret returns, so the plaintext does not linger
// in memory the caller still holds a reference to.
func TestAddSecretZeroesCallerBuffer(t *testing.T) {
	v := newVault(t)
	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, value); err != nil {
		t.Fatal(err)
	}
	for i, b := range value {
		if b != 0 {
			t.Fatalf("value[%d] = %d, want 0: AddSecret must zero the caller's buffer", i, b)
		}
	}
}

// TestAddSecretValueDecryptsWithDeviceKey proves value.age round
// trips: decrypting it with this device's own age identity, using
// age directly (no production decrypt path exists yet — that is Task
// 2), reproduces exactly the dummy value that went in.
func TestAddSecretValueDecryptsWithDeviceKey(t *testing.T) {
	v := newVault(t)
	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, value); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceIdentityFor(t, v)
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		t.Fatalf("value.age must decrypt with this device's key: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != dummySecretValue {
		t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
	}
}

// TestAddSecretEncryptsToEveryRosterRecipient proves a secret added
// while a roster is enrolled decrypts with a SECOND device's own
// identity too, never touching the first device's key — the same
// roster fan-out PackSnapshot already gives skills and memory.
func TestAddSecretEncryptsToEveryRosterRecipient(t *testing.T) {
	v := newVault(t)
	ownRecipient, err := vault.DeviceRecipient(v)
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "this-device", ownRecipient, vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "other-device", other.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}

	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, value); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), other)
	if err != nil {
		t.Fatalf("the second device's identity must decrypt the secret: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != dummySecretValue {
		t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
	}
}

// TestAddSecretAlwaysIncludesSelfEvenWhenNotEnrolled proves the union
// semantics: a roster that exists but does not yet list this device
// must still let this device decrypt the secret it just created.
// Reusing PackSnapshot's packRecipients as-is would fail this (it
// falls back to "self alone" only when the roster is EMPTY), so this
// pins the deliberately different "roster ∪ self" rule secrets need.
func TestAddSecretAlwaysIncludesSelfEvenWhenNotEnrolled(t *testing.T) {
	v := newVault(t)
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "other-device", other.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}

	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, value); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceIdentityFor(t, v)
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		t.Fatalf("this device must be able to decrypt a secret it just created, even while unenrolled: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != dummySecretValue {
		t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
	}
}

func TestAddSecretDuplicateRefused(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err == nil {
		t.Fatal("a duplicate secret name must be refused")
	}
}

func TestAddSecretBadNameRefused(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "Bad Name", "openai", "", "", "human", nil, []byte(dummySecretValue)); err == nil {
		t.Fatal("a non-kebab-case name must be refused")
	}
	if vault.SecretExists(v, "Bad Name") {
		t.Fatal("a refused add must not create anything")
	}
}

func TestListSecretsMetadataOnly(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "zebra-key", "svc-z", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddSecret(v, "alpha-key", "svc-a", "", "", "claude-code", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}

	secrets, err := vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 2 {
		t.Fatalf("len(secrets) = %d, want 2", len(secrets))
	}
	if secrets[0].Name != "alpha-key" || secrets[1].Name != "zebra-key" {
		t.Fatalf("ListSecrets must return name order, got %v", secrets)
	}
	if secrets[0].Service != "svc-a" || secrets[1].Service != "svc-z" {
		t.Fatalf("bad service metadata: %+v", secrets)
	}
	if secrets[0].By != "claude-code" {
		t.Fatalf("bad by metadata: %+v", secrets[0])
	}
	if secrets[0].At == "" {
		t.Fatal("At must be recorded")
	}
}

// TestListSecretsOnEmptyVault proves a vault with no secrets/
// directory yet lists as empty, not an error: no secret has ever
// been added.
func TestListSecretsOnEmptyVault(t *testing.T) {
	v := newVault(t)
	secrets, err := vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 {
		t.Fatalf("secrets = %v, want none", secrets)
	}
}

// TestAddSecretRecoversFromIncompleteDirectory proves a stale,
// half-written secret directory — meta.md present, value.age never
// written, the exact shape a crash between the old two-step write
// left behind — does not strand the name forever. SecretExists and
// ListSecrets both treat it as absent, and a retry succeeds and
// produces a real, decryptable secret.
func TestAddSecretRecoversFromIncompleteDirectory(t *testing.T) {
	v := newVault(t)
	dir := filepath.Join(v.SecretsDir(), "openai-key")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.md"), []byte("---\nname: openai-key\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if vault.SecretExists(v, "openai-key") {
		t.Fatal("a directory missing value.age must not count as an existing secret")
	}
	secrets, err := vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 {
		t.Fatalf("ListSecrets must not surface an incomplete directory, got %v", secrets)
	}

	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatalf("AddSecret must recover from a stale incomplete directory: %v", err)
	}
	if !vault.SecretExists(v, "openai-key") {
		t.Fatal("the secret must exist after a successful retry")
	}

	ciphertext, err := os.ReadFile(filepath.Join(dir, "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceIdentityFor(t, v)
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		t.Fatalf("value.age must decrypt after the recovered write: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != dummySecretValue {
		t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
	}
}

// TestListSecretsSkipsTempDirectories proves a leftover AddSecret temp
// directory — the shape a crash right before the final rename leaves
// behind, with both files already written under its dot-prefixed temp
// name — never surfaces as a secret.
func TestListSecretsSkipsTempDirectories(t *testing.T) {
	v := newVault(t)
	tmpDir := filepath.Join(v.SecretsDir(), ".openai-key.tmp-stale")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "meta.md"), []byte("---\nname: openai-key\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "value.age"), []byte("bogus-ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}

	secrets, err := vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 {
		t.Fatalf("ListSecrets must skip a dot-prefixed temp directory, got %v", secrets)
	}
}

// TestAddSecretValueFileMode0600 proves value.age is written mode
// 0600: it holds encrypted key material, the same sensitivity as
// device.key.
func TestAddSecretValueFileMode0600(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("value.age must be mode 0600, got %o", fi.Mode().Perm())
	}
}

func TestSecretExists(t *testing.T) {
	v := newVault(t)
	if vault.SecretExists(v, "openai-key") {
		t.Fatal("SecretExists must be false before AddSecret")
	}
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if !vault.SecretExists(v, "openai-key") {
		t.Fatal("SecretExists must be true after AddSecret")
	}
}

func TestRemoveSecretDeletesDir(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if err := vault.RemoveSecret(v, "openai-key"); err != nil {
		t.Fatal(err)
	}
	if vault.SecretExists(v, "openai-key") {
		t.Fatal("RemoveSecret must delete the secret's directory")
	}
	if _, err := os.Stat(filepath.Join(v.SecretsDir(), "openai-key")); err == nil {
		t.Fatal("RemoveSecret must remove the whole secret directory")
	}
}

func TestRemoveSecretMissingRefused(t *testing.T) {
	v := newVault(t)
	if err := vault.RemoveSecret(v, "no-such-secret"); err == nil {
		t.Fatal("RemoveSecret must refuse a name that does not exist")
	}
}

// TestSecretsAreTrackedAccessLogIsGitignored proves the Task 1 split:
// secrets/ (ciphertext) is tracked and synced, never gitignored,
// while access.log (Phase 2's device-local decrypt record) is
// gitignored and never enters history.
func TestSecretsAreTrackedAccessLogIsGitignored(t *testing.T) {
	v := newVault(t)
	gitignore, err := os.ReadFile(filepath.Join(v.Root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gitignore), "secrets") {
		t.Fatalf("secrets/ must NOT be gitignored: it syncs as ciphertext, got:\n%s", gitignore)
	}
	if !strings.Contains(string(gitignore), "access.log") {
		t.Fatalf("access.log must be gitignored, got:\n%s", gitignore)
	}

	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Root, "access.log"), []byte(`{"secret":"openai-key"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add a secret"); err != nil {
		t.Fatal(err)
	}

	tracked, err := vault.RecentSubjects(v, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) == 0 || tracked[0] != "add a secret" {
		t.Fatalf("the secret must be committed, got subjects %v", tracked)
	}

	blob, _, err := vault.PackSnapshot(v)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte(dummySecretValue)) {
		t.Fatal("a pack must never carry the plaintext secret value, even double-wrapped in the outer age layer")
	}
}

// TestReEncryptSecretsAddsNewRecipient proves the headline mechanism
// Task 4 exists for: a secret added before some device joined the
// roster is unreadable to that device's identity beforehand, and
// becomes readable once ReEncryptSecrets runs after the roster gains
// it. This device's own identity must still decrypt the secret
// afterward too.
func TestReEncryptSecretsAddsNewRecipient(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}

	newcomer, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), newcomer); err == nil {
		t.Fatal("the newcomer must not be able to decrypt the secret before it joins the roster")
	}

	if err := vault.AddToRoster(v, "newcomer", newcomer.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}

	skipped, err := vault.ReEncryptSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}

	ciphertext, err = os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), newcomer)
	if err != nil {
		t.Fatalf("the newcomer must decrypt the secret after ReEncryptSecrets: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != dummySecretValue {
		t.Fatalf("newcomer's decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
	}

	identity := deviceIdentityFor(t, v)
	r2, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		t.Fatalf("this device must still decrypt its own secret after re-encrypt: %v", err)
	}
	var plaintext2 bytes.Buffer
	if _, err := plaintext2.ReadFrom(r2); err != nil {
		t.Fatal(err)
	}
	if plaintext2.String() != dummySecretValue {
		t.Fatal("this device's own decrypted value must be unchanged after re-encrypt")
	}
}

// TestReEncryptSecretsLeavesMetaUnchanged proves ReEncryptSecrets only
// ever rewrites value.age: meta.md's bytes are untouched.
func TestReEncryptSecretsLeavesMetaUnchanged(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "deploy hook", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(v.SecretsDir(), "openai-key", "meta.md")
	before, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}

	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "other-device", other.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.ReEncryptSecrets(v); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("meta.md must be unchanged by ReEncryptSecrets, before:\n%s\nafter:\n%s", before, after)
	}
}

// TestReEncryptSecretsSkipsUndecryptableSecretByNameOnly proves the
// safety net: a secret's value.age this device was never a recipient
// of (hand-crafted here, the shape an old orphaned secret would take)
// is skipped, not fatal, and the skip list carries only its NAME,
// never its value. Every other, decryptable secret still gets
// re-encrypted normally in the same call.
func TestReEncryptSecretsSkipsUndecryptableSecretByNameOnly(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}

	const orphanValue = "orphan-secret-do-not-leak"
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(v.SecretsDir(), "orphan-key")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := "---\nname: orphan-key\nservice: orphan\nhook: \nrotate_after: \nby: human\nat: 2024-01-01T00:00:00Z\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.md"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, other.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(orphanValue)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "value.age"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	skipped, err := vault.ReEncryptSecrets(v)
	if err != nil {
		t.Fatalf("ReEncryptSecrets must not fail the whole run over one undecryptable secret: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != "orphan-key" {
		t.Fatalf("skipped = %v, want [orphan-key]", skipped)
	}
	for _, name := range skipped {
		if strings.Contains(name, orphanValue) {
			t.Fatal("the skip list must never carry a secret value, names only")
		}
	}

	// The decryptable secret must still have been re-encrypted fine.
	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceIdentityFor(t, v)
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		t.Fatalf("the decryptable secret must remain decryptable: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != dummySecretValue {
		t.Fatal("the decryptable secret's value must be unchanged")
	}
}

// TestSecretDueTrueOncePastDue proves the headline case: a secret
// whose rotate_after has fully elapsed since at is due.
func TestSecretDueTrueOncePastDue(t *testing.T) {
	now := time.Now().UTC()
	s := vault.Secret{
		Name:        "openai-key",
		RotateAfter: "24h",
		At:          now.Add(-25 * time.Hour).Format(time.RFC3339),
	}
	if !vault.SecretDue(s, now) {
		t.Fatal("a secret added 25h ago with a 24h rotate_after must be due")
	}
}

// TestSecretDueFalseWhenNotYetElapsed proves a fresh secret, added
// well within its own rotate_after window, is not due.
func TestSecretDueFalseWhenNotYetElapsed(t *testing.T) {
	now := time.Now().UTC()
	s := vault.Secret{
		Name:        "openai-key",
		RotateAfter: "720h",
		At:          now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	if vault.SecretDue(s, now) {
		t.Fatal("a secret added 1h ago with a 720h rotate_after must not be due")
	}
}

// TestSecretDueFalseWhenRotateAfterEmpty proves a secret with no
// rotation reminder set is never due, no matter how old.
func TestSecretDueFalseWhenRotateAfterEmpty(t *testing.T) {
	now := time.Now().UTC()
	s := vault.Secret{
		Name: "openai-key",
		At:   now.Add(-24 * 365 * time.Hour).Format(time.RFC3339),
	}
	if vault.SecretDue(s, now) {
		t.Fatal("an empty rotate_after must never be due")
	}
}

// TestSecretDueFalseOnUnparseableFields proves SecretDue never errors:
// an unparseable rotate_after or at both read back as "not due",
// never a crash or a false positive.
func TestSecretDueFalseOnUnparseableFields(t *testing.T) {
	now := time.Now().UTC()
	cases := []vault.Secret{
		{Name: "a", RotateAfter: "not-a-duration", At: now.Add(-1000 * time.Hour).Format(time.RFC3339)},
		{Name: "b", RotateAfter: "24h", At: "not-a-timestamp"},
		{Name: "c", RotateAfter: "24h", At: ""},
	}
	for _, s := range cases {
		if vault.SecretDue(s, now) {
			t.Fatalf("a malformed secret %+v must never be reported due", s)
		}
	}
}

// TestRotateSecretReplacesValueKeepsMetaUpdatesAt proves the
// headline mechanism: RotateSecret re-encrypts a new value, decrypts
// back to exactly that new value, preserves service/hook/rotate_after/
// by untouched, and advances at to a fresh timestamp.
func TestRotateSecretReplacesValueKeepsMetaUpdatesAt(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "deploy hook", "24h", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "meta.md"))
	if err != nil {
		t.Fatal(err)
	}
	// at has second-level RFC3339 resolution: sleep past a full second
	// so a real clock advance is guaranteed to show up in the new at,
	// rather than an assertion that is only sometimes true.
	time.Sleep(1100 * time.Millisecond)

	const newValue = "rotated-secret-value-456"
	if err := vault.RotateSecret(v, "openai-key", nil, []byte(newValue)); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceIdentityFor(t, v)
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		t.Fatalf("value.age must decrypt after rotation: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != newValue {
		t.Fatalf("decrypted value = %q, want %q", plaintext.String(), newValue)
	}
	if bytes.Contains(ciphertext, []byte(newValue)) {
		t.Fatal("value.age must hold ciphertext only, never the plaintext")
	}

	after, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "meta.md"))
	if err != nil {
		t.Fatal(err)
	}
	afterText := string(after)
	for _, want := range []string{"service: openai", "hook: deploy hook", "rotate_after: 24h", "by: human"} {
		if !strings.Contains(afterText, want) {
			t.Fatalf("meta.md must keep %q unchanged after rotation, got:\n%s", want, afterText)
		}
	}
	if string(before) == afterText {
		t.Fatal("meta.md's at field must change after rotation")
	}
}

// TestRotateSecretZeroesCallerBuffer proves RotateSecret zeroes the
// caller's new-value buffer, the same as AddSecret.
func TestRotateSecretZeroesCallerBuffer(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	newValue := []byte("rotated-value")
	if err := vault.RotateSecret(v, "openai-key", nil, newValue); err != nil {
		t.Fatal(err)
	}
	for i, b := range newValue {
		if b != 0 {
			t.Fatalf("newValue[%d] = %d, want 0: RotateSecret must zero the caller's buffer", i, b)
		}
	}
}

// TestRotateSecretRefusesNonexistent proves rotate replaces a value,
// it does not create one: a name that was never added is refused.
func TestRotateSecretRefusesNonexistent(t *testing.T) {
	v := newVault(t)
	if err := vault.RotateSecret(v, "no-such-key", nil, []byte("x")); err == nil {
		t.Fatal("RotateSecret must refuse a name that does not exist")
	}
}

// TestRotateSecretEncryptsToCurrentRoster proves a rotated value
// re-encrypts to the CURRENT device roster, not a stale one: a device
// added to the roster after the original AddSecret can still decrypt
// the rotated value.
func TestRotateSecretEncryptsToCurrentRoster(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	newcomer, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "newcomer", newcomer.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}

	const newValue = "rotated-secret-value-789"
	if err := vault.RotateSecret(v, "openai-key", nil, []byte(newValue)); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), newcomer)
	if err != nil {
		t.Fatalf("the newcomer must decrypt the rotated value: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != newValue {
		t.Fatalf("decrypted value = %q, want %q", plaintext.String(), newValue)
	}
}

// TestPathTraversalNameRefusedByEveryVerb proves the path-traversal
// BLOCKER is closed: a name carrying ".." components is refused by
// every verb that turns a name into a filesystem path, and a sentinel
// file OUTSIDE secrets/ — placed at exactly the path filepath.Join
// would have cleaned the hostile name down to — survives every one of
// them untouched.
func TestPathTraversalNameRefusedByEveryVerb(t *testing.T) {
	v := newVault(t)
	const hostileName = "../../../outside-vault-target"

	// This is exactly the directory filepath.Join(v.SecretsDir(),
	// hostileName) resolves to once its ".." components are cleaned:
	// the directory a hostile name would destroy, read, or overwrite
	// if any verb below skipped name validation. It carries a meta.md
	// AND a value.age, the shape SecretExists treats as a real secret
	// — the exact condition that let the unvalidated code past its
	// "no such secret" guard and on to the destructive call — plus a
	// third sentinel file with no special meaning to any secret verb,
	// so its survival proves the directory was never touched at all.
	sentinelDir := filepath.Join(v.SecretsDir(), hostileName)
	if err := os.MkdirAll(sentinelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sentinelDir, "meta.md"), []byte("---\nname: outside-vault-target\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sentinelDir, "value.age"), []byte("bogus-ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinelFile := filepath.Join(sentinelDir, "sentinel.txt")
	if err := os.WriteFile(sentinelFile, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}

	if vault.SecretExists(v, hostileName) {
		t.Fatal("SecretExists must be false for a path-traversal name, even one shaped like a real secret")
	}
	if err := vault.RemoveSecret(v, hostileName); err == nil {
		t.Fatal("RemoveSecret must refuse a path-traversal name")
	}
	if _, err := vault.DecryptSecret(v, hostileName); err == nil {
		t.Fatal("DecryptSecret must refuse a path-traversal name")
	}
	if err := vault.RotateSecret(v, hostileName, nil, []byte("x")); err == nil {
		t.Fatal("RotateSecret must refuse a path-traversal name")
	}
	if err := vault.AddSecret(v, hostileName, "svc", "", "", "human", nil, []byte("x")); err == nil {
		t.Fatal("AddSecret must refuse a path-traversal name")
	}

	// The whole directory — meta.md, value.age, and the sentinel —
	// must survive every one of the calls above, byte for byte.
	for _, f := range []string{"meta.md", "value.age", "sentinel.txt"} {
		if _, err := os.Stat(filepath.Join(sentinelDir, f)); err != nil {
			t.Fatalf("%s outside secrets/ must survive every hostile-name verb, got err=%v", f, err)
		}
	}
	data, err := os.ReadFile(sentinelFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "do not delete" {
		t.Fatal("the sentinel's content must be untouched")
	}
}

// TestValidateSecretNameGrammar pins the exact error grammar every
// verb's refusal shares.
func TestValidateSecretNameGrammar(t *testing.T) {
	err := vault.ValidateSecretName("../x")
	if err == nil {
		t.Fatal("must refuse a path-traversal name")
	}
	want := "../x: not a valid secret name. Fix: use a kebab-case name like openai-key."
	if err.Error() != want {
		t.Fatalf("bad error: got %q want %q", err.Error(), want)
	}
	if err := vault.ValidateSecretName("openai-key"); err != nil {
		t.Fatalf("a valid kebab-case name must be accepted: %v", err)
	}
}

// TestDecryptSecretWrapsUnreadableValueFileError proves a raw
// os.ReadFile failure on value.age (SecretExists already passed, so
// the file exists, but reading it fails anyway) is wrapped in the
// standard grammar, never surfaced as a bare, unwrapped os error.
func TestDecryptSecretWrapsUnreadableValueFileError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a file regardless of its permissions")
	}
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	valuePath := filepath.Join(v.SecretsDir(), "openai-key", "value.age")
	if err := os.Chmod(valuePath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(valuePath, 0o600)

	_, err := vault.DecryptSecret(v, "openai-key")
	if err == nil {
		t.Fatal("DecryptSecret must fail when value.age cannot be read")
	}
	want := "secret/openai-key: the secret value cannot be read:"
	if !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("bad error: got %q, want prefix %q", err.Error(), want)
	}
	if !strings.HasSuffix(err.Error(), "Fix: check the file, or re-add the secret.") {
		t.Fatalf("bad error: %q", err.Error())
	}
}

// TestReEncryptSecretsOnEmptyVault proves a vault with no secrets at
// all is a no-op: no error, no skipped names.
func TestReEncryptSecretsOnEmptyVault(t *testing.T) {
	v := newVault(t)
	skipped, err := vault.ReEncryptSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
}

// TestValidateAllowedHostAcceptsBareHostAndHostPort proves a bare
// host and a host:port both pass — the two shapes the broker's own
// exact-match check expects an allowed_hosts entry to take.
func TestValidateAllowedHostAcceptsBareHostAndHostPort(t *testing.T) {
	for _, host := range []string{"api.example.com", "api.example.com:8443", "127.0.0.1:9090", "localhost"} {
		if err := vault.ValidateAllowedHost(host); err != nil {
			t.Fatalf("ValidateAllowedHost(%q) = %v, want nil", host, err)
		}
	}
}

// TestValidateAllowedHostRejectsSchemePathWildcardSpace proves every
// shape the broker must never be able to misread as a bare host is
// refused: a scheme, a path, a wildcard, a space, and empty.
func TestValidateAllowedHostRejectsSchemePathWildcardSpace(t *testing.T) {
	for _, host := range []string{
		"https://api.example.com",
		"api.example.com/path",
		"*.example.com",
		"api example.com",
		"",
	} {
		if err := vault.ValidateAllowedHost(host); err == nil {
			t.Fatalf("ValidateAllowedHost(%q) = nil, want an error", host)
		}
	}
}

// TestAddSecretStoresAllowedHosts proves AddSecret writes
// allowed_hosts to meta.md and ListSecrets reads it back exactly.
func TestAddSecretStoresAllowedHosts(t *testing.T) {
	v := newVault(t)
	hosts := []string{"api.example.com", "other.example.com:8443"}
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", hosts, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	secrets, err := vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 1 {
		t.Fatalf("want 1 secret, got %d", len(secrets))
	}
	if !reflect.DeepEqual(secrets[0].AllowedHosts, hosts) {
		t.Fatalf("AllowedHosts = %v, want %v", secrets[0].AllowedHosts, hosts)
	}
}

// TestAddSecretAllowedHostsAbsentIsNil proves a secret added with no
// allowed_hosts at all comes back as an empty AllowedHosts — the
// fail-closed default the broker relies on.
func TestAddSecretAllowedHostsAbsentIsNil(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	secrets, err := vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets[0].AllowedHosts) != 0 {
		t.Fatalf("AllowedHosts = %v, want none", secrets[0].AllowedHosts)
	}
}

// TestAddSecretRejectsInvalidAllowedHost proves an invalid
// allowed_hosts entry refuses the whole add, writing nothing.
func TestAddSecretRejectsInvalidAllowedHost(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", []string{"https://evil.example"}, []byte(dummySecretValue)); err == nil {
		t.Fatal("an invalid allowed_hosts entry must be refused")
	}
	if vault.SecretExists(v, "openai-key") {
		t.Fatal("a refused add must not create anything")
	}
}

// TestRotateSecretPreservesAllowedHostsWhenNotGiven proves rotate's
// "keep unless given" rule: a nil allowedHosts argument leaves the
// secret's existing allowed_hosts unchanged, the same way service,
// hook, and rotate_after are always preserved.
func TestRotateSecretPreservesAllowedHostsWhenNotGiven(t *testing.T) {
	v := newVault(t)
	hosts := []string{"api.example.com"}
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", hosts, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if err := vault.RotateSecret(v, "openai-key", nil, []byte("rotated-value")); err != nil {
		t.Fatal(err)
	}
	secrets, err := vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(secrets[0].AllowedHosts, hosts) {
		t.Fatalf("AllowedHosts after rotate = %v, want unchanged %v", secrets[0].AllowedHosts, hosts)
	}
}

// TestRotateSecretReplacesAllowedHostsWhenGiven proves a non-nil
// allowedHosts argument replaces the secret's allowed_hosts, even
// with an empty slice (clearing every host back to fail-closed).
func TestRotateSecretReplacesAllowedHostsWhenGiven(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", []string{"old.example.com"}, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	newHosts := []string{"new.example.com"}
	if err := vault.RotateSecret(v, "openai-key", newHosts, []byte("rotated-value")); err != nil {
		t.Fatal(err)
	}
	secrets, err := vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(secrets[0].AllowedHosts, newHosts) {
		t.Fatalf("AllowedHosts after rotate = %v, want %v", secrets[0].AllowedHosts, newHosts)
	}

	if err := vault.RotateSecret(v, "openai-key", []string{}, []byte("rotated-again")); err != nil {
		t.Fatal(err)
	}
	secrets, err = vault.ListSecrets(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets[0].AllowedHosts) != 0 {
		t.Fatalf("AllowedHosts after clearing = %v, want none", secrets[0].AllowedHosts)
	}
}

// TestRotateSecretRejectsInvalidAllowedHost proves an invalid
// allowed_hosts entry refuses the whole rotate, leaving the old value
// and metadata untouched.
func TestRotateSecretRejectsInvalidAllowedHost(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", []string{"good.example.com"}, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}

	if err := vault.RotateSecret(v, "openai-key", []string{"bad host with space"}, []byte("rotated-value")); err == nil {
		t.Fatal("an invalid allowed_hosts entry must be refused")
	}

	after, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused rotate must never touch the existing value")
	}
}

// TestSecretMetaReadsWithoutTouchingValue proves SecretMeta returns
// the same metadata ListSecrets does, without decrypting value.age.
func TestSecretMetaReadsWithoutTouchingValue(t *testing.T) {
	v := newVault(t)
	hosts := []string{"api.example.com"}
	if err := vault.AddSecret(v, "openai-key", "openai", "a hook", "24h", "human", hosts, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	meta, err := vault.SecretMeta(v, "openai-key")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "openai-key" || meta.Service != "openai" || meta.Hook != "a hook" || meta.RotateAfter != "24h" {
		t.Fatalf("bad metadata: %+v", meta)
	}
	if !reflect.DeepEqual(meta.AllowedHosts, hosts) {
		t.Fatalf("AllowedHosts = %v, want %v", meta.AllowedHosts, hosts)
	}
}

// TestSecretMetaMissingRefused proves SecretMeta refuses a name that
// does not exist, the same message shape DecryptSecret uses.
func TestSecretMetaMissingRefused(t *testing.T) {
	v := newVault(t)
	if _, err := vault.SecretMeta(v, "no-such-secret"); err == nil {
		t.Fatal("SecretMeta must refuse a name that does not exist")
	}
}

// TestAddSecretExcludesNoSecretsDeviceFromRecipients is the headline
// security proof for Phase 8a Task 2: with a roster of this device
// (full) plus a second, no-secrets device, AddSecret's value.age
// decrypts with this device's own key AND with a full roster device's
// key, but a real age.Decrypt with the no-secrets device's identity
// FAILS. A no-secrets device's key is never a recipient of any secret
// — provably, by trying to decrypt with it and getting an error.
func TestAddSecretExcludesNoSecretsDeviceFromRecipients(t *testing.T) {
	v := newVault(t)
	ownRecipient, err := vault.DeviceRecipient(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "this-device", ownRecipient, vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	fullOther, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "full-other", fullOther.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	noSecrets, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "dashboard", noSecrets.Recipient().String(), vault.RoleNoSecrets); err != nil {
		t.Fatal(err)
	}

	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}

	// This device (full): must decrypt.
	selfIdentity := deviceIdentityFor(t, v)
	if r, err := age.Decrypt(bytes.NewReader(ciphertext), selfIdentity); err != nil {
		t.Fatalf("this full device must decrypt the secret: %v", err)
	} else {
		var plaintext bytes.Buffer
		if _, err := plaintext.ReadFrom(r); err != nil {
			t.Fatal(err)
		}
		if plaintext.String() != dummySecretValue {
			t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
		}
	}

	// The full roster device: must decrypt.
	if r, err := age.Decrypt(bytes.NewReader(ciphertext), fullOther); err != nil {
		t.Fatalf("the full roster device must decrypt the secret: %v", err)
	} else {
		var plaintext bytes.Buffer
		if _, err := plaintext.ReadFrom(r); err != nil {
			t.Fatal(err)
		}
		if plaintext.String() != dummySecretValue {
			t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
		}
	}

	// THE SECURITY PROOF: the no-secrets device's key must NOT decrypt.
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), noSecrets); err == nil {
		t.Fatal("SECURITY VIOLATION: a no-secrets device's key must never decrypt a secret, but age.Decrypt succeeded")
	}
}

// TestAddSecretTwoFullOneNoSecrets proves the same exclusion with two
// full devices in the roster: the value.age is a recipient of both
// full devices, but not the no-secrets one.
func TestAddSecretTwoFullOneNoSecrets(t *testing.T) {
	v := newVault(t)
	fullA, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "full-a", fullA.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	fullB, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "full-b", fullB.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	noSecrets, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "dashboard", noSecrets.Recipient().String(), vault.RoleNoSecrets); err != nil {
		t.Fatal(err)
	}

	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []*age.X25519Identity{fullA, fullB} {
		if _, err := age.Decrypt(bytes.NewReader(ciphertext), id); err != nil {
			t.Fatalf("a full roster device must decrypt the secret: %v", err)
		}
	}
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), noSecrets); err == nil {
		t.Fatal("the no-secrets device must not decrypt the secret")
	}
}

// TestSecretRecipientsBackwardCompatAllFull proves a roster of only
// full devices (or no roster at all) behaves exactly as before Phase
// 8a: every device (full roster members plus self) can decrypt.
func TestSecretRecipientsBackwardCompatAllFull(t *testing.T) {
	v := newVault(t)
	// No roster at all: self alone must decrypt (unchanged prior
	// behavior, already covered by other tests) — then add an
	// all-full roster and re-check.
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "other-device", other.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}

	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}

	selfIdentity := deviceIdentityFor(t, v)
	for _, id := range []*age.X25519Identity{selfIdentity, other} {
		r, err := age.Decrypt(bytes.NewReader(ciphertext), id)
		if err != nil {
			t.Fatalf("every device must decrypt with an all-full roster: %v", err)
		}
		var plaintext bytes.Buffer
		if _, err := plaintext.ReadFrom(r); err != nil {
			t.Fatal(err)
		}
		if plaintext.String() != dummySecretValue {
			t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
		}
	}
}

// TestAddSecretRefusedOnNoSecretsSelfDevice proves the write-side
// refusal: a device whose OWN roster role is no-secrets refuses
// AddSecret outright, with the exact fixed error, rather than writing
// a secret it could never read back.
func TestAddSecretRefusedOnNoSecretsSelfDevice(t *testing.T) {
	v := newVault(t)
	ownRecipient, err := vault.DeviceRecipient(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "this-device", ownRecipient, vault.RoleNoSecrets); err != nil {
		t.Fatal(err)
	}

	err = vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue))
	if err == nil {
		t.Fatal("AddSecret must be refused on a no-secrets self device")
	}
	want := "this device is enrolled as no-secrets and cannot add or rotate a secret. Fix: use a full device."
	if err.Error() != want {
		t.Fatalf("bad error: got %q, want %q", err.Error(), want)
	}
	if vault.SecretExists(v, "openai-key") {
		t.Fatal("a refused AddSecret must not create anything")
	}
}

// TestRotateSecretRefusedOnNoSecretsSelfDevice proves the same
// write-side refusal for RotateSecret: added while the device is
// still full, then the device's own role flips to no-secrets (an
// operator hand-edit, or a role change synced in), and rotate is
// refused, leaving the existing value untouched.
func TestRotateSecretRefusedOnNoSecretsSelfDevice(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}

	ownRecipient, err := vault.DeviceRecipient(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "this-device", ownRecipient, vault.RoleNoSecrets); err != nil {
		t.Fatal(err)
	}

	err = vault.RotateSecret(v, "openai-key", nil, []byte("rotated-value"))
	if err == nil {
		t.Fatal("RotateSecret must be refused on a no-secrets self device")
	}
	want := "this device is enrolled as no-secrets and cannot add or rotate a secret. Fix: use a full device."
	if err.Error() != want {
		t.Fatalf("bad error: got %q, want %q", err.Error(), want)
	}

	after, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused rotate must never touch the existing value")
	}
}

// TestSelfNoSecretsRecognizedRegardlessOfRecipientCase is the
// regression test for a fail-open bypass a security review found in
// the self-match comparison: it used raw string equality between a
// roster entry's recipient text and this device's own canonical
// recipient, so this device's OWN roster entry — with an explicit
// role="no-secrets" — went unrecognized whenever that entry's
// recipient was written in a different letter case (a hand-edited
// devices.toml, all-uppercase bech32, is valid input a human or a
// script could produce). An unrecognized self entry fell through to
// "not enrolled" and the bootstrap default of RoleFull, so this
// no-secrets device silently kept — and regained — its own
// secrets-decrypt access. Bech32 casing carries no information (both
// the encoder and decoder fold it before computing the checksum), so
// two recipient strings equal after folding case always name the same
// underlying key; sameRecipient (secret.go) now compares this way
// instead of by raw "==".
//
// This device's own role must be recognized as no-secrets, and
// honored, regardless of the stored recipient's case:
//   - AddSecret is refused outright (the write-side gate recognizes
//     self as no-secrets).
//   - Forcing a write past that gate — ReEncryptSecrets, which has no
//     such refusal, re-encrypting a secret added before the roster
//     entry existed — drops this device from the secret's recipients,
//     and a real age.Decrypt with this device's own identity on the
//     result FAILS.
func TestSelfNoSecretsRecognizedRegardlessOfRecipientCase(t *testing.T) {
	v := newVault(t)

	// Added while this device is still unenrolled (bootstrap: full),
	// so its own identity is a recipient of the ORIGINAL ciphertext.
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}

	ownRecipient, err := vault.DeviceRecipient(v)
	if err != nil {
		t.Fatal(err)
	}
	upper := strings.ToUpper(ownRecipient)
	if upper == ownRecipient {
		t.Fatal("test setup: an age recipient must contain a lowercase letter to exercise the case-sensitivity bug")
	}
	if err := vault.AddToRoster(v, "this-device", upper, vault.RoleNoSecrets); err != nil {
		t.Fatal(err)
	}
	// A full peer, so the roster still has a valid recipient to
	// re-encrypt to once this device is (correctly) excluded — without
	// one, ReEncryptSecrets would fail outright on "no recipients
	// specified" rather than exercising the exclusion this test is
	// about.
	fullPeer, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "full-peer", fullPeer.Recipient().String(), vault.RoleFull); err != nil {
		t.Fatal(err)
	}

	// The write-side gate: AddSecret must recognize this device as
	// no-secrets and refuse, even though the roster stores its
	// recipient in a different case than DeviceRecipient returns.
	err = vault.AddSecret(v, "another-key", "openai", "", "", "human", nil, []byte(dummySecretValue))
	if err == nil {
		t.Fatal("AddSecret must be refused: this device's own roster entry is no-secrets, regardless of its recipient's case")
	}
	want := "this device is enrolled as no-secrets and cannot add or rotate a secret. Fix: use a full device."
	if err.Error() != want {
		t.Fatalf("bad error: got %q, want %q", err.Error(), want)
	}

	// Force a write past that gate: ReEncryptSecrets re-encrypts the
	// pre-existing secret to the CURRENT recipients, and carries no
	// no-secrets refusal of its own.
	if _, err := vault.ReEncryptSecrets(v); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceIdentityFor(t, v)
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), identity); err == nil {
		t.Fatal("SECURITY VIOLATION: this no-secrets device decrypted its own secret after re-encrypt, despite its roster entry's recipient being written in a different case")
	}
	// The full peer must still decrypt: the fix excludes only this
	// no-secrets device, not the whole recipient list.
	if _, err := age.Decrypt(bytes.NewReader(ciphertext), fullPeer); err != nil {
		t.Fatalf("the full peer must still decrypt after re-encrypt: %v", err)
	}
}

// TestSecretRecipientsSelfMatchBackwardCompatCanonicalCase proves the
// fix above changes nothing for the ordinary, canonical-case roster
// this device has always written for itself: a full self-entry, in
// the exact case DeviceRecipient/AddToRoster produce, is still
// recognized, still included, and still decrypts.
func TestSecretRecipientsSelfMatchBackwardCompatCanonicalCase(t *testing.T) {
	v := newVault(t)
	ownRecipient, err := vault.DeviceRecipient(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "this-device", ownRecipient, vault.RoleFull); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddSecret(v, "openai-key", "openai", "", "", "human", nil, []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := os.ReadFile(filepath.Join(v.SecretsDir(), "openai-key", "value.age"))
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceIdentityFor(t, v)
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		t.Fatalf("this device must decrypt its own secret with an ordinary, canonical-case full roster entry: %v", err)
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if plaintext.String() != dummySecretValue {
		t.Fatalf("decrypted value = %q, want %q", plaintext.String(), dummySecretValue)
	}
}
