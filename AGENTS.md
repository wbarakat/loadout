# AGENTS.md: onboarding a user onto Loadout

This file is for an agent, not a human. It tells you how to install
Loadout for a user, pull in their existing skills and memory, and
drive the vault afterward. Follow it in order. Every command below is
real: it matches `loadout help` and the current CLI code, not a
guess.

Loadout is a local-first vault at `~/.loadout`. It stores skills,
memory, and secrets, and projects them into every agent tool on the
machine. The human owns the vault; you drive it. Read PLAN.md
(sections 5 and 8) for the full design rationale if you need it. You
do not need it to complete this file.

## 1. Install Loadout headless

Build or install the CLI, if it is not already on `PATH`:

```
go install ./cmd/loadout
```

Then run the headless installer:

```
loadout init --yes [--tools claude-code,codex] [--no-import] \
  [--remote URL --token-file PATH] [--project-memory]
```

`--yes` skips every prompt. With no `--tools`, it enables an adapter
for every agent tool it detects on the machine. Pass `--tools` with a
comma-separated list to enable only those tools.

`--no-import` skips the import step. Leave it off, and `init --yes`
runs an import automatically, landing every item as a draft (see
section 2).

To connect a self-hosted `loadoutd` remote during install, pass both
`--remote URL` and `--token-file PATH` together, never one without
the other. **Never pass a token as a command-line value.** Write it to
a file first, and pass that file's path to `--token-file`. A token
value on the command line can land in shell history or a process
list; Loadout refuses `--remote` without `--token-file` for exactly
this reason.

Add `--project-memory` to also import per-project or per-profile
memory, not just global instruction files.

`init` is safe to re-run. An existing vault is never destroyed; a
re-run only adds what is missing.

## 2. Import the user's existing content

If you skipped import during `init`, or want to import again later
(new tool installed, more content to pull), run:

```
loadout import [SOURCE...] [--project-memory]
```

With no `SOURCE`, Loadout scans every agent tool it can find on this
machine: `claude-code`, `codex`, `cursor`, `hermes`, `pi`, `gemini`,
`droid`. Run `loadout import --dry-run` first if you want to preview
what would import, with nothing written.

Two tools have a stated limit, not a bug: Devin is hosted, so its
skills and memory are not on this machine to import. Cursor's global
User Rules live in an undocumented internal database that Loadout
cannot read; tell the user to copy them by hand from Cursor's own
Settings.

**Every import lands as a draft.** It is never silently authoritative.
Review what landed:

```
loadout review
```

This lists every draft with who wrote it and when. Keep the items the
user wants, and drop the rest:

```
loadout review keep <kind>/<name>
loadout review drop <kind>/<name>
```

An import usually produces many drafts at once, so both verbs take
several addresses, or `--all` with optional filters:

```
loadout review keep skill/tdd skill/deslop
loadout review keep --all
loadout review keep --all --by import:claude-code
loadout review drop --all --by import:codex
loadout review keep --all --kind skill
```

`--by` matches an item's provenance, exactly as `loadout review` prints
it. `--kind` narrows to `skill` or `memory`. Add `--dry-run` to any of
these to see the resolved list and change nothing. Use it before a bulk
drop, and show the user what it would delete.

The whole batch is one history entry, so a single `loadout undo`
reverses it. A bulk drop only ever deletes drafts, never an item the
user already kept.

Settle every draft before you treat the vault's content as final. If
the user wants everything kept, `loadout review keep --all` is the
explicit action that says so. Never simply assume a draft is already
authoritative.

## 3. Read and write items

`loadout context` is the cheapest way to get your bearings: it prints
the vault's compact picture (item counts, every skill and memory
hook, and recent history) in one call, staying compact by design.
Start here in any new session.

To search:

```
loadout recall <term>...
```

This matches every term, case-insensitively, against item hooks and
bodies, and returns the matching addresses.

To read one item in full:

```
loadout show <kind>/<name>
```

To add a new skill or memory fact:

```
loadout add skill <name> [--by <who>]
loadout add memory <name> [--by <who>]
```

Always pass `--by <your-tool-name>` (for example `--by claude-code`)
when you write on the user's behalf. This records provenance and
marks the item a **draft**, so the user reviews it before it counts as
final, the same review flow as import. Omitting `--by` records the
write as a human's, and marks it kept immediately; do this only when
a human is actually dictating the content to you directly, not when
you are writing from your own inference.

To open an item in an editor (rarely useful for an agent; mostly for
a human):

```
loadout edit <kind>/<name>
```

**The write-back protocol.** Every projection Loadout writes into a
tool's own memory file ends with a short footer that teaches this same
loop: search with `recall`, read one item with `show`, save a new fact
with `add memory --by <tool>`, then run `sync`. If you already see
this footer in your own context (in `CLAUDE.md`, `AGENTS.md`, or
another tool's memory file), you have already learned the protocol
from the projection itself, so no further setup is needed.

## 4. Sync

After adding, keeping, or dropping items, project the changes into
every enabled tool and push to the remote, if one is configured:

```
loadout sync --remote
```

Without `--remote`, `sync` only re-projects the local tools; it does
not push. Run `loadout sync --dry-run` to preview a projection plan
with nothing written, if you want to check your own understanding of
the vault's state first.

## Invariants you must respect

These are not suggestions. Loadout enforces some of them at the CLI
level; treat all of them as hard rules regardless:

1. **A secret's value is never revealed.** `loadout secret show <name>`
   refuses to print a value unless the human passes `--reveal`
   explicitly. Never construct a call that adds `--reveal` on the
   user's behalf without the user asking for it in that moment, and
   never echo a secret's value anywhere in your own output, logs, or
   messages back to the user.
2. **Use a secret without holding it.** To let a command use a secret,
   run it through `loadout run --secret <name> -- <cmd> [args...]`,
   which injects the value into the child process's environment only.
   You never see the value yourself. Over MCP, use the broker's
   `http_request` tool with a `{{secret:<name>}}` placeholder instead;
   Loadout substitutes the real value server-side and the value never
   reaches you.
3. **Imports land as drafts, never as authoritative content.** Always
   route an import through `loadout review` before treating its
   content as final, per section 2.
4. **Never write a secret's plaintext into the vault.** A secret only
   ever enters the vault through `loadout secret add` or
   `loadout secret rotate`, piped on stdin, never as a file you write
   directly, and never as an argument on a command line.

An agent that follows this file end to end (install, import, review,
read, write, sync) can onboard a user onto Loadout with no other
documentation. See `README.md` for the full command reference and the
security model, and `PLAN.md` for the design rationale behind it.
