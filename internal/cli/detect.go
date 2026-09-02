package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// DetectedTool is one agent tool's installation status on this
// machine, as reported by detectTools.
type DetectedTool struct {
	// Name is the tool's short name, for example "claude-code".
	Name string
	// Present reports whether this tool looks installed: its root
	// directory exists, or its binary is on PATH. Cursor also counts
	// its OS-native app-data directory.
	Present bool
	// Root is the tool's resolved root directory: its env override
	// when one is set (claude-code, codex), else its default under
	// home.
	Root string
	// SkillsDir is the tool's default skills directory, under Root —
	// the same default internal/vault's DefaultManifest uses.
	SkillsDir string
	// MemoryFile is the tool's default memory file, under Root. Empty
	// for a tool with no standalone memory file loadout targets
	// (cursor, hermes — see internal/adapter's own cursor.go/hermes.go
	// comments).
	MemoryFile string
}

// toolSpec is one supported tool's detection recipe: how to resolve
// its root directory, which binaries on PATH count as it being
// installed, and its default skills_dir/memory_file layout under that
// root.
type toolSpec struct {
	name string
	// envOverride is the environment variable that relocates this
	// tool's whole root directory, checked before the default path.
	// Empty when the tool has no such override — only claude-code and
	// codex have one (source map §7).
	envOverride string
	// defaultRoot resolves this tool's default root directory from
	// home, used when envOverride is empty or unset.
	defaultRoot func(home string) string
	// bins lists every binary name that counts as this tool being on
	// PATH. Cursor has two: "cursor" and "cursor-agent".
	bins []string
	// skillsDir resolves this tool's default skills directory from
	// its resolved root.
	skillsDir func(root string) string
	// memoryFile resolves this tool's default memory file from its
	// resolved root. nil for a tool with no standalone memory file.
	memoryFile func(root string) string
	// extraPresent reports one more presence signal beyond root/PATH.
	// Only cursor has one: its OS-native Electron app-data directory.
	// nil for every other tool.
	extraPresent func(home string) bool
}

// toolSpecs lists every tool detectTools reports, in the stable order
// it reports them. Devin is deliberately absent: it is hosted, with
// no durable local directory or binary to detect (source map §6).
var toolSpecs = []toolSpec{
	{
		name:        "claude-code",
		envOverride: "CLAUDE_CONFIG_DIR",
		defaultRoot: func(home string) string { return filepath.Join(home, ".claude") },
		bins:        []string{"claude"},
		skillsDir:   func(root string) string { return filepath.Join(root, "skills") },
		memoryFile:  func(root string) string { return filepath.Join(root, "CLAUDE.md") },
	},
	{
		name:        "codex",
		envOverride: "CODEX_HOME",
		defaultRoot: func(home string) string { return filepath.Join(home, ".codex") },
		bins:        []string{"codex"},
		skillsDir:   func(root string) string { return filepath.Join(root, "skills") },
		memoryFile:  func(root string) string { return filepath.Join(root, "AGENTS.md") },
	},
	{
		name:        "cursor",
		defaultRoot: func(home string) string { return filepath.Join(home, ".cursor") },
		bins:        []string{"cursor", "cursor-agent"},
		skillsDir:   func(root string) string { return filepath.Join(root, "skills") },
		// no memoryFile: cursor has no standalone memory file loadout
		// targets.
		extraPresent: func(home string) bool {
			info, err := os.Stat(cursorAppDataDir(home))
			return err == nil && info.IsDir()
		},
	},
	{
		name:        "hermes",
		defaultRoot: func(home string) string { return filepath.Join(home, ".hermes") },
		bins:        []string{"hermes"},
		skillsDir:   func(root string) string { return filepath.Join(root, "skills") },
		// no memoryFile: hermes's MEMORY.md/USER.md are agent-managed,
		// not a stable user-owned instructions file loadout targets.
	},
	{
		name:        "pi",
		defaultRoot: func(home string) string { return filepath.Join(home, ".pi", "agent") },
		bins:        []string{"pi"},
		skillsDir:   func(root string) string { return filepath.Join(root, "skills") },
		memoryFile:  func(root string) string { return filepath.Join(root, "AGENTS.md") },
	},
	{
		name:        "gemini",
		defaultRoot: func(home string) string { return filepath.Join(home, ".gemini") },
		bins:        []string{"gemini"},
		skillsDir:   func(root string) string { return filepath.Join(root, "skills") },
		memoryFile:  func(root string) string { return filepath.Join(root, "GEMINI.md") },
	},
	{
		name:        "droid",
		defaultRoot: func(home string) string { return filepath.Join(home, ".factory") },
		bins:        []string{"droid"},
		skillsDir:   func(root string) string { return filepath.Join(root, "skills") },
		memoryFile:  func(root string) string { return filepath.Join(root, "AGENTS.md") },
	},
}

// cursorAppDataDir resolves the OS-native path to the Cursor Electron
// app's own app-data tree — an extra presence signal alongside the
// CLI-managed ~/.cursor tree (source map §3, §7): macOS
// "~/Library/Application Support/Cursor", Linux "~/.config/Cursor".
// detectTools only ever os.Stats this path; it never opens a file
// inside it.
func cursorAppDataDir(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Cursor")
	}
	return filepath.Join(home, ".config", "Cursor")
}

// detectTools reports, for every agent tool loadout supports, whether
// it looks installed on this machine and where its default
// skills_dir/memory_file would live. Presence checks only two things:
// a tool's root directory exists (os.Stat), or one of its binaries is
// on PATH (lookPath) — cursor also counts its OS-native app-data
// directory. detectTools never opens or reads a config or credential
// file: a tool's root can hold live secrets right next to the files
// loadout cares about (source map §2, §8), and detection has no
// business touching them.
//
// home and lookPath are both injectable, so a test can point them at
// a fixture directory and a fake PATH lookup instead of the real
// machine's home and $PATH. A nil lookPath defaults to exec.LookPath.
// A tool's own env override ($CLAUDE_CONFIG_DIR, $CODEX_HOME) is
// still read from the real process environment via os.Getenv — the
// same choice internal/importer's own claudeRoot/codexRoot make — so
// a test that wants a fixed fixture root must clear it with
// t.Setenv.
//
// Devin is not in the returned set: it is hosted, with no local
// directory to detect (source map §6).
func detectTools(home string, lookPath func(string) (string, error)) []DetectedTool {
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	out := make([]DetectedTool, 0, len(toolSpecs))
	for _, spec := range toolSpecs {
		root := spec.defaultRoot(home)
		if spec.envOverride != "" {
			if dir := os.Getenv(spec.envOverride); dir != "" {
				root = dir
			}
		}

		present := false
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			present = true
		}
		if !present {
			for _, bin := range spec.bins {
				if _, err := lookPath(bin); err == nil {
					present = true
					break
				}
			}
		}
		if !present && spec.extraPresent != nil && spec.extraPresent(home) {
			present = true
		}

		var memoryFile string
		if spec.memoryFile != nil {
			memoryFile = spec.memoryFile(root)
		}

		out = append(out, DetectedTool{
			Name:       spec.name,
			Present:    present,
			Root:       root,
			SkillsDir:  spec.skillsDir(root),
			MemoryFile: memoryFile,
		})
	}
	return out
}
