// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// MatcherRules — the per-service rule editor shared by the create
// (IntegrationNew) and edit (MatcherConfig) surfaces.
//
// An integration is the UNION (OR) of rules. Each rule pins a service and
// carries its own optional attribute predicate, combined per-rule by OR
// ("match any") or AND ("match all"):
//
//   (service = A AND (producer = B OR consumer = C))
//   OR (service = B AND (producer = D OR consumer = D))
//   OR (service = C)
//
// On the wire this is a flat DNF over integration_matchers rows tagged with
// match_group (conditions in a group AND-ed, groups OR-ed). service.name is a
// normal condition inside a group — the backend compiles it against the
// ServiceName column, which is what makes per-service scoping work. The
// rule⇄DNF translation lives here so both surfaces stay consistent:
//   rulesToMatchers  — expand rules into match_group-tagged matcher rows
//   matchersToRules  — bucket matcher rows back into rules by their service
//   rulesPreview     — render the full boolean expression for the live preview

import SearchableSelect from "./SearchableSelect";
import type { MatcherOperator, RuleMatch } from "../api/types";

export const RULE_OPERATORS: { value: MatcherOperator; label: string; sym: string }[] = [
  { value: "equals", label: "is", sym: "=" },
  { value: "prefix", label: "starts with", sym: "starts with" },
  { value: "suffix", label: "ends with", sym: "ends with" },
  { value: "contains", label: "contains", sym: "contains" },
  { value: "regex", label: "matches regex", sym: "matches" },
];

// Attribute conditions get the negations and the existence checks too.
// The service selector above does not: "the service name is absent" is
// not a question, and the backend rejects it.
//
// "is not" and "doesn't contain" also match spans that do not carry the
// attribute at all - an attribute a span lacks reads as empty, so it is
// not equal to anything. When you mean specifically the ones that lack
// it, that is "is absent"; when you mean specifically the ones that have
// it and differ, pair "exists" with "is not" in the same rule.
export const ATTR_OPERATORS: { value: MatcherOperator; label: string; sym: string }[] = [
  ...RULE_OPERATORS,
  { value: "not_equals", label: "is not", sym: "≠" },
  { value: "not_contains", label: "doesn't contain", sym: "doesn't contain" },
  { value: "exists", label: "exists", sym: "exists" },
  { value: "not_exists", label: "is absent", sym: "is absent" },
];

// The two that ask about the key rather than the value, so the value
// input is hidden and no value is sent.
export const VALUELESS_MATCHER_OPS: MatcherOperator[] = ["exists", "not_exists"];

const SERVICE_NAME_ATTR = "service.name";

export interface AttrCond {
  attribute: string;
  operator: MatcherOperator;
  value: string;
  // UI-only: the field is being entered as a free-text custom key rather than
  // chosen from the persisted attribute catalog. Ignored by the DNF
  // translation (which only reads attribute/operator/value).
  custom?: boolean;
}

export interface Rule {
  // The service this rule scopes to. serviceOp is usually "equals" (one
  // concrete service) but may be prefix/regex/… to cover a family.
  serviceOp: MatcherOperator;
  service: string;
  // How the attribute conditions combine within this rule.
  combine: "any" | "all";
  attrs: AttrCond[];
  // Take the spans this rule matches AND everything below them in the
  // trace, whether or not the children satisfy the conditions. A rule
  // property rather than a condition's: on the wire it applies to a whole
  // match group, and an "all" rule is one group, so a per-condition
  // switch would claim a precision the query does not have.
  descendants?: boolean;
}

export type MatcherInput = {
  attribute: string;
  operator: MatcherOperator;
  value: string;
  match_group: number;
  include_descendants?: boolean;
};

export const blankAttr = (): AttrCond => ({ attribute: "", operator: "equals", value: "" });

export const blankRule = (seed?: Partial<Rule>): Rule => ({
  serviceOp: "equals",
  service: "",
  combine: "any",
  attrs: [],
  ...seed,
});

const sym = (o: MatcherOperator) => ATTR_OPERATORS.find((x) => x.value === o)?.sym ?? o;

const CUSTOM_FIELD = "__custom__";

// fieldHelp renders a plain-language description of a condition that updates
// as the user picks a field and operator — the "better help based on what's
// selected" affordance.
function fieldHelp(a: AttrCond): string {
  const field = a.attribute.trim();
  if (!field) return "Pick an attribute to filter this service's traffic on.";
  // The valueless ops need their own sentence: "where rpc.service is
  // absent the value" is not English, and this line is what people read
  // back to check they built what they meant.
  if (a.operator === "exists") {
    return `This service's traffic that carries ${field} at all, whatever its value.`;
  }
  if (a.operator === "not_exists") {
    return `This service's traffic that does not carry ${field} at all.`;
  }
  const phrase: Record<MatcherOperator, string> = {
    equals: "is exactly",
    prefix: "starts with",
    suffix: "ends with",
    contains: "contains",
    regex: "matches the regex",
    not_equals: "is anything other than",
    not_contains: "does not contain",
    exists: "",
    not_exists: "",
  };
  const negated = a.operator === "not_equals" || a.operator === "not_contains";
  return (
    `This service's traffic where ${field} ${phrase[a.operator]} the value.` +
    // The part that surprises people, said before it surprises them.
    (negated ? ` Traffic with no ${field} at all matches too — use "is absent" for only those.` : "")
  );
}

// validAttrs drops half-typed conditions so previews and submissions only
// reflect complete (attribute + value) rows.
const validAttrs = (r: Rule): AttrCond[] =>
  r.attrs
    .map((a) => ({ attribute: a.attribute.trim(), operator: a.operator, value: a.value.trim() }))
    // exists / not_exists are complete without a value. Requiring one
    // here would drop them from the preview and from the submission -
    // the row would sit in the editor looking configured and do nothing.
    .filter((a) => a.attribute && (a.value || VALUELESS_MATCHER_OPS.includes(a.operator)));

// rulesToMatchers expands the rules into a flat DNF of matcher rows. A rule
// with no attributes → one group [service]; an AND rule → one group
// [service, …attrs]; an OR rule → one group per attribute, each [service, attr]
// (distributing service ∧ (a ∨ b) into (service ∧ a) ∨ (service ∧ b)).
export function rulesToMatchers(rules: Rule[]): MatcherInput[] {
  const out: MatcherInput[] = [];
  let group = 0;
  for (const r of rules) {
    const service = r.service.trim();
    if (!service) continue; // incomplete rule — skip
    // Every row of every group the rule expands into carries the flag,
    // so the rule reads back the same whichever row is looked at.
    const flag = r.descendants ? { include_descendants: true } : {};
    const svc = { attribute: SERVICE_NAME_ATTR, operator: r.serviceOp, value: service, ...flag };
    const attrs = validAttrs(r).map((a) => ({ ...a, ...flag }));
    if (attrs.length === 0) {
      out.push({ ...svc, match_group: group });
      group++;
    } else if (r.combine === "all") {
      out.push({ ...svc, match_group: group });
      for (const a of attrs) out.push({ ...a, match_group: group });
      group++;
    } else {
      for (const a of attrs) {
        out.push({ ...svc, match_group: group });
        out.push({ ...a, match_group: group });
        group++;
      }
    }
  }
  return out;
}

// matchersToRules buckets matcher rows (grouped by match_group) back into
// rules by their service.name condition. Groups sharing a service combine into
// one rule: several single-attr groups → an OR rule, one multi-attr group → an
// AND rule, a service-only group → a bare rule. A group with no service.name
// (attribute-only) becomes a rule with an empty service so it stays visible
// and editable rather than being silently dropped.
export function matchersToRules(
  matchers: {
    attribute: string;
    operator: MatcherOperator;
    value: string;
    match_group: number;
    include_descendants?: boolean;
  }[],
): Rule[] {
  const byGroup = new Map<number, typeof matchers>();
  for (const m of matchers) {
    const arr = byGroup.get(m.match_group) ?? [];
    arr.push(m);
    byGroup.set(m.match_group, arr);
  }
  const order: string[] = [];
  const ruleMap = new Map<string, Rule>();
  const noService: Rule[] = [];
  for (const g of [...byGroup.keys()].sort((a, b) => a - b)) {
    const ms = byGroup.get(g)!;
    const svc = ms.find((m) => m.attribute === SERVICE_NAME_ATTR);
    const attrs: AttrCond[] = ms
      .filter((m) => m.attribute !== SERVICE_NAME_ATTR)
      .map((m) => ({ attribute: m.attribute, operator: m.operator, value: m.value }));
    const descendants = ms.some((m) => m.include_descendants);
    if (!svc) {
      noService.push({
        serviceOp: "equals",
        service: "",
        combine: attrs.length > 1 ? "all" : "any",
        attrs,
        descendants,
      });
      continue;
    }
    // \u0000 as the escape, not the byte. Written literally it made this
    // file read as binary to grep, ripgrep and GitHub search, which then
    // silently returned no matches for anything in it - including its own
    // exported constants. Same value at runtime.
    const key = `${svc.operator}\u0000${svc.value}`;
    let rule = ruleMap.get(key);
    if (!rule) {
      rule = { serviceOp: svc.operator, service: svc.value, combine: "any", attrs: [] };
      ruleMap.set(key, rule);
      order.push(key);
    }
    rule.descendants = rule.descendants || descendants;
    if (attrs.length === 1) {
      rule.attrs.push(attrs[0]); // OR across single-attr groups
    } else if (attrs.length > 1) {
      rule.attrs.push(...attrs); // AND within one group
      rule.combine = "all";
    }
  }
  return [...order.map((k) => ruleMap.get(k)!), ...noService];
}

// rulesPreview renders the full boolean expression for the live preview.
export function rulesPreview(rules: Rule[], combine: RuleMatch = "any"): string {
  const parts = rules
    .map((r) => {
      const service = r.service.trim();
      if (!service) return null;
      const svc = `service ${sym(r.serviceOp)} ${service}`;
      const attrs = validAttrs(r);
      const children = r.descendants ? " + child spans" : "";
      if (attrs.length === 0) return `(${svc})${children}`;
      const joiner = r.combine === "all" ? " AND " : " OR ";
      const inner = attrs.map((a) => `${a.attribute} ${sym(a.operator)} ${a.value}`).join(joiner);
      return `(${svc} AND (${inner}))${children}`;
    })
    .filter(Boolean);
  if (combine === "all") {
    // Said plainly, because the difference is not in the operator but in
    // what the operator is applied to: every rule has to be satisfied by
    // some step of the SAME trace.
    return parts.join("  AND  ") + (parts.length > 1 ? "   (all within one trace)" : "");
  }
  return parts.join("  OR  ");
}

export default function MatcherRules({
  rules,
  onChange,
  knownServices,
  attrKeys,
  combine = "any",
  onCombineChange,
}: {
  rules: Rule[];
  onChange: (rules: Rule[]) => void;
  knownServices: string[];
  attrKeys: string[];
  combine?: RuleMatch;
  onCombineChange?: (mode: RuleMatch) => void;
}) {
  const update = (i: number, patch: Partial<Rule>) =>
    onChange(rules.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  const removeRule = (i: number) => onChange(rules.filter((_, idx) => idx !== i));
  const addRule = () => onChange([...rules, blankRule()]);

  const updateAttr = (ri: number, ai: number, patch: Partial<AttrCond>) =>
    update(ri, { attrs: rules[ri].attrs.map((a, idx) => (idx === ai ? { ...a, ...patch } : a)) });
  const addAttr = (ri: number) => update(ri, { attrs: [...rules[ri].attrs, blankAttr()] });
  const removeAttr = (ri: number, ai: number) =>
    update(ri, { attrs: rules[ri].attrs.filter((_, idx) => idx !== ai) });

  const preview = rulesPreview(rules, combine);
  // The field picker lists every persisted attribute key plus the "custom"
  // escape hatch, shared by reference across the rows in one render.
  const fieldOptions = [...attrKeys, CUSTOM_FIELD];

  return (
    <div>
      {onCombineChange && (
        <div className="matcher-form" style={{ alignItems: "center", marginBottom: 10 }}>
          <span style={{ fontSize: 13 }}>Traffic belongs here when it matches</span>
          <select
            className="toolbar__select"
            value={combine}
            onChange={(e) => onCombineChange(e.target.value as RuleMatch)}
            aria-label="How the rules combine"
          >
            <option value="any">any rule</option>
            <option value="all">every rule</option>
          </select>
          <span className="muted" style={{ fontSize: 11 }}>
            {combine === "all"
              ? "Every rule has to be satisfied by some step of the same trace. A trace that satisfies only one of them does not belong."
              : "A step belongs if it satisfies any one rule. The rules do not have to hold together."}
          </span>
        </div>
      )}
      {preview && (
        <p className="muted form__hint" style={{ fontSize: 12, marginBottom: 10 }}>
          Matches: <span className="mono">{preview}</span>
        </p>
      )}

      {rules.map((rule, ri) => (
        <div
          key={ri}
          className="rounded-lg border bg-surface-2"
          style={{ borderColor: "var(--border)", padding: 12, marginBottom: 10 }}
        >
          <div className="matcher-form" style={{ alignItems: "center" }}>
            <span className="muted" style={{ minWidth: 52, fontSize: 13 }}>Service</span>
            <select
              className="toolbar__select"
              value={rule.serviceOp}
              onChange={(e) => update(ri, { serviceOp: e.target.value as MatcherOperator })}
              aria-label="Service match operator"
            >
              {RULE_OPERATORS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            {rule.serviceOp === "equals" ? (
              <SearchableSelect
                value={rule.service}
                onChange={(v) => update(ri, { service: v })}
                options={knownServices}
                placeholder="Filter services…"
                allLabel="Pick a service…"
              />
            ) : (
              <input
                className="search__input matcher-form__value"
                placeholder="e.g. order-"
                value={rule.service}
                onChange={(e) => update(ri, { service: e.target.value })}
                autoComplete="off"
              />
            )}
            {rules.length > 1 && (
              <button type="button" className="btn btn--link" onClick={() => removeRule(ri)}>
                Remove rule
              </button>
            )}
          </div>

          {rule.attrs.length > 0 && (
            <div style={{ marginTop: 8, paddingLeft: 12, borderLeft: "2px solid var(--border)" }}>
              <div className="matcher-form" style={{ alignItems: "center" }}>
                <span className="muted" style={{ fontSize: 13 }}>where it matches</span>
                <select
                  className="toolbar__select"
                  value={rule.combine}
                  onChange={(e) => update(ri, { combine: e.target.value as "any" | "all" })}
                  title="How the conditions below combine for this service."
                >
                  <option value="any">any (OR)</option>
                  <option value="all">all (AND)</option>
                </select>
                <span className="muted" style={{ fontSize: 13 }}>of:</span>
              </div>
              {rule.attrs.map((a, ai) => {
                // A field is "custom" when explicitly flagged, or when its key
                // isn't in the persisted catalog (e.g. a saved matcher whose
                // key isn't in the current window's sample) — so it stays
                // editable as free text instead of vanishing from the select.
                const isCustom = !!a.custom || (!!a.attribute && !attrKeys.includes(a.attribute));
                return (
                  <div key={ai}>
                    <div className="matcher-form">
                      <SearchableSelect
                        value={isCustom ? CUSTOM_FIELD : a.attribute}
                        onChange={(v) => {
                          if (v === CUSTOM_FIELD) updateAttr(ri, ai, { custom: true });
                          else updateAttr(ri, ai, { custom: false, attribute: v });
                        }}
                        options={fieldOptions}
                        labelFor={(v) => (v === CUSTOM_FIELD ? "Custom field…" : v)}
                        placeholder="Search fields…"
                        allLabel="Choose a field…"
                      />
                      {isCustom && (
                        <input
                          className="search__input matcher-form__attr"
                          list="integration-attr-keys"
                          value={a.attribute}
                          onChange={(e) => updateAttr(ri, ai, { attribute: e.target.value, custom: true })}
                          placeholder="attribute key"
                          autoComplete="off"
                          style={{ maxWidth: 180 }}
                        />
                      )}
                      <select
                        className="toolbar__select"
                        value={a.operator}
                        onChange={(e) => updateAttr(ri, ai, { operator: e.target.value as MatcherOperator })}
                        aria-label="Attribute match operator"
                      >
                        {ATTR_OPERATORS.map((o) => (
                          <option key={o.value} value={o.value}>{o.label}</option>
                        ))}
                      </select>
                      {!VALUELESS_MATCHER_OPS.includes(a.operator) && (
                        <input
                          className="search__input matcher-form__value"
                          placeholder="value"
                          value={a.value}
                          onChange={(e) => updateAttr(ri, ai, { value: e.target.value })}
                          autoComplete="off"
                        />
                      )}
                      <button type="button" className="btn btn--link" onClick={() => removeAttr(ri, ai)}>
                        Remove
                      </button>
                    </div>
                    <div className="muted" style={{ fontSize: 11, paddingLeft: 2, marginBottom: 4 }}>
                      {fieldHelp(a)}
                    </div>
                  </div>
                );
              })}
              <button type="button" className="btn" onClick={() => addAttr(ri)} style={{ marginTop: 4 }}>
                + condition
              </button>
              <label
                className="muted"
                style={{ display: "flex", gap: 6, alignItems: "flex-start", fontSize: 13, marginTop: 10 }}
              >
                <input
                  type="checkbox"
                  checked={!!rule.descendants}
                  onChange={(e) => update(ri, { descendants: e.target.checked })}
                  style={{ marginTop: 3 }}
                />
                <span>
                  Include child spans
                  <span style={{ display: "block", fontSize: 11 }}>
                    Also take every span below a matching span in its trace, even when the child spans do not
                    match the conditions themselves.
                  </span>
                </span>
              </label>
            </div>
          )}
          {rule.attrs.length === 0 && (
            <div style={{ marginTop: 8 }}>
              <button type="button" className="btn" onClick={() => addAttr(ri)}>
                + attribute condition
              </button>
            </div>
          )}
        </div>
      ))}

      <datalist id="integration-attr-keys">
        <option value={SERVICE_NAME_ATTR} />
        {attrKeys.map((k) => (
          <option key={k} value={k} />
        ))}
      </datalist>

      <div style={{ display: "flex", gap: 8 }}>
        <button type="button" className="btn" onClick={addRule}>
          + Add rule
        </button>
      </div>
    </div>
  );
}
