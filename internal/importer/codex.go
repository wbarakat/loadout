package importer

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Codex is the import Source for OpenAI Codex CLI's own on-disk
// store: the generic .agents/skills scopes, Codex's own
// .codex/skills user skills, and the AGENTS.md memory chain.
//
// SECRET SAFETY (source map §2): a Codex home directory holds a live
// auth.json and a config.toml that can carry secrets inline (an MCP
// server's env block, for example) right next to AGENTS.md. This
// source must NEVER open, read, or stat-for-content any file under
// the codex root except AGENTS.md, AGENTS.override.md, and the
// SKILL.md files under .codex/skills (outside .system) and
// .agents/skills. It never globs the codex root broadly — every scan
// below names its target directory explicitly.
type Codex struct{}

func (Codex) Name() string { return "codex" }

// codexRoot resolves Codex's root directory: $CODEX_HOME when set,
// else ctx.Home + "/.codex". Detect, Skills, and Memory all call
// this, so every method agrees on the same root. A test that wants a
// fixed fixture root sets ctx.Home and clears CODEX_HOME with
// t.Setenv("CODEX_HOME", "") — this keeps the override real (an
// installer must honor it) without letting the ambient environment
// leak into a test.
func codexRoot(ctx ImportCtx) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(ctx.Home, ".codex")
}

func (Codex) Detect(ctx ImportCtx) (bool, string) {
	root := codexRoot(ctx)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, ""
	}
	return true, root
}

// codexSystemMarker names the sentinel file whose presence in
// <root>/skills/.system means the whole .system subtree is Codex's
// own bundled, vendor-provided skills, not user content — the
// tool-provided vendor-exclusion signal from source map §2. Its
// absence means .system is left alone and scanned like any other
// name: this source never name-matches individual skill names to
// decide what is vendor content.
const codexSystemMarker = ".codex-system-skills.marker"

// Skills scans the generic .agents/skills scopes — project, the
// project's repo root, and global — via scanAgentsSkills, plus
// Codex's own <root>/skills/*/SKILL.md user skills. It excludes the
// entire <root>/skills/.system subtree whenever
// <root>/skills/.system/.codex-system-skills.marker is present. It
// never opens any other file under the codex root.
func (Codex) Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error) {
	root := codexRoot(ctx)

	var dirs []string
	if ctx.ProjectDir != "" {
		dirs = append(dirs, filepath.Join(ctx.ProjectDir, ".agents", "skills"))
		if gitRoot := findGitRoot(ctx.ProjectDir); gitRoot != "" && gitRoot != ctx.ProjectDir {
			dirs = append(dirs, filepath.Join(gitRoot, ".agents", "skills"))
		}
	}
	dirs = append(dirs, filepath.Join(ctx.Home, ".agents", "skills"))

	skills, warnings := scanAgentsSkills(dirs, "codex", ctx)

	codexSkills, codexWarnings := scanCodexUserSkills(root, ctx.VaultSkillsDir)
	skills = append(skills, codexSkills...)
	warnings = append(warnings, codexWarnings...)

	return skills, warnings, nil
}

// scanCodexUserSkills reads <root>/skills, one directory listing, and
// treats each direct subdirectory as one candidate skill folder —
// the same shape scanAgentsSkills uses, reused per-entry via
// scanSkillEntry. It excludes .system whenever its marker file is
// present, and otherwise never opens a file under root beyond a
// candidate skill's own SKILL.md.
func scanCodexUserSkills(root, vaultSkillsDir string) ([]CandidateSkill, []Warning) {
	skillsDir := filepath.Join(root, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, nil
	}

	excludeSystem := false
	if _, err := os.Stat(filepath.Join(skillsDir, ".system", codexSystemMarker)); err == nil {
		excludeSystem = true
	}

	var skills []CandidateSkill
	var warnings []Warning
	for _, e := range entries {
		if excludeSystem && e.Name() == ".system" {
			continue
		}
		s, w := scanSkillEntry(filepath.Join(skillsDir, e.Name()), "codex", vaultSkillsDir)
		if w != nil {
			warnings = append(warnings, *w)
			continue
		}
		if s != nil {
			skills = append(skills, *s)
		}
	}
	return skills, warnings
}

// findGitRoot walks up from dir looking for a directory holding a
// .git entry, returning "" if none is found before reaching the
// filesystem root. Skills and Memory use it to find a project's own
// repo root, which may sit above ctx.ProjectDir.
func findGitRoot(dir string) string {
	if dir == "" {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// projectAgentsDirs lists the directories to check for an AGENTS.md
// file for one project: from the project's repo root (found by
// walking up from projectDir) down to projectDir itself. When
// projectDir sits outside any git repo, the chain is just projectDir
// alone.
func projectAgentsDirs(projectDir string) []string {
	if projectDir == "" {
		return nil
	}
	gitRoot := findGitRoot(projectDir)
	if gitRoot == "" {
		return []string{projectDir}
	}
	rel, err := filepath.Rel(gitRoot, projectDir)
	if err != nil || rel == "." {
		return []string{gitRoot}
	}
	dirs := []string{gitRoot}
	cur := gitRoot
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		dirs = append(dirs, cur)
	}
	return dirs
}

// codexAgentsFileIn reports the one AGENTS file dir holds, per
// Codex's own precedence: AGENTS.override.md if present, else
// AGENTS.md. Only one file per directory counts — the same rule
// Codex's own project_doc discovery applies.
func codexAgentsFileIn(dir string) (path string, ok bool) {
	override := filepath.Join(dir, "AGENTS.override.md")
	if info, err := os.Stat(override); err == nil && !info.IsDir() {
		return override, true
	}
	plain := filepath.Join(dir, "AGENTS.md")
	if info, err := os.Stat(plain); err == nil && !info.IsDir() {
		return plain, true
	}
	return "", false
}

// Memory returns candidate facts from the AGENTS.md chain: the
// global file at the codex root, plus — when ctx.ProjectDir is set —
// one file per directory from the project's repo root down to
// ProjectDir. Codex's own AGENTS.md holds the FULL rendered memory
// inside Loadout's marks (memoryBlock mode, source map §2) — this
// strips that block first, and imports only what is left outside it.
func (Codex) Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error) {
	root := codexRoot(ctx)

	var facts []CandidateFact
	var warnings []Warning

	if path, ok := codexAgentsFileIn(root); ok {
		f, w := readCodexAgentsFile(path, "agents-md", "user")
		facts = append(facts, f...)
		warnings = append(warnings, w...)
	}

	if ctx.ProjectDir != "" {
		dirs := projectAgentsDirs(ctx.ProjectDir)
		top := dirs[0]
		for _, dir := range dirs {
			path, ok := codexAgentsFileIn(dir)
			if !ok {
				continue
			}
			base := "agents-md-project"
			if dir != top {
				if rel, err := filepath.Rel(top, dir); err == nil {
					base = "agents-md-project-" + kebabify(rel)
				}
			}
			f, w := readCodexAgentsFile(path, base, "project")
			facts = append(facts, f...)
			warnings = append(warnings, w...)
		}
	}

	return facts, warnings, nil
}

// readCodexAgentsFile reads one AGENTS.md-shaped file, strips
// Loadout's own managed block, and splits what is left into one fact
// per top-level "##" section (or one fact for the whole file when it
// has no such heading) — the same split readClaudeMDFile applies. A
// damaged block skips the file with a warning; a body left empty
// after stripping (Loadout's own projection, nothing native left to
// recover) skips it without one.
func readCodexAgentsFile(path, base, factType string) ([]CandidateFact, []Warning) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, []Warning{{Tool: "codex", Path: path, Reason: "the file could not be read: " + err.Error()}}
	}

	native, damaged := StripLoadoutBlock(string(raw))
	if damaged {
		return nil, []Warning{{
			Tool:   "codex",
			Path:   path,
			Reason: "the loadout marks in this file are damaged. Fix: repair or remove the marks at the source.",
		}}
	}
	native = strings.TrimSpace(native)
	if native == "" {
		return nil, nil
	}

	var modTime time.Time
	if info, err := os.Stat(path); err == nil {
		modTime = info.ModTime()
	}

	sections, structured := splitTopSections(native)
	var facts []CandidateFact
	for _, sec := range sections {
		body := strings.TrimSpace(sec.body)
		if body == "" {
			continue
		}
		name := base
		description := firstLine(body)
		if structured {
			suffix := "intro"
			if sec.heading != "" {
				suffix = kebabify(sec.heading)
			}
			if suffix != "" {
				name = base + "-" + suffix
			}
			if description == "" {
				description = sec.heading
			}
		}
		if description == "" {
			description = name
		}
		facts = append(facts, CandidateFact{
			Name:        name,
			Description: description,
			Type:        factType,
			Body:        body,
			Tool:        "codex",
			ModTime:     modTime,
		})
	}
	return facts, nil
}
