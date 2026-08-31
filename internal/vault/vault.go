package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Vault struct {
	Root     string
	Manifest Manifest
	// Warnings lists non-fatal problems LoadManifest found while
	// opening the vault, one entry per unknown manifest key.
	Warnings []string
}

// DefaultRoot returns $LOADOUT_HOME, or ~/.loadout.
func DefaultRoot() string {
	if h := os.Getenv("LOADOUT_HOME"); h != "" {
		return ExpandPath(h)
	}
	return ExpandPath("~/.loadout")
}

// structuralDirs lists the vault's fixed directories. Init creates
// them; Open recreates any that went missing, so a stray "rm -rf"
// does not wedge the vault.
func structuralDirs(root string) []string {
	return []string{filepath.Join(root, "skills"), filepath.Join(root, "memory"), filepath.Join(root, "render")}
}

// gitkeepDirs lists the structural directories that get a .gitkeep
// file, so git tracks them while they hold no content yet. render/
// is not among them: it holds only derived output, and the vault
// .gitignore excludes the whole directory.
func gitkeepDirs(root string) []string {
	return []string{filepath.Join(root, "skills"), filepath.Join(root, "memory")}
}

// gitignoreContent lists the vault paths git must never track: OS
// litter, the derived render output, the lock file Lock creates, the
// device manifest, the device identity, and the sync configuration
// and state Phase 4 adds. Decision 13 (spec v3.1 §16) keeps the
// manifest, the device key, the device name, and the remote
// configuration device-local; only skills, memory, and the device
// roster sync. remote.toml and .sync-state.json arrive in later
// tasks; ignoring them now is safe and saves a second edit here.
const gitignoreContent = ".DS_Store\nrender/\nloadout.lock\nloadout.toml\ndevice.key\ndevice.name\nremote.toml\n.sync-state.json\n"

// SyncedSet lists the vault paths that sync between devices: skills,
// memory, and the device roster. Everything else in the vault —
// the manifest, the device key, the device name, and the remote
// configuration — is device-local and never syncs. Decision 13
// (spec v3.1 §16) fixes this split; later sync tasks read this list
// rather than repeating it.
func SyncedSet() []string {
	return []string{"skills", "memory", "devices.toml"}
}

// writeGitignoreIfMissing writes root/.gitignore when no such file
// exists yet, and adds any gitignoreContent line missing from an
// existing file. Init calls it when it creates a vault; Open calls it
// too, so a vault made before a line existed heals itself.
func writeGitignoreIfMissing(root string) error {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(path, []byte(gitignoreContent), 0o644)
		}
		return err
	}
	present := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		present[strings.TrimSpace(line)] = true
	}
	content := string(data)
	changed := false
	for _, line := range strings.Split(strings.TrimRight(gitignoreContent, "\n"), "\n") {
		if present[line] {
			continue
		}
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += line + "\n"
		changed = true
	}
	if !changed {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func Init(root string) (*Vault, error) {
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, "loadout.toml")); err == nil {
		return nil, fmt.Errorf("a vault already exists at %s. Fix: open it with any loadout command, or choose another LOADOUT_HOME.", root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	for _, d := range structuralDirs(root) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	for _, d := range gitkeepDirs(root) {
		if err := os.WriteFile(filepath.Join(d, ".gitkeep"), nil, 0o644); err != nil {
			return nil, err
		}
	}
	if err := writeGitignoreIfMissing(root); err != nil {
		return nil, err
	}
	m := DefaultManifest()
	manifestPath := filepath.Join(root, "loadout.toml")
	if err := SaveManifest(manifestPath, m); err != nil {
		return nil, err
	}
	v := &Vault{Root: root, Manifest: m}
	if err := initHistory(v); err != nil {
		os.Remove(manifestPath)
		return nil, err
	}
	return v, nil
}

func Open(root string) (*Vault, error) {
	var absErr error
	root, absErr = filepath.Abs(root)
	if absErr != nil {
		return nil, absErr
	}
	m, warnings, err := LoadManifest(filepath.Join(root, "loadout.toml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no vault at %s: run \"loadout init\" first", root)
		}
		var ve *versionError
		if errors.As(err, &ve) {
			return nil, err
		}
		return nil, fmt.Errorf("the manifest at %s is unreadable: %v", root, err)
	}
	if err := validateManifestPaths(m); err != nil {
		return nil, err
	}
	// The three content directories are structural: recreate any that
	// went missing, so a stray "rm -rf" does not wedge the vault.
	for _, d := range structuralDirs(root) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := writeGitignoreIfMissing(root); err != nil {
		return nil, err
	}
	v := &Vault{Root: root, Manifest: m, Warnings: warnings}
	healTrackedManifest(v)
	return v, nil
}

// healTrackedManifest untracks loadout.toml when a pre-Phase 4 vault
// still has it committed to history: Decision 13 (spec v3.1 §16)
// keeps the manifest device-local, so history must not carry it
// forward. It runs quietly and changes nothing on a healthy vault,
// where the file is already untracked, so after the first heal every
// later Open finds it so and does nothing. It also does nothing on a
// vault whose history is missing, leaving that vault's own error path
// (noHistoryErr, raised later by log, context, and undo) intact
// instead of failing here on the tracking probe.
//
// It also does nothing while an embedded skill repository is present:
// Snapshot refuses to run there, and this heal would otherwise untrack
// the file with no commit to show for it. Skipping leaves loadout.toml
// tracked, so the next Open retries once the user removes the embedded
// .git (doctor already tells them how).
//
// If Snapshot itself fails after a successful untrack — an embedded
// repo that appears mid-heal, or an index-lock race — the split is
// not lost: git rm --cached already staged the deletion, so the next
// Snapshot any verb runs commits it. Only the "split the manifest"
// marker message is best-effort; the untracking is not.
func healTrackedManifest(v *Vault) {
	out, err := git(v, "ls-files", "loadout.toml")
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	if repos, err := EmbeddedSkillRepos(v); err != nil || len(repos) > 0 {
		return
	}
	if _, err := git(v, "rm", "--cached", "--quiet", "loadout.toml"); err != nil {
		return
	}
	_ = Snapshot(v, "split the manifest")
}

// validateManifestPaths checks that every path an adapter writes to
// is an absolute path or a ~ path. A path relative to the current
// directory would point somewhere different on every run.
func validateManifestPaths(m Manifest) error {
	names := make([]string, 0, len(m.Adapters))
	for name := range m.Adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cfg := m.Adapters[name]
		if err := checkAbsOrHome("adapters."+name+".skills_dir", cfg.SkillsDir); err != nil {
			return err
		}
		if err := checkAbsOrHome("adapters."+name+".memory_file", cfg.MemoryFile); err != nil {
			return err
		}
		for _, target := range cfg.Targets {
			if err := checkAbsOrHome("adapters."+name+".targets", target); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkAbsOrHome reports an error naming key if value is neither
// empty, absolute, nor a ~ path.
func checkAbsOrHome(key, value string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~") {
		return nil
	}
	return fmt.Errorf("the manifest key %s holds a relative path %q. Fix: use an absolute path or a ~ path.", key, value)
}

func (v *Vault) SkillsDir() string { return filepath.Join(v.Root, "skills") }
func (v *Vault) MemoryDir() string { return filepath.Join(v.Root, "memory") }
func (v *Vault) RenderDir() string { return filepath.Join(v.Root, "render") }
