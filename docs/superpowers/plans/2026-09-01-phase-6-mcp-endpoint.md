# Loadout Phase 6 Implementation Plan — MCP Endpoint

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `loadout mcp` — a stdio MCP server exposing the vault's read surface and a brokered `http_request` tool, so an agent recalls a fact and calls an API using a secret it never sees.

**Architecture:** A stdlib JSON-RPC 2.0 server over stdin/stdout (no MCP SDK dependency — the surface is initialize + tools/list + tools/call). It reuses the existing vault read functions (ListFacts, ListSkills, DecryptSecret) directly. Read tools: context, recall, show, list, list_secrets (metadata only). One brokered tool: http_request substitutes `{{secret:<name>}}` server-side, enforces a per-secret `allowed_hosts` allowlist, sends via net/http, returns the response only. Every brokered use logs (no value).

**Tech Stack:** Go stdlib (encoding/json, bufio, net/http) + toml + age. No new dependencies.

**Spec:** `/Users/waleed/loadout/PLAN.md` — §7 (verbs), §8 invariant 10 (no value leak — now also over MCP), §11, §12 Phase 6.

## Global Constraints — invariant 10 extends to MCP

- A secret value appears ONLY as value.age ciphertext, a child env (`run`), an explicit `--reveal`, OR — new in Phase 6 — inside an outbound `http_request` the broker itself sends (the value is placed into the request the server sends and returned to no one). The value NEVER appears in an MCP tool result, an MCP error, the JSON-RPC stream back to the agent, the server's own stdout/stderr, or the access log.
- The broker allowlist is a hard gate: a secret with no `allowed_hosts` is refused for brokering (fail closed); a request to a host not on the secret's list is refused with the value never sent. The host match is exact host (and optional port), not a substring — `api.openai.com` must not match `api.openai.com.evil.com`.
- The MCP server is read-plus-broker only in v1: it does NOT expose add/edit/rm/sync/rotate (no vault mutation from an untrusted agent channel). list_secrets returns metadata only, never a value, never whether the value is decryptable.
- Reuse the existing read functions and DecryptSecret; do not reimplement. The server runs with the vault lock only for the brief reads it needs; a broker call decrypts, sends, and zeroes.
- Standard: Go stdlib + toml + age; ASD-STE100; error grammar (MCP errors are JSON-RPC error objects with a clear message, same spirit); gofmt/vet clean; `go test -race -count=1 ./...` green before every commit; the trailer; temp-home tests only; dummy secrets only; never touch the real home.

## File Structure

```
internal/mcp/     server.go (JSON-RPC loop, initialize/tools/list/tools/call), tools.go (the tool set), broker.go (http_request + allowlist), server_test.go, broker_test.go
internal/vault/   secret.go (Secret gains AllowedHosts; AddSecret/meta.md gains --allowed-hosts)
internal/cli/     mcp.go (the `loadout mcp` verb → runs internal/mcp.Serve over os.Stdin/os.Stdout), secret.go (--allowed-hosts on add/rotate)
```

---

### Task 1: The JSON-RPC stdio server skeleton

**Files:** Create `internal/mcp/server.go` + test, `internal/cli/mcp.go`, modify run.go.

**Interfaces:** `mcp.Serve(v *vault.Vault, in io.Reader, out io.Writer) error` — a line/framed JSON-RPC 2.0 loop. Handle `initialize` (return protocolVersion + serverInfo + capabilities{tools:{}}), `tools/list` (return the tool schemas from Task 2/3), `tools/call` (dispatch to a handler by name; unknown tool → JSON-RPC error -32601). Malformed JSON → a JSON-RPC parse error (-32700) without crashing the loop. The loop reads requests until EOF, one JSON object per message (use the MCP-standard newline-delimited JSON or Content-Length framing — pick newline-delimited JSON for stdlib simplicity and document it). `loadout mcp` wires Serve to os.Stdin/os.Stdout; its own logs (if any) go to stderr, never stdout (stdout is the protocol channel).

**Steps:**
- [ ] Failing tests: feed an `initialize` request → a well-formed result with capabilities; `tools/list` → the (initially empty or stub) tool array; an unknown method → -32601; malformed JSON on one line → a -32700 error AND the loop survives to answer the next valid request; EOF → clean return. Drive Serve with bytes.Buffer in/out.
- [ ] Implement. Green, commit: `Add the MCP stdio JSON-RPC server`.

### Task 2: The read tools (context, recall, show, list, list_secrets)

**Files:** Create `internal/mcp/tools.go` + test.

**Behavior:** register five tools with JSON schemas:
- `context` (no args) → the compact situational picture (reuse the context render; text result).
- `recall {terms: string}` → matching items (addresses + hooks), reuse the recall matcher.
- `show {address: string}` → one item's body (ParseAddress + read; a secret address is REFUSED here — secrets are not shown via MCP).
- `list` (no args) → all items, addresses + hooks.
- `list_secrets` (no args) → secret metadata (name, service, hook, rotate_after, allowed_hosts) — NEVER a value, never decryptability.
Each returns an MCP tool result (content: [{type:"text", text:...}]). Errors (bad address, no match) are tool results with isError or a JSON-RPC error — pick the MCP-correct shape (a tool-level failure is a result with isError:true, not a protocol error) and be consistent.

**Steps:**
- [ ] Failing tests (temp vault with a skill, a fact, a dummy secret): each tool returns the right content; `show` of a secret address is refused; `list_secrets` returns metadata with NO value and does not decrypt; recall finds by term. Assert the dummy secret VALUE never appears in any tool result.
- [ ] Implement. Green, commit: `Add the MCP read tools`.

### Task 3: Secret allowed_hosts + the brokered http_request

**Files:** Modify `internal/vault/secret.go` (AllowedHosts field + AddSecret/RotateSecret param + meta.md), `internal/cli/secret.go` (--allowed-hosts flag). Create `internal/mcp/broker.go` + test.

**Behavior:**
- Secret metadata gains `allowed_hosts` (a comma list in the frontmatter; the Secret struct gains `AllowedHosts []string`). `secret add`/`secret rotate` gain `--allowed-hosts host1,host2` (each validated as a bare host or host:port, no scheme, no path). list/list_secrets show it.
- `http_request {method, url, headers: {name: value}, body: string}` — the agent may put `{{secret:<name>}}` in any header value or the body. The broker:
  1. Parse the URL; extract the host (and port). Reject a non-http(s) scheme.
  2. Find every `{{secret:<name>}}` placeholder across headers + body. For each named secret: load its metadata; if `allowed_hosts` is empty → refuse (fail closed, value never decrypted); if the request host is not an exact match of an allowed host → refuse (value never decrypted). Only when EVERY referenced secret allows this exact host, decrypt each and substitute.
  3. Send via net/http (a sane timeout, no redirect to a different host that would re-send the secret — cap redirects, or disable auto-redirect and return the 3xx). Return {status, headers (minus any that would echo the secret? — return response headers as-is, they are the SERVER's response, not the secret), body} as the tool result.
  4. Zero each decrypted secret after building the request. Append one access-log entry per secret used (verb "broker", secret, host — NEVER the value, never the full URL if it could contain a secret; log host only).
  - The value NEVER appears in: the tool result, an error, the server's stdout/stderr, the access log, or a redirect to another host.

**Steps:**
- [ ] Failing tests (httptest server as the "API"): a secret with allowed_hosts=[the httptest host] + a request with `{{secret:k}}` in an Authorization header → the httptest server RECEIVES the secret value in the header (proving substitution), and the MCP tool result contains the server's response but NOT the secret; a secret with an empty allowed_hosts → refused, value never sent (the httptest server never sees it); a request to a DIFFERENT host than allowed → refused, value never sent; the access log has a "broker" entry with the host, no value; a redirect to another host does not re-send the secret. Assert the dummy value never appears in any tool result or error.
- [ ] Implement. Green, commit: `Add allowed_hosts and the brokered http_request`.

### Task 4: MCP config docs, an adversarial broker test, README, security smoke

**Files:** Modify README, add `internal/mcp/broker_adversarial_test.go`, the security smoke in the report.

**Behavior:** README — an "MCP" section: how to register `loadout mcp` as an MCP server in Claude Code/Codex/etc. (the stdio command), the read tools, the brokered http_request with the allowed_hosts safety model, and the invariant that the agent never sees a key. Adversarial broker tests: an agent tries to exfiltrate — `{{secret:k}}` in a request to attacker.com (refused), a secret with allowed_hosts=api.example.com sent to api.example.com.evil.com (refused — exact host match), a placeholder in the URL host itself (does the substituted-into URL change the host and bypass the check? — the check must run on the FINAL host after any substitution, or refuse placeholders in the host; specify and test), a redirect from the allowed host to attacker.com (secret not re-sent). Security smoke: run `loadout mcp` as a subprocess, drive a real initialize + tools/call over its stdio with a dummy secret and a local httptest API, confirm the value reaches only the outbound request and never the MCP stream/log; transcript in the report.

**Steps:**
- [ ] Failing tests: the four adversarial exfiltration attempts all refuse with the value unsent; the host-in-placeholder case is handled (specify: refuse a `{{secret:}}` placeholder inside the URL, only allow it in headers/body).
- [ ] Implement + README + the subprocess security smoke. Green, commit: `Harden and document the MCP broker`.

---

## Self-Review Notes

- Spec coverage (§12 Phase 6): context + recall over MCP ✓(T2), brokered secret use ✓(T3), "recalls a fact and calls an API without holding the key" = the T3/T4 http_request smoke. The stdlib JSON-RPC choice keeps the no-new-dep rule.
- Invariant 10 over MCP: the value's only new sanctioned location is the outbound brokered request; the allowlist (fail-closed, exact host, post-substitution, redirect-safe) is the exfiltration guard, and it is the adversarial focus of the whole-branch review.
- The MCP channel is read-plus-broker only — no vault mutation from an agent — so a compromised/confused agent cannot rewrite skills or approve devices over MCP.
- Ordering: T1 (server) → T2 (read tools) → T3 (broker, the security core) → T4 (adversarial + docs). The final whole-branch review runs on fable with an adversarial exfiltration pass: it drives the real `loadout mcp` subprocess and tries to make a dummy secret reach a host its allowlist forbids, or appear in the MCP stream; any success is Critical.
- Real-key note: after merge, the user (per their choice) sets `--allowed-hosts` on their real secrets before brokering; a secret with no allowlist simply cannot be brokered (safe default).
