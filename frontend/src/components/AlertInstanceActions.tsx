// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// AlertInstanceActions — Acknowledge / Resolve controls for a firing alert
// instance (a failing health check). Acknowledge silences notifications
// while the alert stays open and is being worked on; Resolve closes it.
// Both hit the existing alert-instance endpoints, the same ones the Alerts
// page "Firing now" table uses — so a failing check can be triaged inline
// from wherever it's listed (the Errors page, an integration's Errors tab).

import { useState } from "react";
import { api } from "../api/client";

export default function AlertInstanceActions({
  instanceId,
  acknowledged,
  onChanged,
  onError,
}: {
  instanceId: string;
  // True once the instance has been acknowledged (handled_at set). The
  // Acknowledge button drops out; only Resolve remains.
  acknowledged: boolean;
  onChanged: () => void;
  onError?: (msg: string) => void;
}) {
  const [busy, setBusy] = useState(false);

  const act = async (action: "acknowledge" | "resolve") => {
    // Confirm both — each changes the alert's state and suppresses further
    // notifications, so a stray click shouldn't trigger it. Mirrors the
    // Alerts page wording exactly.
    const prompt =
      action === "acknowledge"
        ? "Acknowledge this alert? It stays open but stops sending notifications while it's being worked on."
        : "Resolve this alert? It closes the alert and won't re-notify while the underlying condition persists.";
    if (!window.confirm(prompt)) return;
    setBusy(true);
    try {
      if (action === "acknowledge") await api.acknowledgeAlertInstance(instanceId);
      else await api.resolveAlertInstance(instanceId);
      onChanged();
    } catch (e) {
      onError?.(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <span style={{ display: "inline-flex", gap: 6, whiteSpace: "nowrap" }}>
      {!acknowledged && (
        <button
          type="button"
          className="btn btn--sm"
          disabled={busy}
          title="Stop notifications while you work on it — the alert stays open"
          onClick={() => act("acknowledge")}
        >
          Acknowledge
        </button>
      )}
      <button
        type="button"
        className="btn btn--sm"
        disabled={busy}
        title="Close this alert. It won't re-notify while the condition persists."
        onClick={() => act("resolve")}
      >
        Resolve
      </button>
    </span>
  );
}
