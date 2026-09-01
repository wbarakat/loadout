# Loadout Phase 8b (Part 1) Implementation Plan — Browser Vault Client + loadoutd CORS

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a headless, fully-tested TypeScript vault-client library that a browser uses to pull, decrypt, parse, edit, and push a Loadout snapshot against a self-hosted `loadoutd`, plus the one `loadoutd` change (CORS) a browser needs — proven by a cross-language interop test that the Go server and CLI accept.

**Architecture:** The browser is a **no-secrets device** (Phase 8a). It holds an age X25519 identity, pulls the single binary-age snapshot blob from `loadoutd`, decrypts it in-browser, and untars a POSIX ustar stream into skills, memory, secret metadata, and the `devices.toml` roster. It can read skills and memory in full but the per-secret `value.age` bytes stay opaque (its key is not a recipient). To edit, it applies one item change to the freshly-pulled tree, carries every `secrets/**` byte through unchanged, repacks the whole tree, re-encrypts to every device recipient from `devices.toml`, and pushes with the exact version it pulled as `X-Loadout-Parent`; a `409` means reload and re-apply (no browser-side three-way merge — the Go merge needs git history the browser lacks). This part builds no UI; it delivers the tested library and the CORS change. Part 2 builds the Next.js UI on top and deploys to Vercel.

**Tech Stack:** Go stdlib (loadoutd CORS). TypeScript + Vitest for the library; `age-encryption` (Filippo Valsorda's TS age implementation) for X25519 crypto; `smol-toml` for `devices.toml`; a hand-rolled minimal ustar reader/writer (no general tar dependency, so traversal-hardening is under our control). Node 20+, npm.

**Spec:** `/Users/waleed/loadout/PLAN.md` — §11 (security model, v1 trust boundary), §12 Phase 8b, invariant 10 (a no-secrets device never decrypts a secret). **Interop contract (authoritative wire/format reference for every task):** the implementer receives the file `phase-8b-interop-contract.md` (byte-level detail on the HTTP API, the age blob, the tar layout, `value.age`, the roster, the push protocol, enrollment, age specifics, and CORS). Read it first; it pins every format this library must match.

## Global Constraints

- **The no-secrets guarantee is the security invariant.** The browser identity must never be a recipient of any `secrets/*/value.age`. Prove it: a raw `age` decrypt of a `value.age` with the browser (no-secrets) identity MUST fail; the same file MUST decrypt with a full identity. This is the acceptance gate of Task 8.
- **Carry secrets through byte-for-byte.** On write-back the client MUST copy every `secrets/**` tar entry (`meta.md` and `value.age`) verbatim — never decrypt, regenerate, rename, re-encode, or drop one. It has neither the key nor the authority to change secret data.
- **Match the Go formats exactly** (per the interop contract): binary (unarmored) age v1, X25519 only, no custom stanzas; POSIX ustar tar; `devices.toml` roster with `recipient` + optional `role` (`full`/`no-secrets`), absent role = `full`, any unrecognized role = `no-secrets` (fail closed); recipient = `age1` + 58 chars; the push protocol with `X-Loadout-Parent` and the `409 {"latest":"..."}` conflict.
- **Traversal-hardening is security-critical, not cosmetic.** The tar reader MUST reject any entry whose name is absolute, empty, contains a `..` path segment, is a symlink, or does not start with `skills/`, `memory/`, `secrets/`, or equal `devices.toml`. A bad entry aborts the whole unpack (no partial extraction).
- **No new Go dependencies.** The CORS change is stdlib only. The Go server package must not import `filippo.io/age` (interop contract §1).
- **Standard:** ASD-STE100 in docs/comments/commit messages; the error grammar (a message names the repair); gofmt/vet clean and `go test -race -count=1 ./...` green before every Go commit; `npm run typecheck` + `npm test` green before every TS commit; the trailer `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>` on every commit. Temp-home tests only; DUMMY secrets and DUMMY age keys only; never touch the real home (`~/.loadout`, `~/.claude`, etc.); never run a mutating `loadout` verb against a real-home-targeted manifest.

## File Structure

```
internal/server/         cors.go (new: CORS middleware + OPTIONS), api.go (wire the middleware), cors_test.go
cmd/loadoutd/            main.go (a -cors-origin flag / LOADOUT_CORS_ORIGIN env, default off)
internal/interop/        interop_test.go (new: Go writes fixtures for TS; Go reads the TS-written snapshot)
scripts/                 interop-test.sh (new: runs the Go->TS->Go round-trip gate)
web/                     package.json, tsconfig.json, vitest.config.ts, .gitignore
web/lib/vault/           age.ts, tar.ts, model.ts, client.ts, sync.ts, types.ts + *.test.ts
web/lib/vault/testdata/  (gitignored) fixtures generated by the interop harness
```

`web/` is a plain TypeScript package in Part 1 (no Next.js yet). Part 2 adds the Next.js app into the same `web/` directory and imports this library from `web/lib/vault/`.

---

### Task 1: loadoutd CORS

**Files:** Create `internal/server/cors.go`, `internal/server/cors_test.go`; modify `internal/server/api.go` (wrap the handler), `cmd/loadoutd/main.go` (the origin flag).

**Why:** `loadoutd` sets no CORS headers and does not answer `OPTIONS`, so every cross-origin browser fetch is blocked before it is sent (interop contract §10). A browser page served from Vercel must be able to call the `/v1/*` API on the user's `loadoutd`.

**Interfaces:**
- Produces: a `corsMiddleware(next http.Handler, allowedOrigin string) http.Handler` that, when `allowedOrigin != ""`, (a) answers any `OPTIONS` request with `204` and the CORS headers, and (b) adds `Access-Control-Allow-Origin: <allowedOrigin>` to every response. When `allowedOrigin == ""`, it is a pass-through (CORS stays off by default; self-host opt-in).
- Headers on both preflight and actual responses: `Access-Control-Allow-Origin: <exact origin>` (never `*`), `Vary: Origin`. Preflight additionally: `Access-Control-Allow-Methods: GET, POST, OPTIONS`, `Access-Control-Allow-Headers: Authorization, Content-Type, X-Loadout-Parent`, `Access-Control-Max-Age: 600`.
- `Server.Handler()` wraps its mux with `corsMiddleware(mux, s.corsOrigin)`. `cmd/loadoutd` reads the origin from `-cors-origin` flag or `LOADOUT_CORS_ORIGIN` env (flag wins) and passes it into the server config.

**Steps:**
- [ ] **Step 1 — Failing test** (`internal/server/cors_test.go`): with a server configured `corsOrigin = "https://loadout.example.com"`:
  - an `OPTIONS /v1/snapshots/latest` request with `Origin: https://loadout.example.com` returns `204`, `Access-Control-Allow-Origin: https://loadout.example.com`, `Access-Control-Allow-Methods` containing `POST`, and `Access-Control-Allow-Headers` containing `Authorization` and `X-Loadout-Parent`;
  - a normal `GET /health` returns `200` AND carries `Access-Control-Allow-Origin: https://loadout.example.com`;
  - with `corsOrigin = ""` (default), `OPTIONS /v1/snapshots/latest` does NOT return the CORS headers (pass-through) — CORS is opt-in.
  Assert the token is still required for `/v1/*` (an `OPTIONS` preflight carries no auth and must NOT leak data — it returns only headers, never a body).
- [ ] **Step 2 — Run, verify it fails.** `go test ./internal/server -run TestCORS -v` → FAIL (no cors.go).
- [ ] **Step 3 — Implement** `cors.go` and wire it in `api.go` + `main.go`. Keep the auth middleware ahead of the real handlers; the CORS middleware wraps the whole mux so preflights are answered before auth. The preflight path must return before the auth check (a preflight legitimately carries no `Authorization`).
- [ ] **Step 4 — Run tests.** `go test -race -count=1 ./internal/server/... -v` → PASS. Then `go test -race -count=1 ./...` green, gofmt/vet clean.
- [ ] **Step 5 — Commit:** `Add opt-in CORS to loadoutd for the browser dashboard`.

---

### Task 2: TS package scaffold + the age wrapper

**Files:** Create `web/package.json`, `web/tsconfig.json`, `web/vitest.config.ts`, `web/.gitignore`, `web/lib/vault/types.ts`, `web/lib/vault/age.ts`, `web/lib/vault/age.test.ts`.

**Why:** A thin, stable wrapper over `age-encryption` isolates the third-party API and gives the rest of the library one small crypto surface.

**Interfaces (`age.ts`) — the library's stable crypto contract:**
```ts
// A raw age X25519 identity string "AGE-SECRET-KEY-1..." and its recipient "age1...".
export interface AgeKeypair { identity: string; recipient: string }
export function generateKeypair(): Promise<AgeKeypair>
export function recipientFor(identity: string): Promise<string>
// Decrypt one binary age file with one identity; throws AgeDecryptError on identity mismatch.
export function decrypt(ciphertext: Uint8Array, identity: string): Promise<Uint8Array>
// Encrypt to one or more age1 recipients; binary (unarmored) output.
export function encryptTo(plaintext: Uint8Array, recipients: string[]): Promise<Uint8Array>
export class AgeDecryptError extends Error {}
```

**Notes for the implementer:**
- Add `age-encryption` (pin an exact recent version) to `web/package.json`; confirm its ACTUAL API from its installed types/README — the exact method names differ by version. Implement the wrapper against the real API. The wrapper's job is to expose exactly the four functions above regardless of the underlying package's shape. Output MUST be binary age (never ASCII-armored). Reject a non-X25519 identity/recipient with a clear error.
- `tsconfig.json`: `strict: true`, `target ES2022`, `module ESNext`, `moduleResolution bundler`, `lib ["ES2022","DOM"]` (the library runs in a browser). `vitest.config.ts`: node environment is fine for the crypto/tar tests.
- `web/.gitignore`: `node_modules/`, `lib/vault/testdata/`, build output.
- Add scripts to `package.json`: `"typecheck": "tsc --noEmit"`, `"test": "vitest run"`.

**Steps:**
- [ ] **Step 1 — Failing test** (`age.test.ts`): `generateKeypair()` returns an `AGE-SECRET-KEY-1…` identity and an `age1…`+58-char recipient; `recipientFor(identity)` equals that recipient; `encryptTo(bytes, [recipient])` then `decrypt(cipher, identity)` round-trips the exact bytes; `decrypt` with a DIFFERENT identity throws `AgeDecryptError`; the ciphertext begins with the age v1 binary header (`age-encryption.org/v1`) and is NOT armored (no `-----BEGIN AGE`).
- [ ] **Step 2 — Run, verify it fails** (`npm --prefix web test` → FAIL / module missing).
- [ ] **Step 3 — Implement** the scaffold + `age.ts` wrapper.
- [ ] **Step 4 — Run** `npm --prefix web run typecheck && npm --prefix web test` → PASS.
- [ ] **Step 5 — Commit:** `Add the web package and the age wrapper`.

---

### Task 3: the ustar tar reader (with traversal-hardening)

**Files:** Create `web/lib/vault/tar.ts` (read half), `web/lib/vault/tar.test.ts`.

**Why:** After decrypting, the plaintext is a POSIX ustar tar (interop contract §4). The reader turns it into typed entries and enforces the security-critical name checks.

**Interfaces (`tar.ts`):**
```ts
export type TarEntryType = "file" | "dir";
export interface TarEntry { name: string; type: TarEntryType; mode: number; bytes: Uint8Array } // bytes empty for dir
export function readTar(tar: Uint8Array): TarEntry[]   // throws UnsafeEntryError on any disallowed name/type
export class UnsafeEntryError extends Error {}
```

**Behavior:**
- Parse standard 512-byte ustar headers (name, mode octal, size octal, typeflag, prefix). Support the `prefix` field (name = `prefix + "/" + name` when prefix is set). Handle the two zero-block end marker. If a PAX extended header (`typeflag 'x'`/`'g'`) is present, either apply its `path=` override or throw a clear "PAX not supported" error — Go emits PAX only for names >100 bytes, which this vault's short kebab paths never hit, so throwing is acceptable but document it.
- **Reject (throw `UnsafeEntryError`, aborting the whole read)** any entry that: has an absolute name; has an empty name; contains a `.` or `..` path segment (split on `/`); is a symlink or any type other than regular file (`'0'`/`'\0'`) or directory (`'5'`); OR whose name does not start with `skills/`, `memory/`, `secrets/`, and is not exactly `devices.toml`. (`devices.toml` is the only allowed root-level file.)
- Directory entries have a trailing `/` and empty bytes.

**Steps:**
- [ ] **Step 1 — Failing tests** (`tar.test.ts`): given a hand-built tar containing `devices.toml`, `skills/x/SKILL.md`, `memory/y.md`, `secrets/k/meta.md`, `secrets/k/value.age`, `readTar` returns those entries with correct names/bytes; a tar with an entry named `../escape` throws `UnsafeEntryError`; a tar with a `../` mid-path (`skills/../../x`) throws; a symlink entry throws; a root-level `evil.md` (not `devices.toml`) throws; an absolute `/etc/passwd` throws. Build the malicious tars with a tiny in-test ustar writer helper (raw bytes) so the reader is tested against real headers.
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement** the reader.
- [ ] **Step 4 — Run** typecheck + tests → PASS.
- [ ] **Step 5 — Commit:** `Add the ustar tar reader with traversal-hardening`.

---

### Task 4: the ustar tar writer (deterministic)

**Files:** Modify `web/lib/vault/tar.ts` (add the write half), `web/lib/vault/tar.test.ts`.

**Why:** To push an edit the client repacks the whole tree into a ustar tar Go's `UnpackSnapshot` accepts (interop contract §4). Determinism (epoch mtime, sorted paths, dir entries, zeroed uid/gid) keeps output stable and matches the Go packer's conventions.

**Interfaces (`tar.ts`):**
```ts
export function writeTar(entries: TarEntry[]): Uint8Array
```

**Behavior:**
- Emit valid ustar headers: name (with `prefix` split if a name exceeds 100 bytes — for safety, though vault names are short), mode (files `0644`, `value.age` may keep `0600` from the source entry, dirs `0755`), size, `mtime = 0` (epoch), `uid=gid=0`, empty uname/gname, `magic "ustar\0"`, `version "00"`, correct header checksum, typeflag `'0'` for files and `'5'` for dirs. Pad each file body to a 512-byte boundary. End with two zero blocks.
- Sort entries by full path (one global lexicographic sort, matching the Go packer). Preserve `secrets/**` entry bytes exactly as given (byte-for-byte carry-through).
- Directory entries: emit a `'5'` entry with trailing `/` for each directory in the tree (the writer may derive them, or require callers to include them — pick one and test it; deriving them from file paths is simplest and least error-prone).

**Steps:**
- [ ] **Step 1 — Failing tests:** `writeTar(readTar(goFixtureTar))` yields entries that `readTar` parses back identically (round-trip through our own reader); a `value.age` entry's bytes survive write→read unchanged (compare byte arrays); entries come out globally sorted; every directory on a file's path has a `'5'` entry. (Cross-language acceptance — Go actually reading this output — is Task 8.)
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement** the writer.
- [ ] **Step 4 — Run** typecheck + tests → PASS.
- [ ] **Step 5 — Commit:** `Add the deterministic ustar tar writer`.

---

### Task 5: the vault model (parse tree, roster, edit)

**Files:** Create `web/lib/vault/model.ts`, `web/lib/vault/model.test.ts`. Add `smol-toml` to `web/package.json`.

**Why:** Turn raw tar entries into a typed vault the UI can render, parse the roster (recipients + roles, fail-closed), and produce the edited entry set for write-back while carrying secrets through.

**Interfaces (`model.ts`):**
```ts
export type Role = "full" | "no-secrets";
export interface RosterDevice { name: string; recipient: string; role: Role }
export interface Item { address: string; kind: "skill" | "memory"; hook: string; body: string;
                        frontmatter: Record<string,string>; provenance?: string; review?: string }
export interface SecretMeta { name: string; frontmatter: Record<string,string> } // metadata only; never a value
export interface Vault { items: Item[]; secrets: SecretMeta[]; roster: RosterDevice[] }
export function parseVault(entries: TarEntry[]): Vault
// Recipients for the OUTER tar re-encryption = every roster device, sorted by name (all roles).
export function outerRecipients(roster: RosterDevice[]): string[]
// Apply an edit to the raw entry set: replace the bytes of one skill/memory file, carry every other
// entry (all of secrets/**, devices.toml, other items) through unchanged. Returns the new entry set.
export function applyEdit(entries: TarEntry[], address: string, newBody: string): TarEntry[]
```

**Behavior:**
- Parse `devices.toml` with `smol-toml`; for each `[devices.<name>]` read `recipient` and optional `role`; `normalizeRole`: absent → `full`, `"full"` → `full`, `"no-secrets"` → `no-secrets`, anything else → `no-secrets` (fail closed — matches Go). Sort roster by name.
- Parse each `memory/<name>.md` and `skills/<name>/SKILL.md`: split leading `---\n…\n---\n` frontmatter (simple `key: value` lines — a minimal parser is fine; the Go frontmatter is flat) from the body. `hook` = the `description` frontmatter; `provenance` from `by`/`at`; `review` from the `review` field. Address = `skill/<name>` or `memory/<name>`.
- Parse each `secrets/<name>/meta.md` frontmatter into `SecretMeta` — NEVER read or expose `value.age` (its bytes stay only in the raw entry set for carry-through). `parseVault` must not include any secret value.
- `applyEdit`: find the target file entry by address, replace its `bytes` with the UTF-8 of `newBody` (rewriting the full file incl. frontmatter as the caller supplies it), leave all other entries — especially every `secrets/**` — byte-identical. Throw if the address is a secret (secrets are not edited from the browser).

**Steps:**
- [ ] **Step 1 — Failing tests:** `parseVault` of a fixture entry set yields the skill and memory items with correct address/hook/body, secret metadata WITHOUT any value, and a roster with the right recipients + fail-closed roles (feed a `role = "wat"` → parsed as `no-secrets`; absent role → `full`); `outerRecipients` returns all devices sorted; `applyEdit` on a memory address changes only that entry's bytes and leaves every `secrets/**` entry byte-identical (assert deep-equal on the untouched entries); `applyEdit` on a `secret/...` address throws; `parseVault` never surfaces a `value.age` byte in any `SecretMeta`.
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement.**
- [ ] **Step 4 — Run** typecheck + tests → PASS.
- [ ] **Step 5 — Commit:** `Add the vault model, roster parsing, and edit`.

---

### Task 6: the loadoutd HTTP client

**Files:** Create `web/lib/vault/client.ts`, `web/lib/vault/client.test.ts`.

**Why:** A typed wrapper over the `loadoutd` HTTP API (interop contract §1) with the bearer token, the `X-Loadout-Parent` push header, and the `409` conflict surfaced as a typed error.

**Interfaces (`client.ts`):**
```ts
export interface LoadoutdConfig { baseUrl: string; token: string } // token = the bearer, held in the browser
export interface LatestInfo { version: string; parent: string }    // "" when the store is empty
export class ConflictError extends Error { latest: string }
export class LoadoutdClient {
  constructor(cfg: LoadoutdConfig)
  getLatest(): Promise<LatestInfo>
  getSnapshot(version: string): Promise<Uint8Array>                 // raw age blob
  postSnapshot(blob: Uint8Array, parent: string): Promise<string>   // returns new version; throws ConflictError on 409
  registerDevice(name: string, recipient: string): Promise<void>    // POST /v1/devices (bootstrap roster)
  listDevices(): Promise<{ name: string; recipient: string }[]>
}
```

**Behavior:**
- All `/v1/*` requests send `Authorization: Bearer <token>`. `postSnapshot` sends `Content-Type: application/octet-stream` and `X-Loadout-Parent: <parent>` (empty string for a brand-new store), body = the raw blob. On `409` parse `{"latest":"..."}` into `ConflictError`. On other non-2xx, throw an Error naming the status and the server's `{"error":...}` message when present.
- Use `fetch` (browser and Node 20+ both provide it). Read binary bodies via `arrayBuffer()` → `Uint8Array`.

**Steps:**
- [ ] **Step 1 — Failing tests** (mock `fetch`): `getLatest` parses `{version,parent}`; `getSnapshot` returns the blob bytes and sends the bearer header; `postSnapshot` sends `X-Loadout-Parent` and octet-stream and returns the new version on 200; a 409 response makes `postSnapshot` throw `ConflictError` with `latest` set; `registerDevice` posts the right JSON; a 413/400 throws an Error carrying the server message. Assert the token appears in the `Authorization` header on every `/v1/*` call and never in a URL.
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement.**
- [ ] **Step 4 — Run** typecheck + tests → PASS.
- [ ] **Step 5 — Commit:** `Add the loadoutd HTTP client`.

---

### Task 7: the sync orchestration (pull + push with 409-reload)

**Files:** Create `web/lib/vault/sync.ts`, `web/lib/vault/sync.test.ts`.

**Why:** Tie age + tar + model + client into the two operations the UI needs: pull the current vault, and commit one edit safely. No browser-side three-way merge (interop contract §7 RISK 1): pull-right-before-edit, push-right-after, `409` → reload and re-apply, bounded.

**Interfaces (`sync.ts`):**
```ts
export interface Session { client: LoadoutdClient; identity: string }  // identity = the browser's age key
export interface PulledVault { vault: Vault; entries: TarEntry[]; version: string } // entries = raw, for carry-through
export function pull(s: Session): Promise<PulledVault>
// Edit one item: pull latest, apply the edit to THAT tree, repack, re-encrypt to the roster, push with the
// pulled version as parent. On 409, re-pull and re-apply the same edit; give up after `maxRetries` (default 3).
export function commitEdit(s: Session, address: string, newBody: string): Promise<string> // returns new version
export class SyncConflictError extends Error {} // thrown after retries are exhausted; the UI reloads
export class NotApprovedError extends Error {}  // the browser identity cannot decrypt the snapshot yet
```

**Behavior:**
- `pull`: `getLatest()`; if `version === ""` the store is empty (no snapshot yet) — return an empty vault with `version: ""`. Else `getSnapshot(version)` → `decrypt(blob, identity)` → `readTar` → `parseVault`. Keep the raw `entries` for carry-through. If `decrypt` throws `AgeDecryptError`, rethrow as `NotApprovedError` (the device is not in the recipient list yet — the UI shows the approve command).
- `commitEdit`: loop up to `maxRetries`: `pull` → `applyEdit(entries, address, newBody)` → derive `outerRecipients(vault.roster)` → `writeTar(newEntries)` → `encryptTo(tar, recipients)` → `postSnapshot(blob, pulledVersion)`; on success return the new version; on `ConflictError` retry (re-pull picks up the new head). After the loop, throw `SyncConflictError`. If the pulled store was empty (`version === ""`) push with parent `""`.
- The browser identity must be among `outerRecipients` (it is once approved) so the client can re-pull its own push.

**Steps:**
- [ ] **Step 1 — Failing tests** (a fake `LoadoutdClient` backed by an in-memory store + real age/tar/model): `pull` of a store seeded with a snapshot (encrypted to the test identity) returns the parsed vault and version; `commitEdit` changes a memory item and the next `pull` sees the new body while every secret stays byte-identical (decrypt the stored blob, confirm `value.age` bytes unchanged); a fake client that returns `409` once then succeeds makes `commitEdit` retry and succeed; a client that always `409`s makes `commitEdit` throw `SyncConflictError` after 3 tries; an empty store lets `commitEdit` push with parent `""`; a snapshot encrypted WITHOUT the session identity makes `pull` throw `NotApprovedError`. Assert `commitEdit` never decrypts a `value.age` (spy that `decrypt` is called only on the outer blob).
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement.**
- [ ] **Step 4 — Run** typecheck + tests → PASS.
- [ ] **Step 5 — Commit:** `Add the pull and commit-edit sync orchestration`.

---

### Task 8: the cross-language interop gate (the acceptance proof)

**Files:** Create `internal/interop/interop_test.go`, `web/lib/vault/interop.test.ts`, `scripts/interop-test.sh`. Add fixtures under `web/lib/vault/testdata/` (gitignored, generated).

**Why:** Unit tests prove each half in its own language. This task proves the two languages actually interoperate on the wire and that the no-secrets guarantee holds across the boundary — the real point of the phase.

**Design — three fixed DUMMY test keys** (generate once with `age-keygen`, hardcode the identity+recipient strings as constants in BOTH `interop_test.go` and `interop.test.ts`; they are test-only, never real): `FULL_A`, `FULL_B` (two full devices), `NOSEC` (the browser, no-secrets).

**Direction Go → TS** (Go writes, TS reads):
- `internal/interop/interop_test.go` `TestWriteFixturesForTS`: build a temp vault with one skill (`skills/demo/SKILL.md`), one memory (`memory/note.md`), a `devices.toml` roster `{FULL_A: full, FULL_B: full, NOSEC: no-secrets}`, and one secret `k` whose `value.age` is encrypted to FULL devices only (reuse `vault.AddSecret`/`secretRecipients`, or encrypt directly to `[FULL_A, FULL_B]`) with a known dummy plaintext. `PackSnapshot` to the roster recipients (all three). Write the blob to `web/lib/vault/testdata/go-snapshot.age`. Also write the standalone `value.age` bytes to `testdata/go-value.age`.
- `web/lib/vault/interop.test.ts` `reads the Go snapshot`: `decrypt(go-snapshot.age, NOSEC.identity)` succeeds; `readTar` + `parseVault` show the skill and memory bodies and the secret's metadata; the `secrets/k/value.age` entry is present; a raw `decrypt(value.age-bytes, NOSEC.identity)` THROWS `AgeDecryptError` (**the no-secrets guarantee, TS side**); `decrypt(go-value.age, FULL_A.identity)` returns the known plaintext.

**Direction TS → Go** (TS writes, Go reads):
- `web/lib/vault/interop.test.ts` `writes a snapshot Go can read`: start from the Go fixture's parsed entries, `applyEdit` the memory body to a new known string, `writeTar`, `encryptTo([FULL_A.recipient, FULL_B.recipient, NOSEC.recipient])`, write to `testdata/ts-snapshot.age`.
- `internal/interop/interop_test.go` `TestGoReadsTSSnapshot`: `UnpackSnapshot(ts-snapshot.age, NOSEC.identity or FULL_A.identity)` into a temp dir succeeds with NO `unsafePathErr`; the memory file holds the TS-edited string; `skills/demo/SKILL.md` is intact; `secrets/k/value.age` is byte-identical to the original Go `value.age` (**secrets carried through unchanged**); a full device (`FULL_A`) can still `DecryptSecret` `k` to the known plaintext, and the no-secrets device still cannot (**guarantee, Go side, after a browser round-trip**).

**The gate — `scripts/interop-test.sh`** (bash, runs from repo root; SANDBOX: uses only `testdata/`, temp dirs, dummy keys; touches no real home):
1. `go test ./internal/interop -run TestWriteFixturesForTS` (Go → testdata).
2. `npm --prefix web test -- interop` (TS reads Go fixtures, writes ts-snapshot.age).
3. `go test ./internal/interop -run TestGoReadsTSSnapshot` (Go reads the TS output).
Exit non-zero if any step fails. Document it in the report as the phase's acceptance gate.

**Steps:**
- [ ] **Step 1 — Generate** the three dummy keypairs; add them as constants in both test files. Write the Go fixture generator test (RED: it writes files, then the TS test that reads them does not exist yet).
- [ ] **Step 2 — Write** the TS interop test (Go→TS read assertions incl. the no-secrets throw; TS→Go write of `ts-snapshot.age`). Run `scripts/interop-test.sh` — expect the Go-reads-TS step to drive out any tar-format mismatch. Fix the tar writer/reader until Go's `UnpackSnapshot` accepts the TS tar and all assertions pass.
- [ ] **Step 3 — Run the gate** end to end: `bash scripts/interop-test.sh` → all three steps green. Prove the no-secrets guarantee fires on BOTH sides (raw `value.age` decrypt with `NOSEC` fails in TS and via `DecryptSecret` in Go).
- [ ] **Step 4 — Full suites:** `go test -race -count=1 ./...` and `npm --prefix web run typecheck && npm --prefix web test` green.
- [ ] **Step 5 — Commit:** `Prove Go<->TS snapshot interop and the no-secrets guarantee`.

---

## Self-Review Notes

- **Spec coverage (§12 Phase 8b, part 1):** the browser can pull + decrypt + parse a snapshot (Tasks 2–6), edit and push carrying secrets through (Tasks 4,5,7), reach `loadoutd` cross-origin (Task 1), and the no-secrets guarantee is proven across the language boundary (Task 8). The UI, enrollment UX, provenance/review rendering, the audit surface, and the Vercel deploy are Part 2.
- **Invariant 10 / no-secrets guarantee:** enforced structurally — the browser identity is never a `value.age` recipient (Phase 8a, already merged); this part PROVES the browser cannot decrypt a `value.age` even though it holds the ciphertext (Task 8, both directions), and never regenerates or mutates secret bytes on write-back (Tasks 5,7,8).
- **The merge decision:** the browser deliberately does NOT reimplement the Go git-based three-way merge (it has no git history). It uses pull-right-before / push-right-after / `409`-reload (interop contract §7 RISK 1). This can, under a genuine concurrent edit, ask the user to redo an edit — never silently lose data. Documented for Part 2's UI to surface.
- **Audit scope note for Part 2:** the secret-access log is device-local and NOT in the snapshot (interop contract §4); the browser therefore shows the KNOWLEDGE audit trail (provenance + review-state, which ARE in item frontmatter), not the capability/access log. This matches PLAN.md §11's own known-limitation. Part 2 renders provenance/review; server-side access logging stays a hosted-service concern.
- **Ordering:** Task 1 (CORS, independent) then the TS stack bottom-up: age (2) → tar read (3) → tar write (4) → model (5) → client (6) → sync (7) → the cross-language gate (8). Each task is independently testable; Task 8 is the whole-part acceptance proof and the focus of the final whole-branch review, which must re-run `scripts/interop-test.sh` and independently confirm the no-secrets guarantee.
- **Real-key note:** after Part 2 ships, the user runs `loadout devices approve dashboard --no-secrets` on their Mac (interop contract §8) to approve the real browser device; this part uses only dummy keys and dummy secrets.
