# Loadout Phase 9c Implementation Plan — Interactive Installer + Link Adoption

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make `loadout init` a first-run wizard that detects installed agent tools, creates the vault, wires the adapters, offers to import existing content, and optionally connects a remote — with a headless mode so an agent or CI can install Loadout unattended. Plus: teach the adapter to *adopt* a pre-existing foreign skill symlink instead of refusing, so a machine that already shares skills via `~/.agents/skills` installs cleanly.

**Architecture:** A `detectTools()` helper (the source-map §7 detection signals) feeds an interactive `loadout init` wizard (stdlib prompts) and a headless `loadout init --yes [flags]`. The adapter's skill projection (`LinkSkills`) gains a safe adoption rule: a foreign SYMLINK for a skill the vault owns is replaced by Loadout's link; a real file/dir is never clobbered.

**Tech Stack:** Go stdlib only (no TUI dependency). Reuse `internal/vault` (Init/manifest), `internal/adapter` (the adapter configs + LinkSkills), `internal/importer` (offer import), `internal/remote` (optional remote).

**Spec:** `docs/superpowers/specs/2026-09-02-phase-9-adoption-design.md` (§4). Detection signals: `scratchpad/phase-9-import-source-map.md` §7.

## Global Constraints
- **Never clobber a real file/dir.** Link adoption replaces ONLY a foreign SYMLINK (not a real file/dir) and ONLY for a skill name the vault owns. A real file/dir at the target stays a warn-not-touch (current behavior).
- **Headless is first-class.** Every wizard step is reachable non-interactively via flags (`--yes`, `--tools`, `--no-import`, `--remote`, `--token-file`) so an agent/CI installs unattended. No prompt blocks a `--yes` run.
- **No secret to the transcript/logs.** A remote token is read from `--token-file` (never a flag value); never printed. Detection reads only dir/binary presence — never a config/credential file.
- **Idempotent + safe.** Re-running `loadout init` on an existing vault updates config without destroying content; it reports what it changed. Detection degrades to "not installed" rather than guessing (check the tool env override first, then the default path — no XDG assumptions; source-map §7).
- Go stdlib; ASD-STE100; error grammar; gofmt/vet clean; `go test -race -count=1 ./...` green before every commit; the trailer. Tests use temp HOME/vault + fixture tool dirs — NEVER the real home.

## File Structure
```
internal/adapter/  links.go (LinkSkills: adopt a foreign symlink for a vault-owned skill) + links_test.go
internal/cli/      detect.go (detectTools), init.go (the wizard + headless), run.go help
```

---

### Task 1: Tool detection

**Files:** Create `internal/cli/detect.go` + test.

**Interface:** `func detectTools(home string) []DetectedTool` where `DetectedTool{Name string; Present bool; Root string; SkillsDir string; MemoryFile string}` — for each supported tool (claude-code, codex, cursor, hermes, pi, gemini, droid), apply the source-map §7 signal: the tool's env override (`$CLAUDE_CONFIG_DIR`, `$CODEX_HOME`) first, else `home + "/.<tool dir>"` presence (claude `~/.claude`, codex `~/.codex`, cursor `~/.cursor`, hermes `~/.hermes`, pi `~/.pi/agent`, gemini `~/.gemini`, droid `~/.factory`); a binary on PATH is a fallback signal. Return the detected set with the default skills_dir/memory_file each adapter uses (so the installer can write the manifest). `home` and PATH are injectable for tests. Devin is NOT detected (hosted).

**Steps:**
- [ ] Failing test: a fixture home with `~/.claude` + `~/.codex` present (others absent) → detectTools returns claude-code + codex Present with the right roots/skills_dir/memory_file, the rest not Present; `$CODEX_HOME` override is honored over the default. No config/credential file is read (only dir/stat).
- [ ] Implement. Green. Commit: `Add agent-tool detection`.

---

### Task 2: Adapter adopts a foreign skill symlink

**Files:** Modify `internal/adapter/links.go` (LinkSkills) + `links_test.go`.

**Behavior:** When LinkSkills wants to create `<tool-skills-dir>/<name>` and something already exists there:
- It is already Loadout's correct link (resolves to the vault's `<name>` skill) → leave it (idempotent — current behavior).
- It is a **symlink to a NON-vault target** (a foreign link, e.g. `~/.agents/skills/<name>`) AND the vault owns a skill named `<name>` → **ADOPT**: atomically replace it with Loadout's link to the vault's `<name>`, and record it in the report as `adopted` (a new report category alongside applied/pruned/blocked). Resolve real paths (`EvalSymlinks`) — never string-match.
- It is a **real file or real directory** (not a symlink) → **refuse**, report `blocked` with the existing fix message (never clobber user content). Unchanged.
- A `--dry-run`/Check pass reports what WOULD be adopted without changing anything.

**Steps:**
- [ ] Failing tests (real `os.Symlink` fixtures): a foreign symlink `<skillsdir>/archify -> <someother>/archify` with the vault owning `archify` → LinkSkills ADOPTS it (the link now resolves into the vault; reported `adopted`); a real FILE at `<skillsdir>/archify` → BLOCKED (untouched, reported blocked); an already-correct Loadout link → left, not re-reported as adopted; dry-run adopts nothing but reports the pending adoption.
- [ ] Implement. Green (keep all existing adapter tests passing — the report gains an `adopted` field). Commit: `Adopt a foreign skill symlink instead of conflicting`.

---

### Task 3: The `loadout init` wizard (interactive)

**Files:** Modify `internal/cli/init.go` (extend the existing `init` verb into a wizard) + test; `run.go` help.

**Behavior (spec §4):** `loadout init` interactively:
1. **Detect** tools (Task 1); print the detected set.
2. **Create** the vault at `$LOADOUT_HOME`/`~/.loadout` if absent (reuse `vault.Init`); if it exists, keep it and say so.
3. **Enable + configure adapters** for the detected tools — write `loadout.toml` with each tool's correct `skills_dir`/`memory_file` (the defaults the adapters use), asking the user to confirm the set (default: all detected).
4. **Offer import** — run `loadout import --dry-run` to preview, then on confirm run the real import (drafts). Default yes.
5. **Optionally connect a remote** — prompt for a `loadoutd` URL + a token (read from a path, not echoed), or skip (local-only).
6. **Summary** — what was created/changed, what imported (as drafts to review), and the exact next commands (`loadout review`, `loadout sync --remote`, the dashboard).

Use stdlib prompts (bufio over stdin); each prompt has a sensible default. Do not print a token. Drive the vault/adapter/import/remote steps through the existing APIs (do not reimplement).

**Steps:**
- [ ] Failing test (temp HOME + fixture tool dirs, scripted stdin): `loadout init` detects the fixture tools, creates the vault, writes a `loadout.toml` enabling the detected adapters with the right paths, previews + performs an import (drafts present), and prints the summary + next steps; re-running on the existing vault does NOT destroy content and reports "vault already exists". A declined import step imports nothing.
- [ ] Implement. Green. Commit: `Make loadout init an interactive first-run wizard`.

---

### Task 4: Headless mode + integration + help

**Files:** Modify `internal/cli/init.go` (flags) + `run.go` help; add an integration test.

**Behavior:** `loadout init --yes [--tools a,b,...] [--no-import] [--remote URL --token-file PATH] [--project-memory]` runs the SAME steps with NO prompts, deterministically: create the vault, enable adapters for `--tools` (or all detected), import unless `--no-import`, connect the remote if `--remote` (token from `--token-file`, never a flag), print a machine-readable-ish summary. This is the "an agent installs Loadout itself" path. `loadout help` documents `init`'s wizard + the headless flags.

**Steps:**
- [ ] Failing integration test (temp HOME + fixture tools, NO stdin): `loadout init --yes` installs unattended — vault created, adapters enabled for the detected tools, an import ran (drafts present), summary printed, exit 0; `--no-import` skips import; `--tools claude-code` enables only that adapter; `--remote URL --token-file f` writes the remote config with the token from the file (never in argv/output); the token never appears in stdout. `loadout help` names the init flags.
- [ ] Implement. Green, full `-race` suite green, gofmt/vet clean. Commit: `Add headless loadout init for unattended install`.

---

## Self-Review Notes
- **Spec coverage (§4):** detect (T1), the wizard (T3), headless (T4); plus the link-adoption refinement (T2) the real-world test motivated. The installer drives the existing vault/adapter/import/remote APIs — no reinvention.
- **Safety:** link adoption never clobbers a real file/dir (only a foreign symlink for a vault-owned skill); the remote token is file-only, never echoed; detection reads no credential; re-running init is non-destructive.
- **Adopt-links policy (flag for the user's review):** the chosen default is "repoint a foreign symlink for a skill Loadout now owns → Loadout becomes that skill's source across tools." That is the intended source-of-truth behavior, but it does change a user's existing `~/.agents/skills` wiring for imported skills; surfaced here so the user can confirm or narrow it.
- **Ordering:** T1 (detect) → T2 (adopt links, independent adapter change) → T3 (wizard) → T4 (headless + integration). The whole-branch review runs adversarially: try to make init clobber a real file, echo a token, read a credential during detection, or destroy an existing vault on re-run.
- **No real home, ever.** Temp HOME/vault + fixture tool dirs only.
