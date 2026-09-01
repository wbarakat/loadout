package vault_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if err := vault.AddSecret(v, "openai-key", "openai", "deploy hook", "human", value); err != nil {
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
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", value); err != nil {
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
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", value); err != nil {
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
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", value); err != nil {
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
	if err := vault.AddToRoster(v, "this-device", ownRecipient); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddToRoster(v, "other-device", other.Recipient().String()); err != nil {
		t.Fatal(err)
	}

	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", value); err != nil {
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
	if err := vault.AddToRoster(v, "other-device", other.Recipient().String()); err != nil {
		t.Fatal(err)
	}

	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", value); err != nil {
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
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", []byte(dummySecretValue)); err == nil {
		t.Fatal("a duplicate secret name must be refused")
	}
}

func TestAddSecretBadNameRefused(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "Bad Name", "openai", "", "human", []byte(dummySecretValue)); err == nil {
		t.Fatal("a non-kebab-case name must be refused")
	}
	if vault.SecretExists(v, "Bad Name") {
		t.Fatal("a refused add must not create anything")
	}
}

func TestListSecretsMetadataOnly(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "zebra-key", "svc-z", "", "human", []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if err := vault.AddSecret(v, "alpha-key", "svc-a", "", "claude-code", []byte(dummySecretValue)); err != nil {
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

	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", []byte(dummySecretValue)); err != nil {
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
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", []byte(dummySecretValue)); err != nil {
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
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", []byte(dummySecretValue)); err != nil {
		t.Fatal(err)
	}
	if !vault.SecretExists(v, "openai-key") {
		t.Fatal("SecretExists must be true after AddSecret")
	}
}

func TestRemoveSecretDeletesDir(t *testing.T) {
	v := newVault(t)
	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", []byte(dummySecretValue)); err != nil {
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

	if err := vault.AddSecret(v, "openai-key", "openai", "", "human", []byte(dummySecretValue)); err != nil {
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
