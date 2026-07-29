// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Deep-link highlight for alert instances. Notification emails/webhooks
// link back into the app with ?instance=<alert-instance-id> (see
// alerting/delivery.go alertLinkPath); the page that renders that
// instance marks its row with `.instance-highlight` (a pulse, styles.css)
// and scrolls it into view — so the recipient lands on exactly the alert
// that paged them, not just the right page.

import { useCallback, useEffect, useRef } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";

export function useInstanceHighlight() {
  const [params] = useSearchParams();
  const target = params.get("instance");

  // Record that somebody followed the notification back into the app.
  //
  // This is the Alert Fatigue Advisor's engagement signal: without it a
  // rule that everyone reads and judges fine is indistinguishable from
  // one nobody opens, and the advisor would eventually recommend
  // silencing the wrong ones. Fire-and-forget by design — a measurement
  // must never delay or break the page the recipient came to see.
  const pinged = useRef<string | null>(null);
  useEffect(() => {
    if (!target || pinged.current === target) return;
    pinged.current = target;
    void api.markAlertInstanceOpened(target);
  }, [target]);
  // Scroll once per page view, even if the row re-renders on refresh.
  const scrolled = useRef(false);
  const scrollRef = useCallback((el: HTMLDivElement | null) => {
    if (el && !scrolled.current) {
      scrolled.current = true;
      el.scrollIntoView({ block: "center" });
    }
  }, []);

  // props(id, baseClassName) → spread onto the row element. Usable inside
  // list .map()s (it's a plain function, not a hook).
  const props = (
    id: string | undefined,
    base = "",
  ): { className: string; ref?: (el: HTMLDivElement | null) => void } =>
    id && id === target
      ? { className: `${base} instance-highlight`.trim(), ref: scrollRef }
      : { className: base };

  return { target, props };
}
