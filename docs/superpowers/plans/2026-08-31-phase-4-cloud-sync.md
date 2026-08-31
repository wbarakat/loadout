# Loadout Phase 4 Implementation Plan — Cloud Sync

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One self-hostable sync server (`loadoutd`) plus client-side end-to-end encryption, device enrollment, push/pull/merge, and a watch loop — so a vault change on one machine reaches every enrolled machine, and the server never holds a plaintext byte.

**Architecture:** Each device holds an age X25519 identity (device-local, never synced). The synced set — `skills/`, `memory/`, `devices.toml` — packs into a tar at the vault's git HEAD, encrypts to every enrolled device's recipient, and lands on `loadoutd` as a versioned blob. Pull-merge is last-writer-wins per item by snapshot order, with losing versions kept in local git history. `loadout.toml` and the device key become device-local (the manifest split). The server is one Go binary: HTTP API, bearer auth, file blobs, a JSON index under flock.

**Tech Stack:** Go + BurntSushi/toml + `filippo.io/age` (the one new dependency, sanctioned by spec §13). Server: stdlib net/http, file storage. No SQLite/Postgres in v1 (decision 12's "small local index" = a JSON index under flock; revisit at scale).

**Spec:** `/Users/waleed/loadout/PLAN.md` v3.1 — §8 invariants (esp. 8: ciphertext-only server), §11 security model, §12 Phase 4, §16 decisions 11-13.

## Global Constraints

- Dependencies: stdlib + BurntSushi/toml + filippo.io/age. Nothing else.
- Tests never touch the real home; server tests use httptest or a scratch data dir; two-vault tests use two LOADOUT_HOMEs under t.TempDir.
- ASD-STE100 prose; error grammar (`<subject>: <what happened>. Fix: <action>.`); exit codes 0/1/2; `--json` on every new verb; every new verb idempotent and lock-aware.
- gofmt/vet clean, `go test -count=1 ./...` green before every commit; the standard trailer on every commit.
- Invariant 8 is absolute: no plaintext content, item names, or hooks in any request, response, log line, or server file. Blob bytes and opaque version metadata only.
- Secrets on disk (device key, remote token) are 0600 and never enter the vault git history.
- Existing vaults migrate silently and safely: `Open` heals; nothing breaks a Phase 1-3 vault.

## File Structure

```
cmd/loadoutd/main.go            server entry
internal/server/                store.go (blobs+index+flock), api.go (handlers+auth), server_test.go
internal/vault/                 device.go (identity), snapshot.go (pack/encrypt/unpack), split in vault.go
internal/remote/                client.go (HTTP), sync.go (push/pull/merge), config.go (creds 0600)
internal/cli/                   device.go, devices.go, join.go, remote.go, watch.go; sync.go grows --remote
```

---

### Task 1: Device identity

**Files:** go.mod (+age), `internal/vault/device.go` + test, `internal/cli/device.go`, run.go.

**Interfaces:** `vault.DeviceIdentity(v) (name, recipient string, err error)` — lazily creates `<root>/device.key` (age X25519 identity, 0600) and `<root>/device.name` (default: hostname, kebab-cased) on first call; both join the vault `.gitignore` (extend the gitignore content and the heal path). `vault.DeviceRecipient(v)` returns the public recipient. `loadout device [--json]` prints name + recipient. Test: key created 0600, gitignored (a Snapshot after creation does not track it), stable across calls, recipient parses as an age recipient.

- [ ] TDD → implement → green (`go get filippo.io/age`; verify the dependency tree stays small and name it in the report). Commit: `Add the device identity`.

### Task 2: The manifest split

**Files:** `internal/vault/vault.go`, migration test file.

**Behavior:** `loadout.toml`, `device.key`, `device.name`, and `remote.toml` join the vault `.gitignore`. `Open` heals: when `loadout.toml` is tracked in the vault history (a pre-Phase 4 vault), untrack it (`git rm --cached --quiet loadout.toml`) and snapshot `split the manifest`. Define `vault.SyncedSet() []string` = {"skills", "memory", "devices.toml"} — the single source of truth later tasks consume. Tests: fresh Init never tracks loadout.toml; a legacy vault (loadout.toml tracked) heals on Open exactly once (second Open is a no-op); history keeps the old tracked versions (forward-only).

- [ ] TDD → implement → green. Commit: `Split the manifest from the synced set`.

### Task 3: Snapshot pack and unpack

**Files:** `internal/vault/snapshot.go` + test.

**Interfaces:** `PackSnapshot(v) (blob []byte, headHash string, err error)` — tar (deterministic order, no timestamps that churn) of SyncedSet paths that exist, encrypted with age to every recipient in `devices.toml` (`[devices.<name>] recipient = "age1..."`); when devices.toml is absent, encrypt to this device only. `UnpackSnapshot(v, blob, dir string) error` — decrypt with the device key, untar into dir, refusing path traversal in tar entries (validate every name). Round-trip tests incl. a two-identity encrypt/decrypt-by-second-device case and a traversal-refusing tar case.

- [ ] TDD → implement → green. Commit: `Pack and unpack encrypted snapshots`.

### Task 4: The server

**Files:** `cmd/loadoutd/main.go`, `internal/server/store.go`, `api.go`, tests.

**API (bearer token on every route except /health):** `GET /health`; `POST /v1/devices {name, recipient}` (idempotent upsert; the roster here is bootstrap-only plaintext pubkeys — no content); `GET /v1/devices`; `POST /v1/snapshots` body: binary blob, headers `X-Loadout-Parent` (version string or empty) — returns `{version}`, refuses when parent != current latest (409 with `{latest}`; the client merges); `GET /v1/snapshots/latest` → `{version, parent}`; `GET /v1/snapshots/{version}` → blob bytes. Store: `<data>/blobs/<version>` files + `<data>/index.json` under flock; versions are `v<n>-<8 hex random>`; fsync on write. `loadoutd -data <dir> -addr :7777` prints a generated token on first run (stored 0600 in `<data>/token`). Log lines carry versions and byte counts only — never blob contents. httptest suite: auth rejects, upsert idempotent, parent conflict 409, round-trip bytes identical, index survives restart.

- [ ] TDD → implement → green. Commit: `Add the loadoutd server`.

### Task 5: Remote client + sync merge

**Files:** `internal/remote/` (config.go, client.go, sync.go) + tests, `internal/cli/remote.go`, sync.go, run.go.

**Behavior:** `loadout remote add <url> <token>` writes `<root>/remote.toml` 0600 (device-local, gitignored per T2); `loadout remote [--json]` shows url + last synced version (never the token). `remote.Sync(v)` under the vault lock: register this device; GET latest; if unknown remote or remote == last-synced-version → pack+push with parent; on 409 or newer remote → pull blob, unpack to temp, merge, snapshot `sync from <version>`, then pack+push. Merge rule (LWW per item, kept history): for each item path in either tree, the remote version wins when the remote snapshot is newer than the local last-synced base AND the local file is unchanged since that base; when both changed, the remote (newer snapshot order) wins and the local version stays reachable in git history; deletions propagate the same way. Track the base in `<root>/.sync-state.json` (gitignored). `loadout sync --remote` runs the local projection THEN remote.Sync; plain `sync` stays local-only. `status`/`doctor` gain one remote line when remote.toml exists (reachable? version behind/ahead?) with grammar-true failures. Tests: two vaults + httptest server — create on A, sync A, sync B (B receives), edit both sides, sync both (newer wins, loser in history), delete on A propagates to B.

- [ ] TDD → implement → green. Commit: `Sync through the remote`.

### Task 6: Enrollment

**Files:** `internal/cli/join.go`, `devices.go`, vault devices.toml helpers + tests.

**Behavior:** `loadout join <url> <token>` on a fresh machine: init-if-needed, write remote.toml, register the device, then print: `this device waits for an approval. Fix: run loadout devices approve <name> on an enrolled device, then run loadout sync --remote here.` `loadout devices [--json]` merges the server roster with devices.toml state (enrolled vs waiting). `loadout devices approve <name>` (on an enrolled device): fetch the waiting device's recipient from the server roster, add `[devices.<name>]` to devices.toml, snapshot, and sync --remote so the next snapshot encrypts to the newcomer. Tests: full three-step flow across two vault homes + httptest; an unapproved device's pull fails to decrypt (age error surfaced with grammar: `this device is not approved yet. Fix: ...`).

- [ ] TDD → implement → green. Commit: `Enroll devices with an approval`.

### Task 7: The watch loop + E2E smoke + docs

**Files:** `internal/cli/watch.go`, README, smoke in the report.

**Behavior:** `loadout watch [--interval 10s]` — loop: remote.Sync, then local projection when the vault changed; lock-aware (skips a beat when locked), exponential backoff on remote errors (max 5 min), clean exit on SIGINT, one line per action, silent beats otherwise. README: the sync section (remote add, join/approve flow, watch, the invariant that the server holds ciphertext only) + the loadoutd install note (build, -data, token). Sandboxed E2E smoke: loadoutd + two vault homes, full create/edit/conflict/delete arc through `sync --remote` and one `watch` round; transcript in the report. Real-Pi deployment stays OUT of this task: produce `dist/install-pi.md` (cross-compile command `GOOS=linux GOARCH=arm64`, scp, systemd unit example) for the human to run.

- [ ] TDD where testable (backoff/lock-skip via short intervals) → implement → green. Commit: `Add the watch loop and the sync docs`.

---

## Self-Review Notes

- Spec coverage (§12 Phase 4): accounts→bearer token single-user v1 ✓(T4); device keys ✓(T1); encrypted snapshots ✓(T3); the sync agent ✓(T7); "no plaintext ever leaves a device" ✓(T3/T4 + invariant tests); Mac-to-Pi within seconds → watch at 10s default + the Pi install doc (human-run, per the second-machine boundary).
- The Phase 3 backlog items land here: manifest split ✓(T2, decision 13); `enabled` classified device-local ✓(loadout.toml stays device-local wholesale). Doctor-over-disabled-adapters stays open — schedule with Phase 5.
- Ordering: T1→T3→T5 chain (identity→snapshot→sync); T2 independent early; T4 parallel-safe after T3's blob shape exists (execute sequentially regardless); T6 needs T5; T7 last.
- Risks named: tar traversal (T3 test), parent-conflict race (T4 409 contract), merge losing data (T5's kept-history rule + both-changed test), token/key leakage (0600 + never-print tests).
