# Loadout

One secure home for your agent gear. Store your skills and your memory
in one vault. Sync them to every agent tool.

Phase 2 adds the full agent interface: typed reports, the vault lock,
provenance, review, and `--json` on every verb. Phase 3 adds four
adapters: codex, gemini, cursor, and hermes. The Adapters section
below covers all six local tools, plus a generic AGENTS.md adapter.
Phase 4 adds cloud sync: `loadoutd`, device enrollment, and `loadout
watch`. See the "Sync across your machines" section below. Phase 5
adds encrypted secrets: `secret add`, `secret show --reveal`, `secret
rotate`, and `loadout run` to inject a secret into a child process.
See the "Secrets" section below. Phase 6 adds `loadout mcp`: a Model
Context Protocol server, so an agent tool can read the vault and use a
secret through a broker, without ever holding the secret's value. See
the "MCP" section below. See PLAN.md for the roadmap.

## Install

    go build -o loadout ./cmd/loadout
    mv loadout /usr/local/bin/

## Quickstart

    loadout init                    # create the vault at ~/.loadout
    loadout add skill deploy-checks # scaffold a skill
    loadout add memory my-stack     # scaffold a memory fact
    loadout sync                    # project into Claude Code, pi, ...
    loadout status                  # see what is where
    loadout doctor                  # find problems, with the fix for each

Edit the files in ~/.loadout with any editor. Skills reach the tools
through symlinks, so a skill edit is live at once. After a memory edit,
run "loadout sync" again.

## Verbs

Every verb takes `--json`. Every verb is safe to re-run. This table
lists every verb Loadout supports today.

| Verb | Purpose |
|---|---|
| `init` | Create the vault. |
| `add skill\|memory <name> [--by <who>]` | Scaffold a skill or a memory fact. Records who wrote it. |
| `show <kind>/<name>` | Print one item's raw file content. |
| `edit <kind>/<name>` | Open one item in `$EDITOR` (falls back to `vi`). |
| `list` | Print every item, one hook line each. |
| `context` | Print the compact picture of the vault: counts, every hook, recent history. |
| `recall <term>...` | Search hooks and bodies for items that match every term. |
| `sync [--dry-run] [--remote]` | Project the vault into every enabled tool. `--remote` also syncs with the configured remote. |
| `watch [--interval <duration>]` | Run `sync --remote` in a loop until Ctrl-C. Default interval: 10s. |
| `status` | Print vault counts and each adapter's sync state. |
| `doctor` | List every problem, each with its exact fix. |
| `log` | Print the vault history, newest first. |
| `undo` | Restore the vault to the state before its last change. |
| `review` | List draft items — items an agent wrote — that await your decision. |
| `review keep <kind>/<name>` | Mark a draft item kept. |
| `review drop <kind>/<name>` | Delete a draft item. |
| `remote` | Show the configured remote and the last synced version. |
| `remote add <url> <token>` | Configure the remote this vault syncs with. |
| `join <url> <token>` | Enroll this device with a remote. It waits for approval. |
| `devices [--json]` | Show every device: approved, waiting, or re-keyed, with its role. |
| `devices approve <name> [--no-secrets]` | Approve a waiting device. Add `--no-secrets` for a device that must never read a secret. |
| `devices approve <name> --rotate <recipient> [--no-secrets\|--full]` | Trust a verified new key for an already-approved device. Add `--no-secrets` or `--full` to change its role too. |
| `secret add <name> --service <svc> [--hook <text>] [--rotate-after <dur>] [--by <who>]` | Add a secret. Pipe the value on stdin. |
| `secret list [--json]` | Show every secret's metadata. Never shows a value. |
| `secret show <name> [--reveal] [--by <who>]` | Refuse to print the value by default. Print it only with `--reveal`. |
| `secret rotate <name> [--by <who>]` | Replace a secret's value. Pipe the new value on stdin. |
| `secret rm <name>` | Remove a secret. |
| `run --secret <name>[=ENVVAR] [--secret <name2>...] [--by <who>] -- <cmd> [args...]` | Decrypt secrets and inject them into a child process, then run it. |
| `mcp` | Serve the Model Context Protocol on stdin/stdout, for an agent tool to connect to. |

Run `loadout help` at any time to print this list from the binary
itself.

### `--json`

Add `--json` to any verb. Loadout then prints one JSON object to
stdout instead of text, with a stable schema and a deterministic
field order. Nothing else changes: exit codes stay the same, and
warnings still print to stderr as text. Two verbs are exceptions.
`edit` opens an interactive editor, so it has no JSON output: pass
`--json` to `edit` and Loadout exits 2 with a fixed error instead of
opening anything. `help` always prints the usage text as plain text,
even with `--json`, since the usage text has no JSON shape to hold.

### `--dry-run`

Add `--dry-run` to `sync`. Loadout then walks every adapter and reports
the full projection plan — what it would link, prune, or block — and
writes nothing to disk. Use it to check the vault's state before you
commit to a real sync. A dry run takes the same vault lock as a real
sync, so the two never race each other; it only fails when a projected
file is damaged in a way sync itself could not fix, and never fails on
a blocked path.

### `--by`

Add `--by <who>` to `add`. It names who wrote the item: a human, or an
agent tool such as `claude-code` or `pi`. Loadout records this on the
item itself, along with the time. Omit `--by` and Loadout assumes a
human wrote the item, and marks it kept at once. Any other `--by` value
marks the item a draft, so a human reviews it before it counts as
final.

## The review flow

An agent can add a skill or a memory fact straight to the vault. To
keep the human in control, an agent-written item starts as a **draft**.
A human-written item is already **kept**.

- `loadout review` lists every draft, with who wrote it and when.
- `loadout review keep <kind>/<name>` marks a draft item kept.
- `loadout review drop <kind>/<name>` deletes a draft item for good.

Run `loadout sync` after a `keep` or a `drop`, so every projection
reflects the decision.

## Doctor and the orphan scan

`loadout doctor` checks every adapter's projection against the vault
and reports each mismatch, with its exact fix. This covers a missing
link, a stale link, a stale memory block, and a path a real file or a
foreign symlink blocks.

It also runs an orphan scan: it looks for a Loadout-owned symlink in
each adapter's skills directory that no current skill explains — for
example, a link left behind after you delete a skill from the vault.
Run `loadout sync` to prune it. Doctor never flags a real file or a
symlink you made yourself; those are yours to keep.

## Adapters

Loadout projects skills and memory into six agent tools, plus any
AGENTS.md file you name. Every adapter links skills as symlinks, so a
skill edit is live at once. Each adapter writes memory in one of three
modes:

- **import line** — writes one line into the memory file. The line
  points at a rendered file that holds the full memory content. This
  keeps the memory file short.
- **memory block** — writes the full rendered memory straight into
  the memory file, inside loadout marks.
- **skills only** — links skills but writes no memory file, since the
  tool has no stable shared-instructions file today.

| Adapter | Memory mode | Default skills dir | Default memory file | Enabled by default |
|---|---|---|---|---|
| `claude-code` | import line | `~/.claude/skills` | `~/.claude/CLAUDE.md` | Yes |
| `pi` | memory block | `~/.pi/agent/skills` | `~/.pi/agent/AGENTS.md` | Yes |
| `codex` | memory block | `~/.codex/skills` | `~/.codex/AGENTS.md` | No |
| `gemini` | memory block | `~/.gemini/skills` | `~/.gemini/GEMINI.md` | No |
| `cursor` | skills only | `~/.cursor/skills` | none | No |
| `hermes` | skills only | `~/.hermes/skills` | none | No |

Run `loadout sync` while an adapter is still enabled, before you
disable it. A disabled adapter's links stay in place, and loadout no
longer watches them.

`codex`, `gemini`, `cursor`, and `hermes` are off by default. Init
already wrote a stanza for each one in loadout.toml, with the default
paths above filled in. To turn one on, open loadout.toml and edit the
existing stanza, for example:

    [adapters.codex]
    enabled = true
    skills_dir = "~/.codex/skills"
    memory_file = "~/.codex/AGENTS.md"

Set "enabled" to true. Do not add a second `[adapters.codex]` section.
Edit the section that is already there. Run "loadout sync" to write
the projection.

A vault made before this version has no stanza for codex, gemini,
cursor, or hermes. In that case, add the whole section shown above to
loadout.toml. Loadout ignores an adapter name that is not in the
manifest, so the adapter cannot turn on until you add its stanza. When
the stanza already exists, edit it in place; the warning above still
stands, so never add a second one.

### The agents-md adapter

The agents-md adapter writes your memory and a skills index into any
AGENTS.md file you name. It is off by default, since it has no
default target. Init already wrote a `[adapters.agents-md]` section in
loadout.toml. Open that section and edit it:

    [adapters.agents-md]
    enabled = true
    targets = ["~/some-project/AGENTS.md"]

Set "enabled" to true. List one or more target files under "targets".
Do not add a second `[adapters.agents-md]` section. Edit the section
that is already there. Run "loadout sync" to write the block into
each target file.

## Secrets

Loadout stores your API keys and other secrets in the vault. Each
secret sits next to your skills and memory. Loadout keeps every
secret's value encrypted on disk. Only your own device, or a device
you approve, can read it.

### Add a secret

Pipe the value on stdin. Do not pass it as an argument. An argument
can appear in your shell history or in a process list:

    printf %s "sk-abc123" | loadout secret add openai-key --service openai

Add `--rotate-after <duration>` to set a rotation reminder. For
example, use `720h` for 30 days. Add `--hook <text>` to note what the
secret is for. Add `--by <who>` to record who added it, the same way
`add skill` and `add memory` do.

### List and show secrets

Run `loadout secret list` to print every secret's metadata: its name,
its service, and when you added it. This command never prints a
value.

`loadout secret show <name>` refuses to print a value by default. To
see the raw value, add `--reveal`:

    loadout secret show openai-key --reveal

Use `--reveal` only when you must see the value yourself, for example
to copy it into another tool by hand. Each `--reveal` writes one line
to the access log (see below). For a script or an agent, use `loadout
run` instead (next section). It gives the secret straight to the
child process. The agent never has to read the value at all.

### Inject a secret into a command

`loadout run` is the main way an agent uses a secret. It decrypts the
secret, sets the value as an environment variable in a child process,
then runs the command. The value never appears in loadout's own
output:

    loadout run --secret openai-key -- your-tool --flag

This sets `$OPENAI_KEY` in the child process, then runs `your-tool
--flag`. Loadout derives the variable name from the secret's name. To
choose your own name, use `--secret <name>=ENVVAR`:

    loadout run --secret openai-key=API_KEY -- your-tool

Add more `--secret` flags to inject more than one value. Add `--by
<who>` to record who ran the command.

### Rotate a secret

Set `--rotate-after` when you add a secret. `loadout doctor` then
warns you once the secret is old enough to rotate:

    secret/openai-key: is due for rotation (added 2026-01-01T00:00:00Z, rotate after 720h)
      fix: rotate the key at openai, then run loadout secret rotate openai-key to replace it.

First, rotate the key at the service itself. Then replace its value
in the vault:

    printf %s "sk-new456" | loadout secret rotate openai-key

Rotate keeps the secret's service, hook, and rotate-after settings. It
replaces only the value and the added time. Pipe the new value on
stdin, the same way `secret add` does. Never pass it as an argument.

### Remove a secret

    loadout secret rm openai-key

This command deletes the secret and its encrypted value for good.

### The access log

Loadout keeps a local access log at `<vault>/access.log`. This file
stays on your own device; it never syncs. It records every `secret
show --reveal`, every `secret rotate`, and every `loadout run` call.
Each line holds the time, the verb, the secret's name, and who ran it.
It never holds a value. The access log is not part of the vault's git
history.

### Secrets sync as ciphertext

A secret's `value.age` file syncs across your devices the same way a
skill or a memory fact does, through `loadout sync --remote` and
`loadoutd`. `loadoutd` stores ciphertext only. It cannot decrypt a
secret, and neither can a device you have not approved. When you
approve a new full device, loadout re-encrypts every secret so the
new device can read it too. A no-secrets device never joins this
re-encryption: see "Device roles" above.

### The invariant

A secret's value never leaves your device in the clear. The two
exceptions are a child process under `loadout run`, and an explicit
`--reveal`. The value never appears in loadout's own output, in an
error message, in the access log, or anywhere on disk outside
`value.age`.

## MCP

Loadout can serve the Model Context Protocol (MCP). Run `loadout mcp`
to start it. It reads JSON-RPC messages from stdin, and writes replies
to stdout. Use it to connect an agent tool to your vault.

### Register the server

Most MCP clients read a JSON config file. Point the command at
`loadout mcp`, with no arguments:

    {
      "mcpServers": {
        "loadout": {
          "command": "loadout",
          "args": ["mcp"]
        }
      }
    }

For Claude Code, add this stanza to your project's `.mcp.json`, or run:

    claude mcp add loadout -- loadout mcp

For Codex, add the same stanza under `mcp_servers` in your own Codex
config file. Check your agent tool's own docs for its exact config
file and its location. The command and its argument stay the same
everywhere: `loadout mcp`.

### The read tools

Loadout exposes five read-only tools over MCP:

| Tool | Purpose |
|---|---|
| `context` | Read the vault's compact picture: counts, every hook, recent history. |
| `recall` | Search skills and memory facts for terms. |
| `show` | Read one skill or memory item's full body. Refuses a secret address. |
| `list` | List every skill and memory item. Excludes secrets. |
| `list_secrets` | List every secret's metadata: name, service, hook, allowed hosts. Never a value. |

None of these five tools can ever return a secret's value. `show`
refuses a `secret/*` address outright; use `list_secrets` for a
secret's metadata instead.

### The secret broker: `http_request`

An agent often needs a secret to call an API, but it must never hold
the key itself. The `http_request` tool solves this. It sends one HTTP
request on the agent's behalf. The agent writes `{{secret:<name>}}` in
a header value or in the body. Loadout decrypts the secret and
substitutes it into the OUTBOUND request only. The agent never sees
the value.

Loadout refuses the request unless the secret's `allowed_hosts` names
the request's exact host. This check is fail-closed: a secret with no
allowed hosts can never be brokered at all.

Set `allowed_hosts` when you add or rotate a secret:

    printf %s "sk-abc123" | loadout secret add openai-key --service openai --allowed-hosts api.openai.com

    loadout secret rotate openai-key --allowed-hosts api.openai.com,api.openai.com:8443

Loadout also scrubs the outbound server's own response. If the host
reflects the secret back — in a header, or in an error body — Loadout
replaces every occurrence with `[redacted-by-loadout]` before the agent
ever sees the result.

### The trust boundary

An allowed host is fully trusted with the secret, and with its own
response. Loadout sends the value to that host, and hands the host's
reply back to the agent, once scrubbed of the value itself. Allow-list
only a host you trust. Do not allow-list a host that might leak or
misuse the credential. Loadout cannot protect you from a host you
choose to trust.

### The invariant

An agent using `http_request` never receives the secret's value.
Loadout substitutes it into the outbound request on the server side,
and scrubs it from the response before the agent ever reads it. The
same invariant that governs `loadout run` and `secret show --reveal`
holds here too: the value never appears in the MCP stream, in an
error, or in the access log. The access log records the host a secret
reached, never the value.

## How it stays safe

- Loadout writes only inside marked blocks in shared files, and inside
  its own symlinks.
- Loadout never replaces a real file, a real directory, or a foreign
  symlink with one of its own.
- Every write to the vault takes a lock, so two agents never corrupt
  it by writing at once.
- The local git history in the vault records the state at each add,
  each review decision, and each sync. `loadout log` shows it;
  `loadout undo` reverts to the state before the last change.

## Sync across your machines

Loadout syncs one vault across every machine you use. A small server,
`loadoutd`, holds your data in between. `loadoutd` never sees your
content: your device encrypts every snapshot before it leaves, and
`loadoutd` stores only that ciphertext. Only a device you enroll can
decrypt it.

### Run the server

Build `loadoutd` and start it with a data directory:

    go build -o loadoutd ./cmd/loadoutd
    ./loadoutd -data /var/lib/loadout

On its first run, `loadoutd` generates an access token and prints it
once:

    loadoutd: generated an access token: 3f9a1c...e21b

Copy this token now. It never prints again, and it never appears in
any log line. Every device needs it to enroll.

### Connect the first device

Point your vault at the server with the url and the token:

    loadout remote add http://<host>:7777 <token>
    loadout sync --remote

This device is now the vault's first trusted device. `loadout sync
--remote` projects the vault into your local tools, then pushes it to
the server.

### Enroll a second device

On the new machine, join the same server:

    loadout join http://<host>:7777 <token>

The new device waits for approval. It cannot read the vault yet: it
holds no key that decrypts the content. On an already-approved
device, approve it by name:

    loadout devices approve <name>

Now sync on the new device:

    loadout sync --remote

The new device downloads and decrypts the vault. From now on, an edit
on either device reaches the other on its next sync.

Run `loadout devices` at any time. It lists every device: approved,
waiting, or re-keyed. A re-keyed device changed its key — for
example, after a lost machine got a fresh install. Verify the new key
out of band first: read it straight from `loadout device` on that
machine. Then trust it by name:

    loadout devices approve <name> --rotate <recipient>

Never trust a rotated key from the server's own device list alone. An
evicted device can still write to that list. Verify the key on the
device itself before you rotate to it.

### Device roles

Every device has a role: full or no-secrets.

A full device syncs the whole vault and can read every secret. This
is the default role, and the only role a device had before Phase 8a.
Approve a device as full the normal way:

    loadout devices approve <name>

A no-secrets device syncs the whole vault too. It receives every
skill and every memory fact. It can never read a secret's value,
even though the secret's file still reaches it. Approve a device as
no-secrets with a flag:

    loadout devices approve <name> --no-secrets

Use `--no-secrets` for a device that must use your skills and your
memory, but must never hold a key that could leak. The Phase 8b
browser dashboard enrolls this way: it shows your skills and your
memory in a browser tab, but it never becomes a device that could
expose a secret.

Change a device's role by re-approving it. Run `loadout devices
approve <name>` with no flag to promote a no-secrets device to full.
Run `loadout devices approve <name> --no-secrets` to demote a full
device. Either way, loadout re-encrypts every secret at once, so the
change takes effect right away, not just on the next new secret.

`loadout devices approve <name> --rotate <recipient>` keeps a
device's current role by default. Add `--no-secrets` or `--full` to
change the role at the same time as the key.

Run `loadout devices` to see each device's role next to its state:

    device-a — approved (full)
    device-b — approved (no-secrets)

**The guarantee**: loadout never encrypts a secret's value to a
no-secrets device's key. This is not a check the device honors on
its own — the encryption itself excludes it. A no-secrets device
that tries to decrypt a secret fails every time, with no way around
it short of an admin approving it as full.

### Keep every machine in sync automatically

Run `loadout watch` to sync in the background, on a timer:

    loadout watch

By default, `loadout watch` runs a sync beat every 10 seconds. Set a
different interval with `--interval`:

    loadout watch --interval 1m

`loadout watch` prints one line only when a beat changes something. It
stays quiet the rest of the time. When another loadout command holds
the vault lock, it skips that beat and tries again on the next one. If
the remote is unreachable, it prints one error line and waits longer
before the next try, up to five minutes. Press Ctrl-C to stop it: it
finishes its current beat, prints "watch stopped.", and exits.

### The invariant

`loadoutd` stores ciphertext only. It never decrypts a snapshot, never
holds a device's private key, and never sees a skill or a memory fact
in the clear. Only a device listed in the vault's own `devices.toml`
can decrypt what the server holds.

See PLAN.md section 11 for the v1 trust boundary: a snapshot is
encrypted, but not signed, so a bearer-token holder can push content
that enrolled devices will merge.
