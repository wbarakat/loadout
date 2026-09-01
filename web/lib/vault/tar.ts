/**
 * A minimal, read-only POSIX ustar tar parser.
 *
 * Scope: this reads the exact tar shape `loadoutd`/`loadout` produce after
 * age-decrypting a snapshot (Go's `archive/tar.Writer`, USTAR format for
 * this vault's short kebab-case paths — see the interop contract §4). It is
 * NOT a general-purpose tar library: it enforces the same traversal
 * hardening the Go CLI's `UnpackSnapshot` applies, and it refuses anything
 * outside the vault's four synced roots.
 *
 * PAX decision: Go's writer only emits a PAX extended header (typeflag
 * "x"/"g") when a field overflows its USTAR fixed width — for example a
 * name over 100 bytes. This vault's paths (`skills/<name>/...`,
 * `memory/<name>.md`, `secrets/<name>/...`, `devices.toml`) never come
 * close to that limit, so a PAX header here is unexpected. Rather than risk
 * silently applying a PAX override to the wrong following entry, this
 * reader throws a clear error and aborts the whole read.
 */

/** A tar entry's kind, after traversal-hardening. Symlinks and every other
 * tar type are rejected before a TarEntry is ever produced. */
export type TarEntryType = "file" | "dir";

/** One tar entry, decoded and name-checked. `bytes` is empty for a dir. */
export interface TarEntry {
  name: string;
  type: TarEntryType;
  mode: number;
  bytes: Uint8Array;
}

/**
 * Thrown when a tar entry's name or type is not allowed inside a vault
 * snapshot. Reading a tar that trips this check aborts immediately: the
 * caller gets no partial result, only the thrown error.
 */
export class UnsafeEntryError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "UnsafeEntryError";
  }
}

const BLOCK_SIZE = 512;

// USTAR header field offsets and widths (POSIX.1-1988 ustar format). Only
// the fields this reader needs are named here.
const NAME_OFFSET = 0;
const NAME_SIZE = 100;
const MODE_OFFSET = 100;
const MODE_SIZE = 8;
const UID_OFFSET = 108;
const UID_SIZE = 8;
const GID_OFFSET = 116;
const GID_SIZE = 8;
const SIZE_OFFSET = 124;
const SIZE_SIZE = 12;
const MTIME_OFFSET = 136;
const MTIME_SIZE = 12;
const CHECKSUM_OFFSET = 148;
const CHECKSUM_SIZE = 8;
const TYPEFLAG_OFFSET = 156;
const MAGIC_OFFSET = 257;
const MAGIC_SIZE = 6;
const VERSION_OFFSET = 263;
const VERSION_SIZE = 2;
const PREFIX_OFFSET = 345;
const PREFIX_SIZE = 155;

const TYPE_REGULAR = "0";
// Some old (pre-POSIX) tar writers use a NUL typeflag byte for a plain
// regular file. Go's writer never emits this, but we accept it defensively.
const TYPE_REGULAR_LEGACY = "\0";
const TYPE_SYMLINK = "2";
const TYPE_DIR = "5";
const TYPE_PAX_EXTENDED = "x";
const TYPE_PAX_GLOBAL = "g";

// The only root-level names a vault snapshot's tar may contain (interop
// contract §4, SyncedSet()).
const ALLOWED_DIR_ROOTS = ["skills/", "memory/", "secrets/"];
const ALLOWED_ROOT_FILE = "devices.toml";

/** Decode a fixed-width header field as ASCII text, up to its first NUL
 * byte (or the field's end, if none), trimming ASCII space padding. */
function fieldToString(field: Uint8Array): string {
  const nulIndex = field.indexOf(0);
  const end = nulIndex === -1 ? field.length : nulIndex;
  return new TextDecoder().decode(field.subarray(0, end)).trim();
}

/** Parse a ustar numeric field (space/NUL-terminated octal ASCII digits). */
function parseOctalField(field: Uint8Array): number {
  const text = fieldToString(field);
  return text === "" ? 0 : parseInt(text, 8);
}

function isAllZeroBlock(block: Uint8Array): boolean {
  return block.every((byte) => byte === 0);
}

/**
 * Reject any entry name that is not safe to place inside the vault's
 * on-disk tree. Mirrors the Go CLI's `safeJoin` plus the vault's own
 * allowed-roots policy (interop contract §4). Any violation throws
 * `UnsafeEntryError`; the caller must treat this as a hard read failure.
 */
function assertSafeName(name: string): void {
  if (name.startsWith("/")) {
    throw new UnsafeEntryError(`unsafe tar entry: absolute path "${name}"`);
  }
  if (name === "") {
    throw new UnsafeEntryError("unsafe tar entry: empty name");
  }
  for (const segment of name.split("/")) {
    if (segment === "." || segment === "..") {
      throw new UnsafeEntryError(
        `unsafe tar entry: ".." path segment in "${name}"`,
      );
    }
  }
  const underAllowedRoot = ALLOWED_DIR_ROOTS.some((root) =>
    name.startsWith(root),
  );
  const isAllowedRootFile = name === ALLOWED_ROOT_FILE;
  if (!underAllowedRoot && !isAllowedRootFile) {
    throw new UnsafeEntryError(
      `unsafe tar entry: "${name}" is outside the allowed vault paths ` +
        `(skills/, memory/, secrets/, or exactly "devices.toml")`,
    );
  }
}

/** Reject any tar entry type this reader does not support: a symlink, or
 * anything other than a regular file or a directory. */
function assertSafeType(typeflag: string, name: string): void {
  if (typeflag === TYPE_SYMLINK) {
    throw new UnsafeEntryError(`unsafe tar entry: symlink "${name}"`);
  }
  const isRegularFile =
    typeflag === TYPE_REGULAR || typeflag === TYPE_REGULAR_LEGACY;
  if (!isRegularFile && typeflag !== TYPE_DIR) {
    throw new UnsafeEntryError(
      `unsafe tar entry: unsupported type "${typeflag}" for "${name}"`,
    );
  }
}

/**
 * Parse a POSIX ustar tar stream into typed, name-checked entries.
 *
 * @param tar the decrypted vault snapshot's plaintext bytes.
 * @returns every file and directory entry, in tar order.
 * @throws {UnsafeEntryError} on the first entry with a disallowed name or
 * type (see the module doc comment). The read aborts entirely: no partial
 * list of entries is returned.
 */
export function readTar(tar: Uint8Array): TarEntry[] {
  const entries: TarEntry[] = [];
  let offset = 0;

  while (offset + BLOCK_SIZE <= tar.length) {
    const header = tar.subarray(offset, offset + BLOCK_SIZE);

    // Two consecutive all-zero 512-byte blocks mark the end of the archive.
    if (isAllZeroBlock(header)) {
      break;
    }
    offset += BLOCK_SIZE;

    const typeflag = String.fromCharCode(header[TYPEFLAG_OFFSET] ?? 0);
    const size = parseOctalField(
      header.subarray(SIZE_OFFSET, SIZE_OFFSET + SIZE_SIZE),
    );
    if (offset + size > tar.length) {
      // A declared size that runs past the end of the archive must not be
      // silently truncated into a short entry, and must not silently
      // swallow the header/body bytes of any entry that would have
      // followed. Treat this as a hard read failure, the same as an
      // unsafe name or type.
      throw new UnsafeEntryError(
        "declared entry size exceeds the remaining archive bytes",
      );
    }
    const bodyBlockCount = Math.ceil(size / BLOCK_SIZE);
    const body = tar.subarray(offset, offset + size);
    offset += bodyBlockCount * BLOCK_SIZE;

    if (typeflag === TYPE_PAX_EXTENDED || typeflag === TYPE_PAX_GLOBAL) {
      // See the module doc comment: PAX support is intentionally not
      // implemented, to avoid silently mis-associating an override with
      // the wrong following entry.
      throw new UnsafeEntryError(
        "PAX extended headers are not supported by this tar reader",
      );
    }

    const rawName = fieldToString(
      header.subarray(NAME_OFFSET, NAME_OFFSET + NAME_SIZE),
    );
    const prefix = fieldToString(
      header.subarray(PREFIX_OFFSET, PREFIX_OFFSET + PREFIX_SIZE),
    );
    const name = prefix === "" ? rawName : `${prefix}/${rawName}`;
    const mode = parseOctalField(
      header.subarray(MODE_OFFSET, MODE_OFFSET + MODE_SIZE),
    );

    assertSafeName(name);
    assertSafeType(typeflag, name);

    if (typeflag === TYPE_DIR) {
      entries.push({ name, type: "dir", mode, bytes: new Uint8Array(0) });
    } else {
      entries.push({ name, type: "file", mode, bytes: body.slice() });
    }
  }

  return entries;
}

// ---------------------------------------------------------------------
// Writer: builds the exact ustar shape Go's `archive/tar.Writer` produces
// for a vault snapshot (interop contract §4). Round-trip proof against
// `readTar` lives in tar.test.ts; cross-language proof (Go reading this
// writer's output) is a later task.
// ---------------------------------------------------------------------

/** Directories get `0755`; a directory entry given as input keeps its own
 * mode instead (see `mergeDerivedDirs`). */
const DEFAULT_DIR_MODE = 0o755;

/** Split a name that overflows the 100-byte ustar "name" field into a
 * "prefix" (≤155 bytes) plus a short "name" (≤100 bytes), the same rule
 * Go's `archive/tar` uses. Vault paths never reach this size in practice;
 * this is a safety path, not the common case.
 *
 * @throws {Error} if no "/" split point makes both fields fit.
 */
function splitName(name: string): { name: string; prefix: string } {
  const encoder = new TextEncoder();
  if (encoder.encode(name).length <= NAME_SIZE) {
    return { name, prefix: "" };
  }
  for (
    let slash = name.lastIndexOf("/");
    slash > 0;
    slash = name.lastIndexOf("/", slash - 1)
  ) {
    const prefix = name.slice(0, slash);
    const suffix = name.slice(slash + 1);
    if (
      encoder.encode(suffix).length <= NAME_SIZE &&
      encoder.encode(prefix).length <= PREFIX_SIZE
    ) {
      return { name: suffix, prefix };
    }
  }
  throw new Error(`tar entry name too long to encode in ustar format: "${name}"`);
}

/** Write `value` as ASCII text into `block` at `[offset, offset + width)`,
 * left-aligned. The rest of the field stays zero (the block starts
 * zero-filled), which is how ustar readers expect an empty/NUL-terminated
 * text field to look — this reader's own `fieldToString` relies on it. */
function writeStringField(
  block: Uint8Array,
  value: string,
  offset: number,
  width: number,
): void {
  const bytes = new TextEncoder().encode(value);
  block.set(bytes.subarray(0, width), offset);
}

/** Write `value` as a zero-padded octal ustar numeric field, terminated by
 * a NUL byte (left implicit: the block starts zero-filled). */
function writeOctalField(
  block: Uint8Array,
  value: number,
  offset: number,
  width: number,
): void {
  const digits = value.toString(8).padStart(width - 1, "0");
  writeStringField(block, digits, offset, width - 1);
}

/** Derive every directory path implied by a file's path, shallow-to-deep.
 * E.g. "skills/x/SKILL.md" -> ["skills/", "skills/x/"]. */
function deriveDirNames(fileName: string): string[] {
  const segments = fileName.split("/");
  segments.pop(); // drop the file's own basename
  const dirNames: string[] = [];
  let path = "";
  for (const segment of segments) {
    path += `${segment}/`;
    dirNames.push(path);
  }
  return dirNames;
}

/**
 * Union the given entries with a directory entry for every directory any
 * file's path implies, deduped by name. An explicit directory entry in the
 * input (if any) wins over a derived default; a directory that only exists
 * because a file implies it gets `DEFAULT_DIR_MODE`.
 *
 * Deriving directories from file paths (rather than requiring the caller
 * to list every one) is the simplest approach and the least error-prone:
 * it cannot go stale relative to the files it accompanies.
 */
function mergeDerivedDirs(entries: TarEntry[]): TarEntry[] {
  const byName = new Map<string, TarEntry>();
  for (const entry of entries) {
    byName.set(entry.name, entry);
  }
  for (const entry of entries) {
    if (entry.type !== "file") continue;
    for (const dirName of deriveDirNames(entry.name)) {
      if (!byName.has(dirName)) {
        byName.set(dirName, {
          name: dirName,
          type: "dir",
          mode: DEFAULT_DIR_MODE,
          bytes: new Uint8Array(0),
        });
      }
    }
  }
  return [...byName.values()];
}

/** Build one entry's 512-byte USTAR header, checksum included. */
function buildHeader(entry: TarEntry): Uint8Array {
  const block = new Uint8Array(BLOCK_SIZE);
  const { name, prefix } = splitName(entry.name);
  const size = entry.type === "dir" ? 0 : entry.bytes.length;

  writeStringField(block, name, NAME_OFFSET, NAME_SIZE);
  writeOctalField(block, entry.mode, MODE_OFFSET, MODE_SIZE);
  writeOctalField(block, 0, UID_OFFSET, UID_SIZE);
  writeOctalField(block, 0, GID_OFFSET, GID_SIZE);
  writeOctalField(block, size, SIZE_OFFSET, SIZE_SIZE);
  writeOctalField(block, 0, MTIME_OFFSET, MTIME_SIZE); // mtime = epoch
  block.fill(0x20, CHECKSUM_OFFSET, CHECKSUM_OFFSET + CHECKSUM_SIZE); // 8 spaces, for the sum below
  block[TYPEFLAG_OFFSET] = (entry.type === "dir" ? TYPE_DIR : TYPE_REGULAR).charCodeAt(0);
  writeStringField(block, "ustar", MAGIC_OFFSET, MAGIC_SIZE); // + trailing NUL from zero-init
  writeStringField(block, "00", VERSION_OFFSET, VERSION_SIZE);
  // uname/gname are left as "" — the block's zero-fill already reads back
  // as an empty string through `fieldToString`.
  if (prefix !== "") {
    writeStringField(block, prefix, PREFIX_OFFSET, PREFIX_SIZE);
  }

  // The ustar checksum: unsigned byte sum of the whole header with the
  // checksum field itself standing in as 8 spaces (already filled above),
  // written back as 6 octal digits + NUL + space (interop contract §4).
  let sum = 0;
  for (let i = 0; i < BLOCK_SIZE; i++) sum += block[i] ?? 0;
  const checksumDigits = sum.toString(8).padStart(6, "0");
  writeStringField(block, checksumDigits, CHECKSUM_OFFSET, 6);
  block[CHECKSUM_OFFSET + 6] = 0; // NUL
  block[CHECKSUM_OFFSET + 7] = 0x20; // space

  return block;
}

/** Zero-pad `data` up to the next 512-byte boundary. */
function padToBlockSize(data: Uint8Array): Uint8Array {
  const remainder = data.length % BLOCK_SIZE;
  if (remainder === 0) return data;
  const padded = new Uint8Array(data.length + (BLOCK_SIZE - remainder));
  padded.set(data);
  return padded;
}

/**
 * Pack vault entries into a POSIX ustar tar stream matching Go's
 * `archive/tar.Writer` output byte-for-byte for identical content (interop
 * contract §4): epoch mtime, zeroed uid/gid, empty uname/gname, and one
 * global lexicographic sort of the full path across every entry.
 *
 * Directory entries for every directory a file's path implies are added
 * automatically (see `mergeDerivedDirs`); an explicit directory entry
 * already in `entries` is kept instead of the derived default.
 *
 * Each entry's `bytes` are carried through byte-for-byte — required for
 * `secrets/**\/value.age`, which this client can never decrypt and must
 * never mutate.
 *
 * @param entries the vault's files (and, optionally, directories).
 * @returns the tar stream, terminated by two all-zero 512-byte blocks.
 */
export function writeTar(entries: TarEntry[]): Uint8Array {
  const withDirs = mergeDerivedDirs(entries);
  withDirs.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));

  const chunks: Uint8Array[] = [];
  for (const entry of withDirs) {
    chunks.push(buildHeader(entry));
    if (entry.type === "file" && entry.bytes.length > 0) {
      chunks.push(padToBlockSize(entry.bytes));
    }
  }
  chunks.push(new Uint8Array(BLOCK_SIZE * 2)); // two all-zero end-of-archive blocks

  const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.length;
  }
  return out;
}
