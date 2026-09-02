# Loadout Phase 9b Implementation Plan — Remaining Import Sources (pi, gemini, droid, cursor, hermes)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add the remaining import Sources on the Phase 9a kit so `loadout import` pulls skills + memory from pi, Gemini CLI, Droid, Cursor, and hermes — reliably and portably — while inheriting every 9a reliability rule.

**Architecture:** Each tool is a `Source` (the interface from `internal/importer/types.go`) reusing the shared engine, `scanAgentsSkills`, `StripLoadoutBlock`, `IsVaultOwnedSkill`, `collectSkillFiles` (VCS/build-excluded, size-capped), the dedup, and the `ProjectMemory` scoping from 9a. pi/gemini/droid follow the AGENTS.md/GEMINI.md pattern (near-trivial reuse); Cursor and hermes are bespoke. All register in the `loadout import` source registry.

**Tech Stack:** Go stdlib + existing deps. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-02-phase-9-adoption-design.md` (§3). **Authoritative per-tool detail:** the implementer reads `scratchpad/phase-9-import-source-map.md` — §3 (Cursor), §4 (hermes), §5 (pi/Gemini), §6 (Droid), §0 (exclusions). It pins every path/marker/format.

## Global Constraints (inherited from 9a — every Source obeys)
- **Strictly scoped reads** — only the named skill/memory files per source; NEVER glob a tool's config dir (secrets live there). NEVER read a credential/config file for content.
- **Exclude Loadout's own footprint** — `StripLoadoutBlock` on memory; `IsVaultOwnedSkill` per skill entry (call it in the source's walk). Exclude vendor content via the tool's OWN marker (hermes `.bundled_manifest`/`.archive`; never name-guess).
- **collectSkillFiles (9a)** already prunes VCS/build dirs + caps size + skips out-of-folder symlinks — reuse it, do not re-walk.
- **Honest provenance** — `by: import:<tool>` (hermes profiles: `import:hermes:<profile>`), `review: draft`.
- **Memory scope (9a FIX4):** DEFAULT = GLOBAL instruction files only; per-project/profile memory behind `ImportCtx.ProjectMemory` (the `--project-memory` flag). Cursor has NO global memory → skills-only by default.
- **Degrade per-item** (skip+warn, never abort); **dedup** by name+content-hash (incl. Files digest). Imports never push.
- Go stdlib; ASD-STE100; error grammar; gofmt/vet clean; `go test -race -count=1 ./...` green before every commit; the trailer. Fixture home trees under `t.TempDir()` only — NEVER the real home/credential.

## File Structure
```
internal/importer/  memoryfile.go (shared instruction-file reader, refactored from claudecode/codex), pi.go, gemini.go, droid.go, cursor.go, hermes.go + *_test.go + testdata/<tool>-home/
internal/cli/        import.go (register all sources in the detect-all registry; usage/help lists them)
```

---

### Task 1: Shared instruction-file reader + pi, gemini, droid

**Files:** Create `internal/importer/memoryfile.go`, `pi.go`, `gemini.go`, `droid.go` + tests + fixtures. Refactor claudecode.go/codex.go to use the shared reader if it reduces duplication (do not change their behavior/tests).

**Shared reader** `readInstructionMemory(paths []string, tool string) ([]CandidateFact, []Warning)`: for each existing path — read, `StripLoadoutBlock` (damaged → skip+warn), skip >4MiB with a warning, split remaining native content on top-level `##` headings into facts (kebab name from heading, else one fact per file). This is the codex/claude-code memory logic factored once.

**Sources** (source map §5/§6): each `Detect` on the tool dir (+ its env override where one exists), `Skills` via `scanAgentsSkills` on the tool's skills dir(s), `Memory` via `readInstructionMemory` honoring `ProjectMemory`:
- **pi** — `~/.pi/agent`; skills `~/.pi/agent/skills`; memory GLOBAL `~/.pi/agent/AGENTS.md` (default); Tool "pi".
- **gemini** — `~/.gemini`; skills `~/.gemini/skills`; memory GLOBAL `~/.gemini/GEMINI.md` (default), project `GEMINI.md` under ProjectMemory; Tool "gemini".
- **droid** — `~/.factory`; skills via the generic `.agents/skills` scopes (scanAgentsSkills) + `~/.factory/skills`; memory GLOBAL `~/.factory/AGENTS.md` (default), project `AGENTS.md` under ProjectMemory; Tool "droid". Drop Droid's extra SKILL.md frontmatter fields silently (keep name/description/body).

**Steps:**
- [ ] Failing tests (fixture homes): each of pi/gemini/droid imports a skill (Tool tag, draft, by:import:<tool>) and its GLOBAL memory file (stripped) by default; a project memory file is imported ONLY with ProjectMemory; a vault-owned symlinked skill is excluded; the shared reader strips the loadout block. A cross-tool dedup test: a `.agents/skills` skill seen by droid + codex imports once.
- [ ] Implement (refactor the reader, add the 3 sources). Green. Commit: `Add pi, gemini, and droid import sources`.

---

### Task 2: The cursor source

**Files:** Create `internal/importer/cursor.go` + test + `testdata/cursor-home/`.

**Behavior (source map §3):**
- `Detect`: `~/.cursor` dir (CLI half) OR the app-data dir (macOS `~/Library/Application Support/Cursor`, Linux `~/.config/Cursor`) OR a `cursor`/`cursor-agent` binary. Tool "cursor".
- `Skills`: `~/.cursor/skills/*/SKILL.md` (global) + project `.cursor/skills/*/SKILL.md` (when ProjectDir set). Via the skill walk + `IsVaultOwnedSkill` + `collectSkillFiles`. Key off SKILL.md presence, NEVER a dir-name pattern (a stale `~/.cursor/skills-cursor` with no SKILL.md must be ignored).
- `Memory`: **project-scoped only, under `ProjectMemory`** (Cursor has NO importable global memory). When ProjectMemory + ProjectDir: `.cursor/rules/*.mdc` (parse the YAML-ish frontmatter leniently — `description`, `globs`, `alwaysApply`; a parse failure → skip+warn, never crash; put the glob as plain text in the body; `type: project` if globbed, `type: user` if alwaysApply) + `.cursorrules` (**branch on `IsDir` — if a directory, import each file inside as its own fact; if a file, one fact**). By DEFAULT (no ProjectMemory) cursor imports NO memory.
- **Global User Rules → NOT importable** (undocumented SQLite): ALWAYS emit ONE informational Warning when cursor is detected — `Cursor global "User Rules" cannot be imported (Cursor stores them in an internal database). Fix: copy them from Cursor Settings → Rules if you want them in Loadout.`

**Steps:**
- [ ] Failing tests (fixture cursor-home): a `~/.cursor/skills/x/SKILL.md` imports; a stale `skills-cursor/` dir with no SKILL.md is ignored; a vault-owned symlinked skill excluded; default import brings NO memory but ALWAYS emits the User-Rules warning; with ProjectMemory, a `.cursor/rules/a.mdc` imports as a fact (glob in body) and a `.cursorrules` FILE imports as one fact; a `.cursorrules` DIRECTORY imports each inner file; a malformed `.mdc` → skip+warn (run continues).
- [ ] Implement. Green. Commit: `Add the cursor import source`.

---

### Task 3: The hermes source

**Files:** Create `internal/importer/hermes.go` + test + `testdata/hermes-home/`.

**Behavior (source map §4):**
- `Detect`: `~/.hermes` dir with `config.yaml`, or a `hermes` binary. Tool "hermes".
- `Skills`: `~/.hermes/skills/*` — EXCLUDE every skill whose directory name is a key in `~/.hermes/skills/.bundled_manifest` (a `<name>:<hash>` list — read it, build a set, skip those) AND anything under `~/.hermes/skills/.archive/`. Keep the user's own top-level skills (symlinks + real dirs) via `IsVaultOwnedSkill` + `collectSkillFiles`. ALSO enumerate `~/.hermes/profiles/*/skills` (same manifest-exclusion per profile if a profile has its own manifest) — namespace nothing on skills (a skill name is a skill name; dedup handles overlap).
- `Memory`: GLOBAL `~/.hermes/memories/MEMORY.md` + `USER.md` (default) — read, split on the unofficial `§` delimiter into facts (fallback: whole file = one fact if no `§`); `MEMORY.md` chunks `type: project`, `USER.md` chunks `type: user`; `by: import:hermes`. **Check for a `.lock` sidecar** (`MEMORY.md.lock`/`USER.md.lock`) — if present, skip+warn ("hermes is writing this file; try again"). Under `ProjectMemory`: ALSO `~/.hermes/profiles/*/memories/{MEMORY,USER}.md` (namespace `by: import:hermes:<profile>`).
- **EXCLUDE `SOUL.md`** everywhere (persona/identity, never memory) — top-level and per-profile.

**Steps:**
- [ ] Failing tests (fixture hermes-home): a user skill imports; a bundled skill listed in `.bundled_manifest` is EXCLUDED; an `.archive/` skill excluded; `memories/MEMORY.md` + `USER.md` import as facts (§-split, right types) by DEFAULT; a `.lock` sidecar → that file skip+warn; `SOUL.md` NEVER imported; a profile's skills/memories import only under ProjectMemory with `by: import:hermes:<profile>`; a vault-owned symlinked skill excluded.
- [ ] Implement. Green. Commit: `Add the hermes import source`.

---

### Task 4: Register the fleet + full-import integration + help

**Files:** Modify `internal/cli/import.go` (the source registry + usage/help); add a full-fleet integration test.

**Behavior:**
- Register all sources in the `loadout import` detect-all registry: claude-code, codex, cursor, hermes, pi, gemini, droid (the order the report prints). A named `loadout import <tool>` accepts any of them; the unknown-source error lists all valid names. `loadout help`/the verb usage names the supported tools and notes Devin is not locally importable + the Cursor-User-Rules caveat.
- The report already renders warnings (the cursor User-Rules caveat surfaces there) and the `--project-memory` skipped-count.

**Steps:**
- [ ] Failing integration test (`internal/cli/import_test.go`): a fixture Home holding ALL SEVEN tools' trees (each with a skill + its memory, a shared `.agents/skills` skill seen by several, a vault-owned skill, a hermes bundled skill, a cursor stale dir) → `loadout import` detects all present, imports the union as drafts, dedups shared skills to one, excludes vault-owned + vendor, emits the cursor User-Rules warning, and by DEFAULT imports only global memory (project/profile memory only with `--project-memory`); `loadout import cursor` limits to cursor; the unknown-source error lists all 7. No credential/config content in the output.
- [ ] Implement. Green, full `-race` suite green, gofmt/vet clean. Commit: `Register the full import fleet and integration-test it`.

---

## Self-Review Notes
- **Spec coverage (§3):** cursor, hermes, pi, gemini, droid on the kit; the honest limits (Cursor User Rules warned, Devin excluded) surfaced. Every 9a reliability rule inherited centrally (exclusions, caps, dedup, drafts, scoped reads, ProjectMemory).
- **Bespoke risks** are Cursor (`.cursorrules`-may-be-a-dir, lenient `.mdc` parse, User-Rules warn) and hermes (`.bundled_manifest` vendor exclusion, `§`-split, `.lock` sidecar, `SOUL.md` exclusion, profiles). Both have dedicated tests.
- **Ordering:** T1 (shared reader + the 3 easy tools) → T2 (cursor) → T3 (hermes) → T4 (registry + full-fleet integration). The whole-branch review runs adversarially on the most capable model: try to make any new source read a credential, import Loadout's own or a vendor skill, follow an @import, abort on one bad file, or default-import project/profile memory. Re-run a fixture `loadout import --dry-run`.
- **No real home, ever.** Fixture homes + temp vault only.
