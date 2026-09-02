"use client";

import type { ChangeEvent, JSX } from "react";

/** One row the item list can render. No field ever carries a secret value —
 * a secret row's `hook` is its metadata's `hook`/`service` frontmatter, and
 * `address` is only ever used for selection, never for display of a value. */
export interface ListRow {
  address: string;
  title: string;
  hook: string;
  draft: boolean;
}

/** True when `query` (trimmed, case-insensitive) is empty, or is a
 * substring of the row's address, title, or hook. */
function matchesQuery(row: ListRow, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (q === "") return true;
  return (
    row.address.toLowerCase().includes(q) ||
    row.title.toLowerCase().includes(q) ||
    row.hook.toLowerCase().includes(q)
  );
}

/**
 * A search box plus the filtered list of rows it controls. Filtering runs
 * here, over `rows`, using `query` — case-insensitive, over address, title,
 * and hook. Selecting a row fires `onSelect(address)`; the row named by
 * `selectedAddress` is marked.
 */
export function ItemList(props: {
  rows: ListRow[];
  selectedAddress?: string;
  query: string;
  onQuery: (q: string) => void;
  onSelect: (address: string) => void;
}): JSX.Element {
  const filtered = props.rows.filter((row) => matchesQuery(row, props.query));

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-slate-200 p-3 dark:border-slate-800">
        <label htmlFor="item-search" className="sr-only">
          Search
        </label>
        <input
          id="item-search"
          type="search"
          role="searchbox"
          value={props.query}
          onChange={(e: ChangeEvent<HTMLInputElement>) => props.onQuery(e.target.value)}
          placeholder="Search…"
          className="ld-focus w-full rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-slate-900 placeholder:text-slate-400 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:placeholder:text-slate-600"
        />
      </div>
      <ul className="flex-1 overflow-y-auto divide-y divide-slate-100 dark:divide-slate-800">
        {filtered.map((row) => {
          const isSelected = row.address === props.selectedAddress;
          return (
            <li key={row.address}>
              <button
                type="button"
                aria-current={isSelected ? "true" : undefined}
                onClick={() => props.onSelect(row.address)}
                className={`ld-focus flex w-full flex-col items-start gap-0.5 border-l-2 px-3 py-2.5 text-left text-sm transition-colors ${
                  isSelected
                    ? "border-blue-600 bg-blue-50 dark:border-blue-400 dark:bg-blue-500/10"
                    : "border-transparent hover:bg-slate-50 dark:hover:bg-slate-900"
                }`}
              >
                <span className="flex items-center gap-2 font-medium text-slate-900 dark:text-slate-100">
                  {row.title}
                  {row.draft ? (
                    <span className="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-800 dark:bg-amber-500/15 dark:text-amber-300">
                      draft
                    </span>
                  ) : null}
                </span>
                <span className="truncate text-xs text-slate-500 dark:text-slate-400">
                  {row.hook}
                </span>
              </button>
            </li>
          );
        })}
        {filtered.length === 0 ? (
          <li className="p-4 text-center text-sm text-slate-400 dark:text-slate-500">
            No items match.
          </li>
        ) : null}
      </ul>
    </div>
  );
}
