// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Payload links in the message lists: the organization's link attributes
// (read once per page load and shared), and the links of the messages on
// the page, fetched after the list has rendered, like the completion
// column, so showing them never makes the list slower. Every message list
// (an integration's, a service's, the Messages page) reads them through
// here, so the columns appear by the same rule everywhere: when at least
// one message on the page carries the attribute.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api/client";
import type { PayloadLinkAttribute, PayloadLinks, TraceSearchResult } from "../api/types";

let attributesPromise: Promise<PayloadLinkAttribute[]> | null = null;

/** The organization's link attributes, fetched once and shared. */
export function loadPayloadLinkAttributes(): Promise<PayloadLinkAttribute[]> {
  if (!attributesPromise) {
    attributesPromise = api
      .getPayloadLinkAttributes()
      .then((r) => r.attributes ?? [])
      .catch(() => {
        // Retried on the next page that asks; no columns meanwhile.
        attributesPromise = null;
        return [];
      });
  }
  return attributesPromise;
}

/** Forget the shared list after a save, so the lists pick up the change. */
export function resetPayloadLinkAttributes(next?: PayloadLinkAttribute[]) {
  attributesPromise = next ? Promise.resolve(next) : null;
}

export function usePayloadLinkAttributes(): PayloadLinkAttribute[] {
  const [attrs, setAttrs] = useState<PayloadLinkAttribute[]>([]);
  useEffect(() => {
    let live = true;
    loadPayloadLinkAttributes().then((a) => live && setAttrs(a));
    return () => {
      live = false;
    };
  }, []);
  return attrs;
}

/** The column header of an attribute. */
export function payloadLinkLabel(a: PayloadLinkAttribute): string {
  return a.label?.trim() || a.attribute;
}

/** The attributes any of the given messages carries, in configured order. */
export function presentAttributes(
  attrs: PayloadLinkAttribute[],
  links: Record<string, Record<string, PayloadLinks>>,
): PayloadLinkAttribute[] {
  const seen = new Set<string>();
  for (const byAttr of Object.values(links)) {
    for (const [k, v] of Object.entries(byAttr)) if (v.links.length > 0) seen.add(k);
  }
  return attrs.filter((a) => seen.has(a.attribute));
}

/** The earliest-first start of a page: the read's lower time bound. */
function earliest(rows: TraceSearchResult[]): string {
  return rows.reduce((min, r) => (r.trace_start < min ? r.trace_start : min), rows[0].trace_start);
}

/**
 * The payload links of the messages on the page, keyed by trace id, and
 * the link attributes to show as columns. A message being fetched is
 * absent from `links`.
 *
 * Call `reset` where a new search replaces the results, so it reads its
 * links afresh rather than keeping those of the previous result.
 */
export function usePayloadLinkFill(results: TraceSearchResult[], integrationId?: string) {
  const attrs = usePayloadLinkAttributes();
  const [links, setLinks] = useState<Record<string, Record<string, PayloadLinks>>>({});
  const asked = useRef(new Set<string>());
  const reset = useCallback(() => {
    asked.current = new Set();
    setLinks({});
  }, []);

  useEffect(() => {
    if (attrs.length === 0) return;
    const todo = results.filter((r) => r.trace_id && !asked.current.has(r.trace_id));
    if (todo.length === 0) return;
    todo.forEach((r) => asked.current.add(r.trace_id));
    const ids = todo.map((r) => r.trace_id);
    const settle = (byTrace: Record<string, Record<string, PayloadLinks>>) =>
      setLinks((prev) => {
        const next = { ...prev };
        // A message with no links gets an empty entry: answered, not loading.
        for (const id of ids) next[id] = byTrace[id] ?? {};
        return next;
      });
    api
      .payloadLinks(ids, earliest(todo), integrationId)
      .then((res) => settle(res.links ?? {}))
      .catch(() => settle({}));
  }, [results, attrs, integrationId]);

  const columns = useMemo(() => presentAttributes(attrs, links), [attrs, links]);
  return { attributes: attrs, columns, links, reset };
}
