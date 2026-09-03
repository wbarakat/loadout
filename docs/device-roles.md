# Device roles: full vs. no-secrets

Every device in a vault's roster has a role: **full** or
**no-secrets**. This guide explains the guarantee each role carries,
and how to set or change one. See [secrets.md](secrets.md) for how a
secret is stored, and [dashboard.md](dashboard.md) for the one device
that always uses the no-secrets role today.

## The two roles

- **full**: reads every skill, every memory fact, and can decrypt
  every secret. This is the default role for a device you approve
  with no flag.
- **no-secrets**: reads every skill and every memory fact, the same
  as a full device. A secret's value never encrypts to its key at
  all: it is not that the device is asked not to decrypt one, and not
  a client-side check that could be skipped. A secret simply has no
  ciphertext a no-secrets device's key can open. This is what the
  [dashboard](dashboard.md) uses, so a compromised or leaked dashboard
  key can never expose a secret's value.

## Approve a device with a role

```
loadout devices approve <name> [--no-secrets]
```

Approving with no flag grants **full**. Add `--no-secrets` to
approve a device that must never read a secret. See
`loadout devices` to list every device's current state and role
(`approved`, `waiting`, or `re-keyed`).

To trust an out-of-band-verified new key for an already-approved
device (a re-key, or recovering a lost one), name the exact
recipient you verified:

```
loadout devices approve <name> --rotate <recipient> [--no-secrets|--full]
```

Omit `--no-secrets`/`--full` on a rotation to keep that device's
existing role unchanged. A rotation never guesses the new key from
the remote's own live roster; it only ever accepts a recipient you
pass explicitly, verified out-of-band, for example by reading it
straight from `loadout device` on that machine.

## Changing a device's role

Re-approve an already-approved device with a different flag to change
its role:

```
loadout devices approve <name> --no-secrets
```

If `<name>` already holds the **full** role, this demotes it. If it
already holds **no-secrets**, drop the flag to promote it back to
**full**. Either direction re-encrypts every secret to the roster as
it now stands: a device just demoted to no-secrets drops out of every
secret's recipients immediately, and one just promoted to full joins
them. This happens as part of the same approval. There is no
separate step to run.

## What the guarantee actually is

A secret's value is encrypted to every **full** device in the
roster, and to no one else. A no-secrets device receives the exact
same vault snapshot as every other device (same skills, same
memory, same secret *files*), but those secret files hold ciphertext
it has no key for. `loadout secret show`, `loadout run --secret`, and
the MCP broker's `http_request` (see [mcp.md](mcp.md)) all fail to
decrypt on a no-secrets device, the same way they would fail for
anyone without the right key at all.

This is why the dashboard is safe to run as a browser tab talking
directly to your `loadoutd`: even if its device key were ever
extracted from the browser, it opens no secret at all.

## Next steps

See [self-host.md](self-host.md) for the server this roster syncs
through, and [secrets.md](secrets.md) for adding and using a secret
on a full device.
