// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Checks a Liquid template's variable references against the backend's
// variable schema, so a typo ({{ alert.severty }}) is caught while
// editing rather than discovered in a real page.
//
// The hard part is NOT flagging valid things. Liquid binds names the
// schema knows nothing about:
//   - loop variables:  {% for kv in service.metadata %}{{ kv.key }}
//   - assignments:     {% assign sev = alert.severity %}{{ sev }}
//   - filters/literals: {{ rule.name | default: "unnamed" }}
// A checker that cried wolf on those would train people to ignore it,
// which is worse than no checker. So we collect the locally-bound names
// first and only report what remains.

export interface UnknownVariable {
  /** The full dotted path as written, e.g. "alert.severty". */
  path: string;
  /** 1-indexed line in the template, for the message. */
  line: number;
  /** A known path with the same root, when one looks close. */
  suggestion?: string;
}

// Names Liquid itself provides inside constructs we don't model.
const BUILTIN_NAMES = new Set(["forloop", "tablerowloop", "blank", "empty", "nil", "null", "true", "false"]);

/** Collects names bound by {% for X in … %} and {% assign X = … %}. */
function locallyBound(template: string): Set<string> {
  const bound = new Set<string>();
  for (const m of template.matchAll(/\{%-?\s*for\s+([A-Za-z_][\w]*)\s+in\s/g)) bound.add(m[1]);
  for (const m of template.matchAll(/\{%-?\s*assign\s+([A-Za-z_][\w]*)\s*=/g)) bound.add(m[1]);
  // {% capture X %}…{% endcapture %} binds X too.
  for (const m of template.matchAll(/\{%-?\s*capture\s+([A-Za-z_][\w]*)\s*-?%\}/g)) bound.add(m[1]);
  return bound;
}

/**
 * Returns the variable references in `template` that the schema doesn't
 * know about. `knownPaths` is the schema's path list — metadata entries
 * arrive as "service.metadata.<key>" and match any key.
 */
export function unknownVariables(template: string, knownPaths: string[]): UnknownVariable[] {
  if (!template.trim() || knownPaths.length === 0) return [];

  const known = new Set(knownPaths);
  // Roots the schema knows: a reference whose root is one of these but
  // whose full path isn't known is the confident typo case.
  const knownRoots = new Set(knownPaths.map((p) => p.split(".")[0]));
  // Wildcard prefixes from "<key>" paths: service.metadata.<key> means
  // service.metadata.ANYTHING is fine.
  const wildcardPrefixes = knownPaths
    .filter((p) => p.endsWith(".<key>"))
    .map((p) => p.slice(0, -"<key>".length)); // keeps the trailing dot
  // Paths that are objects along the way (service, service.metadata, …)
  // are legitimate references on their own — e.g. {% for kv in service.metadata %}.
  const containerPaths = new Set<string>();
  for (const p of knownPaths) {
    const segs = p.split(".");
    for (let i = 1; i < segs.length; i++) containerPaths.add(segs.slice(0, i).join("."));
  }

  const bound = locallyBound(template);
  const seen = new Set<string>();
  const out: UnknownVariable[] = [];
  const lines = template.split("\n");

  lines.forEach((text, idx) => {
    // Every {{ … }} output and {% if/unless/elsif … %} condition can
    // carry variable references; scan both.
    const exprs: string[] = [];
    for (const m of text.matchAll(/\{\{-?(.*?)-?\}\}/g)) exprs.push(m[1]);
    for (const m of text.matchAll(/\{%-?\s*(?:if|unless|elsif)\s+(.*?)-?%\}/g)) exprs.push(m[1]);

    for (const expr of exprs) {
      // Drop filter arguments and string literals — "{{ x | default: 'a.b' }}"
      // must not report a.b as a variable.
      const head = expr.split("|")[0];
      const cleaned = head.replace(/'[^']*'/g, "").replace(/"[^"]*"/g, "");
      for (const m of cleaned.matchAll(/[A-Za-z_][\w]*(?:\.[A-Za-z_][\w]*)*/g)) {
        const path = m[0];
        const root = path.split(".")[0];
        if (BUILTIN_NAMES.has(root) || bound.has(root)) continue;
        // Liquid operators/keywords that the word regex catches.
        if (["and", "or", "not", "contains", "in"].includes(path)) continue;
        if (known.has(path) || containerPaths.has(path)) continue;
        if (wildcardPrefixes.some((prefix) => path.startsWith(prefix))) continue;
        // An unknown root that isn't locally bound is a typo too
        // (e.g. "alrt.severity"), as is a known root with a bad tail.
        const key = `${path}@${idx}`;
        if (seen.has(key)) continue;
        seen.add(key);
        out.push({
          path,
          line: idx + 1,
          suggestion: knownRoots.has(root) ? closestKnown(path, knownPaths) : undefined,
        });
      }
    }
  });
  return out;
}

/** Cheapest useful suggestion: the known path sharing the most leading characters. */
function closestKnown(path: string, knownPaths: string[]): string | undefined {
  const root = path.split(".")[0];
  const candidates = knownPaths.filter((p) => p.split(".")[0] === root);
  if (candidates.length === 0) return undefined;
  let best = candidates[0];
  let bestScore = -1;
  for (const c of candidates) {
    let i = 0;
    while (i < c.length && i < path.length && c[i] === path[i]) i++;
    if (i > bestScore) {
      bestScore = i;
      best = c;
    }
  }
  return best;
}
