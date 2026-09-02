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
    <div className="space-y-3">
      <h2 className="text-lg font-semibold text-slate-900">
        Edit {props.item.address}
      </h2>
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        disabled={saving}
        rows={20}
        className="w-full rounded border border-slate-300 p-3 font-mono text-sm text-slate-800 disabled:bg-slate-50"
      />
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => void props.onSave(text)}
          disabled={saving}
          className="rounded bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
        >
          {saving ? "Saving" : "Save"}
        </button>
        <button
          type="button"
          onClick={props.onCancel}
          disabled={saving}
          className="rounded bg-slate-200 px-3 py-1.5 text-sm font-medium text-slate-800 hover:bg-slate-300 disabled:opacity-50"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}
