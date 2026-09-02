# Loadout Phase 9 — The Adoption Layer (Design)

**Status:** Draft for review. **Date:** 2026-09-02.
**Spec authority:** `PLAN.md` (the product spec). This design extends it with a new phase.
**Research basis:** `scratchpad/phase-9-import-source-map.md` (per-tool skill/memory conventions, web- and machine-verified 2026-09-02) — the authoritative source map for every path/format claim below.

## 1. Why this phase

Loadout works, and its browser dashboard is live. But adoption is manual: a user hand-adds items, hand-configures adapters, and reads no onboarding. For a SaaS, three gaps block a stranger (human **or** agent) from going from zero to a populated, synced vault:

1. **No import.** A new user already has skills and memory scattered across Claude Code, Codex, Cursor, hermes, and others. Loadout can project *out* to those tools but cannot pull their existing content *in*. The vault starts empty; the user won't hand-copy dozens of skills.
2. **No installer.** `loadout init` creates a bare vault. It does not detect the user's installed agent tools, wire up adapters, offer to import, or connect a remote. Setup is a manual, error-prone sequence (as this project's own live setup proved).
3. **No onboarding docs.** There is a README, but nothing written so a human — or an agent acting for the user — can install, configure, and drive Loadout autonomously from the repository alone.

Phase 9 closes all three. It is the layer that turns a working tool into an adoptable product.

**Design stance: portability over this machine.** Everything here targets *standard, per-tool default locations on an arbitrary machine*, never one user's setup. Paths after `~` are portable; usernames, hashes, and one user's symlink layout are not. Where a tool's convention is unstable or undocumented (Cursor global User Rules), the design states the limit honestly rather than importing unreliably.

## 2. Deliverables

- **A. Importers** — `loadout import`, a universal, reliable, idempotent pull from each supported tool into the vault.
- **B. Interactive installer** — `loadout init` becomes a first-run wizard (with a headless mode for agents/CI).
- **C. Agent-first docs** — a rich public README, a root `AGENTS.md`, and a `docs/` set an agent can follow to install and run Loadout itself.
- **D. The public repository** — wire up `github.com/wbarakat/loadout`, scrub the history of personal identifiers, push. Treat the repo as public.

Each is a candidate for its own implementation plan; §8 suggests the build sequence.

---

## 3. Deliverable A — the import subsystem

### 3.1 The model: importers are the inverse of adapters

Loadout already has **adapters** (`internal/adapter/`): pure functions from vault state to a tool's on-disk state, one per tool, over a shared `fileAdapter` kit. **Importers are the mirror**: pure functions from a tool's on-disk state to a set of candidate vault items. They live in a new `internal/importer/` package with a shared kit, exactly parallel to the adapter kit, so a new importer costs about a day (the same bar the adapter kit set).

```
type Source interface {
    Name() string                                  // "claude-code", "codex", ...
    Detect() (present bool, root string)           // is the tool on this machine? where?
    Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error)
    Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error)
}
```

`ImportCtx` carries the real **vault root** (never hard-coded `~/.loadout` — it is a manifest-configurable value, source-map §0) and the current project directory (for project-scoped sources). A `CandidateSkill`/`CandidateFact` is a normalized, not-yet-written item plus its provenance and a content hash.

### 3.2 The engine

One engine drives every source, so the reliability rules live in exactly one place:

1. **Detect** the source; if absent, skip with a note (never error).
2. **Collect** candidates from the source's declared locations only — *strictly scoped reads* (source-map §8 risk 2: `~/.codex` holds `auth.json` and a 300MB log next to `AGENTS.md`; never glob a config dir).
3. **Exclude Loadout's own footprint** (source-map §0), centrally:
   - Memory: strip everything between the literal `<!-- loadout:begin -->` and `<!-- loadout:end -->` marks (mirror `internal/adapter/managed.go` semantics — one begin, one end = well-formed; anything else = damaged, skip+warn). Do not follow a `memoryImport` `@…/render/memory.md` line.
   - Skills: `os.Lstat` → if symlink, `EvalSymlinks`; if the real target is inside the (parameterized) vault skills dir, exclude. Resolve real paths, never string-match (macOS `/tmp`↔`/private/tmp`).
4. **Exclude vendor/managed** content via each tool's own signal (never name-guessing): Codex `~/.codex/skills/.system/.codex-system-skills.marker`; hermes `~/.hermes/skills/.bundled_manifest` (+ `.archive/`); Claude Code `~/.claude/plugins/**`; org-managed policy files at fixed OS paths.
5. **Map** to a Loadout item (§3.4).
6. **Dedup** by `name` + sha256 of the whitespace-normalized body — against the existing vault *and* within the batch. A name collision with a **different** hash imports both under disambiguated names (or flags for review); it never silently drops one (source-map §8 risk 6, proven by a real `AGENTS.md`/`CLAUDE.md` divergence).
7. **Write** each surviving candidate as a vault item with `review: draft` and `by: import:<tool>` — never silently "kept".
8. **Report** a typed result: imported, deduped, skipped-with-reason, warnings.

Per-item failures (a locked hermes file, a >4MiB CLAUDE.md, malformed `.mdc`, a dangling symlink) **skip-and-warn**; they never abort the run (source-map §8 risk 5).

### 3.3 Supported sources (v1)

Per the source map. The generic `.agents/skills` + `.agent/skills` scanner (project + global) is shared, since Claude Code, Codex, Cursor, pi, and **Droid** all read it — scan once, attribute to each detected tool, dedup.

| Source | Skills | Memory | Notes |
|---|---|---|---|
| **claude-code** | `~/.claude/skills/*/SKILL.md` (+ project `.claude/skills`), exclude `~/.claude/plugins/**` | `CLAUDE.md` hierarchy (strip block) **+ auto-memory** `~/.claude/projects/*/memory/*.md` (its `type` = `user/feedback/project/reference` maps 1:1 to Loadout `type`) | richest source; check `$CLAUDE_CONFIG_DIR` first |
| **codex** | generic `.agents/skills` scopes; exclude `.system/` via marker | `AGENTS.md`/`AGENTS.override.md` chain (strip block) | check `$CODEX_HOME`; never read `config.toml`/`auth.json` for content |
| **cursor** | `~/.cursor/skills/*/SKILL.md` + project `.cursor/skills` | project `.cursor/rules/*.mdc` + `.cursorrules` (may be a **directory** — branch on `IsDir`) | **global User Rules NOT importable** (undocumented SQLite) → explicit warning |
| **hermes** | `~/.hermes/skills` + `~/.hermes/profiles/*/skills`, exclude vendor via `.bundled_manifest`/`.archive` | `~/.hermes/memories/{MEMORY,USER}.md` + profile memories (agent-managed; `§`-split best-effort); exclude `SOUL.md` | namespace profile imports `import:hermes:<profile>` |
| **pi** | `~/.pi/agent/skills` | `~/.pi/agent/AGENTS.md` (strip block) | same AGENTS.md mechanics as codex |
| **gemini** | `~/.gemini/skills` | `~/.gemini/GEMINI.md` hierarchy (strip block) | official hierarchical concat |
| **droid** | covered by the generic `.agents/skills` scan | `~/.factory/AGENTS.md` + repo `AGENTS.md` | no bespoke importer needed beyond generic paths |

**Explicitly out of scope for v1 (stated, not silently dropped):**
- **Devin** — hosted; Skills/Knowledge live in Devin Cloud, no durable local store. (Low-confidence `.devin/rules/` repo convention noted as a future probe.)
- **Cursor global User Rules** — undocumented, unstable SQLite blob. The importer warns and tells the user to copy them from Cursor Settings → Rules.
- **Windows paths** — unverified in research; the installer ships macOS/Linux first, Windows after direct verification.
- **hermes `SOUL.md`, Cursor custom-agent `~/.cursor/agents/*.md`** — persona/subagent config, not portable skill/memory; future targets.

### 3.4 Mapping to Loadout items

- **Skill** → `skills/<name>/SKILL.md` (+ any supporting files, copied). Keep `name` + `description` + body; drop tool-specific frontmatter (`allowed-tools`, `agents/openai.yaml`, Droid's extra fields) silently. `by: import:<tool>`, `at` from a `modified`/mtime source or now, `review: draft`.
- **Memory** → `memory/<name>.md`. One fact per top-level `##` section for structured files, else one fact per file (auto-memory topic files → one fact each, carrying `type` through). `by: import:<tool>`, `review: draft`.

### 3.5 CLI surface

```
loadout import [SOURCE...] [--skills] [--memory] [--project DIR] [--dry-run] [--json]
```
- No `SOURCE` → detect and import from every detected tool, deduped across all of them.
- `--dry-run` → preview: a table of what would import, the dedup/skip/warn summary (incl. the Cursor-User-Rules caveat), **writing nothing**. This is the safe default an agent runs first.
- `--json` → the typed result, for agent consumption.
- Imports do **not** auto-push; they land as drafts. The user reviews (`loadout review`, or the **dashboard Review queue**), keeps what they want, then `loadout sync --remote`. This is the closed loop: *import → review → keep → project back out to every tool*.

### 3.6 Testing (SaaS-grade)

Table-driven per-source tests over **fixture home trees** (a temp dir shaped like `~/.claude`, `~/.codex`, etc., with real SKILL.md/AGENTS.md/`.mdc` fixtures, symlinks, a Loadout-block-containing memory file, a vendor marker, a dangling link). Assert: Loadout's own block/symlinks excluded; vendor content excluded via markers; native content imported as `draft`/`by: import:<tool>`; dedup by name+hash (same content collapses, divergent content both kept); per-item failures warn-not-abort. Never touch the real home; never read a real credential file. A cross-source dedup test proves the same `~/.agents/skills` skill imported once when three tools point at it.

---

## 4. Deliverable B — the interactive installer

`loadout init` becomes a first-run wizard, with every step scriptable for a headless path.

**Interactive flow:**
1. **Detect** installed tools via the source-map §7 signals (dir presence → binary on `PATH` → tool-specific env var; no XDG assumptions). Present the detected set.
2. **Create** the vault at `~/.loadout` (or `$LOADOUT_HOME`) if absent.
3. **Enable + configure adapters** for the detected tools — write `loadout.toml` with each tool's correct `skills_dir`/`memory_file` (the same defaults the adapters already use).
4. **Offer to import** existing skills/memory (`loadout import --dry-run` preview → confirm → import as drafts).
5. **Optionally connect a remote** — point at a self-hosted `loadoutd` URL + token, or skip (local-only).
6. **Summarize**: what was created, what was imported (as drafts to review), and the exact next commands.

**Headless mode** (`loadout init --yes [--tools a,b] [--no-import] [--remote URL --token-file F]`): the same steps, no prompts, deterministic — so an agent or CI can install Loadout unattended. This is the "an agent installs Loadout itself" path and must be first-class, not an afterthought.

**Constraints:** never write a real credential to the transcript or logs; a `--token-file` reads the token from a file, never an argument. Interactive prompts use the stdlib (no new TUI dependency unless a later decision adds one); a genuinely rich TUI is a possible follow-up, not a v1 requirement.

---

## 5. Deliverable C — agent-first documentation

Written to public/front-door quality (the repo will be public). Three layers:

- **`README.md`** (humans, the front door): what Loadout is and why; a 60-second quickstart (`init` → `import` → `sync`); the command surface; the security model (local-first, E2E, no-secrets device, self-host `loadoutd`); the dashboard; install/build. Linear/Resend-grade clarity.
- **`AGENTS.md`** (root, for agents): the "agent is the user" contract — how an agent installs Loadout headless (`init --yes`), imports the user's existing content, reviews/keeps, syncs, and reads/writes items; the verb surface and the write-back protocol; the invariants it must respect (secrets are never revealed; imports land as drafts). An agent that finds the repo can onboard the user from this file alone.
- **`docs/`** set: focused guides — installation, importing, secrets, the MCP endpoint, the dashboard, self-hosting `loadoutd`, device roles. Each short, task-shaped, cross-linked.

**Consistency guard:** the docs describe the *real* command surface. A doc claim that doesn't match a verb in `loadout help` is a defect the review must catch (the docs are tested against the CLI's actual help/flags where feasible).

---

## 6. Deliverable D — the public repository

- **Add remote** `origin = github.com/wbarakat/loadout`.
- **History scrub (blocking, before any push):** remove personal identifiers already committed — the Phase 8b plan docs name the Vercel account slug and team ID; either scrub those lines or drop the deploy-specific detail. A full-history scan for tokens/credentials/personal paths runs as push-prep; the vault (`~/.loadout`) is a separate repo and not present here, and SDD ledgers/reports are gitignored, so the surface is small but must be verified, not assumed.
- **Push** `main` once the scan is clean. Treat every byte as public.
- **Mechanics:** this worktree session is fenced from git operations outside its own branch; the `remote add`/`push` run from the main checkout — the controller prepares exact commands and the scrub, the user (or the controller from the main checkout, if permitted) runs the push.
- **Not in scope here:** CI, release automation, `create_git_project` auto-deploy — noted as follow-ups once the repo is populated.

---

## 7. Cross-cutting principles (from the research)

1. **Strictly scoped reads.** Only the named files/dirs per source. Never glob a tool's config dir (credential-leak + performance risk).
2. **Resolve real paths.** `EvalSymlinks` everywhere exclusion or dedup depends on identity.
3. **Exclude via the tool's own signal**, not name-guessing (markers, manifests, plugin dirs).
4. **Degrade per-item.** Missing/locked/oversized/malformed → skip-and-warn, never abort.
5. **Honest provenance.** Every import is `by: import:<tool>`, `review: draft`. Nothing an importer writes is silently authoritative.
6. **Dedup by name+content-hash**, keep divergent collisions.
7. **State the limits.** Cursor User Rules, Devin, Windows — surfaced explicitly to the user, never papered over.
8. **These conventions drift** (Claude Code alone shipped ~half a dozen 2026 changes) — the source map is a living document; importers carry a re-verification cadence.

## 8. Suggested build sequence (for the plans)

1. **Plan 9a — the import engine + the two richest sources** (`claude-code`, `codex`) end-to-end: the `internal/importer/` kit, the engine (exclusion/dedup/mapping/drafts), the CLI verb, fixture-home tests. Proves the model on the highest-value tools.
2. **Plan 9b — the remaining sources** (`cursor`, `hermes`, `pi`, `gemini`, generic `.agents/skills`/Droid) on the kit, plus the cross-source dedup + the honest-limits warnings.
3. **Plan 9c — the interactive installer** (`loadout init` wizard + headless mode) on top of import.
4. **Plan 9d — docs + the public repo** (README, AGENTS.md, docs/, remote + scrub + push).

Each plan ships working, reviewed software. 9a and 9b are the core; 9c/9d make it adoptable.

## 9. Open questions for review

1. **Scope of v1 sources** — is claude-code + codex + cursor + hermes (+ pi/gemini/droid for free via generic paths) the right first cut, deferring Devin and Cursor User Rules with explicit warnings? (Recommended: yes.)
2. **Installer depth** — stdlib prompts for v1 (a rich TUI later), or invest in a TUI now? (Recommended: stdlib prompts + a solid headless mode first.)
3. **Import auto-push** — keep import local (drafts) and require an explicit `sync --remote`, or add an `--sync` convenience? (Recommended: local drafts by default; `--sync` as an opt-in convenience.)
4. **Repo push mechanics** — controller prepares the scrub + commands and the user runs the push from the main checkout, or is there a path for the controller to push directly? (Recommended: user runs it, given the worktree fence.)
