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
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold text-slate-900">
            {nameFromAddress(item.address)}
          </h2>
          <p className="text-sm text-slate-600">{item.hook}</p>
        </div>
        <span
          className={`shrink-0 rounded px-2 py-0.5 text-xs font-semibold uppercase ${
            draft
              ? "bg-amber-100 text-amber-800"
              : "bg-emerald-100 text-emerald-800"
          }`}
        >
          {draft ? "draft" : "kept"}
        </span>
      </div>

      {item.provenance !== undefined ? (
        <p className="text-xs text-slate-500">{item.provenance}</p>
      ) : null}

      <div className="flex gap-2">
        {props.onEdit ? (
          <button
            type="button"
            onClick={() => props.onEdit?.(item)}
            className="rounded bg-slate-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-800"
          >
            Edit
          </button>
        ) : null}
        {draft && props.onKeep ? (
          <button
            type="button"
            onClick={() => props.onKeep?.(item)}
            className="rounded bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-700"
          >
            Keep
          </button>
        ) : null}
      </div>

      <div className="max-w-none text-sm leading-relaxed text-slate-800 [&_a]:text-blue-700 [&_a]:underline [&_h1]:mt-4 [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:mt-4 [&_h2]:text-base [&_h2]:font-semibold [&_li]:ml-5 [&_li]:list-disc [&_p]:my-2 [&_table]:my-2 [&_td]:border [&_td]:border-slate-300 [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:border-slate-300 [&_th]:bg-slate-50 [&_th]:px-2 [&_th]:py-1">
        <Markdown remarkPlugins={[remarkGfm]}>{item.body}</Markdown>
      </div>
    </div>
  );
}
