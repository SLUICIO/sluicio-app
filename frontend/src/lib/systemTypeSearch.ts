// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Finding a system type by name in the catalog.
//
// The catalog is a flat list that only grows: built-ins ship with the
// product, orgs add their own, and the community repo is meant to be
// browsed and imported from. Past a screenful, "is NATS in here already"
// stops being answerable by looking.
//
// Two decisions worth stating, because both are the kind that get
// "simplified" away later:
//
// KEY MATCHES TOO, not just the label. The key is on screen next to
// every label, it is what an import file and an API call name the type
// by, and someone who just read `otel_collector` in a YAML file will
// type that. Refusing to match it would be a search that cannot find
// what the page is currently showing.
//
// PUNCTUATION AND SPACING ARE IGNORED on both sides. This catalog is
// full of names that are one word to a human and two to a string
// comparison: "RabbitMQ" has key `rabbitmq`, "OTel Collector" has key
// `otel_collector`. Someone typing "otel collector" should find the type
// whose key is `otel_collector`, and someone typing "rabbit mq" should
// find RabbitMQ. Without this the search works only for people who
// already know the exact spelling, which is precisely the people who do
// not need to search.

import type { SystemType } from "../api/types";

/**
 * Lowercase and strip everything that is not a letter or digit.
 *
 * Deliberately Unicode-aware: a custom type may legitimately be called
 * "Journalföring" or "Kötjänst", and folding those to nothing would make
 * every Swedish-named type unsearchable.
 */
export function normalizeForSearch(s: string): string {
  return s.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, "");
}

/** Whether one type matches a query. An empty query matches everything. */
export function systemTypeMatches(t: SystemType, query: string): boolean {
  const q = normalizeForSearch(query);
  if (!q) return true;
  return (
    normalizeForSearch(t.label ?? "").includes(q) ||
    normalizeForSearch(t.key ?? "").includes(q)
  );
}

/**
 * The catalog filtered to a query, in the order it was given.
 *
 * Order is preserved rather than ranked by match quality: the list is
 * the org's catalog, people learn where things sit in it, and reordering
 * on every keystroke would make the page feel like it is shuffling.
 */
export function filterSystemTypes(types: SystemType[], query: string): SystemType[] {
  const q = normalizeForSearch(query);
  if (!q) return types;
  return types.filter((t) => systemTypeMatches(t, query));
}
