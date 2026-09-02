"use client";

import type { JSX } from "react";

/**
 * Shown after a successful pull whose vault has no items yet
 * (`version === ""`). This means the Mac has not synced any skill or
 * memory to `loadoutd`.
 */
export function EmptyVault(props: { onRetry?: () => void }): JSX.Element {
  return (
    <div className="mx-auto w-full max-w-md space-y-4 rounded-xl border border-slate-200 bg-slate-50 p-6 text-center dark:border-slate-800 dark:bg-slate-900">
      <h2 className="text-lg font-semibold tracking-tight text-slate-900 dark:text-slate-50">
        The vault is empty
      </h2>
      <p className="text-sm leading-relaxed text-slate-600 dark:text-slate-400">
        The connection works. But your Mac has not synced any skill or
        memory to loadoutd yet. Run a sync on your Mac, then retry.
      </p>
      {props.onRetry ? (
        <button
          type="button"
          onClick={props.onRetry}
          className="ld-focus rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-slate-700 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white"
        >
          Retry
        </button>
      ) : null}
    </div>
  );
}
