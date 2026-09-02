import { describe, expect, it } from "vitest";
import type { Item } from "../vault/model.js";
import { serializeItem, withReviewKept } from "./review.js";

const DRAFT_ITEM: Item = {
  address: "memory/beta",
  kind: "memory",
  hook: "Beta notes",
  body: "This is the body text.\nIt has two lines.\n",
  frontmatter: { description: "Beta notes", review: "draft" },
  review: "draft",
};

const NO_REVIEW_ITEM: Item = {
  address: "skill/widget-fixer",
  kind: "skill",
  hook: "Fixes widgets",
  body: "# Widget Fixer\n\nRun the fixer script.\n",
  frontmatter: { description: "Fixes widgets" },
};

const KEPT_ITEM: Item = {
  address: "memory/alpha",
  kind: "memory",
  hook: "Alpha notes",
  body: "alpha body\n",
  frontmatter: { description: "Alpha notes", review: "kept" },
  review: "kept",
};

describe("serializeItem", () => {
  it("rebuilds the full file: frontmatter block, then the given prose", () => {
    const out = serializeItem(DRAFT_ITEM, DRAFT_ITEM.body);
    expect(out).toBe(
      "---\ndescription: Beta notes\nreview: draft\n---\n" + DRAFT_ITEM.body,
    );
  });

  it("uses the given prose, not the item's own body, when they differ", () => {
    const out = serializeItem(DRAFT_ITEM, "new prose\n");
    expect(out).toBe("---\ndescription: Beta notes\nreview: draft\n---\nnew prose\n");
  });

  it("returns the prose as-is when the item has no frontmatter", () => {
    const item: Item = { ...NO_REVIEW_ITEM, frontmatter: {} };
    const out = serializeItem(item, "plain body, no frontmatter\n");
    expect(out).toBe("plain body, no frontmatter\n");
  });
});

describe("withReviewKept", () => {
  it("changes only the review line to kept; the body is otherwise unchanged", () => {
    const out = withReviewKept(DRAFT_ITEM);
    expect(out).toBe(
      "---\ndescription: Beta notes\nreview: kept\n---\n" + DRAFT_ITEM.body,
    );
    // Everything except the review line is byte-identical.
    expect(out).toContain(DRAFT_ITEM.body);
    expect(out).toContain("description: Beta notes");
    expect(out).not.toContain("review: draft");
  });

  it("adds a review: kept line, right before the closing ---, when none exists", () => {
    const out = withReviewKept(NO_REVIEW_ITEM);
    expect(out).toBe(
      "---\ndescription: Fixes widgets\nreview: kept\n---\n" + NO_REVIEW_ITEM.body,
    );
  });

  it("leaves an already-kept item as kept", () => {
    const out = withReviewKept(KEPT_ITEM);
    expect(out).toBe("---\ndescription: Alpha notes\nreview: kept\n---\n" + KEPT_ITEM.body);
  });

  it("appends review: kept for an item with no frontmatter at all", () => {
    const item: Item = { ...NO_REVIEW_ITEM, frontmatter: {}, body: "plain body\n" };
    const out = withReviewKept(item);
    expect(out).toBe("---\nreview: kept\n---\nplain body\n");
  });
});
