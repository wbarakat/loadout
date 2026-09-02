"use client";

import type { JSX } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Item } from "../lib/vault/model.js";

/** The last path segment of an address — its display title. For example,
 * `"skill/widget-fixer"` shows as `"widget-fixer"`. */
function nameFromAddress(address: string): string {
  const slash = address.lastIndexOf("/");
  return slash === -1 ? address : address.slice(slash + 1);
}

/** True when the item is a draft. Mirrors Go: an empty or missing `review`
 * reads as "kept" — only the literal string `"draft"` is a draft. */
function isDraft(item: Item): boolean {
  return item.review === "draft";
}

/**
 * Reads one skill or memory item in full: its hook, its body as
 * GitHub-flavored Markdown, its provenance line (when present), and a
 * review badge. Also shows the Edit action, and — for a draft item only —
 * the Keep action. Both actions only fire their callback here; the real
 * edit/keep behavior lands in Task 6.
 */
export function ItemDetail(props: {
  item: Item;
  onEdit?: (item: Item) => void;
  onKeep?: (item: Item) => void;
}): JSX.Element {
  const { item } = props;
  const draft = isDraft(item);

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h2 className="truncate text-lg font-semibold tracking-tight text-slate-900 dark:text-slate-50">
            {nameFromAddress(item.address)}
          </h2>
          <p className="mt-0.5 text-sm text-slate-500 dark:text-slate-400">{item.hook}</p>
        </div>
        <span
          className={`shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold uppercase tracking-wide ${
            draft
              ? "bg-amber-100 text-amber-800 dark:bg-amber-500/15 dark:text-amber-300"
              : "bg-emerald-100 text-emerald-800 dark:bg-emerald-500/15 dark:text-emerald-300"
          }`}
        >
          {draft ? "draft" : "kept"}
        </span>
      </div>

      {item.provenance !== undefined ? (
        <p className="text-xs text-slate-400 dark:text-slate-500">{item.provenance}</p>
      ) : null}

      <div className="flex gap-2">
        {props.onEdit ? (
          <button
            type="button"
            onClick={() => props.onEdit?.(item)}
            className="ld-focus rounded-md bg-slate-900 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-slate-700 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white"
          >
            Edit
          </button>
        ) : null}
        {draft && props.onKeep ? (
          <button
            type="button"
            onClick={() => props.onKeep?.(item)}
            className="ld-focus rounded-md bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-emerald-700 dark:hover:bg-emerald-500"
          >
            Keep
          </button>
        ) : null}
      </div>

      <div className="ld-prose border-t border-slate-100 pt-5 dark:border-slate-800">
        <Markdown remarkPlugins={[remarkGfm]}>{item.body}</Markdown>
      </div>
    </div>
  );
}
