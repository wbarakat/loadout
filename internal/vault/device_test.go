package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestDeviceIdentityCreatesKeyFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	name, recipient, err := DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}
	if name == "" {
		t.Fatal("DeviceIdentity must return a non-empty name")
	}
	if !strings.HasPrefix(recipient, "age1") {
		t.Fatalf("recipient must be an age1 recipient, got %q", recipient)
	}

	keyPath := filepath.Join(v.Root, "device.key")
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("device.key must be mode 0600, got %o", fi.Mode().Perm())
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("device.key must be a parseable age identity: %v", err)
	}
	if got := identity.Recipient().String(); got != recipient {
		t.Fatalf("DeviceIdentity recipient %q does not match the key's own recipient %q", recipient, got)
	}
}

func TestDeviceNameFileMatchesReturnedName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	name, _, err := DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(v.Root, "device.name"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != name {
		t.Fatalf("device.name file %q does not match returned name %q", data, name)
	}
}

func TestDeviceIdentityIsStableAcrossCalls(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	name1, recipient1, err := DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}
	name2, recipient2, err := DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}
	if name1 != name2 {
		t.Fatalf("name changed across calls: %q then %q", name1, name2)
	}
	if recipient1 != recipient2 {
		t.Fatalf("recipient changed across calls: %q then %q", recipient1, recipient2)
	}
}

func TestDeviceRecipientAloneMatchesDeviceIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	_, recipient, err := DeviceIdentity(v)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DeviceRecipient(v)
	if err != nil {
		t.Fatal(err)
	}
	if again != recipient {
		t.Fatalf("DeviceRecipient = %q, want %q", again, recipient)
	}
}

// TestDeviceFilesAreGitignored proves the device key and name never
// enter history: a status check right after creation shows nothing
// new to track, and a snapshot right after is a no-op.
func TestDeviceFilesAreGitignored(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DeviceIdentity(v); err != nil {
		t.Fatal(err)
	}
	out, err := git(v, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("device.key and device.name must be gitignored, got git status %q", out)
	}
	if err := Snapshot(v, "noop"); err != nil {
		t.Fatal(err)
	}
	subjects, err := RecentSubjects(v, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 1 || subjects[0] != "init the vault" {
		t.Fatalf("device identity creation must not add a commit, got %v", subjects)
	}
}

// TestDeviceKeyUnreadableGivesFixedError proves a permission failure
// on device.key uses the standard error grammar, and that the
// message never carries any key material.
func TestDeviceKeyUnreadableGivesFixedError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a file regardless of its permissions")
	}
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DeviceIdentity(v); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(v.Root, "device.key")
	if err := os.Chmod(keyPath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(keyPath, 0o600)

	_, err = DeviceRecipient(v)
	if err == nil {
		t.Fatal("DeviceRecipient must fail when the key file is unreadable")
	}
	if !strings.Contains(err.Error(), keyPath+": the device key cannot be read:") {
		t.Fatalf("bad error: %v", err)
	}
	if !strings.Contains(err.Error(), "Fix: check the file permissions.") {
		t.Fatalf("bad error: %v", err)
	}
}

// TestDeviceKeyTooOpenModeGivesFixedError proves a key file with
// group or other permissions bits set is rejected before it is ever
// read, even though the owner can still read it fine.
func TestDeviceKeyTooOpenModeGivesFixedError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DeviceIdentity(v); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(v.Root, "device.key")
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(keyPath, 0o600)

	_, err = DeviceRecipient(v)
	want := keyPath + ": the device key file mode is too open. Fix: run chmod 600 on the file."
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// TestDeviceHostNameFallsBackForUnusableHostname proves a hostname
// that kebab-cases to nothing — every rune outside [a-z0-9] — still
// yields a usable, storable name instead of an empty string.
func TestDeviceHostNameFallsBackForUnusableHostname(t *testing.T) {
	if got := deviceHostName("日本語ホスト"); got != "device" {
		t.Fatalf("deviceHostName(%q) = %q, want %q", "日本語ホスト", got, "device")
	}
}

func TestKebabCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Waleed's-MacBook.local", "waleed-s-macbook-local"},
		{"already-good", "already-good"},
		{"UPPER_CASE", "upper-case"},
		{"---leading-and-trailing---", "leading-and-trailing"},
		{"a..b", "a-b"},
		{"192.168.1.1", "192-168-1-1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := kebabCase(c.in); got != c.want {
			t.Errorf("kebabCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestKebabCaseCharsetIsRestricted proves the output of a gnarly,
// real-looking hostname holds only [a-z0-9-].
func TestKebabCaseCharsetIsRestricted(t *testing.T) {
	got := kebabCase("Waleed's-MacBook.local")
	for _, r := range got {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			t.Fatalf("kebabCase output %q holds a disallowed rune %q", got, r)
		}
	}
}
