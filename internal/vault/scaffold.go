package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// reviewFor reports the review state for a fresh write by by. A
// human write is already reviewed. Any other writer, such as an
// agent, starts as a draft until a human reviews it.
func reviewFor(by string) string {
	if by == "human" {
		return "kept"
	}
	return "draft"
}

// provenanceLines renders the by, at, and review frontmatter lines
// for a new scaffold file.
func provenanceLines(by string) string {
	at := time.Now().UTC().Format(time.RFC3339)
	return "by: " + by + "\nat: " + at + "\nreview: " + reviewFor(by) + "\n"
}

// AddSkill creates a skill folder with a SKILL.md template. by names
// who is writing it, for example "human" or "claude-code".
func AddSkill(v *Vault, name, by string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("use a kebab-case name, for example: deploy-checks")
	}
	dir := filepath.Join(v.SkillsDir(), name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("the skill %s already exists", name)
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
		return "", fmt.Errorf("the fact %s already exists", name)
	}
	content := "---\nname: " + name + "\ndescription: <one line summary>\ntype: user\n" +
		provenanceLines(by) + "---\n\n<write the fact here>\n"
	return path, os.WriteFile(path, []byte(content), 0o644)
}
