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
      <div className="border-b border-slate-200 p-3">
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
          className="w-full rounded border border-slate-300 px-3 py-2 text-sm"
        />
      </div>
      <ul className="flex-1 overflow-y-auto divide-y divide-slate-100">
        {filtered.map((row) => {
          const isSelected = row.address === props.selectedAddress;
          return (
            <li key={row.address}>
              <button
                type="button"
                aria-current={isSelected ? "true" : undefined}
                onClick={() => props.onSelect(row.address)}
                className={`flex w-full flex-col items-start gap-0.5 px-3 py-2 text-left text-sm ${
                  isSelected ? "bg-blue-50" : "hover:bg-slate-50"
                }`}
              >
                <span className="flex items-center gap-2 font-medium text-slate-900">
                  {row.title}
                  {row.draft ? (
                    <span className="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-amber-800">
                      draft
                    </span>
                  ) : null}
                </span>
                <span className="text-xs text-slate-600">{row.hook}</span>
              </button>
            </li>
          );
        })}
        {filtered.length === 0 ? (
          <li className="p-3 text-sm text-slate-500">No items match.</li>
        ) : null}
      </ul>
    </div>
  );
}
