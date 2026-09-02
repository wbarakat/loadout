package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearToolEnvOverrides clears every tool env override detectTools
// reads, so a test starts from a clean slate regardless of what the
// real machine running the test happens to have set. A test that
// wants to exercise an override sets it back with its own t.Setenv
// call after this.
func clearToolEnvOverrides(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
}

// noBinsOnPath is a lookPath stub reporting every binary as not
// found, the same shape exec.LookPath returns on a real miss.
func noBinsOnPath(string) (string, error) {
	return "", errors.New("not found")
}

// onlyOnPath returns a lookPath stub reporting only the named
// binaries as found, at a fake path built from the binary's own
// name.
func onlyOnPath(names ...string) func(string) (string, error) {
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	return func(name string) (string, error) {
		if found[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

// byName indexes a detectTools result by its Name field, for a test
// to look up one tool's result without depending on slice order.
func byName(got []DetectedTool) map[string]DetectedTool {
	m := make(map[string]DetectedTool, len(got))
	for _, d := range got {
		m[d.Name] = d
	}
	return m
}

// allToolNames is every tool detectTools must report, in the stable
// order it must report them.
var allToolNames = []string{"claude-code", "codex", "cursor", "hermes", "pi", "gemini", "droid"}

func TestDetectToolsStableOrderAndCount(t *testing.T) {
	clearToolEnvOverrides(t)
	home := t.TempDir()

	got := detectTools(home, noBinsOnPath)
	if len(got) != len(allToolNames) {
		t.Fatalf("got %d tools, want %d: %+v", len(got), len(allToolNames), got)
	}
	for i, want := range allToolNames {
		if got[i].Name != want {
			t.Fatalf("tool %d: got %q, want %q (order must be stable)", i, got[i].Name, want)
		}
	}
}

// TestDetectToolsDirsPresent proves the primary signal: a tool whose
// default root directory exists is Present, with the right resolved
// Root/SkillsDir/MemoryFile, while a tool with neither its dir nor
// its binary present is not. Devin is never in the returned set at
// all — it is hosted, with no local directory to detect.
func TestDetectToolsDirsPresent(t *testing.T) {
	clearToolEnvOverrides(t)
	home := t.TempDir()
	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustMkdirAll(t, filepath.Join(home, ".codex"))

	got := byName(detectTools(home, noBinsOnPath))

	claude := got["claude-code"]
	if !claude.Present {
		t.Fatalf("claude-code: want Present, got %+v", claude)
	}
	if claude.Root != filepath.Join(home, ".claude") {
		t.Fatalf("claude-code Root = %q", claude.Root)
	}
	if claude.SkillsDir != filepath.Join(home, ".claude", "skills") {
		t.Fatalf("claude-code SkillsDir = %q", claude.SkillsDir)
	}
	if claude.MemoryFile != filepath.Join(home, ".claude", "CLAUDE.md") {
		t.Fatalf("claude-code MemoryFile = %q", claude.MemoryFile)
	}

	codex := got["codex"]
	if !codex.Present {
		t.Fatalf("codex: want Present, got %+v", codex)
	}
	if codex.Root != filepath.Join(home, ".codex") {
		t.Fatalf("codex Root = %q", codex.Root)
	}
	if codex.SkillsDir != filepath.Join(home, ".codex", "skills") {
		t.Fatalf("codex SkillsDir = %q", codex.SkillsDir)
	}
	if codex.MemoryFile != filepath.Join(home, ".codex", "AGENTS.md") {
		t.Fatalf("codex MemoryFile = %q", codex.MemoryFile)
	}

	for _, name := range []string{"cursor", "hermes", "pi", "gemini", "droid"} {
		d := got[name]
		if d.Present {
			t.Fatalf("%s: want not Present, got %+v", name, d)
		}
	}

	// Every tool's default paths are always reported, present or not
	// — the installer needs to know where a tool WOULD land even
	// before it decides to enable it.
	pi := got["pi"]
	if pi.Root != filepath.Join(home, ".pi", "agent") {
		t.Fatalf("pi Root = %q", pi.Root)
	}
	if pi.SkillsDir != filepath.Join(home, ".pi", "agent", "skills") {
		t.Fatalf("pi SkillsDir = %q", pi.SkillsDir)
	}
	if pi.MemoryFile != filepath.Join(home, ".pi", "agent", "AGENTS.md") {
		t.Fatalf("pi MemoryFile = %q", pi.MemoryFile)
	}

	gemini := got["gemini"]
	if gemini.MemoryFile != filepath.Join(home, ".gemini", "GEMINI.md") {
		t.Fatalf("gemini MemoryFile = %q", gemini.MemoryFile)
	}

	droid := got["droid"]
	if droid.Root != filepath.Join(home, ".factory") {
		t.Fatalf("droid Root = %q", droid.Root)
	}
	if droid.MemoryFile != filepath.Join(home, ".factory", "AGENTS.md") {
		t.Fatalf("droid MemoryFile = %q", droid.MemoryFile)
	}

	// cursor and hermes have no standalone memory file loadout
	// targets.
	if got["cursor"].MemoryFile != "" {
		t.Fatalf("cursor MemoryFile = %q, want empty", got["cursor"].MemoryFile)
	}
	if got["hermes"].MemoryFile != "" {
		t.Fatalf("hermes MemoryFile = %q, want empty", got["hermes"].MemoryFile)
	}
	if got["cursor"].SkillsDir != filepath.Join(home, ".cursor", "skills") {
		t.Fatalf("cursor SkillsDir = %q", got["cursor"].SkillsDir)
	}
	if got["hermes"].SkillsDir != filepath.Join(home, ".hermes", "skills") {
		t.Fatalf("hermes SkillsDir = %q", got["hermes"].SkillsDir)
	}
}

// TestDetectToolsCodexHomeOverride proves $CODEX_HOME is checked
// before the default ~/.codex path, and wins when it points at a
// real directory.
func TestDetectToolsCodexHomeOverride(t *testing.T) {
	clearToolEnvOverrides(t)
	home := t.TempDir()
	// A ~/.codex default dir exists too, so this proves the override
	// wins rather than merely filling in for a missing default.
	mustMkdirAll(t, filepath.Join(home, ".codex"))

	customHome := t.TempDir()
	mustMkdirAll(t, customHome)
	t.Setenv("CODEX_HOME", customHome)

	got := byName(detectTools(home, noBinsOnPath))
	codex := got["codex"]
	if !codex.Present {
		t.Fatalf("codex: want Present, got %+v", codex)
	}
	if codex.Root != customHome {
		t.Fatalf("codex Root = %q, want the CODEX_HOME override %q, not the default ~/.codex", codex.Root, customHome)
	}
	if codex.SkillsDir != filepath.Join(customHome, "skills") {
		t.Fatalf("codex SkillsDir = %q", codex.SkillsDir)
	}
	if codex.MemoryFile != filepath.Join(customHome, "AGENTS.md") {
		t.Fatalf("codex MemoryFile = %q", codex.MemoryFile)
	}
}

// TestDetectToolsBinaryOnPathIsPresenceSignal proves a tool absent
// from disk, but whose binary lookPath finds on PATH, is still
// Present — the documented fallback signal (source map §7).
func TestDetectToolsBinaryOnPathIsPresenceSignal(t *testing.T) {
	clearToolEnvOverrides(t)
	home := t.TempDir() // no tool directories at all

	got := byName(detectTools(home, onlyOnPath("gemini")))

	gemini := got["gemini"]
	if !gemini.Present {
		t.Fatalf("gemini: want Present via PATH, got %+v", gemini)
	}
	// Root/SkillsDir/MemoryFile still resolve to the default layout —
	// a binary-only detection has no other root to offer.
	if gemini.Root != filepath.Join(home, ".gemini") {
		t.Fatalf("gemini Root = %q", gemini.Root)
	}

	for _, name := range []string{"claude-code", "codex", "cursor", "hermes", "pi", "droid"} {
		if d := got[name]; d.Present {
			t.Fatalf("%s: want not Present, got %+v", name, d)
		}
	}
}

// TestDetectToolsCursorAgentBinary proves cursor also counts the
// "cursor-agent" binary name, not just "cursor".
func TestDetectToolsCursorAgentBinary(t *testing.T) {
	clearToolEnvOverrides(t)
	home := t.TempDir()

	got := byName(detectTools(home, onlyOnPath("cursor-agent")))
	if !got["cursor"].Present {
		t.Fatalf("cursor: want Present via cursor-agent on PATH, got %+v", got["cursor"])
	}
}

// TestDetectToolsCursorAppDataDir proves cursor also counts its
// OS-native Electron app-data directory as a presence signal, even
// with no ~/.cursor dir and no binary on PATH.
func TestDetectToolsCursorAppDataDir(t *testing.T) {
	clearToolEnvOverrides(t)
	home := t.TempDir()
	mustMkdirAll(t, cursorAppDataDir(home))

	got := byName(detectTools(home, noBinsOnPath))
	cursor := got["cursor"]
	if !cursor.Present {
		t.Fatalf("cursor: want Present via app-data dir, got %+v", cursor)
	}
	// The app-data dir is a presence signal only — it must not change
	// cursor's reported Root/SkillsDir away from the CLI-managed
	// ~/.cursor default.
	if cursor.Root != filepath.Join(home, ".cursor") {
		t.Fatalf("cursor Root = %q, want the default ~/.cursor even though only app-data was found", cursor.Root)
	}
}

// TestDetectToolsNeverReadsCredentialFile proves detection stays to
// directory presence and PATH lookups only: a sentinel credential
// file sitting right where a real Codex install keeps auth.json is
// never opened, so its content can never leak into a DetectedTool
// field. This is the regression guard for source map §2's "never
// glob-read a tool's whole home directory" risk.
func TestDetectToolsNeverReadsCredentialFile(t *testing.T) {
	clearToolEnvOverrides(t)
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	mustMkdirAll(t, codexDir)

	sentinel := "sk-super-secret-credential-do-not-leak-9f31c2"
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(sentinel), 0o600); err != nil {
		t.Fatalf("could not write sentinel fixture file: %v", err)
	}

	got := detectTools(home, noBinsOnPath)
	for _, d := range got {
		for _, field := range []string{d.Name, d.Root, d.SkillsDir, d.MemoryFile} {
			if strings.Contains(field, sentinel) {
				t.Fatalf("detectTools leaked the credential sentinel into a field: %+v", d)
			}
		}
	}

	// codex itself must still detect correctly alongside the
	// sentinel file sitting right next to AGENTS.md — proving this is
	// a real Detect-then-ignore, not an accidental skip of the whole
	// directory.
	codex := byName(got)["codex"]
	if !codex.Present {
		t.Fatalf("codex: want Present, got %+v", codex)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("could not create fixture dir %s: %v", dir, err)
	}
}
