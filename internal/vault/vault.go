package vault

import (
	"fmt"
	"os"
	"path/filepath"
)

type Vault struct {
	Root     string
	Manifest Manifest
}

// DefaultRoot returns $LOADOUT_HOME, or ~/.loadout.
func DefaultRoot() string {
	if h := os.Getenv("LOADOUT_HOME"); h != "" {
		return h
	}
	return ExpandPath("~/.loadout")
}

func Init(root string) (*Vault, error) {
	if _, err := os.Stat(filepath.Join(root, "loadout.toml")); err == nil {
		return nil, fmt.Errorf("a vault already exists at %s", root)
	}
	for _, d := range []string{root, filepath.Join(root, "skills"), filepath.Join(root, "memory"), filepath.Join(root, "render")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
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
	m, err := LoadManifest(filepath.Join(root, "loadout.toml"))
	if err != nil {
		return nil, fmt.Errorf("no vault at %s: run \"loadout init\" first", root)
	}
	return &Vault{Root: root, Manifest: m}, nil
}

func (v *Vault) SkillsDir() string { return filepath.Join(v.Root, "skills") }
func (v *Vault) MemoryDir() string { return filepath.Join(v.Root, "memory") }
func (v *Vault) RenderDir() string { return filepath.Join(v.Root, "render") }
