# Loadout Phase 3 Implementation Plan — Adapter Coverage

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cover the major local agents — Codex CLI, Cursor, Gemini CLI, and hermes — on the typed-report contract, by first collapsing the adapter duplication into one file-adapter kit that makes a new adapter near-configuration.

**Architecture:** One `fileAdapter` type in `internal/adapter` carries the shared Apply/Check logic (skill links, managed memory block, orphan scan, dry-run, damage checks) with a per-tool memory mode: `import` (one import line, plus the render file — Claude Code), `block` (full rendered projection in the managed block — pi, codex, gemini), or `none` (skills only — cursor, hermes, pending instruction-surface verification). ClaudeCode and Pi refactor onto the kit with byte-identical behavior; the four new adapters are kit instances. The registry grows to seven names.

**Tech Stack:** Go stdlib + BurntSushi/toml. No new dependencies.

**Spec:** `~/loadout/PLAN.md` v3 — sections 4 (adapters), 7 (contract), 8 (invariants), 12 (Phase 3: "the adapter kit must make a new adapter cost under one day").

## Verified paths (controller, this machine, 2026-08-31, read-only)

| Tool | Skills dir (verified) | Instructions surface |
|---|---|---|
| codex | `~/.codex/skills` | `~/.codex/AGENTS.md` (verified present) → mode block |
| cursor | `~/.cursor/skills` | none verified → mode none; task probes for a global rules file |
| gemini | `~/.gemini/skills` | `~/.gemini/GEMINI.md` (documented convention; file absent — managed block creates it) → mode block |
| hermes | `~/.hermes/skills` | only `SOUL.md` found (persona file — do NOT write there) → mode none; task probes hermes docs/help |

## Global Constraints

- Go 1.22+. No new dependencies. Module `loadout.dev/loadout`.
- Tests never touch the real home (t.TempDir/t.Setenv; the shared helpers guard HOME).
- All prose in ASD-STE100 Simplified Technical English. Error grammar: `<address or path>: <what happened>. Fix: <exact action>.`
- `gofmt -l .` empty, `go vet ./...` clean, `go test -count=1 ./...` green before every commit; trailer on every commit (blank line before): `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`
- Refactors must be behavior-preserving: the existing 174 tests pass untouched except where a task names a contract change.
- New adapters ship `enabled = false` in DefaultManifest (a sync must not create directories for tools the user does not use). The README documents enabling them. Existing vaults lack the new manifest entries; `Enabled()` already skips absent names — that is the correct behavior, document it.
- Every adapter keeps every invariant: never replace real files or foreign links, prune only vault-owned links, atomic writes, mark scanning, damage checks, typed Reports with Linked/Error and always-present arrays.

## File Structure

```
internal/adapter/  filekit.go (fileAdapter type + memory modes), filekit_test.go,
                   claudecode.go + pi.go shrink to kit instances (or fold into adapter.go),
                   adapter.go (registry: seven names)
internal/vault/    manifest.go (four new AdapterConfig defaults, disabled)
README.md          adapter table + enabling instructions
```

---

### Task 1: The file-adapter kit

**Files:** Create `internal/adapter/filekit.go` + `filekit_test.go`. Modify `claudecode.go`, `pi.go`, `adapter.go`.

**Interfaces (later tasks rely on these exactly):**

```go
type memoryMode int
const (
	memoryNone   memoryMode = iota // skills only
	memoryBlock                    // full renderProjection in the managed block
	memoryImport                   // import line in the block + render/memory.md file
)
type fileAdapter struct {
	name string
	cfg  vault.AdapterConfig
	mode memoryMode
}
func newFileAdapter(name string, cfg vault.AdapterConfig, mode memoryMode) fileAdapter
// fileAdapter implements Adapter: Name, Apply(v, dry) (Report, error), Check(v) []Problem.
```

Behavior is the union of today's ClaudeCode and Pi, selected by mode: mark scan first (block and import modes); memory projection before skills (block: managed block with renderProjection; import: render file via writeFileAtomic + import-line block; none: skip); LinkSkills with Report wiring (Linked, applied/pruned/blocked, newReport initialization); Check: damage check first (block/import), then memory comparison per mode (import mode also compares the render file), skill checkLinks, orphanLinks. Empty SkillsDir in cfg → skip the skills projection entirely (a future instructions-only adapter); empty MemoryFile with mode != none is a config error at Apply/Check: `the adapter NAME has no memory_file in the manifest. Fix: set adapters.NAME.memory_file, or disable the adapter.`

`ClaudeCode` and `Pi` become thin constructors returning kit instances (`newFileAdapter("claude-code", cfg, memoryImport)`, `newFileAdapter("pi", cfg, memoryBlock)`) — the exported names and registry behavior stay so nothing outside the package changes.

**Steps:**

- [ ] Move-and-verify, not rewrite: build filekit.go from the existing claudecode.go/pi.go bodies. The ENTIRE existing adapter and cli test suites must pass untouched — they are the byte-identical-behavior proof. Add kit-specific tests only for the new seams: memoryNone skips memory entirely (no block, no render file, Check reports no memory problems); empty-MemoryFile config error (both Apply and Check); empty-SkillsDir skips skills.
- [ ] `go test -count=1 ./...` green with zero pre-existing test edits, gofmt/vet clean. Commit: `Extract the file-adapter kit`.

### Task 2: The codex adapter

**Files:** Modify `internal/vault/manifest.go` (add `codex` default: enabled false, skills_dir `~/.codex/skills`, memory_file `~/.codex/AGENTS.md`), `internal/adapter/adapter.go` (registry: `newFileAdapter("codex", cfg, memoryBlock)`). Test: enable codex in a test manifest, drive Apply/Check/dry against sandboxed paths — the standard adapter test trio (apply+check clean, drift flagged, blocked real dir protected).

**Steps:**

- [ ] Failing tests → implement → green. Commit: `Add the codex adapter`.

### Task 3: The gemini adapter

Same shape as Task 2: manifest default (enabled false, `~/.gemini/skills`, `~/.gemini/GEMINI.md`), registry entry, mode block, test trio plus one extra: the memory file does not exist before the first sync — the managed block creates it (the kit already does; assert it).

- [ ] Failing tests → implement → green. Commit: `Add the gemini adapter`.

### Task 4: The cursor adapter

Manifest default (enabled false, `~/.cursor/skills`, memory_file EMPTY), registry entry, mode none. Verification step first (read-only, sandbox-exempt because it reads docs/help, never writes): probe for a Cursor global-rules file under `~/.cursor` (e.g. a rules or AGENTS.md convention) via `ls ~/.cursor` and any local docs; if a real global instructions file is confirmed, use mode block with that path and record the evidence in the report; otherwise ship mode none and note the projection is skills-only. Test trio minus memory (mode none assertions from the kit tests apply).

- [ ] Verify → failing tests → implement → green. Commit: `Add the cursor adapter` (commit body names the verified paths and the mode decision evidence).

### Task 5: The hermes adapter

Same shape as Task 4: manifest default (enabled false, `~/.hermes/skills`, memory_file EMPTY), registry, mode none. Verification step: `hermes --help` and `ls ~/.hermes` for an instructions surface that is NOT SOUL.md (never target a persona file); mode block only with confirmed evidence, else skills-only. Test trio minus memory.

- [ ] Verify → failing tests → implement → green. Commit: `Add the hermes adapter` (evidence in the commit body).

### Task 6: Grammar sweep, docs, smoke

**Files:** The Phase 1-era errors without the grammar (final-review rec): `a vault already exists at %s` (+ `Fix: open it with any loadout command, or choose another LOADOUT_HOME.`), `the skill %s already exists` / `the fact %s already exists` (+ `Fix: choose another name, or edit the existing item.`), `the loadout marks in %s are damaged: repair or remove them` → `the loadout marks in %s are damaged. Fix: repair or remove the marks in %s.` — update every test asserting the old strings and every Check/dry path that matches on them (grep first; the damage error is compared in several places). README: the six-adapter table with modes and enabling instructions. Sandboxed smoke: enable all six adapters in a scratch vault, sync, doctor, dry-run; transcript in the report.

- [ ] Grep for every assertion on the old strings → failing tests updated → implement → green. Commit: `Sweep the error grammar and document the adapters`.

---

## Self-Review Notes

- Spec coverage (PLAN.md §12 Phase 3): four new local agents ✓ (T2-T5), typed-report contract ✓ (kit, T1), under-a-day adapter cost ✓ (T2/T3 are manifest+registry+tests only), grammar sweep rec ✓ (T6).
- Ordering: T1 before all; T2-T5 independent after T1 (still execute sequentially — same files: manifest.go, adapter.go); T6 last.
- Risk: T1 is the load-bearing refactor — its guard is the untouched 174-test suite. T4/T5 verification steps keep unverified instruction surfaces out of the write path (invariant: never write where the layout is a guess).
- Real-vault migration (controller, post-merge, with the user's standing dogfood approval): add the four stanzas to `~/.loadout/loadout.toml` with `enabled = true` for the tools present, sync, and surface the blocked copy-pasted skills (tldraw-offline ×4, coast-cli-skill ×3) as the migration list for the user.
