// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SharePermalinkButton — copies a deep-link to the current Messages
// view to the clipboard. The link encodes the user-set filters
// (?q / ?s) and the time range (?range) so the recipient lands on the
// exact same view. When the surface hydrates a saved view from the URL
// (the global Messages page), pass `viewId` to emit a compact
// ?view=<id> link instead of the expanded filter set.
//
// Pure client-side: no backend, no URL-shortening service. The page's
// own URL-sync already keeps ?q/?s/?range current, so this is really
// just "snapshot the address and copy it" with a friendly confirmation.

import { useEffect, useState } from "react";
import type { Filter } from "./FilterEditor";
import { writeFiltersToParams } from "../../lib/messageFilterUrl";

interface Props {
  // The route the link should open, e.g. "/search" or
  // "/integrations/<id>/messages". No query string — this component
  // owns the query params it understands.
  path: string;
  // User-set filters to encode (locked/scope rows are skipped by the
  // serializer; they're implied by the route).
  filters: Filter[];
  // The active time window (?range), e.g. "6h".
  range: string;
  // When set, emit ?view=<id> instead of the expanded filters. Only
  // pass this on surfaces that hydrate ?view on load.
  viewId?: string;
  // Extra query params to merge into the link (e.g. {tab: "traces"} so
  // the recipient lands on the right tab of an entity detail page).
  extraParams?: Record<string, string>;
  className?: string;
}

// copyText copies via the async clipboard API, falling back to a
// hidden textarea + execCommand for non-secure contexts (http://, some
// embedded webviews) where navigator.clipboard is unavailable.
async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall through to the legacy path */
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

export default function SharePermalinkButton({
  path,
  filters,
  range,
  viewId,
  extraParams,
  className,
}: Props) {
  const [state, setState] = useState<"idle" | "copied" | "error">("idle");

  // Reset the confirmation back to the default label after a moment.
  useEffect(() => {
    if (state === "idle") return;
    const t = window.setTimeout(() => setState("idle"), 1600);
    return () => window.clearTimeout(t);
  }, [state]);

  const onClick = async () => {
    const params = new URLSearchParams();
    if (viewId) {
      params.set("view", viewId);
    } else {
      writeFiltersToParams(filters, params);
    }
    if (range) params.set("range", range);
    if (extraParams) {
      for (const [k, v] of Object.entries(extraParams)) params.set(k, v);
    }
    const qs = params.toString();
    const url = `${window.location.origin}${path}${qs ? `?${qs}` : ""}`;
    const ok = await copyText(url);
    setState(ok ? "copied" : "error");
  };

  return (
    <button
      type="button"
      className={className ?? "btn"}
      onClick={onClick}
      title="Copy a shareable link to this view (filters + time range)"
    >
      {state === "copied"
        ? "✓ link copied"
        : state === "error"
          ? "copy failed"
          : "↗ share"}
    </button>
  );
}
