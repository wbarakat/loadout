# 🎒 Loadout

Loadout is a local-first vault for your agent setup. It keeps your
skills, memory, and secrets in one place (`~/.loadout`) and projects
them into every agent tool you use: Claude Code, Codex, Cursor, Hermes,
pi, Gemini CLI, and Droid. You edit a skill once and every tool sees
the change. You add a key once and every agent can use it, but no agent
ever reads its value. Loadout syncs the vault between your devices with
end-to-end encryption through a small server you host yourself,
`loadoutd`.

## 🤖 Install with your agent

Loadout is built for agents. Paste this to Claude Code, Codex, Cursor,
or any tool that runs shell commands:

```
Install and set up Loadout for me. Read the guide at
https://github.com/wbarakat/loadout/blob/main/AGENTS.md, install the
loadout CLI, then run `loadout init --yes` to detect my tools and
import my existing skills and memory as drafts. Show me the summary.
```

The agent installs the CLI, creates the vault, and imports your current
skills and memory. You review the drafts and keep the ones you want.

## ⚡ Quickstart

```
loadout init
loadout review
loadout sync --remote
```

- `loadout init` runs the first-run wizard. It finds the agent tools on
  your machine, creates the vault, enables an adapter for each tool,
  and imports your existing skills and memory as drafts. Every prompt
  has a safe default, so you can press Enter through it.
- `loadout review` lists the drafts. Keep one with
  `loadout review keep <kind>/<name>` or drop it with
  `loadout review drop <kind>/<name>`. After a first import there are
  usually many, so both take several names at once, or `--all`:
  `loadout review keep --all --by import:claude-code`. Add `--dry-run`
  to see what a command would do first. The whole batch is one step, so
  `loadout undo` reverses it.
- `loadout sync --remote` projects the vault into every enabled tool,
  then pushes it to your remote if you set one.

For an unattended install, run `loadout init --yes`. See `AGENTS.md`
for the full headless flow.

## 📥 Install

Download the latest binary for your system:

```
curl -fsSL https://raw.githubusercontent.com/wbarakat/loadout/main/install.sh | sh
```

This installs `loadout` (the CLI) and `loadoutd` (the sync server).
Prebuilt binaries for macOS and Linux are also on the
[Releases](https://github.com/wbarakat/loadout/releases) page.

To build from source, use any Go 1.23 or newer toolchain:

```
git clone https://github.com/wbarakat/loadout
cd loadout
go install ./cmd/loadout ./cmd/loadoutd
```

## 🧰 What is in the vault

- **Skills** (`skill/<name>`): a folder with a `SKILL.md` file, in the
  open agent-skills format. Loadout links it into every enabled tool.
- **Memory** (`memory/<name>`): one markdown fact with a one-line hook
  and a body. Loadout writes it into each tool's instructions file.
- **Secrets** (`secret/<name>`): an API key or other credential.
  Loadout encrypts the value on disk. An agent can use it without
  reading it.

Every item records who wrote it and when. An item an agent writes
starts as a draft; you keep or drop it with `loadout review`. An item
you write is already kept.

## 🧭 Commands

Run `loadout help` for the full list with every flag. The commands you
use most:

| Command | Purpose |
|---|---|
| `loadout init` | First-run wizard. Add `--yes` to run it headless. |
| `loadout import [SOURCE...]` | Pull skills and memory from installed tools, as drafts. |
| `loadout review` | List drafts. `keep` or `drop` each one. |
| `loadout sync [--remote]` | Project the vault into every tool. `--remote` also syncs the server. |
| `loadout recall <term>...` | Search skills and memory. |
| `loadout add skill\|memory <name>` | Scaffold a new item. |
| `loadout secret add <name> --service <svc>` | Add a secret. Pipe the value on stdin. |
| `loadout run --secret <name> -- <cmd>` | Run a command with secrets injected as environment variables. |
| `loadout status` / `loadout doctor` | Show sync state, or list every problem with its fix. |
| `loadout mcp` | Serve the vault to an agent over MCP on stdio. |

Every verb takes `--json` for scriptable output.

## 🔐 Security

- **Local-first.** The vault at `~/.loadout` is the source of truth.
  Every projection into a tool comes from it.
- **Encrypt before upload.** Loadout encrypts each snapshot with age
  (X25519) on your device before it leaves. The server stores only
  ciphertext.
- **Per-device keys.** Each device has its own keypair. A new device
  runs `loadout join` and waits for an approved device to run
  `loadout devices approve`.
- **No-secrets devices.** Approve a device with `--no-secrets` and it
  syncs every skill and memory fact, but no secret encrypts to its key.
  The browser dashboard uses this: it cannot decrypt a secret at all.
- **Secrets stay out of agent context.** A secret value exists only as
  ciphertext at rest, as an environment variable in a child process
  from `loadout run`, or on stdout under `loadout secret show --reveal`.
  It never enters a vault file, a projection, a log, or `--json`
  output.

The self-hosted server signs nothing in v1. Any device with the
server's bearer token can push a snapshot, and an enrolled device
merges it. Treat the token as the credential and host only on
infrastructure you trust.

## ✨ Features

- **Dashboard.** A browser tab that shows your skills and memory,
  served as a static site with no backend. It enrolls as a no-secrets
  device and talks to your own `loadoutd`. A secret shows only its
  metadata, never its value.
- **MCP endpoint.** `loadout mcp` gives an agent five read-only tools
  (`context`, `recall`, `show`, `list`, `list_secrets`) that never
  return a secret value, plus one brokered tool, `http_request`. The
  agent writes `{{secret:<name>}}` in a header or body, and Loadout
  puts the real value into the outbound request. Each secret lists its
  `allowed_hosts`, and Loadout refuses to send it anywhere else.
- **Device roles.** Every device is `full` or `no-secrets`. Change the
  role by re-approving the device.
- **Import.** `loadout import` reads skills and memory from seven tools
  (`claude-code`, `codex`, `cursor`, `hermes`, `pi`, `gemini`,
  `droid`). Everything lands as a draft, deduplicated against the
  vault. Add `--dry-run` to preview, or `--verbose` for the full list.
  Two limits: Devin keeps its content in its own cloud, and Cursor
  keeps global User Rules in an internal store, so Loadout cannot read
  either from your machine.

## 🖥️ Self-host loadoutd

`loadoutd` stores encrypted blobs on disk and never reads them:

```
loadoutd -data /var/lib/loadout
```

On its first run, it prints an access token once. Copy it now, because
it never prints again.

| Flag | Purpose |
|---|---|
| `-data <dir>` | The data directory. Required. |
| `-addr <addr>` | The listen address. Default `:7777`. |
| `-cors-origin <origin>` | The browser origin allowed over CORS. Needed only for the dashboard. |

Connect a vault to it:

```
loadout remote add https://loadoutd.example <token>
loadout sync --remote
```

A browser page served over HTTPS cannot call a plain `http://` address.
Put an HTTPS URL in front of `loadoutd` for the dashboard.

## 📖 More

- `AGENTS.md`: how an agent installs and drives Loadout on its own.
- `docs/`: guides for install, import, secrets, MCP, the dashboard,
  self-host, and device roles.
- `PLAN.md`: the product plan and roadmap.
