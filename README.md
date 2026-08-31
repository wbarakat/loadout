# Loadout

One secure home for your agent gear. Store your skills and your memory
in one vault. Sync them to every agent tool.

Phase 1 is local only. Cloud sync, secrets, and more adapters come next.
See PLAN.md for the roadmap.

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

## Enable more adapters

Some adapters are off by default. The agents-md adapter writes your
memory and a skills index into any AGENTS.md file you name. Init
already wrote a `[adapters.agents-md]` section in loadout.toml. Open
that section and edit it:

    [adapters.agents-md]
    enabled = true
    targets = ["~/some-project/AGENTS.md"]

Set "enabled" to true. List one or more target files under "targets".
Do not add a second `[adapters.agents-md]` section. Edit the section
that is already there. Run "loadout sync" to write the block into
each target file.

## How it stays safe

- Loadout writes only inside marked blocks in shared files.
- Loadout never replaces a real file or directory with a symlink.
- The local git history in the vault records the state at each add
  and each sync. Undo with git if you need to.
