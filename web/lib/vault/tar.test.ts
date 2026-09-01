import { describe, expect, it } from "vitest";
import { readTar, UnsafeEntryError, type TarEntry } from "./tar.js";

/*
 * A tiny, test-only USTAR writer.
 *
 * This does NOT reuse the reader under test. It builds raw 512-byte header
 * blocks by hand, from the same field offsets the reader parses, so the
 * reader is proven against real ustar bytes and not against its own
 * assumptions.
 */

const BLOCK_SIZE = 512;

interface RawEntrySpec {
  /** The tar "name" field (100 bytes). May be any string for these tests,
   * including unsafe ones the reader must reject. */
  name: string;
  /** ustar typeflag byte: "0" regular file, "5" directory, "2" symlink,
   * "x" PAX extended header. */
  typeflag: string;
  /** Permission bits, e.g. 0o644. Defaults to 0o644. */
  mode?: number;
  /** File body. Ignored for directories. Defaults to empty. */
  body?: Uint8Array;
  /** The ustar "prefix" field (155 bytes), for names over 100 bytes. */
  prefix?: string;
}

function octalField(value: number, width: number): Uint8Array {
  // A POSIX ustar numeric field: zero-padded octal digits, left-aligned,
  // followed by a trailing NUL (the rest of the field stays zero).
  const digits = value.toString(8).padStart(width - 1, "0");
  const field = new Uint8Array(width);
  for (let i = 0; i < digits.length; i++) {
    field[i] = digits.charCodeAt(i);
  }
  return field;
}

function stringField(value: string, width: number): Uint8Array {
  const field = new Uint8Array(width);
  const bytes = new TextEncoder().encode(value);
  field.set(bytes.subarray(0, width));
  return field;
}

function buildHeader(spec: RawEntrySpec, size: number): Uint8Array {
  const block = new Uint8Array(BLOCK_SIZE);
  block.set(stringField(spec.name, 100), 0);
  block.set(octalField(spec.mode ?? 0o644, 8), 100); // mode
  block.set(octalField(0, 8), 108); // uid
  block.set(octalField(0, 8), 116); // gid
  block.set(octalField(size, 12), 124); // size
  block.set(octalField(0, 12), 136); // mtime
  block.set(stringField("        ", 8), 148); // checksum placeholder, 8 spaces
  block[156] = spec.typeflag.charCodeAt(0); // typeflag
  block.set(stringField("ustar", 6), 257); // magic ("ustar" + NUL from zero-init)
  block.set(stringField("00", 2), 263); // version
  if (spec.prefix) {
    block.set(stringField(spec.prefix, 155), 345); // prefix
  }

  // The standard ustar checksum: the unsigned byte sum of the whole header
  // with the checksum field itself treated as 8 spaces.
  let sum = 0;
  for (let i = 0; i < BLOCK_SIZE; i++) sum += block[i];
  const checksumDigits = sum.toString(8).padStart(6, "0");
  const checksumField = new Uint8Array(8);
  for (let i = 0; i < 6; i++) checksumField[i] = checksumDigits.charCodeAt(i);
  checksumField[6] = 0; // NUL
  checksumField[7] = 0x20; // space
  block.set(checksumField, 148);

  return block;
}

function padTo512(data: Uint8Array): Uint8Array {
  const remainder = data.length % BLOCK_SIZE;
  if (remainder === 0) return data;
  const padded = new Uint8Array(data.length + (BLOCK_SIZE - remainder));
  padded.set(data);
  return padded;
}

/** Build a raw ustar tar stream from a list of raw entry specs. */
function buildTar(specs: RawEntrySpec[]): Uint8Array {
  const chunks: Uint8Array[] = [];
  for (const spec of specs) {
    const isDir = spec.typeflag === "5";
    const body = isDir ? new Uint8Array(0) : (spec.body ?? new Uint8Array(0));
    chunks.push(buildHeader(spec, body.length));
    if (body.length > 0) {
      chunks.push(padTo512(body));
    }
  }
  chunks.push(new Uint8Array(BLOCK_SIZE * 2)); // two all-zero end-of-archive blocks

  const total = chunks.reduce((n, c) => n + c.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.length;
  }
  return out;
}

function fileEntry(name: string, text: string, mode = 0o644): RawEntrySpec {
  return { name, typeflag: "0", mode, body: new TextEncoder().encode(text) };
}

function dirEntry(name: string, mode = 0o755): RawEntrySpec {
  return { name, typeflag: "5", mode };
}

function findEntry(entries: TarEntry[], name: string): TarEntry {
  const found = entries.find((e) => e.name === name);
  if (!found) throw new Error(`test setup error: no entry named "${name}"`);
  return found;
}

describe("readTar", () => {
  it("reads a well-formed vault tar into typed entries with correct names/bytes/types/modes", () => {
    const tar = buildTar([
      fileEntry("devices.toml", "[devices.mac]\n"),
      dirEntry("memory/"),
      fileEntry("memory/y.md", "# y\n"),
      dirEntry("secrets/"),
      dirEntry("secrets/k/"),
      fileEntry("secrets/k/meta.md", "name: k\n"),
      fileEntry("secrets/k/value.age", "age-encryption.org/v1\n...", 0o600),
      dirEntry("skills/"),
      dirEntry("skills/x/"),
      fileEntry("skills/x/SKILL.md", "# x skill\n"),
    ]);

    const entries = readTar(tar);
    expect(entries).toHaveLength(10);

    const devicesToml = findEntry(entries, "devices.toml");
    expect(devicesToml.type).toBe("file");
    expect(devicesToml.mode).toBe(0o644);
    expect(new TextDecoder().decode(devicesToml.bytes)).toBe("[devices.mac]\n");

    const skillFile = findEntry(entries, "skills/x/SKILL.md");
    expect(skillFile.type).toBe("file");
    expect(new TextDecoder().decode(skillFile.bytes)).toBe("# x skill\n");

    const secretValue = findEntry(entries, "secrets/k/value.age");
    expect(secretValue.type).toBe("file");
    expect(secretValue.mode).toBe(0o600);
    expect(new TextDecoder().decode(secretValue.bytes)).toBe(
      "age-encryption.org/v1\n...",
    );

    const memoryDir = findEntry(entries, "memory/");
    expect(memoryDir.type).toBe("dir");
    expect(memoryDir.mode).toBe(0o755);
    expect(memoryDir.bytes).toHaveLength(0);

    const skillsXDir = findEntry(entries, "skills/x/");
    expect(skillsXDir.type).toBe("dir");
    expect(skillsXDir.bytes).toHaveLength(0);
  });

  it("throws UnsafeEntryError on a top-level name starting with '..'", () => {
    const tar = buildTar([fileEntry("../escape", "evil")]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
  });

  it("throws UnsafeEntryError on a '..' path segment in the middle of a name", () => {
    const tar = buildTar([fileEntry("skills/../../x", "evil")]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
  });

  it("throws UnsafeEntryError on a symlink entry", () => {
    const tar = buildTar([{ name: "skills/link", typeflag: "2", mode: 0o777 }]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
  });

  it("throws UnsafeEntryError on a root-level file that is not devices.toml", () => {
    const tar = buildTar([fileEntry("evil.md", "evil")]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
  });

  it("throws UnsafeEntryError on an absolute path", () => {
    const tar = buildTar([fileEntry("/etc/passwd", "evil")]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
  });

  it("throws UnsafeEntryError on an empty name", () => {
    const tar = buildTar([fileEntry("", "evil")]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
  });

  it("aborts the whole read on the first unsafe entry, returning no partial results", () => {
    const tar = buildTar([
      fileEntry("devices.toml", "[devices.mac]\n"),
      fileEntry("../escape", "evil"),
    ]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
  });

  it("throws a clear UnsafeEntryError on a PAX extended header, per the documented decision", () => {
    const tar = buildTar([{ name: "PaxHeaders/x", typeflag: "x", body: new TextEncoder().encode("30 path=x\n") }]);
    expect(() => readTar(tar)).toThrow(/PAX/i);
  });

  it("resolves the ustar prefix field onto the name", () => {
    const tar = buildTar([
      { name: "y.md", typeflag: "0", prefix: "memory", body: new TextEncoder().encode("hi") },
    ]);
    const entries = readTar(tar);
    expect(entries).toHaveLength(1);
    expect(entries[0]?.name).toBe("memory/y.md");
  });
});
