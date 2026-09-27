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

import { useState } from "react";
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

/**
 * RuleEditor edits ONE member: how its service is matched, and the
 * attribute conditions that narrow it. The membership editor opens it
 * inside a member's row; removing the member, and how members combine,
 * live on the row and in the editor around it.
 *
 * Every control keeps its own accessible name. A pill's visible text is
 * its VALUE and changes as somebody edits it, so the name is separate.
 */
export function RuleEditor({
  rule,
  onChange,
  knownServices,
  attrKeys,
  locked = false,
  orUnavailable,
}: {
  rule: Rule;
  onChange: (rule: Rule) => void;
  knownServices: string[];
  attrKeys: string[];
  /** Shown as it is, with nothing to open. */
  locked?: boolean;
  /** Set when the conditions cannot be joined with "or" here, to the
   *  reason. A new condition is then joined with "and". */
  orUnavailable?: string;
}) {
  const update = (patch: Partial<Rule>) => onChange({ ...rule, ...patch });
  const updateAttr = (ai: number, patch: Partial<AttrCond>) =>
    update({ attrs: rule.attrs.map((a, idx) => (idx === ai ? { ...a, ...patch } : a)) });
  const addAttr = () =>
    update({ attrs: [...rule.attrs, blankAttr()], ...(orUnavailable ? { combine: "all" as const } : {}) });
  const removeAttr = (ai: number) => update({ attrs: rule.attrs.filter((_, idx) => idx !== ai) });
  const attrs = rule.attrs;

  return (
    <div>
      {/* The rule as a sentence, in the same pills the Messages filters
          use. Three dropdowns in a row say the same thing and read as a
          form; this reads as a claim about traffic. */}
      <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
        <span className="muted" style={{ fontSize: 13 }}>Traffic from service</span>
        <Pill
          kind="op"
          locked={locked}
          ariaLabel="Service match operator"
          label={RULE_OPERATORS.find((o) => o.value === rule.serviceOp)?.label ?? rule.serviceOp}
          editor={({ close }) => (
            <OperatorList
              options={RULE_OPERATORS}
              current={rule.serviceOp}
              onPick={(op) => {
                update({ serviceOp: op });
                close();
              }}
            />
          )}
        />
        <Pill
          kind="value"
          accent
          locked={locked}
          ariaLabel="Service"
          label={rule.service.trim() || "pick a service"}
          editor={({ close }) =>
            rule.serviceOp === "equals" ? (
              // Inline rather than a SearchableSelect: a popover inside
              // a popover fights over the click that closes it, and this
              // one is a list either way.
              <NamePicker
                current={rule.service}
                options={knownServices}
                placeholder="Search services…"
                empty="No service here matches that."
                onPick={(v) => {
                  update({ service: v });
                  close();
                }}
                footer={
                  // The list holds what this cell has SEEN in the
                  // window. A nightly job that ran at three in the
                  // morning is not in it, and a rule you cannot write
                  // for a quiet service is a rule you cannot write for
                  // the ones that matter most.
                  <div style={{ borderTop: "1px solid var(--border)", marginTop: 8, paddingTop: 8 }}>
                    <input
                      className="search__input mono"
                      aria-label="Service name"
                      placeholder="or type a service that has been quiet"
                      defaultValue={knownServices.includes(rule.service) ? "" : rule.service}
                      onKeyDown={(e) => {
                        if (e.key !== "Enter") return;
                        e.preventDefault();
                        const v = (e.target as HTMLInputElement).value.trim();
                        if (!v) return;
                        update({ service: v });
                        close();
                      }}
                      onBlur={(e) => {
                        const v = e.target.value.trim();
                        if (v) update({ service: v });
                      }}
                      style={{ width: "100%", fontSize: 12.5 }}
                    />
                  </div>
                }
              />
            ) : (
              <TextValueEditor
                value={rule.service}
                placeholder="order-"
                hint="Matched against the service name."
                onCommit={(v) => {
                  update({ service: v });
                  close();
                }}
              />
            )
          }
        />
      </div>

      {attrs.length > 0 && (
        <div style={{ marginTop: 10, display: "grid", gap: 6 }}>
          {attrs.map((a, ai) => {
            const isCustom = !!a.custom || (!!a.attribute && !attrKeys.includes(a.attribute));
            const valueless = VALUELESS_MATCHER_OPS.includes(a.operator);
            return (
              <div key={ai} style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
                {ai === 0 ? (
                  <span className="muted" style={{ fontSize: 13, width: 62 }}>
                    where
                  </span>
                ) : (
                  // The joiner is the control. A separate "the conditions
                  // combine with" row said the same thing twice and put
                  // the answer away from the word that shows it.
                  <span style={{ width: 62 }}>
                    <Pill
                      kind="op"
                      locked={locked}
                      ariaLabel="How the conditions combine"
                      label={rule.combine === "all" ? "and" : "or"}
                      editor={({ close }) => (
                        <div style={{ display: "grid", gap: 2, minWidth: 220 }}>
                          {(
                            [
                              ["any", "or", "Any one condition is enough."],
                              ["all", "and", "Every condition has to hold."],
                            ] as const
                          ).map(([value, label, hint]) => {
                            // Offered but not choosable, with the reason
                            // where the choice would have been: a missing
                            // option reads as a missing feature.
                            const off = value === "any" && !!orUnavailable;
                            return (
                              <button
                                key={value}
                                type="button"
                                className="btn btn--ghost"
                                disabled={off}
                                style={{ display: "block", width: "100%", textAlign: "left", padding: "6px 8px" }}
                                onClick={() => {
                                  update({ combine: value as "any" | "all" });
                                  close();
                                }}
                              >
                                <span>
                                  <span style={{ fontWeight: rule.combine === value ? 600 : 400 }}>{label}</span>
                                  <span className="muted" style={{ display: "block", fontSize: 11 }}>
                                    {off ? orUnavailable : hint}
                                  </span>
                                </span>
                              </button>
                            );
                          })}
                        </div>
                      )}
                    />
                  </span>
                )}
                <Pill
                  kind="field"
                  locked={locked}
                  ariaLabel="Attribute"
                  label={a.attribute.trim() || "attribute"}
                  editor={({ close }) => (
                    <AttributeFieldPicker
                      current={a.attribute}
                      isCustom={isCustom}
                      options={attrKeys}
                      onPick={(key, custom) => {
                        updateAttr(ai, { attribute: key, custom });
                        if (!custom) close();
                      }}
                    />
                  )}
                />
                <Pill
                  kind="op"
                  locked={locked}
                  ariaLabel="Attribute match operator"
                  label={ATTR_OPERATORS.find((o) => o.value === a.operator)?.label ?? a.operator}
                  editor={({ close }) => (
                    <OperatorList
                      options={ATTR_OPERATORS}
                      current={a.operator}
                      onPick={(op) => {
                        updateAttr(ai, { operator: op });
                        close();
                      }}
                    />
                  )}
                />
                {!valueless && (
                  <Pill
                    kind="value"
                    accent
                    locked={locked}
                    ariaLabel="Attribute value"
                    label={a.value.trim() || "value"}
                    editor={({ close }) => (
                      <TextValueEditor
                        value={a.value}
                        placeholder="value"
                        hint={fieldHelp(a)}
                        onCommit={(v) => {
                          updateAttr(ai, { value: v });
                          close();
                        }}
                      />
                    )}
                  />
                )}
                {!locked && (
                  <button
                    type="button"
                    className="btn btn--link"
                    aria-label="Remove condition"
                    style={{ marginLeft: "auto" }}
                    onClick={() => removeAttr(ai)}
                  >
                    ✕
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}

      {!locked && (
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 10, flexWrap: "wrap" }}>
          <button type="button" className="btn btn--sm" onClick={addAttr}>
            + condition
          </button>
          {attrs.length > 0 && (
            <button
              type="button"
              onClick={() => update({ descendants: !rule.descendants })}
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
        </div>
      )}
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
export function NamePicker({
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
