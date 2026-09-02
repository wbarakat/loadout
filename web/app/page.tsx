"use client";

/**
 * The dashboard's app shell: config → connect-or-browse.
 *
 * On mount, this loads the saved connection config. With none saved, it
 * renders `ConnectForm`. With one saved, it pulls the vault and renders the
 * workspace: `Sidebar` + `ItemList` + a detail pane. A selected skill or
 * memory shows `ItemDetail`; a selected secret shows `SecretDetail`
 * (metadata only — a secret value never reaches this pane). Edit and Keep
 * have no handler yet; Task 6 wires them.
 *
 * The pulled `{vault, entries, version}` stays in state as-is — `entries`
 * feeds `commitEdit` in Task 6, so this file never drops it, even though
 * nothing here reads it yet.
 */
import { useEffect, useState, type JSX } from "react";
import { ConnectForm } from "../components/ConnectForm.js";
import { ItemDetail } from "../components/ItemDetail.js";
import { ItemList, type ListRow } from "../components/ItemList.js";
import { SecretDetail } from "../components/SecretDetail.js";
import { Sidebar, type Section, type SectionCounts } from "../components/Sidebar.js";
import { EmptyVault } from "../components/states/EmptyVault.js";
import { NotApproved } from "../components/states/NotApproved.js";
import { clearConfig, loadConfig, type DashConfig } from "../lib/dash/config.js";
import { sessionFrom } from "../lib/dash/session.js";
import { recipientFor } from "../lib/vault/age.js";
import type { Vault } from "../lib/vault/model.js";
import { NotApprovedError, pull, type PulledVault } from "../lib/vault/sync.js";

/** The shell's state machine. Only one of these is ever active at a time. */
type Phase =
  | { kind: "loading" }
  | { kind: "no-config" }
  | { kind: "not-approved"; recipient: string; deviceName: string }
  | { kind: "empty" }
  | { kind: "error"; message: string }
  | { kind: "ready" };

const EMPTY_VAULT: Vault = { items: [], secrets: [], roster: [] };

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
 * skill/memory, `SecretDetail` (metadata only, no value) for a secret. Edit
 * and Keep have no handler yet — Task 6 wires them. */
function renderDetail(vault: Vault, selectedAddress: string | undefined): JSX.Element {
  if (selectedAddress === undefined) {
    return <p className="text-sm text-slate-400">Select an item to view it.</p>;
  }

  if (selectedAddress.startsWith("secret/")) {
    const name = selectedAddress.slice("secret/".length);
    const secret = vault.secrets.find((s) => s.name === name);
    if (secret === undefined) {
      return <p className="text-sm text-slate-400">Select an item to view it.</p>;
    }
    return <SecretDetail secret={secret} />;
  }

  const item = vault.items.find((i) => i.address === selectedAddress);
  if (item === undefined) {
    return <p className="text-sm text-slate-400">Select an item to view it.</p>;
  }
  return <ItemDetail item={item} />;
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

  /** Pulls with `cfg`, and routes to the right phase: ready, empty,
   * not-approved (deriving the recipient to show), or a generic error. */
  async function attemptPull(cfg: DashConfig): Promise<void> {
    try {
      const result = await pull(sessionFrom(cfg));
      setPulled(result);
      setPhase(result.version === "" ? { kind: "empty" } : { kind: "ready" });
    } catch (err) {
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

  function handleConnected(r: { vault: Vault; version: string }): void {
    setConfig(loadConfig());
    setPulled({ vault: r.vault, entries: [], version: r.version });
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
  }

  if (phase.kind === "loading") {
    return <p className="p-6 text-sm text-slate-600">Loading…</p>;
  }

  if (phase.kind === "no-config") {
    return (
      <div className="p-6">
        <ConnectForm onConnected={handleConnected} />
      </div>
    );
  }

  if (phase.kind === "not-approved") {
    return (
      <div className="p-6">
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
      <div className="p-6">
        <EmptyVault onRetry={handleRetry} />
      </div>
    );
  }

  if (phase.kind === "error") {
    return (
      <div className="mx-auto max-w-md space-y-4 p-6 text-center">
        <h2 className="text-lg font-semibold text-red-800">Something went wrong</h2>
        <p className="text-sm text-red-700">{phase.message}</p>
        <button
          type="button"
          onClick={handleReconnect}
          className="rounded bg-slate-700 px-3 py-2 text-sm font-medium text-white hover:bg-slate-800"
        >
          Reconnect / Settings
        </button>
      </div>
    );
  }

  // phase.kind === "ready"
  const vault = pulled?.vault ?? EMPTY_VAULT;
  const counts = countsFor(vault);
  const rows = rowsFor(section, vault);

  return (
    <div className="flex h-screen">
      <Sidebar active={section} counts={counts} onSelect={handleSelectSection} />
      {section === "settings" ? (
        <div className="flex-1 overflow-y-auto p-6">
          <ConnectForm initial={config ?? undefined} onConnected={handleConnected} />
        </div>
      ) : (
        <>
          <div className="w-80 shrink-0 border-r border-slate-200">
            <ItemList
              rows={rows}
              selectedAddress={selectedAddress}
              query={query}
              onQuery={setQuery}
              onSelect={setSelectedAddress}
            />
          </div>
          <div className="flex-1 overflow-y-auto p-6">
            {renderDetail(vault, selectedAddress)}
          </div>
        </>
      )}
    </div>
  );
}
