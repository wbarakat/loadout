# Loadout Phase 8b (Part 2) Implementation Plan — Dashboard UI + Vercel Deploy

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Linear-grade browser dashboard on top of the proven Part 1 vault-client library: connect to a self-hosted `loadoutd` as a no-secrets device, browse skills/memory, view secret metadata + provenance/review, edit and keep (review) items, and deploy the app to the owner's Vercel.

**Architecture:** A Next.js App-Router app in `web/`, exported as a fully static site (`output: "export"`) — there is NO server component to the data path. Every byte of vault I/O and crypto runs in the browser via the Part 1 library (`web/lib/vault/`): the page holds the no-secrets age identity, calls `loadoutd` directly, decrypts and renders in the browser. Vercel only serves static assets. Secret values never decrypt in the browser (Phase 8a + Part 1 guarantee); secrets show as metadata only. State (loadoutd URL, bearer token, device identity, last-known version) lives in the viewer's browser storage. Styling is Tailwind; item bodies render as Markdown. Deploy targets the owner's Vercel team.

**Tech Stack:** Next.js (App Router, static export) + React + TypeScript; Tailwind CSS; `react-markdown` + `remark-gfm` for item bodies; the Part 1 library (`age-encryption`, `smol-toml`). Tests: Vitest + `@testing-library/react` + `@vitejs/plugin-react` in a jsdom/happy-dom environment (the existing lib tests keep their node environment).

**Spec:** `~/loadout/PLAN.md` — §5 (agent-is-the-user; the same clarity serves a human), §10 (provenance = the knowledge audit trail), §12 Phase 8b. **Part 1 library (the app's only data layer)** — its exports are the interface every task builds on:
- `web/lib/vault/sync.ts`: `pull(session) → {vault, entries, version}`, `commitEdit(session, address, newBody) → version`, `Session {client, identity}`, `NotApprovedError`, `SyncConflictError`.
- `web/lib/vault/client.ts`: `LoadoutdClient({baseUrl, token})`, `registerDevice(name, recipient)`, `ConflictError`.
- `web/lib/vault/age.ts`: `generateKeypair() → {identity, recipient}`, `recipientFor(identity)`.
- `web/lib/vault/model.ts`: `Vault {items, secrets, roster}`, `Item {address, kind, hook, body, frontmatter, provenance?, review?}`, `SecretMeta {name, frontmatter}`, `RosterDevice {name, recipient, role}`.

## Global Constraints

- **Secrets never decrypt or display a value in the browser.** The dashboard shows secret METADATA only (name + `meta.md` frontmatter: service, hook, rotate_after, allowed_hosts, by/at). No screen, component, network call, or state ever holds a secret value. There is no "reveal" in the dashboard. A test must assert no secret value reaches the DOM.
- **All vault logic goes through the Part 1 library.** Do NOT reimplement crypto, tar, the roster, or the push protocol in the UI. The UI calls `pull`/`commitEdit`/`registerDevice`/`generateKeypair` and renders their results. Do NOT add a Next.js API route or any server-side code that touches the vault, keys, or token — the data path is browser-only (static export).
- **No secret/token leakage.** The bearer token and age identity live only in the viewer's browser storage and in-memory; never log them, never put them in a URL/query string, never send them to Vercel or any third host. Only `loadoutd` (the user's own origin) ever receives the token.
- **Review vocabulary mirrors Go exactly:** an item's `review` is `"kept"` or `"draft"` (empty means `kept`); an agent's write starts `draft` until a human keeps it (`internal/vault/memory.go:23`, `skill.go:20`, `review.go`, `scaffold.go`). The dashboard's review action sets `review: kept` on a draft item (a "Keep" action). There is no separate reject state — mirror Go, do not invent one.
- **Standard:** ASD-STE100 in copy/comments/commit messages; `npm run typecheck`, `npm run lint` (if configured), `npm test`, and `npm run build` (the static export) all green before every commit; the trailer `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`. Tests use fixtures/mocks only — never a real `loadoutd`, never the real home, never a real key or token.

## File Structure

```
web/                     next.config.mjs (output:"export"), tailwind.config.ts, postcss.config.mjs,
                         vitest.config.ts (add jsdom env for *.tsx tests; keep node for lib), package.json
web/app/                 layout.tsx, page.tsx (the shell), globals.css
web/components/          Sidebar.tsx, ItemList.tsx, ItemDetail.tsx, SecretDetail.tsx, Editor.tsx,
                         ReviewQueue.tsx, ConnectForm.tsx, states (NotApproved.tsx, EmptyVault.tsx) + *.test.tsx
web/lib/dash/            config.ts (browser-storage config), session.ts (build Session + enroll), *.test.ts
web/lib/vault/           (Part 1 — unchanged; imported by the app)
```

The existing Part 1 lib and its 71 tests must keep passing. Configure Vitest with two environments: node for `lib/vault/**` (unchanged) and jsdom (or happy-dom) for `**/*.test.tsx` and `lib/dash/**`.

---

### Task 1: Next.js scaffold (static export) + Tailwind + the browser-config store

**Files:** Create `web/next.config.mjs`, `web/tailwind.config.ts`, `web/postcss.config.mjs`, `web/app/layout.tsx`, `web/app/globals.css`, `web/app/page.tsx` (a minimal shell for now), `web/lib/dash/config.ts`, `web/lib/dash/config.test.ts`; modify `web/package.json`, `web/vitest.config.ts`, `web/tsconfig.json`.

**Why:** Establish the app skeleton (static export, no server data path) and the small typed store that persists the connection config, without breaking the Part 1 lib tests.

**Interfaces (`config.ts`):**
```ts
export interface DashConfig { baseUrl: string; token: string; identity: string; deviceName: string; lastVersion: string }
export function loadConfig(): DashConfig | null      // null when unset or storage unavailable
export function saveConfig(c: DashConfig): void
export function clearConfig(): void
export function setLastVersion(v: string): void       // updates just the version field if a config exists
```

**Notes:**
- Add `next`, `react`, `react-dom` (pinned), `tailwindcss`+`postcss`+`autoprefixer`, and dev deps `@testing-library/react`, `@testing-library/jest-dom`, `@vitejs/plugin-react`, `jsdom` (or `happy-dom`). `next.config.mjs`: `{ output: "export" }`. `package.json` scripts: add `"dev": "next dev"`, `"build": "next build"`, keep `"typecheck"`/`"test"`.
- `config.ts` uses `localStorage` under a single key `"loadout.dash"` (JSON). EVERY access wrapped in try/catch — return `null`/no-op when `window`/`localStorage` is absent or throws (SSR/build/private-window safe). Document the security tradeoff in a comment: the token and the no-secrets age identity live in `localStorage`; this is acceptable for a personal self-host no-secrets device (the key cannot decrypt any secret), but note it.
- Vitest: split environments (node for lib/vault, jsdom for tsx/dash). Confirm `npm test` still runs all 71 Part 1 tests plus the new ones.

**Steps:**
- [ ] **Step 1 — Failing test** (`config.test.ts`, jsdom env): `loadConfig()` returns null when nothing stored; `saveConfig` then `loadConfig` round-trips a full DashConfig; `setLastVersion` updates only the version; `clearConfig` empties it; a thrown `localStorage` (stub it to throw) makes `loadConfig` return null without throwing.
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement** the scaffold + config store. Confirm `npm run build` (static export) succeeds with the shell page.
- [ ] **Step 4 — Run** `npm --prefix web run typecheck && npm --prefix web test && npm --prefix web run build` → all green (71 lib + new).
- [ ] **Step 5 — Commit:** `Scaffold the Next.js dashboard and the config store`.

---

### Task 2: the session + enroll module

**Files:** Create `web/lib/dash/session.ts`, `web/lib/dash/session.test.ts`.

**Why:** Turn a `DashConfig` into a Part 1 `Session`, and drive the one-time enroll handshake (generate key → register on the bootstrap roster → show the exact approve command the user runs on their Mac).

**Interfaces (`session.ts`):**
```ts
import type { Session } from "../vault/sync";
export function sessionFrom(c: DashConfig): Session               // { client: new LoadoutdClient({baseUrl,token}), identity: c.identity }
export function newDevice(): Promise<{ identity: string; recipient: string }>   // generateKeypair()
export function approveCommand(deviceName: string): string        // `loadout devices approve ${deviceName} --no-secrets`
// Register this device on loadoutd's bootstrap roster so the Mac's `approve` can find its recipient.
export function registerForApproval(c: DashConfig, recipient: string): Promise<void> // client.registerDevice(c.deviceName, recipient)
```

**Notes:**
- `approveCommand` returns exactly the CLI string the user runs on a full device; the enroll UI displays it copyable. The device name default is `"dashboard"` (the user may change it in the connect form).
- The enroll order (mirror interop contract §8): the browser generates a key, `registerForApproval` POSTs it to `/v1/devices` (bootstrap roster), the user runs `approveCommand` on their Mac (which adds the recipient to `devices.toml` as no-secrets, re-encrypts, and syncs), then the browser can `pull`.

**Steps:**
- [ ] **Step 1 — Failing test** (jsdom, mock the LoadoutdClient / fetch): `sessionFrom` builds a Session whose client targets the config baseUrl and whose identity is the config identity; `newDevice` returns an `AGE-SECRET-KEY-1…` + `age1…` pair (real generateKeypair); `approveCommand("dashboard")` equals `loadout devices approve dashboard --no-secrets`; `registerForApproval` calls `registerDevice` with the device name + recipient.
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement.**
- [ ] **Step 4 — Run** typecheck + tests → PASS.
- [ ] **Step 5 — Commit:** `Add the dashboard session and enroll helpers`.

---

### Task 3: the Connect / Settings flow + the not-approved and empty states

**Files:** Create `web/components/ConnectForm.tsx`, `web/components/states/NotApproved.tsx`, `web/components/states/EmptyVault.tsx`, and their `*.test.tsx`.

**Why:** The first-run experience: enter the loadoutd URL + token, generate/import a device key, register, and test the connection — plus the clear states when the device is not yet approved or the vault is empty.

**Interfaces:**
```ts
// ConnectForm: controlled inputs for baseUrl, token, deviceName; a "Generate key" button (newDevice) OR an
// "Import identity" textarea; shows the recipient + copyable approveCommand after a key exists; a "Register + Connect"
// button that: saveConfig, registerForApproval, then pull(). onConnected(vault, version) fires on a successful pull.
export function ConnectForm(props: { initial?: DashConfig; onConnected: (r: {vault: Vault; version: string}) => void }): JSX.Element
// NotApproved: shows the recipient + the exact approveCommand + a "Retry connection" button. Given when pull throws NotApprovedError.
export function NotApproved(props: { recipient: string; deviceName: string; onRetry: () => void }): JSX.Element
// EmptyVault: shown when pull returns an empty vault (version ""). Explains the Mac has not synced yet.
```

**Behavior:**
- On "Register + Connect": `saveConfig(cfg)`, `registerForApproval(cfg, recipient)`, then `pull(sessionFrom(cfg))`. On success → `onConnected`. On `NotApprovedError` → render `NotApproved` with the recipient + approve command (the device is registered but not yet approved on the Mac). On a network/other error → an inline error naming the fix (check the URL/token, is loadoutd running + CORS set to this origin?).
- The approve command and the recipient are copyable (a copy button).

**Steps:**
- [ ] **Step 1 — Failing tests** (jsdom + @testing-library/react, mocked session/pull): filling the form + Generate key shows the recipient and the exact approve command; "Register + Connect" calls registerDevice then pull and fires onConnected on success; a mocked `NotApprovedError` renders the NotApproved state with the approve command; a mocked network error shows the fix-naming message; config is persisted (saveConfig called with the entered values). Assert the token is never rendered into any link href or logged.
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement.**
- [ ] **Step 4 — Run** typecheck + tests + build → PASS.
- [ ] **Step 5 — Commit:** `Add the connect flow and the not-approved and empty states`.

---

### Task 4: the app shell + the browse list

**Files:** Create `web/components/Sidebar.tsx`, `web/components/ItemList.tsx`; modify `web/app/page.tsx` (the shell that wires config → connect-or-browse), + `*.test.tsx`.

**Why:** The main workspace: a Linear-style sidebar (Skills / Memory / Secrets / Review / Settings) and a searchable item list, driven by a single `pull`.

**Behavior:**
- `page.tsx` (a client component): on mount, `loadConfig()`; if absent → render `ConnectForm`; else `pull(sessionFrom(cfg))` and render the workspace (Sidebar + ItemList + detail). Handle `NotApprovedError` (→ NotApproved), empty (→ EmptyVault), errors (→ an error panel with a reconnect/settings link). Keep the pulled `{vault, entries, version}` in React state; the raw `entries` feed `commitEdit` later.
- `Sidebar`: sections Skills, Memory, Secrets, Review (count of `draft` items), Settings. Selecting one filters the list.
- `ItemList`: the selected kind's items (skills/memory from `vault.items` by `kind`; secrets from `vault.secrets`), each row = address + hook + a `draft` badge when `review === "draft"`. A search box filters by address/hook/body (client-side, case-insensitive). Selecting a row opens the detail (Task 5).

**Steps:**
- [ ] **Step 1 — Failing tests** (mock `pull` to return a fixture Vault with a skill, a memory (one `draft`), and a secret): the workspace renders the three groups; selecting "Memory" lists the memory item; the search box filters to matching items; the Review section shows the draft count; a secret row appears under Secrets (metadata name only). With no config, the shell renders ConnectForm instead.
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement.**
- [ ] **Step 4 — Run** typecheck + tests + build → PASS.
- [ ] **Step 5 — Commit:** `Add the app shell, sidebar, and searchable item list`.

---

### Task 5: item detail + secret detail (metadata only)

**Files:** Create `web/components/ItemDetail.tsx`, `web/components/SecretDetail.tsx`, + `*.test.tsx`. Add `react-markdown` + `remark-gfm` to package.json.

**Why:** Read an item in full (skills/memory) with its knowledge audit trail (provenance + review-state); read a secret as metadata only, with the value structurally absent.

**Behavior:**
- `ItemDetail` (skill/memory): renders `item.hook`, the `item.body` as GitHub-flavored Markdown (react-markdown + remark-gfm), a provenance line (`by · at` from frontmatter), and a review badge (`kept`/`draft`). An "Edit" and (for a `draft`) a "Keep" action surface here (wired in Task 6).
- `SecretDetail`: renders the secret name + a metadata table from `secret.frontmatter` (service, hook, rotate_after, allowed_hosts, by, at) and a clear notice: "The value is stored encrypted and cannot be read here. Use a full device or `loadout secret show` on the CLI." NO value field, no reveal control, no fetch of `value.age`.

**Steps:**
- [ ] **Step 1 — Failing tests:** a skill/memory renders its body (assert Markdown output, e.g. a heading/list element), the provenance line, and the correct review badge; `SecretDetail` renders every metadata field present and the not-readable notice; CRITICAL assertion — `SecretDetail` given a `SecretMeta` renders NO secret value and the component never receives/reads a `value.age` (the model has no value field, so assert the rendered DOM contains only metadata + the notice, and there is no reveal/fetch control).
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement.**
- [ ] **Step 4 — Run** typecheck + tests + build → PASS.
- [ ] **Step 5 — Commit:** `Add item detail and secret metadata detail`.

---

### Task 6: edit + keep (review) with conflict handling

**Files:** Create `web/components/Editor.tsx`, `web/components/ReviewQueue.tsx`; wire actions into `ItemDetail`/`page.tsx`; + `*.test.tsx`.

**Why:** The write path: edit a skill/memory body and save through `commitEdit`; keep a draft item (set `review: kept`); handle a concurrent-edit conflict without data loss.

**Behavior:**
- `Editor`: a textarea seeded with the item body; Save → `commitEdit(session, address, newBody)`; on success update the in-memory vault + `setLastVersion(newVersion)` and re-render the detail; on `SyncConflictError` → a toast "The vault changed on another device. Reloading the latest." then re-`pull` and re-open the item (the user re-applies the edit — no silent loss); on `NotApprovedError` → route to NotApproved. Edit is offered ONLY for skills/memory — never for a secret.
- Keep action (in ItemDetail and ReviewQueue): rewrite the item's serialized file so the `review` frontmatter line becomes `kept` (compose the new body = frontmatter with `review: kept` + the unchanged body; a helper `withReviewKept(item)` in `lib/dash`), then `commitEdit(session, item.address, newSerialized)`. Same conflict handling. Mirror Go's `SetReviewKept` semantics (only that one line changes; add a `review: kept` line if none exists).
- `ReviewQueue`: lists all `draft` items with a Keep button each; keeping one removes it from the queue.

**Steps:**
- [ ] **Step 1 — Failing tests** (mock `commitEdit`): saving an edit calls `commitEdit` with the item address + the new body and updates the shown body + stored version; a `SyncConflictError` from commitEdit shows the reload toast and triggers a re-pull; the Keep action calls `commitEdit` with a body whose frontmatter `review` line is `kept` and the body otherwise byte-identical; the Editor/Keep controls are ABSENT for a secret; ReviewQueue lists only `draft` items and removes one after Keep.
- [ ] **Step 2 — Run, verify it fails.**
- [ ] **Step 3 — Implement** (`withReviewKept` mirrors `internal/vault/review.go`).
- [ ] **Step 4 — Run** typecheck + tests + build → PASS.
- [ ] **Step 5 — Commit:** `Add editing, keep-review, and conflict handling`.

---

### Task 7: design polish + docs + the manual smoke checklist

**Files:** Modify the components' Tailwind classes/layout for a Linear-grade look; `web/app/globals.css`; add `web/README.md` (or a section in the repo README) "Dashboard"; add `docs/dashboard-smoke.md` (the manual smoke checklist). No new logic.

**Why:** Make it feel like Linear/Resend — calm, dense-but-legible, keyboard-friendly — and document how to run it end to end against a real `loadoutd`.

**Behavior:**
- Visual pass: a two-pane layout (sidebar + list + detail), system font stack, restrained borders/spacing, a clear selected-row state, accessible focus rings, empty/loading/error states that read well, a keyboard focusable search. Light/dark via `prefers-color-scheme`. No functional change — keep all tests green.
- README "Dashboard": the end-to-end setup — run `loadoutd` locally; put an HTTPS URL in front via `portless`; start `loadoutd` with `-cors-origin=<the Vercel URL>`; open the dashboard, generate the key, run `loadout devices approve dashboard --no-secrets` on the Mac, connect.
- `docs/dashboard-smoke.md`: a numbered manual smoke against a real local `loadoutd` with a seeded DUMMY vault (connect → not-approved → approve on Mac → connect → browse a skill/memory → confirm a secret shows metadata only → edit a memory → confirm the Mac sees the edit via `loadout` CLI → keep a draft → confirm on the Mac). This is the human acceptance test at deploy time.

**Steps:**
- [ ] **Step 1** — Apply the visual pass across components; verify `npm test` stays green (update only assertions that keyed on removed placeholder text, never to weaken a behavior check).
- [ ] **Step 2** — Write the README section + `docs/dashboard-smoke.md`.
- [ ] **Step 3 — Run** typecheck + tests + build → PASS.
- [ ] **Step 4 — Commit:** `Polish the dashboard and document the setup and smoke test`.

---

### Task 8: deploy to Vercel — CONTROLLER + USER, not a subagent

**This task is NOT dispatched to an implementer subagent.** It is an outward-facing publish to the user's Vercel account and depends on the user's live `loadoutd`; the controller performs it directly, with the user, after Tasks 1–7 are merged and green.

Sequence (controller):
- Confirm `npm --prefix web run build` produces a clean static export.
- Deploy `web/` to the owner's Vercel team as project `loadout` via the Vercel MCP; capture the production URL.
- Tell the user the deployment URL so they start `loadoutd` with `-cors-origin=<that URL>` (and a portless HTTPS front), then run `loadout devices approve dashboard --no-secrets` after generating the key in the dashboard.
- Walk the `docs/dashboard-smoke.md` checklist together against their real `loadoutd`. Fix any issue found, redeploy.
- This is a stop-and-confirm point: do not deploy without the user's go-ahead at that moment (the Vercel account + the CORS origin are theirs to set).

---

## Self-Review Notes

- **Spec coverage (§12 Phase 8b, the UI half):** connect as a no-secrets device (T2/T3), full browse (T4), read with the knowledge audit trail = provenance + review (T5), edit + keep/review (T6), secrets shown as metadata only with no readable value (T5, the Global Constraint), deploy to the owner's Vercel (T8). The success criterion "no key value ever reaches the browser" is enforced by Part 1 (structural) and re-asserted in T5's test.
- **Invariant 10 / no-secrets:** the dashboard has no code path that decrypts a `value.age`; secrets are metadata only; the write path carries secret ciphertext through via the Part 1 library (never touched by the UI). T5 asserts no value in the DOM; the whole-branch review re-checks that no component fetches or renders a secret value.
- **The audit note:** the browser shows the KNOWLEDGE audit trail (provenance + review-state, which are in the synced item frontmatter). The capability/access log is device-local and NOT synced (Part 1 interop contract §4); it is intentionally out of scope for the browser and stays a hosted-service concern (PLAN.md §11). Do not build an access-log view.
- **Static export is the security posture:** `output: "export"` means Vercel runs no server code on the data path — the token and key never leave the browser except to the user's own `loadoutd`. Do not add API routes.
- **Ordering:** T1 (scaffold + config) → T2 (session/enroll) → T3 (connect + states) → T4 (shell + browse) → T5 (detail) → T6 (edit/review) → T7 (polish + docs) → T8 (deploy, controller+user). Each of T1–T7 is independently testable and reviewable; the final whole-branch review runs on the most capable model with a no-secret-value-in-the-DOM adversarial pass and a `npm run build` check, then Phase 8b (Part 1 + Part 2) merges to main as one, then T8 deploys.
- **Real-infra note:** T8 needs the user's `loadoutd` reachable over HTTPS (portless) with `-cors-origin` set to the deployed URL, and the browser device approved on the Mac — all the user's actions, walked together at deploy time.
