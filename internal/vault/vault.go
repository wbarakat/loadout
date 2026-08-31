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
// litter, the derived render output, and the lock file Lock creates.
const gitignoreContent = ".DS_Store\nrender/\nloadout.lock\n"

// writeGitignoreIfMissing writes root/.gitignore when no such file
// exists yet. Init calls it when it creates a vault; Open calls it
// too, so a vault made before this file existed heals itself.
func writeGitignoreIfMissing(root string) error {
	path := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte(gitignoreContent), 0o644)
}

func Init(root string) (*Vault, error) {
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, "loadout.toml")); err == nil {
		return nil, fmt.Errorf("a vault already exists at %s", root)
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
	return &Vault{Root: root, Manifest: m, Warnings: warnings}, nil
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
