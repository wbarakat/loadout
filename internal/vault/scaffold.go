package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// AddSkill creates a skill folder with a SKILL.md template.
func AddSkill(v *Vault, name string) (string, error) {
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
	content := "---\nname: " + name + "\ndescription: <one line: when an agent must use this skill>\n---\n\n# " + name + "\n\n<write the instructions here>\n"
	return path, os.WriteFile(path, []byte(content), 0o644)
}

// AddFact creates a memory fact file with a template.
func AddFact(v *Vault, name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("use a kebab-case name, for example: my-stack")
	}
	path := filepath.Join(v.MemoryDir(), name+".md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("the fact %s already exists", name)
	}
	content := "---\nname: " + name + "\ndescription: <one line summary>\ntype: user\n---\n\n<write the fact here>\n"
	return path, os.WriteFile(path, []byte(content), 0o644)
}
