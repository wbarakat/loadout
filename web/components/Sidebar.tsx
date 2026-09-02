"use client";

import type { JSX } from "react";

/** The dashboard's five top-level sections. */
export type Section = "skills" | "memory" | "secrets" | "review" | "settings";

/** How many items each countable section holds. `settings` has no count. */
export interface SectionCounts {
  skills: number;
  memory: number;
  secrets: number;
  review: number;
}

const NAV_ITEMS: ReadonlyArray<{ id: Section; label: string }> = [
  { id: "skills", label: "Skills" },
  { id: "memory", label: "Memory" },
  { id: "secrets", label: "Secrets" },
  { id: "review", label: "Review" },
  { id: "settings", label: "Settings" },
];

/**
 * The Linear-style left sidebar: one nav entry per section, each with its
 * item count. The active section is visually marked; clicking any entry
 * fires `onSelect`.
 */
export function Sidebar(props: {
  active: Section;
  counts: SectionCounts;
  onSelect: (s: Section) => void;
}): JSX.Element {
  return (
    <nav
      aria-label="Sections"
      className="flex w-56 shrink-0 flex-col gap-0.5 border-r border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-slate-900"
    >
      {NAV_ITEMS.map((item) => {
        const isActive = item.id === props.active;
        const count = item.id === "settings" ? undefined : props.counts[item.id];
        return (
          <button
            key={item.id}
            type="button"
            aria-current={isActive ? "page" : undefined}
            onClick={() => props.onSelect(item.id)}
            className={`ld-focus flex w-full items-center justify-between rounded-md px-3 py-2 text-sm font-medium transition-colors ${
              isActive
                ? "bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900"
                : "text-slate-600 hover:bg-slate-200/70 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-slate-100"
            }`}
          >
            <span>{item.label}</span>
            {count !== undefined ? (
              <span
                aria-hidden="true"
                className={`rounded-full px-2 py-0.5 text-xs tabular-nums ${
                  isActive
                    ? "bg-white/15 text-white dark:bg-slate-900/10 dark:text-slate-900"
                    : "bg-slate-200 text-slate-600 dark:bg-slate-800 dark:text-slate-400"
                }`}
              >
                {count}
              </span>
            ) : null}
          </button>
        );
      })}
    </nav>
  );
}
