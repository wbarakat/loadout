# Install Loadout

Loadout is a local-first vault for your agent skills, memory, and
secrets. This guide installs it and runs the first-run wizard. For
what Loadout does after install, see [import.md](import.md),
[secrets.md](secrets.md), and the [README](../README.md).

## Build or install the binary

```
go install ./cmd/loadout
```

This puts `loadout` on your `PATH`. `loadout help` prints the full
command list at any time.

## Run the wizard

```
loadout init
```

The wizard runs four steps, in order:

1. **Detect.** It looks for seven agent tools on your machine:
   `claude-code`, `codex`, `cursor`, `hermes`, `pi`, `gemini`, and
   `droid`. It checks each tool's root directory, and its binary on
   `PATH`. It never opens a config or credential file to do this.
2. **Enable adapters.** It asks whether to enable an adapter for
   every tool it found. Say yes, and Loadout configures an adapter
   for exactly the tools it detected — not for every tool Loadout
   supports, only the ones present on this machine.
3. **Import.** It asks whether to import your existing skills and
   memory now. Say yes, and it runs an import for every detected
   tool, landing every item as a **draft**. See
   [import.md](import.md) for what a draft is and how to review one.
4. **Remote.** It asks whether to connect a self-hosted `loadoutd`
   remote. Say yes, and it asks for the remote's URL and a path to a
   file holding its token. See [self-host.md](self-host.md).

Every question has a safe default. Press Enter to accept it. Answer
`Y` or `N` (either case) to choose explicitly.

## Run it headless

An agent or a script can run the same install with no prompts:

```
loadout init --yes [--tools a,b,...] [--no-import] \
  [--remote URL --token-file PATH] [--project-memory]
```

- `--yes` skips every prompt.
- `--tools a,b,...` enables adapters only for the named tools. Each
  name must be a tool `init` actually detected on this machine, or
  the command refuses and lists the valid names. Omit `--tools` to
  enable an adapter for every detected tool, the same default the
  interactive wizard uses.
- `--no-import` skips the import step. Leave it off, and `init --yes`
  imports automatically.
- `--remote URL --token-file PATH` connects a self-hosted `loadoutd`.
  Pass both flags together, or neither. **Never pass the token as a
  command-line value.** Write it to a file first, and point
  `--token-file` at that file. A token typed on the command line can
  land in shell history or a process list; `init` refuses `--remote`
  without `--token-file` for this reason.
- `--project-memory` also imports per-project or per-profile memory,
  not just global instruction files. See
  [import.md](import.md#memory-scope) for the difference.

## What gets configured

For each tool you enable, Loadout writes its skills directory and its
memory file path into the vault's manifest (`loadout.toml`). These
match the tool's own defaults — for example, Claude Code's skills
live under `~/.claude/skills`, and its memory file is `~/.claude/CLAUDE.md`.
Every enabled tool then gets a fresh projection on every `loadout sync`.

## Link adoption

When Loadout links a vault-owned skill into a tool's skills
directory, it may find a symlink already there under that skill's
name, pointing somewhere outside the vault — a **foreign link**, made
by hand or by another tool before Loadout ever ran. Because the vault
owns a skill with that exact name, Loadout **adopts** the foreign
link: it repoints the symlink at the vault's copy. It reports this as
an adoption, not an error.

A real file or a real directory in that same spot is never touched.
Loadout leaves it alone and reports it as blocked, so you can move it
out of the way by hand if you want the vault's version linked there
instead.

## Re-running init is safe

Run `loadout init` again at any time. It never destroys an existing
vault:

- On a fresh machine, it creates the vault.
- On a machine with a vault already, it keeps it and only adds what
  is missing.
- An adapter you already enabled keeps its existing skills directory
  and memory file, even if you name that tool in `--tools` again — a
  path you customized by hand is never reset back to the detected
  default. Only a newly enabled adapter adopts the detected default.
- A re-run enables the adapters for every tool it detects — or for
  the tools you name with `--tools`. So a tool you disabled but still
  have installed is turned back on by a plain re-run. To keep it off,
  run with `--tools` naming only the tools you want.

## Next steps

After `init`, review what it imported:

```
loadout review
loadout sync --remote
```

See [import.md](import.md) for the review workflow, and
[README.md](../README.md) for the full command surface and the
security model.
