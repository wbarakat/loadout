"use client";

import type { JSX } from "react";
import type { SecretMeta } from "../lib/vault/model.js";

/** A field label shown for a known frontmatter key. Any other key present
 * in `frontmatter` still renders, using the raw key as its label — see
 * `orderedFields`. */
const KNOWN_LABELS: Readonly<Record<string, string>> = {
  service: "Service",
  hook: "Hook",
  rotate_after: "Rotate after",
  allowed_hosts: "Allowed hosts",
  by: "Added by",
  at: "Added at",
};

/** The known fields first (in a fixed, readable order), then every other
 * frontmatter key present, in the order Object.entries yields them. */
function orderedFields(
  frontmatter: Record<string, string>,
): Array<{ key: string; label: string; value: string }> {
  const knownOrder = ["service", "hook", "rotate_after", "allowed_hosts", "by", "at"];
  const seen = new Set<string>();
  const rows: Array<{ key: string; label: string; value: string }> = [];

  for (const key of knownOrder) {
    const value = frontmatter[key];
    if (value === undefined) continue;
    rows.push({ key, label: KNOWN_LABELS[key] ?? key, value });
    seen.add(key);
  }
  for (const [key, value] of Object.entries(frontmatter)) {
    if (seen.has(key)) continue;
    rows.push({ key, label: KNOWN_LABELS[key] ?? key, value });
  }
  return rows;
}

/**
 * Reads a secret's metadata only. `SecretMeta` has no value field, so
 * there is nothing here to reveal, fetch, or render as a value — this
 * component only ever reads `secret.name` and `secret.frontmatter`.
 *
 * Renders a metadata table (every frontmatter field present) and a clear
 * notice that the value is not readable here.
 */
export function SecretDetail(props: { secret: SecretMeta }): JSX.Element {
  const fields = orderedFields(props.secret.frontmatter);

  return (
    <div className="mx-auto max-w-2xl space-y-5">
      <h1 className="text-lg font-semibold tracking-tight text-slate-900 dark:text-slate-50">
        {props.secret.name}
      </h1>

      {fields.length > 0 ? (
        <table className="w-full border-collapse text-sm">
          <tbody>
            {fields.map((field) => (
              <tr key={field.key} className="border-b border-slate-100 dark:border-slate-800">
                <th
                  scope="row"
                  className="w-40 py-2 pr-4 text-left align-top text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400"
                >
                  {field.label}
                </th>
                <td className="py-2 text-slate-800 dark:text-slate-200">{field.value}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}

      <p className="rounded-lg border border-slate-200 bg-slate-50 p-4 text-sm leading-relaxed text-slate-600 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400">
        The value is stored encrypted and cannot be read here. Use a full
        device, or{" "}
        <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs text-slate-700 dark:bg-slate-800 dark:text-slate-300">
          loadout secret show {props.secret.name}
        </code>{" "}
        on the CLI.
      </p>
    </div>
  );
}
