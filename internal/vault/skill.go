package vault

import (
	"os"
	"path/filepath"
)

// Skill is one skill folder with a SKILL.md file.
type Skill struct {
	Name        string
	Description string
	Dir         string
}

// ListSkills reads every skill folder, in name order. It skips a
// folder without a SKILL.md file; InvalidSkillDirs reports those.
func ListSkills(v *Vault) ([]Skill, error) {
	entries, err := os.ReadDir(v.SkillsDir())
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(v.SkillsDir(), e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}
		fields, _ := parseFrontmatter(raw)
		skills = append(skills, Skill{Name: e.Name(), Description: fields["description"], Dir: dir})
	}
	return skills, nil
}

// InvalidSkillDirs lists skill folders without a SKILL.md file.
func InvalidSkillDirs(v *Vault) ([]string, error) {
	entries, err := os.ReadDir(v.SkillsDir())
	if err != nil {
		return nil, err
	}
	var bad []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(v.SkillsDir(), e.Name())
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			bad = append(bad, dir)
		}
	}
	return bad, nil
}
