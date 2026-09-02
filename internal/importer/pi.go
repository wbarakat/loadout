package importer

import (
	"os"
	"path/filepath"
)

// Pi is the import Source for pi's own on-disk store: skills at
// ~/.pi/agent/skills, and one global memory file, ~/.pi/agent/AGENTS.md.
// pi keeps no per-project memory file of its own (source map §5) —
// its AGENTS.md is a single global file, always in scope, regardless
// of ctx.ProjectMemory (that flag only gates OTHER sources' own
// per-project files).
type Pi struct{}

func (Pi) Name() string { return "pi" }

// piRoot resolves pi's root directory: ctx.Home + "/.pi/agent". pi
// has no documented relocation environment variable (source map §7),
// so, unlike Codex or Claude Code, there is no override to check
// first.
func piRoot(ctx ImportCtx) string {
	return filepath.Join(ctx.Home, ".pi", "agent")
}

func (Pi) Detect(ctx ImportCtx) (bool, string) {
	root := piRoot(ctx)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, ""
	}
	return true, root
}

// Skills scans ~/.pi/agent/skills via the shared generic scanner —
// the same .agents/skills-shaped convention Codex, Gemini, and Droid
// all read (source map §5), including its own vault-owned-symlink
// exclusion and size caps.
func (Pi) Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error) {
	dirs := []string{filepath.Join(piRoot(ctx), "skills")}
	skills, warnings := scanAgentsSkills(dirs, "pi", ctx)
	return skills, warnings, nil
}

// Memory returns candidate facts from pi's one global AGENTS.md file,
// via the shared instruction-memory reader.
func (Pi) Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error) {
	path := filepath.Join(piRoot(ctx), "AGENTS.md")
	facts, warnings := readInstructionMemory([]string{path}, "pi")
	return facts, warnings, nil
}
