# Loadout

Loadout is a local-first vault for your agent gear. It stores your
skills, your memory, and your secrets in one place: `~/.loadout`. It
projects them into every agent tool you use — Claude Code, Codex,
Cursor, hermes, pi, Gemini CLI — so each tool sees the same skills and
the same memory. It syncs the vault, end-to-end encrypted, between
your devices through a small, self-hostable server called `loadoutd`.
You edit a skill once. Every tool sees the change. You add a key once.
Every agent can use it, but no agent ever reads its value.

## 60-second quickstart

```
go install ./cmd/loadout
loadout init
loadout review
loadout sync --remote
```

`loadout init` runs a first-run wizard. It detects the agent tools on
your machine, creates the vault, enables an adapter for each tool it
finds, and offers to import your existing skills and memory as
**drafts**. It can also connect a self-hosted `loadoutd` remote, if
you have one. Every prompt has a safe default, so you can just press
Enter through it.

`loadout review` lists every draft the wizard imported. Keep the ones
you want with `loadout review keep <kind>/<name>`, or drop the rest
with `loadout review drop <kind>/<name>`.

`loadout sync --remote` projects the vault into every enabled tool,
then pushes it to your remote, if you configured one.

An agent can run this whole flow unattended. See `AGENTS.md` for the
headless path.

## What's in the vault

- **Skills** (`skill/<name>`) — a folder with a `SKILL.md` file, in the
  open agent-skills format. Loadout links it into every enabled tool.
- **Memory** (`memory/<name>`) — one markdown fact, with a short
  one-line hook and a body. Loadout projects it into each tool's own
  instructions file.
- **Secrets** (`secret/<name>`) — an API key or another credential.
  Loadout encrypts its value on disk. An agent can use a secret
  without ever reading it.

Every item carries **provenance**: who wrote it, and when. An
agent-written item starts as a **draft**; you keep it or drop it with
`loadout review`. A human-written item is already **kept**.

## The command surface

Run `loadout help` at any time to print this from the binary itself.
Every verb takes `--json` for a stable, scriptable output; two
exceptions are `edit` and `run`, which have no JSON shape of their
own.

### Items

| Command | Purpose |
|---|---|
| `loadout add skill <name> [--by <who>]` | Scaffold a new skill. |
| `loadout add memory <name> [--by <who>]` | Scaffold a new memory fact. |
| `loadout show <kind>/<name>` | Print one item's raw file. |
| `loadout list` | Print every item, one hook line each. |
| `loadout recall <term>...` | Search hooks and bodies for items that match every term. |
| `loadout edit <kind>/<name>` | Open one item in `$EDITOR`. |
| `loadout context` | Print the compact picture of the vault: counts, every hook, recent history. |

### Review

| Command | Purpose |
|---|---|
| `loadout review` | List every draft item awaiting your decision. |
| `loadout review keep <kind>/<name>` | Mark a draft item kept. |
| `loadout review drop <kind>/<name>` | Delete a draft item. |

### Secrets

| Command | Purpose |
|---|---|
| `loadout secret add <name> --service <svc> [--hook <text>] [--rotate-after <dur>] [--by <who>] [--allowed-hosts <h1,h2>]` | Add a secret. Pipe the value on stdin. |
| `loadout secret list [--json]` | Show every secret's metadata. Never a value. |
| `loadout secret show <name> [--reveal] [--by <who>]` | Refuse to print the value, unless you pass `--reveal`. |
| `loadout secret rotate <name> [--by <who>] [--allowed-hosts <h1,h2>]` | Replace a secret's value. Pipe the new value on stdin. |
| `loadout secret rm <name>` | Remove a secret. |
| `loadout run --secret <name>[=ENVVAR] [--secret <name2>...] [--by <who>] -- <cmd> [args...]` | Decrypt secrets, inject them into a child process, then run it. |

### Import

| Command | Purpose |
|---|---|
| `loadout import [SOURCE...] [--skills] [--memory] [--project DIR] [--project-memory] [--dry-run]` | Pull skills and memory from installed agent tools into the vault, as drafts. |

`SOURCE` is one of: `claude-code`, `codex`, `cursor`, `hermes`, `pi`,
`gemini`, `droid`. With no `SOURCE`, Loadout scans every tool it can
find. `--project-memory` also pulls per-project or per-profile memory
for `--project DIR`, or the current directory; without it, memory
import is scoped to global instruction files only.

Two tools have a stated limit. Devin is a hosted agent: its skills and
memory live in Devin's own cloud, so Loadout cannot import them from
your machine. Cursor keeps its global User Rules in an internal
database with no stable format, so Loadout cannot import them either;
copy them by hand from Cursor's own Settings.

### Sync and diagnostics

| Command | Purpose |
|---|---|
| `loadout sync [--dry-run] [--remote]` | Project the vault into every enabled tool. `--remote` also syncs with the configured remote. |
| `loadout watch [--interval <dur>]` | Run `sync --remote` in a loop until Ctrl-C. Default interval: 10s. |
| `loadout status` | Print vault counts and each adapter's sync state. |
| `loadout doctor` | List every problem, each with its exact fix. |
| `loadout log` | Print the vault history, newest first. |
| `loadout undo` | Restore the vault to the state before its last change. |

### Devices and remotes

| Command | Purpose |
|---|---|
| `loadout device` | Show this device's name and its age recipient key. |
| `loadout remote` | Show the configured remote and the last synced version. |
| `loadout remote add <url> <token>` | Configure the remote this vault syncs with. |
| `loadout join <url> <token>` | Enroll this device with a remote. It waits for approval. |
| `loadout devices [--json]` | List every device: approved, waiting, or re-keyed, with its role. |
| `loadout devices approve <name> [--no-secrets]` | Approve a waiting device. Add `--no-secrets` for a device that must never read a secret. |
| `loadout devices approve <name> --rotate <recipient> [--no-secrets\|--full]` | Trust an out-of-band-verified new key for an already-approved device. |

### Setup and MCP

| Command | Purpose |
|---|---|
| `loadout init [--yes] [--tools a,b,...] [--no-import] [--remote URL --token-file PATH] [--project-memory]` | The first-run wizard, or its headless form. See below. |
| `loadout mcp` | Serve the vault over MCP as JSON-RPC on stdio. |

## The security model

- **Local-first.** The vault at `~/.loadout` is the source of truth on
  each device. Every projection into a tool is derivable from it.
- **Encrypt before upload.** Loadout encrypts every snapshot on your
  device with age (X25519) before it ever leaves. The remote server
  stores only ciphertext; it never holds a plaintext byte of your
  content.
- **Per-device keys, with approval.** Each device has its own keypair.
  A new device enrolls with `loadout join` and waits until an
  already-trusted device runs `loadout devices approve`.
- **A no-secrets device.** A device can be approved with `--no-secrets`.
  It syncs the whole vault — every skill, every memory fact — but a
  secret never encrypts to its key. This is what the browser dashboard
  uses: it can browse and edit your skills and memory, but it
  structurally cannot decrypt a secret, however it is used.
- **Secrets never enter agent context.** A secret's value lives only
  as ciphertext at rest, as an environment variable in a child process
  spawned by `loadout run`, or on stdout under an explicit
  `loadout secret show --reveal`. It never enters a plaintext vault
  file, a projection, an error message, a log line, or `--json`
  output.
- **The self-host v1 trust boundary.** A snapshot is encrypted, but it
  is not signed. Any device holding the remote's bearer token can read
  the enrolled recipients and push a new snapshot; an enrolled device
  merges it without checking who really sent it. In self-host v1, the
  holder of the token is trusted as the vault's owner. Per-device
  snapshot signing is a planned improvement for a hosted service, not
  yet built.

## Features

### The dashboard

A browser tab that shows your skills and your memory, served as a
static site with no server of its own. It enrolls as a no-secrets
device and talks straight to your own `loadoutd`; no third party ever
touches your vault, your key, or your token. It browses, searches,
edits, and reviews drafts, the same as the CLI. A secret shows only
its metadata — its name, its service, its rotation setting — never
its value.

### The MCP endpoint

`loadout mcp` serves the Model Context Protocol as JSON-RPC on stdio:
an agent tool spawns it locally, with no network listener. It exposes
five read-only tools — `context`, `recall`, `show`, `list`,
`list_secrets` — none of which can ever return a secret's value. It
also exposes one brokered tool, `http_request`: the agent writes
`{{secret:<name>}}` in a request header or body, and Loadout
substitutes the real value into the outbound request server-side. The
agent never sees it. Each secret declares `allowed_hosts`; Loadout
refuses to send a secret to any host not on that list.

### Device roles

Every device has a role: `full` or `no-secrets`. A full device can
read every secret. A no-secrets device receives the same skills and
memory, but no secret ever encrypts to its key — not a design
convention, a property of what gets encrypted to it. Change a
device's role by re-approving it with or without `--no-secrets`.

### Import

`loadout import` pulls skills and memory from seven agent tools
(`claude-code`, `codex`, `cursor`, `hermes`, `pi`, `gemini`, `droid`)
into the vault. Every import lands as a **draft**, deduplicated by
name and content against what the vault already holds, never silently
authoritative. Run `loadout import --dry-run` first to preview what
would import, with nothing written.

## Install and build

Loadout is written in Go. Clone the repository, then build or install
the two binaries:

```
go install ./cmd/loadout ./cmd/loadoutd
```

This installs `loadout` (the CLI) and `loadoutd` (the sync server)
into your Go bin directory. To build them as local files instead:

```
go build -o loadout ./cmd/loadout
go build -o loadoutd ./cmd/loadoutd
```

### Self-host `loadoutd`

`loadoutd` is a small, self-hostable server. It stores encrypted blobs
on disk and never parses them:

```
./loadoutd -data /var/lib/loadout
```

On its first run, it prints an access token once. Copy it now; it
never prints again. Flags:

| Flag | Purpose |
|---|---|
| `-data <dir>` | The server's data directory. Required. |
| `-addr <addr>` | The address to listen on. Default `:7777`. |
| `-cors-origin <origin>` | The browser origin allowed to call this server over CORS. Needed only for the dashboard. Default: off. |

Connect a vault to it:

```
loadout remote add https://loadoutd.example <token>
loadout sync --remote
```

A browser page served over HTTPS cannot call a plain `http://`
address — the browser's own mixed-content rule. Put an HTTPS URL in
front of `loadoutd` if you plan to use the dashboard.

## Limitations / not yet

- **Devin** is a hosted agent. Loadout cannot import its skills or
  memory from your machine; they live in Devin's own cloud.
- **Cursor global User Rules** live in an undocumented internal
  database. Loadout cannot import them; copy them by hand.
- **Self-host snapshot signing is deferred.** A self-hosted `loadoutd`
  accepts any snapshot pushed with a valid bearer token; it does not
  yet verify who signed it. Treat the token as the real credential,
  and self-host only on infrastructure you trust.
- **The secrets access log is device-local.** It records every
  `secret show --reveal`, every rotation, and every `loadout run` call
  on the device where it happened. It does not sync, and a dashboard
  decrypt (a no-secrets device can never do this) would need
  server-side logging to appear the same way — not yet built.
- **Windows is unverified.** Loadout's paths and adapters are built
  and tested on macOS and Linux. Windows support has not been checked.

See `PLAN.md` for the full product plan and roadmap.
