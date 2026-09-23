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

import { useEffect, useState } from "react";
import Pill from "./search/Pill";
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

/** What a draft rule matches right now, as the preview endpoint
 *  answers it. */
export interface RulePreview {
  incomplete?: boolean;
  service_count?: number;
  services?: string[];
  trace_count?: number;
  error_trace_count?: number;
}

export default function MatcherRules({
  rules,
  onChange,
  knownServices,
  attrKeys,
  combine = "any",
  onCombineChange,
  onPreviewRule,
}: {
  rules: Rule[];
  onChange: (rules: Rule[]) => void;
  knownServices: string[];
  attrKeys: string[];
  combine?: RuleMatch;
  onCombineChange?: (mode: RuleMatch) => void;
  /** Asks the server what one rule would match. Omitted on surfaces
   *  that cannot ask (a read-only view). */
  onPreviewRule?: (rule: Rule) => Promise<RulePreview>;
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


  return (
    <div>
      {onCombineChange && (
        <div
          className="rounded-lg border"
          style={{ borderColor: "var(--border)", padding: "10px 12px", marginBottom: 12 }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <span style={{ fontSize: 13 }}>Traffic belongs here when it matches</span>
            <Pill
              kind="op"
              label={combine === "all" ? "every rule" : "any rule"}
              accent
              ariaLabel="How the rules combine"
              editor={({ close }) => (
                <div style={{ display: "grid", gap: 2, minWidth: 260 }}>
                  {(
                    [
                      ["any", "any rule", "A step belongs if it satisfies one rule. The rules do not have to hold together."],
                      ["all", "every rule", "Every rule must be satisfied by some step of the same trace."],
                    ] as const
                  ).map(([value, label, hint]) => (
                    <button
                      key={value}
                      type="button"
                      className="btn btn--ghost"
                      style={{ display: "block", width: "100%", textAlign: "left", padding: "6px 8px" }}
                      onClick={() => {
                        onCombineChange(value as RuleMatch);
                        close();
                      }}
                    >
                      <span>
                        <span style={{ fontWeight: combine === value ? 600 : 400 }}>{label}</span>
                        <span className="muted" style={{ display: "block", fontSize: 11 }}>{hint}</span>
                      </span>
                    </button>
                  ))}
                </div>
              )}
            />
          </div>
        </div>
      )}

      {rules.map((rule, ri) => {
        const attrs = rule.attrs;
        return (
          <div
            key={ri}
            className="rounded-lg border bg-surface-2"
            style={{ borderColor: "var(--border)", padding: 12, marginBottom: 10 }}
          >
            {/* The rule as a sentence, in the same pills the Messages
                filters use. Three dropdowns in a row say the same thing
                and read as a form; this reads as a claim about traffic. */}
            <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
              <span className="muted" style={{ fontSize: 13 }}>Traffic from service</span>
              <Pill
                kind="op"
                ariaLabel="Service match operator"
              label={RULE_OPERATORS.find((o) => o.value === rule.serviceOp)?.label ?? rule.serviceOp}
                editor={({ close }) => (
                  <OperatorList
                    options={RULE_OPERATORS}
                    current={rule.serviceOp}
                    onPick={(op) => {
                      update(ri, { serviceOp: op });
                      close();
                    }}
                  />
                )}
              />
              <Pill
                kind="value"
                accent
                ariaLabel="Service"
                label={rule.service.trim() || "pick a service"}
                editor={({ close }) =>
                  rule.serviceOp === "equals" ? (
                    // Inline rather than a SearchableSelect: a popover
                    // inside a popover fights over the click that closes
                    // it, and this one is a list either way.
                    <NamePicker
                      current={rule.service}
                      options={knownServices}
                      placeholder="Search services…"
                      empty="No service here matches that."
                      onPick={(v) => {
                        update(ri, { service: v });
                        close();
                      }}
                    />
                  ) : (
                    <TextValueEditor
                      value={rule.service}
                      placeholder="e.g. order-"
                      hint="Matched against the service name."
                      onCommit={(v) => {
                        update(ri, { service: v });
                        close();
                      }}
                    />
                  )
                }
              />
              {rules.length > 1 && (
                <button
                  type="button"
                  className="btn btn--link"
                  style={{ marginLeft: "auto" }}
                  onClick={() => removeRule(ri)}
                >
                  ✕ remove rule
                </button>
              )}
            </div>

            {attrs.length > 0 && (
              <div style={{ marginTop: 10, display: "grid", gap: 6 }}>
                {attrs.map((a, ai) => {
                  const isCustom = !!a.custom || (!!a.attribute && !attrKeys.includes(a.attribute));
                  const valueless = VALUELESS_MATCHER_OPS.includes(a.operator);
                  return (
                    <div
                      key={ai}
                      style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}
                    >
                      {ai === 0 ? (
                        <span className="muted" style={{ fontSize: 13, width: 62 }}>
                          where
                        </span>
                      ) : (
                        // The joiner is the control. A separate "the
                        // conditions combine with" row said the same
                        // thing twice and put the answer away from the
                        // word that shows it.
                        <span style={{ width: 62 }}>
                          <Pill
                            kind="op"
                            ariaLabel="How the conditions combine"
                            label={rule.combine === "all" ? "and" : "or"}
                            editor={({ close }) => (
                              <div style={{ display: "grid", gap: 2, minWidth: 220 }}>
                                {(
                                  [
                                    ["any", "or", "Any one condition is enough."],
                                    ["all", "and", "Every condition has to hold."],
                                  ] as const
                                ).map(([value, label, hint]) => (
                                  <button
                                    key={value}
                                    type="button"
                                    className="btn btn--ghost"
                                    style={{ display: "block", width: "100%", textAlign: "left", padding: "6px 8px" }}
                                    onClick={() => {
                                      update(ri, { combine: value as "any" | "all" });
                                      close();
                                    }}
                                  >
                                    <span>
                                      <span style={{ fontWeight: rule.combine === value ? 600 : 400 }}>{label}</span>
                                      <span className="muted" style={{ display: "block", fontSize: 11 }}>{hint}</span>
                                    </span>
                                  </button>
                                ))}
                              </div>
                            )}
                          />
                        </span>
                      )}
                      <Pill
                        kind="field"
                        ariaLabel="Attribute"
                        label={a.attribute.trim() || "attribute"}
                        editor={({ close }) => (
                          <AttributeFieldPicker
                            current={a.attribute}
                            isCustom={isCustom}
                            options={attrKeys}
                            onPick={(key, custom) => {
                              updateAttr(ri, ai, { attribute: key, custom });
                              if (!custom) close();
                            }}
                          />
                        )}
                      />
                      <Pill
                        kind="op"
                        ariaLabel="Attribute match operator"
                        label={ATTR_OPERATORS.find((o) => o.value === a.operator)?.label ?? a.operator}
                        editor={({ close }) => (
                          <OperatorList
                            options={ATTR_OPERATORS}
                            current={a.operator}
                            onPick={(op) => {
                              updateAttr(ri, ai, { operator: op });
                              close();
                            }}
                          />
                        )}
                      />
                      {!valueless && (
                        <Pill
                          kind="value"
                          accent
                          ariaLabel="Attribute value"
                          label={a.value.trim() || "value"}
                          editor={({ close }) => (
                            <TextValueEditor
                              value={a.value}
                              placeholder="value"
                              hint={fieldHelp(a)}
                              onCommit={(v) => {
                                updateAttr(ri, ai, { value: v });
                                close();
                              }}
                            />
                          )}
                        />
                      )}
                      <button
                        type="button"
                        className="btn btn--link"
                        style={{ marginLeft: "auto" }}
                        onClick={() => removeAttr(ri, ai)}
                      >
                        ✕
                      </button>
                    </div>
                  );
                })}
              </div>
            )}

            <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 10, flexWrap: "wrap" }}>
              <button type="button" className="btn btn--sm" onClick={() => addAttr(ri)}>
                + condition
              </button>
              {attrs.length > 0 && (
                <button
                  type="button"
                  onClick={() => update(ri, { descendants: !rule.descendants })}
                  aria-pressed={!!rule.descendants}
                  title="Also take every span below a matching span in its trace, even when the child spans do not carry the attribute."
                  className="rounded-full border px-2 py-0.5 text-xs"
                  style={{
                    borderColor: rule.descendants
                      ? "color-mix(in oklab, var(--primary) 35%, transparent)"
                      : "var(--border)",
                    background: rule.descendants ? "var(--primary-soft)" : "transparent",
                    color: rule.descendants ? "var(--primary-ink)" : "var(--muted)",
                  }}
                >
                  {rule.descendants ? "✓ with child spans" : "+ child spans"}
                </button>
              )}
              {onPreviewRule && <RuleMatchLine rule={rule} preview={onPreviewRule} />}
            </div>
          </div>
        );
      })}

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

// OperatorList is the popover body behind an operator pill: the choices,
// as a list you read, rather than a select you open and scan.
function OperatorList({
  options,
  current,
  onPick,
}: {
  options: { value: MatcherOperator; label: string }[];
  current: MatcherOperator;
  onPick: (op: MatcherOperator) => void;
}) {
  return (
    <div style={{ display: "grid", gap: 2, minWidth: 200 }}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          className="btn btn--ghost"
          style={{
            display: "block",
            width: "100%",
            textAlign: "left",
            padding: "5px 8px",
            fontWeight: o.value === current ? 600 : 400,
          }}
          onClick={() => onPick(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

// TextValueEditor is the popover body behind a value pill. Enter commits,
// because a value pill is one field and reaching for a button to confirm
// one field is a step nobody wants.
function TextValueEditor({
  value,
  placeholder,
  hint,
  onCommit,
}: {
  value: string;
  placeholder: string;
  hint?: string;
  onCommit: (v: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  return (
    <div style={{ minWidth: 260 }}>
      <input
        className="search__input"
        autoFocus
        value={draft}
        placeholder={placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            onCommit(draft.trim());
          }
        }}
        style={{ width: "100%" }}
      />
      {hint && (
        <div className="muted" style={{ fontSize: 11, marginTop: 6, lineHeight: 1.45 }}>
          {hint}
        </div>
      )}
      <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
        <button type="button" className="btn btn--sm btn--primary" onClick={() => onCommit(draft.trim())}>
          Apply
        </button>
      </div>
    </div>
  );
}

// NamePicker is the popover body behind a pill whose value comes from a
// list the cell has seen: a search box and the matches, no dropdown of
// its own. A popover inside a popover fights over the click that closes
// it, which is why this is not a SearchableSelect.
function NamePicker({
  current,
  options,
  placeholder,
  empty,
  onPick,
  footer,
}: {
  current: string;
  options: string[];
  placeholder: string;
  empty: string;
  onPick: (value: string) => void;
  footer?: React.ReactNode;
}) {
  const [q, setQ] = useState("");
  const shown = options.filter((k) => k.toLowerCase().includes(q.trim().toLowerCase())).slice(0, 40);
  return (
    <div style={{ minWidth: 280 }}>
      <input
        className="search__input"
        autoFocus
        role="searchbox"
        placeholder={placeholder}
        value={q}
        onChange={(e) => setQ(e.target.value)}
        style={{ width: "100%", marginBottom: 6 }}
      />
      <div style={{ maxHeight: 220, overflowY: "auto", display: "grid", gap: 2 }}>
        {shown.map((k) => (
          <button
            key={k}
            type="button"
            className="btn btn--ghost mono"
            style={{
              display: "block",
              width: "100%",
              textAlign: "left",
              padding: "5px 8px",
              fontSize: 12.5,
              fontWeight: k === current ? 600 : 400,
            }}
            onClick={() => onPick(k)}
          >
            {k}
          </button>
        ))}
        {shown.length === 0 && (
          <div className="muted" style={{ fontSize: 12, padding: "6px 8px" }}>
            {empty}
          </div>
        )}
      </div>
      {footer}
    </div>
  );
}

// AttributeFieldPicker is NamePicker plus the escape hatch: an attribute
// that exists in the code but has not been emitted yet is a real case,
// and a picker that only offers what it has seen cannot express it.
function AttributeFieldPicker({
  current,
  isCustom,
  options,
  onPick,
}: {
  current: string;
  isCustom: boolean;
  options: string[];
  onPick: (key: string, custom: boolean) => void;
}) {
  return (
    <NamePicker
      current={current}
      options={options}
      placeholder="Search attributes…"
      empty="No attribute here matches that."
      onPick={(k) => onPick(k, false)}
      footer={
        <div style={{ borderTop: "1px solid var(--border)", marginTop: 8, paddingTop: 8 }}>
          <input
            className="search__input mono"
            placeholder="or type a key this cell has not seen yet"
            value={isCustom ? current : ""}
            onChange={(e) => onPick(e.target.value, true)}
            style={{ width: "100%", fontSize: 12.5 }}
          />
        </div>
      }
    />
  );
}

// RuleMatchLine says what this rule matches right now.
//
// The editor could always be saved and then read back, and that is what
// it used to take: save, open the integration, find it empty, come back
// and work out which of four conditions was wrong. A misspelled
// attribute is a five-second mistake that used to cost a round trip.
function RuleMatchLine({
  rule,
  preview,
}: {
  rule: Rule;
  preview: (rule: Rule) => Promise<RulePreview>;
}) {
  const [state, setState] = useState<RulePreview | null>(null);
  const [busy, setBusy] = useState(false);
  // Keyed on the rule's content, so a redraw that changes nothing does
  // not ask again, and a change that matters always does.
  const key = JSON.stringify([
    rule.serviceOp,
    rule.service,
    rule.combine,
    rule.descendants,
    rule.attrs.map((a) => [a.attribute, a.operator, a.value]),
  ]);
  useEffect(() => {
    let cancelled = false;
    if (!rule.service.trim()) {
      setState(null);
      return;
    }
    // Typing is not a question. The pause is what asks.
    const t = window.setTimeout(() => {
      setBusy(true);
      preview(rule)
        .then((r) => !cancelled && setState(r))
        .catch(() => !cancelled && setState(null))
        .finally(() => !cancelled && setBusy(false));
    }, 450);
    return () => {
      cancelled = true;
      window.clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  if (!rule.service.trim()) {
    return <span className="muted" style={{ fontSize: 12 }}>Pick a service to see what this matches.</span>;
  }
  if (busy && !state) {
    return <span className="muted" style={{ fontSize: 12 }}>Checking…</span>;
  }
  if (!state) return null;
  if (state.incomplete) {
    return <span className="muted" style={{ fontSize: 12 }}>Finish the condition to see what it matches.</span>;
  }
  const services = state.service_count ?? 0;
  if (services === 0) {
    return (
      <span style={{ fontSize: 12, color: "var(--warn-ink, var(--ink-2))" }}>
        No service matches this rule in the selected range.
      </span>
    );
  }
  const traces = state.trace_count;
  return (
    <span className="muted" style={{ fontSize: 12 }}>
      Matches {services} {services === 1 ? "service" : "services"}
      {typeof traces === "number" ? `, ${traces.toLocaleString()} ${traces === 1 ? "message" : "messages"} in range` : ""}
      {typeof traces === "number" && traces === 0 ? " — nothing came through yet" : ""}
    </span>
  );
}
