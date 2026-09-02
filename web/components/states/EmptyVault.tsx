"use client";

import type { JSX } from "react";

/**
 * Shown after a successful pull whose vault has no items yet
 * (`version === ""`). This means the Mac has not synced any skill or
 * memory to `loadoutd`.
 */
export function EmptyVault(props: { onRetry?: () => void }): JSX.Element {
  return (
    <div className="mx-auto max-w-md space-y-4 rounded-lg border border-slate-300 bg-slate-50 p-6 text-center">
      <h2 className="text-lg font-semibold text-slate-900">The vault is empty</h2>
      <p className="text-sm text-slate-700">
        The connection works. But your Mac has not synced any skill or
        memory to loadoutd yet. Run a sync on your Mac, then retry.
      </p>
      {props.onRetry ? (
        <button
          type="button"
          onClick={props.onRetry}
          className="rounded bg-slate-700 px-3 py-2 text-sm font-medium text-white hover:bg-slate-800"
        >
          Retry
        </button>
      ) : null}
    </div>
  );
}
