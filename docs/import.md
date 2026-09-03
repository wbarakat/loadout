# Import your existing skills and memory

`loadout import` pulls skills and memory from an agent tool already
on your machine into the vault. `loadout init` runs this for you on
first install (see [install.md](install.md)); use this guide to
import again later, or to import from just one tool.

## Run it

```
loadout import [SOURCE...] [--skills] [--memory] [--project DIR] [--project-memory] [--dry-run] [--verbose]
```

`SOURCE` is one of: `claude-code`, `codex`, `cursor`, `hermes`, `pi`,
`gemini`, `droid`. Give one or more source names to import from just
those tools. With no `SOURCE`, Loadout scans every tool it can find.

- `--skills` imports only skills; `--memory` imports only memory.
  Give neither flag to import both, the default.
- `--project DIR` sets the project directory `--project-memory`
  scopes to. It defaults to the current directory.
- `--dry-run` previews exactly what a real run would import, and
  writes nothing. Run this first when you are not sure what will
  land.
- `--verbose` (or `-v`) prints every imported item and every warning
  in full. Without it, the report is a short summary: the counts, and
  a grouped digest of warnings by category.

## What the report shows

A real import prints a short summary: how many drafts it wrote, by
kind and by tool, how many duplicates it skipped, and a grouped digest
of any warnings (for example `12  folders with no SKILL.md`). Add
`--verbose` to see each item and each warning in full, or `--json` for
the complete machine-readable result.

## Memory scope

By default, memory import pulls only **global** instruction files,
for example Claude Code's `~/.claude/CLAUDE.md`, not a per-project
`CLAUDE.md` inside a repository. Pass `--project-memory` to also pull
per-project or per-profile memory, scoped to `--project DIR` or the
current directory.

## Everything lands as a draft

An import never writes authoritative content. Every skill and every
memory fact it pulls in lands with `review: draft`. Review it before
you treat it as final:

```
loadout review
```

This lists every draft, with who wrote it and when. Keep the items
you want:

```
loadout review keep <kind>/<name>
```

Drop the rest:

```
loadout review drop <kind>/<name>
```

### Reviewing many at once

A first import can leave dozens of drafts, so `keep` and `drop` both
take several addresses, or `--all`:

```
loadout review keep skill/tdd skill/deslop memory/claude-md
loadout review keep --all
```

Use `--by` to act on one tool's batch, and `--kind` to act on skills or
memory alone. The `--by` value is the item's provenance, as shown by
`loadout review`:

```
loadout review keep --all --by import:claude-code
loadout review drop --all --by import:codex
loadout review keep --all --kind skill
```

Add `--dry-run` to list exactly what a command would act on, and change
nothing:

```
loadout review drop --all --by import:codex --dry-run
```

A bulk action writes one history entry for the whole batch, so a single
`loadout undo` reverses all of it. A bulk drop only ever deletes drafts:
an item you already kept is never touched.

Once you are done reviewing, project the kept items into every
enabled tool and push them to your remote, if you configured one:

```
loadout sync --remote
```

## Deduplication

Loadout drops a duplicate at two points, so importing twice, or
importing from two tools with overlapping content, never doubles up
your vault:

1. **Across sources.** Two candidates with the same kind, name, and
   content (one from `claude-code`, say, and an identical one from
   `codex`) collapse into one imported item.
2. **Against the vault.** A candidate whose name already exists in
   the vault, with matching content, is skipped and reported as
   deduped rather than imported again.

The import report always shows a deduped count, even when it is
zero.

## Honest limits

Two tools cannot be imported at all, and Loadout says so plainly
rather than silently skipping them:

- **Devin** is a hosted agent. Its skills and memory live in Devin's
  own cloud, not on this machine, so there is nothing local to
  import. Devin is not a valid `SOURCE` name.
- **Cursor's global User Rules** live in an internal database with no
  stable, documented format. Loadout cannot read them. Copy them by
  hand from Cursor's own Settings. (Cursor's project-level `.cursor`
  rules and skills are imported normally.)

Within every source, Loadout also excludes content that is not yours
to import:

- **Vendor, VCS, and build directories** are excluded from a skill
  folder before it is even read: `.git`, `.hg`, `.svn`,
  `node_modules`, `.venv`/`venv`/`env`, `.gradle`, `vendor`, and
  similar. A tool's own bundled, vendor-provided skills are excluded
  the same way.
- **An oversized skill is skipped**, with a reason naming its size.
  Trim the folder, or add it to the vault by hand with
  `loadout add skill`.

## Next steps

See [secrets.md](secrets.md) to add credentials by hand (import never
touches secrets), [mcp.md](mcp.md) to let an agent read the vault it
now holds, and [README.md](../README.md) for the full command
surface.
