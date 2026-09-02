package importer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Cursor is the import Source for Cursor's own on-disk store: skills
// at ~/.cursor/skills (global) and <project>/.cursor/skills
// (project), plus project-scoped memory only — Cursor's global "User
// Rules" live in an undocumented internal database and are never
// imported (source map §3).
//
// SECRET SAFETY: this source reads only the named SKILL.md files a
// skill scan finds, plus <project>/.cursor/rules/*.mdc and
// <project>/.cursorrules. It never opens, reads, or globs anything
// under the Cursor Electron app's app-data directory (macOS
// ~/Library/Application Support/Cursor, Linux ~/.config/Cursor) — that
// tree holds an undocumented state.vscdb SQLite database (source map
// §3) this source only ever os.Stats, for Detect, never reads. It
// never globs ~/.cursor broadly either: Skills names its two scan
// directories explicitly, so a leftover ~/.cursor/agents or
// ~/.cursor/hooks.json is never touched.
type Cursor struct{}

func (Cursor) Name() string { return "cursor" }

// cursorRoot resolves Cursor's CLI-managed root directory: ctx.Home +
// "/.cursor". Cursor has no documented relocation environment
// variable (source map §7).
func cursorRoot(ctx ImportCtx) string {
	return filepath.Join(ctx.Home, ".cursor")
}

// cursorAppDataDir resolves the OS-native path to the Cursor Electron
// app's own app-data tree — the other half of Cursor's on-disk
// footprint (source map §3), used only by Detect. This source never
// reads a file inside this directory.
func cursorAppDataDir(ctx ImportCtx) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(ctx.Home, "Library", "Application Support", "Cursor")
	}
	return filepath.Join(ctx.Home, ".config", "Cursor")
}

// Detect reports Cursor present when either half of its on-disk
// footprint exists: the CLI-managed ~/.cursor tree, checked first, or
// the Electron app's OS-native app-data directory. Both checks are a
// plain os.Stat — presence alone is enough; a cursor/cursor-agent
// binary on PATH is a documented fallback signal (source map §7) not
// implemented here, since directory presence already covers every
// real install this importer needs to detect.
func (Cursor) Detect(ctx ImportCtx) (bool, string) {
	root := cursorRoot(ctx)
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return true, root
	}
	appData := cursorAppDataDir(ctx)
	if info, err := os.Stat(appData); err == nil && info.IsDir() {
		return true, appData
	}
	return false, ""
}

// userRulesWarning is Cursor's one, always-on informational warning:
// global "User Rules" live in Cursor's internal, undocumented
// state.vscdb database, with no stable schema or official API to read
// them by (source map §3) — this source never opens that file, so
// there is no way to import them. It is emitted from BOTH Skills and
// Memory whenever Cursor is detected, so a skills-only run, a
// memory-only run, and a run doing both each see it. A run that does
// both would otherwise see it twice; engine.go's RunImport collapses
// that back down to one entry (see dedupWarnings), so this source does
// not need to track which of its own methods already emitted it.
func userRulesWarning() Warning {
	return Warning{
		Tool:   "cursor",
		Reason: `Cursor global "User Rules" cannot be imported — Cursor keeps them in an internal database with no stable format. Fix: copy them from Cursor Settings → Rules if you want them in Loadout.`,
	}
}

// Skills scans ~/.cursor/skills (global) and, when ctx.ProjectDir is
// set, <project>/.cursor/skills, via the shared generic scanner —
// scanAgentsSkills, which keys off SKILL.md FILE presence, never a
// directory-name pattern. This is what lets a stale
// ~/.cursor/skills-cursor leftover directory, with no SKILL.md inside
// any of its subfolders, sit untouched: it is simply not one of the
// two directories named below, and even if it were, an entry inside
// it with no SKILL.md would still be skipped by scanSkillEntry, not
// specially recognized by its directory name (source map §3).
//
// Also always emits the one User-Rules warning when Cursor is
// present — see userRulesWarning.
func (Cursor) Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error) {
	dirs := []string{filepath.Join(cursorRoot(ctx), "skills")}
	if ctx.ProjectDir != "" {
		dirs = append(dirs, filepath.Join(ctx.ProjectDir, ".cursor", "skills"))
	}

	skills, warnings := scanAgentsSkills(dirs, "cursor", ctx)

	if present, _ := (Cursor{}).Detect(ctx); present {
		warnings = append(warnings, userRulesWarning())
	}

	return skills, warnings, nil
}

// Memory returns candidate facts from Cursor's project-scoped memory
// ONLY, and only when ctx.ProjectMemory is set and ctx.ProjectDir is
// not empty: Cursor has no importable GLOBAL memory at all. By
// default (ctx.ProjectMemory false), Cursor imports no memory —
// unlike every other source in this package, there is no
// global-instructions fallback to import instead, so there is nothing
// to report as "skipped, pass --project-memory" either.
//
// It still always emits the one User-Rules warning when Cursor is
// detected — see userRulesWarning — even on this early-return path,
// since a caller running a memory-only import (RunImport still calls
// Memory whenever opt.Memory is set, no matter ctx.ProjectMemory) must
// see the warning too, not just a skills-only or a both run.
//
// Two native stores are read, both under ctx.ProjectDir:
//   - .cursor/rules/*.mdc — see scanCursorRules.
//   - .cursorrules — see scanCursorrulesPath, which branches on
//     os.Lstat's IsDir: a real-world non-standard reuse of the legacy
//     name as a directory (source map §3) imports each file inside as
//     its own fact.
func (Cursor) Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error) {
	var warnings []Warning
	if present, _ := (Cursor{}).Detect(ctx); present {
		warnings = append(warnings, userRulesWarning())
	}

	if !ctx.ProjectMemory || ctx.ProjectDir == "" {
		return nil, warnings, nil
	}

	facts, ruleWarnings := scanCursorRules(ctx.ProjectDir)
	warnings = append(warnings, ruleWarnings...)

	crFacts, crWarnings := scanCursorrulesPath(ctx.ProjectDir)
	facts = append(facts, crFacts...)
	warnings = append(warnings, crWarnings...)

	return facts, warnings, nil
}

// parseCursorRuleFrontmatter parses one .mdc file's YAML-ish
// frontmatter LENIENTLY: a plain "key: value" line per field, the
// same forgiving shape parseFrontmatter (claudecode.go) already
// applies to a SKILL.md file. ok is false only when the file OPENS a
// "---" frontmatter fence but never CLOSES it — a genuinely malformed
// block the caller cannot safely guess the end of — so the caller
// skips the file and warns rather than risk swallowing half the rule
// body as if it were frontmatter. A file with no frontmatter fence at
// all is not malformed: every .mdc field is optional in Cursor's own
// docs, so the whole file is simply treated as the rule body with no
// fields.
func parseCursorRuleFrontmatter(raw []byte) (fields map[string]string, body string, ok bool) {
	text := normalizeText(string(raw))
	if !strings.HasPrefix(text, "---\n") {
		return map[string]string{}, strings.TrimSpace(text), true
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", false
	}
	fields = map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, cut := strings.Cut(line, ":")
		if !cut {
			continue
		}
		fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	body = strings.TrimSpace(strings.TrimPrefix(rest[end+len("\n---"):], "\n"))
	return fields, body, true
}

// scanCursorRules reads every *.mdc file directly under
// <projectDir>/.cursor/rules — a missing directory is not a problem,
// most projects have none. Each file becomes one candidate fact: name
// is the file's own kebab base (falling back to the description, then
// to a fixed "cursor-rule", if the filename itself kebabifies to
// nothing usable); type is "project" when globs is set (globs wins
// even over alwaysApply, since an explicit glob scope is the more
// specific rule), "user" when alwaysApply is "true" with no globs,
// and "project" as the default otherwise — Loadout has no glob
// concept, so a globbed rule's pattern is appended to the body as
// PLAIN TEXT rather than dropped. A file whose frontmatter fence
// never closes is skipped with a warning, never aborting the rest of
// the directory's scan.
func scanCursorRules(projectDir string) ([]CandidateFact, []Warning) {
	dir := filepath.Join(projectDir, ".cursor", "rules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	var facts []CandidateFact
	var warnings []Warning
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mdc") {
			continue
		}
		path := filepath.Join(dir, e.Name())

		raw, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, Warning{Tool: "cursor", Path: path, Reason: "the file could not be read: " + err.Error()})
			continue
		}
		fields, body, ok := parseCursorRuleFrontmatter(raw)
		if !ok {
			warnings = append(warnings, Warning{
				Tool:   "cursor",
				Path:   path,
				Reason: "no valid frontmatter: the --- block is never closed. Fix: close the --- block, or remove it.",
			})
			continue
		}

		globs := strings.TrimSpace(fields["globs"])
		alwaysApply := strings.TrimSpace(fields["alwaysApply"]) == "true"

		factType := "project"
		if alwaysApply {
			factType = "user"
		}
		if globs != "" {
			factType = "project"
			body = strings.TrimSpace(strings.TrimSpace(body) + "\n\nGlob: " + globs)
		}

		description := fields["description"]
		if body == "" {
			if description == "" {
				// Nothing to import: no body, no description.
				continue
			}
			body = description
		}

		name := kebabify(strings.TrimSuffix(e.Name(), ".mdc"))
		if name == "" {
			name = kebabify(description)
		}
		if name == "" {
			name = "cursor-rule"
		}
		if description == "" {
			description = firstLine(body)
		}
		if description == "" {
			description = name
		}

		modTime := time.Time{}
		if info, err := e.Info(); err == nil {
			modTime = info.ModTime()
		}

		facts = append(facts, CandidateFact{
			Name:        name,
			Description: description,
			Type:        factType,
			Body:        body,
			Tool:        "cursor",
			ModTime:     modTime,
		})
	}
	return facts, warnings
}

// scanCursorrulesPath reads <projectDir>/.cursorrules. A missing path
// is not a problem. Per source map §3's confirmed real-world gotcha —
// a project on this machine reuses the legacy ".cursorrules" name as
// a DIRECTORY of loose docs, not a file — this branches on
// os.Lstat's own IsDir: a FILE imports as one fact; a DIRECTORY
// imports each regular file directly inside it as its own fact
// (non-recursive: a nested subdirectory is left alone rather than
// guessed at).
func scanCursorrulesPath(projectDir string) ([]CandidateFact, []Warning) {
	path := filepath.Join(projectDir, ".cursorrules")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil
	}

	if info.IsDir() {
		return scanCursorrulesDir(path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, []Warning{{Tool: "cursor", Path: path, Reason: "the file could not be read: " + err.Error()}}
	}
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return nil, nil
	}
	return []CandidateFact{{
		Name:        "cursorrules",
		Description: firstLine(body),
		Type:        "project",
		Body:        body,
		Tool:        "cursor",
		ModTime:     info.ModTime(),
	}}, nil
}

// scanCursorrulesDir reads every regular file directly inside a
// .cursorrules directory, turning each into its own candidate fact,
// named "cursorrules-<file's own kebab base>" so it never collides
// with the plain-file shape's own "cursorrules" name. A subdirectory
// inside .cursorrules is skipped, not descended into.
func scanCursorrulesDir(dir string) ([]CandidateFact, []Warning) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []Warning{{Tool: "cursor", Path: dir, Reason: "the directory could not be read: " + err.Error()}}
	}

	var facts []CandidateFact
	var warnings []Warning
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, Warning{Tool: "cursor", Path: path, Reason: "the file could not be read: " + err.Error()})
			continue
		}
		body := strings.TrimSpace(string(raw))
		if body == "" {
			continue
		}
		modTime := time.Time{}
		if fi, err := e.Info(); err == nil {
			modTime = fi.ModTime()
		}
		base := kebabify(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		if base == "" {
			base = "file"
		}
		facts = append(facts, CandidateFact{
			Name:        "cursorrules-" + base,
			Description: firstLine(body),
			Type:        "project",
			Body:        body,
			Tool:        "cursor",
			ModTime:     modTime,
		})
	}
	return facts, warnings
}
