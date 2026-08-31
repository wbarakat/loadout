// Package vault owns the loadout vault: its manifest, its content,
// and its history.
package vault

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Manifest struct {
	Version  int                      `toml:"version"`
	Adapters map[string]AdapterConfig `toml:"adapters"`
}

type AdapterConfig struct {
	Enabled    bool     `toml:"enabled"`
	SkillsDir  string   `toml:"skills_dir,omitempty"`
	MemoryFile string   `toml:"memory_file,omitempty"`
	Targets    []string `toml:"targets,omitempty"`
}

func DefaultManifest() Manifest {
	return Manifest{
		Version: 1,
		Adapters: map[string]AdapterConfig{
			"claude-code": {Enabled: true, SkillsDir: "~/.claude/skills", MemoryFile: "~/.claude/CLAUDE.md"},
			"pi":          {Enabled: true, SkillsDir: "~/.pi/agent/skills", MemoryFile: "~/.pi/AGENTS.md"},
			"agents-md":   {Enabled: false},
		},
	}
}

func LoadManifest(path string) (Manifest, error) {
	var m Manifest
	_, err := toml.DecodeFile(path, &m)
	return m, err
}

func SaveManifest(path string, m Manifest) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(m)
}

// ExpandPath replaces a leading "~" with the user home directory.
func ExpandPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
