# Loadout Phase 2 Implementation Plan — The Agent Interface

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Loadout fully agent-native: typed adapter reports, the vault lock, provenance and review states, addresses, the query verbs (show, list, recall, context, log), undo, `--json` everywhere, `sync --dry-run`, the protocol footer, and the deferred hardening items.

**Architecture:** No new packages. `internal/vault` gains lock, addresses, provenance, and history queries. `internal/adapter` gains the Report type, the footer, and the orphan scan. `internal/cli` gains the new verbs and a small JSON output layer. Every change keeps the Phase 1 invariants: managed blocks only, never replace real files or foreign links, atomic writes, everything in history.

**Tech Stack:** Go stdlib + the existing `github.com/BurntSushi/toml`. No new dependencies.

**Spec:** `~/loadout/PLAN.md` v3 — sections 4 (items), 5 (principles), 6 (tower), 7 (interface contract), 8 (invariants), 12 (Phase 2).

## Global Constraints

- Go 1.22+. No new dependencies. Module `loadout.dev/loadout`.
- Tests never touch the real home: `t.TempDir` and `t.Setenv` (the shared helpers already guard HOME).
- All prose (comments, CLI output, README, commit messages) in ASD-STE100 Simplified Technical English.
- `gofmt -l .` prints nothing; `go vet ./...` clean; `go test -count=1 ./...` green before every commit.
- End every commit message with (blank line before it): `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`
- Error grammar (spec §7): `<address or path>: <what happened>. Fix: <exact command or action>.` New errors follow it; do not rewrite old errors outside your task.
- Exit codes: 0 ok, 1 failure, 2 usage.
- Addresses are `kind/name` everywhere: output, errors, JSON.
- The existing 51 tests must keep passing; update a test only when your task changes its contract, and say so in your report.

## File Structure

```
internal/vault/    lock.go, address.go, provenance in scaffold.go/memory.go/skill.go,
                   historyq.go (log/undo), manifest.go (warnings, version guard)
internal/adapter/  report.go, footer.go, orphans.go, changes in links.go + 3 adapters
internal/cli/      output.go (json layer), show.go, list.go, recall.go, context.go,
                   log.go, undo.go, review.go, edit.go, changes in run.go/sync.go/doctor.go/add.go
```

---

### Task 1: Typed adapter reports

**Files:** Create `internal/adapter/report.go`. Modify `adapter.go`, `links.go`, `claudecode.go`, `pi.go`, `agentsmd.go`, `internal/cli/sync.go`, and the affected tests.

**Interfaces (later tasks rely on these exactly):**

```go
// report.go
type Report struct {
	Adapter string   `json:"adapter"`
	DryRun  bool     `json:"dry_run,omitempty"`
	Applied []string `json:"applied,omitempty"` // "skill/x: linked", "memory: block written"
	Pruned  []string `json:"pruned,omitempty"`  // "skill/x: stale link removed"
	Blocked []string `json:"blocked,omitempty"` // "skill/x: a real file or a foreign link occupies PATH. Fix: move or remove PATH."
}
// adapter.go
type Adapter interface {
	Name() string
	Apply(v *vault.Vault, dry bool) (Report, error)
	Check(v *vault.Vault) []Problem
}
```

Semantics: `Apply` returns an error only for real failures (IO, damaged marks, mark scan). Blocked skills go into `Report.Blocked`, not the error. `LinkSkills` changes to `LinkSkills(skills []vault.Skill, vaultSkillsDir, dir string, dry bool) (applied, pruned, blocked []string, err error)`, where the strings already carry the address/fix grammar above. With `dry` true, nothing on disk changes; the same decisions are reported. `blockedSkillsError` is deleted. `cmdSync` prints one line per adapter — `synced <name> (N linked, M pruned)` — then one line per blocked entry to errOut, and exits 1 when any report has blocked entries or any error occurred. The snapshot still runs.

**Steps:**

- [ ] Write failing tests: update `TestClaudeCodeApplyReportsBlockedSkill` (blocked now in Report, err nil, memory still projected), `TestLinkSkillsRefusesRealDir`, `TestLinkSkillsPrunesStaleLinks` (assert pruned strings), and a new `TestSyncExitsOneOnBlocked` in `internal/cli/run_test.go` (real dir at the link path → exit 1, errOut names the address, out still says `synced pi`). Run; confirm failures.
- [ ] Implement the interface change across all three adapters and sync. Dry-run behavior gets its full test in Task 10; here only the `dry` parameter exists and is false everywhere.
- [ ] `go test ./...` green, gofmt/vet clean. Commit: `Add typed adapter reports`.

### Task 2: The vault lock

**Files:** Create `internal/vault/lock.go` + `lock_test.go`. Modify `internal/cli/add.go`, `sync.go`.

**Interfaces:** `Lock(v *Vault) (release func(), err error)` — an exclusive `syscall.Flock` on `<root>/loadout.lock` (LOCK_EX|LOCK_NB in a 100ms poll loop, 10s timeout). Timeout error: `the vault at <root> is locked by another loadout command. Fix: wait for it to finish, or remove loadout.lock if no loadout process runs.`

**Steps:**

- [ ] Failing tests:

```go
func TestLockBlocksSecondHolder(t *testing.T) {
	v := newVault(t)
	release, err := vault.Lock(v)
	if err != nil { t.Fatal(err) }
	done := make(chan error, 1)
	go func() { r2, err2 := vault.Lock(v); if err2 == nil { r2() }; done <- err2 }()
	select {
	case err2 := <-done:
		t.Fatalf("second lock must wait, got %v", err2)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	if err2 := <-done; err2 != nil { t.Fatalf("second lock must win after release: %v", err2) }
}
```

Note: flock is per-fd, so the goroutine's separate open file descriptor makes this test valid in one process.
- [ ] Implement lock.go. Wire `Lock` into `cmdAdd` and `cmdSync` (acquire after `Open`, defer release). `loadout.lock` joins the vault `.gitignore` (Task 3 owns the ignore file; here append the entry if the file exists).
- [ ] Green, clean, commit: `Add the vault lock`.

### Task 3: Manifest guard rails + history hygiene

**Files:** Modify `internal/vault/manifest.go`, `vault.go`, tests.

**Interfaces:** `LoadManifest(path) (Manifest, []string, error)` — the new middle value is warnings, one per undecoded key (via `toml.MetaData.Undecoded()`): `the manifest key <key> is unknown; loadout ignores it.` Version > 1 → hard error: `the vault manifest is version N; this loadout build understands version 1. Fix: upgrade loadout.` `Open` stores warnings on `Vault.Warnings []string`; `cmdSync`/`cmdStatus`/`cmdDoctor` print them once to errOut. `Init` writes `<root>/.gitignore` with `.DS_Store`, `render/`, `loadout.lock`; `Open` writes that file when it is absent (heals existing vaults). Stop writing `render/.gitkeep` (the dir is derived state; `Open` still re-creates it).

**Steps:**

- [ ] Failing tests: unknown key (`enable = true`) → warning naming the key, load still succeeds; `version = 2` → the hard error; fresh `Init` → `.gitignore` present with the three entries; `Open` on a vault without `.gitignore` → file appears.
- [ ] Implement; update all `LoadManifest` callers. Green, clean, commit: `Guard the manifest and clean the history`.

### Task 4: Provenance

**Files:** Modify `internal/vault/scaffold.go`, `memory.go`, `skill.go`, `internal/cli/add.go`, tests.

**Interfaces:** `AddSkill(v, name, by string)` and `AddFact(v, name, by string)`. Frontmatter gains `by: <who>`, `at: <RFC3339>`, `review: kept|draft` — `by == "human"` (the default) → `kept`; anything else → `draft`. `Fact` gains fields `By, At, Review string`; `Skill` gains the same, parsed from SKILL.md frontmatter. CLI: `loadout add skill|memory <name> [--by <who>]`, default `human`.

**Steps:**

- [ ] Failing tests: `AddFact(v, "x", "claude-code")` → file holds `by: claude-code`, `review: draft`, valid `at`; `ListFacts` surfaces the three fields; default add → `kept`. CLI test: `add memory x --by pi` → draft on disk.
- [ ] Implement. Green, clean, commit: `Record provenance on every write`.

### Task 5: Addresses, show, list

**Files:** Create `internal/vault/address.go`, `internal/cli/show.go`, `list.go`. Modify `run.go`. Tests.

**Interfaces:**

```go
// address.go
func ParseAddress(s string) (kind, name string, err error) // kinds: skill, memory
// err: `<s>: not an address. Fix: use kind/name, for example memory/my-stack.`
func ItemPath(v *Vault, kind, name string) (string, error)
// memory -> memory/<name>.md ; skill -> skills/<name>/SKILL.md ; missing -> `<addr>: no such item. Fix: run loadout list.`
```

`loadout show <addr>` prints the file's content raw, exit 0. `loadout list` prints every item, kind-then-name order, one line each: `<kind>/<name> — <hook>` (hook = description, `(no description)` when empty). Both join the run.go switch; usage text updated.

**Steps:**

- [ ] Failing tests: ParseAddress good/bad; show on a real fact prints the body; show on a missing item exits 1 with the no-such-item error; list shows both a skill and a fact in order with hooks.
- [ ] Implement. Green, clean, commit: `Add addresses, show, and list`.

### Task 6: recall and context

**Files:** Create `internal/cli/recall.go`, `context.go`. Modify `run.go`. Tests.

**Behavior:** `loadout recall <term>...` — case-insensitive substring match of every term against name + hook + body (facts) and name + hook (skills; do not read every skill body). Output: matching items in list format; no match → `no items match. Fix: run loadout list to see every item.` exit 0. `loadout context` — the compact picture, in order: `vault: <root> (N skills, M facts)`; a `memory:` section listing fact hooks; a `skills:` section listing skill hooks; a `recent:` section with the last three history subjects (`git log --format=%s -n 3`); final line `next: loadout show <kind/name> reads one item; loadout recall <terms> searches.`

**Steps:**

- [ ] Failing tests: recall finds a fact by a body word and a skill by name, misses on a bogus term with the fixed message; context contains the counts line, one hook from each section, a known history subject, and the next line.
- [ ] Implement (history subjects via a small exported `vault.RecentSubjects(v, n) ([]string, error)` using the existing git helper). Green, clean, commit: `Add recall and context`.

### Task 7: log and undo

**Files:** Create `internal/vault/historyq.go`, `internal/cli/log.go`, `undo.go`. Modify `run.go`. Tests.

**Interfaces:**

```go
type HistoryEntry struct { At, Subject string } // At = short date
func History(v *Vault, n int) ([]HistoryEntry, error)   // git log --format=%ad|%s --date=short -n
func Undo(v *Vault) error
// Undo: git checkout HEAD~1 -- . ; then Snapshot(v, "undo"). History stays forward-only.
// No parent commit -> `nothing to undo: the vault has no earlier state.`
```

`loadout log` prints `<date>  <subject>` lines (20 max). `loadout undo` runs Undo under the vault lock, then prints `restored the previous vault state` and `next: run loadout sync to project it`. Both verbs join run.go.

**Steps:**

- [ ] Failing tests: after two adds, `log` shows both subjects, newest first; `undo` removes the second fact from disk and `log` gains an `undo` entry; `undo` on a fresh vault errors with the fixed message; a foreign file in the vault survives undo only per git semantics — assert the undone fact is gone and the first fact remains.
- [ ] Implement. Green, clean, commit: `Add log and undo`.

### Task 8: The review verb

**Files:** Create `internal/cli/review.go`. Modify `run.go`. Small vault helper for rewriting the `review:` field. Tests.

**Behavior:** `loadout review` — list every `draft` item in list format plus its `by` and `at`; none → `no drafts. Every item is kept.` `loadout review keep <addr>` — set `review: kept` in the item's frontmatter (rewrite only that line, atomic write), snapshot `review keep <addr>`. `loadout review drop <addr>` — delete the item (fact file, or the whole skill folder), snapshot `review drop <addr>`, print `dropped <addr>` and `next: run loadout sync`. Both take the vault lock. Unknown address → the Task 5 no-such-item error.

**Steps:**

- [ ] Failing tests: an agent-added draft appears in `review`; `keep` flips the field on disk and empties the review list; `drop` removes a draft skill folder; drop of a missing address exits 1.
- [ ] Implement. Green, clean, commit: `Add the review verb`.

### Task 9: --json everywhere, plus help

**Files:** Create `internal/cli/output.go`. Modify every command file + `run.go`. Tests.

**Interfaces:** `Run` strips a `--json` argument from any position and passes a mode flag down. Each verb builds a typed result struct and either prints its current text or `json.MarshalIndent`. Minimum schemas (stable field names, all lowercase snake): init `{vault}`; add `{address, path, review}`; sync `{reports: [Report...], snapshot: bool}`; status `{vault, skills, facts, adapters: [{name, problems}]}`; doctor `{problems: [{source, detail, fix}], count}`; list/recall `{items: [{address, hook}]}`; show `{address, content}`; context `{vault, skills, facts, memory: [...], skills_list: [...], recent: [...]}`; log `{entries: [{at, subject}]}`; review `{drafts: [{address, hook, by, at}]}`. Also: `loadout help`, `--help`, `-h` print the usage to stdout, exit 0.

**Steps:**

- [ ] Failing tests: `status --json` parses with `json.Unmarshal` and holds the right counts; `doctor --json` on a broken vault yields count ≥ 1 with fix strings; `list --json` items carry addresses; `help` exits 0 and shows every verb; text output of every verb is byte-identical to before when `--json` is absent (spot-check status and doctor against their existing assertions).
- [ ] Implement. Green, clean, commit: `Add JSON output and help`.

### Task 10: sync --dry-run

**Files:** Modify `internal/cli/sync.go`, adapters if needed. Tests.

**Behavior:** `loadout sync --dry-run` (works with `--json` too): every adapter runs with `dry=true` — full decisions, zero writes, no snapshot, reports carry `DryRun: true`, text lines say `would sync <name> (N to link, M to prune)` and blocked lines print as usual. Exit 0 unless a real error occurs. Managed-block dry behavior: compute the block and report `memory: block would change` or `memory: up to date` by comparing against `ReadManagedBlock` — never write.

**Steps:**

- [ ] Failing tests: dry-run on a fresh vault reports the plan and leaves the target home byte-untouched (assert the CLAUDE.md file does not exist after); dry-run after a real sync reports up-to-date; a blocked path appears in the dry report; `--dry-run --json` sets `dry_run: true`.
- [ ] Implement. Green, clean, commit: `Add the sync dry run`.

### Task 11: The protocol footer

**Files:** Create `internal/adapter/footer.go`. Modify `claudecode.go`, `pi.go`, `agentsmd.go`, their tests.

**Interfaces:**

```go
// footer.go
const ProtocolFooter = `

## How to use this memory (for agents)

This content syncs from the Loadout vault. Do not edit it here; edit the vault.
- Search first: loadout recall <terms>
- Read one item: loadout show <kind/name>
- Save a fact you learned: loadout add memory <name> --by <your-tool>, write the file it names, then run: loadout sync
- See every command: loadout help`
```

The footer joins the rendered memory in all three projections: pi's block, agents-md's block, and `render/memory.md`. Introduce one helper `renderProjection(facts []vault.Fact) string` = `vault.RenderMemory(facts) + ProtocolFooter`, used by every Apply AND every Check comparison (they must stay in lockstep, or doctor reports false drift). The footer contains no loadout marks (verify in a test). agents-md keeps its skills index between the memory and the footer.

**Steps:**

- [ ] Failing tests: after sync, the pi block and render/memory.md end with the footer; Check stays clean after Apply (lockstep proof); the footer holds no mark text.
- [ ] Implement. Green, clean, commit: `Teach the protocol at every surface`.

### Task 12: Doctor orphan scan, README, smoke

**Files:** Create `internal/adapter/orphans.go`. Modify `claudecode.go`, `pi.go` (Check), `doctor` test, `README.md`.

**Behavior:** `orphanLinks(skills []vault.Skill, vaultSkillsDir, dir string) []Problem` — scan `dir` for vault-owned symlinks that no current skill explains: `stale link <path>. Fix: run: loadout sync.` Wire into both Checks. README: document the new verbs (one table matching spec §7), `--json`, `--dry-run`, `--by`, and the review flow, in ASD-STE100 prose. Sandboxed smoke: build the binary, run init → add (with `--by test-agent`) → review → review keep → sync --dry-run → sync → context → recall → log → undo → doctor under a scratch HOME/LOADOUT_HOME; record the transcript in the report. Do not touch the real home.

**Steps:**

- [ ] Failing test: delete a skill after sync; doctor now reports the stale link (this was invisible before); after sync it goes quiet.
- [ ] Implement, write the README section, run the smoke.
- [ ] Green, clean, commit: `Scan for orphans and document the interface`.

---

## Self-Review Notes

- Spec coverage (PLAN.md §12 Phase 2): json ✓(T9) dry-run ✓(T10) context/show/list/recall/log/undo/edit… **edit**: `loadout edit <addr>` spawns $EDITOR and cannot be tested non-interactively; implement in T5 as a thin verb (parse address, exec editor, print `next: run loadout sync`) with a test only for the missing-address path. lock ✓(T2) provenance+review ✓(T4,T8) footer ✓(T11) typed reports ✓(T1). Deferred Fable items: version guard + unknown keys + hygiene ✓(T3), orphan scan ✓(T12). Manifest synced-vs-device-local split stays in Phase 4 by design.
- Type consistency: Report fields (T1) are what T9's sync schema and T10's dry-run reuse; `renderProjection` (T11) must be adopted by every Check that compares blocks; ParseAddress/ItemPath (T5) serve T8's review and T5's show/edit.
- Ordering: T1 before T10; T4 before T8 and T11 (the footer names `--by`); T5 before T6/T8; T9 after T5-T8 so the retrofit covers them.
