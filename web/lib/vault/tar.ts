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
const SIZE_OFFSET = 124;
const SIZE_SIZE = 12;
const TYPEFLAG_OFFSET = 156;
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
