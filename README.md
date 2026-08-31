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

## How it stays safe

- Loadout writes only inside marked blocks in shared files.
- Loadout never replaces a real file or directory with a symlink.
- Every change lands in a local git history inside the vault. Undo with
  git if you need to.
