"use client";

import { useState, type JSX } from "react";

/**
 * A button that copies `value` to the clipboard.
 *
 * Shows "Copied" for a short time after a click. Never puts `value` into a
 * link, a log line, or a URL — the caller is trusted to pass only text that
 * is already safe to display (a recipient or a CLI command, never a token).
 */
export function CopyButton(props: { value: string; label: string }): JSX.Element {
  const [copied, setCopied] = useState(false);

  async function handleClick(): Promise<void> {
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        await navigator.clipboard.writeText(props.value);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }
    } catch {
      // The clipboard is blocked or missing. The text next to this
      // button is still there, so the user can select and copy it by
      // hand.
    }
  }

  return (
    <button
      type="button"
      onClick={() => {
        void handleClick();
      }}
      className="shrink-0 rounded border border-slate-300 px-2 py-1 text-xs font-medium text-slate-700 hover:bg-slate-100"
    >
      {copied ? "Copied" : props.label}
    </button>
  );
}
