import { describe, expect, it } from "vitest";
import type { TarEntry } from "../vault/tar.js";
import { applyRawEdit, rawFileFor, withReviewKept } from "./review.js";

function fileEntry(name: string, text: string): TarEntry {
  return { name, type: "file", mode: 0o644, bytes: new TextEncoder().encode(text) };
}

// A frontmatter block deliberately NOT in canonical "key: value" form:
// extra inner spacing on the review line, a blank line, a comment-like
// (no-colon) line, an unknown key, and non-alphabetical key order. A
// reserialize-from-parsed-map approach would normalize or drop all of
// this; raw byte splicing must not.
const NON_CANONICAL_DRAFT =
  "---\n" +
  "zeta: last-ish key\n" +
  "description: Beta notes\n" +
  "\n" +
  "# a comment-like line, no colon\n" +
  "review:   draft\n" +
  "alpha: first-ish key\n" +
  "---\n" +
  "This is the prose.\nIt has two lines.\n";

const NON_CANONICAL_NO_REVIEW =
  "---\n" +
  "zeta: last-ish key\n" +
  "\n" +
  "# a comment-like line, no colon\n" +
  "alpha: first-ish key\n" +
  "---\n" +
  "Prose without any review line yet.\n";

const NON_CANONICAL_KEPT =
  "---\n" +
  "zeta: last-ish key\n" +
  "\n" +
  "review:   kept\n" +
  "---\n" +
  "Already kept.\n";

describe("rawFileFor", () => {
  const entries: TarEntry[] = [
    fileEntry("memory/alpha.md", "alpha body"),
    fileEntry("skills/widget-fixer/SKILL.md", "---\ndescription: fixes\n---\nbody"),
  ];

  it("finds a memory address's file and decodes it", () => {
    expect(rawFileFor(entries, "memory/alpha")).toBe("alpha body");
  });

  it("finds a skill address's file and decodes it", () => {
    expect(rawFileFor(entries, "skill/widget-fixer")).toBe(
      "---\ndescription: fixes\n---\nbody",
    );
  });

  it("throws when no matching entry exists", () => {
    expect(() => rawFileFor(entries, "memory/missing")).toThrow(/no such item/i);
  });

  it("throws for a secret address, never reading one raw", () => {
    expect(() => rawFileFor(entries, "secret/stripe-key")).toThrow();
  });
});

describe("applyRawEdit", () => {
  it("preserves a simple frontmatter block byte-for-byte, replacing only the prose", () => {
    const raw = "---\ndescription: fixes widgets\n---\nold prose\n";
    const out = applyRawEdit(raw, "new prose\n");
    expect(out).toBe("---\ndescription: fixes widgets\n---\nnew prose\n");
  });

  it("preserves a NON-canonical frontmatter block byte-for-byte on edit", () => {
    const out = applyRawEdit(NON_CANONICAL_DRAFT, "Brand new prose.\n");
    expect(out).toBe(
      "---\n" +
        "zeta: last-ish key\n" +
        "description: Beta notes\n" +
        "\n" +
        "# a comment-like line, no colon\n" +
        "review:   draft\n" +
        "alpha: first-ish key\n" +
        "---\n" +
        "Brand new prose.\n",
    );
    // The frontmatter block — including its extra spacing, blank line,
    // comment-like line, unknown keys, and their order — is untouched.
    const frontmatterPrefix = NON_CANONICAL_DRAFT.slice(
      0,
      NON_CANONICAL_DRAFT.indexOf("This is the prose"),
    );
    expect(out.startsWith(frontmatterPrefix)).toBe(true);
    expect(out).not.toContain("This is the prose");
  });

  it("returns the new prose unchanged when the file has no frontmatter block", () => {
    const out = applyRawEdit("just plain body text, no frontmatter\n", "edited plain text\n");
    expect(out).toBe("edited plain text\n");
  });
});

describe("withReviewKept", () => {
  it("changes only the review line to kept on a simple file", () => {
    const raw = "---\ndescription: fixes widgets\nreview: draft\n---\nbody\n";
    const out = withReviewKept(raw);
    expect(out).toBe("---\ndescription: fixes widgets\nreview: kept\n---\nbody\n");
  });

  it("changes ONLY the review line on a non-canonical file — every other byte identical", () => {
    const out = withReviewKept(NON_CANONICAL_DRAFT);
    expect(out).toBe(
      "---\n" +
        "zeta: last-ish key\n" +
        "description: Beta notes\n" +
        "\n" +
        "# a comment-like line, no colon\n" +
        "review: kept\n" +
        "alpha: first-ish key\n" +
        "---\n" +
        "This is the prose.\nIt has two lines.\n",
    );
    // Fidelity, spelled out: the blank line, the comment-like line, the
    // unknown keys (still in their original order), and the whole prose
    // all survive untouched.
    expect(out).toContain("zeta: last-ish key\ndescription: Beta notes\n\n#");
    expect(out).toContain("# a comment-like line, no colon\n");
    expect(out).toContain("alpha: first-ish key\n---\n");
    expect(out).toContain("This is the prose.\nIt has two lines.\n");
    // Only the review line's value changed.
    expect(out).not.toContain("review:   draft");
  });

  it("inserts review: kept right before the closing --- when no review line exists", () => {
    const out = withReviewKept(NON_CANONICAL_NO_REVIEW);
    expect(out).toBe(
      "---\n" +
        "zeta: last-ish key\n" +
        "\n" +
        "# a comment-like line, no colon\n" +
        "alpha: first-ish key\n" +
        "review: kept\n" +
        "---\n" +
        "Prose without any review line yet.\n",
    );
  });

  it("leaves an already-kept item as kept, byte-identical apart from the review line", () => {
    const out = withReviewKept(NON_CANONICAL_KEPT);
    expect(out).toBe(
      "---\n" + "zeta: last-ish key\n" + "\n" + "review: kept\n" + "---\n" + "Already kept.\n",
    );
  });

  it("throws when the file has no frontmatter block at all", () => {
    expect(() => withReviewKept("just plain body text, no frontmatter\n")).toThrow(
      /no frontmatter block/i,
    );
  });
});
