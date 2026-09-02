package vault

import (
	"os"
	"path/filepath"
)

// Skill is one skill folder with a SKILL.md file.
type Skill struct {
	Name        string
	Description string
	// Body is the skill's content below its frontmatter block, the
	// same shape ListFacts already returns for a Fact. Dedup uses it
	// to hash a skill's content.
	Body string
	Dir  string
	// By names who wrote this skill, for example "human" or
	// "claude-code". Empty for a skill scaffolded before provenance
	// tracking existed.
	By string
	// At is the RFC3339 time of the write. Empty for a skill
	// scaffolded before provenance tracking existed.
	At string
	// Review is "kept" or "draft". Empty means the same as "kept":
	// a skill scaffolded before provenance tracking existed has no
	// review field at all, and must count as already reviewed.
	Review string
}

// isSkillDir reports whether entry is a directory, or a symlink that
// resolves to one.
func isSkillDir(e os.DirEntry, path string) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// ListSkills reads every skill folder, in name order. A symlink that
// resolves to a directory counts as a skill folder too. It skips a
// folder without a SKILL.md file; InvalidSkillDirs reports those.
func ListSkills(v *Vault) ([]Skill, error) {
	entries, err := os.ReadDir(v.SkillsDir())
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, e := range entries {
		dir := filepath.Join(v.SkillsDir(), e.Name())
		if !isSkillDir(e, dir) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}
		fields, body := parseFrontmatter(raw)
		skills = append(skills, Skill{
			Name:        e.Name(),
			Description: fields["description"],
			Body:        body,
			Dir:         dir,
			By:          fields["by"],
			At:          fields["at"],
			Review:      fields["review"],
		})
	}
	return skills, nil
}

// InvalidSkillDirs lists skill folders without a SKILL.md file, and a
// skill folder whose SKILL.md file exists but cannot be read.
func InvalidSkillDirs(v *Vault) ([]string, error) {
	entries, err := os.ReadDir(v.SkillsDir())
	if err != nil {
		return nil, err
	}
	var bad []string
	for _, e := range entries {
		dir := filepath.Join(v.SkillsDir(), e.Name())
		if !isSkillDir(e, dir) {
			continue
		}
		if _, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err != nil {
			bad = append(bad, dir)
		}
	}
	return bad, nil
}
