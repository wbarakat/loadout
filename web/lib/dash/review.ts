/**
 * Builds the full file content for a skill or memory item, and the
 * "Keep" variant of that content.
 *
 * `Item.body` (from `../vault/model.js`) is only the prose AFTER the
 * frontmatter block — `parseFrontmatter` strips the `---\n...\n---\n`
 * header before it ever reaches `Item.body`. But `commitEdit` (and the
 * `applyEdit` it calls internally) writes its `newBody` argument as the
 * ENTIRE file's bytes, frontmatter included (see `applyEdit` in
 * `../vault/model.js`: it replaces a tar entry's bytes outright with
 * `newBody`, with no frontmatter splitting of its own). So a caller
 * that wants to edit only the prose must rebuild the full file — the
 * frontmatter block plus the (possibly edited) prose — before calling
 * `commitEdit`. `serializeItem` does that rebuild.
 */
import type { Item } from "../vault/model.js";

/**
 * Rebuilds an item's full file content: its frontmatter block, then
 * `newProse`. Mirrors how `parseFrontmatter` (`../vault/model.js`) splits
 * a file, in reverse: `key: value` lines (in the item's own key order),
 * wrapped in `---` markers, followed by the body.
 *
 * When the item has no frontmatter at all (an empty map — the file never
 * had a `---` block), the file IS the prose, so this returns `newProse`
 * unchanged.
 */
export function serializeItem(item: Item, newProse: string): string {
  const lines = Object.entries(item.frontmatter).map(
    ([key, value]) => `${key}: ${value}`,
  );
  if (lines.length === 0) {
    return newProse;
  }
  return `---\n${lines.join("\n")}\n---\n${newProse}`;
}

/**
 * Returns an item's full file content with its `review` frontmatter line
 * set to `kept`. Mirrors Go's `SetReviewKept`
 * (`internal/vault/review.go`): changes only that one line — every other
 * frontmatter line and the whole body stay exactly as they were — or
 * adds a `review: kept` line, right before the closing `---`, when no
 * review line exists yet.
 *
 * This is the `newBody` a "Keep" action passes to `commitEdit`.
 */
export function withReviewKept(item: Item): string {
  // Object spread preserves each existing key's original position; the
  // explicit `review` only lands at the end when it was not already a
  // key of `item.frontmatter` — matching Go's "found? edit in place :
  // append at the end" behavior.
  const frontmatter = { ...item.frontmatter, review: "kept" };
  return serializeItem({ ...item, frontmatter }, item.body);
}
