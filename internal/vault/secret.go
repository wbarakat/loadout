package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// SecretExists reports whether a secret named name has metadata on
// disk.
func SecretExists(v *Vault, name string) bool {
	_, err := os.Stat(secretMetaPath(v, name))
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
// plus this device. by names who is writing it, for example "human"
// or "claude-code". value is zeroed before AddSecret returns, on
// every path, so the caller's own buffer stops holding the plaintext
// as soon as this call is done with it.
//
// INVARIANT 10: value never appears anywhere on disk except as
// ciphertext inside value.age, and never in an error message.
func AddSecret(v *Vault, name, service, hook, by string, value []byte) error {
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

	recipients, err := secretRecipients(v)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return fmt.Errorf("the secret %s cannot be encrypted: %v", name, err)
	}
	if _, err := w.Write(value); err != nil {
		return fmt.Errorf("the secret %s cannot be encrypted: %v", name, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the secret %s cannot be encrypted: %v", name, err)
	}

	if err := os.MkdirAll(secretDir(v, name), 0o755); err != nil {
		return err
	}
	at := time.Now().UTC().Format(time.RFC3339)
	meta := renderSecretMeta(name, service, hook, "", by, at)
	if err := writeFileAtomicVault(secretMetaPath(v, name), []byte(meta)); err != nil {
		return err
	}
	return writeFileAtomicVault(secretValuePath(v, name), buf.Bytes())
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
		if !e.IsDir() {
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
