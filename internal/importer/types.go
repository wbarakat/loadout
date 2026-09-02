// Package importer pulls skills and memory facts from an agent tool
// INTO the vault — the inverse of internal/adapter, which projects
// the vault OUT to each tool. A Source knows how to find and read one
// tool's native content; RunImport applies the shared reliability
// rules once for every Source: exclude Loadout's own footprint,
// dedup across sources and against the vault, and write each surviving
// item as review:draft.
package importer

import "time"

// ImportCtx carries the paths an import run needs. The CLI fills
// these from the real environment; a test fills them with temp
// paths. The engine itself reads only from ImportCtx, never
// os.Getenv or os.UserHomeDir. A Source's Detect method can still
// read its own override variable, such as CLAUDE_CONFIG_DIR or
// CODEX_HOME, to find that tool's real root. A test that wants a
// fixed fixture root must clear the relevant variable with
// t.Setenv, so the ambient environment does not leak in.
type ImportCtx struct {
	// Home is the user's home directory.
	Home string
	// VaultRoot is the vault's root directory.
	VaultRoot string
	// VaultSkillsDir is the vault's own skills directory — the
	// exclusion target for IsVaultOwnedSkill. A Loadout-projected
	// skill is a symlink whose real target resolves inside here.
	VaultSkillsDir string
	// ProjectDir is the current project directory, for a Source that
	// also reads project-scoped content (a repo's own AGENTS.md, for
	// example).
	ProjectDir string
	// ProjectMemory opts into per-project memory: the auto-memory
	// vaults (~/.claude/projects/*/memory/*.md) and the project-scoped
	// instruction files (project CLAUDE.md/.claude/CLAUDE.md/
	// CLAUDE.local.md, project AGENTS.md) for ProjectDir. The CLI sets
	// this from its own --project-memory flag. The default, false,
	// scopes Memory to GLOBAL instruction files only (~/.claude/
	// CLAUDE.md, ~/.codex/AGENTS.md) — a per-project auto-memory store
	// holds per-project work notes that flood the vault when imported
	// by default.
	ProjectMemory bool
}

// CandidateSkill is one skill a Source found in a tool's native
// store, not yet written to the vault.
type CandidateSkill struct {
	Name        string
	Description string
	Body        string
	// Files holds extra support files to write alongside SKILL.md,
	// keyed by a path relative to the skill folder.
	Files map[string][]byte
	// Tool is the source tool's name, for example "claude-code". It
	// becomes part of the item's by: import:<tool> provenance.
	Tool string
	// ModTime is the source file's own modification time, used as
	// the item's at: instead of the import time. Zero means "use
	// now".
	ModTime time.Time
}

// CandidateFact is one memory fact a Source found in a tool's native
// store, not yet written to the vault.
type CandidateFact struct {
	Name        string
	Description string
	// Type is the fact's type, for example "user" or "project" — the
	// same vocabulary a vault memory fact's type frontmatter key
	// uses.
	Type string
	Body string
	// Tool is the source tool's name. See CandidateSkill.Tool.
	Tool string
	// ModTime is the source file's own modification time. See
	// CandidateSkill.ModTime.
	ModTime time.Time
}

// Warning names one non-fatal problem an import run hit: a file it
// could not read, a name it had to skip, a damaged loadout mark. It
// never aborts the run.
type Warning struct {
	Tool   string
	Path   string
	Reason string
}

// Source knows how to find and read one tool's native skills and
// memory. A Source should already exclude Loadout's own footprint —
// its own managed blocks, its own projected skill symlinks — but the
// engine enforces the same exclusions again as defense-in-depth.
type Source interface {
	// Name returns the source's short tool name, for example
	// "claude-code". It becomes the by: import:<tool> provenance tag
	// on every item this source contributes.
	Name() string
	// Detect reports whether this tool's native store is present on
	// this machine, and the root path it detected it at (for a
	// warning message; may be empty).
	Detect(ctx ImportCtx) (present bool, root string)
	// Skills returns every candidate skill this source finds, plus
	// any non-fatal warnings hit along the way. A single unreadable
	// skill must produce a Warning, not a returned error.
	Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error)
	// Memory returns every candidate memory fact this source finds,
	// plus any non-fatal warnings hit along the way.
	Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error)
}

// Options selects what an import run does.
type Options struct {
	Skills bool
	Memory bool
	// DryRun previews what RunImport would write, without writing
	// anything.
	DryRun bool
}

// ItemRef names one item RunImport imported or deduped: its kind
// ("skill" or "memory"), its final name in the vault, and the tool it
// came from.
type ItemRef struct {
	Kind string
	Name string
	Tool string
}

// ImportResult is what one RunImport call did.
type ImportResult struct {
	// Imported lists every item RunImport wrote — or, under DryRun,
	// every item it would have written.
	Imported []ItemRef
	// Deduped lists every candidate RunImport dropped because it
	// already had the same name and content, either from another
	// source or already in the vault.
	Deduped []ItemRef
	// Skipped lists every candidate RunImport declined to write for
	// some other reason: a bad name, a damaged loadout mark, a name
	// collision with different content.
	Skipped []Warning
	// Warnings lists every non-fatal problem a Source hit while
	// reading its native store.
	Warnings []Warning
}
