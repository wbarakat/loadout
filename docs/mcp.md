# The MCP endpoint

`loadout mcp` serves the vault to an agent tool over the Model
Context Protocol, as JSON-RPC 2.0 on stdio. No network port opens: an
agent tool spawns `loadout mcp` as a local child process and talks to
it over its stdin and stdout.

```
loadout mcp
```

It takes no arguments. Every diagnostic message goes to stderr, never
stdout, so it can never corrupt the JSON-RPC stream. It implements
`initialize`, `tools/list`, and `tools/call`, at protocol version
`2024-11-05`.

## Register it with an agent

Most MCP-compatible tools accept a stdio server as a command plus
arguments, in a form close to this:

```json
{
  "mcpServers": {
    "loadout": {
      "command": "loadout",
      "args": ["mcp"]
    }
  }
}
```

Add this to your tool's own MCP configuration file. Some tools also
offer a short command for the same thing — check your tool's own
documentation for its exact syntax.

## The read tools

Five tools are read-only. None of them can ever return a secret's
value:

| Tool | Arguments | Reads |
|---|---|---|
| `context` | none | The vault's compact picture: counts, every hook, recent history. |
| `recall` | `terms` (string) | Every skill and memory fact matching all the given terms. |
| `show` | `address` (string, `kind/name`) | One item's full body. Refuses a `secret/*` address outright. |
| `list` | none | Every skill and memory fact, as an address with its hook. Secrets excluded. |
| `list_secrets` | none | Every secret's metadata: name, service, hook, rotation reminder, and allowed hosts. Never a value. |

## The brokered tool: `http_request`

`http_request` is the one place a secret's value is ever allowed to
flow. An agent never sees the value directly. Instead, it writes a
placeholder in a header value or the request body:

```
{{secret:<name>}}
```

Loadout substitutes the real, decrypted value into the **outbound**
request, server-side, and returns only the response. The agent never
reads the value.

Arguments: `method`, `url`, `headers` (an object), `body` (a string).
A placeholder is refused in `url` outright — before the URL is even
parsed — so a secret can never choose or rewrite the host a request
is sent to.

Before decrypting anything, Loadout checks every referenced secret's
`allowed_hosts` against the request's exact host, case-insensitively.
An empty `allowed_hosts` refuses the whole request — no host is
permitted by default. A host that is not an exact match also refuses
the whole request; a subdomain or a suffix match does not count. This
check runs for every referenced secret before any of them is
decrypted, so one permitted secret can never carry another,
unpermitted one along.

Only once every check passes does Loadout decrypt, substitute, and
send the request. It also:

- refuses to follow a redirect to a different host, or to a less
  secure scheme on the same host, so a secret can never be re-sent
  elsewhere or downgraded to cleartext by a `3xx` response;
- scrubs the response of any exact occurrence of a substituted
  value, replacing it with `[redacted-by-loadout]`, so a host that
  reflects a credential back (an echoed header, an error message
  quoting it) cannot hand it to the agent that never saw it;
- appends one access-log entry per secret used, naming the host —
  never the full URL, never the value. See
  [secrets.md](secrets.md#the-access-log).

Set a secret's allow-list when you add or rotate it:

```
loadout secret add openai-key --service openai --allowed-hosts api.openai.com
```

See [secrets.md](secrets.md) for `secret add`/`rotate`, and
[device-roles.md](device-roles.md) for which devices can decrypt a
secret at all — the MCP broker only ever works on a **full** device.
