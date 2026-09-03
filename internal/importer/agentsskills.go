package importer

import (
	"os"
	"path/filepath"
)

// scanAgentsSkills scans the generic ".agents/skills" shape shared by
// several agent tools: for every dir in dirs that exists, each direct
// subdirectory is one candidate skill folder, expected to hold a
// SKILL.md file. Codex uses this today; pi, Gemini, and Droid reuse
// it in a later phase, since all four read the same convention.
//
// For each entry it excludes Loadout's own projected skills
// (IsVaultOwnedSkill against ctx.VaultSkillsDir) and turns a
// dangling symlink, a non-directory entry, an unreadable SKILL.md,
// or a SKILL.md with no valid frontmatter into a Warning rather than
// aborting the scan. Every returned skill carries Tool = tool. A
// missing dir is not a problem — most installs have no skills in a
// given scope at all — and a dir seen more than once in dirs is only
// scanned once.
func scanAgentsSkills(dirs []string, tool string, ctx ImportCtx) ([]CandidateSkill, []Warning) {
	var skills []CandidateSkill
	var warnings []Warning
	seen := map[string]bool{}

	for _, dir := range dirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			s, w := scanSkillEntry(filepath.Join(dir, e.Name()), tool, ctx.VaultSkillsDir)
			warnings = append(warnings, w...)
			if s != nil {
				skills = append(skills, *s)
			}
		}
	}
	return skills, warnings
}

// scanSkillEntry treats entryPath as one candidate skill folder: a
// vault-owned entry (IsVaultOwnedSkill against vaultSkillsDir) comes
// back as (nil, nil) — excluded silently, since that is expected, not
// a problem. Anything else that keeps the entry from importing comes
// back as a Warning, never an error, so one bad entry never stops the
// rest of a scan: a dangling symlink, a non-directory entry, a
// missing or unreadable SKILL.md, or a SKILL.md with no valid
// frontmatter. A returned skill can still carry its own warnings — a
// support file symlink escaping the skill folder, for example —
// alongside it, so those never get lost just because the skill itself
// imported fine.
func scanSkillEntry(entryPath, tool, vaultSkillsDir string) (*CandidateSkill, []Warning) {
	owned, err := IsVaultOwnedSkill(entryPath, vaultSkillsDir)
	if err != nil {
		return nil, []Warning{{
			Tool:   tool,
			Path:   entryPath,
			Reason: "this skill link is dangling and does not resolve. Fix: remove the link, or point it at a real skill.",
		}}
	}
	if owned {
		return nil, nil
	}

	// Resolve the skill folder to its REAL path before reading
	// anything from it — see collectSkillFiles for why this matters
	// for a symlinked skill folder's support files.
	realPath, err := filepath.EvalSymlinks(entryPath)
	if err != nil {
		return nil, []Warning{{
			Tool:   tool,
			Path:   entryPath,
			Reason: "this skill link is dangling and does not resolve. Fix: remove the link, or point it at a real skill.",
		}}
	}

	info, err := os.Stat(realPath)
	if err != nil || !info.IsDir() {
		return nil, []Warning{{
			Tool:   tool,
			Path:   entryPath,
			Reason: "this is not a skill folder. Fix: a skill must be a directory holding a SKILL.md file.",
		}}
	}

	skillMDPath := filepath.Join(realPath, "SKILL.md")
	raw, err := os.ReadFile(skillMDPath)
	if err != nil {
		return nil, []Warning{{
			Tool:   tool,
			Path:   entryPath,
			Reason: "no readable SKILL.md file. Fix: add a SKILL.md file, or remove the folder.",
		}}
	}
	name, description, body, ok := parseSkillFrontmatter(raw)
	if !ok {
		return nil, []Warning{{
			Tool:   tool,
			Path:   skillMDPath,
			Reason: "no valid frontmatter. Fix: add a --- block with a name field.",
		}}
	}

	modTime := info.ModTime()
	if st, err := os.Stat(skillMDPath); err == nil {
		modTime = st.ModTime()
	}

	files, fileWarnings := collectSkillFiles(realPath, tool)

	if total, tooLarge := skillTooLarge(raw, files); tooLarge {
		// The whole skill is skipped, so its per-file support-file
		// warnings are moot noise — report only the one folder-too-large
		// warning, not a warning per dropped file.
		return nil, []Warning{tooLargeSkillWarning(tool, entryPath, name, total)}
	}

	return &CandidateSkill{
		Name:        name,
		Description: description,
		Body:        body,
		Files:       files,
		Tool:        tool,
		ModTime:     modTime,
	}, fileWarnings
}
