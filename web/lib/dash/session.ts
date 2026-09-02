/**
 * Turns a stored `DashConfig` into a Part 1 `Session`, and drives the
 * one-time enroll handshake for a new no-secrets browser device.
 *
 * This module adds no crypto, tar, or HTTP logic of its own. It is a thin
 * wrapper over `../vault/age.js` (key generation), `../vault/client.js`
 * (the `loadoutd` HTTP client), and `./config.js` (the stored connection
 * settings).
 *
 * Enroll order (interop contract §8):
 *   1. The browser generates a device key (`newDevice`).
 *   2. The browser registers its recipient on `loadoutd`'s bootstrap
 *      roster (`registerForApproval`), so `GET /v1/devices` can find it.
 *   3. The user runs the printed command (`approveCommand`) on an
 *      already-approved full device (their Mac). That command adds the
 *      recipient to the vault's own `devices.toml` with role
 *      `no-secrets`, re-encrypts, and pushes.
 *   4. Only then can the browser decrypt a pulled snapshot with its own
 *      identity — see `pull` in `../vault/sync.js`.
 */
import { generateKeypair } from "../vault/age.js";
import { LoadoutdClient } from "../vault/client.js";
import type { Session } from "../vault/sync.js";
import type { DashConfig } from "./config.js";

/** The device name the enroll UI uses when the user has not typed one. */
const DEFAULT_DEVICE_NAME = "dashboard";

/**
 * Build a `Session` from a stored `DashConfig`.
 *
 * @param c the stored dashboard config.
 * @returns a `Session`: a fresh `LoadoutdClient` for `c.baseUrl`/`c.token`,
 * paired with this browser's own age identity.
 */
export function sessionFrom(c: DashConfig): Session {
  return {
    client: new LoadoutdClient({ baseUrl: c.baseUrl, token: c.token }),
    identity: c.identity,
  };
}

/**
 * Generate a new age X25519 device key for this browser.
 *
 * @returns a fresh `AGE-SECRET-KEY-1...` identity and its paired
 * `age1...` recipient.
 */
export async function newDevice(): Promise<{
  identity: string;
  recipient: string;
}> {
  return generateKeypair();
}

/**
 * Build the exact CLI command the user runs, on an already-approved full
 * device, to approve this browser as a no-secrets device.
 *
 * A blank name (empty, or whitespace only) defaults to `"dashboard"`.
 * Surrounding whitespace on a real name is trimmed. A name with
 * whitespace inside it is rejected, since device names are kebab-case
 * and a space in the middle would break the copyable command.
 *
 * @param deviceName the device name to approve.
 * @returns the exact command string, for example
 * `"loadout devices approve dashboard --no-secrets"`.
 * @throws if the (trimmed) name contains internal whitespace.
 */
export function approveCommand(deviceName: string): string {
  const trimmed = deviceName.trim();
  const name = trimmed === "" ? DEFAULT_DEVICE_NAME : trimmed;
  if (/\s/.test(name)) {
    throw new Error(
      `device name must not contain whitespace, got ${JSON.stringify(deviceName)}`,
    );
  }
  return `loadout devices approve ${name} --no-secrets`;
}

/**
 * Register this browser device on `loadoutd`'s bootstrap roster, so a
 * later `loadout devices approve` on the user's Mac can find its
 * recipient by name.
 *
 * This grants no trust by itself (interop contract §8): it only makes
 * the name and recipient visible to `GET /v1/devices`. Decrypt access is
 * granted only once the user runs `approveCommand`'s command.
 *
 * @param c the stored dashboard config (its `baseUrl`, `token`, and
 * `deviceName` are used).
 * @param recipient this browser's own `age1...` recipient, from
 * `newDevice`.
 */
export async function registerForApproval(
  c: DashConfig,
  recipient: string,
): Promise<void> {
  const client = new LoadoutdClient({ baseUrl: c.baseUrl, token: c.token });
  await client.registerDevice(c.deviceName, recipient);
}
