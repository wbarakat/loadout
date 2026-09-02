"use client";

import type { JSX } from "react";
import { approveCommand } from "../../lib/dash/session.js";
import { CopyButton } from "../CopyButton.js";

/**
 * Shown after this browser registers with `loadoutd` but is not yet in the
 * vault's `devices.toml`. Tells the user which command to run, on an
 * already-approved device, to finish enrollment.
 */
export function NotApproved(props: {
  recipient: string;
  deviceName: string;
  onRetry: () => void;
}): JSX.Element {
  const command = approveCommand(props.deviceName);

  return (
    <div className="mx-auto max-w-md space-y-4 rounded-lg border border-amber-300 bg-amber-50 p-6">
      <h2 className="text-lg font-semibold text-amber-900">Waiting for approval</h2>
      <p className="text-sm text-amber-900">
        This device is registered but not yet approved. Run the command
        below on an approved device, such as your Mac. Then click Retry.
      </p>

      <div>
        <div className="text-xs font-medium text-amber-900">Device recipient</div>
        <div className="mt-1 flex items-center gap-2">
          <code className="min-w-0 flex-1 truncate rounded bg-white px-2 py-1 text-xs text-slate-800">
            {props.recipient}
          </code>
          <CopyButton value={props.recipient} label="Copy recipient" />
        </div>
      </div>

      <div>
        <div className="text-xs font-medium text-amber-900">Approve command</div>
        <div className="mt-1 flex items-center gap-2">
          <code className="min-w-0 flex-1 truncate rounded bg-white px-2 py-1 text-xs text-slate-800">
            {command}
          </code>
          <CopyButton value={command} label="Copy approve command" />
        </div>
      </div>

      <button
        type="button"
        onClick={props.onRetry}
        className="w-full rounded bg-amber-600 px-3 py-2 text-sm font-medium text-white hover:bg-amber-700"
      >
        Retry connection
      </button>
    </div>
  );
}
