"use client";

import { useState, type JSX } from "react";
import type { Item } from "../lib/vault/model.js";

/**
 * Edits one skill or memory item's prose. The textarea starts with the
 * item's current body — the prose after its frontmatter, as `../lib/vault/
 * model.js` parses it. Save hands the edited text to `onSave`; Cancel
 * discards it and calls `onCancel`.
 *
 * This component is presentational only: it holds no `commitEdit` call
 * and no conflict handling. `page.tsx` owns both — it turns the saved
 * prose into the item's full file content (frontmatter preserved) before
 * calling `commitEdit`, and decides what happens next.
 */
export function Editor(props: {
  item: Item;
  onSave: (newBody: string) => Promise<void>;
  onCancel: () => void;
  saving?: boolean;
}): JSX.Element {
  const [text, setText] = useState(props.item.body);
  const saving = props.saving ?? false;

  return (
    <div className="mx-auto max-w-3xl space-y-3">
      <h2 className="text-lg font-semibold tracking-tight text-slate-900 dark:text-slate-50">
        Edit {props.item.address}
      </h2>
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        disabled={saving}
        rows={20}
        className="ld-focus w-full rounded-lg border border-slate-300 bg-white p-3 font-mono text-sm text-slate-800 disabled:bg-slate-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:disabled:bg-slate-950"
      />
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => void props.onSave(text)}
          disabled={saving}
          className="ld-focus rounded-md bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-emerald-500"
        >
          {saving ? "Saving" : "Save"}
        </button>
        <button
          type="button"
          onClick={props.onCancel}
          disabled={saving}
          className="ld-focus rounded-md bg-slate-200 px-3 py-1.5 text-sm font-medium text-slate-800 transition-colors hover:bg-slate-300 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}
