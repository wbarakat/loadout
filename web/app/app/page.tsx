"use client";

/**
 * The dashboard's app shell: config → connect-or-browse.
 *
 * On mount, this loads the saved connection config. With none saved, it
 * renders `ConnectForm`. With one saved, it pulls the vault and renders the
 * workspace: `Sidebar` + `ItemList` + a detail pane. A selected skill or
 * memory shows `ItemDetail`; a selected secret shows `SecretDetail`
 * (metadata only — a secret value never reaches this pane).
 *
 * This file also owns the write path: Edit opens `Editor` for a skill or
 * memory item; Keep (from `ItemDetail` or `ReviewQueue`) rewrites the
 * item's `review` line to `kept`. Both splice the item's RAW file text —
 * read from the last-pulled `entries` via `rawFileFor`
 * (`../lib/dash/review.js`) — rather than reserializing the parsed
 * `Item.frontmatter` map, so no byte inside the frontmatter block (extra
 * spacing, a blank line, a comment-like line, key order) is ever
 * normalized away. The spliced full file then goes through `commitEdit`
 * (`../lib/vault/sync.js`), which does its own pull-latest → apply →
 * re-encrypt → push internally — this file never builds tar entries for
 * the PUSH itself, and never pre-pulls before an edit beyond the read
 * above. On success it re-pulls to refresh the shown vault. On a
 * `SyncConflictError` it shows a reload message and re-pulls, discarding
 * nothing silently — the user re-applies their edit against the
 * refreshed tree. On a `NotApprovedError` it routes to the `NotApproved`
 * screen, same as the initial pull.
 *
 * Edit and Keep are offered only through `ItemDetail`/`ReviewQueue`, and
 * both only ever hold a skill/memory `Item` — never a secret. `renderMain`
 * below never passes an item from `vault.secrets` to either.
 */
import { useEffect, useState, type JSX } from "react";
import { ConnectForm } from "../../components/ConnectForm.js";
import { Editor } from "../../components/Editor.js";
import { ItemDetail } from "../../components/ItemDetail.js";
import { ItemList, type ListRow } from "../../components/ItemList.js";
import { ReviewQueue } from "../../components/ReviewQueue.js";
import { SecretDetail } from "../../components/SecretDetail.js";
import { Sidebar, type Section, type SectionCounts } from "../../components/Sidebar.js";
import { EmptyVault } from "../../components/states/EmptyVault.js";
import { NotApproved } from "../../components/states/NotApproved.js";
import { clearConfig, loadConfig, setLastVersion, type DashConfig } from "../../lib/dash/config.js";
import { applyRawEdit, rawFileFor, withReviewKept } from "../../lib/dash/review.js";
import { sessionFrom } from "../../lib/dash/session.js";
import { recipientFor } from "../../lib/vault/age.js";
import type { Item, Vault } from "../../lib/vault/model.js";
import {
  commitEdit,
  NotApprovedError,
  pull,
  SyncConflictError,
  type PulledVault,
} from "../../lib/vault/sync.js";

/** The shell's state machine. Only one of these is ever active at a time. */
type Phase =
  | { kind: "loading" }
  | { kind: "no-config" }
  | { kind: "not-approved"; recipient: string; deviceName: string }
  | { kind: "empty" }
  | { kind: "error"; message: string }
  | { kind: "ready" };

const EMPTY_VAULT: Vault = { items: [], secrets: [], roster: [] };

/** What `commitAndRefresh` settled on, so its caller can decide what the
 * UI does next (in particular, whether an open `Editor` should close).
 * `"failed"` covers both a `NotApprovedError`/generic error on the push
 * itself AND a re-pull that failed after either a successful push or a
 * conflict — either way `handlePullFailure` has already switched `phase`
 * away from `"ready"`, so the caller has nothing left to do. */
type CommitOutcome = "success" | "conflict" | "failed";

/** Shown after a `SyncConflictError`: no edit is lost silently — the user
 * sees the latest tree and re-applies their change. */
const CONFLICT_MESSAGE =
  "The vault changed on another device. Reloading the latest.";

/** The last path segment of an address — its display name. For example,
 * `"skill/widget-fixer"` shows as `"widget-fixer"`. */
function nameFromAddress(address: string): string {
  const slash = address.lastIndexOf("/");
  return slash === -1 ? address : address.slice(slash + 1);
}

function describeError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** Rows for one section, built straight from the pulled `Vault`. A secret
 * row never carries a value — the model has none to carry (`SecretMeta`
 * has no value field), so there is nothing to leak here. */
function rowsFor(section: Section, vault: Vault): ListRow[] {
  switch (section) {
    case "skills":
      return vault.items
        .filter((item) => item.kind === "skill")
        .map((item) => ({
          address: item.address,
          title: nameFromAddress(item.address),
          hook: item.hook,
          draft: item.review === "draft",
        }));
    case "memory":
      return vault.items
        .filter((item) => item.kind === "memory")
        .map((item) => ({
          address: item.address,
          title: nameFromAddress(item.address),
          hook: item.hook,
          draft: item.review === "draft",
        }));
    case "review":
      return vault.items
        .filter((item) => item.review === "draft")
        .map((item) => ({
          address: item.address,
          title: nameFromAddress(item.address),
          hook: item.hook,
          draft: true,
        }));
    case "secrets":
      return vault.secrets.map((secret) => ({
        address: `secret/${secret.name}`,
        title: secret.name,
        hook: secret.frontmatter["hook"] ?? secret.frontmatter["service"] ?? "",
        draft: false,
      }));
    case "settings":
      return [];
  }
}

/** Renders the detail pane for the selected address: `ItemDetail` for a
 * skill/memory (with Edit and, for a draft, Keep wired in), `SecretDetail`
 * (metadata only, no value, no Edit/Keep) for a secret. */
/** The placeholder shown in the detail pane before any row is selected, or
 * when a selected address no longer resolves to an item. Centered in the
 * pane, not just left-aligned at its top. */
function emptySelection(): JSX.Element {
  return (
    <div className="flex h-full items-center justify-center">
      <p className="text-sm text-slate-400 dark:text-slate-500">Select an item to view it.</p>
    </div>
  );
}

function renderDetail(
  vault: Vault,
  selectedAddress: string | undefined,
  onEdit: (item: Item) => void,
  onKeep: (item: Item) => Promise<void>,
): JSX.Element {
  if (selectedAddress === undefined) {
    return emptySelection();
  }

  if (selectedAddress.startsWith("secret/")) {
    const name = selectedAddress.slice("secret/".length);
    const secret = vault.secrets.find((s) => s.name === name);
    if (secret === undefined) {
      return emptySelection();
    }
    return <SecretDetail secret={secret} />;
  }

  const item = vault.items.find((i) => i.address === selectedAddress);
  if (item === undefined) {
    return emptySelection();
  }
  return <ItemDetail item={item} onEdit={onEdit} onKeep={onKeep} />;
}

function countsFor(vault: Vault): SectionCounts {
  return {
    skills: vault.items.filter((item) => item.kind === "skill").length,
    memory: vault.items.filter((item) => item.kind === "memory").length,
    secrets: vault.secrets.length,
    review: vault.items.filter((item) => item.review === "draft").length,
  };
}

export default function Home(): JSX.Element {
  const [config, setConfig] = useState<DashConfig | null>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  const [pulled, setPulled] = useState<PulledVault | null>(null);
  const [section, setSection] = useState<Section>("skills");
  const [query, setQuery] = useState("");
  const [selectedAddress, setSelectedAddress] = useState<string | undefined>(undefined);
  const [editingAddress, setEditingAddress] = useState<string | undefined>(undefined);
  const [saving, setSaving] = useState(false);
  const [toast, setToast] = useState<string | undefined>(undefined);

  /** Routes any pull failure to the right phase: not-approved (deriving
   * the recipient to show), or the generic error panel. Shared by the
   * initial pull and every re-pull that runs after a commit or a
   * conflict — a re-pull failing must never leave the UI silently stuck
   * (for example a permanent "Saving"), so each re-pull site below
   * funnels its failure through this same routing. */
  async function handlePullFailure(err: unknown, cfg: DashConfig): Promise<void> {
    if (err instanceof NotApprovedError) {
      try {
        const recipient = await recipientFor(cfg.identity);
        setPhase({ kind: "not-approved", recipient, deviceName: cfg.deviceName });
      } catch (recipientErr) {
        setPhase({ kind: "error", message: describeError(recipientErr) });
      }
    } else {
      setPhase({ kind: "error", message: describeError(err) });
    }
  }

  /** Pulls with `cfg`, and routes to the right phase: ready, empty,
   * not-approved, or a generic error. */
  async function attemptPull(cfg: DashConfig): Promise<void> {
    try {
      const result = await pull(sessionFrom(cfg));
      setPulled(result);
      setPhase(result.version === "" ? { kind: "empty" } : { kind: "ready" });
    } catch (err) {
      await handlePullFailure(err, cfg);
    }
  }

  useEffect(() => {
    const cfg = loadConfig();
    setConfig(cfg);
    if (cfg === null) {
      setPhase({ kind: "no-config" });
      return;
    }
    void attemptPull(cfg);
    // Runs once, on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /** Re-pulls with the current config and refreshes the shown vault, in
   * place — used after a successful edit/keep, and after a conflict. */
  async function refreshPull(cfg: DashConfig): Promise<void> {
    const result = await pull(sessionFrom(cfg));
    setPulled(result);
  }

  /**
   * Pushes one edit through `commitEdit` and settles the outcome:
   * - success: records the new version, re-pulls to show it.
   * - `SyncConflictError`: shows the reload message, re-pulls — no local
   *   edit is applied on top of stale state, so nothing is silently lost.
   *   The CALLER decides what happens to any in-progress UI (Minor (a):
   *   an open `Editor` must stay open, with the user's typed prose
   *   intact, not be closed out from under them).
   * - `NotApprovedError`: routes to the `NotApproved` screen.
   * - anything else, INCLUDING a re-pull that itself fails (Minor (b)):
   *   the generic error panel. A re-pull failure is never left
   *   unhandled — that would strand a caller's "saving" flag forever.
   */
  async function commitAndRefresh(address: string, newBody: string): Promise<CommitOutcome> {
    if (config === null) return "failed";
    const cfg = config;
    try {
      const newVersion = await commitEdit(sessionFrom(cfg), address, newBody);
      setLastVersion(newVersion);
      try {
        await refreshPull(cfg);
        return "success";
      } catch (refreshErr) {
        await handlePullFailure(refreshErr, cfg);
        return "failed";
      }
    } catch (err) {
      if (err instanceof SyncConflictError) {
        setToast(CONFLICT_MESSAGE);
        try {
          await refreshPull(cfg);
        } catch (refreshErr) {
          await handlePullFailure(refreshErr, cfg);
          return "failed";
        }
        return "conflict";
      }
      await handlePullFailure(err, cfg);
      return "failed";
    }
  }

  function handleEdit(item: Item): void {
    setToast(undefined);
    setEditingAddress(item.address);
  }

  function handleCancelEdit(): void {
    setEditingAddress(undefined);
  }

  // Both handlers below read the item's RAW file text from `pulled.entries`
  // — the exact bytes this page last pulled — and splice it (never
  // reserialize `Item.frontmatter`, which would normalize/drop bytes the
  // parsed map does not keep; see `../lib/dash/review.js`). `commitEdit`
  // re-pulls fresh internally before it pushes, so a change to the vault
  // between this page's last pull and the push lands as a `409` — the
  // small window where `pulled.entries` is momentarily stale is exactly
  // what the existing conflict handling below (`SyncConflictError` →
  // reload) already covers; it is the same edit model `commitEdit` itself
  // documents, not a new risk this raw-read introduces.

  async function handleSaveEdit(item: Item, newProse: string): Promise<void> {
    if (pulled === null) return;
    setSaving(true);
    try {
      const raw = rawFileFor(pulled.entries, item.address);
      const outcome = await commitAndRefresh(item.address, applyRawEdit(raw, newProse));
      // Minor (a): on a conflict, the Editor stays open and the user's
      // typed prose is untouched — it lives in the Editor's own state,
      // which this component never resets, since the Editor component
      // itself never unmounts here (only its `item` prop's content may
      // change after the re-pull above). Only a genuine success closes
      // it; "failed" already moved `phase` away from "ready", so the
      // Editor is gone regardless of what this does.
      if (outcome === "success") {
        setEditingAddress(undefined);
      }
    } finally {
      // Minor (b): whatever happened above — success, conflict, or a
      // double fault where the re-pull itself failed — "Saving" must
      // never stick.
      setSaving(false);
    }
  }

  async function handleKeep(item: Item): Promise<void> {
    if (pulled === null) return;
    const raw = rawFileFor(pulled.entries, item.address);
    await commitAndRefresh(item.address, withReviewKept(raw));
  }

  /** `r` is the FULL `PulledVault` `ConnectForm` just pulled — vault,
   * entries, AND version. Storing it as-is (not just `{vault, version}`)
   * means `pulled.entries` is populated immediately on connect, so an
   * Edit or Keep attempted in this same session — before any later
   * re-pull — has the raw file bytes `rawFileFor` needs, instead of
   * throwing "no such item in the pulled vault entries". */
  function handleConnected(r: PulledVault): void {
    setConfig(loadConfig());
    setPulled(r);
    setSection("skills");
    setSelectedAddress(undefined);
    setPhase(r.version === "" ? { kind: "empty" } : { kind: "ready" });
  }

  function handleRetry(): void {
    if (config === null) return;
    setPhase({ kind: "loading" });
    void attemptPull(config);
  }

  function handleReconnect(): void {
    clearConfig();
    setConfig(null);
    setPulled(null);
    setPhase({ kind: "no-config" });
  }

  function handleSelectSection(next: Section): void {
    setSection(next);
    setQuery("");
    setSelectedAddress(undefined);
    setEditingAddress(undefined);
  }

  if (phase.kind === "loading") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-white dark:bg-slate-950">
        <p className="text-sm text-slate-400 dark:text-slate-500">Loading…</p>
      </div>
    );
  }

  if (phase.kind === "no-config") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-white p-6 dark:bg-slate-950">
        <ConnectForm onConnected={handleConnected} />
      </div>
    );
  }

  if (phase.kind === "not-approved") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-white p-6 dark:bg-slate-950">
        <NotApproved
          recipient={phase.recipient}
          deviceName={phase.deviceName}
          onRetry={handleRetry}
        />
      </div>
    );
  }

  if (phase.kind === "empty") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-white p-6 dark:bg-slate-950">
        <EmptyVault onRetry={handleRetry} />
      </div>
    );
  }

  if (phase.kind === "error") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-white p-6 dark:bg-slate-950">
        <div className="mx-auto max-w-md space-y-4 rounded-xl border border-red-200 bg-red-50 p-6 text-center dark:border-red-900/50 dark:bg-red-500/10">
          <h2 className="text-lg font-semibold text-red-800 dark:text-red-300">
            Something went wrong
          </h2>
          <p className="text-sm text-red-700 dark:text-red-400">{phase.message}</p>
          <button
            type="button"
            onClick={handleReconnect}
            className="ld-focus rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-slate-700 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white"
          >
            Reconnect / Settings
          </button>
        </div>
      </div>
    );
  }

  // phase.kind === "ready"
  const vault = pulled?.vault ?? EMPTY_VAULT;
  const counts = countsFor(vault);
  const rows = rowsFor(section, vault);
  const draftItems = vault.items.filter((item) => item.review === "draft");
  const editingItem =
    editingAddress === undefined
      ? undefined
      : vault.items.find((i) => i.address === editingAddress);

  return (
    <div className="flex h-screen flex-col bg-white text-slate-900 dark:bg-slate-950 dark:text-slate-100">
      <header className="flex shrink-0 items-center gap-2 border-b border-slate-200 px-4 py-2.5 dark:border-slate-800">
        <span className="text-sm font-semibold tracking-tight">Loadout</span>
        {config !== null ? (
          <>
            <span aria-hidden="true" className="text-slate-300 dark:text-slate-700">
              ·
            </span>
            <span className="truncate text-xs text-slate-500 dark:text-slate-400">
              {config.baseUrl}
            </span>
          </>
        ) : null}
      </header>
      {toast !== undefined ? (
        <div className="flex items-center justify-between gap-4 border-b border-amber-300 bg-amber-50 px-4 py-2 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-500/10 dark:text-amber-200">
          <span>{toast}</span>
          <button
            type="button"
            onClick={() => setToast(undefined)}
            className="ld-focus shrink-0 rounded text-xs font-medium underline"
          >
            Dismiss
          </button>
        </div>
      ) : null}
      <div className="flex flex-1 overflow-hidden">
        <Sidebar active={section} counts={counts} onSelect={handleSelectSection} />
        {section === "settings" ? (
          <div className="flex-1 overflow-y-auto p-6">
            <ConnectForm initial={config ?? undefined} onConnected={handleConnected} />
          </div>
        ) : section === "review" ? (
          <div className="flex-1 overflow-y-auto p-6">
            <ReviewQueue drafts={draftItems} onKeep={handleKeep} />
          </div>
        ) : (
          <>
            <div className="w-80 shrink-0 border-r border-slate-200 dark:border-slate-800">
              <ItemList
                rows={rows}
                selectedAddress={selectedAddress}
                query={query}
                onQuery={setQuery}
                onSelect={(address) => {
                  setSelectedAddress(address);
                  setEditingAddress(undefined);
                }}
              />
            </div>
            <div className="flex-1 overflow-y-auto p-6">
              {editingItem !== undefined ? (
                <Editor
                  item={editingItem}
                  saving={saving}
                  onCancel={handleCancelEdit}
                  onSave={(newProse) => handleSaveEdit(editingItem, newProse)}
                />
              ) : (
                renderDetail(vault, selectedAddress, handleEdit, handleKeep)
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
