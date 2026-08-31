package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// deviceKeyPath returns the path to the vault's age identity file.
func deviceKeyPath(v *Vault) string { return filepath.Join(v.Root, "device.key") }

// deviceNamePath returns the path to the vault's device name file.
func deviceNamePath(v *Vault) string { return filepath.Join(v.Root, "device.name") }

// DeviceIdentity returns this device's name and its age recipient. On
// the first call for a vault it generates the device key and picks
// the device name; every later call reads the same two files back.
// The device key itself never leaves this package: only the derived
// recipient is ever returned.
func DeviceIdentity(v *Vault) (name, recipient string, err error) {
	name, err = deviceName(v)
	if err != nil {
		return "", "", err
	}
	identity, err := deviceKey(v)
	if err != nil {
		return "", "", err
	}
	return name, identity.Recipient().String(), nil
}

// DeviceRecipient returns this device's age recipient, generating the
// device key on first call. Later tasks use it to encrypt a synced
// vault to every known device.
func DeviceRecipient(v *Vault) (string, error) {
	identity, err := deviceKey(v)
	if err != nil {
		return "", err
	}
	return identity.Recipient().String(), nil
}

// deviceKey reads the vault's age identity, generating and storing
// one on first call. The identity string never appears in an error:
// a read or parse failure names only the path.
func deviceKey(v *Vault) (*age.X25519Identity, error) {
	path := deviceKeyPath(v)
	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: the device key cannot be read: %v. Fix: check the file permissions.", path, err)
		}
		return generateDeviceKey(v, path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s: the device key file mode is too open. Fix: run chmod 600 on the file.", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: the device key cannot be read: %v. Fix: check the file permissions.", path, err)
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("%s: the device key cannot be read: not a valid age identity. Fix: remove the file and run any loadout command to create a new one.", path)
	}
	return identity, nil
}

// generateDeviceKey creates a fresh age identity and stores it at
// path, mode 0600. It also heals the vault .gitignore, so the key
// never has a chance to enter history.
func generateDeviceKey(v *Vault, path string) (*age.X25519Identity, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("%s: the device identity cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	if err := writeGitignoreIfMissing(v.Root); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(identity.String()+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("%s: the device identity cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	return identity, nil
}

// deviceName reads the vault's device name, picking and storing the
// kebab-cased hostname on first call.
func deviceName(v *Vault) (string, error) {
	path := deviceNamePath(v)
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s: the device name cannot be read: %v. Fix: check the file permissions.", path, err)
	}
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("the hostname cannot be read: %v. Fix: set a device name in %s.", err, path)
	}
	name := deviceHostName(host)
	if err := writeGitignoreIfMissing(v.Root); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(name+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("%s: the device name cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	return name, nil
}

// deviceHostName reduces host to a device name: kebab-cased, and the
// fixed name "device" when that leaves nothing usable. A symbol-only
// or non-ASCII hostname would otherwise kebab-case to "", and
// deviceName would store that empty name forever.
func deviceHostName(host string) string {
	if name := kebabCase(host); name != "" {
		return name
	}
	return "device"
}

// kebabCase lowercases s and keeps only [a-z0-9-]: every other rune
// becomes "-", repeated "-" runs collapse to one, and the result is
// trimmed of leading and trailing "-".
func kebabCase(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
