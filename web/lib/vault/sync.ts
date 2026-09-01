/**
 * Orchestrates the two operations the dashboard UI needs: pull the current
 * vault, and commit one edit safely.
 *
 * This composes every other vault module — `age` (decrypt/encryptTo), `tar`
 * (readTar/writeTar), `model` (parseVault/outerRecipients/applyEdit), and
 * `client` (LoadoutdClient) — but adds no crypto or tar logic of its own.
 *
 * No browser-side three-way merge (interop contract §7, RISK 1): the Go
 * CLI's merge needs local git history to know each path's state "since the
 * last confirmed sync," and a browser tab has no git. Instead this module
 * always pulls immediately before an edit and pushes immediately after; on
 * a `409` conflict it re-pulls and re-applies the SAME edit, bounded by
 * `maxRetries`. This is the low-risk strategy the interop contract itself
 * recommends over reimplementing the Go CLI's git-backed merge.
 */
import { AgeDecryptError, decrypt, encryptTo, recipientFor } from "./age.js";
import { readTar, writeTar, type TarEntry } from "./tar.js";
import { applyEdit, outerRecipients, parseVault, type Vault } from "./model.js";
import { ConflictError, type LoadoutdClient } from "./client.js";

/** One browser session: a configured `loadoutd` client plus this device's
 * own age identity (its private key — never sent anywhere; used only to
 * decrypt a pulled snapshot). */
export interface Session {
  client: LoadoutdClient;
  identity: string;
}

/** The result of `pull`: the parsed vault, its raw tar entries (kept for
 * write-back carry-through — see `commitEdit`), and the version it was
 * pulled at. */
export interface PulledVault {
  vault: Vault;
  entries: TarEntry[];
  version: string;
}

/** Thrown when `commitEdit` gives up after `maxRetries` straight `409`
 * conflicts. The UI should reload (re-`pull`) and let the user redo the
 * edit against the latest tree. */
export class SyncConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SyncConflictError";
  }
}

/** Thrown when this browser identity cannot decrypt the current snapshot —
 * its recipient is not yet in `devices.toml`. The UI should show the
 * `loadout devices approve` command for an admin to run. */
export class NotApprovedError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "NotApprovedError";
  }
}

/**
 * Pulls the current vault from `loadoutd`.
 *
 * A store that has never received a snapshot (`getLatest()` returns
 * `version: ""`) reads back as an empty vault with `version: ""` — there is
 * nothing to decrypt yet.
 *
 * @throws {NotApprovedError} if this device's identity cannot decrypt the
 * snapshot (its recipient is not yet in `devices.toml`).
 * @throws {UnsafeEntryError} (from `readTar`) if the decrypted tar contains
 * a hostile entry — this propagates unchanged; a hostile snapshot must
 * never be silently accepted.
 */
export async function pull(s: Session): Promise<PulledVault> {
  const latest = await s.client.getLatest();
  if (latest.version === "") {
    return { vault: { items: [], secrets: [], roster: [] }, entries: [], version: "" };
  }

  const blob = await s.client.getSnapshot(latest.version);
  let plaintext: Uint8Array;
  try {
    plaintext = await decrypt(blob, s.identity);
  } catch (cause) {
    if (cause instanceof AgeDecryptError) {
      throw new NotApprovedError(
        "this device cannot decrypt the current snapshot yet — ask an " +
          "approved device to run `loadout devices approve`",
        { cause },
      );
    }
    throw cause;
  }

  const entries = readTar(plaintext);
  const vault = parseVault(entries);
  return { vault, entries, version: latest.version };
}

/** How many times `commitEdit` re-pulls and re-applies the same edit after
 * a `409` conflict, before giving up (mirrors the interop contract §7 Go
 * CLI's `maxMergeRetries`). */
const DEFAULT_MAX_RETRIES = 3;

/**
 * Edits one skill or memory item and pushes the result.
 *
 * Each attempt pulls the current vault, applies the SAME edit to the
 * just-pulled tree, repacks the full tree, re-encrypts it to the current
 * roster, and pushes with the pulled version as parent. A `409` conflict
 * re-pulls and retries (up to `maxRetries` attempts total). This never
 * decrypts a `secrets/**\/value.age` file: `pull` only ever decrypts the
 * outer blob, and every `secrets/**` entry is carried through `applyEdit`
 * and `writeTar` byte-for-byte unchanged.
 *
 * @returns the new version string on success.
 * @throws {SyncConflictError} if every attempt conflicts.
 * @throws {NotApprovedError} (from `pull`) if this device cannot decrypt
 * the snapshot.
 */
export async function commitEdit(
  s: Session,
  address: string,
  newBody: string,
  maxRetries = DEFAULT_MAX_RETRIES,
): Promise<string> {
  for (let attempt = 0; attempt < maxRetries; attempt++) {
    const p = await pull(s);
    const newEntries = applyEdit(p.entries, address, newBody);

    let recipients = outerRecipients(p.vault.roster);
    if (recipients.length === 0) {
      // A brand-new store bootstrap: no device has been approved into
      // devices.toml yet. Fall back to this device's own recipient so the
      // push is at least readable by the device that made it.
      recipients = [await recipientFor(s.identity)];
    }

    const tar = writeTar(newEntries);
    const blob = await encryptTo(tar, recipients);

    try {
      return await s.client.postSnapshot(blob, p.version);
    } catch (err) {
      if (err instanceof ConflictError) {
        continue; // re-pull picks up the new head; the loop re-applies the same edit
      }
      throw err;
    }
  }

  throw new SyncConflictError(
    `commitEdit gave up after ${maxRetries} attempts: the store kept ` +
      "changing before this edit could be pushed",
  );
}
