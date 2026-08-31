// Package vault owns the loadout vault: its manifest, its content,
// and its history.
package vault

import (
	"fmt"
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
			"pi":          {Enabled: true, SkillsDir: "~/.pi/agent/skills", MemoryFile: "~/.pi/agent/AGENTS.md"},
			"codex":       {Enabled: false, SkillsDir: "~/.codex/skills", MemoryFile: "~/.codex/AGENTS.md"},
			"gemini":      {Enabled: false, SkillsDir: "~/.gemini/skills", MemoryFile: "~/.gemini/GEMINI.md"},
			"cursor":      {Enabled: false, SkillsDir: "~/.cursor/skills"},
			"agents-md":   {Enabled: false},
		},
	}
}

// maxManifestVersion is the highest manifest version this build
// understands.
const maxManifestVersion = 1

// versionError reports a manifest version this build does not
// understand. Open surfaces its text as-is, not wrapped in the
// "unreadable" text it uses for parse failures.
type versionError struct {
	version int
}

func (e *versionError) Error() string {
	return fmt.Sprintf("the vault manifest is version %d; this loadout build understands version %d. Fix: upgrade loadout.", e.version, maxManifestVersion)
}

// LoadManifest reads the manifest at path. Alongside the manifest it
// returns one warning per key the file holds that this build does
// not recognize (via toml.MetaData.Undecoded); the manifest still
// loads despite these. A manifest version newer than this build
// understands is a hard error instead, since loadout cannot know
// what such a manifest means.
func LoadManifest(path string) (Manifest, []string, error) {
	var m Manifest
	meta, err := toml.DecodeFile(path, &m)
	if err != nil {
		return Manifest{}, nil, err
	}
	if m.Version > maxManifestVersion {
		return Manifest{}, nil, &versionError{version: m.Version}
	}
	var warnings []string
	for _, key := range meta.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("the manifest key %s is unknown; loadout ignores it.", key.String()))
	}
	return m, warnings, nil
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
