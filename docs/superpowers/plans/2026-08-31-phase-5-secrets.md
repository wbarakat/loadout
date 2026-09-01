# Loadout Phase 5 Implementation Plan — Secrets

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store API keys as encrypted `secret/*` items and let an agent use a key without ever reading it, via `loadout run -- <cmd>` env injection — so all plaintext keys leave disk and repo configs.

**Architecture:** A secret is two files under `secrets/<name>/`: `meta.md` (plaintext frontmatter — service, hook, optional `rotate_after`; never the value) and `value.age` (the value, age-encrypted to every enrolled device recipient, reusing Phase 4's roster). Secrets join the synced set, so ciphertext syncs but plaintext never leaves a device. Values are read only into a child process env (`run`), or to stdout under an explicit `--reveal`. Every decrypt appends to a local, gitignored access log. Approving a device re-encrypts every `value.age` to the new roster.

**Tech Stack:** Go + BurntSushi/toml + filippo.io/age (all existing). No new dependencies.

**Spec:** `/Users/waleed/loadout/PLAN.md` v3.1 — §4 (secret item), §8 invariant 10 (the secret security spine), §11 (security model), §12 Phase 5.

## Global Constraints — the secret security spine (invariant 10)

These bind EVERY task. A violation is a Critical, no matter how small.

- A secret VALUE appears only in three places: the `value.age` ciphertext at rest; a child process environment under `run`; stdout under an explicit `secret show --reveal`. Nowhere else — not a plaintext vault file, not git history, not any projection/adapter output, not an agent-context file (CLAUDE.md/AGENTS.md/render), not a log line, not an error message, not a command argument, not `--json` output, not `recall`/`context`/`list` output.
- Values are NEVER accepted as a command-line argument (leaks to shell history and `ps`). `secret add` reads the value from stdin (piped) or a secure prompt (no echo) only.
- `run` passes secrets to the child via its environment only; it never writes them to a temp file, and it must not leak them into its own stdout/stderr or the access log.
- The access log records device, tool (from `--by`/env), time (RFC3339), secret name, and the verb — never the value. It is gitignored (device-local, never synced).
- Secrets are excluded from the adapters entirely: no adapter ever reads `secrets/`, and `sync`'s projection never touches a secret. `SyncedSet` gains `secrets` for the CLOUD sync (ciphertext), but the ADAPTER projection set is separate and never includes it.
- Reuse Phase 4 crypto (age, the device roster, the atomic writers). Do not invent crypto or a new key path.
- Standard constraints: Go stdlib + toml + age; ASD-STE100; error grammar; exit 0/1/2; `--json` on every verb (but a secret value is never in the JSON); gofmt/vet clean; `go test -race -count=1 ./...` green before every commit; the commit trailer; the sandbox rule (no mutating verb against real-home-targeted manifests in tests — but secrets don't project, so this is lower-risk; still use temp HOME/LOADOUT_HOME).
- Tests use only DUMMY secret values (e.g. "test-secret-value-123"). No real credential appears in any test or fixture.

## File Structure

```
internal/vault/   secret.go (Secret type, Add/List/Get/Remove, value encrypt/decrypt), secret_test.go
                  vault.go (SyncedSet gains "secrets"; the access-log path + gitignore entry)
                  snapshot.go (secrets join the synced tar; re-encrypt-on-approve hook)
internal/cli/     secret.go (secret add/list/show/rm), run.go-verb (run.go grows the `run` verb), secretlog.go (access log)
                  doctor.go (rotate_after staleness check)
                  devices.go (approve re-encrypts secret values to the newcomer)
```

---

### Task 1: The secret store (value encryption at rest)

**Files:** Create `internal/vault/secret.go` + test. Modify `vault.go` (paths, gitignore).

**Interfaces:**
```go
type Secret struct { Name, Service, Hook, RotateAfter, By, At string } // NEVER a Value field
func AddSecret(v *Vault, name, service, hook, by string, value []byte) error
  // writes secrets/<name>/meta.md (plaintext frontmatter, no value) + value.age (age-encrypted to the roster; self if roster empty)
func ListSecrets(v *Vault) ([]Secret, error)              // metadata only, name order
func GetSecret(v *Vault) ...                                // NOT in this task — decryption lands in Task 2
func RemoveSecret(v *Vault, name string) error
func SecretExists(v *Vault, name string) bool
```
`AddSecret` validates the name (kebab-case, the address rules), refuses a duplicate, encrypts `value` with age to `roster ∪ this device` (reuse the Phase 3/4 recipient logic — factor a shared `rosterRecipients(v)` if not already shared), atomic-writes both files, records provenance. The value byte slice is zeroed after encryption where Go allows. `meta.md` frontmatter: `name, service, hook, rotate_after, by, at` — assert in a test that the value string never appears in either file on disk.

**Steps:**
- [ ] Failing tests: AddSecret writes both files; grep both files' bytes for the dummy value → ABSENT (only value.age holds ciphertext, and it is not the plaintext); ListSecrets returns metadata with no value field; duplicate refused; bad name refused; RemoveSecret deletes the dir. Assert value.age decrypts back to the dummy with the device key (using age directly in the test, proving round-trip without a production decrypt path yet).
- [ ] Implement; `secrets/` and the access log join the vault gitignore? NO — `secrets/` SYNCS (it is ciphertext), so it is NOT gitignored and IS tracked; only the access log is gitignored. Add the access-log path to gitignore. Green, commit: `Add the encrypted secret store`.

### Task 2: Decrypt + the access log + secret show

**Files:** Create `internal/cli/secretlog.go` + test. Modify `internal/vault/secret.go` (DecryptSecret), `internal/cli/secret.go` (new: add/list/show), `run.go`.

**Interfaces:**
```go
func DecryptSecret(v *Vault, name string) ([]byte, error)  // device key; missing/undecryptable → grammar error
func AppendAccessLog(v *Vault, entry AccessEntry) error     // <root>/access.log (gitignored), append-only, one JSON line per access
```
CLI: `loadout secret add <name> --service <svc> [--hook <text>] [--rotate-after <dur>]` reads the value from stdin (if piped) or a no-echo prompt; `loadout secret list [--json]` (metadata only, values never present); `loadout secret show <name>` PRINTS NOTHING by default and errors `refusing to reveal a secret without --reveal. Fix: run loadout secret show <name> --reveal, or use loadout run to inject it.`; `--reveal` prints the value to stdout and appends an access-log entry. Every decrypt (show --reveal) logs.

**Steps:**
- [ ] Failing tests: secret add via stdin pipe stores it (value absent from disk except value.age); `secret show x` without --reveal exits 1, prints no value, writes NO access-log entry; `secret show x --reveal` prints exactly the value and appends one access-log line containing the name + time + verb but NOT the value; `secret list --json` has no value field; the access log is gitignored (a Snapshot tracks nothing new). 
- [ ] Implement (stdin read must not echo; use term.ReadPassword only if a TTY, else read piped stdin — but term is x/term, a NEW dep; AVOID: read all of stdin when not a TTY, and when a TTY, print a prompt and read a line with echo disabled via the stdlib `syscall` on the fd, or simplest: require piped stdin and document it, erroring on a TTY with `pipe the value on stdin: printf %s "$VALUE" | loadout secret add <name> --service <svc>`). Choose the pipe-only approach to avoid x/term. Green, commit: `Add secret decrypt, the access log, and reveal`.

### Task 3: `loadout run` — use a key without reading it

**Files:** Create the `run` command (in `internal/cli/run_cmd.go` — do NOT collide with run.go the dispatcher; name it runcmd.go). Modify run.go dispatch. Test.

**Behavior:** `loadout run --secret <name>[=ENVVAR] [--secret <name2>] -- <cmd> [args...]` decrypts each named secret, sets it in the child process environment (as ENVVAR if `name=ENVVAR` given, else as the uppercased kebab→SNAKE name, e.g. `openai-key` → `OPENAI_KEY`), and execs the command with the parent env PLUS those vars. The value is never printed, never written to disk, never in loadout's own stdout/stderr. Each injected secret appends one access-log entry (verb `run`, the child argv[0] as the tool). The child inherits stdio directly (loadout is a transparent wrapper). loadout's exit code is the child's exit code. `--` is mandatory to separate loadout flags from the command.

**Steps:**
- [ ] Failing tests: `loadout run --secret test-key=FOO -- sh -c 'printf %s "$FOO"'` → the child prints the dummy value (captured from the child's stdout, proving injection) while loadout added nothing of its own to stdout; the access log gains a `run` entry naming the secret and `sh`, no value; a missing secret → grammar error exit 1, the child never runs; the child's exit code propagates (`run ... -- sh -c 'exit 7'` → loadout exits 7); the default env-var name derivation (`openai-key` → `OPENAI_KEY`). Assert loadout's OWN stdout/stderr never contains the value in any of these.
- [ ] Implement (os/exec, os.Environ()+injected, cmd.Stdin/out/err = os.Stdin/out/err; exit code via exec.ExitError). Green, commit: `Add loadout run for env injection`.

### Task 4: Secrets sync + re-encrypt on approve

**Files:** Modify `internal/vault/snapshot.go` (SyncedSet already gains "secrets" in Task 1's vault.go — confirm the tar includes it), `internal/vault/secret.go` (ReEncryptSecrets), `internal/cli/devices.go` (approve calls it).

**Behavior:** `secrets/` is in the SyncedSet, so value.age ciphertext syncs via the Phase 4 snapshot (the server never sees plaintext — invariant 8 already holds since value.age is ciphertext and the tar is re-encrypted anyway). `ReEncryptSecrets(v)` re-encrypts every `secrets/<name>/value.age` to the CURRENT roster (decrypt with the device key, re-encrypt to roster ∪ self, atomic replace) — called by `devices approve` and `devices approve --rotate` AFTER the roster changes and BEFORE the sync, so the newcomer receives secrets it can decrypt. A device that cannot decrypt a value.age during re-encrypt (it was not a recipient) skips that secret with a doctor-surfaced warning rather than failing.

**Steps:**
- [ ] Failing tests (two-vault + httptest, dummy secrets, temp homes): A adds a secret, A syncs, B (already approved) syncs → B decrypts the secret with B's key; a NEWLY approved device C → after approve+sync, C decrypts the secret (proving re-encrypt ran); the server blob for a secret-bearing snapshot never contains the dummy value (grep the stored blob bytes → absent). Confirm invariant 8 holds for secrets end to end.
- [ ] Implement. Green, commit: `Sync secrets and re-encrypt on approval`.

### Task 5: Rotation reminders, doctor, README, security smoke

**Files:** Modify `internal/cli/doctor.go`, `internal/cli/secret.go` (rm already? ensure `secret rm`), README, the security smoke in the report.

**Behavior:** doctor flags a secret whose `rotate_after` duration has elapsed since `at`: `secret/<name> is due for rotation (added <at>, rotate after <dur>). Fix: rotate the key at <service>, then run loadout secret add <name> --service <service> to replace it.` (add overwrites-with-confirmation, or `secret add` refuses a duplicate and a separate `secret rotate <name>` replaces the value — implement `secret rotate <name>` reading a new value from stdin, re-using AddSecret's write path, keeping the metadata, updating `at`). README: a "Secrets" section (add via stdin pipe, list, run, the never-reveal-by-default rule, rotation, the access log, the sync-is-ciphertext note). Security smoke (sandboxed, dummy values): add a secret, confirm the value is absent from every file under the vault except value.age (grep the whole vault tree), absent from git log -p, absent from a sync snapshot blob, absent from `list`/`context`/`recall`/`--json` output; `run` injects it into a child that echoes it (the ONLY place the value surfaces); the access log has entries with no values. Transcript in the report.

**Steps:**
- [ ] Failing test: a secret with `rotate_after: 1ns` (added in the past) → doctor flags it with the fix; a fresh one is not flagged. `secret rotate` replaces the value and updates `at`.
- [ ] Implement + README + smoke. Green, commit: `Add rotation reminders and document secrets`.

---

## Self-Review Notes

- Spec coverage (§12 Phase 5): encrypted secret store ✓(T1), env injection ✓(T3, the `run` verb = the spec's Mode 1), access logs ✓(T2), rotation reminders ✓(T5); success criterion "remove all plaintext keys from disk" is the whole design plus the T5 grep-the-tree smoke.
- Invariant 10 is enforced task by task and re-checked in the T5 security smoke (grep the entire vault + git history + a sync blob + every read verb's output for a dummy value → the ONLY hit is value.age ciphertext and the deliberate `run`/`--reveal` child output).
- Ordering: T1 (store) → T2 (decrypt+log+show) → T3 (run) → T4 (sync+re-encrypt) → T5 (rotation+docs+smoke). T3 depends on T2's DecryptSecret; T4 depends on T1's rosterRecipients.
- The final whole-branch review runs on fable with an ADVERSARIAL leak-hunt: it must actively try to make a secret value appear somewhere invariant 10 forbids (a crafted secret name, a value containing newlines/mark text/JSON, an error path, a --json field, a projected file, git history) and report any leak as Critical.
- Real-key migration (post-merge, WITH the user, per their choice): only after the branch is merged and the security smoke is clean, help the user move their real keys in via stdin pipes, one at a time, confirming each; never place a real key in an argument, a file, or this session's visible output.
