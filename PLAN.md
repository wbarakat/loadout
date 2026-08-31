# Loadout — Product Plan

Status: draft for review. Date: 2026-08-30.
"Loadout" is a working name. See the open questions.

## 1. Vision

Loadout is one secure cloud account for your agent gear. It stores your skills, your API keys, and your memory. It syncs them to every agent tool on every machine.

You edit a skill once. Every tool sees the change. You add a key once. Every agent can use it, but no agent can read it. Your memory follows you from Claude Code to pi to your next tool.

## 2. Inspiration

Take these qualities from Linear and Resend:

- **From Linear**: a local-first sync engine. The app feels instant because data lives on the device. The cloud is for sync, not for reads. Also: opinionated design, few options, high polish.
- **From Resend**: a developer-first surface. One clean CLI and one clean API. Setup takes five minutes. The docs read like a product.

The product rule: each feature must be simple, complete, and correct. Ship fewer features that work perfectly.

## 3. What exists today (prior art)

The parts exist. The whole does not.

- **Skills sync**: people symlink one git repo into each tool's skill directory. Tools such as Skills Board manage skill folders. Codex CLI now imports Cursor skills. These are manual, local, and skill-only.
- **Memory**: Mem0 (OpenMemory) and Letta offer hosted agent memory over MCP. They serve app builders more than tool users. They do not manage skills or keys.
- **Secrets**: 1Password, Doppler, and Infisical hold secrets. 1Password has an MCP server that brokers access. These are general tools. They do not know what an agent loadout is.

No product unifies the three with a sync engine and Linear-grade polish. That is the gap.

## 4. Core concepts

- **Vault**: the local directory that holds all content. It is the source of truth on each device. Format: plain files and folders, one manifest.
- **Skill**: a folder with a `SKILL.md` file plus resources. This matches the open agent-skills format.
- **Secret**: a named, encrypted value with metadata (service, scopes, notes). The plain value never enters the vault files.
- **Memory**: small markdown facts with frontmatter (type, description, links). This matches the flat-file memory pattern.
- **Adapter**: a projection of the vault into one tool's expected layout. Example: symlink skills into `~/.claude/skills`, render memory into `MEMORY.md`, inject secrets as env vars.
- **Device**: an enrolled machine with its own key. Devices sync through the cloud.

## 5. Approach options

### Option A — Local-first vault with cloud sync (recommended)

A CLI owns a canonical vault directory. Adapters project the vault into each tool's format. A small cloud service syncs encrypted snapshots between devices.

- Pro: works with every tool today, because every tool reads files.
- Pro: works offline. Sync is a background concern, like Linear.
- Pro: the MVP is useful on one machine with zero cloud code.
- Con: you must write and maintain one adapter per tool.

### Option B — MCP-first service

Everything lives in the cloud. Tools reach it live over MCP. No files on disk.

- Pro: no sync problem and no adapters.
- Con: skills and instructions must exist as files for most tools. MCP alone cannot deliver them.
- Con: no offline use. Every session depends on the network.

### Option C — Git repo plus hosted UI

A dotfiles-style git repo with a thin web viewer.

- Pro: leanest possible build.
- Con: git is a poor and unsafe home for secrets.
- Con: manual pulls, merge conflicts, no polish. This is the status quo with a coat of paint.

**Decision: Option A.** Add an MCP endpoint in a later phase for runtime memory recall and brokered secrets. This gives Option B's benefits without its limits.

## 6. Architecture (v1)

```
┌────────────── device ──────────────┐      ┌───── cloud ─────┐
│  agent tools (Claude Code, pi, …)  │      │                 │
│        ▲ files / env / MCP         │      │  API (auth,     │
│  adapters                          │      │  versions,      │
│        ▲                           │ sync │  devices)       │
│  vault (~/.loadout)  ◄── CLI ──────┼─────►│                 │
│  device key                        │ E2E  │  blob store     │
└────────────────────────────────────┘      │  (ciphertext)   │
                                            └─────────────────┘
```

Components, each with one purpose:

1. **CLI (`loadout`)**: init, add, edit, sync, status, doctor. The single binary also runs the background sync agent.
2. **Vault**: plain files, one `loadout.toml` manifest, content-addressed history for undo.
3. **Adapters**: pure functions from vault state to tool state. Each adapter declares its target paths. `loadout doctor` verifies the projections.
4. **Sync service**: accounts, device enrollment, versioned encrypted snapshots, one conflict rule (last-writer-wins per file, with kept history).
5. **Dashboard**: a web app to browse, edit, and audit. It decrypts in the browser.

## 7. Security model

- Encrypt all content on the device before upload (age/libsodium). The server stores ciphertext only.
- Give each device its own keypair. Enroll a new device with an approval from an old device or a recovery phrase.
- Never write plain secret values into the vault files or into agent context.
- Deliver secrets in two modes. Mode 1: inject as env vars when the tool starts. Mode 2 (later): an MCP broker uses the key server-side and returns only results.
- Log every secret access with device, tool, and time.

## 8. Phases

Each phase ships a complete, usable product. Do not start a phase before the prior phase works perfectly.

**Phase 1 — Local vault + adapters (no cloud).**
Build the CLI, the vault format, and two adapters: Claude Code and pi. Include a generic `AGENTS.md` adapter. Success: one edit in the vault appears in both tools. Dogfood on this Mac.

**Phase 2 — Cloud sync.**
Build accounts, device keys, encrypted snapshots, and the sync agent. Success: a change on the Mac appears on the Pi within seconds. No plaintext ever leaves a device.

**Phase 3 — Secrets done right.**
Add the encrypted secret store, env injection, access logs, and rotation reminders. Success: remove all plaintext keys from disk and from repo configs.

**Phase 4 — MCP endpoint.**
Serve memory recall and brokered secret use over MCP. Success: an agent recalls a fact and calls an API without ever holding the key.

**Phase 5 — Dashboard.**
Build the Linear-grade web app with in-browser decryption. Success: full browse, edit, and audit from the browser.

Out of scope for v1: teams, sharing, a public skill marketplace, mobile apps, billing. Add them only after v1 is perfect for one person.

## 9. Recommended stack

- CLI and sync agent: Go. One static binary, low memory use, easy install.
- API: a small service on Postgres plus S3-compatible blob storage.
- Dashboard: Next.js, deployed on Vercel.
- Crypto: age for file encryption, libsodium for device keys. Do not invent crypto.

## 10. Success criteria

- Setup on a new machine takes under five minutes.
- A vault edit reaches every tool on every device in under ten seconds.
- The server never holds a plaintext byte of user content.
- The user deletes their manual symlink scripts and copy-paste habits.

## 11. Assumptions (made without you; correct me)

1. You build this as a real product, not a personal script. The Linear/Resend framing implies that.
2. The first users are you and your own setup: Claude Code, pi, the Mac, and the Raspberry Pi.
3. v1 is single-user. Teams come later.
4. File-based delivery is the right compatibility bet for 2026 agent tools.

## 12. Open questions

1. The name. "Loadout" is a placeholder. Other candidates: Satchel, Kitbag.
2. Product or personal tool first? The phases work for both, but the cloud service only pays off as a product.
3. Which third adapter matters most after Claude Code and pi? Codex, Cursor, or the generic `AGENTS.md` path?
4. Should memory sync include session transcripts, or curated facts only? The plan assumes curated facts only.
