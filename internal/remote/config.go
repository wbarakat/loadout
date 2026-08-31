// Package remote implements the loadout side of cloud sync: the
// remote.toml configuration, the HTTP client that talks to loadoutd
// (Task 4's server), and the merge that reconciles a pulled snapshot
// with the local working tree. Every file this package touches under
// the vault root is device-local (remote.toml, .sync-state.json) or
// already part of SyncedSet (skills/, memory/, devices.toml): it
// never invents a new synced path.
package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"loadout.dev/loadout/internal/vault"
)

// ErrNotConfigured is what Load returns when the vault holds no
// remote.toml yet: every caller that reaches it prints the same fix,
// so callers compare with errors.Is rather than parsing text.
var ErrNotConfigured = errors.New("no remote configured. Fix: run loadout remote add <url> <token>.")

// Config is the device-local remote configuration: the loadoutd url,
// this device's bearer token, and the last snapshot version this
// device is known to hold. It lives at <root>/remote.toml, mode 0600,
// and never enters git history (Task 2 already lists it in the
// vault .gitignore). The token must never appear in output or a log
// line.
type Config struct {
	URL         string `toml:"url"`
	Token       string `toml:"token"`
	LastVersion string `toml:"last_version"`
}

// configPath returns the path to the vault's remote configuration.
func configPath(v *vault.Vault) string { return filepath.Join(v.Root, "remote.toml") }

// Load reads the vault's remote configuration. It returns
// ErrNotConfigured, unwrapped, when no remote.toml exists yet, so a
// caller can test for it with errors.Is before it prints anything.
func Load(v *vault.Vault) (*Config, error) {
	path := configPath(v)
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotConfigured
		}
		return nil, fmt.Errorf("%s: the remote configuration cannot be read: %v. Fix: run loadout remote add <url> <token> again.", path, err)
	}
	return &cfg, nil
}

// Save writes cfg to the vault's remote.toml. It writes a temp file
// first, at mode 0600 throughout, then renames it into place, so a
// crash mid-write never leaves a partial file behind and the token
// is never briefly readable by another user on the machine.
func Save(v *vault.Vault, cfg *Config) error {
	path := configPath(v)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("%s: the remote configuration cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("%s: the remote configuration cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("%s: the remote configuration cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s: the remote configuration cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	return os.Rename(tmp, path)
}

// SetLastVersion updates just the last-synced version in the vault's
// remote configuration, preserving the url and the token.
func SetLastVersion(v *vault.Vault, version string) error {
	cfg, err := Load(v)
	if err != nil {
		return err
	}
	cfg.LastVersion = version
	return Save(v, cfg)
}

// syncState is .sync-state.json's on-disk shape: the version this
// device last synced to, and the vault commit whose content matched
// that version at the time — the merge's base tree the next sync
// compares local and incoming content against. It is gitignored
// (Task 1); it never syncs, since it is meaningful only to this one
// device's next merge.
type syncState struct {
	Version    string `json:"version"`
	BaseCommit string `json:"baseCommit"`
}

// syncStatePath returns the path to the vault's local sync-state file.
func syncStatePath(v *vault.Vault) string { return filepath.Join(v.Root, ".sync-state.json") }

// readSyncState reads .sync-state.json. A vault that has never synced
// (or a device syncing for the first time) reads back as a zero
// syncState, not an error: its BaseCommit is then "", which the merge
// treats as an empty base tree.
func readSyncState(v *vault.Vault) (syncState, error) {
	path := syncStatePath(v)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return syncState{}, nil
		}
		return syncState{}, fmt.Errorf("%s: the sync state cannot be read: %v. Fix: remove the file to force a full re-merge.", path, err)
	}
	var st syncState
	if err := json.Unmarshal(data, &st); err != nil {
		return syncState{}, fmt.Errorf("%s: the sync state cannot be read: %v. Fix: remove the file to force a full re-merge.", path, err)
	}
	return st, nil
}

// writeSyncState records st to .sync-state.json, atomically.
func writeSyncState(v *vault.Vault, st syncState) error {
	path := syncStatePath(v)
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("%s: the sync state cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s: the sync state cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	return nil
}
