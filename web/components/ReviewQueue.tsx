"use client";

import type { JSX } from "react";
import type { Item } from "../lib/vault/model.js";

/** The last path segment of an address — its display title. For example,
 * `"memory/beta"` shows as `"beta"`. */
function nameFromAddress(address: string): string {
  const slash = address.lastIndexOf("/");
  return slash === -1 ? address : address.slice(slash + 1);
}

/**
 * Lists every draft item, each with a Keep button. `page.tsx` supplies
 * `drafts` straight from the pulled vault (every item with
 * `review === "draft"`); once a Keep succeeds and the page re-pulls, the
 * kept item's `review` is no longer `"draft"`, so it drops out of the
 * `drafts` array the next render — this component holds no list state
 * of its own.
 */
export function ReviewQueue(props: {
  drafts: Item[];
  onKeep: (item: Item) => Promise<void>;
}): JSX.Element {
  if (props.drafts.length === 0) {
    return <p className="text-sm text-slate-400 dark:text-slate-500">No drafts to review.</p>;
  }

  return (
    <ul className="mx-auto max-w-2xl space-y-2">
      {props.drafts.map((item) => (
        <li
          key={item.address}
          className="flex items-center justify-between gap-4 rounded-lg border border-slate-200 p-3 dark:border-slate-800"
        >
          <div className="min-w-0">
            <div className="text-sm font-medium text-slate-900 dark:text-slate-100">
              {nameFromAddress(item.address)}
            </div>
            <div className="truncate text-xs text-slate-500 dark:text-slate-400">{item.hook}</div>
          </div>
          <button
            type="button"
            onClick={() => void props.onKeep(item)}
            className="ld-focus shrink-0 rounded-md bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-emerald-700 dark:hover:bg-emerald-500"
          >
            Keep
          </button>
        </li>
      ))}
    </ul>
  );
}
