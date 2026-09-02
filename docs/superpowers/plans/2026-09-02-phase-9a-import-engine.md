# Loadout Phase 9a Implementation Plan — Import Engine + claude-code & codex

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `loadout import` — a universal, reliable, idempotent pull of existing skills and memory from a user's agent tools into the vault — with the shared import engine and the two richest sources (claude-code, codex) end to end, proven on fixture home trees.

**Architecture:** A new `internal/importer/` package, the mirror of `internal/adapter/`. One `Source` per tool produces candidate skills/facts from that tool's standard locations; one shared engine applies every reliability rule once (exclude Loadout's own footprint, exclude vendor content via each tool's own marker, map to Loadout items, dedup by name+content-hash, write as `review: draft` / `by: import:<tool>`, degrade per-item). The `loadout import` CLI verb drives the engine across detected sources.

**Tech Stack:** Go stdlib + the existing deps (toml, age). No new dependencies. Reuse `internal/vault` (item write/scaffold) and `internal/adapter`'s managed-block marker semantics (single source of truth for the `loadout:begin/end` strip).

**Spec:** `/Users/waleed/loadout/PLAN.md` and the Phase 9 design `docs/superpowers/specs/2026-09-02-phase-9-adoption-design.md`. **Authoritative per-tool source map (every path/format/exclusion detail):** the implementer receives `scratchpad/phase-9-import-source-map.md` — READ §0 (Loadout footprint to exclude), §1 (claude-code), §2 (codex), §8 (reliability). It pins the exact locations, markers, and formats.

## Global Constraints

- **Never read a credential or a config dir wholesale.** Read ONLY the named skill/memory files per the source map (§8 risk 2: `~/.codex` holds `auth.json` + a 300MB log next to `AGENTS.md`). No glob of a tool's config root. A test must prove the codex source never opens `config.toml`/`auth.json`.
- **Exclude Loadout's own footprint** (source map §0): strip everything between the literal `<!-- loadout:begin -->` and `<!-- loadout:end -->` marks (reuse `internal/adapter/managed.go` semantics — one begin/one end = well-formed; else damaged → skip+warn; never follow a `@…/render/memory.md` import). For skills, `EvalSymlinks` and exclude any target inside the (parameterized) vault skills dir — resolve REAL paths, never string-match.
- **Exclude vendor/managed via the tool's own signal**, not name-guessing: codex `~/.codex/skills/.system/.codex-system-skills.marker` (skip the whole `.system/` subtree); claude-code `~/.claude/plugins/**` (+ org-policy files); never a hardcoded skill-name denylist as the primary check.
- **Honest provenance:** every imported item is `by: import:<tool>` and `review: draft`. Nothing an importer writes is silently `kept`.
- **Dedup by `name` + sha256(whitespace-normalized body)** against the existing vault AND within the batch. A name collision with a DIFFERENT hash imports both under disambiguated names — never silently drop one.
- **Degrade per-item:** a missing dir, a dangling symlink, a >4MiB file, a damaged block, a malformed file → skip-and-warn for THAT item; never abort the run.
- **Vault root is a parameter**, never hardcoded `~/.loadout` (it is manifest-configurable). Imports land as drafts; `import` does NOT push (no `sync`).
- **Standard:** Go stdlib + existing deps; ASD-STE100; the error grammar (a message names the repair); gofmt/vet clean; `go test -race -count=1 ./...` green before every commit; the trailer `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`. Tests use FIXTURE home trees under `t.TempDir()` — NEVER the real home, NEVER a real credential file, NEVER a mutating verb against the real vault.

## File Structure

```
internal/importer/   types.go (Source, ImportCtx, Candidate*, Warning, ImportResult)
                     engine.go (RunImport: collect→exclude→map→dedup→write→report)
                     exclude.go (StripLoadoutBlock via adapter markers; IsVaultOwnedSkill via EvalSymlinks)
                     dedup.go (name+content-hash key, disambiguation)
                     write.go (candidate → vault item, review:draft, by:import:<tool>)
                     claudecode.go, codex.go (the two Sources)
                     agentsskills.go (shared generic .agents/skills scanner — used by codex now, reused in 9b)
                     *_test.go + testdata/ (fixture home trees)
internal/adapter/    managed.go (export a StripManagedBlock helper if not already exported)
internal/cli/        import.go (the `loadout import` verb), main dispatch wire-up
```

---

### Task 1: The importer kit + engine (with a fake Source)

**Files:** Create `internal/importer/{types.go,engine.go,exclude.go,dedup.go,write.go}` + tests; modify `internal/adapter/managed.go` (export the strip helper if needed).

**Interfaces (`types.go`):**
```go
type ImportCtx struct { Home string; VaultRoot string; VaultSkillsDir string; ProjectDir string } // Home/VaultRoot injectable for tests
type CandidateSkill struct { Name, Description, Body string; Files map[string][]byte; Tool string; ModTime time.Time }
type CandidateFact  struct { Name, Description, Type, Body string; Tool string; ModTime time.Time }
type Warning struct { Tool, Path, Reason string }
type Source interface {
    Name() string
    Detect(ctx ImportCtx) (present bool, root string)
    Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error)
    Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error)
}
type Options struct { Skills, Memory, DryRun bool }
type ImportResult struct {
    Imported []ItemRef; Deduped []ItemRef; Skipped []Warning; Warnings []Warning
} // ItemRef{Kind, Name, Tool}
```

**Engine (`engine.go`):** `func RunImport(v *vault.Vault, sources []Source, ctx ImportCtx, opt Options) (ImportResult, error)`:
1. For each source: `Detect`; if absent, skip (note in result). Else collect `Skills`/`Memory` per `opt` (accumulate their warnings).
2. Apply exclusions (Task-1 helpers) to every candidate. (Sources SHOULD already exclude, but the engine enforces the Loadout-footprint strip on memory bodies and the vault-owned-skill check centrally as defense-in-depth.)
3. Map + dedup across ALL sources (Task-1 `dedup.go`): key = `name` + sha256(normalizeWhitespace(body)); drop exact dupes (record in `Deduped`); a name collision with a different hash → disambiguate the name (`<name>-<tool>` then `<name>-2`) and keep both.
4. Dedup against the EXISTING vault (an item already present with the same name+hash is skipped as `Deduped`).
5. If `DryRun`: record what WOULD be written into `Imported` (as a preview) and return WITHOUT writing. Else write each via `write.go` and record.
6. Return the typed `ImportResult`.

**Exclusion (`exclude.go`):**
- `StripLoadoutBlock(content string) (native string, damaged bool)` — reuse `adapter`'s managed-block parse (export `adapter.StripManagedBlock` if not present; do NOT re-hardcode the markers in a second place). Returns the content with the one well-formed block removed; `damaged=true` when marks are malformed/duplicated (caller skips+warns).
- `IsVaultOwnedSkill(entryPath, vaultSkillsDir string) (bool, error)` — `os.Lstat`; if symlink, `filepath.EvalSymlinks` both the target and `vaultSkillsDir`; return true if the resolved target is within the resolved vault skills dir. A dangling symlink → error the caller turns into skip+warn.

**Write (`write.go`):** `func writeSkill(v, CandidateSkill) error` / `func writeFact(v, CandidateFact) error` — create `skills/<name>/SKILL.md` (+ Files) / `memory/<name>.md` with Loadout frontmatter (`name`, `description`, `by: import:<tool>`, `at` from `ModTime` or now in RFC3339 UTC, `review: draft`; facts also `type`). Reuse the vault's own scaffold/write helpers (`internal/vault/scaffold.go`, `skill.go`, `memory.go`) so the format matches exactly — do not hand-roll frontmatter that diverges from what `loadout add`/the parser expects. Validate the name (kebab; reuse the vault's name validation) — a bad name → skip+warn.

**Steps:**
- [ ] **Failing tests** (`engine_test.go`, `exclude_test.go`, `dedup_test.go`) with a FAKE Source: RunImport writes a fake source's candidate skill + fact as vault items with `review: draft` + `by: import:faketool`; `StripLoadoutBlock` removes a well-formed `loadout:begin/end` block and flags a damaged one; `IsVaultOwnedSkill` returns true for a symlink into the vault skills dir (build a real temp symlink), false for one pointing elsewhere, and skip+warn for a dangling link; dedup drops an exact name+content dupe and keeps a same-name-different-content pair under disambiguated names; dedup against an existing vault item skips it; `DryRun` writes NOTHING (assert the vault is unchanged) but reports the preview.
- [ ] Run → fail. Implement. Green. Commit: `Add the import engine, exclusion, dedup, and write`.

---

### Task 2: The claude-code source

**Files:** Create `internal/importer/claudecode.go` + `claudecode_test.go` + `testdata/claude-home/...` (a fixture `~/.claude` tree).

**Behavior (source map §1):**
- `Detect`: `ctx.Home + "/.claude"` exists (honor `$CLAUDE_CONFIG_DIR` first if set — read it from the environment, but tests inject via `ctx.Home`/an override field). Return present + the resolved root.
- `Skills`: enumerate `<root>/skills/*/SKILL.md` (each skill is a directory with SKILL.md; also copy supporting files in the dir). Resolve symlinks; EXCLUDE any whose real target is inside `ctx.VaultSkillsDir` (Loadout's own) OR inside `<root>/plugins/` (vendor/plugin). Parse frontmatter `name`+`description`, body = the rest. Project skills at `ctx.ProjectDir + "/.claude/skills"` too when ProjectDir is set. Skip+warn on a missing dir, a SKILL.md without frontmatter, a dangling link.
- `Memory`: two paths —
  1. The `CLAUDE.md` hierarchy: `<root>/CLAUDE.md` (user global) + `ctx.ProjectDir + "/CLAUDE.md"` and `/.claude/CLAUDE.md` + `CLAUDE.local.md` when ProjectDir is set. For each: read, `StripLoadoutBlock` (skip+warn if damaged), and if native content remains, split on top-level `##` sections into facts (or one fact for the whole file if unstructured). Do NOT follow `@import` lines into other files in v1 (note it; a bare `@render/memory.md` is Loadout's own and already stripped). Skip a >4MiB file with a warning.
  2. The auto-memory vault: glob `<root>/projects/*/memory/*.md`, EXCLUDING `MEMORY.md` (the truncatable index). Each topic file → one fact, carrying its frontmatter `type` (`user`/`feedback`/`project`/`reference`) through unchanged; `at` from a `modified` frontmatter field if present.
- All items `Tool = "claude-code"`.

**Steps:**
- [ ] **Failing tests** with a fixture `testdata/claude-home`: a `skills/mytool/SKILL.md` imports (name/description/body correct); a `skills/loadout-dogfood` symlink into a fake vault-skills dir is EXCLUDED; a plugin skill under `plugins/.../skills` is EXCLUDED; `CLAUDE.md` with a `loadout:begin/end` block imports only the NATIVE text outside the block (and nothing when the block is the whole file); a damaged block → skip+warn (no fact); an auto-memory `projects/x/memory/note.md` imports as a fact carrying its `type`; `projects/x/memory/MEMORY.md` is NOT imported; a dangling skill symlink → skip+warn. Assert every imported item is `review: draft` + `by: import:claude-code`.
- [ ] Run → fail. Implement. Green. Commit: `Add the claude-code import source`.

---

### Task 3: The codex source + the shared .agents/skills scanner

**Files:** Create `internal/importer/codex.go`, `internal/importer/agentsskills.go` (shared) + tests + `testdata/codex-home/...`.

**Behavior (source map §2):**
- `agentsskills.go` — a shared scanner `scanAgentsSkills(dirs []string, ctx) ([]CandidateSkill, []Warning)`: for each `.agents/skills` (and `.agent/skills`) directory given, enumerate `*/SKILL.md`, resolve symlinks, exclude vault-owned. This is reused by codex now and by pi/droid/gemini in 9b.
- `codex.Detect`: `ctx.Home + "/.codex"` (honor `$CODEX_HOME`). 
- `codex.Skills`: the generic `.agents/skills` scopes — `ctx.ProjectDir + "/.agents/skills"`, `<repo-root>/.agents/skills` (walk up for a `.git`), `ctx.Home + "/.agents/skills"` — via `scanAgentsSkills`. EXCLUDE `<root>/.codex/skills/.system/**` when the marker `<root>/.codex/skills/.system/.codex-system-skills.marker` exists (skip the whole `.system/` subtree). Also scan `<root>/.codex/skills/*/SKILL.md` for non-`.system` user skills.
- `codex.Memory`: the AGENTS.md chain — `<root>/.codex/AGENTS.override.md` else `<root>/.codex/AGENTS.md` (global), plus `ctx.ProjectDir` AGENTS.override.md/AGENTS.md walking git-root→cwd. For each: read, `StripLoadoutBlock` (codex uses `memoryBlock` — the whole rendered memory is inside the marks; the native content is OUTSIDE the marks), split remaining into facts on top-level headings. **NEVER read `config.toml` or `auth.json`** for content.
- All items `Tool = "codex"`.

**Steps:**
- [ ] **Failing tests** with `testdata/codex-home`: a user skill under `.agents/skills/mycodexskill/SKILL.md` imports; a `.codex/skills/.system/review-agent/SKILL.md` (with the `.codex-system-skills.marker` present) is EXCLUDED; `AGENTS.md` with a `loadout:begin/end` block imports only the native text outside it; a vault-owned symlinked skill is excluded; a test asserts the source NEVER opens `config.toml`/`auth.json` (e.g. put a sentinel secret in a fixture `config.toml` and assert it appears in no candidate/warning/output). Assert `review: draft` + `by: import:codex`. Add a cross-source dedup test: the SAME `.agents/skills` skill seen by both claude-code and codex imports ONCE (name+hash), not twice.
- [ ] Run → fail. Implement. Green. Commit: `Add the codex import source and the shared agents-skills scanner`.

---

### Task 4: The `loadout import` CLI verb + end-to-end

**Files:** Create `internal/cli/import.go`; modify the CLI dispatch (`internal/cli/*` main switch) + `loadout help`. Add an end-to-end test.

**Behavior:**
- `loadout import [SOURCE...] [--skills] [--memory] [--project DIR] [--dry-run] [--json]`:
  - No SOURCE → detect + import from every registered source that `Detect`s present (v1 registry = claude-code, codex; 9b adds more). A named SOURCE limits to it.
  - Default: both skills + memory; `--skills`/`--memory` narrow.
  - Build `ImportCtx` from the real environment (Home, the vault root + skills dir from the loaded vault/manifest, ProjectDir = `--project` or cwd). Call `importer.RunImport`.
  - Text output: a concise human report — imported (by kind + tool), deduped count, skipped-with-reason, and the WARNINGS incl. the Cursor-User-Rules-style caveats (none for claude-code/codex, but the mechanism must render warnings). Remind the user the items landed as **drafts** → review (`loadout review` / the dashboard) → `loadout sync --remote` to push.
  - `--json` → the `ImportResult` as JSON (for agents).
  - `--dry-run` → the same report, writing nothing.
- Wire it into `loadout help` (a one-line entry) matching the existing help style.

**Steps:**
- [ ] **Failing end-to-end test** (`internal/cli/import_test.go`) with a temp vault + a fixture home containing BOTH a claude-code and a codex tree: `loadout import --dry-run` writes nothing and reports the previewed items; `loadout import` writes the expected draft items to the vault (skills + facts), excludes Loadout-owned + vendor content, dedups a shared `.agents/skills` skill to one, and prints a report naming drafts + the review/sync next step; `--json` emits a parseable ImportResult; `loadout import codex` limits to codex; every written item is `review: draft` + `by: import:<tool>`; NO credential file content appears anywhere in the output. Assert the real home is untouched (temp Home/vault only).
- [ ] Run → fail. Implement. Green, gofmt/vet clean, full `-race` suite green. Commit: `Add the loadout import CLI verb`.

---

## Self-Review Notes

- **Spec coverage (Phase 9 §3):** the engine + exclusion/dedup/mapping/drafts (T1), claude-code (T2, richest source incl. auto-memory), codex + the shared `.agents/skills` scanner (T3), the `loadout import` verb with dry-run/json/detect-all (T4). Remaining sources (cursor, hermes, pi, gemini, droid) are Plan 9b on this kit; the installer is 9c; docs+repo 9d.
- **Reliability invariants** (source map §8) are enforced centrally in the engine, so a new source in 9b inherits them: strip Loadout's block, resolve symlinks, exclude vendor via the tool's own marker, scoped reads (no config-dir slurp), per-item skip-and-warn, dedup by name+content-hash, drafts only. The secret-safety of codex (never read `config.toml`/`auth.json`) is a named test.
- **The dashboard loop:** imported drafts appear in the Phase 8b dashboard's Review queue — import → review → keep → `sync --remote` → project back out. No new dashboard work here; it already renders drafts.
- **Ordering:** T1 (engine + fake source) → T2 (claude-code) → T3 (codex + shared scanner) → T4 (CLI + e2e). The final whole-branch review runs adversarially on the most capable model: try to make an importer read a credential file, import Loadout's own projected content (circular), silently drop a divergent same-name item, or abort on one bad file. Any success is a defect.
- **No real home, ever:** every test uses `t.TempDir()` fixture homes and a temp vault; the sandbox rule holds.
