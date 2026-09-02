package importer

import (
	"fmt"
	"os"
	"path/filepath"
)

// Gemini is the import Source for Gemini CLI's own on-disk store:
// skills at ~/.gemini/skills, a global memory file at
// ~/.gemini/GEMINI.md (always in scope), and a project memory file at
// <project>/GEMINI.md — only when ctx.ProjectMemory is set (source
// map §5, RULING 2: the default is global instruction files only).
type Gemini struct{}

func (Gemini) Name() string { return "gemini" }

// geminiRoot resolves Gemini CLI's root directory: ctx.Home +
// "/.gemini". Gemini has no documented relocation environment
// variable (source map §7).
func geminiRoot(ctx ImportCtx) string {
	return filepath.Join(ctx.Home, ".gemini")
}

func (Gemini) Detect(ctx ImportCtx) (bool, string) {
	root := geminiRoot(ctx)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, ""
	}
	return true, root
}

// Skills scans ~/.gemini/skills via the shared generic scanner.
func (Gemini) Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error) {
	dirs := []string{filepath.Join(geminiRoot(ctx), "skills")}
	skills, warnings := scanAgentsSkills(dirs, "gemini", ctx)
	return skills, warnings, nil
}

// Memory returns candidate facts from the global GEMINI.md always,
// plus the project's own GEMINI.md only when ctx.ProjectMemory is
// set — the same per-project opt-in every other source applies (see
// claudecode.go's Memory doc). When ProjectMemory is off and a
// project GEMINI.md exists anyway, this warns that --project-memory
// would include it, so the flag stays discoverable rather than
// silently importing nothing with no explanation.
func (Gemini) Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error) {
	facts, warnings := readInstructionMemory([]string{filepath.Join(geminiRoot(ctx), "GEMINI.md")}, "gemini")

	if ctx.ProjectDir == "" {
		return facts, warnings, nil
	}

	projectPath := filepath.Join(ctx.ProjectDir, "GEMINI.md")
	if ctx.ProjectMemory {
		pf, pw := readInstructionMemory([]string{projectPath}, "gemini")
		for i := range pf {
			pf[i].Type = "project"
			// Both the global and project files are named GEMINI.md,
			// so pathFactBase alone gives them the identical base
			// "gemini" — suffix the project one so a direct Memory()
			// call's own result never names two different files'
			// facts alike (the shared dedup pass, dedup.go, would
			// still resolve the collision correctly on its own, but
			// there is no reason to lean on that here).
			pf[i].Name += "-project"
		}
		facts = append(facts, pf...)
		warnings = append(warnings, pw...)
		return facts, warnings, nil
	}

	if info, err := os.Stat(projectPath); err == nil && !info.IsDir() {
		warnings = append(warnings, Warning{
			Tool:   "gemini",
			Reason: fmt.Sprintf("%d per-project memory sources skipped; pass --project-memory to include them.", 1),
		})
	}
	return facts, warnings, nil
}
