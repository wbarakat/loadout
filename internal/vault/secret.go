package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// AllowedHosts lists the exact hosts (a bare host, or host:port)
	// the brokered http_request MCP tool may send this secret's value
	// to. An empty list means the broker must refuse every request
	// that references this secret: fail closed until an operator
	// opts a host in explicitly. See internal/mcp/broker.go.
	AllowedHosts []string
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

// ValidateSecretName reports whether name is a valid secret name — the
// same kebab-case grammar every scaffolded item name uses. Every
// function on this page that turns name into a filesystem path
// (secretDir, and so secretMetaPath and secretValuePath) calls this
// FIRST, before touching disk at all.
//
// This closes a path-traversal hole: filepath.Join cleans ".."
// components, so an unvalidated name like "../../outside-vault-target"
// would resolve to a path OUTSIDE the vault's secrets/ directory
// entirely. RemoveSecret would then delete that outside directory,
// and DecryptSecret or RotateSecret would read or overwrite it — all
// three a destructive Invariant-2/3 violation reachable from any
// attacker-influenced name (a hostile --secret flag on "loadout run",
// for example). Validating the grammar up front means the join can
// never see a name shaped like a path in the first place.
func ValidateSecretName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%s: not a valid secret name. Fix: use a kebab-case name like openai-key.", name)
	}
	return nil
}

// ValidateAllowedHost reports whether host is a valid allowed_hosts
// entry for a secret: a bare host, or host:port. It must carry no
// scheme, no path, no space, and no wildcard — the broker
// (internal/mcp/broker.go) compares a request's host against this
// string with a plain, exact, case-insensitive equality check, so
// anything shaped like a URL or a pattern here would mislead rather
// than help.
func ValidateAllowedHost(host string) error {
	invalid := host == "" ||
		strings.ContainsAny(host, " \t\r\n") ||
		strings.Contains(host, "://") ||
		strings.Contains(host, "/") ||
		strings.Contains(host, "*")
	if invalid {
		return fmt.Errorf("%s: not a valid allowed host. Fix: use a bare host like api.example.com or api.example.com:8443 — no scheme, no path, no wildcard.", host)
	}
	return nil
}

// validateAllowedHosts validates every host in hosts, in order,
// returning the first error found.
func validateAllowedHosts(hosts []string) error {
	for _, h := range hosts {
		if err := ValidateAllowedHost(h); err != nil {
			return err
		}
	}
	return nil
}

// parseAllowedHosts splits meta.md's raw "allowed_hosts" frontmatter
// value into a slice: comma-separated, each entry trimmed of spaces,
// empty entries dropped. An absent or empty field parses as no hosts
// at all — the fail-closed default the broker relies on.
func parseAllowedHosts(raw string) []string {
	if raw == "" {
		return nil
	}
	var hosts []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			hosts = append(hosts, part)
		}
	}
	return hosts
}

// SecretExists reports whether a secret named name has BOTH its
// metadata and its encrypted value on disk. A directory holding only
// one of the two — debris from a write AddSecret never finished, or a
// hand-edited vault — does not count as a real secret: AddSecret
// treats it as absent and safe to replace. An invalid name (see
// ValidateSecretName) is always reported absent, never stat'd.
func SecretExists(v *Vault, name string) bool {
	if ValidateSecretName(name) != nil {
		return false
	}
	if _, err := os.Stat(secretMetaPath(v, name)); err != nil {
		return false
	}
	_, err := os.Stat(secretValuePath(v, name))
	return err == nil
}

// secretRecipients lists the age recipients a secret value must be
// encrypted to: every roster device whose role is RoleFull, plus this
// device itself IF this device's own role is RoleFull. A RoleNoSecrets
// roster device is NEVER a recipient of any secret — that is the
// security invariant Phase 8a Task 2 exists to enforce: a no-secrets
// device (a future browser dashboard) provably cannot decrypt a
// secret, because its key is never on the recipient list in the first
// place.
//
// This device's own role comes from its own roster entry, found by
// matching DeviceRecipient against every entry's Recipient, using
// sameRecipient (case-insensitive) rather than raw string equality —
// see sameRecipient's own doc comment for why a plain "==" or even an
// age.ParseX25519Recipient round trip both fail to recognize a
// same-key entry written in a different letter case. A device not yet
// enrolled in the roster at all (bootstrap: the first, owner device,
// before any devices.toml exists) is treated as RoleFull — the same
// "roster ∪ self" union secretRecipients has always given an
// unenrolled device, now conditioned on role.
//
// With a roster of only full devices, or no roster at all, this
// behaves exactly as before Phase 8a: every full device plus self,
// deduped.
func secretRecipients(v *Vault) ([]age.Recipient, error) {
	entries, err := ReadRosterEntries(v)
	if err != nil {
		return nil, err
	}
	identity, err := deviceKey(v)
	if err != nil {
		return nil, err
	}
	self := identity.Recipient()

	selfRole := RoleFull
	selfEnrolled := false
	for _, e := range entries {
		if sameRecipient(e.Recipient, self.String()) {
			selfRole = e.Role
			selfEnrolled = true
			break
		}
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	recipients := make([]age.Recipient, 0, len(names)+1)
	seen := make(map[string]bool, len(names)+1)
	for _, name := range names {
		e := entries[name]
		if e.Role != RoleFull {
			continue
		}
		r, err := age.ParseX25519Recipient(e.Recipient)
		if err != nil {
			return nil, rosterErr(devicesTomlPath(v), fmt.Errorf("device %q holds an invalid recipient", name))
		}
		if seen[r.String()] {
			continue
		}
		seen[r.String()] = true
		recipients = append(recipients, r)
	}

	if !seen[self.String()] && (!selfEnrolled || selfRole == RoleFull) {
		recipients = append(recipients, self)
	}
	return recipients, nil
}

// selfRole reports this device's own role: the role on its own roster
// entry (matched by recipient), or RoleFull when this device is not
// enrolled in the roster yet (bootstrap). AddSecret and RotateSecret
// call this to refuse a write on a no-secrets device before it ever
// touches disk.
func selfRole(v *Vault) (string, error) {
	entries, err := ReadRosterEntries(v)
	if err != nil {
		return "", err
	}
	identity, err := deviceKey(v)
	if err != nil {
		return "", err
	}
	self := identity.Recipient().String()
	for _, e := range entries {
		if sameRecipient(e.Recipient, self) {
			return e.Role, nil
		}
	}
	return RoleFull, nil
}

// SelfRole reports this device's own role: RoleFull or RoleNoSecrets,
// the same value selfRole computes for AddSecret and RotateSecret.
// Exported for internal/remote's sync merge path and internal/cli's
// doctor check, which both must know whether THIS device may
// reconcile or is expected to be unable to read secrets — see
// ReEncryptSecrets and the Phase 8a whole-branch fix wave.
func SelfRole(v *Vault) (string, error) {
	return selfRole(v)
}

// sameRecipient reports whether raw — a roster entry's recipient text,
// exactly as devices.toml holds it — identifies the same age X25519
// recipient as self, which must already be in its canonical form (the
// exact string identity.Recipient().String() returns).
//
// This is deliberately a case-insensitive TEXT compare, not a
// parse-then-compare: age.ParseX25519Recipient's own bech32 decoder
// checks the decoded human-readable part against the literal lowercase
// "age" WITHOUT folding case first (filippo.io/age's x25519.go), so it
// REJECTS a validly-encoded all-uppercase recipient outright rather
// than normalizing it — parsing raw and comparing .String() would
// therefore still miss a same-key entry written in a different case,
// exactly the raw "==" bug this function replaces.
//
// This is still safe and exact: bech32's checksum is computed over the
// human-readable part folded to lowercase either way (both the
// reference algorithm and this age fork's own hrpExpand lowercase it
// internally), so casing carries no information distinguishing one
// valid recipient from another — two strings equal after folding case
// always decode to the identical public key. A raw, non-bech32,
// unparseable, or genuinely different-key string never matches self
// here either: it is simply unequal to self's canonical text once both
// are folded to the same case.
func sameRecipient(raw, self string) bool {
	return strings.EqualFold(raw, self)
}

// noSecretsWriteErr is the fixed error AddSecret and RotateSecret
// return when this device's own role is RoleNoSecrets: a no-secrets
// device must never write a secret, since secretRecipients would
// exclude it from the very ciphertext it just wrote, leaving it unable
// to read back its own value.
var noSecretsWriteErr = errors.New("this device is enrolled as no-secrets and cannot add or rotate a secret. Fix: use a full device.")

// renderSecretMeta builds a secret's plaintext meta.md frontmatter.
// It NEVER holds the secret value: only name, service, hook,
// rotate_after, by, at, and allowed_hosts.
func renderSecretMeta(name, service, hook, rotateAfter, by, at string, allowedHosts []string) string {
	return "---\n" +
		"name: " + name + "\n" +
		"service: " + service + "\n" +
		"hook: " + hook + "\n" +
		"rotate_after: " + rotateAfter + "\n" +
		"by: " + by + "\n" +
		"at: " + at + "\n" +
		"allowed_hosts: " + strings.Join(allowedHosts, ",") + "\n" +
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
// allowedHosts lists the exact hosts the brokered http_request MCP
// tool may later send this secret's value to (see
// internal/mcp/broker.go); an empty list means the broker must
// refuse every request that references this secret. Each entry is
// validated by ValidateAllowedHost before anything is written.
//
// INVARIANT 10: value never appears anywhere on disk except as
// ciphertext inside value.age, and never in an error message.
func AddSecret(v *Vault, name, service, hook, rotateAfter, by string, allowedHosts []string, value []byte) error {
	defer func() {
		for i := range value {
			value[i] = 0
		}
	}()

	role, err := selfRole(v)
	if err != nil {
		return err
	}
	if role != RoleFull {
		return noSecretsWriteErr
	}

	if err := ValidateSecretName(name); err != nil {
		return err
	}
	if err := validateAllowedHosts(allowedHosts); err != nil {
		return err
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
	meta := renderSecretMeta(name, service, hook, rotateAfter, by, at, allowedHosts)
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

// readSecretMeta reads and parses one secret's meta.md into a Secret,
// falling back to dirName for the Name field when meta.md carries no
// "name:" line of its own (the same fallback ListSecrets has always
// used). It never touches value.age.
func readSecretMeta(v *Vault, dirName string) (Secret, error) {
	raw, err := os.ReadFile(secretMetaPath(v, dirName))
	if err != nil {
		return Secret{}, err
	}
	fields, _ := parseFrontmatter(raw)
	name := fields["name"]
	if name == "" {
		name = dirName
	}
	return Secret{
		Name:         name,
		Service:      fields["service"],
		Hook:         fields["hook"],
		RotateAfter:  fields["rotate_after"],
		By:           fields["by"],
		At:           fields["at"],
		AllowedHosts: parseAllowedHosts(fields["allowed_hosts"]),
	}, nil
}

// SecretMeta reads one secret's plaintext metadata by name, without
// touching its encrypted value.age. It refuses an invalid name, and a
// name with no such secret, exactly as DecryptSecret does for those
// two cases — used by the MCP broker (internal/mcp/broker.go) to
// check a secret's allowed_hosts before ever deciding to decrypt it.
func SecretMeta(v *Vault, name string) (Secret, error) {
	if err := ValidateSecretName(name); err != nil {
		return Secret{}, err
	}
	if !SecretExists(v, name) {
		return Secret{}, fmt.Errorf("secret/%s: no such secret. Fix: run loadout secret list.", name)
	}
	return readSecretMeta(v, name)
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
		secret, err := readSecretMeta(v, e.Name())
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// A directory with no meta.md yet is not a secret:
				// skip it rather than fail the whole listing.
				continue
			}
			return nil, err
		}
		secrets = append(secrets, secret)
	}
	return secrets, nil
}

// SecretDue reports whether s is due for rotation at now: true only
// when RotateAfter parses as a non-empty, valid Go duration, At parses
// as an RFC3339 timestamp, and now is after At plus that duration. A
// secret with an empty or unparseable RotateAfter, or an unparseable
// At, is never due — that is a "no reminder set" or "cannot tell"
// case, not an error, so doctor's rotation check never fails a run
// over a malformed or hand-edited meta.md.
func SecretDue(s Secret, now time.Time) bool {
	if s.RotateAfter == "" {
		return false
	}
	dur, err := time.ParseDuration(s.RotateAfter)
	if err != nil {
		return false
	}
	at, err := time.Parse(time.RFC3339, s.At)
	if err != nil {
		return false
	}
	return now.After(at.Add(dur))
}

// RemoveSecret deletes a secret's whole directory: its metadata and
// its encrypted value together.
func RemoveSecret(v *Vault, name string) error {
	if err := ValidateSecretName(name); err != nil {
		return err
	}
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
	if err := ValidateSecretName(name); err != nil {
		return nil, err
	}
	if !SecretExists(v, name) {
		return nil, fmt.Errorf("secret/%s: no such secret. Fix: run loadout secret list.", name)
	}
	ciphertext, err := os.ReadFile(secretValuePath(v, name))
	if err != nil {
		return nil, fmt.Errorf("secret/%s: the secret value cannot be read: %v. Fix: check the file, or re-add the secret.", name, err)
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

// ReEncryptSecrets re-encrypts every secret's value.age to the
// CURRENT device roster (secretRecipients: roster ∪ self). Call it
// right after a roster change — a fresh device approval, or a
// rotation — so the next snapshot carries ciphertext the newcomer can
// actually decrypt. value.age's old bytes are atomically replaced;
// meta.md is never touched.
//
// A secret this device cannot decrypt is skipped, not fatal: this
// should not happen for a device that already holds every secret, but
// ReEncryptSecrets stays safe against it rather than assume it cannot
// occur. skipped lists such a secret's NAME only — INVARIANT 10 holds
// here too, so the caller can warn about it without ever handling or
// logging the value. A real failure past that point (the roster
// itself unreadable, or a write failing) still stops the whole run
// and returns a non-nil error, since that is not a per-secret problem
// this function can safely paper over.
//
// Every plaintext DecryptSecret hands back is zeroed once this
// function is done with it, on every path, the same way AddSecret
// zeroes its caller's own buffer.
func ReEncryptSecrets(v *Vault) (skipped []string, err error) {
	secrets, err := ListSecrets(v)
	if err != nil {
		return nil, err
	}
	if len(secrets) == 0 {
		return nil, nil
	}
	recipients, err := secretRecipients(v)
	if err != nil {
		return nil, err
	}
	for _, secret := range secrets {
		skip, err := reEncryptOneSecret(v, secret.Name, recipients)
		if err != nil {
			return skipped, err
		}
		if skip {
			skipped = append(skipped, secret.Name)
		}
	}
	return skipped, nil
}

// reEncryptOneSecret decrypts one secret with this device's own key
// and rewrites its value.age encrypted to recipients. skip is true,
// with a nil error, when this device cannot decrypt the secret at
// all: ReEncryptSecrets treats that as one skipped item, never a
// reason to fail the whole run.
//
// This treats ANY DecryptSecret error as a skip, inherited from
// DecryptSecret's own single fixed error for every decrypt failure: a
// local device.key I/O error (rather than a genuine "not a recipient"
// case) would therefore also present as a skip here. Acknowledged;
// tightening that distinction is deferred.
func reEncryptOneSecret(v *Vault, name string, recipients []age.Recipient) (skip bool, err error) {
	plaintext, err := DecryptSecret(v, name)
	if err != nil {
		return true, nil
	}
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()
	return false, writeSecretValue(v, name, recipients, plaintext)
}

// writeSecretValue age-encrypts plaintext to recipients and
// atomically replaces secret name's value.age: a temp file written
// next to it, then renamed into place, so a crash mid-write never
// leaves value.age half-written or missing.
//
// INVARIANT 10: plaintext never appears in an error message here.
func writeSecretValue(v *Vault, name string, recipients []age.Recipient, plaintext []byte) error {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return fmt.Errorf("secret/%s: cannot be re-encrypted: %v", name, err)
	}
	// age buffers plaintext chunks inside its own STREAM writer while
	// it encrypts. This code cannot reach or zero that buffer: it is a
	// known, accepted, library-level exposure window (see AddSecret).
	if _, err := w.Write(plaintext); err != nil {
		return fmt.Errorf("secret/%s: cannot be re-encrypted: %v", name, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("secret/%s: cannot be re-encrypted: %v", name, err)
	}

	dir := secretDir(v, name)
	tmp, err := os.CreateTemp(dir, ".value.age.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, secretValuePath(v, name))
}

// writeSecretMeta atomically replaces secret name's meta.md: a temp
// file written next to it, then renamed into place, the same pattern
// writeSecretValue uses for value.age, so a crash mid-write never
// leaves meta.md half-written.
func writeSecretMeta(v *Vault, name, content string) error {
	dir := secretDir(v, name)
	tmp, err := os.CreateTemp(dir, ".meta.md.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, secretMetaPath(v, name))
}

// RotateSecret replaces an existing secret's value: it keeps the
// secret's own service, hook, rotate_after, and by fields exactly as
// they were, stamps a fresh at, and re-encrypts newValue to the
// CURRENT device roster (secretRecipients: roster ∪ self) — the write
// path AddSecret and ReEncryptSecrets both use.
//
// allowedHosts follows the same "keep unless given" rule as the other
// preserved fields: nil means keep the secret's current AllowedHosts
// unchanged; a non-nil slice (even an empty one, to clear every host)
// replaces it, after ValidateAllowedHost passes on every entry.
//
// It refuses a name that does not exist yet: rotate replaces a
// secret's value, it does not create one (use AddSecret for that).
//
// value.age is replaced first, then meta.md: if the meta.md rewrite
// fails after a successful value.age replacement, the new value is
// already live and safe, and only the rotation timestamp is stale —
// never the reverse, where meta.md would claim a fresh rotation that
// never actually replaced the value.
//
// newValue is zeroed before RotateSecret returns, on every path, the
// same way AddSecret zeroes its own caller's buffer.
//
// INVARIANT 10: newValue never appears anywhere on disk except as
// ciphertext inside value.age, and never in an error message.
func RotateSecret(v *Vault, name string, allowedHosts []string, newValue []byte) error {
	defer func() {
		for i := range newValue {
			newValue[i] = 0
		}
	}()

	role, err := selfRole(v)
	if err != nil {
		return err
	}
	if role != RoleFull {
		return noSecretsWriteErr
	}

	if err := ValidateSecretName(name); err != nil {
		return err
	}
	if !SecretExists(v, name) {
		return fmt.Errorf("secret/%s: no such secret. Fix: run loadout secret list.", name)
	}
	meta, err := readSecretMeta(v, name)
	if err != nil {
		return err
	}
	hosts := meta.AllowedHosts
	if allowedHosts != nil {
		if err := validateAllowedHosts(allowedHosts); err != nil {
			return err
		}
		hosts = allowedHosts
	}
	recipients, err := secretRecipients(v)
	if err != nil {
		return err
	}
	if err := writeSecretValue(v, name, recipients, newValue); err != nil {
		return err
	}
	at := time.Now().UTC().Format(time.RFC3339)
	rendered := renderSecretMeta(meta.Name, meta.Service, meta.Hook, meta.RotateAfter, meta.By, at, hosts)
	return writeSecretMeta(v, name, rendered)
}
