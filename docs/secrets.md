# Secrets

Loadout stores an API key or another credential encrypted, and lets
an agent use it without ever reading it. This guide covers the
`secret` and `run` commands. See [mcp.md](mcp.md) for the equivalent
flow inside an MCP session, and [device-roles.md](device-roles.md)
for who can decrypt a secret at all.

## Add a secret

```
printf %s "$VALUE" | loadout secret add <name> --service <svc> \
  [--hook <text>] [--rotate-after <duration>] [--by <who>] \
  [--allowed-hosts <host1,host2>]
```

The value is never a flag or an argument. Pipe it on stdin. Loadout
refuses to run `secret add` at all when stdin is a terminal, since
there is no safe way to read a value there without echoing it back.

- `--service` is required: the service this key belongs to, for
  example `stripe`.
- `--hook` is a short one-line note shown in listings.
- `--rotate-after` is a reminder duration, for example `720h`.
- `--allowed-hosts` lists the hosts the MCP broker may send this
  secret to (see [mcp.md](mcp.md)). Give a bare host or `host:port`
  (no scheme, no path, no wildcard). Leave it unset, and the broker
  refuses to send this secret anywhere at all: the default is fail
  closed, not open.
- `--by` records who added it. Omit it for a human typing directly;
  pass `--by <tool-name>` when an agent adds it on a human's behalf.

## List secrets

```
loadout secret list [--json]
```

Prints every secret's metadata: name, service, who added it, and
when. Never a value.

## Show a secret

```
loadout secret show <name> [--reveal] [--by <who>]
```

Without `--reveal`, this refuses and prints nothing:

```
refusing to reveal a secret without --reveal. Fix: run loadout secret show <name> --reveal, or use loadout run to inject it.
```

With `--reveal`, it prints the raw value to stdout and appends one
entry to the access log. `--reveal` never combines with `--json`.

## Rotate a secret

```
printf %s "$NEW_VALUE" | loadout secret rotate <name> \
  [--by <who>] [--allowed-hosts <host1,host2>]
```

Replaces the value, piped on stdin the same way `secret add` reads
it. The service, hook, and rotation reminder stay as they were.
Passing `--allowed-hosts` replaces the allow-list; omit it to keep
the existing one unchanged.

## Remove a secret

```
loadout secret rm <name>
```

## Use a secret without holding it

```
loadout run --secret <name>[=ENVVAR] [--secret <name2>...] [--by <who>] -- <cmd> [args...]
```

`run` decrypts every named secret, injects each into the child
process's environment, then execs `<cmd>`. Loadout's own exit code is
the child's exit code. By default the environment variable name is
derived from the secret's name (`openai-key` becomes `OPENAI_KEY`),
or give an explicit name with `<name>=ENVVAR`.

The value never reaches Loadout's own stdout, stderr, or the access
log, only the child process's environment. `run` has no `--json`
form: it is a transparent wrapper around the child, with nothing of
its own to report.

## The access log

Every real use of a secret appends one JSON line to `access.log`, at
the root of the vault (`~/.loadout/access.log` by default, or
`$LOADOUT_HOME/access.log`). Each line names the time, the verb
(`show`, `rotate`, `run`, or `broker` for an MCP-brokered request),
the secret's name, and who used it, never a value. A brokered
request also names the exact host the secret was sent to. This file
is device-local: it never syncs, and it never enters the vault's own
history.

## The invariant

A secret's decrypted value may exist in exactly four places, and
nowhere else:

1. As ciphertext at rest, in the vault's `secret/<name>` files.
2. As an environment variable inside a child process `loadout run`
   spawned, never in Loadout's own process output.
3. On stdout, under an explicit `loadout secret show <name> --reveal`.
   It is never shown by default, and never in JSON output.
4. Inside an outbound HTTP request the MCP broker's `http_request`
   tool builds, substituted server-side, never returned to the
   agent that asked for it. See [mcp.md](mcp.md).

It never appears in a plaintext vault file, a tool projection, an
error message, a log line, or `--json` output.
