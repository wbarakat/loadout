/**
 * The vault model: turns raw tar entries (from `readTar`) into a typed
 * `Vault` the UI can render, parses the device roster (recipients + roles,
 * fail closed), and applies a single skill/memory edit to the raw entry
 * set for write-back.
 *
 * NEVER reads or exposes a secret's `value.age` bytes (interop contract
 * §5). This module only ever looks at `secrets/<name>/meta.md` for a
 * secret, and carries every `secrets/**` entry through `applyEdit`
 * completely unchanged.
 */
import { parse as parseToml, type TomlTableWithoutBigInt } from "smol-toml";
import type { TarEntry } from "./tar.js";

export type Role = "full" | "no-secrets";

/** One device from the vault's `devices.toml` trust roster. */
export interface RosterDevice {
  name: string;
  recipient: string;
  role: Role;
}

/** A skill or memory item, parsed from its file's frontmatter and body. */
export interface Item {
  address: string;
  kind: "skill" | "memory";
  hook: string;
  body: string;
  frontmatter: Record<string, string>;
  provenance?: string;
  review?: string;
}

/** A secret's plaintext metadata ONLY. This type can never hold a value:
 * there is no field for it, and `parseVault` never reads `value.age`. */
export interface SecretMeta {
  name: string;
  frontmatter: Record<string, string>;
}

export interface Vault {
  items: Item[];
  secrets: SecretMeta[];
  roster: RosterDevice[];
}

function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function byName(a: { name: string }, b: { name: string }): number {
  return compareStrings(a.name, b.name);
}

/**
 * Maps an on-disk role string to a recognized `Role`. Mirrors Go's
 * `normalizeRole` (`internal/vault/snapshot.go`) exactly: absent or `""`
 * and `"full"` both read as `"full"`; `"no-secrets"` reads as itself; ANY
 * other string reads as `"no-secrets"`. This is a fail-closed default —
 * a typo in `devices.toml` must never grant secret access.
 */
function normalizeRole(raw: string | undefined): Role {
  if (raw === undefined || raw === "" || raw === "full") return "full";
  return "no-secrets";
}

function asTable(value: unknown): TomlTableWithoutBigInt {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as TomlTableWithoutBigInt)
    : {};
}

function asString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

/** Parses `devices.toml`'s text into a roster, sorted by device name. */
function parseRoster(text: string): RosterDevice[] {
  const root = asTable(parseToml(text));
  const devices = asTable(root["devices"]);
  const roster: RosterDevice[] = [];
  for (const [name, raw] of Object.entries(devices)) {
    const device = asTable(raw);
    roster.push({
      name,
      recipient: asString(device["recipient"]) ?? "",
      role: normalizeRole(asString(device["role"])),
    });
  }
  roster.sort(byName);
  return roster;
}

/**
 * Splits a leading `---\n...\n---\n` frontmatter block (flat `key: value`
 * lines) from the body. Mirrors Go's `parseFrontmatter`
 * (`internal/vault/memory.go`) exactly: strips a leading UTF-8 BOM,
 * normalizes CRLF to LF, and falls back to "no frontmatter, whole file is
 * the body" whenever the leading/closing `---` markers are not found.
 */
function parseFrontmatter(bytes: Uint8Array): {
  frontmatter: Record<string, string>;
  body: string;
} {
  let text = new TextDecoder().decode(bytes);
  if (text.startsWith("﻿")) text = text.slice(1);
  text = text.replace(/\r\n/g, "\n");

  const frontmatter: Record<string, string> = {};
  if (!text.startsWith("---\n")) {
    return { frontmatter, body: text };
  }
  const rest = text.slice("---\n".length);
  const end = rest.indexOf("\n---");
  if (end < 0) {
    return { frontmatter, body: text };
  }
  for (const line of rest.slice(0, end).split("\n")) {
    const colon = line.indexOf(":");
    if (colon < 0) continue;
    const key = line.slice(0, colon).trim();
    const value = line.slice(colon + 1).trim();
    frontmatter[key] = value;
  }
  let body = rest.slice(end + "\n---".length);
  if (body.startsWith("\n")) body = body.slice(1);
  return { frontmatter, body };
}

/** Builds an Item from a skill/memory file's raw bytes and address. */
function parseItem(bytes: Uint8Array, kind: Item["kind"], address: string): Item {
  const { frontmatter, body } = parseFrontmatter(bytes);
  const by = frontmatter["by"];
  const at = frontmatter["at"];
  const provenance = by !== undefined && at !== undefined ? `${by} · ${at}` : undefined;
  return {
    address,
    kind,
    hook: frontmatter["description"] ?? "",
    body,
    frontmatter,
    provenance,
    review: frontmatter["review"],
  };
}

const MEMORY_FILE_RE = /^memory\/([^/]+)\.md$/;
const SKILL_FILE_RE = /^skills\/([^/]+)\/SKILL\.md$/;
const SECRET_META_RE = /^secrets\/([^/]+)\/meta\.md$/;

/**
 * Parses raw tar entries (from `readTar`) into a typed `Vault`.
 *
 * NEVER reads `secrets/<name>/value.age`: the regexes below only ever
 * match `meta.md`, so a secret's ciphertext bytes are never even decoded,
 * let alone exposed on a `SecretMeta`.
 */
export function parseVault(entries: TarEntry[]): Vault {
  const items: Item[] = [];
  const secrets: SecretMeta[] = [];
  let roster: RosterDevice[] = [];

  for (const entry of entries) {
    if (entry.type !== "file") continue;

    if (entry.name === "devices.toml") {
      roster = parseRoster(new TextDecoder().decode(entry.bytes));
      continue;
    }

    const memoryMatch = MEMORY_FILE_RE.exec(entry.name);
    if (memoryMatch?.[1] !== undefined) {
      items.push(parseItem(entry.bytes, "memory", `memory/${memoryMatch[1]}`));
      continue;
    }

    const skillMatch = SKILL_FILE_RE.exec(entry.name);
    if (skillMatch?.[1] !== undefined) {
      items.push(parseItem(entry.bytes, "skill", `skill/${skillMatch[1]}`));
      continue;
    }

    const secretMatch = SECRET_META_RE.exec(entry.name);
    if (secretMatch?.[1] !== undefined) {
      const { frontmatter } = parseFrontmatter(entry.bytes);
      secrets.push({ name: secretMatch[1], frontmatter });
      continue;
    }

    // Everything else (directories, a skill folder's other files such as
    // scripts/references, and — critically — every `value.age`) is not
    // part of the model. `applyEdit` still carries it through unchanged.
  }

  items.sort((a, b) => compareStrings(a.address, b.address));
  secrets.sort(byName);
  return { items, secrets, roster };
}

/**
 * Recipients for the OUTER tar re-encryption: every device in the roster,
 * regardless of role, sorted by device name (interop contract §6). Secret
 * values are re-encrypted to full-only devices, but that is the writer's
 * (Go CLI's) concern — the browser never re-encrypts `value.age`.
 */
export function outerRecipients(roster: RosterDevice[]): string[] {
  return [...roster].sort(byName).map((device) => device.recipient);
}

function targetFileName(address: string): string {
  if (address.startsWith("secret/") || address.startsWith("secrets/")) {
    throw new Error(
      `cannot edit "${address}": secrets are never edited from the browser`,
    );
  }
  if (address.startsWith("skill/")) {
    return `skills/${address.slice("skill/".length)}/SKILL.md`;
  }
  if (address.startsWith("memory/")) {
    return `memory/${address.slice("memory/".length)}.md`;
  }
  throw new Error(`unrecognized item address: "${address}"`);
}

/**
 * Applies one edit to the raw entry set: replaces the bytes of the
 * skill/memory file named by `address` with the UTF-8 of `newBody`, and
 * returns a NEW entry array where every other entry — especially every
 * `secrets/**` entry and `devices.toml` — is the exact same object,
 * untouched.
 *
 * Throws if `address` names a secret, or if no matching file entry exists.
 */
export function applyEdit(
  entries: TarEntry[],
  address: string,
  newBody: string,
): TarEntry[] {
  const targetName = targetFileName(address);
  const index = entries.findIndex((entry) => entry.name === targetName);
  if (index === -1) {
    throw new Error(`no such item in the vault: "${address}"`);
  }

  const newBytes = new TextEncoder().encode(newBody);
  return entries.map((entry, i) =>
    i === index ? { ...entry, bytes: newBytes } : entry,
  );
}
