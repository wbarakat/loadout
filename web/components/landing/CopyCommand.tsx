"use client";

import { useState, type JSX, type ReactNode } from "react";

/**
 * A block of copyable text with a copy control.
 *
 * `value` is what lands on the clipboard; `children` is what the page
 * shows, so a long prompt can be displayed in a readable form while the
 * full text is copied. The control is a plain text button rather than an
 * icon, so its action is readable without a tooltip.
 */
export function CopyCommand(props: {
  value: string;
  children: ReactNode;
  /** Larger treatment for the page's primary command. */
  primary?: boolean;
}): JSX.Element {
  const [copied, setCopied] = useState(false);

  async function copy(): Promise<void> {
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        await navigator.clipboard.writeText(props.value);
        setCopied(true);
        setTimeout(() => setCopied(false), 1600);
      }
    } catch {
      // Clipboard blocked or missing. The text is on the page, so it can
      // still be selected by hand.
    }
  }

  return (
    <div
      className={`flex items-start gap-4 border-y border-black/15 dark:border-white/20 ${
        props.primary ? "py-6" : "py-4"
      }`}
    >
      <pre
        className={`min-w-0 flex-1 overflow-x-auto font-mono leading-relaxed ${
          props.primary ? "text-base sm:text-lg" : "text-[13px] sm:text-sm"
        }`}
      >
        {props.children}
      </pre>
      <button
        type="button"
        onClick={() => {
          void copy();
        }}
        aria-label={copied ? "Copied to clipboard" : "Copy to clipboard"}
        className="shrink-0 font-mono text-xs underline decoration-1 underline-offset-4 outline-none focus-visible:ring-2 focus-visible:ring-[#0033FF] dark:focus-visible:ring-[#8AA0FF]"
      >
        <span className={copied ? "text-[#0033FF] dark:text-[#8AA0FF]" : "opacity-60 hover:opacity-100"}>
          {copied ? "copied" : "copy"}
        </span>
      </button>
    </div>
  );
}
