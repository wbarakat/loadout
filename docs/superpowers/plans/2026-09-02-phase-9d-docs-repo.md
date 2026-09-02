# Loadout Phase 9d Implementation Plan — Agent-First Docs + Public Repo

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Give Loadout a public-quality front door — a rich human README, a root `AGENTS.md` an agent can follow to install and drive Loadout itself, and a focused `docs/` set — then wire up `github.com/wbarakat/loadout`, scrub the history of personal identifiers, and prepare the push. Treat every byte as public.

**Architecture:** Docs are prose, not code, so each doc task's "test" is an ACCURACY GATE: every command, flag, path, and claim must match the REAL CLI (`loadout help` + `loadout <verb>` usage) and the real behavior in the code — nothing invented. The repo task scrubs committed personal identifiers and scans history before any push; the actual `git push` runs from the user's main checkout (the worktree is fenced), so this plan PREPARES it.

**Spec:** `docs/superpowers/specs/2026-09-02-phase-9-adoption-design.md` (§5 docs, §6 repo). The real command surface is the source of truth — run `loadout help` and read `internal/cli/*` rather than trusting memory.

## Global Constraints
- **Accuracy over polish.** No command, flag, path, or capability appears in a doc unless it exists in the current CLI/code. A doc claim that doesn't match `loadout help` or the code is a defect. Verify against the binary.
- **Public-safe.** No secrets, tokens, personal machine paths, or personal account identifiers in any doc or in the committed history that will be pushed. The user's real `~/.loadout` vault is a SEPARATE repo (not here); `.superpowers/sdd/**` is gitignored (reports/ledgers never ship).
- **Honest limits.** Docs state what does NOT work / is deferred (Devin not locally importable, Cursor global User Rules, the self-host v1 trust boundary, secrets-access-log is device-local) — never oversell.
- **Standard:** ASD-STE100 throughout; Markdown; the trailer on every commit. No code changes in this phase except the identifier scrub (a docs edit) — the Go/TS suites must stay green (they will; no source touched).

## File Structure
```
README.md                 (rewrite: the public front door)
AGENTS.md                 (new at repo root: agent onboarding contract)
docs/                     install.md, import.md, secrets.md, mcp.md, dashboard.md, self-host.md, device-roles.md
docs/superpowers/plans/   (scrub personal Vercel identifiers from the committed 8b plans)
```

---

### Task 1: README + root AGENTS.md

**Files:** Rewrite `README.md`; create `AGENTS.md`.

**Before writing:** run `loadout help` (build the binary if needed) and read `internal/cli/run.go` + the verb files to get the EXACT verb/flag surface; read `PLAN.md` (§ vision, security model, phases) and the specs for the security model. Cite only what is real.

**README.md** (public front door, Linear/Resend-grade clarity):
- What Loadout is (one paragraph): a local-first vault (`~/.loadout`) that stores your agent skills, memory, and secrets and projects them into every agent tool, syncing E2E-encrypted between devices via a self-hostable `loadoutd`.
- 60-second quickstart: `loadout init` (the wizard: detect tools → import your existing skills/memory as drafts → sync), `loadout review`, `loadout sync --remote`.
- The command surface (grouped, from `loadout help`): items (add/show/list/recall/edit/context), review, secrets + `run`, import, sync/watch/status/doctor, devices, mcp.
- The security model: local-first, encrypt-before-upload (age), per-device keys + approval, no-secrets device (the dashboard), the self-host v1 trust boundary (encrypted-not-signed; the token holder is trusted), secrets never in agent context.
- The dashboard (a no-secrets browser device; secrets metadata-only), the MCP endpoint (recall + brokered secret use without holding the key), device roles, import (7 tools, drafts, deduped, honest limits).
- Install/build (Go), self-host `loadoutd`. Honest limitations section.

**AGENTS.md** (root — the agent-is-the-user contract): how an agent installs Loadout headless (`loadout init --yes [--tools] [--no-import] [--remote --token-file]`); imports the user's existing content (`loadout import [--project-memory]`, drafts); reviews/keeps (`loadout review`, `review keep/drop`); reads/writes items (`context`, `recall`, `show`, `add`, `edit`) and the write-back protocol; syncs (`sync --remote`); the invariants it MUST respect (a secret value is never revealed — `secret show` refuses without `--reveal`; imports land as drafts; never commit a secret to the vault plaintext). An agent that finds this repo can onboard a user from this file alone.

**Steps:**
- [ ] Draft README.md + AGENTS.md strictly from `loadout help` + the code + PLAN.md. Every verb/flag verified against the binary.
- [ ] Commit: `Add the public README and agent onboarding guide`.

---

### Task 2: The docs/ set

**Files:** Create `docs/{install,import,secrets,mcp,dashboard,self-host,device-roles}.md`.

Each is a short, task-shaped guide, cross-linked, accurate to the real commands:
- **install.md** — the wizard (interactive + headless flags), what it detects/configures, link adoption, re-run safety.
- **import.md** — `loadout import` across the 7 tools, drafts → review → sync, `--project-memory`, dedup, the honest limits (Devin hosted, Cursor User Rules, vendor/VCS excluded).
- **secrets.md** — `secret add/show/rotate/rm/list`, `run --secret`, env injection, the access log, invariant 10 (value only as ciphertext/child-env/reveal).
- **mcp.md** — `loadout mcp` (stdio JSON-RPC), the read tools + brokered `http_request` with `allowed_hosts`, registering it with an agent.
- **dashboard.md** — the no-secrets browser device, connecting to loadoutd via an HTTPS URL, the enroll (`devices approve <name> --no-secrets`), secrets metadata-only, the review queue.
- **self-host.md** — running `loadoutd` (data dir, `-addr`, `-cors-origin`), `loadout remote add`, the v1 trust boundary, the hosted-service obligations (per-device signing, server-side access logging) deferred.
- **device-roles.md** — full vs no-secrets, `devices approve [--no-secrets]`, the guarantee (a no-secrets device cannot decrypt any secret; the re-encrypt-on-role-change).

**Steps:**
- [ ] Draft the seven guides, each verified against the real verbs/flags/behavior.
- [ ] Commit: `Add the docs guides for install, import, secrets, mcp, dashboard, self-host, device roles`.

---

### Task 3: Scrub personal identifiers + wire the repo + prepare the push

**Files:** Edit the committed Phase 8b plan docs (`docs/superpowers/plans/2026-09-01-phase-8b-*.md`) to remove personal Vercel identifiers (the account slug `wbarakats-projects` and the team ID `team_...`), replacing them with a neutral placeholder (e.g. "the owner's Vercel team"). Prepare (do NOT push) the remote wiring.

**Behavior:**
- **Scrub the working tree:** grep the whole repo (tracked files only) for the personal identifiers — the Vercel team id (`team_` + the hash), the account slug, the deployment URL, the user's email, any absolute `/Users/<name>/...` paths in committed docs — and replace them with neutral placeholders. Note: these are in DOC files (plans), not code. (Deep history rewrite is out of scope; the repo is new/private-going-public, and the identifiers are non-secret personal handles — scrubbing the tip + noting the history is sufficient; flag if a true secret is found in history, which would need a real rewrite.)
- **Scan history for a real secret:** `git log -p`-style scan (or `git grep` across history) for anything that looks like a credential (an `age` secret key `AGE-SECRET-KEY-`, a bearer token, `auth.json` content, a private key). The vault is a separate repo and the SDD reports are gitignored, so the surface is small — but VERIFY, don't assume. If a real secret is found in history, STOP and report (history rewrite needed before any public push).
- **Prepare the remote + push commands (do NOT run the push — the worktree is fenced; the user runs it from the main checkout):** write the exact commands into the report: `git remote add origin https://github.com/wbarakat/loadout.git`, then (after merging Phase 8b + 9 to main) `git push -u origin main`. Note that the push publishes the code — the user runs it deliberately.

**Steps:**
- [ ] Scrub the committed docs of personal identifiers (working tree); scan history for a real secret and report the result.
- [ ] Commit the scrub: `Scrub personal identifiers from the committed docs`.
- [ ] Write the remote-wiring + push commands + the history-scan result into the report for the controller/user.

---

## Self-Review Notes
- **Spec coverage (§5/§6):** README + AGENTS.md (T1), the docs/ set (T2), the scrub + repo prep (T3). The push itself is a user action (worktree fenced; outward-facing) — this plan prepares it and hands over the exact commands + the clean history-scan result.
- **The accuracy gate is the review:** each doc task's review runs `loadout help` and reads the code, and flags ANY command/flag/path/claim that doesn't match — a doc that oversells or invents a flag is a defect. The reviewer also confirms no secret/personal identifier remains.
- **Ordering:** T1 (README + AGENTS.md) → T2 (docs/ set) → T3 (scrub + repo prep). The whole-branch review verifies the docs are accurate end-to-end (spot-run several documented commands), the history is secret-free, and no personal identifier survives.
- **No code touched** (except the doc scrub) — the Go/TS suites remain green.
