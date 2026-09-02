package importer

import (
	"fmt"
	"os"
	"path/filepath"
)

// Droid is the import Source for Factory AI's Droid: skills from the
// generic .agents/skills scopes (project, the project's repo root,
// and global — the same convention Codex reads) plus Droid's own
// ~/.factory/skills, and the AGENTS.md memory chain — a global file
// always, a per-project chain only under ctx.ProjectMemory (source
// map §6, RULING 2).
//
// Droid's own SKILL.md frontmatter carries extra fields beyond the
// open-standard minimum (allowed-tools, enabled, user-invocable,
// disable-model-invocation, license, compatibility, version,
// metadata). No extra handling is needed for that here:
// parseSkillFrontmatter (claudecode.go), which scanAgentsSkills calls
// for every entry, already reads only name/description/body out of a
// SKILL.md's frontmatter block and drops every other key silently.
type Droid struct{}

func (Droid) Name() string { return "droid" }

// droidRoot resolves Droid's root directory: ctx.Home + "/.factory".
// Droid has no documented relocation environment variable (source
// map §7).
func droidRoot(ctx ImportCtx) string {
	return filepath.Join(ctx.Home, ".factory")
}

func (Droid) Detect(ctx ImportCtx) (bool, string) {
	root := droidRoot(ctx)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, ""
	}
	return true, root
}

// Skills scans the generic .agents/skills scopes — project, the
// project's repo root, and global, via findGitRoot (codex.go) — plus
// Droid's own <root>/skills, all through the one shared
// scanAgentsSkills call, so every scope gets the same vault-owned
// exclusion and size caps.
func (Droid) Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error) {
	root := droidRoot(ctx)

	var dirs []string
	if ctx.ProjectDir != "" {
		dirs = append(dirs, filepath.Join(ctx.ProjectDir, ".agents", "skills"))
		if gitRoot := findGitRoot(ctx.ProjectDir); gitRoot != "" && gitRoot != ctx.ProjectDir {
			dirs = append(dirs, filepath.Join(gitRoot, ".agents", "skills"))
		}
	}
	dirs = append(dirs, filepath.Join(ctx.Home, ".agents", "skills"))
	dirs = append(dirs, filepath.Join(root, "skills"))

	skills, warnings := scanAgentsSkills(dirs, "droid", ctx)
	return skills, warnings, nil
}

// Memory returns candidate facts from the global AGENTS.md always,
// plus — only when ctx.ProjectMemory is set, and ctx.ProjectDir is
// too — one file per directory from the project's repo root
// (findGitRoot/projectAgentsDirs, both in codex.go) down to
// ProjectDir. Each project-chain file is read through its own
// readInstructionMemory call so a directory-based suffix can
// disambiguate facts between directories that all happen to hold a
// file named "AGENTS.md" — pathFactBase alone only tells two files
// apart by their own basename, which is identical at every level of
// this chain.
func (Droid) Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error) {
	root := droidRoot(ctx)

	facts, warnings := readInstructionMemory([]string{filepath.Join(root, "AGENTS.md")}, "droid")

	if ctx.ProjectMemory && ctx.ProjectDir != "" {
		dirs := projectAgentsDirs(ctx.ProjectDir)
		top := dirs[0]
		for _, dir := range dirs {
			path := filepath.Join(dir, "AGENTS.md")
			pf, pw := readInstructionMemory([]string{path}, "droid")
			// Every project-chain fact gets a "-project" (or
			// "-project-<rel>") suffix, even the repo-root file —
			// without it, the repo root's own AGENTS.md would name
			// its facts identically to the always-global
			// ~/.factory/AGENTS.md facts, both being plain "AGENTS.md"
			// files whose pathFactBase is the same "agents". The
			// shared dedup pass (dedup.go) would still resolve that
			// collision correctly, but naming it distinctly here
			// keeps a direct Memory() call's own result unambiguous.
			suffix := "-project"
			if dir != top {
				if rel, err := filepath.Rel(top, dir); err == nil {
					suffix = "-project-" + kebabify(rel)
				}
			}
			for i := range pf {
				pf[i].Type = "project"
				pf[i].Name += suffix
			}
			facts = append(facts, pf...)
			warnings = append(warnings, pw...)
		}
	} else if !ctx.ProjectMemory {
		if n := countDroidSkippedProjectMemory(ctx); n > 0 {
			warnings = append(warnings, Warning{
				Tool:   "droid",
				Reason: fmt.Sprintf("%d per-project memory sources skipped; pass --project-memory to include them.", n),
			})
		}
	}

	return facts, warnings, nil
}

// countDroidSkippedProjectMemory reports how many directories in
// ctx.ProjectDir's own AGENTS chain (repo root down to ProjectDir)
// hold an AGENTS.md file, without opening any of them — used only to
// report how many per-project sources --project-memory would add
// when it is not set. An empty ctx.ProjectDir yields 0, matching
// projectAgentsDirs' own rule.
func countDroidSkippedProjectMemory(ctx ImportCtx) int {
	n := 0
	for _, dir := range projectAgentsDirs(ctx.ProjectDir) {
		if info, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil && !info.IsDir() {
			n++
		}
	}
	return n
}
