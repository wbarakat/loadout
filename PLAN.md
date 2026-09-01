# Loadout — Product Plan

Status: v3. Phase 1 is built and under final review. This version adds the agent-native design layer (sections 5–8). Date: 2026-08-31.

## 1. Vision

Loadout is one secure cloud account for your agent gear. It stores your skills, your API keys, and your memory. It syncs them to every agent tool on every machine.

An agent session is ephemeral compute. The vault is the durable half of the agent. Loadout gives an agent one self across many bodies: the same skills, the same memory, the same keys, in every harness and on every machine.

You edit a skill once. Every tool sees the change. You add a key once. Every agent can use it, but no agent can read it. Your memory follows you from Claude Code to pi to your next tool. And what your agents learn flows back into the vault, under your control.

## 2. Inspiration

- **From Linear**: a local-first sync engine. The app feels instant because data lives on the device. The cloud is for sync, not for reads. Also: opinionated design, few options, high polish.
- **From Resend**: a developer-first surface. One clean CLI and one clean API. Setup takes five minutes. The docs read like a product.
- **From agent harnesses themselves**: the index-plus-files pattern. An agent reads one small index of one-line hooks. It loads a body only when it needs it. This is the cheapest shape knowledge can take inside a context window.

The product rule: each feature must be simple, complete, and correct. Ship fewer features that work perfectly.

## 3. What exists today (prior art)

The parts exist. The whole does not.

- **Skills sync**: people symlink one git repo into each tool's skill directory. Tools such as Skills Board manage skill folders. Codex CLI now imports Cursor skills. These are manual, local, and skill-only.
- **Memory**: Mem0 (OpenMemory) and Letta offer hosted agent memory over MCP. They serve app builders more than tool users. They do not manage skills or keys.
- **Secrets**: 1Password, Doppler, and Infisical hold secrets. 1Password has an MCP server that brokers access. These are general tools. They do not know what an agent loadout is.

No product unifies the three with a sync engine and Linear-grade polish. That is the gap.

## 4. Core concepts

The vault holds **items**. One grammar covers every item:

- **Address**: `kind/name`, kebab-case. Example: `skill/deploy-checks`, `memory/my-stack`. Every verb, every error, and every log line uses the address.
- **Hook**: the one-line description in the frontmatter. Indexes and listings show hooks only. Bodies load on demand.
- **Body**: the content. Its physical form fits the kind.
- **Provenance**: who wrote the item — the human, or an agent (which tool, which session), and when. Loadout records provenance on every write.
- **Review state**: an agent-written item starts as a `draft`. The owner keeps it or drops it. A kept item becomes `kept`. `loadout review` lists the drafts.

The kinds:

- **Skill** (`skill/*`): a folder with a `SKILL.md` file plus resources. Matches the open agent-skills format.
- **Memory** (`memory/*`): one markdown fact with frontmatter.
- **Secret** (`secret/*`, Phase 5): plaintext metadata (name, hook, service), encrypted body. The plain value never enters the vault files.

Around the items:

- **Vault**: the local directory that holds all items, one manifest, one lock, and a git-backed history. It is the source of truth on each device.
- **Adapter**: a pure function from vault state to one tool's expected layout. A **file adapter** serves local tools (Claude Code, Codex CLI, Cursor, hermes, pi, Gemini CLI). A **connector** serves hosted agents (Devin) over their APIs. An adapter returns a typed report — applied, blocked, pruned — never a bare error string.
- **Device**: an enrolled machine with its own key. Devices sync through the cloud (Phase 4).

## 5. The agent is the user

The human owns the vault. The agent drives it. Design every surface for the driver:

1. **Zero-cost first contact.** Projections put knowledge inside each harness's native files. An agent learns its human, its memory, and its skill index with no tool call at all.
2. **Hooks first, bodies on demand.** Context tokens are the scarce resource. Every listing is one line per item. Depth is always one explicit step away.
3. **Every output is a complete picture, and it names the next action.** `status` tells the agent where it stands. `doctor` lists every problem with its exact repair. No output is a fragment.
4. **Every verb is idempotent.** Re-running any command is always safe. An agent never has to reason about state before it acts. `sync --dry-run` shows the full projection plan without writing, so an agent can test its model of the system for free.
5. **No dead-end errors.** Every failure names the file, the cause, and the repair command. An error an agent cannot act on is a bug.
6. **Structured everywhere.** Every verb takes `--json` and emits a stable schema. Ordering is deterministic. Exit codes are API: 0 ok, 1 failure, 2 usage.
7. **The system teaches its own protocol.** Every projected block carries a short protocol footer: how to recall, how to add a fact, the rule to check before writing. Any agent that reads the projection learns how to write back. This closes the accretive loop at every surface, in every harness, with no setup.
8. **Agents write back; the human stays in control.** Writes are cheap, carry provenance, land as drafts, and revert in one command. Accretion without audit is contamination; audit without cheap writes is stagnation. Loadout provides both halves.

## 6. The tower

Loadout is one system, not an assemblage. Each layer builds on the one below, and each layer has exactly one verb that makes it legible:

| Layer | What it is | Legibility verb |
|---|---|---|
| 0. History | git-backed time; every state recoverable | `loadout log`, `loadout undo` |
| 1. Items | the uniform knowledge unit (address, hook, body) | `loadout show <kind/name>` |
| 2. Vault | the agent's persistent self: index, lock, manifest | `loadout context`, `loadout recall <terms>` |
| 3. Projections | presence inside each harness's native surface | `loadout status`, `doctor`, `sync --dry-run` |
| 4. Protocol | the write-back loop: CLI verbs, the projected footer, MCP later | the protocol footer itself |
| 5. Fleet | one self across devices and hosted agents | `loadout devices` (Phase 4+) |

An agent can enter at any layer, ask one question, and get one complete answer. Nothing requires knowledge of a lower layer's internals: history does not require knowing the vault is git; projection repair does not require knowing symlink rules.

## 7. The interface contract

The verbs, complete:

| Verb | Purpose | Layer |
|---|---|---|
| `init` | create the vault | 2 |
| `add skill\|memory <name>` | scaffold an item (records provenance) | 1 |
| `show <kind/name>` | print one item's body | 1 |
| `edit <kind/name>` | open one item in $EDITOR | 1 |
| `list` | all items, one hook per line | 2 |
| `context` | the compact situational picture: owner facts, skill index, recent changes | 2 |
| `recall <terms>` | search hooks and bodies; returns addresses + hooks | 2 |
| `sync [--dry-run]` | project the vault into every enabled tool | 3 |
| `status` | vault counts + per-adapter state | 3 |
| `doctor` | every problem, ranked, each with its repair | 3 |
| `log` | what changed, when, by whom | 0 |
| `undo` | restore the previous vault state | 0 |
| `review` | list agent-written drafts; keep or drop | 1 |

Contract rules, binding on every verb:

- `--json` on everything; stable schemas; deterministic order.
- Idempotent and safe to re-run; mutating verbs take the vault lock (one writer at a time; concurrent agents wait, never corrupt).
- Error grammar: `<address or path>: <what happened>. Fix: <exact command or action>.`
- Compact by default. A full `context` costs one command and stays under about five hundred tokens.
- The cost gradient, cheapest first: projection (free at session start) → `context` (one call) → targeted `show`/`recall` (one call each). An agent should almost never need more.

## 8. Invariants

The system's axioms. An agent can deduce correct behavior from these alone:

1. The vault is the single source of truth. Every projection is derivable from it, always.
2. Loadout writes only inside its own marks and its own symlinks. It never touches user content outside them.
3. Loadout never replaces a real file, a real directory, or a foreign symlink.
4. Every write is atomic. A crash never leaves a torn file.
5. Every vault state is in history. Any change reverts in one command.
6. Any verb can run at any time without breaking the vault.
7. Every error names its repair.
8. The server (Phase 4+) never holds a plaintext byte of user content.
9. Every agent-written item carries provenance and starts as a draft.

## 9. Approach (decided)

Local-first vault with cloud sync. A CLI owns the canonical vault. Adapters project it into each tool. A small cloud service syncs encrypted snapshots between devices. Rejected: MCP-only (most tools need files; fails offline) and git-repo-plus-viewer (unsafe for secrets, no polish). An MCP endpoint arrives in Phase 6 as an additional surface, not a replacement.

## 10. Architecture

```
┌────────────── device ──────────────┐      ┌───── cloud ─────┐
│  agent tools (Claude Code, pi, …)  │      │                 │
│        ▲ files / env / MCP         │      │  API (auth,     │
│  projections (+ protocol footer)   │      │  versions,      │
│        ▲                           │ sync │  devices)       │
│  vault (~/.loadout)  ◄── CLI ──────┼─────►│                 │
│  items · index · lock · history    │ E2E  │  blob store     │
└────────────────────────────────────┘      │  (ciphertext)   │
                                            └─────────────────┘
```

Components, each with one purpose:

1. **CLI (`loadout`)**: all verbs from section 7. The single binary also runs the background sync agent (Phase 4).
2. **Vault**: items, one `loadout.toml` manifest, one lock file, git-backed history.
3. **Adapters**: pure functions from vault state to tool state, returning typed reports. `doctor` verifies every projection.
4. **Sync service** (Phase 4): accounts, device enrollment, versioned encrypted snapshots, last-writer-wins per item with kept history.
5. **Dashboard** (Phase 8): browse, edit, review drafts, audit access. It decrypts in the browser.

## 11. Security model

- Encrypt all content on the device before upload (age/libsodium). The server stores ciphertext only.
- Give each device its own keypair. Enroll a new device with an approval from an old device or a recovery phrase.
- Never write plain secret values into the vault files or into agent context.
- Deliver secrets in two modes. Mode 1: inject as env vars when the tool starts. Mode 2 (later): an MCP broker uses the key server-side and returns only results.
- Log every secret access with device, tool, agent, and time. Provenance is the audit trail for knowledge; the access log is the audit trail for capability.
- **v1 trust boundary (self-host).** A snapshot is encrypted. A snapshot is not signed. Any device with the bearer token can read the enrolled recipients from `GET /v1/devices`. That device can encrypt a new snapshot to those recipients and push it. Enrolled devices merge the snapshot. They do not check its real author.
- This includes the operator of a self-hosted server. In self-host v1, the token holder is trusted as the vault owner.
- The hosted service (decision 12) must add per-device snapshot signing before it ships. It must reject a merge when the snapshot's signer is not in `devices.toml`. This is a wire-protocol change, not a client-only fix.

## 12. Phases

Each phase ships a complete, usable product. Do not start a phase before the prior phase works perfectly.

**Phase 1 — Local vault + first adapters. SHIPPED 2026-08-31.**
The CLI (init, add, sync, status, doctor), the vault format, git history, and adapters for Claude Code, pi, and generic `AGENTS.md`. One edit in the vault reaches both tools.

**Phase 2 — The agent interface.**
Make the system fully agent-native: `--json` everywhere; `sync --dry-run`; `context`, `show`, `list`, `recall`, `log`, `undo`, `edit`; the vault lock; provenance and review states with `review`; the protocol footer in every projected block; adapters return typed reports (applied, blocked, pruned). Success: an agent that has never seen Loadout learns the write-back protocol from a projected block alone, and drives the whole system without one dead end.

**Phase 3 — Adapter coverage.**
Cover the major local agents: Codex CLI, Cursor, hermes, and Gemini CLI, on the typed-report contract. The adapter kit must make a new adapter cost under one day. Success: every local agent on the machine reads the same vault.

**Phase 4 — Cloud sync.**
Accounts, device keys, encrypted snapshots, the sync agent. Success: a change on the Mac appears on the Pi within seconds. No plaintext ever leaves a device.

**Phase 5 — Secrets done right.**
The encrypted secret store as `secret/*` items, env injection, access logs, rotation reminders. Success: remove all plaintext keys from disk and from repo configs.

**Phase 6 — MCP endpoint.**
Serve `context`, `recall`, and brokered secret use over MCP. Success: an agent recalls a fact and calls an API without ever holding the key.

**Phase 7 — Connectors for hosted agents.**
Push items to hosted agents such as Devin over their APIs. Success: a vault edit reaches a hosted agent with no manual step.

**Phase 8 — Dashboard.**
The Linear-grade web app with in-browser decryption. Success: full browse, edit, review, and audit from the browser.

## 13. Recommended stack

- CLI and sync agent: Go. One static binary, low memory use, easy install.
- Sync server: one self-hostable Go binary (`loadoutd`). In self-host mode it stores encrypted blobs on the filesystem with a small local index. The hosted service later runs the same API on Postgres plus S3-compatible storage.
- Dashboard: Next.js, deployed on Vercel.
- Crypto: age (X25519) for device identity and blob encryption. Do not invent crypto.

## 14. Success criteria

- Setup on a new machine takes under five minutes.
- A vault edit reaches every tool on every device in under ten seconds.
- The server never holds a plaintext byte of user content.
- The user deletes their manual symlink scripts and copy-paste habits.
- An agent with no prior knowledge of Loadout learns the protocol from one projected block and contributes its first fact correctly.
- Every verb re-runs safely; every error names its repair; a full situational picture costs one command.

## 15. Assumptions

1. The first users are you and your own setup: Claude Code, pi, the Mac, and the Raspberry Pi.
2. v1 is single-user (one human owner; many agents). Teams come later.
3. File-based delivery is the right compatibility bet for 2026 agent tools.

## 16. Decisions

1. **Name**: Loadout.
2. **Positioning**: a public product from day one. Dogfood it on your own setup.
3. **Source model**: open source the CLI, the vault format, and the adapters. Charge for the hosted sync service.
4. **Memory scope**: curated facts only in v1. No transcripts.
5. **Agent coverage**: all major agents. File adapters for Claude Code, Codex CLI, Cursor, hermes, pi, and Gemini CLI. Connectors for hosted agents such as Devin.
6. **(v3) The agent is the primary user.** Every surface is designed for the driving agent; the human is the owner and auditor.
7. **(v3) One item grammar.** Skills, memory, and secrets share the address, hook, provenance, and review-state model. Physical form stays kind-appropriate.
8. **(v3) The projection teaches the protocol.** Every projected block carries the write-back footer.
9. **(v3) Typed adapter reports.** `Apply` returns applied/blocked/pruned; bare error strings are not a contract. Lands in Phase 2, before the adapter wave copies the old shape.
10. **(v3) Agent interface before adapter wave.** Phase 2 reorders ahead of coverage: the grammar must be right before six more adapters and a cloud service inherit it.
11. **(v3.1) Self-host first.** Phase 4 ships `loadoutd` as a self-hostable binary and dogfoods Mac-to-Pi sync over Tailscale. Product hosting is a launch decision, not a build decision.
12. **(v3.1) Lean self-host storage.** v1 `loadoutd` stores encrypted blobs as files with a small local index. Postgres and S3 arrive with the hosted service, behind the same API.
13. **(v3.1) The manifest splits.** Adapter paths, enabled flags, and device keys are device-local and never sync. Skills, memory, and the device roster sync.
