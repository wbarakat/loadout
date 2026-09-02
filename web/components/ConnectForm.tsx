"use client";

import { useEffect, useState, type ChangeEvent, type FormEvent, type JSX } from "react";
import { saveConfig, type DashConfig } from "../lib/dash/config.js";
import {
  approveCommand,
  newDevice,
  registerForApproval,
  sessionFrom,
} from "../lib/dash/session.js";
import { recipientFor } from "../lib/vault/age.js";
import type { Vault } from "../lib/vault/model.js";
import { NotApprovedError, pull } from "../lib/vault/sync.js";
import { CopyButton } from "./CopyButton.js";
import { NotApproved } from "./states/NotApproved.js";

const DEFAULT_DEVICE_NAME = "dashboard";

/** The message shown for any connect failure other than "not approved yet".
 * Names the three most likely fixes, in order: a wrong URL or token, a
 * `loadoutd` that is not running, and a `-cors-origin` mismatch. */
const NETWORK_ERROR_MESSAGE =
  "Could not reach loadoutd. Check the URL and token, confirm loadoutd is " +
  "running, and that its -cors-origin is set to this page's origin.";

function describeError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function effectiveDeviceName(deviceName: string): string {
  const trimmed = deviceName.trim();
  return trimmed === "" ? DEFAULT_DEVICE_NAME : trimmed;
}

/**
 * The first-run connect form: enter the `loadoutd` URL and token, add a
 * device key, then register and pull the vault.
 *
 * Manages its own "not approved yet" state: when `pull` throws
 * `NotApprovedError`, this component renders `NotApproved` in place of the
 * form, and its Retry button re-runs `pull` with the same config.
 */
export function ConnectForm(props: {
  initial?: DashConfig;
  onConnected: (r: { vault: Vault; version: string }) => void;
}): JSX.Element {
  const [baseUrl, setBaseUrl] = useState(props.initial?.baseUrl ?? "");
  const [token, setToken] = useState(props.initial?.token ?? "");
  const [deviceName, setDeviceName] = useState(
    props.initial?.deviceName ?? DEFAULT_DEVICE_NAME,
  );
  const [identity, setIdentity] = useState(props.initial?.identity ?? "");
  const [recipient, setRecipient] = useState("");
  const [importText, setImportText] = useState("");
  const [keyError, setKeyError] = useState<string | null>(null);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [connecting, setConnecting] = useState(false);
  const [notApproved, setNotApproved] = useState(false);

  const hasKey = identity !== "" && recipient !== "";
  const canConnect = hasKey && baseUrl.trim() !== "" && token !== "" && !connecting;

  // A reload with an already-saved identity should not force a fresh key:
  // that would abandon the recipient an admin already approved. Derive its
  // recipient once, so the key panel shows without a click.
  useEffect(() => {
    const startingIdentity = props.initial?.identity;
    if (startingIdentity === undefined || startingIdentity === "") return;
    let cancelled = false;
    recipientFor(startingIdentity)
      .then((derived) => {
        if (!cancelled) setRecipient(derived);
      })
      .catch(() => {
        // A stored identity that no longer parses. Leave recipient empty
        // so the user must generate or import a fresh key.
      });
    return () => {
      cancelled = true;
    };
    // Runs once, for the identity this component started with.
  }, []);

  function buildConfig(): DashConfig {
    return {
      baseUrl,
      token,
      identity,
      deviceName: effectiveDeviceName(deviceName),
      lastVersion: "",
    };
  }

  async function handleGenerateKey(): Promise<void> {
    setKeyError(null);
    try {
      const device = await newDevice();
      setIdentity(device.identity);
      setRecipient(device.recipient);
    } catch (err) {
      setKeyError(describeError(err));
    }
  }

  async function handleImportIdentity(): Promise<void> {
    setKeyError(null);
    const trimmed = importText.trim();
    if (trimmed === "") {
      setKeyError("Paste an identity first.");
      return;
    }
    try {
      const derived = await recipientFor(trimmed);
      setIdentity(trimmed);
      setRecipient(derived);
      setImportText("");
    } catch (err) {
      setKeyError(describeError(err));
    }
  }

  /** Pulls the vault with the given config, and routes to the right state:
   * connected, not-approved, or a network error on the form. */
  async function attemptPull(cfg: DashConfig): Promise<void> {
    try {
      const pulled = await pull(sessionFrom(cfg));
      setNotApproved(false);
      setConnectError(null);
      props.onConnected({ vault: pulled.vault, version: pulled.version });
    } catch (err) {
      if (err instanceof NotApprovedError) {
        setNotApproved(true);
      } else {
        setNotApproved(false);
        setConnectError(NETWORK_ERROR_MESSAGE);
      }
    }
  }

  async function handleConnect(): Promise<void> {
    if (!hasKey) {
      setConnectError("Generate or import a device key first.");
      return;
    }
    const cfg = buildConfig();
    saveConfig(cfg);
    setConnectError(null);
    setConnecting(true);
    try {
      await registerForApproval(cfg, recipient);
      await attemptPull(cfg);
    } catch (err) {
      setNotApproved(false);
      setConnectError(NETWORK_ERROR_MESSAGE);
    } finally {
      setConnecting(false);
    }
  }

  async function handleRetry(): Promise<void> {
    setConnecting(true);
    try {
      await attemptPull(buildConfig());
    } finally {
      setConnecting(false);
    }
  }

  if (notApproved) {
    return (
      <NotApproved
        recipient={recipient}
        deviceName={effectiveDeviceName(deviceName)}
        onRetry={() => {
          void handleRetry();
        }}
      />
    );
  }

  const command = hasKey ? approveCommand(deviceName) : "";

  return (
    <form
      className="mx-auto max-w-md space-y-6 rounded-lg border border-slate-300 bg-white p-6"
      onSubmit={(e: FormEvent<HTMLFormElement>) => {
        e.preventDefault();
        void handleConnect();
      }}
    >
      <div>
        <h1 className="text-lg font-semibold text-slate-900">Connect to loadoutd</h1>
        <p className="text-sm text-slate-600">
          Enter your loadoutd address and token. Add a device key. Then
          register this browser.
        </p>
      </div>

      <div className="space-y-4">
        <div>
          <label htmlFor="baseUrl" className="block text-sm font-medium text-slate-800">
            loadoutd URL
          </label>
          <input
            id="baseUrl"
            type="url"
            required
            value={baseUrl}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setBaseUrl(e.target.value)}
            placeholder="http://100.x.x.x:7777"
            className="mt-1 block w-full rounded border border-slate-300 px-3 py-2 text-sm"
          />
        </div>

        <div>
          <label htmlFor="token" className="block text-sm font-medium text-slate-800">
            Bearer token
          </label>
          <input
            id="token"
            type="password"
            required
            autoComplete="off"
            value={token}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setToken(e.target.value)}
            className="mt-1 block w-full rounded border border-slate-300 px-3 py-2 text-sm"
          />
        </div>

        <div>
          <label htmlFor="deviceName" className="block text-sm font-medium text-slate-800">
            Device name
          </label>
          <input
            id="deviceName"
            type="text"
            value={deviceName}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setDeviceName(e.target.value)}
            className="mt-1 block w-full rounded border border-slate-300 px-3 py-2 text-sm"
          />
        </div>
      </div>

      <div className="space-y-3 border-t border-slate-200 pt-4">
        {!hasKey ? (
          <div className="space-y-3">
            <p className="text-sm text-slate-600">
              Add a device key. Generate a new one, or paste one you
              already have.
            </p>
            <button
              type="button"
              onClick={() => {
                void handleGenerateKey();
              }}
              className="rounded bg-slate-800 px-3 py-2 text-sm font-medium text-white hover:bg-slate-900"
            >
              Generate key
            </button>

            <div>
              <label
                htmlFor="importIdentity"
                className="block text-sm font-medium text-slate-800"
              >
                Or paste an existing identity
              </label>
              <textarea
                id="importIdentity"
                rows={2}
                value={importText}
                onChange={(e: ChangeEvent<HTMLTextAreaElement>) =>
                  setImportText(e.target.value)
                }
                placeholder="AGE-SECRET-KEY-1..."
                className="mt-1 block w-full rounded border border-slate-300 px-3 py-2 font-mono text-xs"
              />
              <button
                type="button"
                onClick={() => {
                  void handleImportIdentity();
                }}
                className="mt-2 rounded border border-slate-300 px-3 py-2 text-sm font-medium text-slate-800 hover:bg-slate-100"
              >
                Import identity
              </button>
            </div>

            {keyError ? <p className="text-sm text-red-700">{keyError}</p> : null}
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-sm text-slate-600">
              This is your device key. It can read the vault, but never a
              secret value.
            </p>

            <div>
              <div className="text-xs font-medium text-slate-800">Device recipient</div>
              <div className="mt-1 flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded bg-slate-100 px-2 py-1 text-xs text-slate-800">
                  {recipient}
                </code>
                <CopyButton value={recipient} label="Copy recipient" />
              </div>
            </div>

            <div>
              <div className="text-xs font-medium text-slate-800">Approve command</div>
              <div className="mt-1 flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded bg-slate-100 px-2 py-1 text-xs text-slate-800">
                  {command}
                </code>
                <CopyButton value={command} label="Copy approve command" />
              </div>
              <p className="mt-1 text-xs text-slate-500">
                Run this on an approved device, such as your Mac, before you
                connect.
              </p>
            </div>

            <button
              type="button"
              onClick={() => {
                setIdentity("");
                setRecipient("");
                setKeyError(null);
              }}
              className="text-xs font-medium text-slate-500 underline hover:text-slate-700"
            >
              Use a different key
            </button>
          </div>
        )}
      </div>

      {connectError ? (
        <p className="rounded border border-red-300 bg-red-50 p-3 text-sm text-red-800">
          {connectError}
        </p>
      ) : null}

      <button
        type="submit"
        disabled={!canConnect}
        className="w-full rounded bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-slate-300"
      >
        {connecting ? "Connecting…" : "Register + Connect"}
      </button>
    </form>
  );
}
