package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
)

// Secret is one secret item's plaintext metadata. It NEVER holds the
// secret's value (INVARIANT 10): the value lives only as ciphertext
// in value.age, and a caller that wants it back must decrypt that
// file explicitly (Task 2's DecryptSecret).
type Secret struct {
	Name        string
	Service     string
	Hook        string
	RotateAfter string
	By          string
	At          string
}

// secretDir returns the directory that holds one secret's two files:
// meta.md and value.age.
func secretDir(v *Vault, name string) string {
	return filepath.Join(v.SecretsDir(), name)
}

// secretMetaPath returns the path to a secret's plaintext metadata
// file.
func secretMetaPath(v *Vault, name string) string {
	return filepath.Join(secretDir(v, name), "meta.md")
}

// secretValuePath returns the path to a secret's age-encrypted value
// file. Its bytes are ciphertext only: the plaintext value never
// touches disk anywhere else.
func secretValuePath(v *Vault, name string) string {
	return filepath.Join(secretDir(v, name), "value.age")
}

// SecretExists reports whether a secret named name has BOTH its
// metadata and its encrypted value on disk. A directory holding only
// one of the two — debris from a write AddSecret never finished, or a
// hand-edited vault — does not count as a real secret: AddSecret
// treats it as absent and safe to replace.
func SecretExists(v *Vault, name string) bool {
	if _, err := os.Stat(secretMetaPath(v, name)); err != nil {
		return false
	}
	_, err := os.Stat(secretValuePath(v, name))
	return err == nil
}

// secretRecipients lists the age recipients a secret value must be
// encrypted to: every device roster recipient, plus this device
// itself, so the device that creates a secret can always decrypt it
// again, even before it is enrolled in the roster. It builds on
// rosterRecipients (snapshot.go) — the same roster-reading logic
// PackSnapshot's packRecipients uses — so devices.toml is read and
// parsed in exactly one place.
func secretRecipients(v *Vault) ([]age.Recipient, error) {
	recipients, err := rosterRecipients(v)
	if err != nil {
		return nil, err
	}
	identity, err := deviceKey(v)
	if err != nil {
		return nil, err
	}
	self := identity.Recipient()
	for _, r := range recipients {
		if x, ok := r.(*age.X25519Recipient); ok && x.String() == self.String() {
			return recipients, nil
		}
	}
	return append(recipients, self), nil
}

// renderSecretMeta builds a secret's plaintext meta.md frontmatter.
// It NEVER holds the secret value: only name, service, hook,
// rotate_after, by, and at.
func renderSecretMeta(name, service, hook, rotateAfter, by, at string) string {
	return "---\n" +
		"name: " + name + "\n" +
		"service: " + service + "\n" +
		"hook: " + hook + "\n" +
		"rotate_after: " + rotateAfter + "\n" +
		"by: " + by + "\n" +
		"at: " + at + "\n" +
		"---\n"
}

// AddSecret creates a new secret: meta.md holds its plaintext
// metadata, value.age holds value age-encrypted to the device roster
// plus this device. rotateAfter is an optional duration string (for
// example "720h"), stored as-is in meta.md; empty means no rotation
// reminder. by names who is writing it, for example "human" or
// "claude-code". value is zeroed before AddSecret returns, on every
// path, so the caller's own buffer stops holding the plaintext as
// soon as this call is done with it.
//
// AddSecret writes meta.md and value.age into a temp directory next
// to secrets/<name>, then renames that temp directory into place in
// one step. This makes the pair atomic as a unit: a crash mid-write
// leaves only an orphaned temp directory behind, never a half-written
// secrets/<name> that SecretExists would wrongly treat as real.
//
// INVARIANT 10: value never appears anywhere on disk except as
// ciphertext inside value.age, and never in an error message.
func AddSecret(v *Vault, name, service, hook, rotateAfter, by string, value []byte) error {
	defer func() {
		for i := range value {
			value[i] = 0
		}
	}()

	if !namePattern.MatchString(name) {
		return fmt.Errorf("use a kebab-case name, for example: openai-key")
	}
	if SecretExists(v, name) {
		return fmt.Errorf("the secret %s already exists. Fix: choose another name, or rotate the existing secret.", name)
	}
	// A directory may already sit at secretDir(v, name) without
	// counting as a real secret (SecretExists just said so): debris
	// from an interrupted write, or a hand-edited vault. Clear it so
	// the rename below lands cleanly.
	if err := os.RemoveAll(secretDir(v, name)); err != nil {
		return err
	}

	recipients, err := secretRecipients(v)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return fmt.Errorf("the secret %s cannot be encrypted: %v", name, err)
	}
	// age buffers plaintext chunks inside its own STREAM writer while
	// it encrypts. This code cannot reach or zero that buffer: it is a
	// known, accepted, library-level exposure window.
	if _, err := w.Write(value); err != nil {
		return fmt.Errorf("the secret %s cannot be encrypted: %v", name, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the secret %s cannot be encrypted: %v", name, err)
	}

	if err := os.MkdirAll(v.SecretsDir(), 0o755); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(v.SecretsDir(), "."+name+".tmp-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir) // no-op once the rename below succeeds

	at := time.Now().UTC().Format(time.RFC3339)
	meta := renderSecretMeta(name, service, hook, rotateAfter, by, at)
	if err := os.WriteFile(filepath.Join(tmpDir, "meta.md"), []byte(meta), 0o644); err != nil {
		return err
	}
	// value.age holds the encrypted key material: mode 0600, matching
	// device.key's own sensitivity.
	if err := os.WriteFile(filepath.Join(tmpDir, "value.age"), buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpDir, secretDir(v, name))
}

// ListSecrets reads every secret's metadata, in name order. It never
// touches value.age: a Secret carries no value field, only what
// meta.md holds in plaintext.
func ListSecrets(v *Vault) ([]Secret, error) {
	entries, err := os.ReadDir(v.SecretsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var secrets []Secret
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			// A dot-prefixed entry is an AddSecret temp directory: one
			// that never got renamed into place, orphaned by a crash
			// between its own write and the final rename.
			continue
		}
		if !SecretExists(v, e.Name()) {
			// Debris: a directory missing meta.md or value.age is not
			// a complete secret. AddSecret treats it the same way.
			continue
		}
		raw, err := os.ReadFile(filepath.Join(v.SecretsDir(), e.Name(), "meta.md"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// A directory with no meta.md yet is not a secret:
				// skip it rather than fail the whole listing.
				continue
			}
			return nil, err
		}
		fields, _ := parseFrontmatter(raw)
		name := fields["name"]
		if name == "" {
			name = e.Name()
		}
		secrets = append(secrets, Secret{
			Name:        name,
			Service:     fields["service"],
			Hook:        fields["hook"],
			RotateAfter: fields["rotate_after"],
			By:          fields["by"],
			At:          fields["at"],
		})
	}
	return secrets, nil
}

// RemoveSecret deletes a secret's whole directory: its metadata and
// its encrypted value together.
func RemoveSecret(v *Vault, name string) error {
	if !SecretExists(v, name) {
		return fmt.Errorf("secret/%s: no such item. Fix: run loadout secret list.", name)
	}
	return os.RemoveAll(secretDir(v, name))
}

// DecryptSecret reads secret/<name>'s value.age and decrypts it with
// this device's own age identity. The caller owns the returned
// plaintext: it must zero the slice once it is done with it, the same
// way AddSecret's own caller must zero the value it hands in.
//
// INVARIANT 10: the plaintext never appears in an error. A missing
// secret and an undecryptable one each get a fixed message that names
// only the secret, never the value — there is no value to name yet in
// either case.
func DecryptSecret(v *Vault, name string) ([]byte, error) {
	if !SecretExists(v, name) {
		return nil, fmt.Errorf("secret/%s: no such secret. Fix: run loadout secret list.", name)
	}
	ciphertext, err := os.ReadFile(secretValuePath(v, name))
	if err != nil {
		return nil, err
	}
	identity, err := deviceKey(v)
	if err != nil {
		return nil, err
	}
	cannotReadErr := fmt.Errorf("secret/%s: this device cannot read the secret. Fix: approve this device, then sync, so the secret re-encrypts to it.", name)
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return nil, cannotReadErr
	}
	var plaintext bytes.Buffer
	if _, err := plaintext.ReadFrom(r); err != nil {
		return nil, cannotReadErr
	}
	return plaintext.Bytes(), nil
}
