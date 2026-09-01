import { afterEach, describe, expect, it, vi } from "vitest";
import * as ageModule from "./age.js";
import { decrypt, encryptTo, generateKeypair } from "./age.js";
import { readTar, writeTar, type TarEntry } from "./tar.js";
import { parseVault } from "./model.js";
import {
  ConflictError,
  LoadoutdClient,
  type LatestInfo,
} from "./client.js";
import {
  commitEdit,
  NotApprovedError,
  pull,
  SyncConflictError,
  type Session,
} from "./sync.js";

/*
 * A fake `loadoutd` backed by an in-memory store: a `latest` version+blob,
 * `getSnapshot` returns it, `postSnapshot` checks the pushed `parent`
 * against the current latest and either accepts it (bumping the version)
 * or throws `ConflictError` — exactly the real server's optimistic
 * concurrency rule (interop contract §7), minus the HTTP wire format.
 *
 * It subclasses the real `LoadoutdClient` (rather than a plain object
 * literal) so it satisfies `Session.client: LoadoutdClient` under
 * TypeScript's nominal typing for classes with private fields.
 */
class FakeLoadoutdClient extends LoadoutdClient {
  private latestVersion: string;
  private readonly blobs = new Map<string, Uint8Array>();
  private versionCounter = 1;
  private forcedConflicts: number;
  readonly postSnapshotCalls: { blob: Uint8Array; parent: string }[] = [];

  constructor(
    opts: {
      seedVersion?: string;
      seedBlob?: Uint8Array;
      forcedConflicts?: number;
    } = {},
  ) {
    super({ baseUrl: "http://loadoutd.fake.test", token: "fake-token" });
    this.latestVersion = opts.seedVersion ?? "";
    if (opts.seedVersion !== undefined && opts.seedBlob !== undefined) {
      this.blobs.set(opts.seedVersion, opts.seedBlob);
    }
    this.forcedConflicts = opts.forcedConflicts ?? 0;
  }

  override async getLatest(): Promise<LatestInfo> {
    return { version: this.latestVersion, parent: "" };
  }

  override async getSnapshot(version: string): Promise<Uint8Array> {
    const blob = this.blobs.get(version);
    if (!blob) {
      throw new Error(`fake client: no such snapshot version "${version}"`);
    }
    return blob;
  }

  override async postSnapshot(blob: Uint8Array, parent: string): Promise<string> {
    this.postSnapshotCalls.push({ blob, parent });
    if (this.forcedConflicts > 0) {
      this.forcedConflicts -= 1;
      throw new ConflictError(this.latestVersion);
    }
    if (parent !== this.latestVersion) {
      throw new ConflictError(this.latestVersion);
    }
    const version = `v${this.versionCounter}-${this.versionCounter
      .toString(16)
      .padStart(8, "0")}`;
    this.versionCounter += 1;
    this.blobs.set(version, blob);
    this.latestVersion = version;
    return version;
  }
}

// --- Fixture builders --------------------------------------------------

function textEntry(name: string, text: string, mode = 0o644): TarEntry {
  return { name, type: "file", mode, bytes: new TextEncoder().encode(text) };
}

function fileEntry(name: string, bytes: Uint8Array, mode = 0o644): TarEntry {
  return { name, type: "file", mode, bytes };
}

function dirEntry(name: string, mode = 0o755): TarEntry {
  return { name, type: "dir", mode, bytes: new Uint8Array(0) };
}

// A fixed byte pattern standing in for a secret's ciphertext. This client
// must never decrypt it and must carry it through byte-for-byte.
const VALUE_AGE_BYTES = new Uint8Array([1, 2, 3, 4, 5, 250, 251, 252, 0, 255]);

interface RosterFixtureDevice {
  name: string;
  recipient: string;
  role?: string;
}

function devicesToml(devices: RosterFixtureDevice[]): string {
  return devices
    .map((d) => {
      const roleLine = d.role !== undefined ? `\nrole = "${d.role}"` : "";
      return `[devices.${d.name}]\nrecipient = "${d.recipient}"${roleLine}\n`;
    })
    .join("\n");
}

/** Builds a full vault tar entry set: one memory item, one secret
 * (meta.md + value.age), and an optional devices.toml (omitted entirely
 * when `roster` is not given — the bootstrap/fresh-store case). */
function buildVaultEntries(opts: {
  memoryBody: string;
  roster?: RosterFixtureDevice[];
}): TarEntry[] {
  const entries: TarEntry[] = [
    dirEntry("memory/"),
    textEntry(
      "memory/note.md",
      "---\n" +
        "name: note\n" +
        "description: A note.\n" +
        "by: human\n" +
        "at: 2026-08-01T00:00:00Z\n" +
        "---\n" +
        opts.memoryBody,
    ),
    dirEntry("secrets/"),
    dirEntry("secrets/my-secret/"),
    textEntry(
      "secrets/my-secret/meta.md",
      "---\nname: my-secret\nservice: test\nby: human\nat: 2026-08-01T00:00:00Z\n---\n",
    ),
    fileEntry("secrets/my-secret/value.age", VALUE_AGE_BYTES, 0o600),
  ];
  if (opts.roster !== undefined && opts.roster.length > 0) {
    entries.unshift(textEntry("devices.toml", devicesToml(opts.roster)));
  }
  return entries;
}

function findEntry(entries: TarEntry[], name: string): TarEntry {
  const found = entries.find((e) => e.name === name);
  if (!found) throw new Error(`test setup error: no entry named "${name}"`);
  return found;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("pull", () => {
  it("returns the parsed vault and version for a seeded store", async () => {
    const identity = await generateKeypair();
    const entries = buildVaultEntries({
      memoryBody: "Original body.\n",
      roster: [{ name: "dashboard", recipient: identity.recipient, role: "no-secrets" }],
    });
    const blob = await encryptTo(writeTar(entries), [identity.recipient]);
    const client = new FakeLoadoutdClient({ seedVersion: "v1-seed0001", seedBlob: blob });
    const session: Session = { client, identity: identity.identity };

    const pulled = await pull(session);

    expect(pulled.version).toBe("v1-seed0001");
    expect(pulled.entries).toHaveLength(entries.length);
    const item = pulled.vault.items.find((i) => i.address === "memory/note");
    expect(item?.body).toBe("Original body.\n");
    expect(pulled.vault.roster.map((d) => d.name)).toEqual(["dashboard"]);
  });

  it("returns an empty vault with version \"\" for a store that has never received a snapshot", async () => {
    const identity = await generateKeypair();
    const client = new FakeLoadoutdClient();
    const session: Session = { client, identity: identity.identity };

    const pulled = await pull(session);

    expect(pulled).toEqual({
      vault: { items: [], secrets: [], roster: [] },
      entries: [],
      version: "",
    });
  });

  it("throws NotApprovedError when this device cannot decrypt the snapshot", async () => {
    const enrolled = await generateKeypair();
    const outsider = await generateKeypair();
    const entries = buildVaultEntries({
      memoryBody: "Body.\n",
      roster: [{ name: "mac", recipient: enrolled.recipient, role: "full" }],
    });
    // Encrypted only to the enrolled device — the outsider's identity is
    // not among the recipients.
    const blob = await encryptTo(writeTar(entries), [enrolled.recipient]);
    const client = new FakeLoadoutdClient({ seedVersion: "v1-seed0001", seedBlob: blob });
    const session: Session = { client, identity: outsider.identity };

    await expect(pull(session)).rejects.toThrow(NotApprovedError);
  });
});

describe("commitEdit", () => {
  it("changes a memory item; the next pull sees the new body; secrets carry through byte-identical", async () => {
    const identity = await generateKeypair();
    const entries = buildVaultEntries({
      memoryBody: "Original body.\n",
      roster: [{ name: "dashboard", recipient: identity.recipient, role: "no-secrets" }],
    });
    const blob = await encryptTo(writeTar(entries), [identity.recipient]);
    const client = new FakeLoadoutdClient({ seedVersion: "v1-seed0001", seedBlob: blob });
    const session: Session = { client, identity: identity.identity };

    const before = await pull(session);
    const beforeSecretBytes = Array.from(
      findEntry(before.entries, "secrets/my-secret/value.age").bytes,
    );

    const newVersion = await commitEdit(session, "memory/note", "Updated body.\n");

    expect(newVersion).not.toBe("v1-seed0001");
    const after = await pull(session);
    expect(after.vault.items.find((i) => i.address === "memory/note")?.body).toBe(
      "Updated body.\n",
    );
    const afterSecretBytes = Array.from(
      findEntry(after.entries, "secrets/my-secret/value.age").bytes,
    );
    expect(afterSecretBytes).toEqual(beforeSecretBytes);
    expect(afterSecretBytes).toEqual(Array.from(VALUE_AGE_BYTES));

    // Cross-check directly against the stored blob too, not just what
    // pull() reports back.
    const storedBlob = client.postSnapshotCalls.at(-1)?.blob;
    if (!storedBlob) throw new Error("expected a stored push");
    const storedEntries = readTar(await decrypt(storedBlob, identity.identity));
    expect(Array.from(findEntry(storedEntries, "secrets/my-secret/value.age").bytes)).toEqual(
      Array.from(VALUE_AGE_BYTES),
    );
  });

  it("retries after a single 409 conflict and succeeds", async () => {
    const identity = await generateKeypair();
    const entries = buildVaultEntries({
      memoryBody: "Original body.\n",
      roster: [{ name: "dashboard", recipient: identity.recipient, role: "no-secrets" }],
    });
    const blob = await encryptTo(writeTar(entries), [identity.recipient]);
    const client = new FakeLoadoutdClient({
      seedVersion: "v1-seed0001",
      seedBlob: blob,
      forcedConflicts: 1,
    });
    const session: Session = { client, identity: identity.identity };

    const version = await commitEdit(session, "memory/note", "Retried body.\n");

    // Proves the retry actually happened: two postSnapshot attempts, the
    // first rejected, the second accepted.
    expect(client.postSnapshotCalls).toHaveLength(2);
    expect(version).not.toBe("v1-seed0001");
    const pulled = await pull(session);
    expect(pulled.vault.items.find((i) => i.address === "memory/note")?.body).toBe(
      "Retried body.\n",
    );
  });

  it("throws SyncConflictError after exhausting all retries against a client that always conflicts", async () => {
    const identity = await generateKeypair();
    const entries = buildVaultEntries({
      memoryBody: "Original body.\n",
      roster: [{ name: "dashboard", recipient: identity.recipient, role: "no-secrets" }],
    });
    const blob = await encryptTo(writeTar(entries), [identity.recipient]);
    const client = new FakeLoadoutdClient({
      seedVersion: "v1-seed0001",
      seedBlob: blob,
      forcedConflicts: Infinity,
    });
    const session: Session = { client, identity: identity.identity };

    await expect(
      commitEdit(session, "memory/note", "Never lands.\n"),
    ).rejects.toThrow(SyncConflictError);
    // Exactly 3 attempts: the default maxRetries.
    expect(client.postSnapshotCalls).toHaveLength(3);
  });

  it("falls back to this device's own recipient and pushes with the pulled version as parent, for a fresh store with no roster yet", async () => {
    const identity = await generateKeypair();
    // No `roster` given: devices.toml is absent entirely — the bootstrap
    // case (nobody has been approved into the vault's trust roster yet),
    // encrypted only to this device's own key, exactly as the Go side's
    // packRecipients bootstrap fallback does (interop contract §3/§6).
    const entries = buildVaultEntries({ memoryBody: "Bootstrap body.\n" });
    const blob = await encryptTo(writeTar(entries), [identity.recipient]);
    const client = new FakeLoadoutdClient({ seedVersion: "v1-bootstrap", seedBlob: blob });
    const session: Session = { client, identity: identity.identity };

    const version = await commitEdit(session, "memory/note", "First edit.\n");

    expect(client.postSnapshotCalls).toHaveLength(1);
    expect(client.postSnapshotCalls[0]?.parent).toBe("v1-bootstrap");
    expect(version).not.toBe("v1-bootstrap");

    // The pushed blob must still be decryptable by this device even though
    // the roster was empty at pull time (outerRecipients([]) fell back to
    // recipientFor(identity)).
    const pushedBlob = client.postSnapshotCalls[0]?.blob;
    if (!pushedBlob) throw new Error("expected a pushed blob");
    const pushedVault = parseVault(readTar(await decrypt(pushedBlob, identity.identity)));
    expect(pushedVault.items.find((i) => i.address === "memory/note")?.body).toBe(
      "First edit.\n",
    );
  });

  it("throws when there is truly nothing to edit yet (a never-pushed store has no entries)", async () => {
    const identity = await generateKeypair();
    const client = new FakeLoadoutdClient(); // no snapshot ever pushed
    const session: Session = { client, identity: identity.identity };

    await expect(commitEdit(session, "memory/note", "text")).rejects.toThrow(
      /no such item/,
    );
    expect(client.postSnapshotCalls).toHaveLength(0);
  });

  it("never decrypts a value.age — decrypt is called only once, on the outer blob", async () => {
    const identity = await generateKeypair();
    const entries = buildVaultEntries({
      memoryBody: "Original body.\n",
      roster: [{ name: "dashboard", recipient: identity.recipient, role: "no-secrets" }],
    });
    const blob = await encryptTo(writeTar(entries), [identity.recipient]);
    const client = new FakeLoadoutdClient({ seedVersion: "v1-seed0001", seedBlob: blob });
    const session: Session = { client, identity: identity.identity };

    const decryptSpy = vi.spyOn(ageModule, "decrypt");

    await commitEdit(session, "memory/note", "Spied body.\n");

    expect(decryptSpy).toHaveBeenCalledTimes(1);
    expect(decryptSpy).toHaveBeenCalledWith(blob, identity.identity);
    for (const call of decryptSpy.mock.calls) {
      expect(call[0]).not.toEqual(VALUE_AGE_BYTES);
    }
  });
});
