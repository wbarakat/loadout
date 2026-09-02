# Self-hosting loadoutd

`loadoutd` is the sync server. It stores only opaque, encrypted
snapshot blobs; it never parses or decrypts one. This guide runs it
and connects a vault to it. See [device-roles.md](device-roles.md)
for who can decrypt what once devices are enrolled, and
[dashboard.md](dashboard.md) for the browser client that talks to
this same server.

## Run the server

```
loadoutd -data DIR [-addr :7777] [-cors-origin URL]
```

- `-data DIR` is required: the directory `loadoutd` stores its
  snapshots and device roster in.
- `-addr` is the address it listens on. Default: `:7777`.
- `-cors-origin` is the single browser origin allowed to call this
  server over CORS. Leave it unset, and CORS stays off: a
  self-hosted server answers no cross-origin browser request unless
  you opt in. Set it to the exact origin the [dashboard](dashboard.md)
  is served from, for example `https://dashboard.example`.

If `-cors-origin` is not passed, `loadoutd` falls back to the
`LOADOUT_CORS_ORIGIN` environment variable. The flag wins when both
are set.

On its very first run for a given `-data` directory, `loadoutd`
generates a random access token, stores it at `<data>/token` (mode
`0600`), and prints it once:

```
loadoutd: generated an access token: <token>
```

Save it now. Every later run only prints the address it listens on;
the token itself never appears again.

Front `loadoutd` with an HTTPS URL of your own — a reverse proxy, a
tunnel, or a Tailscale/VPN address — before you connect a real
device. The token is a bearer credential; do not send it over plain
HTTP on an untrusted network.

## Connect a vault to it

On a device that already has a vault:

```
loadout remote add <url> <token>
loadout sync --remote
```

`loadout remote add` never prints the token back. To enroll a
brand-new device against a remote instead, see `loadout join <url>
<token>` in the [README](../README.md#devices-and-remotes) and
[device-roles.md](device-roles.md).

## The v1 trust boundary

A snapshot is encrypted before it ever leaves a device. It is **not
signed**. Any device holding the remote's bearer token can:

- read the enrolled device roster (`GET /v1/devices`);
- encrypt a new snapshot to those recipients, and push it.

An already-enrolled device merges a pushed snapshot without checking
who really authored it. In self-host v1, **the holder of the bearer
token is trusted as the vault's owner** — this includes the operator
of a self-hosted server. Treat the token with the same care as a root
credential to the vault: anyone who has it can push content every
device will merge.

## Deferred for a hosted service

Two protections are planned for a hosted version of this service, not
yet built into self-host v1:

- **Per-device snapshot signing.** A future wire protocol rejects a
  merge when the snapshot's signer is not already in `devices.toml`,
  closing the trust gap above.
- **Server-side access logging for the dashboard.** Today, every
  secret access is logged only on the device that made it (see
  [secrets.md](secrets.md#the-access-log)); a hosted service serving
  the dashboard needs its own server-side record of dashboard
  activity.

Self-host v1 is appropriate for a vault you and people you trust
operate directly. It is not yet the multi-tenant trust model a hosted
service needs.
