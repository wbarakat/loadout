package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidName reports whether name is a valid kebab-case item name —
// the same rule AddSkill, AddFact, and every import writer enforce.
func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

// reviewFor reports the review state for a fresh write by by. A
// human write is already reviewed. Any other writer, such as an
// agent or an importer, starts as a draft until a human reviews it.
func reviewFor(by string) string {
	if by == "human" {
		return "kept"
	}
	return "draft"
}

// provenanceLines renders the by, at, and review frontmatter lines
// for a new scaffold file, with at set to now.
func provenanceLines(by string) string {
	return provenanceLinesAt(by, time.Now())
}

// provenanceLinesAt renders the by, at, and review frontmatter lines
// with an explicit at time. An importer uses this through
// WriteSkillContent/WriteFactContent to carry forward the source
// tool's own modification time instead of the write time.
func provenanceLinesAt(by string, at time.Time) string {
	return "by: " + by + "\nat: " + at.UTC().Format(time.RFC3339) + "\nreview: " + reviewFor(by) + "\n"
}

// AddSkill creates a skill folder with a SKILL.md template. by names
// who is writing it, for example "human" or "claude-code".
func AddSkill(v *Vault, name, by string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("use a kebab-case name, for example: deploy-checks")
	}
	dir := filepath.Join(v.SkillsDir(), name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("the skill %s already exists. Fix: choose another name, or edit the existing item.", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "SKILL.md")
	content := "---\nname: " + name + "\ndescription: <one line: when an agent must use this skill>\n" +
		provenanceLines(by) + "---\n\n# " + name + "\n\n<write the instructions here>\n"
	return path, os.WriteFile(path, []byte(content), 0o644)
}

// AddFact creates a memory fact file with a template. by names who
// is writing it, for example "human" or "claude-code".
func AddFact(v *Vault, name, by string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("use a kebab-case name, for example: my-stack")
	}
	path := filepath.Join(v.MemoryDir(), name+".md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("the fact %s already exists. Fix: choose another name, or edit the existing item.", name)
	}
	content := "---\nname: " + name + "\ndescription: <one line summary>\ntype: user\n" +
		provenanceLines(by) + "---\n\n<write the fact here>\n"
	return path, os.WriteFile(path, []byte(content), 0o644)
}

// WriteSkillContent creates a skill folder with real content, instead
// of AddSkill's placeholder template. by and at set the same
// provenance fields AddSkill sets from "now" — an importer passes
// "import:<tool>" and the source tool's own modification time
// instead, so a non-human by still yields review: draft. files holds
// extra support files to write next to SKILL.md, keyed by a path
// relative to the skill folder. It returns the same "bad name" and
// "already exists" errors as AddSkill, so a caller such as an
// importer can turn either into a skip and a warning.
//
// A files key must never write outside the skill folder. Each key is
// checked with filepath.IsLocal before use: a key that is absolute,
// or that holds a ".." element, is not written, and its key is
// returned in skipped instead — a caller such as an importer turns
// that into its own warning. This guard runs here, not only in a
// caller, since WriteSkillContent is the one place that actually
// turns a key into a path on disk.
// yamlScalar renders s so it is safe to place after "key: " in a
// frontmatter block. Most descriptions are plain prose and pass through
// unchanged. A value that would make the line ambiguous or invalid YAML
// is double-quoted instead: one holding a colon-space (an imported
// description really did read "patterns including: inflated symbolism"),
// one opening with a YAML indicator character such as ">" or "|", one
// ending in a colon, or an empty one. Without this, a valid description
// could still produce frontmatter no tool can parse.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := strings.Contains(s, ": ") ||
		strings.Contains(s, " #") ||
		strings.HasSuffix(s, ":") ||
		strings.ContainsAny(s[:1], `>|&*!%@`+"`"+`"'#?:,[]{}-`) ||
		strings.ContainsAny(s, "\n\r\t")
	if !needsQuote {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func WriteSkillContent(v *Vault, name, description, body, by string, at time.Time, files map[string][]byte) (path string, skipped []string, err error) {
	if !namePattern.MatchString(name) {
		return "", nil, fmt.Errorf("use a kebab-case name, for example: deploy-checks")
	}
	dir := filepath.Join(v.SkillsDir(), name)
	if _, statErr := os.Stat(dir); statErr == nil {
		return "", nil, fmt.Errorf("the skill %s already exists. Fix: choose another name, or edit the existing item.", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	path = filepath.Join(dir, "SKILL.md")
	content := "---\nname: " + name + "\ndescription: " + yamlScalar(description) + "\n" +
		provenanceLinesAt(by, at) + "---\n\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", nil, err
	}
	for rel, data := range files {
		if !filepath.IsLocal(rel) {
			skipped = append(skipped, rel)
			continue
		}
		fp := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return "", skipped, err
		}
		if err := os.WriteFile(fp, data, 0o644); err != nil {
			return "", skipped, err
		}
	}
	return path, skipped, nil
}

// WriteFactContent creates a memory fact file with real content,
// instead of AddFact's placeholder template. See WriteSkillContent
// for by and at.
func WriteFactContent(v *Vault, name, description, factType, body, by string, at time.Time) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("use a kebab-case name, for example: my-stack")
	}
	path := filepath.Join(v.MemoryDir(), name+".md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("the fact %s already exists. Fix: choose another name, or edit the existing item.", name)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\ntype: " + factType + "\n" +
		provenanceLinesAt(by, at) + "---\n\n" + strings.TrimSpace(body) + "\n"
	return path, os.WriteFile(path, []byte(content), 0o644)
}
