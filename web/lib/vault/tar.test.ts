import { describe, expect, it } from "vitest";
import { readTar, writeTar, UnsafeEntryError, type TarEntry } from "./tar.js";

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
  /** Override the header's declared "size" field to something other than
   * `body`'s actual length — used to build a malformed archive whose
   * header claims more bytes than the stream actually holds. */
  declaredSize?: number;
  /** Override the raw bytes of the "size" field entirely, bypassing octal
   * encoding — used to build a header whose size field is not valid octal
   * ASCII at all (e.g. digits "8"/"9", or letters). */
  rawSizeField?: Uint8Array;
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
  block.set(spec.rawSizeField ?? octalField(size, 12), 124); // size
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
    const declaredSize = spec.declaredSize ?? body.length;
    chunks.push(buildHeader(spec, declaredSize));
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

  it("throws UnsafeEntryError when a declared size exceeds the remaining archive bytes, rather than silently truncating", () => {
    const tar = buildTar([
      {
        name: "memory/y.md",
        typeflag: "0",
        body: new TextEncoder().encode("only a few bytes"),
        declaredSize: 5000,
      },
    ]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
    expect(() => readTar(tar)).toThrow(/size/i);
  });

  it("throws UnsafeEntryError when the size field is not valid octal ASCII", () => {
    const tar = buildTar([
      {
        name: "memory/y.md",
        typeflag: "0",
        body: new TextEncoder().encode("hi"),
        // "8" and "9" are not octal digits — parseInt(..., 8) yields NaN.
        rawSizeField: new TextEncoder().encode("88888888888\0"),
      },
    ]);
    expect(() => readTar(tar)).toThrow(UnsafeEntryError);
    expect(() => readTar(tar)).toThrow(/size/i);
  });
});

/*
 * writeTar: the deterministic ustar writer.
 *
 * These tests prove the writer round-trips through OUR OWN reader (`readTar`
 * above). Cross-language acceptance — Go's `UnpackSnapshot` reading this
 * writer's output — is proven separately, later.
 */

/** A small vault-shaped file list. Deliberately omits directory entries, so
 * the same fixture doubles as the "derive directories from file paths"
 * test. `secrets/k/value.age` carries arbitrary non-UTF8 bytes (including
 * an embedded NUL) at mode 0600, matching a real secret value file. */
function sampleFileEntries(): TarEntry[] {
  return [
    {
      name: "devices.toml",
      type: "file",
      mode: 0o644,
      bytes: new TextEncoder().encode("[devices.mac]\n"),
    },
    {
      name: "memory/y.md",
      type: "file",
      mode: 0o644,
      bytes: new TextEncoder().encode("# y\n"),
    },
    {
      name: "secrets/k/meta.md",
      type: "file",
      mode: 0o644,
      bytes: new TextEncoder().encode("name: k\n"),
    },
    {
      name: "secrets/k/value.age",
      type: "file",
      mode: 0o600,
      bytes: new Uint8Array([0x00, 0xff, 0xfe, 0x02, 0x81, 0x0a, 0x00, 0x9d]),
    },
    {
      name: "skills/x/SKILL.md",
      type: "file",
      mode: 0o644,
      bytes: new TextEncoder().encode("# x skill\n"),
    },
  ];
}

describe("writeTar", () => {
  it("round-trips every file entry through readTar, preserving value.age's non-UTF8 bytes byte-for-byte", () => {
    const entries = sampleFileEntries();
    const readBack = readTar(writeTar(entries));

    for (const expected of entries) {
      const actual = findEntry(readBack, expected.name);
      expect(actual.type).toBe("file");
      expect(actual.mode).toBe(expected.mode);
      expect(Array.from(actual.bytes)).toEqual(Array.from(expected.bytes));
    }
  });

  it("derives a directory entry for every directory on each file's path", () => {
    const readBack = readTar(writeTar(sampleFileEntries()));
    for (const dirName of [
      "memory/",
      "secrets/",
      "secrets/k/",
      "skills/",
      "skills/x/",
    ]) {
      const dir = findEntry(readBack, dirName);
      expect(dir.type).toBe("dir");
      expect(dir.name.endsWith("/")).toBe(true);
    }
  });

  it("produces byte-identical output for the same content across two separate calls", () => {
    const first = writeTar(sampleFileEntries());
    const second = writeTar(sampleFileEntries());
    expect(Array.from(first)).toEqual(Array.from(second));
  });

  it("emits entries in one global lexicographic sort by full path, regardless of input order", () => {
    const entries = sampleFileEntries();
    const shuffled = [entries[4], entries[0], entries[3], entries[1], entries[2]] as TarEntry[];
    const readBack = readTar(writeTar(shuffled));
    const names = readBack.map((entry) => entry.name);
    const expectedOrder = [...names].sort();
    expect(names).toEqual(expectedOrder);
  });

  it("preserves a value.age entry's 0600 mode through write and read", () => {
    const readBack = readTar(writeTar(sampleFileEntries()));
    expect(findEntry(readBack, "secrets/k/value.age").mode).toBe(0o600);
  });

  it("splits a name over 100 bytes into ustar prefix+name fields and round-trips it", () => {
    const longDirSegment = "x".repeat(95);
    const longName = `skills/${longDirSegment}/SKILL.md`;
    expect(new TextEncoder().encode(longName).length).toBeGreaterThan(100);

    const entries: TarEntry[] = [
      {
        name: longName,
        type: "file",
        mode: 0o644,
        bytes: new TextEncoder().encode("# long\n"),
      },
    ];
    const readBack = readTar(writeTar(entries));
    const found = findEntry(readBack, longName);
    expect(new TextDecoder().decode(found.bytes)).toBe("# long\n");
  });
});
