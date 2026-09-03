# The dashboard

The dashboard is a browser tab that browses and edits your vault. It
is a static site with no server of its own: it talks straight to your
own `loadoutd`, over an HTTPS URL you control. No third party ever
touches your vault, your key, or your token. It enrolls itself as a
**no-secrets device** (see [device-roles.md](device-roles.md)): it
can read every skill and every memory fact, but it structurally
cannot decrypt a secret.

## Before you start

Run `loadoutd` behind an HTTPS URL first. See
[self-host.md](self-host.md) for the server flags, including
`-cors-origin`, which must match the dashboard's own origin or the
browser refuses every request.

## Connect and enroll

Open the dashboard in your browser. It asks for three fields:

- **loadoutd URL**: for example `https://loadoutd.example`.
- **Bearer token**: the access token `loadoutd` printed on its first
  run.
- **Device name**: defaults to `dashboard`.

Then add a device key: click **Generate key** to create a new one in
the browser, or paste an existing age identity. The dashboard shows
the new key's recipient (`age1...`) and the exact command to run:

```
loadout devices approve dashboard --no-secrets
```

Run that command on an already-approved **full** device, your
laptop, say. `--no-secrets` is what keeps the browser out of every
secret's recipient list; see [device-roles.md](device-roles.md) for
what that guarantees.

Click **Register + Connect**. Before the approval lands, the
dashboard shows a "Waiting for approval" screen with the same
recipient and command. Once you have run the approve command, click
**Retry connection**. The dashboard now shows the workspace: a
sidebar with Skills, Memory, Secrets, and Review.

## Browse and edit

Select Skills or Memory to list items, and click one to read its full
body. A draft or a kept item both render the same way. Editing a
memory fact and clicking Save writes straight back to the vault, the
same as `loadout edit`; run `loadout sync --remote` on any full
device afterward to confirm the change and push it on.

## Secrets are metadata-only

Select Secrets, then click a secret's name. The page shows only its
metadata: service, hook, rotation reminder, allowed hosts, who added
it, and when. There is no value anywhere on the page, and no button
or control to reveal one. A note on the page says so directly, and
points at the CLI:

```
loadout secret show <name>
```

## The review queue

Select Review to see every draft item. Each one has a **Keep**
button, the same action as `loadout review keep <kind>/<name>`: it
marks the item kept and drops it out of the queue. A skill or memory
item also offers an **Edit** control, so you can fix a draft before
you keep it. There is no drop control in the dashboard; drop an
unwanted draft from the CLI instead, with `loadout review drop
<kind>/<name>`.

The dashboard reviews one item at a time, which suits editing a draft
before keeping it. When you are not editing, and a first import has
left dozens of drafts, the CLI settles them in one command:

```
loadout review keep --all --by import:claude-code
loadout review drop --all --by import:codex --dry-run
```

See [import.md](import.md#reviewing-many-at-once) for the filters and
the bulk workflow.

## Next steps

See [device-roles.md](device-roles.md) for the full vs. no-secrets
guarantee, [self-host.md](self-host.md) for running `loadoutd`, and
`docs/dashboard-smoke.md` in this repository for the end-to-end
manual checklist run before a deploy.
