/**
 * Browser-side store for the dashboard's connection settings.
 *
 * Security tradeoff: the bearer token and the age identity below are a
 * "no-secrets" device key. This key can decrypt vault items and their
 * metadata, but it can never decrypt a secret value — the vault never
 * encrypts a secret's value to a no-secrets device. Storing this key in
 * `localStorage` is an acceptable tradeoff for a personal self-host
 * dashboard: the worst case of a stolen browser profile is read access
 * to non-secret items, not exposure of a secret. Do not reuse this
 * storage pattern for a device key that CAN decrypt secrets.
 *
 * Every read and write goes through try/catch. This keeps the store
 * safe to import at build time (no `window`), during server-side
 * rendering, and in a private browsing window where `localStorage`
 * throws instead of being merely empty.
 */

const STORAGE_KEY = "loadout.dash";

/** The dashboard's saved connection settings. */
export interface DashConfig {
  baseUrl: string;
  token: string;
  identity: string;
  deviceName: string;
  lastVersion: string;
}

function readRaw(): string | null {
  try {
    if (typeof window === "undefined") return null;
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeRaw(value: string): void {
  try {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(STORAGE_KEY, value);
  } catch {
    // Storage is unavailable, full, or blocked. The caller keeps
    // running with in-memory state only; nothing more to do here.
  }
}

/** Load the saved dashboard config. Returns null when none is stored. */
export function loadConfig(): DashConfig | null {
  const raw = readRaw();
  if (raw === null) return null;
  try {
    return JSON.parse(raw) as DashConfig;
  } catch {
    return null;
  }
}

/** Save the full dashboard config, replacing any prior value. */
export function saveConfig(config: DashConfig): void {
  try {
    writeRaw(JSON.stringify(config));
  } catch {
    // A config value that cannot be serialized is dropped, not thrown.
  }
}

/** Remove the saved dashboard config, if any. */
export function clearConfig(): void {
  try {
    if (typeof window === "undefined") return;
    window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // Nothing to clear, or storage is unavailable. Either way, done.
  }
}

/**
 * Update only the `lastVersion` field of the saved config.
 * Does nothing when no config is stored yet.
 */
export function setLastVersion(version: string): void {
  const current = loadConfig();
  if (current === null) return;
  saveConfig({ ...current, lastVersion: version });
}
