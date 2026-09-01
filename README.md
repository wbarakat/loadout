# Loadout

One secure home for your agent gear. Store your skills and your memory
in one vault. Sync them to every agent tool.

Phase 2 adds the full agent interface: typed reports, the vault lock,
provenance, review, and `--json` on every verb. Phase 3 adds four
adapters: codex, gemini, cursor, and hermes. The Adapters section
below covers all six local tools, plus a generic AGENTS.md adapter.
Phase 4 adds cloud sync: `loadoutd`, device enrollment, and `loadout
watch`. See the "Sync across your machines" section below. Secrets
come next. See PLAN.md for the roadmap.

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
| `devices [--json]` | Show every device: approved, waiting, or re-keyed. |
| `devices approve <name>` | Approve a waiting device. |
| `devices approve <name> --rotate <recipient>` | Trust a verified new key for an already-approved device. |

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
