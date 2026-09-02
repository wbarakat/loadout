# Loadout Phase 8a Implementation Plan — Device Roles

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each device a role (`full` or `no-secrets`); encrypt every `secret/*` value to full devices only, so a no-secrets device syncs the whole vault but provably cannot decrypt any secret.

**Architecture:** `devices.toml` gains a per-device `role`. The tar snapshot still encrypts to every device recipient (skills + memory reach everyone). `secretRecipients` becomes roster-full-devices ∪ self-if-full (a no-secrets device is never a secret recipient, and a no-secrets device does not add itself). `devices approve --no-secrets` sets the role; re-encrypt-on-approve honors it. This is the security foundation for the Phase 8b browser dashboard.

**Tech Stack:** Go stdlib + toml + age (all existing). No new dependencies.

**Spec:** `~/loadout/PLAN.md` — §8 invariant 10, §11, §12 Phase 8a.

## Global Constraints — the no-secrets guarantee

- A no-secrets device's key MUST NOT be a recipient of any `secret/*` value.age. This is the security invariant: after `approve --no-secrets` + re-encrypt + sync, that device's age identity cannot decrypt any secret. Prove it with a test that tries to decrypt with the no-secrets key and fails for every secret.
- The tar snapshot still reaches every device (packRecipients unchanged) — no-secrets devices still get skills, memory, and the value.age CIPHERTEXT (which they cannot open). This is correct: the snapshot syncs the whole vault; the per-value encryption is the gate.
- Backward compatible: a device with no `role` in devices.toml is `full` (existing vaults keep working; every current device stays a secret recipient).
- Reuse Phase 4/5/6 crypto and the roster. Do not invent a new mechanism. secretRecipients is the single choke point — every secret write (AddSecret, RotateSecret, ReEncryptSecrets) goes through it.
- Standard: Go stdlib + toml + age; ASD-STE100; error grammar; gofmt/vet clean; `go test -race -count=1 ./...` green before every commit; the trailer; temp-home tests; DUMMY secrets only; never the real home; the sandbox rule (no mutating verb against a real-home-targeted manifest).

## File Structure

```
internal/vault/   secret.go (secretRecipients honors roles), devices roster helpers (role field)
                  snapshot.go (ReadRoster/AddToRoster carry role; packRecipients unchanged — all devices)
internal/cli/     devices.go (approve --no-secrets; devices list shows role)
```

---

### Task 1: The role in the roster

**Files:** Modify `internal/vault/snapshot.go` (or wherever ReadRoster/AddToRoster live) + tests.

**Interfaces:** the roster entry gains a role. `devices.toml` format: `[devices.<name>] recipient = "age1..." role = "full"` (default `full` when absent). `ReadRoster` returns name→{recipient, role} (extend the existing return, or add a `ReadRosterEntries(v) (map[string]RosterEntry, error)` where `RosterEntry{Recipient, Role string}` and keep the old `ReadRoster` as recipients-only for callers that don't care). `AddToRoster(v, name, recipient, role string)` writes the role. An unknown role value → treated as `full` with a doctor-surfaced warning (fail safe: unknown = full = gets secrets; NO — fail CLOSED for secrets: an unknown role is treated as `no-secrets` so a typo never leaks a secret to an unintended device... decide: the SAFE default for an EXISTING entry with NO role is `full` (backward compat), but an explicit UNKNOWN role string is an error at write time and `no-secrets` at read time. Implement: absent role = full; a present-but-unrecognized role = no-secrets at read + a doctor warning).

**Steps:**
- [ ] Failing tests: a roster with `role = "no-secrets"` reads back that role; absent role reads as `full`; an unknown role string reads as `no-secrets` (fail closed) — round-trip through AddToRoster/ReadRosterEntries; the existing recipients-only ReadRoster still works for its callers.
- [ ] Implement; update callers. Green, commit: `Add a role to the device roster`.

### Task 2: secretRecipients honors the role

**Files:** Modify `internal/vault/secret.go` (secretRecipients) + tests.

**Behavior:** `secretRecipients(v)` = the recipients of every roster entry whose role is `full`, PLUS this device's own recipient ONLY IF this device's role is `full` (a no-secrets device does not encrypt secrets to itself). How does a device know its own role? It is this device's entry in devices.toml (matched by its own recipient); if this device is not in the roster yet (bootstrap), it is `full` (the first/owner device is full). A no-secrets device should not be writing secrets at all, but if it does, secretRecipients still excludes no-secrets devices — so a no-secrets device writing a secret would encrypt to the full devices + NOT itself, meaning it cannot read back its own write. That is acceptable (a no-secrets device is not expected to add secrets); optionally refuse AddSecret/RotateSecret on a no-secrets device with a clear error. IMPLEMENT the refusal: AddSecret/RotateSecret on a device whose own role is no-secrets → error `this device is enrolled as no-secrets and cannot add or rotate a secret. Fix: use a full device.`

**Steps:**
- [ ] Failing tests: with a roster of one full device (self) + one no-secrets device, AddSecret encrypts the value.age to the full device only — the no-secrets device's age key CANNOT decrypt it (try and assert failure), the full device CAN; a self-role-no-secrets device refuses AddSecret/RotateSecret.
- [ ] Implement. Green, commit: `Encrypt secrets to full devices only`.

### Task 3: approve --no-secrets + re-encrypt + docs + the no-secrets proof

**Files:** Modify `internal/cli/devices.go` (approve --no-secrets flag; devices list shows role), README, the proof smoke in the report.

**Behavior:** `loadout devices approve <name> [--no-secrets]` — sets the role (default full) when adding to the roster; then ReEncryptSecrets (which now honors roles) re-encrypts every secret to the full set (so a newly-approved no-secrets device is excluded, and a newly-approved full device is included); then sync. `--rotate` keeps the existing role unless --no-secrets/--full is given. `loadout devices` shows each device's role. README: a "Device roles" section — what no-secrets means, why the dashboard uses it, `approve --no-secrets`, and the guarantee (a no-secrets device cannot read any secret). The no-secrets PROOF smoke (sandboxed, dummy secrets, real loadoutd): full device A creates a secret; approve device B as --no-secrets; sync; B receives the snapshot and reads skills+memory but DecryptSecret fails for the secret AND B's raw age key cannot decrypt the value.age (prove both); a full device C approved normally CAN decrypt. Transcript in the report.

**Steps:**
- [ ] Failing test: the full approve/sync/decrypt path still works (regression); the --no-secrets path → B cannot decrypt any secret (DecryptSecret error + raw age-key decrypt failure), B CAN read skills/memory; devices list shows the roles.
- [ ] Implement + README + the proof smoke. Green, commit: `Add approve --no-secrets and prove the guarantee`.

---

## Self-Review Notes

- Spec coverage (§12 Phase 8a): device role full/no-secrets ✓(T1); secrets to full devices only ✓(T2); approve --no-secrets ✓(T3); the success criterion (a no-secrets device syncs the whole vault and cannot decrypt any secret) = the T3 proof smoke (decrypt fails with the no-secrets key, skills/memory readable).
- The no-secrets guarantee is the security invariant: secretRecipients is the single choke point; every secret write path uses it; the proof is a real decrypt-with-the-no-secrets-key failure, not an assertion.
- Ordering: T1 (role in roster) → T2 (secretRecipients honors it, the choke point) → T3 (CLI + re-encrypt + the proof). The whole-branch review runs on fable with an adversarial pass: try to make a no-secrets device decrypt a secret (via a stale value.age from before the role change, via a role typo, via a no-secrets device writing then reading, via re-encrypt skipping it) — any success is Critical.
- Migration: the user's real vault has one full device (the Mac). After merge, the dashboard (Phase 8b) enrolls as a no-secrets device. Existing secrets re-encrypt to exclude it on its approval. No change to existing behavior for full devices.
- Phase 8b (the web app) depends on this: the dashboard's browser key is a no-secrets device key, so decrypting the synced tar in the browser yields skills/memory but opaque secret ciphertext.
