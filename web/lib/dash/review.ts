/**
 * Builds the full file content for an Edit or a Keep, by splicing the
 * RAW bytes of the item's file — never by reserializing `Item.frontmatter`
 * (a parsed `Record<string, string>`) back into text.
 *
 * Why raw bytes, not the parsed map: `Item.frontmatter` only keeps each
 * line's trimmed key/value — reserializing it would normalize whitespace
 * and silently drop any blank line or comment-like (no-colon) line inside
 * the frontmatter block. Go's `SetReviewKept`
 * (`internal/vault/review.go`) never does that: it does line surgery on
 * the raw file text, changing only the one line it means to change. This
 * module mirrors that: it reads the file's raw text straight from the
 * pulled tar entries (`rawFileFor`) and only ever touches the exact bytes
 * the edit/keep is about — every other byte, however unusual, survives
 * untouched.
 *
 * `Item.body` (from `../vault/model.ts`) is only the prose AFTER the
 * frontmatter block — `parseFrontmatter` strips the `---\n...\n---\n`
 * header before it ever reaches `Item.body`. But `commitEdit` (and the
 * `applyEdit` it calls internally) writes its `newBody` argument as the
 * ENTIRE file's bytes, frontmatter included (`applyEdit` replaces a tar
 * entry's bytes outright, with no frontmatter splitting of its own). So a
 * caller that wants to edit only the prose must rebuild the full file —
 * frontmatter preserved, prose replaced — before calling `commitEdit`.
 * `applyRawEdit` does that rebuild; `withReviewKept` does the analogous
 * rebuild for a Keep.
 */
import type { TarEntry } from "../vault/tar.js";
import { targetFileName } from "../vault/model.js";

/**
 * Reads the raw file text for a skill or memory address, straight from
 * the pulled tar entries — no frontmatter parsing, no reformatting,
 * nothing beyond UTF-8 decoding. This is the byte-for-byte starting
 * point `applyRawEdit` and `withReviewKept` splice into.
 *
 * @throws (via `targetFileName`) if `address` names a secret — secrets
 * are never read raw from the browser, let alone edited.
 * @throws if no matching entry exists in `entries`. This should not
 * happen: every displayed item came from `parseVault(entries)` on this
 * same pulled snapshot.
 */
export function rawFileFor(entries: TarEntry[], address: string): string {
  const name = targetFileName(address);
  const entry = entries.find((e) => e.type === "file" && e.name === name);
  if (entry === undefined) {
    throw new Error(`no such item in the pulled vault entries: "${address}"`);
  }
  return new TextDecoder().decode(entry.bytes);
}

function normalizeLineEndings(raw: string): string {
  let text = raw;
  if (text.startsWith("﻿")) text = text.slice(1);
  return text.replace(/\r\n/g, "\n");
}

/**
 * Locates a raw file's leading `---\n...\n---` frontmatter delimiters,
 * after the same BOM-strip / CRLF-normalize tolerance `parseFrontmatter`
 * (`../vault/model.ts`) applies on read. Returns `null` when the file has
 * none — the exact same `startsWith`/`indexOf` checks as
 * `parseFrontmatter`'s own "no frontmatter, whole file is the body"
 * fallback.
 *
 * `innerRaw` is the frontmatter's lines, RAW — the untouched text between
 * the opening `---\n` and the closing `\n---`. `tailFromClosing` is
 * EVERYTHING from that closing `\n---` onward, verbatim, unsplit — the
 * one piece `withReviewKept` must reattach without changing a byte of,
 * to match Go's own reconstruction (`"---\n" + lines.join("\n") +
 * rest[end:]`) exactly.
 */
function locateFrontmatter(
  rawText: string,
): { innerRaw: string; tailFromClosing: string } | null {
  const text = normalizeLineEndings(rawText);
  if (!text.startsWith("---\n")) return null;
  const rest = text.slice("---\n".length);
  const end = rest.indexOf("\n---");
  if (end < 0) return null;
  return { innerRaw: rest.slice(0, end), tailFromClosing: rest.slice(end) };
}

/**
 * Builds the full file content for an Edit: `rawFile`'s frontmatter block
 * preserved byte-for-byte, followed by `newProse`. Only the text after
 * the closing `---` (plus its one separating newline, when the original
 * had one — the same one `parseFrontmatter` strips off `Item.body`)
 * changes; the frontmatter block itself — spacing, blank lines,
 * comment-like lines, key order — is never touched, let alone
 * reformatted from a parsed map.
 *
 * When `rawFile` has no frontmatter block (not expected for a real vault
 * item), the file IS the prose: this returns `newProse` unchanged.
 */
export function applyRawEdit(rawFile: string, newProse: string): string {
  const fm = locateFrontmatter(rawFile);
  if (fm === null) {
    return newProse;
  }
  // `tailFromClosing` is "\n---" followed by whatever came after in the
  // original file. `parseFrontmatter` treats everything after that
  // "\n---" as the body, MINUS exactly one leading newline when present
  // (the closing line's own terminator) — reproduce that same one-
  // newline separator here, so `newProse` (edited from `item.body`,
  // itself built the same way) lines up with the original split exactly.
  const afterClosing = fm.tailFromClosing.slice("\n---".length);
  const separator = afterClosing.startsWith("\n") ? "\n" : "";
  return "---\n" + fm.innerRaw + "\n---" + separator + newProse;
}

/**
 * Returns `rawFile`'s content with its `review` frontmatter line set to
 * `kept`. Mirrors Go's `SetReviewKept` (`internal/vault/review.go`,
 * lines 37-52) exactly: line surgery on the raw frontmatter block only.
 * The line whose key (text before the first `:`, trimmed) is `review` is
 * replaced with the literal line `review: kept`; when no such line
 * exists, `review: kept` is appended as the block's last line (i.e.
 * immediately before the closing `---`). Every other byte — spacing,
 * blank lines, comment-like lines, unknown keys, key order, and the
 * entire prose — is untouched: `tailFromClosing` (the closing `---` and
 * everything after it) is reattached verbatim, unsplit.
 *
 * @throws when `rawFile` has no frontmatter block — the same case Go's
 * `SetReviewKept` returns an error for.
 */
export function withReviewKept(rawFile: string): string {
  const fm = locateFrontmatter(rawFile);
  if (fm === null) {
    throw new Error(
      "withReviewKept: the item has no frontmatter block to set review: kept in",
    );
  }

  const lines = fm.innerRaw.split("\n");
  let found = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    const colon = line.indexOf(":");
    // Mirrors Go's `strings.Cut(line, ":")`: a line with no colon at all
    // never matches — `ok` is false — so a comment-like or blank line is
    // never mistaken for a `review` line.
    if (colon >= 0 && line.slice(0, colon).trim() === "review") {
      lines[i] = "review: kept";
      found = true;
      break;
    }
  }
  if (!found) {
    lines.push("review: kept");
  }

  return "---\n" + lines.join("\n") + fm.tailFromClosing;
}
