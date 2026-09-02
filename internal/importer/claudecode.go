package importer

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// maxMemoryFileSize is the same 4MiB limit Claude Code itself applies
// to a CLAUDE.md file. A larger file is skipped with a warning rather
// than read and silently truncated differently than Claude Code would
// have truncated it.
const maxMemoryFileSize = 4 * 1024 * 1024

// ClaudeCode is the import Source for Claude Code's own on-disk
// store: personal and project skills under skills/, the CLAUDE.md
// memory hierarchy, and the separate auto-memory vault Claude Code
// itself writes under projects/*/memory.
type ClaudeCode struct{}

func (ClaudeCode) Name() string { return "claude-code" }

// claudeRoot resolves Claude Code's root directory: $CLAUDE_CONFIG_DIR
// when set, else ctx.Home + "/.claude". Detect, Skills, and Memory all
// call this, so every method agrees on the same root. A test that
// wants a fixed fixture root sets ctx.Home and clears
// CLAUDE_CONFIG_DIR with t.Setenv("CLAUDE_CONFIG_DIR", "") — this
// keeps the override real (an installer must honor it) without
// letting the ambient environment leak into a test.
func claudeRoot(ctx ImportCtx) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(ctx.Home, ".claude")
}

func (ClaudeCode) Detect(ctx ImportCtx) (bool, string) {
	root := claudeRoot(ctx)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, ""
	}
	return true, root
}

// Skills enumerates personal skills at <root>/skills/*/SKILL.md and,
// when ctx.ProjectDir is set, project skills at
// ctx.ProjectDir/.claude/skills/*/SKILL.md. It excludes Loadout's own
// projected skills and any skill resolving into <root>/plugins/
// (vendor/plugin content), and turns a single unreadable or dangling
// entry into a Warning instead of aborting the scan.
func (ClaudeCode) Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error) {
	root := claudeRoot(ctx)
	pluginsDir := filepath.Join(root, "plugins")
	if resolved, err := filepath.EvalSymlinks(pluginsDir); err == nil {
		pluginsDir = resolved
	}

	var skills []CandidateSkill
	var warnings []Warning

	add := func(dir string) {
		s, w := scanSkillsDir(dir, ctx.VaultSkillsDir, pluginsDir)
		skills = append(skills, s...)
		warnings = append(warnings, w...)
	}

	add(filepath.Join(root, "skills"))
	if ctx.ProjectDir != "" {
		add(filepath.Join(ctx.ProjectDir, ".claude", "skills"))
	}
	return skills, warnings, nil
}

// scanSkillsDir reads every entry directly under dir as one candidate
// skill folder. A missing dir is not a problem — most installs have no
// skills at all — and produces neither a skill nor a warning.
func scanSkillsDir(dir, vaultSkillsDir, pluginsDir string) ([]CandidateSkill, []Warning) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	var skills []CandidateSkill
	var warnings []Warning
	for _, e := range entries {
		entryPath := filepath.Join(dir, e.Name())

		owned, err := IsVaultOwnedSkill(entryPath, vaultSkillsDir)
		if err != nil {
			warnings = append(warnings, danglingSkillWarning(entryPath))
			continue
		}
		if owned {
			// Loadout's own projected skill — exclude silently, this is
			// expected, not a problem.
			continue
		}

		// Resolve the skill folder to its REAL path before reading
		// anything from it. A skill folder is often itself a symlink
		// (~/.claude/skills/foo -> ~/.agents/skills/foo, the common
		// cross-tool shape) — reading SKILL.md and walking for support
		// files through the resolved directory, rather than the
		// symlink itself, is what lets a symlinked skill folder's
		// support files be found at all (a plain filepath.WalkDir on
		// a symlink root does not descend into it).
		realPath, err := filepath.EvalSymlinks(entryPath)
		if err != nil {
			warnings = append(warnings, danglingSkillWarning(entryPath))
			continue
		}
		if pluginsDir != "" && isWithinDir(pluginsDir, realPath) {
			// A vendor/plugin skill, not user-authored — exclude
			// silently.
			continue
		}

		info, err := os.Stat(realPath)
		if err != nil || !info.IsDir() {
			warnings = append(warnings, Warning{
				Tool:   "claude-code",
				Path:   entryPath,
				Reason: "this is not a skill folder. Fix: a skill must be a directory holding a SKILL.md file.",
			})
			continue
		}

		skillMDPath := filepath.Join(realPath, "SKILL.md")
		raw, err := os.ReadFile(skillMDPath)
		if err != nil {
			warnings = append(warnings, Warning{
				Tool:   "claude-code",
				Path:   entryPath,
				Reason: "no readable SKILL.md file. Fix: add a SKILL.md file, or remove the folder.",
			})
			continue
		}
		name, description, body, ok := parseSkillFrontmatter(raw)
		if !ok {
			warnings = append(warnings, Warning{
				Tool:   "claude-code",
				Path:   skillMDPath,
				Reason: "no valid frontmatter. Fix: add a --- block with a name field.",
			})
			continue
		}

		modTime := info.ModTime()
		if st, err := os.Stat(skillMDPath); err == nil {
			modTime = st.ModTime()
		}

		files, fileWarnings := collectSkillFiles(realPath, "claude-code")
		warnings = append(warnings, fileWarnings...)

		skills = append(skills, CandidateSkill{
			Name:        name,
			Description: description,
			Body:        body,
			Files:       files,
			Tool:        "claude-code",
			ModTime:     modTime,
		})
	}
	return skills, warnings
}

func danglingSkillWarning(entryPath string) Warning {
	return Warning{
		Tool:   "claude-code",
		Path:   entryPath,
		Reason: "this skill link is dangling and does not resolve. Fix: remove the link, or point it at a real skill.",
	}
}

// collectSkillFiles reads every file under dir except SKILL.md itself,
// keyed by its path relative to dir, so a skill's supporting files
// (references, scripts, nested folders) copy into the vault alongside
// SKILL.md. dir must already be the skill folder's REAL path (every
// symlink in it resolved) — the caller is responsible for that, since
// it also needs the real path to read SKILL.md itself.
//
// SECRET SAFETY: a support file that is itself a symlink is only
// collected when its own real target stays inside dir. A symlink
// pointing outside the skill folder — a credential file elsewhere on
// disk, for example — is skipped and warned about, never read or
// copied. A file that fails to read is dropped without a warning,
// same as before; a dangling or escaping support-file link is not.
func collectSkillFiles(dir, tool string) (map[string][]byte, []Warning) {
	files := map[string][]byte{}
	var warnings []Warning
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil || rel == "SKILL.md" {
			return nil
		}
		realPath, evalErr := filepath.EvalSymlinks(path)
		if evalErr != nil {
			warnings = append(warnings, Warning{
				Tool:   tool,
				Path:   path,
				Reason: "this support file link is dangling and does not resolve. Fix: remove the link, or point it at a real file.",
			})
			return nil
		}
		if !isWithinDir(dir, realPath) {
			warnings = append(warnings, Warning{
				Tool:   tool,
				Path:   path,
				Reason: "this support file link points outside the skill folder. Fix: keep support files inside the skill folder.",
			})
			return nil
		}
		data, readErr := os.ReadFile(realPath)
		if readErr != nil {
			return nil
		}
		files[rel] = data
		return nil
	})
	return files, warnings
}

// parseFrontmatter splits simple "key: value" YAML-ish frontmatter
// from the body, the same shape vault's own scaffold files use. It
// strips a leading byte order mark and normalizes CRLF line endings,
// so a file saved from another editor still parses.
func parseFrontmatter(raw []byte) (map[string]string, string) {
	text := normalizeText(string(raw))
	fields := map[string]string{}
	if !strings.HasPrefix(text, "---\n") {
		return fields, text
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fields, text
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	body := strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	return fields, strings.TrimSpace(body)
}

// normalizeText strips a leading UTF-8 byte order mark and normalizes
// CRLF line endings to LF.
func normalizeText(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// parseSkillFrontmatter parses a SKILL.md file's frontmatter. ok is
// false when the file has no --- frontmatter block at all, or the
// block has no name field — either way the caller must skip the entry
// and warn rather than write a nameless skill.
func parseSkillFrontmatter(raw []byte) (name, description, body string, ok bool) {
	text := normalizeText(string(raw))
	if !strings.HasPrefix(text, "---\n") {
		return "", "", "", false
	}
	fields, b := parseFrontmatter([]byte(text))
	name = fields["name"]
	if name == "" {
		return "", "", "", false
	}
	return name, fields["description"], b, true
}

// topHeadingRe matches a top-level "## Heading" markdown line —
// exactly two "#" characters, never a "###" or deeper subheading.
var topHeadingRe = regexp.MustCompile(`(?m)^## +(.+?)\s*$`)

// nonKebabRun matches every run of characters a kebab-case name
// cannot hold.
var nonKebabRun = regexp.MustCompile(`[^a-z0-9]+`)

// kebabify turns s into a kebab-case fragment: lowercased, with every
// run of non-alphanumeric characters collapsed to one hyphen, and
// leading/trailing hyphens trimmed.
func kebabify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonKebabRun.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// firstLine returns the first non-blank trimmed line of s, or "" for
// blank input.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// mdSection is one chunk of a markdown file, split on its top-level
// "##" headings. heading is "" for the content before the first
// heading, and for the single whole-file section an unstructured file
// (no headings at all) produces.
type mdSection struct {
	heading string
	body    string
}

// splitTopSections splits content on its top-level "##" headings.
// structured is false when content has no such heading at all — the
// caller then treats content as one fact for the whole file, per the
// source's own convention, rather than inventing a heading. When
// structured is true, sections holds one entry per heading, plus a
// leading entry with an empty heading for any non-blank preamble
// before the first heading.
func splitTopSections(content string) (sections []mdSection, structured bool) {
	matches := topHeadingRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return []mdSection{{body: content}}, false
	}
	if pre := strings.TrimSpace(content[:matches[0][0]]); pre != "" {
		sections = append(sections, mdSection{body: pre})
	}
	for i, m := range matches {
		heading := strings.TrimSpace(content[m[2]:m[3]])
		start := m[1]
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		sections = append(sections, mdSection{heading: heading, body: strings.TrimSpace(content[start:end])})
	}
	return sections, true
}

// Memory returns candidate facts from two distinct native stores: the
// CLAUDE.md hierarchy (global, and — when ctx.ProjectDir is set — the
// project's own CLAUDE.md, .claude/CLAUDE.md, and CLAUDE.local.md),
// and the separate auto-memory vault Claude Code writes under
// projects/*/memory.
func (ClaudeCode) Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error) {
	root := claudeRoot(ctx)

	type claudeMDFile struct {
		path, base, factType string
	}
	files := []claudeMDFile{
		{filepath.Join(root, "CLAUDE.md"), "claude-md", "user"},
	}
	if ctx.ProjectDir != "" {
		files = append(files,
			claudeMDFile{filepath.Join(ctx.ProjectDir, "CLAUDE.md"), "claude-md-project", "project"},
			claudeMDFile{filepath.Join(ctx.ProjectDir, ".claude", "CLAUDE.md"), "claude-md-project-claude", "project"},
			claudeMDFile{filepath.Join(ctx.ProjectDir, "CLAUDE.local.md"), "claude-md-local", "project"},
		)
	}

	var facts []CandidateFact
	var warnings []Warning
	for _, f := range files {
		fFacts, fWarnings := readClaudeMDFile(f.path, f.base, f.factType)
		facts = append(facts, fFacts...)
		warnings = append(warnings, fWarnings...)
	}

	autoFacts, autoWarnings := scanAutoMemory(root)
	facts = append(facts, autoFacts...)
	warnings = append(warnings, autoWarnings...)

	return facts, warnings, nil
}

// readClaudeMDFile reads one CLAUDE.md-shaped file, strips Loadout's
// own managed block, and splits what is left into one fact per
// top-level "##" section (or one fact for the whole file when it has
// no such heading). A missing file is not a problem. Do NOT follow
// "@path" import lines into other files: a bare "@…/render/memory.md"
// line is Loadout's own memoryImport block body, already removed by
// the strip above, and following user @imports into other project
// files is out of scope for this pass.
func readClaudeMDFile(path, base, factType string) ([]CandidateFact, []Warning) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil
	}
	if info.Size() > maxMemoryFileSize {
		return nil, []Warning{{
			Tool:   "claude-code",
			Path:   path,
			Reason: "this file is larger than 4MiB, the same limit Claude Code itself applies. Fix: split it into smaller files.",
		}}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, []Warning{{Tool: "claude-code", Path: path, Reason: "the file could not be read: " + err.Error()}}
	}

	native, damaged := StripLoadoutBlock(string(raw))
	if damaged {
		return nil, []Warning{{
			Tool:   "claude-code",
			Path:   path,
			Reason: "the loadout marks in this file are damaged. Fix: repair or remove the marks at the source.",
		}}
	}
	native = strings.TrimSpace(native)
	if native == "" {
		return nil, nil
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
			Tool:        "claude-code",
			ModTime:     info.ModTime(),
		})
	}
	return facts, nil
}

// scanAutoMemory globs <root>/projects/*/memory/*.md — Claude Code's
// own separate auto-memory system — and returns one candidate fact
// per topic file, excluding the truncatable MEMORY.md index. Each
// fact carries its frontmatter type through unchanged.
func scanAutoMemory(root string) ([]CandidateFact, []Warning) {
	matches, err := filepath.Glob(filepath.Join(root, "projects", "*", "memory", "*.md"))
	if err != nil || len(matches) == 0 {
		return nil, nil
	}
	sort.Strings(matches)

	var facts []CandidateFact
	var warnings []Warning
	for _, path := range matches {
		if filepath.Base(path) == "MEMORY.md" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			warnings = append(warnings, Warning{Tool: "claude-code", Path: path, Reason: "the file could not be read: " + err.Error()})
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, Warning{Tool: "claude-code", Path: path, Reason: "the file could not be read: " + err.Error()})
			continue
		}
		fields, body := parseFrontmatter(raw)
		body = strings.TrimSpace(body)
		if body == "" {
			warnings = append(warnings, Warning{Tool: "claude-code", Path: path, Reason: "this file has no content to import."})
			continue
		}
		name := fields["name"]
		if name == "" {
			name = kebabify(strings.TrimSuffix(filepath.Base(path), ".md"))
		}
		description := fields["description"]
		if description == "" {
			description = firstLine(body)
		}
		modTime := info.ModTime()
		if m := fields["modified"]; m != "" {
			if t, err := time.Parse(time.RFC3339, m); err == nil {
				modTime = t
			}
		}
		facts = append(facts, CandidateFact{
			Name:        name,
			Description: description,
			Type:        fields["type"],
			Body:        body,
			Tool:        "claude-code",
			ModTime:     modTime,
		})
	}
	return facts, warnings
}
