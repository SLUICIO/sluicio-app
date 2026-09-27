// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The reasoning behind the membership editor, kept apart from its markup
// so it can be tested as sentences rather than as clicks.
//
// A member is one Rule (see MatcherRules): a service, or a pattern over
// service names, optionally narrowed by attribute conditions. Nothing
// here changes what gets stored. The rule <-> matcher translation stays
// in MatcherRules, and every function below reads rules as that
// translation will store them.

import type { MatcherOperator, NeighborsResponse, RuleMatch } from "../../api/types";
import {
  VALUELESS_MATCHER_OPS,
  blankRule,
  matchersToRules,
  rulesToMatchers,
  type AttrCond,
  type Rule,
} from "../MatcherRules";

/** Identity of a member: the same service under the same operator is
 *  the same member, whatever its conditions say. */
export const ruleKey = (r: Rule): string => `${r.serviceOp}\u0000${r.service.trim()}`;

/** Whether one member's service part takes in this service name. */
export function ruleCovers(rule: Rule, name: string): boolean {
  const v = rule.service.trim();
  if (!v) return false;
  switch (rule.serviceOp) {
    case "equals":
      return name === v;
    case "prefix":
      return name.startsWith(v);
    case "suffix":
      return name.endsWith(v);
    case "contains":
      return name.includes(v);
    case "regex":
      try {
        return new RegExp(v).test(name);
      } catch {
        return false;
      }
    default:
      return false;
  }
}

export const rulesCover = (rules: Rule[], name: string): boolean => rules.some((r) => ruleCovers(r, name));

const escapeRegex = (s: string) => s.replace(/[.+?^${}()|[\]\\]/g, "\\$&");

/**
 * What typing into the add box adds.
 *
 * A plain name is a service. A star makes it a pattern, in the shape
 * people already type into a shell: `order-*`, `*-worker`, `*pay*`.
 * A star anywhere else, or `/.../`, is a regular expression. A lone
 * star is refused: it is a rule that takes in every service in the
 * cell, which is somebody still typing rather than a member.
 */
export function parseAddQuery(raw: string): Rule | null {
  const q = raw.trim();
  if (!q) return null;
  const slashed = /^\/(.+)\/$/.exec(q);
  if (slashed) return blankRule({ serviceOp: "regex", service: slashed[1] });
  if (!q.includes("*")) return blankRule({ serviceOp: "equals", service: q });
  const body = q.replace(/^\*+|\*+$/g, "");
  if (!body) return null;
  const lead = q.startsWith("*");
  const trail = q.endsWith("*");
  if (!body.includes("*")) {
    if (lead && trail) return blankRule({ serviceOp: "contains", service: body });
    if (trail) return blankRule({ serviceOp: "prefix", service: body });
    if (lead) return blankRule({ serviceOp: "suffix", service: body });
  }
  const re = "^" + q.split("*").map(escapeRegex).join(".*") + "$";
  return blankRule({ serviceOp: "regex", service: re });
}

const PATTERN_WORDS: Partial<Record<MatcherOperator, string>> = {
  prefix: "services starting with",
  suffix: "services ending with",
  contains: "services containing",
  regex: "services matching",
};

/** "services starting with" - the words in front of a pattern's value. */
export const patternWords = (op: MatcherOperator): string => PATTERN_WORDS[op] ?? `services where the name ${op}`;

/** The member as one phrase, for sentences like "added ...". */
export function memberPhrase(rule: Rule): string {
  const value = rule.service.trim();
  if (!value) return "a condition with no service";
  if (rule.serviceOp === "equals") return value;
  return `${patternWords(rule.serviceOp)} ${value}`;
}

const COND_WORDS: Record<MatcherOperator, string> = {
  equals: "is",
  prefix: "starts with",
  suffix: "ends with",
  contains: "contains",
  regex: "matches",
  not_equals: "is not",
  not_contains: "doesn't contain",
  exists: "is set",
  not_exists: "is absent",
};

/** Conditions complete enough to be stored, which is what a summary
 *  should describe. A half-typed condition is not part of the rule. */
export function completeConds(rule: Rule): AttrCond[] {
  return rule.attrs
    .map((a) => ({ ...a, attribute: a.attribute.trim(), value: a.value.trim() }))
    .filter((a) => a.attribute && (a.value || VALUELESS_MATCHER_OPS.includes(a.operator)));
}

/**
 * What the member takes, in words. This line is what somebody reads to
 * check they built what they meant, so it says the conditions rather
 * than counting them.
 */
export function conditionsPhrase(rule: Rule): string {
  const conds = completeConds(rule);
  if (conds.length === 0) return "All traffic";
  const joiner = rule.combine === "all" ? " and " : " or ";
  const parts = conds.map((c) =>
    VALUELESS_MATCHER_OPS.includes(c.operator)
      ? `${c.attribute} ${COND_WORDS[c.operator]}`
      : `${c.attribute} ${COND_WORDS[c.operator]} ${c.value}`,
  );
  return `Only where ${parts.join(joiner)}${rule.descendants ? ", with child spans" : ""}`;
}

/**
 * Whether a member joins its conditions with "or" where the integration
 * cannot store that yet.
 *
 * On the wire a member with "or" is several match groups, one per
 * alternative, and nothing records that they came from one member. With
 * members combined as "one trace through every member" the backend
 * requires EVERY group, so the alternatives would all be required: "A
 * where x = 1 or x = 2" would mean a span of A with 1 and another with 2.
 * Until the groups carry their member, the editor keeps "or" out of that
 * mode rather than store a rule that means something else.
 */
export const orConflict = (rule: Rule, mode: RuleMatch): boolean =>
  mode === "all" && rule.combine !== "all" && completeConds(rule).length >= 2;

/** Why "or" is not offered, in the words the editor shows. */
export const OR_UNAVAILABLE =
  'With "one trace through every member", a member\'s conditions can only be joined with "and" for now. "Or" would make every alternative required.';

const contentSig = (r: Rule) =>
  JSON.stringify([
    r.combine,
    !!r.descendants,
    completeConds(r)
      .map((c) => [c.attribute, c.operator, c.value])
      .sort(),
  ]);

/**
 * The unsaved changes, as the save bar lists them.
 *
 * The draft is read as it WILL be stored - through the same translation
 * the save path runs - so a row the save would drop shows up here as
 * removed, instead of vanishing quietly after the click.
 */
export function describeChanges(
  stored: Rule[],
  draft: Rule[],
  storedMode: RuleMatch,
  draftMode: RuleMatch,
): string[] {
  const after = matchersToRules(rulesToMatchers(draft));
  const before = new Map(stored.map((r) => [ruleKey(r), r]));
  const now = new Map(after.map((r) => [ruleKey(r), r]));
  const out: string[] = [];
  for (const [k, r] of now) {
    const prev = before.get(k);
    if (!prev) {
      out.push(`added ${memberPhrase(r)}`);
      continue;
    }
    if (contentSig(prev) === contentSig(r)) continue;
    const had = completeConds(prev).length > 0;
    const has = completeConds(r).length > 0;
    if (!had && has) out.push(`narrowed ${memberPhrase(r)}`);
    else if (had && !has) out.push(`widened ${memberPhrase(r)} to all traffic`);
    else out.push(`changed the conditions on ${memberPhrase(r)}`);
  }
  // A stored condition with no service is one the save cannot keep. It
  // is only a change once there is a save to lose it in: listing it on
  // its own would show an unsaved change on an editor nobody has touched.
  const dropped: string[] = [];
  for (const [k, r] of before) {
    if (now.has(k)) continue;
    (r.service.trim() ? out : dropped).push(`removed ${memberPhrase(r)}`);
  }
  if (storedMode !== draftMode) {
    out.push(draftMode === "all" ? "now needs one trace through every member" : "back to matching any member");
  }
  return out.length > 0 ? [...out, ...dropped] : [];
}

export interface Suggestion {
  name: string;
  /** Why it is suggested, e.g. "called by order-processor". */
  reason: string;
  /** Traces that connect it to a member, calls and hand-offs together. */
  traffic: number;
}

/**
 * One list out of every member's trace-graph neighbours.
 *
 * The same service is usually a neighbour of several members - the
 * queue behind three of them - and one panel per member listed it three
 * times. Here it is listed once, with the connection that carries the
 * most traffic as its reason.
 */
export function mergeSuggestions(
  neighbourhoods: { focal: string; data: NeighborsResponse }[],
  isCovered: (name: string) => boolean,
): Suggestion[] {
  const focals = new Set(neighbourhoods.map((n) => n.focal));
  const best = new Map<string, Suggestion>();
  const consider = (name: string, reason: string, traffic: number) => {
    if (focals.has(name) || isCovered(name) || traffic <= 0) return;
    const prev = best.get(name);
    if (!prev || traffic > prev.traffic) best.set(name, { name, reason, traffic });
  };
  for (const { focal, data } of neighbourhoods) {
    for (const n of data.downstream ?? []) {
      const calls = n.trace_count ?? 0;
      const links = n.link_trace_count ?? 0;
      consider(n.service_name, calls >= links ? `called by ${focal}` : `receives hand-offs from ${focal}`, calls + links);
    }
    for (const n of data.upstream ?? []) {
      const calls = n.trace_count ?? 0;
      const links = n.link_trace_count ?? 0;
      consider(n.service_name, calls >= links ? `calls ${focal}` : `hands off to ${focal}`, calls + links);
    }
  }
  return [...best.values()].sort((a, b) => b.traffic - a.traffic || a.name.localeCompare(b.name));
}

/** The range as a sentence says it: "in the last 24h", or "in this
 *  range" for an absolute one. */
export function windowPhrase(w: string): string {
  return /^\d+[mhdw]$/.test(w) ? `in the last ${w}` : "in this range";
}

/** The range as a label says it: "24h", or "range". */
export function windowShort(w: string): string {
  return /^\d+[mhdw]$/.test(w) ? w : "range";
}
