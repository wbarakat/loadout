import { describe, expect, it } from "vitest";
import { applyEdit, outerRecipients, parseVault } from "./model.js";
import type { TarEntry } from "./tar.js";

/*
 * A hand-built fixture that mirrors a real vault snapshot's tar entries
 * (interop contract §4/§6): one skill, one memory item, one secret
 * (meta.md + value.age), a devices.toml roster, and a non-SKILL.md file
 * inside the skill folder (a script) that must survive `applyEdit`
 * untouched even though `parseVault` ignores it.
 */

function fileEntry(name: string, bytes: Uint8Array, mode = 0o644): TarEntry {
  return { name, type: "file", mode, bytes };
}

function dirEntry(name: string, mode = 0o755): TarEntry {
  return { name, type: "dir", mode, bytes: new Uint8Array(0) };
}

function textEntry(name: string, text: string, mode = 0o644): TarEntry {
  return fileEntry(name, new TextEncoder().encode(text), mode);
}

// A marker embedded in the secret's value.age fixture bytes. Never a valid
// UTF-8-safe age ciphertext, but that's fine: parseVault must never decode
// it at all, so it never has a chance to leak into any string output.
const VALUE_AGE_MARKER = "THE-SECRET-VALUE-MUST-NEVER-APPEAR-HERE";

function valueAgeBytes(): Uint8Array {
  const marker = new TextEncoder().encode(VALUE_AGE_MARKER);
  const noise = new Uint8Array([0x00, 0xff, 0xfe, 0x02, 0x81, 0x0a, 0x00, 0x9d]);
  const out = new Uint8Array(marker.length + noise.length);
  out.set(marker, 0);
  out.set(noise, marker.length);
  return out;
}

const DEVICES_TOML = `
[devices.mac]
recipient = "age1mac0000000000000000000000000000000000000000000000000000"
role = "full"

[devices.legacy]
recipient = "age1legacy000000000000000000000000000000000000000000000000"

[devices.dashboard]
recipient = "age1dashboard00000000000000000000000000000000000000000000"
role = "no-secrets"

[devices.typo-device]
recipient = "age1typo00000000000000000000000000000000000000000000000000"
role = "wat"
`;

const MEMORY_MD = `---
name: my-fact
description: A short fact worth remembering.
type: user
by: human
at: 2026-08-01T00:00:00Z
review: kept
---
This is the fact body.
It has two lines.
`;

const SKILL_MD = `---
name: deploy-checks
description: Run before every deploy.
by: claude-code
at: 2026-08-15T12:00:00Z
review: draft
---
# deploy-checks

Do the checks before every deploy.
`;

const SECRET_META_MD = `---
name: openai-key
service: openai
hook: Used for embeddings.
rotate_after: 720h
by: human
at: 2026-07-01T00:00:00Z
allowed_hosts: api.openai.com
---
`;

function buildFixture(): TarEntry[] {
  return [
    textEntry("devices.toml", DEVICES_TOML),
    dirEntry("memory/"),
    textEntry("memory/my-fact.md", MEMORY_MD),
    dirEntry("skills/"),
    dirEntry("skills/deploy-checks/"),
    textEntry("skills/deploy-checks/SKILL.md", SKILL_MD),
    dirEntry("skills/deploy-checks/scripts/"),
    textEntry("skills/deploy-checks/scripts/run.sh", "#!/bin/sh\necho hi\n"),
    dirEntry("secrets/"),
    dirEntry("secrets/openai-key/"),
    textEntry("secrets/openai-key/meta.md", SECRET_META_MD),
    fileEntry("secrets/openai-key/value.age", valueAgeBytes(), 0o600),
  ];
}

function findEntry(entries: TarEntry[], name: string): TarEntry {
  const found = entries.find((e) => e.name === name);
  if (!found) throw new Error(`test setup error: no entry named "${name}"`);
  return found;
}

describe("parseVault", () => {
  it("parses the memory item's address, hook, body, frontmatter, and provenance", () => {
    const vault = parseVault(buildFixture());
    const item = vault.items.find((i) => i.address === "memory/my-fact");
    if (!item) throw new Error("expected a memory/my-fact item");

    expect(item.kind).toBe("memory");
    expect(item.hook).toBe("A short fact worth remembering.");
    expect(item.body).toBe("This is the fact body.\nIt has two lines.\n");
    expect(item.frontmatter).toEqual({
      name: "my-fact",
      description: "A short fact worth remembering.",
      type: "user",
      by: "human",
      at: "2026-08-01T00:00:00Z",
      review: "kept",
    });
    expect(item.provenance).toBe("human · 2026-08-01T00:00:00Z");
    expect(item.review).toBe("kept");
  });

  it("parses the skill item's address, hook, body, frontmatter, and provenance", () => {
    const vault = parseVault(buildFixture());
    const item = vault.items.find((i) => i.address === "skill/deploy-checks");
    if (!item) throw new Error("expected a skill/deploy-checks item");

    expect(item.kind).toBe("skill");
    expect(item.hook).toBe("Run before every deploy.");
    expect(item.body).toBe(
      "# deploy-checks\n\nDo the checks before every deploy.\n",
    );
    expect(item.provenance).toBe("claude-code · 2026-08-15T12:00:00Z");
    expect(item.review).toBe("draft");
  });

  it("mirrors Go's parseFrontmatter exactly: a blank line after the closing --- leaves one leading newline in the body (TrimPrefix strips only one)", () => {
    const withBlankLineAfterClose = [
      "---",
      "name: x",
      "description: d",
      "by: human",
      "at: 2026-01-01T00:00:00Z",
      "review: kept",
      "---",
      "",
      "body text",
      "",
    ].join("\n");
    const vault = parseVault([textEntry("memory/x.md", withBlankLineAfterClose)]);
    const item = vault.items[0];
    if (!item) throw new Error("expected one item");
    expect(item.body).toBe("\nbody text\n");
  });

  it("does not turn a non-SKILL.md file inside a skill folder into an item", () => {
    const vault = parseVault(buildFixture());
    expect(vault.items).toHaveLength(2);
    expect(
      vault.items.some((i) => i.address.includes("run.sh")),
    ).toBe(false);
  });

  it("parses secret metadata without ever exposing a value", () => {
    const vault = parseVault(buildFixture());
    expect(vault.secrets).toHaveLength(1);
    const secret = vault.secrets[0];
    if (!secret) throw new Error("expected one secret");

    expect(secret.name).toBe("openai-key");
    expect(secret.frontmatter).toEqual({
      name: "openai-key",
      service: "openai",
      hook: "Used for embeddings.",
      rotate_after: "720h",
      by: "human",
      at: "2026-07-01T00:00:00Z",
      allowed_hosts: "api.openai.com",
    });
    // Structural guarantee: SecretMeta has exactly {name, frontmatter} —
    // no field could hold a value even by accident.
    expect(Object.keys(secret).sort()).toEqual(["frontmatter", "name"]);
  });

  it("never surfaces a value.age byte anywhere in the parsed vault", () => {
    const vault = parseVault(buildFixture());
    // JSON.stringify walks every field of every item/secret/roster entry:
    // if the marker embedded in value.age's bytes ever leaked into a
    // string field, it would show up here.
    expect(JSON.stringify(vault)).not.toContain(VALUE_AGE_MARKER);
  });

  it("normalizes roster roles fail-closed and sorts by device name", () => {
    const vault = parseVault(buildFixture());
    expect(vault.roster.map((d) => d.name)).toEqual([
      "dashboard",
      "legacy",
      "mac",
      "typo-device",
    ]);

    const byName = new Map(vault.roster.map((d) => [d.name, d]));
    expect(byName.get("mac")?.role).toBe("full"); // explicit "full"
    expect(byName.get("legacy")?.role).toBe("full"); // absent role -> full
    expect(byName.get("dashboard")?.role).toBe("no-secrets"); // explicit
    expect(byName.get("typo-device")?.role).toBe("no-secrets"); // "wat" fails closed

    expect(byName.get("mac")?.recipient).toBe(
      "age1mac0000000000000000000000000000000000000000000000000000",
    );
  });
});

describe("outerRecipients", () => {
  it("returns every roster device's recipient, sorted by device name, regardless of input order", () => {
    const vault = parseVault(buildFixture());
    const shuffled = [...vault.roster].reverse();

    const recipients = outerRecipients(shuffled);

    expect(recipients).toEqual(
      ["dashboard", "legacy", "mac", "typo-device"].map(
        (name) => vault.roster.find((d) => d.name === name)?.recipient,
      ),
    );
  });

  it("includes no-secrets devices too — the outer tar is not role-gated", () => {
    const vault = parseVault(buildFixture());
    const recipients = outerRecipients(vault.roster);
    expect(recipients).toContain(
      "age1dashboard00000000000000000000000000000000000000000000",
    );
  });
});

describe("applyEdit", () => {
  it("changes only the target memory entry's bytes, leaving every other entry byte-identical", () => {
    const entries = buildFixture();
    const untouchedNames = entries
      .map((e) => e.name)
      .filter((name) => name !== "memory/my-fact.md");
    const originalByName = new Map(entries.map((e) => [e.name, e]));

    const edited = applyEdit(entries, "memory/my-fact", "new body\n");

    const editedTarget = findEntry(edited, "memory/my-fact.md");
    expect(new TextDecoder().decode(editedTarget.bytes)).toBe("new body\n");

    for (const name of untouchedNames) {
      const before = originalByName.get(name);
      const after = findEntry(edited, name);
      // Byte-identical, and in fact the very same entry object — applyEdit
      // must not reconstruct or re-encode anything it did not change.
      expect(after).toBe(before);
    }
  });

  it("leaves every secrets/** entry byte-identical, including value.age's raw bytes", () => {
    const entries = buildFixture();
    const originalMeta = findEntry(entries, "secrets/openai-key/meta.md");
    const originalValue = findEntry(entries, "secrets/openai-key/value.age");

    const edited = applyEdit(entries, "skill/deploy-checks", "# changed\n");

    const editedMeta = findEntry(edited, "secrets/openai-key/meta.md");
    const editedValue = findEntry(edited, "secrets/openai-key/value.age");

    expect(editedMeta).toBe(originalMeta);
    expect(editedValue).toBe(originalValue);
    expect(Array.from(editedValue.bytes)).toEqual(
      Array.from(valueAgeBytes()),
    );
  });

  it("carries devices.toml and a skill's non-SKILL.md file through unchanged", () => {
    const entries = buildFixture();
    const originalDevicesToml = findEntry(entries, "devices.toml");
    const originalScript = findEntry(
      entries,
      "skills/deploy-checks/scripts/run.sh",
    );

    const edited = applyEdit(entries, "memory/my-fact", "new body\n");

    expect(findEntry(edited, "devices.toml")).toBe(originalDevicesToml);
    expect(findEntry(edited, "skills/deploy-checks/scripts/run.sh")).toBe(
      originalScript,
    );
  });

  it("throws when asked to edit a secret address (secret/...)", () => {
    const entries = buildFixture();
    expect(() =>
      applyEdit(entries, "secret/openai-key", "nope"),
    ).toThrow();
  });

  it("throws when asked to edit a secret address (secrets/...)", () => {
    const entries = buildFixture();
    expect(() =>
      applyEdit(entries, "secrets/openai-key/meta", "nope"),
    ).toThrow();
  });

  it("throws a clear error when the address is not found", () => {
    const entries = buildFixture();
    expect(() =>
      applyEdit(entries, "memory/does-not-exist", "nope"),
    ).toThrow(/does-not-exist/);
  });
});
